package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

type Config struct {
	Upstream string
	Store    *accounts.Store
	Logger   *slog.Logger
}

func New(config Config) (*http.Server, error) {
	upstream, err := url.Parse(config.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parse upstream: %w", err)
	}
	handler := &handler{
		upstream: upstream,
		store:    config.Store,
		client:   http.DefaultClient,
		logger:   config.Logger,
	}
	if handler.logger == nil {
		handler.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &http.Server{Handler: handler}, nil
}

type handler struct {
	upstream *url.URL
	store    *accounts.Store
	client   *http.Client
	logger   *slog.Logger
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
			if h.updateUsageLimits(r, resp, account) {
				resp.Body.Close()
				account, ok = h.store.Current(time.Now())
				if !ok {
					h.logger.Warn("no_eligible_account_after_usage_update", "method", r.Method, "path", r.URL.Path)
					http.Error(w, "codextra has no eligible account", http.StatusServiceUnavailable)
					return
				}
				resp, err = h.forward(r.Context(), r, body, account)
				if err != nil {
					h.logger.Warn("upstream_request_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", err)
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
			}
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

func (h *handler) updateUsageLimits(r *http.Request, resp *http.Response, account accounts.Account) bool {
	if r.URL.Path != "/backend-api/wham/usage" || resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.logger.Warn("usage_body_read_failed", "path", r.URL.Path, "alias", account.Alias, "error", err)
		return false
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	limit, resetAt, exhausted := exhaustedUsageLimit(body)
	if !exhausted {
		return false
	}
	next, rotated, err := h.store.RotateFrom(account.Alias, limit, resetAt, time.Now())
	if err != nil {
		h.logger.Warn("usage_rotation_failed", "path", r.URL.Path, "alias", account.Alias, "limit", limit, "error", err)
		return false
	}
	if !rotated {
		h.logger.Warn("usage_rotation_exhausted", "path", r.URL.Path, "alias", account.Alias, "limit", limit, "reset_at", resetAt.Format(time.RFC3339))
		return false
	}
	h.logger.Info("usage_rotation_detected", "path", r.URL.Path, "from", account.Alias, "to", next.Alias, "limit", limit, "reset_at", resetAt.Format(time.RFC3339))
	return true
}

func (h *handler) logResponse(r *http.Request, resp *http.Response, account accounts.Account, elapsed time.Duration, body []byte, copyErr error) {
	args := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"query", r.URL.RawQuery,
		"alias", account.Alias,
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
	target := *h.upstream
	target.Path = singleJoiningSlash(h.upstream.Path, original.URL.Path)
	target.RawQuery = original.URL.RawQuery

	req, err := http.NewRequestWithContext(ctx, original.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = original.Header.Clone()
	req.Host = h.upstream.Host
	req.Header.Set("Authorization", "Bearer "+account.AccessToken)
	if account.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", account.AccountID)
	}
	return h.client.Do(req)
}

func (h *handler) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	// Standard-library-only WebSocket proxying is intentionally deferred. HTTP
	// Upgrade tunneling needs careful hijacking and frame piping. For now, fail
	// clearly instead of pretending this path works.
	http.Error(w, "codextra websocket proxy is not implemented yet", http.StatusNotImplemented)
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

func exhaustedUsageLimit(body []byte) (string, time.Time, bool) {
	var payload struct {
		RateLimit struct {
			PrimaryWindow   usageWindow `json:"primary_window"`
			SecondaryWindow usageWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "", time.Time{}, false
	}
	if payload.RateLimit.SecondaryWindow.UsedPercent >= 100 {
		return "codex_weekly", time.Unix(payload.RateLimit.SecondaryWindow.ResetAt, 0), true
	}
	if payload.RateLimit.PrimaryWindow.UsedPercent >= 100 {
		return "codex_5h", time.Unix(payload.RateLimit.PrimaryWindow.ResetAt, 0), true
	}
	return "", time.Time{}, false
}

type usageWindow struct {
	UsedPercent int   `json:"used_percent"`
	ResetAt     int64 `json:"reset_at"`
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
