package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

type Config struct {
	Upstream    string
	APIUpstream string
	Store       *accounts.Store
	Logger      *slog.Logger
}

func New(config Config) (*http.Server, error) {
	upstream, err := url.Parse(config.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parse upstream: %w", err)
	}
	apiUpstreamValue := config.APIUpstream
	if apiUpstreamValue == "" {
		apiUpstreamValue = config.Upstream
	}
	apiUpstream, err := url.Parse(apiUpstreamValue)
	if err != nil {
		return nil, fmt.Errorf("parse api upstream: %w", err)
	}
	handler := &handler{
		upstream:    upstream,
		apiUpstream: apiUpstream,
		store:       config.Store,
		client:      http.DefaultClient,
		logger:      config.Logger,
	}
	if handler.logger == nil {
		handler.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &http.Server{Handler: handler}, nil
}

type handler struct {
	upstream    *url.URL
	apiUpstream *url.URL
	store       *accounts.Store
	client      *http.Client
	logger      *slog.Logger
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__codextra/health" {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	if isWebSocket(r) {
		h.serveWebSocket(w, r)
		return
	}
	h.serveHTTP(w, r)
}

func (h *handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn("request_body_read_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	now := time.Now()
	account, ok := h.store.Current(now)
	if !ok {
		h.logger.Warn("no_eligible_account", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "codextra has no eligible account", http.StatusServiceUnavailable)
		return
	}

	for {
		resp, err := h.forward(r.Context(), r, body, account)
		if err != nil {
			h.logger.Warn("upstream_request_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		if resp.StatusCode != http.StatusTooManyRequests || !isUsageLimit(resp) {
			captured, err := copyResponse(w, resp, responseCaptureLimit(resp))
			h.logResponse(r, resp, account, time.Since(start), captured, err)
			return
		}

		limit, resetAt := limitInfo(resp)
		resp.Body.Close()
		h.logger.Info("usage_limit_detected", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit, "reset_at", resetAt.Format(time.RFC3339))
		next, rotated, err := h.store.RotateFrom(account.Alias, limit, resetAt, time.Now())
		if err != nil {
			h.logger.Warn("account_rotation_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !rotated {
			h.logger.Warn("account_rotation_exhausted", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit)
			http.Error(w, "all codextra accounts are usage limited", http.StatusTooManyRequests)
			return
		}
		h.logger.Info("account_rotated", "method", r.Method, "path", r.URL.Path, "from", account.Alias, "to", next.Alias, "limit", limit)
		account = next
	}
}

func (h *handler) logResponse(r *http.Request, resp *http.Response, account accounts.Account, elapsed time.Duration, body []byte, copyErr error) {
	args := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"query", r.URL.RawQuery,
		"alias", account.Alias,
		"upstream_host", h.upstreamFor(r.URL.Path).Host,
		"status", resp.StatusCode,
		"content_type", resp.Header.Get("content-type"),
		"duration_ms", elapsed.Milliseconds(),
	}
	if len(body) > 0 {
		args = append(args,
			"usage_marker", usageLimitMarker(body),
			"body_prefix", compactPrefix(body, 512),
		)
	}
	if copyErr != nil {
		args = append(args, "copy_error", copyErr)
	}
	h.logger.Info("upstream_response", args...)
}

func (h *handler) forward(ctx context.Context, original *http.Request, body []byte, account accounts.Account) (*http.Response, error) {
	upstream := h.upstreamFor(original.URL.Path)
	target := *upstream
	target.Path = singleJoiningSlash(upstream.Path, original.URL.Path)
	target.RawQuery = original.URL.RawQuery

	req, err := http.NewRequestWithContext(ctx, original.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = original.Header.Clone()
	req.Host = upstream.Host
	applyAuthHeaders(req.Header, account)
	return h.client.Do(req)
}

func applyAuthHeaders(header http.Header, account accounts.Account) {
	header.Set("Authorization", "Bearer "+account.AccessToken)
	if account.AccountID != "" {
		header.Set("ChatGPT-Account-ID", account.AccountID)
	} else {
		header.Del("ChatGPT-Account-ID")
	}
}

func (h *handler) upstreamFor(path string) *url.URL {
	if path == "/v1" || strings.HasPrefix(path, "/v1/") {
		return h.apiUpstream
	}
	return h.upstream
}

func (h *handler) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	account, ok := h.store.Current(time.Now())
	if !ok {
		h.logger.Warn("no_eligible_account", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "codextra has no eligible account", http.StatusServiceUnavailable)
		return
	}

	for {
		upstreamConn, upstreamReader, upstreamReq, resp, err := h.openWebSocket(r.Context(), r, account)
		if err != nil {
			h.logger.Warn("websocket_upstream_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if resp.StatusCode == http.StatusSwitchingProtocols {
			h.logger.Info("websocket_upgraded", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "upstream_host", upstreamReq.Host, "duration_ms", time.Since(start).Milliseconds())
			h.tunnelWebSocket(w, r, upstreamConn, upstreamReader, resp)
			return
		}

		if resp.StatusCode != http.StatusTooManyRequests || !isUsageLimit(resp) {
			captured, err := copyResponse(w, resp, responseCaptureLimit(resp))
			h.logResponse(r, resp, account, time.Since(start), captured, err)
			upstreamConn.Close()
			return
		}

		limit, resetAt := limitInfo(resp)
		resp.Body.Close()
		upstreamConn.Close()
		h.logger.Info("usage_limit_detected", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit, "reset_at", resetAt.Format(time.RFC3339))
		next, rotated, err := h.store.RotateFrom(account.Alias, limit, resetAt, time.Now())
		if err != nil {
			h.logger.Warn("account_rotation_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !rotated {
			h.logger.Warn("account_rotation_exhausted", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit)
			http.Error(w, "all codextra accounts are usage limited", http.StatusTooManyRequests)
			return
		}
		h.logger.Info("account_rotated", "method", r.Method, "path", r.URL.Path, "from", account.Alias, "to", next.Alias, "limit", limit)
		account = next
	}
}

func (h *handler) openWebSocket(ctx context.Context, original *http.Request, account accounts.Account) (net.Conn, *bufio.Reader, *http.Request, *http.Response, error) {
	upstream := h.upstreamFor(original.URL.Path)
	target := *upstream
	target.Path = singleJoiningSlash(upstream.Path, original.URL.Path)
	target.RawQuery = original.URL.RawQuery

	req := original.Clone(ctx)
	req.URL = &target
	req.RequestURI = ""
	req.Host = upstream.Host
	req.Header = original.Header.Clone()
	applyAuthHeaders(req.Header, account)

	conn, err := dialURL(ctx, upstream)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, nil, nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		conn.Close()
		return nil, nil, nil, nil, err
	}
	return conn, reader, req, resp, nil
}

func dialURL(ctx context.Context, target *url.URL) (net.Conn, error) {
	addr := target.Host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		switch target.Scheme {
		case "https", "wss":
			addr = net.JoinHostPort(target.Host, "443")
		default:
			addr = net.JoinHostPort(target.Host, "80")
		}
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "https" && target.Scheme != "wss" {
		return conn, nil
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: target.Hostname(), MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (h *handler) tunnelWebSocket(w http.ResponseWriter, r *http.Request, upstreamConn net.Conn, upstreamReader *bufio.Reader, resp *http.Response) {
	defer upstreamConn.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		resp.Body.Close()
		http.Error(w, "response writer cannot hijack connection", http.StatusInternalServerError)
		return
	}
	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		resp.Body.Close()
		h.logger.Warn("websocket_hijack_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return
	}
	defer clientConn.Close()

	if err := resp.Write(clientRW); err != nil {
		h.logger.Warn("websocket_response_write_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return
	}
	if err := clientRW.Flush(); err != nil {
		h.logger.Warn("websocket_response_flush_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return
	}
	resp.Body.Close()

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = clientConn.Close()
			_ = upstreamConn.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer closeBoth()
		if upstreamReader.Buffered() > 0 {
			_, _ = io.CopyN(clientConn, upstreamReader, int64(upstreamReader.Buffered()))
		}
		_, _ = io.Copy(clientConn, upstreamConn)
	}()
	go func() {
		defer wg.Done()
		defer closeBoth()
		if clientRW.Reader.Buffered() > 0 {
			_, _ = io.CopyN(upstreamConn, clientRW.Reader, int64(clientRW.Reader.Buffered()))
		}
		_, _ = io.Copy(upstreamConn, clientConn)
	}()
	wg.Wait()
}

func copyResponse(w http.ResponseWriter, resp *http.Response, captureLimit int) ([]byte, error) {
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	capture := &prefixCapture{limit: captureLimit}
	_, err := io.Copy(io.MultiWriter(w, capture), resp.Body)
	return capture.bytes, err
}

type prefixCapture struct {
	limit int
	bytes []byte
}

func (c *prefixCapture) Write(p []byte) (int, error) {
	if c.limit > 0 && len(c.bytes) < c.limit {
		remaining := c.limit - len(c.bytes)
		if len(p) < remaining {
			remaining = len(p)
		}
		c.bytes = append(c.bytes, p[:remaining]...)
	}
	return len(p), nil
}

func copyHeader(dst, src http.Header) {
	for k, values := range src {
		for _, value := range values {
			dst.Add(k, value)
		}
	}
}

func isUsageLimit(resp *http.Response) bool {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return usageLimitMarker(body)
}

func limitInfo(resp *http.Response) (string, time.Time) {
	limit := resp.Header.Get("x-codex-active-limit")
	if limit == "" {
		limit = "codex"
	}
	resetAt := time.Time{}
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		var payload struct {
			Error struct {
				ResetsAt int64 `json:"resets_at"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &payload) == nil && payload.Error.ResetsAt > 0 {
			resetAt = time.Unix(payload.Error.ResetsAt, 0)
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}
	return limit, resetAt
}

func usageLimitMarker(body []byte) bool {
	return bytes.Contains(body, []byte(`"usage_limit_reached"`)) ||
		bytes.Contains(body, []byte("usage limit")) ||
		bytes.Contains(body, []byte("Usage limit"))
}

func responseCaptureLimit(resp *http.Response) int {
	contentType := resp.Header.Get("content-type")
	if resp.StatusCode >= http.StatusBadRequest || strings.Contains(contentType, "text/event-stream") {
		return 256 * 1024
	}
	return 0
}

func compactPrefix(body []byte, max int) string {
	if len(body) > max {
		body = body[:max]
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > max {
		return text[:max]
	}
	return text
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}
