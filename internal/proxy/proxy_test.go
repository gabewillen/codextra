package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

func TestProxyForwardsWithActiveAccountAuth(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotAccount string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		w.Header().Set("x-upstream", "ok")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("accepted"))
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:       "personal",
			AccessToken: "token-personal",
			AccountID:   "acct-personal",
		}},
	})

	req := httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses?foo=bar", strings.NewReader("body"))
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusAccepted, resp.Body.String())
	}
	if resp.Header().Get("x-upstream") != "ok" {
		t.Fatalf("x-upstream = %q, want ok", resp.Header().Get("x-upstream"))
	}
	if resp.Body.String() != "accepted" {
		t.Fatalf("body = %q, want accepted", resp.Body.String())
	}
	if gotAuth != "Bearer token-personal" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotAccount != "acct-personal" {
		t.Fatalf("ChatGPT-Account-ID = %q, want acct-personal", gotAccount)
	}
}

func TestProxyRotatesOnUsageLimitBeforeReturningResponse(t *testing.T) {
	t.Parallel()

	var tokens []string
	resetAt := time.Unix(1_700_000_123, 0)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
			w.Header().Set("x-codex-active-limit", "codex_weekly")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"type":      "usage_limit_reached",
					"resets_at": resetAt.Unix(),
				},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "rotated")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{
			{Alias: "personal", AccessToken: "token-personal"},
			{Alias: "work", AccessToken: "token-work"},
		},
	})
	server := newProxyWithStore(t, upstream.URL, store)

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", resp.Code, resp.Body.String())
	}
	if resp.Body.String() != "rotated" {
		t.Fatalf("body = %q, want rotated", resp.Body.String())
	}
	wantTokens := []string{"Bearer token-personal", "Bearer token-work"}
	if len(tokens) != len(wantTokens) {
		t.Fatalf("tokens = %#v, want %#v", tokens, wantTokens)
	}
	for i := range wantTokens {
		if tokens[i] != wantTokens[i] {
			t.Fatalf("tokens[%d] = %q, want %q", i, tokens[i], wantTokens[i])
		}
	}
	if store.Data.ActiveAlias != "work" {
		t.Fatalf("ActiveAlias = %q, want work", store.Data.ActiveAlias)
	}
	if got := store.Data.Accounts[0].DisabledUntil["codex_weekly"]; got != resetAt.Unix() {
		t.Fatalf("personal disabled reset = %d, want %d", got, resetAt.Unix())
	}
}

func TestProxyReturnsServiceUnavailableWithoutEligibleAccount(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		Accounts: []accounts.Account{{Alias: "empty"}},
	})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/responses", nil))

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}
}

func TestProxyReturnsBadGatewayWhenUpstreamUnavailable(t *testing.T) {
	t.Parallel()

	server := newTestProxy(t, "http://127.0.0.1:1", accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/responses", nil))

	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadGateway)
	}
}

func TestProxyReturnsTooManyRequestsWhenAllAccountsLimited(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"usage_limit_reached"}}`))
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/responses", nil))

	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusTooManyRequests)
	}
}

func TestProxyRejectsWebSocketClearly(t *testing.T) {
	t.Parallel()

	server := newTestProxy(t, "http://example.test", accounts.Data{
		Accounts: []accounts.Account{{Alias: "personal", AccessToken: "token"}},
	})
	req := httptest.NewRequest(http.MethodGet, "/realtime", nil)
	req.Header.Set("Upgrade", "websocket")
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNotImplemented)
	}
}

func TestNewRejectsInvalidUpstream(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Upstream: "://bad-url", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(invalid upstream) error = nil, want error")
	}
}

func TestProxyPreservesNonUsageLimitTooManyRequests(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_reached"}}`))
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/responses", nil))

	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusTooManyRequests)
	}
	if !strings.Contains(resp.Body.String(), "rate_limit_reached") {
		t.Fatalf("body = %q, want original upstream body", resp.Body.String())
	}
}

func TestProxyDefaultsLimitIDWhenHeaderMissing(t *testing.T) {
	t.Parallel()

	resetAt := time.Unix(1_700_000_123, 0)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type":      "usage_limit_reached",
				"resets_at": resetAt.Unix(),
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	server := newProxyWithStore(t, upstream.URL, store)
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/responses", nil))

	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusTooManyRequests)
	}
	if got := store.Data.Accounts[0].DisabledUntil["codex"]; got != resetAt.Unix() {
		t.Fatalf("DisabledUntil[codex] = %d, want %d", got, resetAt.Unix())
	}
}

func TestProxyJoinsUpstreamAndRequestPaths(t *testing.T) {
	t.Parallel()

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL+"/backend-api/", accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/codex/responses", nil))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if gotPath != "/backend-api/codex/responses" {
		t.Fatalf("path = %q, want /backend-api/codex/responses", gotPath)
	}
}

func TestProxyForwardsWithoutAccountIDHeaderWhenMissing(t *testing.T) {
	t.Parallel()

	gotAccount := "unset"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/responses", nil))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if gotAccount != "" {
		t.Fatalf("ChatGPT-Account-ID = %q, want empty", gotAccount)
	}
}

func newTestProxy(t *testing.T, upstream string, data accounts.Data) *http.Server {
	t.Helper()
	return newProxyWithStore(t, upstream, newTestStore(t, data))
}

func newProxyWithStore(t *testing.T, upstream string, store *accounts.Store) *http.Server {
	t.Helper()
	server, err := New(Config{Upstream: upstream, Store: store})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server
}

func newTestStore(t *testing.T, data accounts.Data) *accounts.Store {
	t.Helper()
	store, err := accounts.LoadStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	store.Data = data
	return store
}
