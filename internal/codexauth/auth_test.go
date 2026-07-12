package codexauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gabewillen/codextra/internal/accounts"
)

func TestImportReadsCodexAuth(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	auth := File{
		Tokens: &TokenData{
			IDToken:      map[string]any{"email": "work@example.com"},
			AccessToken:  fakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com", "chatgpt_plan_type": "pro"}),
			RefreshToken: "refresh-work",
		},
	}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	account, err := Import("work", path)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	want := accounts.Account{
		Alias:        "work",
		AccessToken:  auth.Tokens.AccessToken,
		RefreshToken: "refresh-work",
		IDToken:      `{"email":"work@example.com"}`,
		AccountID:    "acct-work",
		Email:        "work@example.com",
		PlanType:     "pro",
	}
	if !reflect.DeepEqual(account, want) {
		t.Fatalf("account = %#v, want %#v", account, want)
	}
}

func TestIdentityFromAccessTokenClaimPrecedenceAndFallbacks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		token  string
		claims map[string]any
		want   Identity
	}{
		{
			name: "openai account id and standard identity claims win",
			claims: map[string]any{
				"https://api.openai.com/auth_account_id": "acct-auth",
				"chatgpt_account_id":                     "acct-chatgpt",
				"account_id":                             "acct-legacy",
				"email":                                  "standard@example.com",
				"https://api.openai.com/email":           "url@example.com",
				"chatgpt_plan_type":                      "team",
				"https://api.openai.com/plan_type":       "pro",
			},
			want: Identity{AccountID: "acct-auth", Email: "standard@example.com", PlanType: "team"},
		},
		{
			name: "chatgpt account id and namespaced identity fallbacks",
			claims: map[string]any{
				"chatgpt_account_id":               "acct-chatgpt",
				"account_id":                       "acct-legacy",
				"https://api.openai.com/email":     "url@example.com",
				"https://api.openai.com/plan_type": "pro",
			},
			want: Identity{AccountID: "acct-chatgpt", Email: "url@example.com", PlanType: "pro"},
		},
		{
			name:   "legacy account id fallback",
			claims: map[string]any{"account_id": "acct-legacy"},
			want:   Identity{AccountID: "acct-legacy"},
		},
		{
			name:  "opaque token",
			token: "not-a-jwt",
			want:  Identity{},
		},
		{
			name:  "invalid payload",
			token: "header.!bad.signature",
			want:  Identity{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.token
			if token == "" {
				token = fakeJWT(t, tc.claims)
			}
			if got := IdentityFromAccessToken(token); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("IdentityFromAccessToken() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestWriteCodexAuth(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "codex", "auth.json")
	account := accounts.Account{
		Alias:        "work",
		AccessToken:  "token-work",
		RefreshToken: "refresh-work",
		IDToken:      `{"email":"work@example.com"}`,
		AccountID:    "acct-work",
	}
	if err := Write(path, account); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var auth File
	if err := json.Unmarshal(bytes, &auth); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	want := &TokenData{
		IDToken:      map[string]any{"email": "work@example.com"},
		AccessToken:  "token-work",
		RefreshToken: "refresh-work",
		AccountID:    "acct-work",
	}
	if !reflect.DeepEqual(auth.Tokens, want) {
		t.Fatalf("tokens = %#v, want %#v", auth.Tokens, want)
	}
	if auth.LastRefresh == "" {
		t.Fatal("LastRefresh = empty, want timestamp")
	}
}

func TestWriteReplacesExistingCodexAuth(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"old-token"}}`), 0600); err != nil {
		t.Fatalf("WriteFile(existing auth) error = %v", err)
	}
	if err := Write(path, accounts.Account{Alias: "work", AccessToken: "new-token"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	account, err := Import("work", path)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if account.AccessToken != "new-token" {
		t.Fatalf("AccessToken = %q, want new-token", account.AccessToken)
	}
}

func TestReplaceWrittenAuthWindowsReplacesExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(path, []byte("old auth"), 0600); err != nil {
		t.Fatalf("WriteFile(existing auth) error = %v", err)
	}
	if err := os.WriteFile(tmp, []byte("new auth"), 0600); err != nil {
		t.Fatalf("WriteFile(replacement auth) error = %v", err)
	}
	if err := replaceWrittenAuth(tmp, path, true); err != nil {
		t.Fatalf("replaceWrittenAuth(windows) error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(replaced auth) error = %v", err)
	}
	if got := string(contents); got != "new auth" {
		t.Fatalf("auth contents = %q, want new auth", got)
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup stat error = %v, want not exist", err)
	}
}

func TestPathUsesCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if got, want := mustPath(t), filepath.Join(home, "auth.json"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestPathDefaultsToHomeCodexAuth(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	path, err := Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join(".codex", "auth.json")) {
		t.Fatalf("Path() = %q, want ~/.codex/auth.json suffix", path)
	}
}

func TestImportRejectsInvalidAuth(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{}}`), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Import("empty", path); err == nil {
		t.Fatal("Import(empty tokens) error = nil, want error")
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatalf("WriteFile(malformed) error = %v", err)
	}
	if _, err := Import("bad", path); err == nil {
		t.Fatal("Import(malformed) error = nil, want error")
	}
}

func TestWriteRejectsTokenlessAccount(t *testing.T) {
	t.Parallel()

	if err := Write(filepath.Join(t.TempDir(), "auth.json"), accounts.Account{Alias: "empty"}); err == nil {
		t.Fatal("Write(tokenless) error = nil, want error")
	}
}

func TestWriteReturnsDirectoryError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile(blocker) error = %v", err)
	}
	err := Write(filepath.Join(blocker, "auth.json"), accounts.Account{Alias: "work", AccessToken: "token"})
	if err == nil {
		t.Fatal("Write(path under file) error = nil, want error")
	}
}

func TestWriteUsesEmptyObjectForMissingIDToken(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := Write(path, accounts.Account{Alias: "work", AccessToken: "token"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var auth File
	if err := json.Unmarshal(bytes, &auth); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(auth.Tokens.IDToken, map[string]any{}) {
		t.Fatalf("IDToken = %#v, want empty object", auth.Tokens.IDToken)
	}
}

func TestWritePreservesStringIDToken(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "auth.json")
	account := accounts.Account{
		Alias:       "work",
		AccessToken: "token-work",
		IDToken:     "raw-id-token",
	}
	if err := Write(path, account); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var auth File
	if err := json.Unmarshal(bytes, &auth); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if auth.Tokens.IDToken != "raw-id-token" {
		t.Fatalf("IDToken = %#v, want raw-id-token", auth.Tokens.IDToken)
	}
}

func TestTokenHelpersHandleInvalidInputs(t *testing.T) {
	t.Parallel()

	if got := idTokenString(make(chan int)); got != "" {
		t.Fatalf("idTokenString(unmarshalable) = %q, want empty", got)
	}
	if got := jwtClaims("not-a-jwt"); got != nil {
		t.Fatalf("jwtClaims(short) = %#v, want nil", got)
	}
	if got := jwtClaims("header.!bad.signature"); got != nil {
		t.Fatalf("jwtClaims(bad base64) = %#v, want nil", got)
	}
	badJSON := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	if got := jwtClaims("header." + badJSON + ".signature"); got != nil {
		t.Fatalf("jwtClaims(bad json) = %#v, want nil", got)
	}
	if got := stringClaim(nil, "email"); got != "" {
		t.Fatalf("stringClaim(nil) = %q, want empty", got)
	}
	if got := firstNonEmpty("", "  ", "value"); got != "value" {
		t.Fatalf("firstNonEmpty() = %q, want value", got)
	}
	if got := firstNonEmpty("", "  "); got != "" {
		t.Fatalf("firstNonEmpty(empty) = %q, want empty", got)
	}
}

func redactSecret(value string) string {
	if value == "" {
		return "<empty>"
	}
	if len(value) <= 12 {
		return "<redacted>"
	}
	return value[:4] + "…" + value[len(value)-4:]
}

func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(claims) error = %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".signature"
}

func mustPath(t *testing.T) string {
	t.Helper()
	path, err := Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	return path
}
