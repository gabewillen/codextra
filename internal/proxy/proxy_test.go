package proxy

import (
	"encoding/json"
	"errors"
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
	server := newProxyWithConfig(t, Config{
		Upstream: upstream.URL,
		Store:    store,
	})

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

func TestProxyHealthEndpoint(t *testing.T) {
	t.Parallel()

	server := newTestProxy(t, "http://example.test", accounts.Data{})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/__codextra/health", nil))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if strings.TrimSpace(resp.Body.String()) != `{"ok":true}` {
		t.Fatalf("body = %q, want health JSON", resp.Body.String())
	}
}

func TestNewRejectsInvalidUpstream(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Upstream: "://bad-url", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(invalid upstream) error = nil, want error")
	}
}

func TestNewRejectsInvalidAPIUpstream(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Upstream: "http://example.test", APIUpstream: "://bad-url", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(invalid API upstream) error = nil, want error")
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

func TestProxyDoesNotRotateOnExhaustedUsageStatusResponse(t *testing.T) {
	t.Parallel()

	resetAt := time.Unix(1_778_632_231, 0)
	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"email": "personal@example.com",
			"rate_limit": map[string]any{
				"primary_window": map[string]any{
					"used_percent": 14,
					"reset_at":     resetAt.Add(-time.Hour).Unix(),
				},
				"secondary_window": map[string]any{
					"used_percent": 100,
					"reset_at":     resetAt.Unix(),
				},
			},
		})
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{
			{Alias: "personal", AccessToken: "token-personal"},
			{Alias: "work", AccessToken: "token-work"},
		},
	})
	server := newProxyWithConfig(t, Config{
		Upstream: upstream.URL,
		Store:    store,
	})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/backend-api/wham/usage", nil))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "personal@example.com") {
		t.Fatalf("body = %q, want original usage response", resp.Body.String())
	}
	wantTokens := []string{"Bearer token-personal"}
	if len(tokens) != len(wantTokens) {
		t.Fatalf("tokens = %#v, want %#v", tokens, wantTokens)
	}
	for i := range wantTokens {
		if tokens[i] != wantTokens[i] {
			t.Fatalf("tokens[%d] = %q, want %q", i, tokens[i], wantTokens[i])
		}
	}
	if store.Data.ActiveAlias != "personal" {
		t.Fatalf("ActiveAlias = %q, want personal", store.Data.ActiveAlias)
	}
	if len(store.Data.Accounts[0].DisabledUntil) != 0 {
		t.Fatalf("DisabledUntil = %#v, want unchanged", store.Data.Accounts[0].DisabledUntil)
	}
}

func TestCompactPrefixTruncatesBodyAndWhitespace(t *testing.T) {
	t.Parallel()

	got := compactPrefix([]byte("alpha\n\n beta gamma delta"), 12)
	if got != "alpha beta" {
		t.Fatalf("compactPrefix() = %q, want alpha beta", got)
	}
	got = compactPrefix([]byte("abcdefghijk"), 5)
	if got != "abcde" {
		t.Fatalf("compactPrefix(long word) = %q, want abcde", got)
	}
}

func TestProxyReturnsBadRequestWhenRequestBodyCannotBeRead(t *testing.T) {
	t.Parallel()

	server := newTestProxy(t, "http://example.test", accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/responses", nil)
	req.Body = errReader{}
	resp := httptest.NewRecorder()

	server.Handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
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

func TestProxyRoutesV1PathsToAPIUpstream(t *testing.T) {
	t.Parallel()

	var chatGPTRequests int
	chatGPTUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chatGPTRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer chatGPTUpstream.Close()

	var gotPath string
	var gotAuth string
	apiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer apiUpstream.Close()

	server := newProxyWithConfig(t, Config{
		Upstream:    chatGPTUpstream.URL,
		APIUpstream: apiUpstream.URL,
		Store: newTestStore(t, accounts.Data{
			ActiveAlias: "personal",
			Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
		}),
	})
	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusCreated)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("api path = %q, want /v1/responses", gotPath)
	}
	if gotAuth != "Bearer token-personal" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if chatGPTRequests != 0 {
		t.Fatalf("chatGPTRequests = %d, want 0", chatGPTRequests)
	}
}

func TestSingleJoiningSlashVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    string
		b    string
		want string
	}{
		{name: "both slash", a: "/base/", b: "/path", want: "/base/path"},
		{name: "neither slash", a: "/base", b: "path", want: "/base/path"},
		{name: "left slash", a: "/base/", b: "path", want: "/base/path"},
		{name: "right slash", a: "/base", b: "/path", want: "/base/path"},
	}
	for _, tc := range cases {
		if got := singleJoiningSlash(tc.a, tc.b); got != tc.want {
			t.Fatalf("%s: singleJoiningSlash(%q, %q) = %q, want %q", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsUsageLimitReturnsFalseOnReadError(t *testing.T) {
	t.Parallel()

	resp := &http.Response{Body: errReader{}}
	if isUsageLimit(resp) {
		t.Fatal("isUsageLimit(errReader) = true, want false")
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

type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read failed")
}

func (errReader) Close() error {
	return nil
}

func newTestProxy(t *testing.T, upstream string, data accounts.Data) *http.Server {
	t.Helper()
	return newProxyWithStore(t, upstream, newTestStore(t, data))
}

func newProxyWithStore(t *testing.T, upstream string, store *accounts.Store) *http.Server {
	t.Helper()
	return newProxyWithConfig(t, Config{Upstream: upstream, Store: store})
}

func newProxyWithConfig(t *testing.T, config Config) *http.Server {
	t.Helper()
	server, err := New(config)
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
