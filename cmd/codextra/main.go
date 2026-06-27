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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gabewillen/codextra/internal/codexauth"
	"github.com/gabewillen/codextra/internal/proxy"
)

const proxyStateVersion = 12
const defaultProxyLogMaxBytes int64 = 1 << 20
const defaultProxyIdleGrace = 10 * time.Second
const defaultProxyUpgradeWait = 10 * time.Second

func main() {
	if err := runForever(); err != nil {
		fmt.Fprintln(os.Stderr, "codextra:", err)
		os.Exit(1)
	}
}

var errRestartRequested = errors.New("upgrade restart requested")

// preExitCleanup runs platform cleanup (notably removing the macOS tray status
// item) before a forced os.Exit so a hard shutdown doesn't strand resources that
// the deferred cleanup path would otherwise handle.
var (
	preExitCleanupMu sync.Mutex
	preExitCleanup   func()
)

func registerPreExitCleanup(fn func()) {
	preExitCleanupMu.Lock()
	preExitCleanup = fn
	preExitCleanupMu.Unlock()
}

func forcedExit() {
	preExitCleanupMu.Lock()
	fn := preExitCleanup
	preExitCleanupMu.Unlock()
	if fn != nil {
		fn()
	}
	os.Exit(130)
}

func runForever() error {
	for {
		err := run()
		if !errors.Is(err, errRestartRequested) {
			return err
		}
		log.Printf("restarting to pick up upgraded codextra binary")
		// Re-exec the on-disk binary so the upgrade actually loads the new
		// build; on success this replaces the process and never returns. If it
		// fails, surface the error instead of looping back into run() — the
		// child was already stopped for the restart, so re-running would spawn
		// a fresh codex session the user did not ask for.
		if err := reexecSelf(); err != nil {
			return fmt.Errorf("upgrade re-exec failed: %w", err)
		}
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	shutdownDone := make(chan struct{})
	defer close(shutdownDone)
	go func() {
		<-sigCh
		cancel()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()

		select {
		case <-shutdownDone:
			return
		case <-sigCh:
			forcedExit()
		case <-timer.C:
			forcedExit()
		}
	}()

	if len(os.Args) > 1 && os.Args[1] == "login" {
		return runLogin(ctx, os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "serve-proxy" {
		return runProxyServer(ctx)
	}

	options, userArgs, err := parseCodextraArgs(os.Args[1:])
	if err != nil {
		return err
	}
	if options.accountAlias != "" {
		if _, err := activateAccount(options.accountAlias); err != nil {
			return err
		}
	}

	proxyURL, err := ensureProxy()
	if err != nil {
		return err
	}
	client, err := keepProxyAlive(ctx, proxyURL)
	if err != nil {
		return err
	}
	defer client.Close()

	storePath, err := defaultStorePath()
	if err != nil {
		return err
	}
	stopTray := startTray(ctx, storePath, proxyURL, func(alias string) error {
		_, err := activateAccount(alias)
		if err != nil {
			return err
		}
		return nil
	})
	defer stopTray()

	restartReqs := make(chan struct{}, 1)
	stopRestartWatch := startRestartSignalWatcher(ctx, func() {
		select {
		case restartReqs <- struct{}{}:
		default:
		}
	})
	defer stopRestartWatch()

	restartWait := defaultUpgradeWait()
	restartPending := false
	commandRunning := atomic.Bool{}
	commandRunning.Store(true)
	codexArgs := codexArgs(proxyURL, userArgs)
	if options.desktop {
		codexArgs = codexDesktopArgs(codexArgs)
	}
	cmdCtx, stopCmd := context.WithCancel(ctx)
	defer stopCmd()
	cmd := exec.CommandContext(cmdCtx, getenv("CODEXTRA_CODEX_BIN", "codex"), codexArgs...)
	configureCommandProcess(cmd)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = codexEnv(os.Environ(), proxyURL)

	log.Printf("using proxy %s", proxyDisplayURL(proxyURL))

	keepProxyAliveForDesktop := func(cmdErr error) error {
		if cmdErr != nil || !options.desktop || !codexDesktopShouldKeepAlive(userArgs) {
			return nil
		}
		log.Printf("desktop app launched; press Ctrl+C to stop codextra proxy keepalive")
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-restartReqs:
				// The codex child is already gone, but proxied requests from the
				// detached desktop app may still be in flight; wait for the proxy
				// to drain (or time out) before re-exec, as the other paths do.
				log.Printf("codextra received upgrade signal; waiting for proxy idleness before restart")
				if err := waitForProxyIdle(cmdCtx, proxyURL, restartWait); err != nil {
					// ctx canceled mid-wait — treat as a normal shutdown.
					return nil
				}
				log.Printf("restarting codextra wrapper")
				return errRestartRequested
			}
		}
	}

	var stopCommandOnce sync.Once
	requestCommandStop := func() {
		stopCommandOnce.Do(func() {
			if cmd.Process != nil {
				signalCommandProcess(cmd)
				time.AfterFunc(500*time.Millisecond, func() {
					killCommandProcess(cmd)
				})
			}
			stopCmd()
		})
	}
	go func() {
		<-ctx.Done()
		requestCommandStop()
	}()

	runDone := make(chan error, 1)
	waitCommand := func() error {
		select {
		case err := <-runDone:
			return err
		case <-time.After(2 * time.Second):
			return ctx.Err()
		}
	}
	go func() {
		runDone <- cmd.Run()
	}()

	if trayRun := takeTrayRunner(); trayRun != nil {
		trayDone := make(chan error, 1)
		go func() {
			for {
				select {
				case err := <-runDone:
					commandRunning.Store(false)
					if restartPending {
						stopTray()
						trayDone <- errRestartRequested
						return
					}
					if err != nil && !errors.Is(err, context.Canceled) {
						stopTray()
						trayDone <- err
						return
					}
					if options.desktop && codexDesktopShouldKeepAlive(userArgs) {
						// The desktop app detaches immediately; keep the tray and
						// proxy alive until codextra is signaled to shut down,
						// matching the non-tray keepalive path.
						log.Printf("desktop app launched; codextra tray stays active until quit")
						continue
					}
					stopTray()
					trayDone <- nil
					return
				case <-restartReqs:
					restartPending = true
					log.Printf("codextra received upgrade signal; waiting for proxy idleness before restart")
					if err := waitForProxyIdle(cmdCtx, proxyURL, restartWait); err != nil {
						// Cancellation mid-wait means the user is shutting down;
						// exit cleanly rather than reporting a failure.
						stopTray()
						if errors.Is(err, context.Canceled) {
							trayDone <- nil
						} else {
							trayDone <- err
						}
						return
					}
					log.Printf("restarting codextra wrapper")
					if !commandRunning.Load() {
						// The codex child already exited (desktop keepalive); there
						// is nothing to stop, so restart the wrapper directly.
						stopTray()
						trayDone <- errRestartRequested
						return
					}
					stopCmd()
				case <-ctx.Done():
					err := waitCommand()
					stopTray()
					if err == nil || errors.Is(err, context.Canceled) {
						trayDone <- nil
						return
					}
					trayDone <- err
					return
				}
			}
		}()
		if err := trayRun(); err != nil {
			return err
		}
		return <-trayDone
	}

	for {
		select {
		case err := <-runDone:
			commandRunning.Store(false)
			if restartPending {
				return errRestartRequested
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return keepProxyAliveForDesktop(err)
		case <-restartReqs:
			if !commandRunning.Load() {
				continue
			}
			restartPending = true
			log.Printf("codextra received upgrade signal; waiting for proxy idleness before restart")
			if err := waitForProxyIdle(cmdCtx, proxyURL, restartWait); err != nil {
				// Cancellation mid-wait means the user is shutting down; exit
				// cleanly rather than reporting a failure.
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}
			log.Printf("restarting codextra wrapper")
			stopCmd()
		case <-ctx.Done():
			err := waitCommand()
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
}

func runProxyServer(ctx context.Context) error {
	// The detached proxy shares the codextra binary name, so the installer's
	// upgrade SIGUSR1 also lands here. The proxy is upgraded separately via the
	// proxyStateVersion check, so ignore the signal rather than letting its
	// default action kill the proxy mid-install.
	ignoreUpgradeSignal()

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

	addr := os.Getenv("CODEXTRA_PROXY_ADDR")
	explicitAddr := addr != ""
	if !explicitAddr {
		addr = reusableProxyAddr()
	}
	listener, err := listenProxy(addr, explicitAddr, logger)
	if err != nil {
		return fmt.Errorf("listen proxy: %w", err)
	}
	defer listener.Close()

	server, err := proxy.New(proxy.Config{
		Upstream:        upstream,
		APIUpstream:     apiUpstream,
		Store:           store,
		Logger:          logger,
		OnAccountUpdate: updateCodexAuthForAccount,
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
	activity := newProxyActivityTracker()
	// Strip the route prefix before the activity/lifecycle handlers inspect the
	// path. Clients reach these control endpoints at proxyURL ("/<prefix>/__codextra/...")
	// so the prefix must be trimmed first; otherwise the prefixed health endpoint
	// would skip request tracking and the long-lived client connection would be
	// counted as active traffic forever.
	server.Handler = newRoutePrefixHandler(routePrefix, newProxyActivityHandler(lifecycle, activity))

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

func defaultUpgradeWait() time.Duration {
	value := os.Getenv("CODEXTRA_UPGRADE_WAIT_SECONDS")
	if value == "" {
		return defaultProxyUpgradeWait
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return defaultProxyUpgradeWait
	}
	return time.Duration(seconds) * time.Second
}

func waitForProxyIdle(ctx context.Context, proxyURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		active, err := proxyActiveRequests(ctx, proxyURL)
		if err == nil && active == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			// Per the documented contract, give up waiting after the timeout and
			// restart anyway rather than aborting the upgrade.
			log.Printf("codextra upgrade wait timed out after %s; restarting with traffic still in flight", timeout)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func proxyActiveRequests(ctx context.Context, proxyURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(proxyURL, "/")+"/__codextra/health", nil)
	if err != nil {
		return 0, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("proxy health status %s", res.Status)
	}

	var payload struct {
		OK             bool `json:"ok"`
		ActiveRequests int  `json:"active_requests"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return 0, err
	}
	if !payload.OK {
		return 0, fmt.Errorf("proxy health reported not ok")
	}
	return payload.ActiveRequests, nil
}

func randomRoutePrefix() (string, error) {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate proxy route token: %w", err)
	}
	return "/__codextra/" + hex.EncodeToString(bytes[:]), nil
}

func listenProxy(addr string, explicit bool, logger *slog.Logger) (net.Listener, error) {
	listener, err := listenProxyWithRetry(addr)
	if err == nil || explicit || addr == "127.0.0.1:0" {
		return listener, err
	}
	logger.Warn("proxy_reuse_addr_failed", "addr", addr, "error", err)
	return net.Listen("tcp", "127.0.0.1:0")
}

func listenProxyWithRetry(addr string) (net.Listener, error) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		listener, err := net.Listen("tcp", addr)
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) {
			return listener, err
		}
		time.Sleep(25 * time.Millisecond)
	}
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
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
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

type proxyActivityTracker struct {
	mu             sync.Mutex
	activeRequests int
}

func newProxyActivityTracker() *proxyActivityTracker {
	return &proxyActivityTracker{}
}

func (t *proxyActivityTracker) activeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activeRequests
}

func (t *proxyActivityTracker) withRequest() func() {
	t.mu.Lock()
	t.activeRequests++
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		if t.activeRequests > 0 {
			t.activeRequests--
		}
		t.mu.Unlock()
	}
}

func newProxyActivityHandler(next http.Handler, tracker *proxyActivityTracker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__codextra/health" {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":              true,
				"active_requests": tracker.activeCount(),
			})
			return
		}
		if r.URL.Path == "/__codextra/client" {
			next.ServeHTTP(w, r)
			return
		}

		done := tracker.withRequest()
		defer done()
		next.ServeHTTP(w, r)
	})
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

type proxyKeepAlive struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func keepProxyAlive(ctx context.Context, proxyURL string) (*proxyKeepAlive, error) {
	client, err := attachProxyClient(ctx, proxyURL)
	if err != nil {
		return nil, err
	}

	keepCtx, cancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(ready)
		defer client.Close()

		backoff := 100 * time.Millisecond
		for {
			select {
			case <-keepCtx.Done():
				return
			case <-client.done:
			}
			client.Close()

			for {
				select {
				case <-keepCtx.Done():
					return
				case <-time.After(backoff):
				}

				next, err := attachProxyClient(keepCtx, proxyURL)
				if err == nil {
					client = next
					backoff = 100 * time.Millisecond
					break
				}
				if backoff < time.Second {
					backoff *= 2
				}
			}
		}
	}()
	<-ready

	return &proxyKeepAlive{cancel: cancel, done: done}, nil
}

func (k *proxyKeepAlive) Close() {
	k.cancel()
	<-k.done
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

var codexAuthWriteMu sync.Mutex

func updateCodexAuthForAccount(account accounts.Account) error {
	codexAuthWriteMu.Lock()
	defer codexAuthWriteMu.Unlock()

	authPath, err := codexauth.Path()
	if err != nil {
		return err
	}
	return codexauth.Write(authPath, account)
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

func refreshAccountUsage(ctx context.Context, proxyURL string, storePath string) {
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		log.Printf("codextra usage store: %v", err)
		return
	}

	// Capture the active account before the fetch: the proxy serves /wham/usage
	// for whichever account is active, so the result belongs to this alias.
	before, err := store.Snapshot(time.Now())
	if err != nil {
		log.Printf("codextra usage snapshot: %v", err)
		return
	}
	alias := before.CurrentAlias
	if alias == "" {
		return
	}

	usageCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	percent, resetAt, err := fetchAccountUsage(usageCtx, proxyURL)
	if err != nil {
		log.Printf("codextra usage fetch: %v", err)
		return
	}

	// If the active account changed while the request was in flight, the usage
	// may belong to a different account; skip the write to avoid attributing it
	// to the wrong one. The post-switch refresh fetches fresh data for the new
	// account.
	after, err := store.Snapshot(time.Now())
	if err != nil {
		log.Printf("codextra usage snapshot: %v", err)
		return
	}
	if after.CurrentAlias != alias {
		return
	}

	if err := store.UpdateUsage(alias, percent, resetAt); err != nil {
		log.Printf("codextra usage update: %v", err)
	}
}

func fetchAccountUsage(ctx context.Context, proxyURL string) (int, int64, error) {
	usageURL := strings.TrimRight(proxyURL, "/") + "/backend-api/wham/usage"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return 0, 0, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch wham/usage: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, 0, fmt.Errorf("read wham/usage: %w", err)
	}

	var payload struct {
		RateLimit map[string]json.RawMessage `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, 0, fmt.Errorf("parse wham/usage: %w", err)
	}

	type rateWindow struct {
		UsedPercent int   `json:"used_percent"`
		ResetAt     int64 `json:"reset_at"`
	}
	maxPercent := 0
	var resetAt int64
	for _, raw := range payload.RateLimit {
		var window rateWindow
		if err := json.Unmarshal(raw, &window); err != nil {
			continue
		}
		// Pair the reset countdown with the window that drives the displayed
		// peak usage so the tray spotlight shows a consistent bucket, rather
		// than the peak percent next to an unrelated window's reset time.
		if window.UsedPercent > maxPercent {
			maxPercent = window.UsedPercent
			resetAt = window.ResetAt
		}
	}
	return maxPercent, resetAt, nil
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

func codexDesktopArgs(args []string) []string {
	desktopArgs := make([]string, 0, len(args)+1)
	desktopArgs = append(desktopArgs, "app")
	desktopArgs = append(desktopArgs, args...)
	return desktopArgs
}

func codexDesktopShouldKeepAlive(userArgs []string) bool {
	for _, arg := range userArgs {
		if arg == "--" {
			return true
		}
		if arg == "-h" || arg == "--help" {
			return false
		}
	}
	return true
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

func codexEnv(base []string, proxyURL string) []string {
	env := make([]string, 0, len(base)+2)
	for _, value := range base {
		if strings.HasPrefix(value, "CODEXTRA_PROXY_URL=") {
			continue
		}
		env = append(env, value)
	}
	env = append(env, "CODEXTRA_PROXY_URL="+proxyURL)
	return env
}

type codextraOptions struct {
	accountAlias string
	desktop      bool
}

func parseCodextraArgs(args []string) (codextraOptions, []string, error) {
	var options codextraOptions
	pass := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pass = append(pass, args[i:]...)
			break
		}
		if arg == "--account" {
			if i+1 >= len(args) {
				return codextraOptions{}, nil, fmt.Errorf("--account requires an alias")
			}
			options.accountAlias = args[i+1]
			i++
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--account="); ok {
			if value == "" {
				return codextraOptions{}, nil, fmt.Errorf("--account requires an alias")
			}
			options.accountAlias = value
			continue
		}
		if arg == "--desktop" {
			options.desktop = true
			continue
		}
		pass = append(pass, arg)
	}
	return options, pass, nil
}
