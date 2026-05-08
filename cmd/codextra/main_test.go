package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gabewillen/codextra/internal/codexauth"
)

func TestCodexArgsPassesUserArgsThroughAfterProxyOverride(t *testing.T) {
	t.Parallel()

	userArgs := []string{"--model", "gpt-5.4", "--", "hello -c untouched"}
	got := codexArgs("http://127.0.0.1:1234", userArgs)
	want := []string{
		"-c",
		"chatgpt_base_url=http://127.0.0.1:1234/backend-api",
		"--model",
		"gpt-5.4",
		"--",
		"hello -c untouched",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexArgs() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(userArgs, []string{"--model", "gpt-5.4", "--", "hello -c untouched"}) {
		t.Fatalf("codexArgs mutated userArgs: %#v", userArgs)
	}
}

func TestActivateAccountWritesSelectedAliasEvenWhenDisabled(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)

	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "limited",
		AccessToken:  "token-limited",
		RefreshToken: "refresh-limited",
		AccountID:    "acct-limited",
	}); err != nil {
		t.Fatalf("Upsert(limited) error = %v", err)
	}
	loaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(persisted) error = %v", err)
	}
	loaded.Data.Accounts[0].DisabledUntil = map[string]int64{"codex_weekly": 9_999_999_999}
	if err := os.WriteFile(storePath, mustJSON(t, loaded.Data), 0600); err != nil {
		t.Fatalf("WriteFile(disabled store) error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "fallback",
		AccessToken:  "token-fallback",
		RefreshToken: "refresh-fallback",
		AccountID:    "acct-fallback",
	}); err != nil {
		t.Fatalf("Upsert(fallback) error = %v", err)
	}

	if err := activateAccount("limited"); err != nil {
		t.Fatalf("activateAccount(limited) error = %v", err)
	}

	bytes, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(auth.json) error = %v", err)
	}
	var auth codexauth.File
	if err := json.Unmarshal(bytes, &auth); err != nil {
		t.Fatalf("Unmarshal(auth.json) error = %v", err)
	}
	if auth.Tokens.AccessToken != "token-limited" {
		t.Fatalf("AccessToken = %q, want token-limited", auth.Tokens.AccessToken)
	}
	if auth.Tokens.AccountID != "acct-limited" {
		t.Fatalf("AccountID = %q, want acct-limited", auth.Tokens.AccountID)
	}
}

func TestCodexArgsAllowsUserOverrideToWinByOrder(t *testing.T) {
	t.Parallel()

	got := codexArgs("http://proxy", []string{"-c", "chatgpt_base_url=http://custom"})
	want := []string{"-c", "chatgpt_base_url=http://proxy/backend-api", "-c", "chatgpt_base_url=http://custom"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexArgs() = %#v, want %#v", got, want)
	}
}

func TestCodexChatGPTBaseURLPreservesBackendAPIBasePath(t *testing.T) {
	t.Parallel()

	got := codexChatGPTBaseURL("http://127.0.0.1:1234/")
	want := "http://127.0.0.1:1234/backend-api"
	if got != want {
		t.Fatalf("codexChatGPTBaseURL() = %q, want %q", got, want)
	}
}

func TestCodexEnvAppendsProxyURLWithoutDroppingBase(t *testing.T) {
	t.Parallel()

	base := []string{"A=1", "B=two"}
	got := codexEnv(base, "http://127.0.0.1:9999")
	want := []string{"A=1", "B=two", "CODEXTRA_PROXY_URL=http://127.0.0.1:9999"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexEnv() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(base, []string{"A=1", "B=two"}) {
		t.Fatalf("codexEnv mutated base: %#v", base)
	}
}

func TestParseCodextraArgsConsumesAccountFlag(t *testing.T) {
	t.Parallel()

	account, pass, err := parseCodextraArgs([]string{"--account", "work", "--model", "gpt-5.4"})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if account != "work" {
		t.Fatalf("account = %q, want work", account)
	}
	want := []string{"--model", "gpt-5.4"}
	if !reflect.DeepEqual(pass, want) {
		t.Fatalf("pass = %#v, want %#v", pass, want)
	}
}

func TestParseCodextraArgsConsumesAccountEqualsFlag(t *testing.T) {
	t.Parallel()

	account, pass, err := parseCodextraArgs([]string{"--model", "gpt-5.4", "--account=personal", "prompt"})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if account != "personal" {
		t.Fatalf("account = %q, want personal", account)
	}
	want := []string{"--model", "gpt-5.4", "prompt"}
	if !reflect.DeepEqual(pass, want) {
		t.Fatalf("pass = %#v, want %#v", pass, want)
	}
}

func TestParseCodextraArgsLeavesArgumentsAfterDashDashUntouched(t *testing.T) {
	t.Parallel()

	account, pass, err := parseCodextraArgs([]string{"--account=work", "--", "--account", "literal"})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if account != "work" {
		t.Fatalf("account = %q, want work", account)
	}
	want := []string{"--", "--account", "literal"}
	if !reflect.DeepEqual(pass, want) {
		t.Fatalf("pass = %#v, want %#v", pass, want)
	}
}

func TestParseCodextraArgsRejectsMissingAccountAlias(t *testing.T) {
	t.Parallel()

	if _, _, err := parseCodextraArgs([]string{"--account"}); err == nil {
		t.Fatal("parseCodextraArgs(--account) error = nil, want error")
	}
	if _, _, err := parseCodextraArgs([]string{"--account="}); err == nil {
		t.Fatal("parseCodextraArgs(--account=) error = nil, want error")
	}
}

func TestGetenvUsesFallbackOnlyForEmptyValues(t *testing.T) {
	t.Setenv("CODEXTRA_TEST_VALUE", "set")
	t.Setenv("CODEXTRA_TEST_EMPTY", "")

	if got := getenv("CODEXTRA_TEST_VALUE", "fallback"); got != "set" {
		t.Fatalf("getenv(set) = %q, want set", got)
	}
	if got := getenv("CODEXTRA_TEST_EMPTY", "fallback"); got != "fallback" {
		t.Fatalf("getenv(empty) = %q, want fallback", got)
	}
	if got := getenv("CODEXTRA_TEST_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("getenv(missing) = %q, want fallback", got)
	}
}

func TestProxyLeaseTTLUsesFallbackForInvalidValues(t *testing.T) {
	t.Setenv("CODEXTRA_PROXY_LEASE_TTL_SECONDS", "bad")
	if got := proxyLeaseTTL(); got != defaultProxyLeaseTTL {
		t.Fatalf("proxyLeaseTTL(invalid) = %s, want %s", got, defaultProxyLeaseTTL)
	}
	t.Setenv("CODEXTRA_PROXY_LEASE_TTL_SECONDS", "-1")
	if got := proxyLeaseTTL(); got != defaultProxyLeaseTTL {
		t.Fatalf("proxyLeaseTTL(negative) = %s, want %s", got, defaultProxyLeaseTTL)
	}
	t.Setenv("CODEXTRA_PROXY_LEASE_TTL_SECONDS", "7")
	if got := proxyLeaseTTL(); got != 7*time.Second {
		t.Fatalf("proxyLeaseTTL(valid) = %s, want 7s", got)
	}
}

func TestProxyLeaseLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEXTRA_HOME", home)

	lease, err := acquireProxyLease()
	if err != nil {
		t.Fatalf("acquireProxyLease() error = %v", err)
	}
	active, err := hasActiveProxyLease(time.Now())
	if err != nil {
		t.Fatalf("hasActiveProxyLease() error = %v", err)
	}
	if !active {
		t.Fatal("hasActiveProxyLease() = false, want true")
	}

	lease.Close()
	active, err = hasActiveProxyLease(time.Now())
	if err != nil {
		t.Fatalf("hasActiveProxyLease(after close) error = %v", err)
	}
	if active {
		t.Fatal("hasActiveProxyLease(after close) = true, want false")
	}
	if _, err := os.Stat(lease.path); !os.IsNotExist(err) {
		t.Fatalf("lease file still exists or stat error = %v", err)
	}
}

func TestHasActiveProxyLeaseRemovesStaleFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEXTRA_HOME", home)
	dir, err := proxyLeaseDir()
	if err != nil {
		t.Fatalf("proxyLeaseDir() error = %v", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(dir, "stale.lease")
	if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	old := time.Now().Add(-2 * defaultProxyLeaseTTL)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	active, err := hasActiveProxyLease(time.Now())
	if err != nil {
		t.Fatalf("hasActiveProxyLease() error = %v", err)
	}
	if active {
		t.Fatal("hasActiveProxyLease(stale) = true, want false")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale lease still exists or stat error = %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return bytes
}

func TestActivateAccountWritesSelectedAliasToCodexAuth(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)

	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "personal",
		AccessToken:  "token-personal",
		RefreshToken: "refresh-personal",
		AccountID:    "acct-personal",
	}); err != nil {
		t.Fatalf("Upsert(personal) error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  "token-work",
		RefreshToken: "refresh-work",
		IDToken:      `{"email":"work@example.com","chatgpt_plan_type":"pro"}`,
		AccountID:    "acct-work",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}

	if err := activateAccount("work"); err != nil {
		t.Fatalf("activateAccount(work) error = %v", err)
	}

	loaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(persisted) error = %v", err)
	}
	if loaded.Data.ActiveAlias != "work" {
		t.Fatalf("ActiveAlias = %q, want work", loaded.Data.ActiveAlias)
	}

	bytes, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(auth.json) error = %v", err)
	}
	var auth codexauth.File
	if err := json.Unmarshal(bytes, &auth); err != nil {
		t.Fatalf("Unmarshal(auth.json) error = %v", err)
	}
	want := &codexauth.TokenData{
		IDToken: map[string]any{
			"email":             "work@example.com",
			"chatgpt_plan_type": "pro",
		},
		AccessToken:  "token-work",
		RefreshToken: "refresh-work",
		AccountID:    "acct-work",
	}
	if !reflect.DeepEqual(auth.Tokens, want) {
		t.Fatalf("auth.Tokens = %#v, want %#v", auth.Tokens, want)
	}
	if auth.LastRefresh == "" {
		t.Fatal("LastRefresh = empty, want timestamp")
	}
}
