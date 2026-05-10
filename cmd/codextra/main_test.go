package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		"-c",
		`model_providers.codextra={ name="Codextra", base_url="http://127.0.0.1:1234/backend-api/codex", wire_api="responses", requires_openai_auth=true }`,
		"-c",
		`model_provider="codextra"`,
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

func TestActivateAccountSelectsAliasEvenWhenDisabled(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	t.Setenv("CODEXTRA_STORE", storePath)

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

	account, err := activateAccount("limited")
	if err != nil {
		t.Fatalf("activateAccount(limited) error = %v", err)
	}
	if account.Alias != "limited" {
		t.Fatalf("activated account alias = %q, want limited", account.Alias)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	if reloaded.Data.ActiveAlias != "limited" {
		t.Fatalf("ActiveAlias = %q, want limited", reloaded.Data.ActiveAlias)
	}
	limited, ok := reloaded.Get("limited")
	if !ok {
		t.Fatal("limited account missing after activate")
	}
	if len(limited.DisabledUntil) != 0 {
		t.Fatalf("DisabledUntil = %#v, want cleared", limited.DisabledUntil)
	}
}

func TestCodexArgsAllowsUserOverrideToWinByOrder(t *testing.T) {
	t.Parallel()

	got := codexArgs("http://proxy", []string{
		"-c", "chatgpt_base_url=http://custom",
		"-c", `model_provider="openai"`,
	})
	want := []string{
		"-c", "chatgpt_base_url=http://proxy/backend-api",
		"-c", `model_providers.codextra={ name="Codextra", base_url="http://proxy/backend-api/codex", wire_api="responses", requires_openai_auth=true }`,
		"-c", `model_provider="codextra"`,
		"-c", "chatgpt_base_url=http://custom",
		"-c", `model_provider="openai"`,
	}
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

func TestCodexModelProviderConfigUsesChatGPTCodexBasePath(t *testing.T) {
	t.Parallel()

	got := codexModelProviderConfig("http://127.0.0.1:1234/")
	want := `{ name="Codextra", base_url="http://127.0.0.1:1234/backend-api/codex", wire_api="responses", requires_openai_auth=true }`
	if got != want {
		t.Fatalf("codexModelProviderConfig() = %q, want %q", got, want)
	}
}

func TestCodexEnvAppendsProxyURLWithoutDroppingBase(t *testing.T) {
	t.Parallel()

	base := []string{"A=1", "B=two"}
	got := codexEnv(base, "http://127.0.0.1:9999", "")
	want := []string{"A=1", "B=two", "CODEXTRA_PROXY_URL=http://127.0.0.1:9999"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexEnv() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(base, []string{"A=1", "B=two"}) {
		t.Fatalf("codexEnv mutated base: %#v", base)
	}
}

func TestCodexEnvReplacesProxyURLAndCodexHome(t *testing.T) {
	t.Parallel()

	base := []string{"CODEX_HOME=/real", "CODEXTRA_PROXY_URL=http://old", "A=1"}
	got := codexEnv(base, "http://127.0.0.1:9999", "/tmp/codex-home")
	want := []string{"A=1", "CODEXTRA_PROXY_URL=http://127.0.0.1:9999", "CODEX_HOME=/tmp/codex-home"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexEnv() = %#v, want %#v", got, want)
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

func TestProxyIdleGraceUsesFallbackForInvalidValues(t *testing.T) {
	t.Setenv("CODEXTRA_PROXY_IDLE_GRACE_SECONDS", "bad")
	if got := proxyIdleGrace(); got != defaultProxyIdleGrace {
		t.Fatalf("proxyIdleGrace(invalid) = %s, want %s", got, defaultProxyIdleGrace)
	}
	t.Setenv("CODEXTRA_PROXY_IDLE_GRACE_SECONDS", "-1")
	if got := proxyIdleGrace(); got != defaultProxyIdleGrace {
		t.Fatalf("proxyIdleGrace(negative) = %s, want %s", got, defaultProxyIdleGrace)
	}
	t.Setenv("CODEXTRA_PROXY_IDLE_GRACE_SECONDS", "7")
	if got := proxyIdleGrace(); got != 7*time.Second {
		t.Fatalf("proxyIdleGrace(valid) = %s, want 7s", got)
	}
}

func TestProxyLifecycleStreamTracksClientDisconnect(t *testing.T) {
	shutdown := make(chan struct{})
	lifecycle := newProxyLifecycle(http.NotFoundHandler(), slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {
		close(shutdown)
	})
	lifecycle.grace = 10 * time.Millisecond
	server := httptest.NewServer(lifecycle)
	defer server.Close()

	client, err := attachProxyClient(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("attachProxyClient() error = %v", err)
	}
	lifecycle.mu.Lock()
	clients := lifecycle.clients
	lifecycle.mu.Unlock()
	if clients != 1 {
		t.Fatalf("clients = %d, want 1", clients)
	}

	client.Close()
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("proxy lifecycle did not shut down after client disconnect")
	}
}

func TestProxyLifecycleRejectsWrongMethod(t *testing.T) {
	lifecycle := newProxyLifecycle(http.NotFoundHandler(), slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {})
	req := httptest.NewRequest(http.MethodGet, "/__codextra/client", nil)
	resp := httptest.NewRecorder()

	lifecycle.ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusMethodNotAllowed)
	}
}

func TestRoutePrefixHandlerRequiresSecretPrefix(t *testing.T) {
	t.Parallel()

	var gotPath string
	handler := newRoutePrefixHandler("/__codextra/secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/backend-api/wham/usage", nil))
	if unauthorized.Code != http.StatusNotFound {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusNotFound)
	}

	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, httptest.NewRequest(http.MethodGet, "/__codextra/secret/backend-api/wham/usage", nil))
	if authorized.Code != http.StatusAccepted {
		t.Fatalf("authorized status = %d, want %d", authorized.Code, http.StatusAccepted)
	}
	if gotPath != "/backend-api/wham/usage" {
		t.Fatalf("forwarded path = %q, want /backend-api/wham/usage", gotPath)
	}
}

func TestProxyDisplayURLRedactsRoutePrefix(t *testing.T) {
	t.Parallel()

	got := proxyDisplayURL("http://127.0.0.1:1234/__codextra/secret")
	want := "http://127.0.0.1:1234"
	if got != want {
		t.Fatalf("proxyDisplayURL() = %q, want %q", got, want)
	}
}

func TestRandomRoutePrefixIsUnguessablePath(t *testing.T) {
	t.Parallel()

	first, err := randomRoutePrefix()
	if err != nil {
		t.Fatalf("randomRoutePrefix() error = %v", err)
	}
	second, err := randomRoutePrefix()
	if err != nil {
		t.Fatalf("randomRoutePrefix() second error = %v", err)
	}
	if !strings.HasPrefix(first, "/__codextra/") {
		t.Fatalf("prefix = %q, want /__codextra/ prefix", first)
	}
	if len(strings.TrimPrefix(first, "/__codextra/")) != 48 {
		t.Fatalf("token length = %d, want 48", len(strings.TrimPrefix(first, "/__codextra/")))
	}
	if first == second {
		t.Fatal("randomRoutePrefix returned duplicate prefixes")
	}
}

func TestRoutePrefixFromProxyURLValidatesSecretPrefix(t *testing.T) {
	t.Parallel()

	prefix, ok := routePrefixFromProxyURL("http://127.0.0.1:1234/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef")
	if !ok {
		t.Fatal("routePrefixFromProxyURL(valid) ok = false, want true")
	}
	if prefix != "/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("prefix = %q", prefix)
	}

	for _, value := range []string{
		"http://127.0.0.1:1234",
		"http://127.0.0.1:1234/backend-api",
		"http://127.0.0.1:1234/__codextra/not-hex",
		"http://127.0.0.1:1234/__codextra/0123456789abcdef",
		"://bad",
	} {
		if prefix, ok := routePrefixFromProxyURL(value); ok {
			t.Fatalf("routePrefixFromProxyURL(%q) = %q, true; want false", value, prefix)
		}
	}
}

func TestReusableProxyAddrUsesPreviousLoopbackPort(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CODEXTRA_HOME", tempDir)
	if err := writeProxyState(proxyState{
		URL:     "http://127.0.0.1:49408/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef",
		PID:     123,
		Version: proxyStateVersion,
	}); err != nil {
		t.Fatalf("writeProxyState() error = %v", err)
	}

	if got := reusableProxyAddr(); got != "127.0.0.1:49408" {
		t.Fatalf("reusableProxyAddr() = %q, want 127.0.0.1:49408", got)
	}
}

func TestReusableProxyAddrRejectsNonLoopbackHost(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CODEXTRA_HOME", tempDir)
	if err := writeProxyState(proxyState{
		URL:     "http://example.com:49408/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef",
		PID:     123,
		Version: proxyStateVersion,
	}); err != nil {
		t.Fatalf("writeProxyState() error = %v", err)
	}

	if got := reusableProxyAddr(); got != "127.0.0.1:0" {
		t.Fatalf("reusableProxyAddr() = %q, want 127.0.0.1:0", got)
	}
}

func TestReusableProxyAddrRejectsNonNumericPort(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CODEXTRA_HOME", tempDir)
	if err := writeProxyState(proxyState{
		URL:     "http://127.0.0.1:http/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef",
		PID:     123,
		Version: proxyStateVersion,
	}); err != nil {
		t.Fatalf("writeProxyState() error = %v", err)
	}

	if got := reusableProxyAddr(); got != "127.0.0.1:0" {
		t.Fatalf("reusableProxyAddr() = %q, want 127.0.0.1:0", got)
	}
}

func TestReusableProxyAddrRejectsOutOfRangePort(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CODEXTRA_HOME", tempDir)
	if err := writeProxyState(proxyState{
		URL:     "http://127.0.0.1:70000/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef",
		PID:     123,
		Version: proxyStateVersion,
	}); err != nil {
		t.Fatalf("writeProxyState() error = %v", err)
	}

	if got := reusableProxyAddr(); got != "127.0.0.1:0" {
		t.Fatalf("reusableProxyAddr() = %q, want 127.0.0.1:0", got)
	}
}

func TestReusableRoutePrefixUsesPreviousSecret(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CODEXTRA_HOME", tempDir)
	if err := writeProxyState(proxyState{
		URL:     "http://127.0.0.1:49408/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef",
		PID:     123,
		Version: proxyStateVersion,
	}); err != nil {
		t.Fatalf("writeProxyState() error = %v", err)
	}

	prefix, err := reusableRoutePrefix()
	if err != nil {
		t.Fatalf("reusableRoutePrefix() error = %v", err)
	}
	if prefix != "/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("reusableRoutePrefix() = %q", prefix)
	}
}

func TestAttachProxyClientReturnsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusTeapot)
	}))
	defer server.Close()

	client, err := attachProxyClient(context.Background(), server.URL)
	if err == nil {
		client.Close()
		t.Fatal("attachProxyClient(status error) error = nil, want error")
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

func TestActivateAccountSetsSelectedAliasOnlyInCodextraStore(t *testing.T) {
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

	account, err := activateAccount("work")
	if err != nil {
		t.Fatalf("activateAccount(work) error = %v", err)
	}
	if account.Alias != "work" {
		t.Fatalf("activated account alias = %q, want work", account.Alias)
	}

	loaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(persisted) error = %v", err)
	}
	if loaded.Data.ActiveAlias != "work" {
		t.Fatalf("ActiveAlias = %q, want work", loaded.Data.ActiveAlias)
	}

	if _, err := os.Stat(filepath.Join(codexHome, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json stat error = %v, want not exist", err)
	}
}

func TestPrepareCodexHomeWritesSelectedAuthWithoutTouchingRealAuth(t *testing.T) {
	tempDir := t.TempDir()
	realHome := filepath.Join(tempDir, "real-codex")
	t.Setenv("CODEX_HOME", realHome)
	if err := os.MkdirAll(realHome, 0700); err != nil {
		t.Fatalf("MkdirAll(realHome) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(realHome, "config.toml"), []byte("model = \"gpt-5.5\"\n"), 0600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(realHome, "auth.json"), []byte(`{"tokens":{"access_token":"real"}}`), 0600); err != nil {
		t.Fatalf("WriteFile(real auth) error = %v", err)
	}

	home, cleanup, err := prepareCodexHome(accounts.Account{
		Alias:        "work",
		AccessToken:  "token-work",
		RefreshToken: "refresh-work",
		IDToken:      `{"email":"work@example.com"}`,
		AccountID:    "acct-work",
	})
	if err != nil {
		t.Fatalf("prepareCodexHome() error = %v", err)
	}
	defer cleanup()

	var auth codexauth.File
	bytes, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(temp auth) error = %v", err)
	}
	if err := json.Unmarshal(bytes, &auth); err != nil {
		t.Fatalf("Unmarshal(temp auth) error = %v", err)
	}
	if auth.Tokens.AccessToken != "token-work" {
		t.Fatalf("temp auth access token = %q, want token-work", auth.Tokens.AccessToken)
	}
	if auth.Tokens.AccountID != "acct-work" {
		t.Fatalf("temp auth account id = %q, want acct-work", auth.Tokens.AccountID)
	}
	configTarget, err := os.Readlink(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("Readlink(config) error = %v", err)
	}
	if configTarget != filepath.Join(realHome, "config.toml") {
		t.Fatalf("config symlink = %q, want real config", configTarget)
	}
	realAuth, err := os.ReadFile(filepath.Join(realHome, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(real auth) error = %v", err)
	}
	if string(realAuth) != `{"tokens":{"access_token":"real"}}` {
		t.Fatalf("real auth = %q, want unchanged", string(realAuth))
	}
}
