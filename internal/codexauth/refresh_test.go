package codexauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

func TestAccessTokenStaleUsesExpiryWithSkew(t *testing.T) {
	t.Parallel()

	expired := fakeJWT(t, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()})
	if !AccessTokenStale(expired, time.Now()) {
		t.Fatal("AccessTokenStale(expired) = false, want true")
	}

	fresh := fakeJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	if AccessTokenStale(fresh, time.Now()) {
		t.Fatal("AccessTokenStale(fresh) = true, want false")
	}

	nearExpiry := fakeJWT(t, map[string]any{"exp": time.Now().Add(10 * time.Second).Unix()})
	if !AccessTokenStale(nearExpiry, time.Now()) {
		t.Fatal("AccessTokenStale(nearExpiry) = false, want true")
	}
}

func TestRefreshReturnsUpdatedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode(body) error = %v", err)
		}
		if req.ClientID != clientID || req.GrantType != "refresh_token" || req.RefreshToken != "refresh-old" {
			t.Fatalf("request = %#v, want codex refresh payload", req)
		}
		_ = json.NewEncoder(w).Encode(refreshResponse{
			AccessToken:  "access-new",
			RefreshToken: "refresh-new",
			IDToken:      "id-new",
		})
	}))
	defer server.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", server.URL)

	tokens, err := Refresh(context.Background(), server.Client(), "refresh-old")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if tokens.AccessToken != "access-new" {
		t.Fatalf("AccessToken = %q, want access-new", tokens.AccessToken)
	}
	if tokens.RefreshToken != "refresh-new" {
		t.Fatalf("RefreshToken = %q, want refresh-new", tokens.RefreshToken)
	}
	if tokens.IDToken != "id-new" {
		t.Fatalf("IDToken = %q, want id-new", tokens.IDToken)
	}
}

func TestRefreshRejectsMissingRefreshToken(t *testing.T) {
	t.Parallel()

	if _, err := Refresh(context.Background(), http.DefaultClient, ""); err == nil {
		t.Fatal("Refresh(empty) error = nil, want error")
	}
}

func TestRefreshMapsUnauthorizedFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"refresh_token_expired"}}`))
	}))
	defer server.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", server.URL)

	_, err := Refresh(context.Background(), server.Client(), "refresh-old")
	if err == nil || err.Error() != "refresh token expired; sign in again" {
		t.Fatalf("Refresh() error = %v, want refresh token expired", err)
	}
}

func TestMergeRefreshUpdatesAccountFields(t *testing.T) {
	t.Parallel()

	account := accounts.Account{
		Alias:        "work",
		AccessToken:  "old",
		RefreshToken: "refresh-old",
		AccountID:    "acct-work",
	}
	tokens := TokenData{
		AccessToken:  fakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com", "chatgpt_plan_type": "pro"}),
		RefreshToken: "refresh-new",
		IDToken:      map[string]any{"email": "work@example.com"},
	}

	updated := MergeRefresh(account, tokens)
	if updated.AccessToken != tokens.AccessToken {
		t.Fatalf("AccessToken = %q, want refreshed token", updated.AccessToken)
	}
	if updated.RefreshToken != "refresh-new" {
		t.Fatalf("RefreshToken = %q, want refresh-new", updated.RefreshToken)
	}
	if updated.Email != "work@example.com" {
		t.Fatalf("Email = %q, want work@example.com", updated.Email)
	}
	if updated.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want pro", updated.PlanType)
	}
}

func TestRefreshErrorCodeParsesNestedAndTopLevelErrors(t *testing.T) {
	t.Parallel()

	if got := refreshErrorCode([]byte(`{"error":{"code":"refresh_token_reused"}}`)); got != "refresh_token_reused" {
		t.Fatalf("nested code = %q, want refresh_token_reused", got)
	}
	if got := refreshErrorCode([]byte(`{"error":"refresh_token_invalidated"}`)); got != "refresh_token_invalidated" {
		t.Fatalf("string code = %q, want refresh_token_invalidated", got)
	}
	if got := refreshErrorCode([]byte(`{"code":"refresh_token_expired"}`)); got != "refresh_token_expired" {
		t.Fatalf("top-level code = %q, want refresh_token_expired", got)
	}
	if got := refreshErrorCode([]byte(`not-json`)); got != "" {
		t.Fatalf("invalid json code = %q, want empty", got)
	}
}

func TestRefreshMapsOtherFailureModes(t *testing.T) {
	if err := classifyRefreshFailure(http.StatusUnauthorized, []byte(`{"error":{"code":"refresh_token_reused"}}`)); err == nil || err.Error() != "refresh token already used; sign in again" {
		t.Fatalf("classify reused = %v, want refresh token already used", err)
	}
	if err := classifyRefreshFailure(http.StatusUnauthorized, []byte(`{"error":{"code":"refresh_token_invalidated"}}`)); err == nil || err.Error() != "refresh token revoked; sign in again" {
		t.Fatalf("classify revoked = %v, want refresh token revoked", err)
	}
	if err := classifyRefreshFailure(http.StatusUnauthorized, []byte(`{"error":{"code":"unknown"}}`)); err == nil || err.Error() != "refresh token rejected; sign in again" {
		t.Fatalf("classify unknown 401 = %v, want refresh token rejected", err)
	}
	if err := classifyRefreshFailure(http.StatusBadGateway, []byte(`{}`)); err == nil || err.Error() != "refresh request returned Bad Gateway" {
		t.Fatalf("classify 502 = %v, want Bad Gateway", err)
	}
}

func TestRefreshReturnsTransportAndParseErrors(t *testing.T) {
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", "http://127.0.0.1:1")

	if _, err := Refresh(context.Background(), http.DefaultClient, "refresh-old"); err == nil {
		t.Fatal("Refresh(unreachable) error = nil, want error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", server.URL)

	if _, err := Refresh(context.Background(), server.Client(), "refresh-old"); err == nil {
		t.Fatal("Refresh(invalid json) error = nil, want error")
	}
}

func TestRefreshRejectsMissingAccessTokenInSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"refresh_token":"refresh-new"}`))
	}))
	defer server.Close()
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", server.URL)

	if _, err := Refresh(context.Background(), server.Client(), "refresh-old"); err == nil {
		t.Fatal("Refresh(missing access token) error = nil, want error")
	}
}

func TestAccessTokenStaleIgnoresMalformedJWT(t *testing.T) {
	t.Parallel()

	if AccessTokenStale("not-a-jwt", time.Now()) {
		t.Fatal("AccessTokenStale(malformed) = true, want false")
	}
}

func TestRefreshTokenURLUsesDefaultWhenUnset(t *testing.T) {
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", "")
	if got, want := refreshTokenURL(), defaultRefreshURL; got != want {
		t.Fatalf("refreshTokenURL() = %q, want %q", got, want)
	}
}
