package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gabewillen/codextra/internal/codexauth"
	"github.com/gabewillen/codextra/internal/proxy"
)

const proxyStateVersion = 12
const defaultProxyLogMaxBytes int64 = 1 << 20
const defaultProxyIdleGrace = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "codextra:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 && os.Args[1] == "login" {
		return runLogin(ctx, os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "serve-proxy" {
		return runProxyServer(ctx)
	}

	accountAlias, userArgs, err := parseCodextraArgs(os.Args[1:])
	if err != nil {
		return err
	}
	var selectedAccount *accounts.Account
	if accountAlias != "" {
		account, err := activateAccount(accountAlias)
		if err != nil {
			return err
		}
		selectedAccount = &account
	}

	proxyURL, err := ensureProxy()
	if err != nil {
		return err
	}
	client, err := attachProxyClient(ctx, proxyURL)
	if err != nil {
		return err
	}
	defer client.Close()

	codexArgs := codexArgs(proxyURL, userArgs)
	cmd := exec.CommandContext(ctx, getenv("CODEXTRA_CODEX_BIN", "codex"), codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	codexHome := ""
	if selectedAccount != nil {
		home, cleanup, err := prepareCodexHome(*selectedAccount)
		if err != nil {
			return err
		}
		defer cleanup()
		codexHome = home
	}
	cmd.Env = codexEnv(os.Environ(), proxyURL, codexHome)

	log.Printf("using proxy %s", proxyDisplayURL(proxyURL))
	return cmd.Run()
}

func runProxyServer(ctx context.Context) error {
	storePath, err := defaultStorePath()
	if err != nil {
		return err
	}
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		return err
	}

	upstream := getenv("CODEXTRA_UPSTREAM", "https://chatgpt.com")
	apiUpstream := getenv("CODEXTRA_API_UPSTREAM", "https://api.openai.com")
	logger, logCloser, err := proxyLogger()
	if err != nil {
		return err
	}
	defer logCloser()

	addr := getenv("CODEXTRA_PROXY_ADDR", "")
	if addr == "" {
		addr = reusableProxyAddr()
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil && os.Getenv("CODEXTRA_PROXY_ADDR") == "" && addr != "127.0.0.1:0" {
		logger.Warn("proxy_reuse_addr_failed", "addr", addr, "error", err)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return fmt.Errorf("listen proxy: %w", err)
	}
	defer listener.Close()

	server, err := proxy.New(proxy.Config{
		Upstream:    upstream,
		APIUpstream: apiUpstream,
		Store:       store,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	routePrefix, err := reusableRoutePrefix()
	if err != nil {
		return err
	}
	lifecycle := newProxyLifecycle(server.Handler, logger, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	})
	server.Handler = newRoutePrefixHandler(routePrefix, lifecycle)

	listenURL := "http://" + listener.Addr().String()
	proxyURL := listenURL + routePrefix
	if err := writeProxyState(proxyState{
		URL:         proxyURL,
		PID:         os.Getpid(),
		Upstream:    upstream,
		APIUpstream: apiUpstream,
		Version:     proxyStateVersion,
	}); err != nil {
		return err
	}
	logger.Info("proxy_listening", "url", listenURL, "upstream", upstream, "api_upstream", apiUpstream, "store", storePath)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	lifecycle.scheduleIdleShutdown()

	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type proxyState struct {
	URL         string `json:"url"`
	PID         int    `json:"pid"`
	Upstream    string `json:"upstream"`
	APIUpstream string `json:"api_upstream,omitempty"`
	Version     int    `json:"version,omitempty"`
}

func ensureProxy() (string, error) {
	if state, err := readProxyState(); err == nil && healthy(state.URL) {
		if state.Version == proxyStateVersion {
			return state.URL, nil
		}
		stopProxy(state.PID)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find codextra executable: %w", err)
	}
	logPath, err := proxyLogPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return "", fmt.Errorf("create proxy log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("open proxy log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "serve-proxy")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start proxy: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return "", fmt.Errorf("release proxy process: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := readProxyState()
		if err == nil && healthy(state.URL) {
			return state.URL, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("proxy did not become healthy; see %s", logPath)
}

func stopProxy(pid int) {
	if pid <= 0 {
		return
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Signal(syscall.SIGTERM)
	}
}

func proxyLogger() (*slog.Logger, func() error, error) {
	logPath, err := proxyLogPath()
	if err != nil {
		return nil, nil, err
	}
	writer, err := newCappedLogWriter(logPath, proxyLogMaxBytes())
	if err != nil {
		return nil, nil, err
	}
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(handler), writer.Close, nil
}

func proxyLogMaxBytes() int64 {
	value := os.Getenv("CODEXTRA_PROXY_LOG_MAX_BYTES")
	if value == "" {
		return defaultProxyLogMaxBytes
	}
	maxBytes, err := strconv.ParseInt(value, 10, 64)
	if err != nil || maxBytes <= 0 {
		return defaultProxyLogMaxBytes
	}
	return maxBytes
}

func proxyIdleGrace() time.Duration {
	value := os.Getenv("CODEXTRA_PROXY_IDLE_GRACE_SECONDS")
	if value == "" {
		return defaultProxyIdleGrace
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return defaultProxyIdleGrace
	}
	return time.Duration(seconds) * time.Second
}

func randomRoutePrefix() (string, error) {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate proxy route token: %w", err)
	}
	return "/__codextra/" + hex.EncodeToString(bytes[:]), nil
}

func reusableProxyAddr() string {
	state, err := readProxyState()
	if err != nil || state.URL == "" {
		return "127.0.0.1:0"
	}
	parsed, err := url.Parse(state.URL)
	if err != nil || parsed.Host == "" {
		return "127.0.0.1:0"
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || port == "" {
		return "127.0.0.1:0"
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "127.0.0.1:0"
	}
	return net.JoinHostPort(host, port)
}

func reusableRoutePrefix() (string, error) {
	state, err := readProxyState()
	if err == nil {
		if prefix, ok := routePrefixFromProxyURL(state.URL); ok {
			return prefix, nil
		}
	}
	return randomRoutePrefix()
}

func routePrefixFromProxyURL(proxyURL string) (string, bool) {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "", false
	}
	prefix := strings.TrimRight(parsed.EscapedPath(), "/")
	if !strings.HasPrefix(prefix, "/__codextra/") {
		return "", false
	}
	token := strings.TrimPrefix(prefix, "/__codextra/")
	if len(token) != 48 {
		return "", false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", false
	}
	return prefix, true
}

func proxyDisplayURL(proxyURL string) string {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

type routePrefixHandler struct {
	prefix string
	next   http.Handler
}

func newRoutePrefixHandler(prefix string, next http.Handler) http.Handler {
	return routePrefixHandler{prefix: strings.TrimRight(prefix, "/"), next: next}
}

func (h routePrefixHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	trimmedPath, ok := strings.CutPrefix(r.URL.Path, h.prefix)
	if !ok || (trimmedPath != "" && !strings.HasPrefix(trimmedPath, "/")) {
		http.NotFound(w, r)
		return
	}
	if trimmedPath == "" {
		trimmedPath = "/"
	}
	cloned := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = trimmedPath
	urlCopy.RawPath = ""
	cloned.URL = &urlCopy
	h.next.ServeHTTP(w, cloned)
}

type proxyClient struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func attachProxyClient(ctx context.Context, proxyURL string) (*proxyClient, error) {
	clientCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(clientCtx, http.MethodPost, strings.TrimRight(proxyURL, "/")+"/__codextra/client", nil)
	if err != nil {
		cancel()
		return nil, err
	}

	ready := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			ready <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			ready <- fmt.Errorf("proxy client attach returned %s", resp.Status)
			return
		}
		ready <- nil
		_, _ = io.Copy(io.Discard, resp.Body)
	}()

	select {
	case err := <-ready:
		if err != nil {
			cancel()
			<-done
			return nil, fmt.Errorf("attach proxy client: %w", err)
		}
		return &proxyClient{cancel: cancel, done: done}, nil
	case <-ctx.Done():
		cancel()
		<-done
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		return nil, errors.New("attach proxy client: timeout")
	}
}

func (c *proxyClient) Close() {
	c.cancel()
	<-c.done
}

type proxyLifecycle struct {
	next     http.Handler
	logger   *slog.Logger
	shutdown func()
	grace    time.Duration
	mu       sync.Mutex
	clients  int
	timer    *time.Timer
	closed   bool
}

func newProxyLifecycle(next http.Handler, logger *slog.Logger, shutdown func()) *proxyLifecycle {
	return &proxyLifecycle{
		next:     next,
		logger:   logger,
		shutdown: shutdown,
		grace:    proxyIdleGrace(),
	}
}

func (l *proxyLifecycle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/__codextra/client" {
		l.next.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	l.attach()
	defer l.detach()

	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(": connected\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	<-r.Context().Done()
}

func (l *proxyLifecycle) attach() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.clients++
	l.logger.Info("proxy_client_attached", "clients", l.clients)
}

func (l *proxyLifecycle) detach() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clients > 0 {
		l.clients--
	}
	l.logger.Info("proxy_client_detached", "clients", l.clients)
	if l.clients == 0 {
		l.scheduleIdleShutdownLocked()
	}
}

func (l *proxyLifecycle) scheduleIdleShutdown() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clients == 0 {
		l.scheduleIdleShutdownLocked()
	}
}

func (l *proxyLifecycle) scheduleIdleShutdownLocked() {
	if l.closed {
		return
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	l.timer = time.AfterFunc(l.grace, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.clients != 0 || l.closed {
			return
		}
		l.closed = true
		l.logger.Info("proxy_idle_shutdown")
		go l.shutdown()
	})
}

func healthy(proxyURL string) bool {
	if proxyURL == "" {
		return false
	}
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(proxyURL + "/__codextra/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func readProxyState() (proxyState, error) {
	var state proxyState
	path, err := proxyStatePath()
	if err != nil {
		return state, err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(bytes, &state); err != nil {
		return state, err
	}
	return state, nil
}

func writeProxyState(state proxyState) error {
	path, err := proxyStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create codextra state directory: %w", err)
	}
	bytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode proxy state: %w", err)
	}
	return os.WriteFile(path, append(bytes, '\n'), 0600)
}

func proxyStatePath() (string, error) {
	if path := os.Getenv("CODEXTRA_PROXY_STATE"); path != "" {
		return path, nil
	}
	dir, err := codextraDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "proxy.json"), nil
}

func proxyLogPath() (string, error) {
	dir, err := codextraDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "proxy.log"), nil
}

func defaultStorePath() (string, error) {
	if path := os.Getenv("CODEXTRA_STORE"); path != "" {
		return path, nil
	}
	dir, err := codextraDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "accounts.json"), nil
}

func activateAccount(alias string) (accounts.Account, error) {
	storePath, err := defaultStorePath()
	if err != nil {
		return accounts.Account{}, err
	}
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		return accounts.Account{}, err
	}
	if err := store.SetActive(alias); err != nil {
		return accounts.Account{}, err
	}
	account, ok := store.Get(alias)
	if !ok {
		return accounts.Account{}, fmt.Errorf("account %q not found", alias)
	}
	return account, nil
}

func codextraDir() (string, error) {
	if dir := os.Getenv("CODEXTRA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".codextra"), nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func codexArgs(proxyURL string, userArgs []string) []string {
	args := make([]string, 0, len(userArgs)+6)
	args = append(args, "-c", "chatgpt_base_url="+codexChatGPTBaseURL(proxyURL))
	args = append(args, "-c", "model_providers.codextra="+codexModelProviderConfig(proxyURL))
	args = append(args, "-c", `model_provider="codextra"`)
	args = append(args, userArgs...)
	return args
}

func codexChatGPTBaseURL(proxyURL string) string {
	return strings.TrimRight(proxyURL, "/") + "/backend-api"
}

func codexChatGPTCodexBaseURL(proxyURL string) string {
	return codexChatGPTBaseURL(proxyURL) + "/codex"
}

func codexModelProviderConfig(proxyURL string) string {
	return fmt.Sprintf(
		`{ name="Codextra", base_url="%s", wire_api="responses", requires_openai_auth=true }`,
		codexChatGPTCodexBaseURL(proxyURL),
	)
}

func prepareCodexHome(account accounts.Account) (string, func(), error) {
	dir, err := os.MkdirTemp("", "codextra-codex-home-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary codex home: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	if err := mirrorCodexHome(dir); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := codexauth.Write(filepath.Join(dir, "auth.json"), account); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

func mirrorCodexHome(dst string) error {
	src, err := realCodexHome()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read codex home: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "auth.json" {
			continue
		}
		if err := os.Symlink(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			return fmt.Errorf("mirror codex home entry %q: %w", name, err)
		}
	}
	return nil
}

func realCodexHome() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func codexEnv(base []string, proxyURL string, codexHome string) []string {
	env := make([]string, 0, len(base)+2)
	for _, value := range base {
		if strings.HasPrefix(value, "CODEXTRA_PROXY_URL=") {
			continue
		}
		if codexHome != "" && strings.HasPrefix(value, "CODEX_HOME=") {
			continue
		}
		env = append(env, value)
	}
	env = append(env, "CODEXTRA_PROXY_URL="+proxyURL)
	if codexHome != "" {
		env = append(env, "CODEX_HOME="+codexHome)
	}
	return env
}

func parseCodextraArgs(args []string) (string, []string, error) {
	var account string
	pass := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pass = append(pass, args[i:]...)
			break
		}
		if arg == "--account" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--account requires an alias")
			}
			account = args[i+1]
			i++
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--account="); ok {
			if value == "" {
				return "", nil, fmt.Errorf("--account requires an alias")
			}
			account = value
			continue
		}
		pass = append(pass, arg)
	}
	return account, pass, nil
}
