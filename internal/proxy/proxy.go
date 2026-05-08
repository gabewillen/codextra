package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

type Config struct {
	Upstream string
	Store    *accounts.Store
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
	}
	return &http.Server{Handler: handler}, nil
}

type handler struct {
	upstream *url.URL
	store    *accounts.Store
	client   *http.Client
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isWebSocket(r) {
		h.serveWebSocket(w, r)
		return
	}
	h.serveHTTP(w, r)
}

func (h *handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	now := time.Now()
	account, ok := h.store.Current(now)
	if !ok {
		http.Error(w, "codextra has no eligible account", http.StatusServiceUnavailable)
		return
	}

	for {
		resp, err := h.forward(r.Context(), r, body, account)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		if resp.StatusCode != http.StatusTooManyRequests || !isUsageLimit(resp) {
			copyResponse(w, resp)
			return
		}

		limit, resetAt := limitInfo(resp)
		resp.Body.Close()
		next, rotated, err := h.store.RotateFrom(account.Alias, limit, resetAt, time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !rotated {
			http.Error(w, "all codextra accounts are usage limited", http.StatusTooManyRequests)
			return
		}
		account = next
	}
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

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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
	return bytes.Contains(body, []byte(`"usage_limit_reached"`))
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
