package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
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

func TestCodexDesktopArgsPrefixesAppCommand(t *testing.T) {
	t.Parallel()

	base := []string{"-c", "chatgpt_base_url=http://proxy/backend-api", "."}
	got := codexDesktopArgs(base)
	want := []string{"app", "-c", "chatgpt_base_url=http://proxy/backend-api", "."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexDesktopArgs() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(base, []string{"-c", "chatgpt_base_url=http://proxy/backend-api", "."}) {
		t.Fatalf("codexDesktopArgs mutated base: %#v", base)
	}
}

func TestCodexDesktopShouldKeepAlive(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "workspace", args: []string{"."}, want: true},
		{name: "no args", args: nil, want: true},
		{name: "help", args: []string{"--help"}, want: false},
		{name: "short help", args: []string{"-h"}, want: false},
		{name: "literal help path", args: []string{"--", "--help"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := codexDesktopShouldKeepAlive(tc.args); got != tc.want {
				t.Fatalf("codexDesktopShouldKeepAlive(%#v) = %t, want %t", tc.args, got, tc.want)
			}
		})
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
	got := codexEnv(base, "http://127.0.0.1:9999")
	want := []string{"A=1", "B=two", "CODEXTRA_PROXY_URL=http://127.0.0.1:9999"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexEnv() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(base, []string{"A=1", "B=two"}) {
		t.Fatalf("codexEnv mutated base: %#v", base)
	}
}

func TestCodexEnvReplacesProxyURLAndPreservesCodexHome(t *testing.T) {
	t.Parallel()

	base := []string{"CODEX_HOME=/real", "CODEXTRA_PROXY_URL=http://old", "A=1"}
	got := codexEnv(base, "http://127.0.0.1:9999")
	want := []string{"CODEX_HOME=/real", "A=1", "CODEXTRA_PROXY_URL=http://127.0.0.1:9999"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexEnv() = %#v, want %#v", got, want)
	}
}

func TestParseCodextraArgsConsumesAccountFlag(t *testing.T) {
	t.Parallel()

	options, pass, err := parseCodextraArgs([]string{"--account", "work", "--model", "gpt-5.4"})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if options.accountAlias != "work" {
		t.Fatalf("account = %q, want work", options.accountAlias)
	}
	if options.desktop {
		t.Fatal("desktop = true, want false")
	}
	want := []string{"--model", "gpt-5.4"}
	if !reflect.DeepEqual(pass, want) {
		t.Fatalf("pass = %#v, want %#v", pass, want)
	}
}

func TestParseCodextraArgsConsumesAccountEqualsFlag(t *testing.T) {
	t.Parallel()

	options, pass, err := parseCodextraArgs([]string{"--model", "gpt-5.4", "--account=personal", "prompt"})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if options.accountAlias != "personal" {
		t.Fatalf("account = %q, want personal", options.accountAlias)
	}
	want := []string{"--model", "gpt-5.4", "prompt"}
	if !reflect.DeepEqual(pass, want) {
		t.Fatalf("pass = %#v, want %#v", pass, want)
	}
}

func TestParseCodextraArgsLeavesArgumentsAfterDashDashUntouched(t *testing.T) {
	t.Parallel()

	options, pass, err := parseCodextraArgs([]string{"--account=work", "--", "--account", "literal"})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if options.accountAlias != "work" {
		t.Fatalf("account = %q, want work", options.accountAlias)
	}
	want := []string{"--", "--account", "literal"}
	if !reflect.DeepEqual(pass, want) {
		t.Fatalf("pass = %#v, want %#v", pass, want)
	}
}

func TestParseCodextraArgsConsumesDesktopFlag(t *testing.T) {
	t.Parallel()

	options, pass, err := parseCodextraArgs([]string{"--desktop", "--account=work", "."})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if !options.desktop {
		t.Fatal("desktop = false, want true")
	}
	if options.accountAlias != "work" {
		t.Fatalf("account = %q, want work", options.accountAlias)
	}
	want := []string{"."}
	if !reflect.DeepEqual(pass, want) {
		t.Fatalf("pass = %#v, want %#v", pass, want)
	}
}

func TestParseCodextraArgsLeavesDesktopAfterDashDashUntouched(t *testing.T) {
	t.Parallel()

	options, pass, err := parseCodextraArgs([]string{"--account=work", "--", "--desktop"})
	if err != nil {
		t.Fatalf("parseCodextraArgs() error = %v", err)
	}
	if options.desktop {
		t.Fatal("desktop = true, want false")
	}
	want := []string{"--", "--desktop"}
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

func TestProxyActivityHandlerTracksActiveRequests(t *testing.T) {
	tracker := newProxyActivityTracker()
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := newProxyActivityHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}), tracker)
	server := httptest.NewServer(handler)
	defer server.Close()

	go func() {
		resp, err := http.Get(server.URL + "/backend-api/messages")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started
	active, err := proxyActiveRequests(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("proxyActiveRequests() error = %v", err)
	}
	if active != 1 {
		t.Fatalf("active requests = %d, want 1", active)
	}

	close(release)

	active, err = proxyActiveRequests(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("proxyActiveRequests() after done error = %v", err)
	}
	if active != 0 {
		t.Fatalf("active requests = %d, want 0", active)
	}
}

func TestPrefixedProxyHealthTracksActiveRequests(t *testing.T) {
	tracker := newProxyActivityTracker()
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	const prefix = "/__codextra/0123456789abcdef0123456789abcdef0123456789abcdef"
	inner := newProxyActivityHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}), tracker)
	handler := newRoutePrefixHandler(prefix, inner)
	server := httptest.NewServer(handler)
	defer server.Close()

	// Clients reach control endpoints at proxyURL, which already carries the prefix.
	proxyURL := server.URL + prefix

	go func() {
		resp, err := http.Get(proxyURL + "/backend-api/messages")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started
	active, err := proxyActiveRequests(context.Background(), proxyURL)
	if err != nil {
		t.Fatalf("proxyActiveRequests() error = %v", err)
	}
	if active != 1 {
		t.Fatalf("active requests = %d, want 1", active)
	}

	close(release)

	active, err = proxyActiveRequests(context.Background(), proxyURL)
	if err != nil {
		t.Fatalf("proxyActiveRequests() after done error = %v", err)
	}
	if active != 0 {
		t.Fatalf("active requests = %d, want 0", active)
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

func TestKeepProxyAliveReattachesDroppedClientStream(t *testing.T) {
	var attaches atomic.Int32
	releaseSecond := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__codextra/client" {
			http.NotFound(w, r)
			return
		}
		count := attaches.Add(1)
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(": connected\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if count == 1 {
			return
		}
		<-releaseSecond
	}))
	defer server.Close()

	keeper, err := keepProxyAlive(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("keepProxyAlive() error = %v", err)
	}
	defer close(releaseSecond)
	defer keeper.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if attaches.Load() >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("client stream attaches = %d, want at least 2", attaches.Load())
}

func TestFetchAccountUsageFlagsUnauthorized(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	_, err := fetchAccountUsage(context.Background(), server.URL)
	if err == nil {
		t.Fatal("fetchAccountUsage() error = nil, want unauthorized")
	}
	// The sentinel lets the tray back off instead of hammering the proxy, which
	// would otherwise force a token refresh on every poll and burn the account's
	// single-use refresh token.
	if !errors.Is(err, errUsageUnauthorized) {
		t.Fatalf("fetchAccountUsage() error = %v, want errUsageUnauthorized", err)
	}
}

func TestFetchAccountUsageReturnsCodexWindows(t *testing.T) {
	t.Parallel()

	// Mirrors the real wham/usage shape: primary (5h) and secondary (weekly).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":111},"secondary_window":{"used_percent":80,"limit_window_seconds":604800,"reset_at":222}}}`))
	}))
	defer server.Close()

	windows, err := fetchAccountUsage(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetchAccountUsage() error = %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want 2 (5h + weekly)", windows)
	}
	if windows[0].Label != "5h" || windows[0].Percent != 12 || windows[0].ResetAt != 111 {
		t.Fatalf("primary window = %#v, want {5h 12 111}", windows[0])
	}
	if windows[1].Label != "Weekly" || windows[1].Percent != 80 || windows[1].ResetAt != 222 {
		t.Fatalf("secondary window = %#v, want {Weekly 80 222}", windows[1])
	}
}

func TestUsageWindowLabel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		seconds int64
		want    string
	}{
		{18000, "5h"},
		{604800, "Weekly"},
		{86400, "Daily"},
		{0, "Usage"},
	} {
		if got := usageWindowLabel(tc.seconds); got != tc.want {
			t.Fatalf("usageWindowLabel(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
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

func mainFakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(claims) error = %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".signature"
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
func TestRunLoginTagImportsCurrentCodexAuth(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"tokens":{"access_token":"token-work","refresh_token":"refresh-work","account_id":"acct-work"}}`), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	if err := runLogin(context.Background(), []string{"--tag"}); err != nil {
		t.Fatalf("runLogin(--tag) error = %v", err)
	}

	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	account, ok := store.Get("acct-work")
	if !ok {
		t.Fatal("tagged account missing")
	}
	if account.AccessToken != "token-work" {
		t.Fatalf("AccessToken = %q, want token-work", account.AccessToken)
	}
	if account.RefreshToken != "refresh-work" {
		t.Fatalf("RefreshToken = %q, want refresh-work", account.RefreshToken)
	}
	if account.AccountID != "acct-work" {
		t.Fatalf("AccountID = %q, want acct-work", account.AccountID)
	}
	if store.Data.ActiveAlias != "acct-work" {
		t.Fatalf("ActiveAlias = %q, want acct-work", store.Data.ActiveAlias)
	}
}

func TestImportCurrentAuthUpdatesExistingAliasWithoutClearingState(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	oldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com"})
	newAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com", "chatgpt_plan_type": "pro"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	resetAt := time.Now().Add(time.Hour).Unix()
	if err := store.Upsert(accounts.Account{
		Alias:         "work",
		AccessToken:   oldAccess,
		RefreshToken:  "refresh-work-old",
		AccountID:     "acct-work",
		Email:         "work@example.com",
		DisabledUntil: map[string]int64{"codex_weekly": resetAt},
		Usage:         []accounts.UsageWindow{{Label: "Weekly", Percent: 99, ResetAt: resetAt}},
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  newAccess,
			"refresh_token": "refresh-work-new",
			"account_id":    "acct-work",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	if err := importCurrentAuth("work", false); err != nil {
		t.Fatalf("importCurrentAuth(work) error = %v", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	account, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if account.AccessToken != newAccess {
		t.Fatalf("AccessToken not updated")
	}
	if account.RefreshToken != "refresh-work-new" {
		t.Fatalf("RefreshToken = %q, want refresh-work-new", account.RefreshToken)
	}
	if got := account.DisabledUntil["codex_weekly"]; got != resetAt {
		t.Fatalf("DisabledUntil[codex_weekly] = %d, want %d", got, resetAt)
	}
	if len(account.Usage) != 1 || account.Usage[0].Percent != 99 {
		t.Fatalf("Usage = %#v, want preserved weekly usage", account.Usage)
	}
}

func TestImportCurrentAuthClearsMissingRefreshToken(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	oldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com"})
	newAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  oldAccess,
		RefreshToken: "refresh-stale",
		AccountID:    "acct-work",
		Email:        "work@example.com",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token": newAccess,
			"account_id":   "acct-work",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	if err := importCurrentAuth("work", false); err != nil {
		t.Fatalf("importCurrentAuth(work) error = %v", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	account, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if account.AccessToken != newAccess {
		t.Fatalf("AccessToken not updated")
	}
	if account.RefreshToken != "" {
		t.Fatalf("RefreshToken = %q, want cleared when imported auth omits refresh_token", account.RefreshToken)
	}
}

func TestImportCurrentAuthDoesNotOverwriteAliasWithDifferentAccount(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	workAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com"})
	personalOldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com"})
	personalNewAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  workAccess,
		RefreshToken: "refresh-work-old",
		AccountID:    "acct-work",
		Email:        "work@example.com",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "personal",
		AccessToken:  personalOldAccess,
		RefreshToken: "refresh-personal-old",
		AccountID:    "acct-personal",
		Email:        "personal@example.com",
	}); err != nil {
		t.Fatalf("Upsert(personal) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  personalNewAccess,
			"refresh_token": "refresh-personal-new",
			"account_id":    "acct-personal",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("work", false)
	if err == nil || !strings.Contains(err.Error(), `existing alias "personal" instead of "work"`) {
		t.Fatalf("importCurrentAuth(work) error = %v, want different-account protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.RefreshToken != "refresh-work-old" || work.AccessToken != workAccess {
		t.Fatalf("work account was overwritten: %#v", work)
	}
	personal, ok := reloaded.Get("personal")
	if !ok {
		t.Fatal("personal account missing")
	}
	if personal.RefreshToken != "refresh-personal-new" || personal.AccessToken != personalNewAccess {
		t.Fatalf("personal account not updated with returned credentials: %#v", personal)
	}
}

func TestImportCurrentAuthRejectsIdentitylessAuthForIdentifiedAlias(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	workAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  workAccess,
		RefreshToken: "refresh-work-old",
		AccountID:    "acct-work",
		Email:        "work@example.com",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  "opaque-access-token",
			"refresh_token": "refresh-opaque",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("work", false)
	if err == nil || !strings.Contains(err.Error(), `without account identity`) {
		t.Fatalf("importCurrentAuth(work) error = %v, want identityless protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.AccessToken != workAccess || work.RefreshToken != "refresh-work-old" {
		t.Fatalf("work account changed after identityless import: %#v", work)
	}
}

func TestImportCurrentAuthRejectsAmbiguousExistingAliasIdentity(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	oldAccess := mainFakeJWT(t, map[string]any{"scope": "old"})
	newAccess := mainFakeJWT(t, map[string]any{"email": "work@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  oldAccess,
		RefreshToken: "refresh-work-old",
		AccountID:    "acct-work",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  newAccess,
			"refresh_token": "refresh-work-new",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("work", false)
	if err == nil || !strings.Contains(err.Error(), `ambiguous account identity`) {
		t.Fatalf("importCurrentAuth(work) error = %v, want ambiguous identity protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.AccessToken != oldAccess || work.RefreshToken != "refresh-work-old" || work.AccountID != "acct-work" || work.Email != "" {
		t.Fatalf("work account changed after ambiguous import: %#v", work)
	}
}

func TestImportCurrentAuthRejectsReverseAmbiguousExistingAliasIdentity(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	oldAccess := mainFakeJWT(t, map[string]any{"scope": "old"})
	newAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  oldAccess,
		RefreshToken: "refresh-work-old",
		Email:        "work@example.com",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  newAccess,
			"refresh_token": "refresh-work-new",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("work", false)
	if err == nil || !strings.Contains(err.Error(), `ambiguous account identity`) {
		t.Fatalf("importCurrentAuth(work) error = %v, want ambiguous identity protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.AccessToken != oldAccess || work.RefreshToken != "refresh-work-old" || work.AccountID != "" || work.Email != "work@example.com" {
		t.Fatalf("work account changed after reverse ambiguous import: %#v", work)
	}
}

func TestImportCurrentAuthDoesNotDuplicateExistingAccountUnderNewAlias(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	workOldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com"})
	workNewAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  workOldAccess,
		RefreshToken: "refresh-work-old",
		AccountID:    "acct-work",
		Email:        "work@example.com",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  workNewAccess,
			"refresh_token": "refresh-work-new",
			"account_id":    "acct-work",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("new-work", false)
	if err == nil || !strings.Contains(err.Error(), `existing alias "work" instead of new alias "new-work"`) {
		t.Fatalf("importCurrentAuth(new-work) error = %v, want duplicate-account protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	if _, ok := reloaded.Get("new-work"); ok {
		t.Fatal("new-work alias was created for an existing account")
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.RefreshToken != "refresh-work-new" || work.AccessToken != workNewAccess {
		t.Fatalf("work account not updated with returned credentials: %#v", work)
	}
}

func TestImportCurrentAuthMatchesLegacyStoredTokenClaims(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	workOldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com"})
	workNewAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  workOldAccess,
		RefreshToken: "refresh-work-old",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  workNewAccess,
			"refresh_token": "refresh-work-new",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("new-work", false)
	if err == nil || !strings.Contains(err.Error(), `existing alias "work" instead of new alias "new-work"`) {
		t.Fatalf("importCurrentAuth(new-work) error = %v, want legacy-token duplicate protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	if _, ok := reloaded.Get("new-work"); ok {
		t.Fatal("new-work alias was created for legacy stored token identity")
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.AccessToken != workNewAccess || work.RefreshToken != "refresh-work-new" {
		t.Fatalf("work account not updated with returned credentials: %#v", work)
	}
	if work.AccountID != "acct-work" || work.Email != "work@example.com" {
		t.Fatalf("work identity not filled from imported token: %#v", work)
	}
}

func TestImportCurrentAuthRejectsIdentitylessNewAliasWhenRegistryExists(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	personalAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "personal",
		AccessToken:  personalAccess,
		RefreshToken: "refresh-personal",
		AccountID:    "acct-personal",
		Email:        "personal@example.com",
	}); err != nil {
		t.Fatalf("Upsert(personal) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  "opaque-access-token",
			"refresh_token": "refresh-opaque",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("opaque", false)
	if err == nil || !strings.Contains(err.Error(), `without account identity`) {
		t.Fatalf("importCurrentAuth(opaque) error = %v, want identityless new-alias protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	if _, ok := reloaded.Get("opaque"); ok {
		t.Fatal("opaque alias was created from identityless credentials")
	}
	personal, ok := reloaded.Get("personal")
	if !ok {
		t.Fatal("personal account missing")
	}
	if personal.AccessToken != personalAccess || personal.RefreshToken != "refresh-personal" {
		t.Fatalf("personal account changed: %#v", personal)
	}
}

func TestImportCurrentAuthExistingIdentitylessAliasUpdatesMatchingAlias(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	personalOldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com"})
	personalNewAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		RefreshToken: "refresh-work-old",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "personal",
		AccessToken:  personalOldAccess,
		RefreshToken: "refresh-personal-old",
		AccountID:    "acct-personal",
		Email:        "personal@example.com",
	}); err != nil {
		t.Fatalf("Upsert(personal) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  personalNewAccess,
			"refresh_token": "refresh-personal-new",
			"account_id":    "acct-personal",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("work", false)
	if err == nil || !strings.Contains(err.Error(), `existing alias "personal" instead of "work"`) {
		t.Fatalf("importCurrentAuth(work) error = %v, want matching-alias protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.AccessToken != "" || work.RefreshToken != "refresh-work-old" || work.AccountID != "" || work.Email != "" {
		t.Fatalf("identityless work alias changed: %#v", work)
	}
	personal, ok := reloaded.Get("personal")
	if !ok {
		t.Fatal("personal account missing")
	}
	if personal.AccessToken != personalNewAccess || personal.RefreshToken != "refresh-personal-new" {
		t.Fatalf("personal account not updated with returned credentials: %#v", personal)
	}
}

func TestImportCurrentAuthRejectsIdentitylessAuthForIdentitylessAliasWhenOthersExist(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	personalAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		RefreshToken: "refresh-work-old",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "personal",
		AccessToken:  personalAccess,
		RefreshToken: "refresh-personal-old",
		AccountID:    "acct-personal",
		Email:        "personal@example.com",
	}); err != nil {
		t.Fatalf("Upsert(personal) error = %v", err)
	}
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  "opaque-access-token",
			"refresh_token": "refresh-opaque",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, auth), 0600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	err = importCurrentAuth("work", false)
	if err == nil || !strings.Contains(err.Error(), `without account identity`) {
		t.Fatalf("importCurrentAuth(work) error = %v, want identityless protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.AccessToken != "" || work.RefreshToken != "refresh-work-old" || work.AccountID != "" || work.Email != "" {
		t.Fatalf("identityless work alias changed: %#v", work)
	}
	personal, ok := reloaded.Get("personal")
	if !ok {
		t.Fatal("personal account missing")
	}
	if personal.AccessToken != personalAccess || personal.RefreshToken != "refresh-personal-old" {
		t.Fatalf("personal account changed: %#v", personal)
	}
}

func TestLoginAccountFromTraySeedsTargetAuthBeforeCodexLogin(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	personalAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com"})
	workOldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com"})
	workNewAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-work", "email": "work@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "personal",
		AccessToken:  personalAccess,
		RefreshToken: "refresh-personal-old",
		AccountID:    "acct-personal",
		Email:        "personal@example.com",
	}); err != nil {
		t.Fatalf("Upsert(personal) error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		AccessToken:  workOldAccess,
		RefreshToken: "refresh-work-old",
		AccountID:    "acct-work",
		Email:        "work@example.com",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	initialAuth := map[string]any{
		"tokens": map[string]any{
			"access_token":  personalAccess,
			"refresh_token": "refresh-personal-old",
			"account_id":    "acct-personal",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, initialAuth), 0600); err != nil {
		t.Fatalf("WriteFile(initial auth) error = %v", err)
	}

	newAuth := string(mustJSON(t, map[string]any{
		"tokens": map[string]any{
			"access_token":  workNewAccess,
			"refresh_token": "refresh-work-new",
			"account_id":    "acct-work",
		},
	}))
	fakeCodex := filepath.Join(tempDir, "codex-login")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "${1:-}" != "login" ]; then
  echo "unexpected command: ${1:-}" >&2
  exit 12
fi
if ! grep -Fq %q "$CODEX_HOME/auth.json"; then
  echo "target account was not seeded before login" >&2
  exit 13
fi
cat > "$CODEX_HOME/auth.json" <<'JSON'
%s
JSON
`, workOldAccess, newAuth)
	if err := os.WriteFile(fakeCodex, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(fake codex) error = %v", err)
	}
	t.Setenv("CODEXTRA_CODEX_BIN", fakeCodex)

	if err := loginAccountFromTray(context.Background(), "work"); err != nil {
		t.Fatalf("loginAccountFromTray(work) error = %v", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.RefreshToken != "refresh-work-new" || work.AccessToken != workNewAccess {
		t.Fatalf("work account not updated after tray reauth: %#v", work)
	}
	personal, ok := reloaded.Get("personal")
	if !ok {
		t.Fatal("personal account missing")
	}
	if personal.RefreshToken != "refresh-personal-old" || personal.AccessToken != personalAccess {
		t.Fatalf("personal account changed during work reauth: %#v", personal)
	}
	globalAuth, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(global auth) error = %v", err)
	}
	if !strings.Contains(string(globalAuth), personalAccess) || strings.Contains(string(globalAuth), workNewAccess) {
		t.Fatalf("global auth was not left on the original account: %s", globalAuth)
	}
}

func TestLoginAccountFromTrayTokenlessAliasUsesIsolatedEmptyAuth(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "codextra", "accounts.json")
	codexHome := filepath.Join(tempDir, "codex")
	t.Setenv("CODEXTRA_STORE", storePath)
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatalf("MkdirAll(codexHome) error = %v", err)
	}

	personalOldAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com"})
	personalNewAccess := mainFakeJWT(t, map[string]any{"chatgpt_account_id": "acct-personal", "email": "personal@example.com", "token_version": "new"})
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "work",
		RefreshToken: "refresh-work-old",
		AccountID:    "acct-work",
		Email:        "work@example.com",
	}); err != nil {
		t.Fatalf("Upsert(work) error = %v", err)
	}
	if err := store.Upsert(accounts.Account{
		Alias:        "personal",
		AccessToken:  personalOldAccess,
		RefreshToken: "refresh-personal-old",
		AccountID:    "acct-personal",
		Email:        "personal@example.com",
	}); err != nil {
		t.Fatalf("Upsert(personal) error = %v", err)
	}
	initialAuth := map[string]any{
		"tokens": map[string]any{
			"access_token":  personalOldAccess,
			"refresh_token": "refresh-personal-old",
			"account_id":    "acct-personal",
		},
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), mustJSON(t, initialAuth), 0600); err != nil {
		t.Fatalf("WriteFile(initial auth) error = %v", err)
	}

	newAuth := string(mustJSON(t, map[string]any{
		"tokens": map[string]any{
			"access_token":  personalNewAccess,
			"refresh_token": "refresh-personal-new",
			"account_id":    "acct-personal",
		},
	}))
	fakeCodex := filepath.Join(tempDir, "codex-login")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "${1:-}" != "login" ]; then
  echo "unexpected command: ${1:-}" >&2
  exit 12
fi
if [ "$CODEX_HOME" = %q ]; then
  echo "login used the real Codex home" >&2
  exit 13
fi
if [ -e "$CODEX_HOME/auth.json" ]; then
  echo "tokenless account should not seed temp auth" >&2
  exit 14
fi
cat > "$CODEX_HOME/auth.json" <<'JSON'
%s
JSON
`, codexHome, newAuth)
	if err := os.WriteFile(fakeCodex, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(fake codex) error = %v", err)
	}
	t.Setenv("CODEXTRA_CODEX_BIN", fakeCodex)

	err = loginAccountFromTray(context.Background(), "work")
	if err == nil || !strings.Contains(err.Error(), `existing alias "personal" instead of "work"`) {
		t.Fatalf("loginAccountFromTray(work) error = %v, want wrong-account protection", err)
	}

	reloaded, err := accounts.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore(reloaded) error = %v", err)
	}
	work, ok := reloaded.Get("work")
	if !ok {
		t.Fatal("work account missing")
	}
	if work.AccessToken != "" || work.RefreshToken != "refresh-work-old" {
		t.Fatalf("work tokenless account changed: %#v", work)
	}
	personal, ok := reloaded.Get("personal")
	if !ok {
		t.Fatal("personal account missing")
	}
	if personal.RefreshToken != "refresh-personal-new" || personal.AccessToken != personalNewAccess {
		t.Fatalf("personal account not updated with returned credentials: %#v", personal)
	}
	globalAuth, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(global auth) error = %v", err)
	}
	if !strings.Contains(string(globalAuth), personalOldAccess) || strings.Contains(string(globalAuth), personalNewAccess) {
		t.Fatalf("global auth was modified during isolated tokenless reauth: %s", globalAuth)
	}
}

func TestParseLoginArgsConsumesTagFlag(t *testing.T) {
	alias, tagOnly, pass, err := parseLoginArgs([]string{"personal", "--tag"})
	if err != nil {
		t.Fatalf("parseLoginArgs() error = %v", err)
	}
	if alias != "personal" {
		t.Fatalf("alias = %q, want personal", alias)
	}
	if !tagOnly {
		t.Fatal("tagOnly = false, want true")
	}
	if len(pass) != 0 {
		t.Fatalf("pass = %#v, want empty", pass)
	}
}

func TestParseLoginArgsAllowsTagWithoutAlias(t *testing.T) {
	alias, tagOnly, pass, err := parseLoginArgs([]string{"--tag"})
	if err != nil {
		t.Fatalf("parseLoginArgs(--tag) error = %v", err)
	}
	if alias != "" {
		t.Fatalf("alias = %q, want empty", alias)
	}
	if !tagOnly {
		t.Fatal("tagOnly = false, want true")
	}
	if len(pass) != 0 {
		t.Fatalf("pass = %#v, want empty", pass)
	}
}
