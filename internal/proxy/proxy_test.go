package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gabewillen/codextra/internal/codexauth"
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

func TestProxyReactivelyRefreshesAfterRotatingAccounts(t *testing.T) {
	refreshCalls := 0
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode(refresh) error = %v", err)
		}
		switch req.RefreshToken {
		case "refresh-personal":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token":  "token-personal-new",
				"refresh_token": "refresh-personal-new",
			})
		case "refresh-work":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token":  "token-work-new",
				"refresh_token": "refresh-work-new",
			})
		default:
			t.Fatalf("refresh token = %q, want refresh-personal or refresh-work", req.RefreshToken)
		}
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	resetAt := time.Unix(1_700_000_123, 0)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer token-personal":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "token_expired"}})
		case "Bearer token-personal-new":
			w.Header().Set("x-codex-active-limit", "codex_weekly")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"type":      "usage_limit_reached",
					"resets_at": resetAt.Unix(),
				},
			})
		case "Bearer token-work":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "token_expired"}})
		case "Bearer token-work-new":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
		default:
			t.Fatalf("Authorization = %q, want known bearer token", r.Header.Get("Authorization"))
		}
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{
			{Alias: "personal", AccessToken: "token-personal", RefreshToken: "refresh-personal"},
			{Alias: "work", AccessToken: "token-work", RefreshToken: "refresh-work"},
		},
	})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusOK, resp.Body.String())
	}
	if resp.Body.String() != "ok" {
		t.Fatalf("body = %q, want ok", resp.Body.String())
	}
	if refreshCalls != 2 {
		t.Fatalf("refreshCalls = %d, want 2", refreshCalls)
	}
}

func TestProxyReactivelyRefreshesWhenUpstreamRejectsFreshJWT(t *testing.T) {
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "token-new",
			"refresh_token": "refresh-new",
		})
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	freshToken := freshJWT(t)

	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  map[string]any{"code": "token_expired"},
				"status": 401,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "refreshed")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  freshToken,
			RefreshToken: "refresh-old",
		}},
	})
	server := newProxyWithConfig(t, Config{Upstream: upstream.URL, Store: store})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusOK, resp.Body.String())
	}
	wantTokens := []string{"Bearer " + freshToken, "Bearer token-new"}
	if !reflect.DeepEqual(tokens, wantTokens) {
		t.Fatalf("tokens = %#v, want %#v", tokens, wantTokens)
	}
}

func TestProxyRefreshesExpiredTokenBeforeReturningResponse(t *testing.T) {
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "token-new",
			"refresh_token": "refresh-new",
		})
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  map[string]any{"code": "token_expired"},
				"status": 401,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "refreshed")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  "token-old",
			RefreshToken: "refresh-old",
		}},
	})
	var synced accounts.Account
	server := newProxyWithConfig(t, Config{
		Upstream: upstream.URL,
		Store:    store,
		OnAccountUpdate: func(account accounts.Account) error {
			synced = account
			return nil
		},
	})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusOK, resp.Body.String())
	}
	if resp.Body.String() != "refreshed" {
		t.Fatalf("body = %q, want refreshed", resp.Body.String())
	}
	wantTokens := []string{"Bearer token-old", "Bearer token-new"}
	if !reflect.DeepEqual(tokens, wantTokens) {
		t.Fatalf("tokens = %#v, want %#v", tokens, wantTokens)
	}
	if store.Data.Accounts[0].AccessToken != "token-new" {
		t.Fatalf("stored AccessToken = %q, want token-new", store.Data.Accounts[0].AccessToken)
	}
	if store.Data.Accounts[0].RefreshToken != "refresh-new" {
		t.Fatalf("stored RefreshToken = %q, want refresh-new", store.Data.Accounts[0].RefreshToken)
	}
	if synced.AccessToken != "token-new" {
		t.Fatalf("synced AccessToken = %q, want token-new", synced.AccessToken)
	}
}

func TestProxySerializesConcurrentTokenRefresh(t *testing.T) {
	refreshCalls := 0
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls++
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "token-new",
			"refresh_token": "refresh-new",
		})
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  expiredJWT(t),
			RefreshToken: "refresh-old",
		}},
	})
	server := newProxyWithConfig(t, Config{Upstream: upstream.URL, Store: store})

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			resp := httptest.NewRecorder()
			server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))
			if resp.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.Code)
			}
		}()
	}
	wg.Wait()

	if refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", refreshCalls)
	}
}

func TestProxyProactivelyRefreshesStaleAccessToken(t *testing.T) {
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "token-new",
			"refresh_token": "refresh-new",
		})
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  expiredJWT(t),
			RefreshToken: "refresh-old",
		}},
	})
	server := newProxyWithConfig(t, Config{Upstream: upstream.URL, Store: store})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusOK, resp.Body.String())
	}
	if gotAuth != "Bearer token-new" {
		t.Fatalf("Authorization = %q, want Bearer token-new", gotAuth)
	}
}

func TestProxyAdoptsCodexAuthWithoutCallingRefreshEndpoint(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	const accountID = "acct-personal"
	fresh := jwtWithAccountExpiry(t, time.Now().Add(time.Hour).Unix(), accountID)
	stale := jwtWithAccountExpiry(t, time.Now().Add(-time.Hour).Unix(), accountID)
	auth := codexauth.File{
		Tokens: &codexauth.TokenData{
			AccessToken:  fresh,
			RefreshToken: "refresh-live",
			AccountID:    accountID,
		},
	}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  stale,
			RefreshToken: "refresh-old",
			AccountID:    accountID,
		}},
	})
	server := newProxyWithConfig(t, Config{Upstream: upstream.URL, Store: store})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusOK, resp.Body.String())
	}
	wantAuth := "Bearer " + fresh
	if gotAuth != wantAuth {
		t.Fatalf("Authorization mismatch (got %s, want %s)", redactTestSecret(gotAuth), redactTestSecret(wantAuth))
	}
	if refreshCalls.Load() != 0 {
		t.Fatalf("refreshCalls = %d, want 0", refreshCalls.Load())
	}
	if store.Data.Accounts[0].AccessToken != fresh {
		t.Fatalf("stored AccessToken mismatch (got %s, want %s)", redactTestSecret(store.Data.Accounts[0].AccessToken), redactTestSecret(fresh))
	}
}

func TestProxyAdoptsCodexAuthOnReactiveRefreshWithoutOAuthCall(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	const accountID = "acct-personal"
	fresh := jwtWithAccountExpiry(t, time.Now().Add(time.Hour).Unix(), accountID)
	stale := jwtWithAccountExpiry(t, time.Now().Add(time.Hour).Unix(), accountID)
	auth := codexauth.File{
		Tokens: &codexauth.TokenData{
			AccessToken:  fresh,
			RefreshToken: "refresh-live",
			AccountID:    accountID,
		},
	}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  map[string]any{"code": "token_expired"},
				"status": 401,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  stale,
			RefreshToken: "refresh-old",
			AccountID:    accountID,
		}},
	})
	server := newProxyWithConfig(t, Config{Upstream: upstream.URL, Store: store})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusOK, resp.Body.String())
	}
	wantTokens := []string{"Bearer " + stale, "Bearer " + fresh}
	if !reflect.DeepEqual(tokens, wantTokens) {
		t.Fatalf("tokens = %#v, want %#v", redactTestSecrets(tokens), redactTestSecrets(wantTokens))
	}
	if refreshCalls.Load() != 0 {
		t.Fatalf("refreshCalls = %d, want 0", refreshCalls.Load())
	}
}

func TestTokenExpiredMarkerDetects401Payload(t *testing.T) {
	t.Parallel()

	body := []byte(`{"error":{"code":"token_expired"},"status":401}`)
	if !tokenExpiredMarker(body) {
		t.Fatal("tokenExpiredMarker() = false, want true")
	}
}

func TestNotifyAccountUpdateHandlesNilAndCallbackErrors(t *testing.T) {
	t.Parallel()

	handler := &handler{}
	if err := handler.notifyAccountUpdate(accounts.Account{Alias: "work"}); err != nil {
		t.Fatalf("notifyAccountUpdate(nil callback) error = %v", err)
	}
	wantErr := errors.New("callback failed")
	handler.onAccountUpdate = func(accounts.Account) error {
		return wantErr
	}
	if err := handler.notifyAccountUpdate(accounts.Account{Alias: "work"}); err != wantErr {
		t.Fatalf("notifyAccountUpdate(error) = %v, want %v", err, wantErr)
	}
}

func expiredJWT(t *testing.T) string {
	t.Helper()
	return jwtWithExpiry(t, time.Now().Add(-time.Minute).Unix())
}

func freshJWT(t *testing.T) string {
	t.Helper()
	return jwtWithExpiry(t, time.Now().Add(time.Hour).Unix())
}

func jwtWithExpiry(t *testing.T, exp int64) string {
	t.Helper()
	return jwtWithAccountExpiry(t, exp, "")
}

func redactTestSecret(value string) string {
	if value == "" {
		return "<empty>"
	}
	if len(value) <= 12 {
		return "<redacted>"
	}
	return value[:4] + "…" + value[len(value)-4:]
}

func redactTestSecrets(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = redactTestSecret(value)
	}
	return out
}

func jwtWithAccountExpiry(t *testing.T, exp int64, accountID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	claims := map[string]any{"exp": exp}
	if accountID != "" {
		claims["chatgpt_account_id"] = accountID
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(claims) error = %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".signature"
}

func TestProxyRefreshesTokenEvenWhenSyncFails(t *testing.T) {
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "token-new",
			"refresh_token": "refresh-new",
		})
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  expiredJWT(t),
			RefreshToken: "refresh-old",
		}},
	})
	server := newProxyWithConfig(t, Config{
		Upstream: upstream.URL,
		Store:    store,
		OnAccountUpdate: func(accounts.Account) error {
			return errors.New("sync failed")
		},
	})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusOK, resp.Body.String())
	}
	if store.Data.Accounts[0].AccessToken != "token-new" {
		t.Fatalf("stored AccessToken = %q, want token-new", store.Data.Accounts[0].AccessToken)
	}
}

func TestProxyReturnsUnauthorizedWhenTokenRefreshFails(t *testing.T) {
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"refresh_token_expired"}}`))
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  map[string]any{"code": "token_expired"},
			"status": 401,
		})
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  "token-old",
			RefreshToken: "refresh-old",
		}},
	})

	resp := httptest.NewRecorder()
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", strings.NewReader("body")))

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}
}

func TestProxyWebSocketReturnsUnauthorizedWhenTokenRefreshFails(t *testing.T) {
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"refresh_token_expired"}}`))
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  map[string]any{"code": "token_expired"},
			"status": 401,
		})
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  "token-old",
			RefreshToken: "refresh-old",
		}},
	})
	proxyServer := httptest.NewServer(server.Handler)
	defer proxyServer.Close()

	conn, _, resp := openTestWebSocket(t, proxyServer.URL, "/backend-api/codex/responses")
	defer conn.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestProxyWebSocketRefreshesExpiredTokenBeforeUpgrade(t *testing.T) {
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "token-new",
			"refresh_token": "refresh-new",
		})
	}))
	defer refreshServer.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", refreshServer.URL)

	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  map[string]any{"code": "token_expired"},
				"status": 401,
			})
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("upstream writer is not hijackable")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("upstream hijack error = %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{{
			Alias:        "personal",
			AccessToken:  "token-old",
			RefreshToken: "refresh-old",
		}},
	})
	proxy := newProxyWithConfig(t, Config{Upstream: upstream.URL, Store: store})
	proxyServer := httptest.NewServer(proxy.Handler)
	defer proxyServer.Close()

	conn, _, resp := openTestWebSocket(t, proxyServer.URL, "/backend-api/codex/responses")
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	wantTokens := []string{"Bearer token-old", "Bearer token-new"}
	if !reflect.DeepEqual(tokens, wantTokens) {
		t.Fatalf("tokens = %#v, want %#v", tokens, wantTokens)
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

func TestProxyServerSetsReadHeaderTimeout(t *testing.T) {
	t.Parallel()

	server := newTestProxy(t, "http://example.test", accounts.Data{})
	if server.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout = 0, want positive timeout")
	}
}

func TestResponseCaptureLimitDoesNotCaptureSuccessfulStreams(t *testing.T) {
	t.Parallel()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"content-type": []string{"text/event-stream"}},
	}
	if got := responseCaptureLimit(resp); got != 0 {
		t.Fatalf("responseCaptureLimit(success stream) = %d, want 0", got)
	}
}

func TestResponseCaptureLimitCapsErrorBodies(t *testing.T) {
	t.Parallel()

	resp := &http.Response{StatusCode: http.StatusBadGateway}
	if got := responseCaptureLimit(resp); got != 4*1024 {
		t.Fatalf("responseCaptureLimit(error) = %d, want 4096", got)
	}
}

func TestProxyTunnelsWebSocketWithActiveAccountAuth(t *testing.T) {
	t.Parallel()

	authSeen := make(chan string, 1)
	accountSeen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen <- r.Header.Get("Authorization")
		accountSeen <- r.Header.Get("ChatGPT-Account-ID")
		if r.URL.Path != "/v1/responses" {
			t.Errorf("upstream path = %q, want /v1/responses", r.URL.Path)
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("upstream writer is not hijackable")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("upstream hijack error = %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_, _ = rw.WriteString("upstream-ready\n")
		if err := rw.Flush(); err != nil {
			t.Errorf("upstream flush error = %v", err)
			return
		}
		line, err := rw.ReadString('\n')
		if err != nil {
			t.Errorf("upstream read error = %v", err)
			return
		}
		_, _ = rw.WriteString("echo: " + line)
		_ = rw.Flush()
	}))
	defer upstream.Close()

	proxy := newProxyWithConfig(t, Config{
		Upstream:    "http://example.test",
		APIUpstream: upstream.URL,
		Store: newTestStore(t, accounts.Data{
			ActiveAlias: "personal",
			Accounts: []accounts.Account{{
				Alias:       "personal",
				AccessToken: "token-personal",
				AccountID:   "acct-personal",
			}},
		}),
	})
	proxyServer := httptest.NewServer(proxy.Handler)
	defer proxyServer.Close()

	conn, reader, resp := openTestWebSocket(t, proxyServer.URL, "/v1/responses")
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	ready, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(ready) error = %v", err)
	}
	if ready != "upstream-ready\n" {
		t.Fatalf("ready = %q, want upstream-ready", ready)
	}
	if _, err := conn.Write([]byte("client-data\n")); err != nil {
		t.Fatalf("Write(client-data) error = %v", err)
	}
	echo, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString(echo) error = %v", err)
	}
	if echo != "echo: client-data\n" {
		t.Fatalf("echo = %q, want echo", echo)
	}
	if got := <-authSeen; got != "Bearer token-personal" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	if got := <-accountSeen; got != "acct-personal" {
		t.Fatalf("ChatGPT-Account-ID = %q, want acct-personal", got)
	}
}

func TestProxyWebSocketRotatesOnUsageLimitBeforeUpgrade(t *testing.T) {
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
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("upstream writer is not hijackable")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("upstream hijack error = %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
	}))
	defer upstream.Close()

	store := newTestStore(t, accounts.Data{
		ActiveAlias: "personal",
		Accounts: []accounts.Account{
			{Alias: "personal", AccessToken: "token-personal"},
			{Alias: "work", AccessToken: "token-work"},
		},
	})
	proxy := newProxyWithConfig(t, Config{Upstream: upstream.URL, Store: store})
	proxyServer := httptest.NewServer(proxy.Handler)
	defer proxyServer.Close()

	conn, _, resp := openTestWebSocket(t, proxyServer.URL, "/backend-api/codex/responses")
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
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
		t.Fatalf("DisabledUntil[codex_weekly] = %d, want %d", got, resetAt.Unix())
	}
}

func TestProxyWebSocketCopiesPreUpgradeError(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Unauthorized"}`))
	}))
	defer upstream.Close()

	proxy := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	proxyServer := httptest.NewServer(proxy.Handler)
	defer proxyServer.Close()

	conn, _, resp := openTestWebSocket(t, proxyServer.URL, "/backend-api/codex/responses")
	defer conn.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(body) error = %v", err)
	}
	if !strings.Contains(string(body), "Unauthorized") {
		t.Fatalf("body = %q, want unauthorized detail", string(body))
	}
}

func TestProxyWebSocketReturnsUnavailableWithoutEligibleAccount(t *testing.T) {
	t.Parallel()

	server := newTestProxy(t, "http://example.test", accounts.Data{
		Accounts: []accounts.Account{{Alias: "empty"}},
	})
	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp := httptest.NewRecorder()

	server.Handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}
}

func TestProxyWebSocketReturnsBadGatewayWhenUpstreamUnavailable(t *testing.T) {
	t.Parallel()

	server := newTestProxy(t, "http://127.0.0.1:1", accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp := httptest.NewRecorder()

	server.Handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadGateway)
	}
}

func TestProxyWebSocketReturnsTooManyRequestsWhenAllAccountsLimited(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-codex-active-limit", "codex_weekly")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"type": "usage_limit_reached"},
		})
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp := httptest.NewRecorder()

	server.Handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusTooManyRequests)
	}
}

func TestProxyWebSocketReturnsServerErrorWhenWriterCannotHijack(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("upstream writer is not hijackable")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("upstream hijack error = %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
	}))
	defer upstream.Close()

	server := newTestProxy(t, upstream.URL, accounts.Data{
		ActiveAlias: "personal",
		Accounts:    []accounts.Account{{Alias: "personal", AccessToken: "token-personal"}},
	})
	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp := httptest.NewRecorder()

	server.Handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusInternalServerError)
	}
}

func TestDialURLReportsTLSHandshakeFailure(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewTLSServer(http.NotFoundHandler())
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("Parse(upstream.URL) error = %v", err)
	}

	conn, err := dialURL(contextWithTimeout(t), parsed)
	if err == nil {
		conn.Close()
		t.Fatal("dialURL(NewTLSServer) error = nil, want certificate error")
	}
}

func TestNewRejectsInvalidUpstream(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Upstream: "://bad-url", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(invalid upstream) error = nil, want error")
	}
	if _, err := New(Config{Upstream: "file:///tmp/socket", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(file upstream) error = nil, want error")
	}
	if _, err := New(Config{Upstream: "https://", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(hostless upstream) error = nil, want error")
	}
}

func TestNewRejectsInvalidAPIUpstream(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Upstream: "http://example.test", APIUpstream: "://bad-url", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(invalid API upstream) error = nil, want error")
	}
	if _, err := New(Config{Upstream: "http://example.test", APIUpstream: "ftp://example.test", Store: newTestStore(t, accounts.Data{})}); err == nil {
		t.Fatal("New(ftp API upstream) error = nil, want error")
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

func TestProxyPreservesAmbiguousUsageLimitText(t *testing.T) {
	t.Parallel()

	var tokens []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("You've hit your usage limit."))
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
	server.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/responses", nil))

	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusTooManyRequests)
	}
	if len(tokens) != 1 || tokens[0] != "Bearer token-personal" {
		t.Fatalf("tokens = %#v, want only personal token", tokens)
	}
	if store.Data.ActiveAlias != "personal" {
		t.Fatalf("ActiveAlias = %q, want personal", store.Data.ActiveAlias)
	}
}

func TestUsageLimitMarkerRequiresStructuredJSON(t *testing.T) {
	t.Parallel()

	if !usageLimitMarker(http.Header{}, []byte(`{"error":{"type":"usage_limit_reached"}}`)) {
		t.Fatal("usageLimitMarker(structured) = false, want true")
	}
	if !usageLimitMarker(http.Header{}, []byte(`{"errors":[{"code":"usage_limit_reached"}]}`)) {
		t.Fatal("usageLimitMarker(array value) = false, want true")
	}
	if usageLimitMarker(http.Header{}, []byte("usage_limit_reached")) {
		t.Fatal("usageLimitMarker(raw text) = true, want false")
	}
	if usageLimitMarker(http.Header{}, []byte(`{"error":{"type":"rate_limit_reached","message":"usage limit"}}`)) {
		t.Fatal("usageLimitMarker(ambiguous text) = true, want false")
	}
	if usageLimitMarker(http.Header{}, []byte(`{"error":{"type":429}}`)) {
		t.Fatal("usageLimitMarker(non-string value) = true, want false")
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

func TestLogResponseIncludesCopyError(t *testing.T) {
	t.Parallel()

	handler := &handler{
		upstream:    mustParseURL(t, "http://chatgpt.test"),
		apiUpstream: mustParseURL(t, "http://api.test"),
		logger:      slogDiscard(),
	}
	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/responses", nil)
	resp := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"content-type": []string{"text/plain"}}}
	handler.logResponse(req, resp, accounts.Account{Alias: "personal"}, time.Millisecond, []byte("Usage limit"), errors.New("copy failed"))
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

func openTestWebSocket(t *testing.T, serverURL string, path string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", serverURL, err)
	}
	conn, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("Dial(%q) error = %v", parsed.Host, err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+parsed.Host+path, nil)
	if err != nil {
		conn.Close()
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")
	if err := req.Write(conn); err != nil {
		conn.Close()
		t.Fatalf("Write(request) error = %v", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		conn.Close()
		t.Fatalf("ReadResponse() error = %v", err)
	}
	return conn, reader, resp
}

func contextWithTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func mustParseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return parsed
}

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
