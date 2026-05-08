package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gabewillen/codextra/internal/proxy"
)

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

	proxyURL, err := ensureProxy()
	if err != nil {
		return err
	}

	codexArgs := codexArgs(proxyURL, os.Args[1:])
	cmd := exec.CommandContext(ctx, getenv("CODEXTRA_CODEX_BIN", "codex"), codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = codexEnv(os.Environ(), proxyURL)

	log.Printf("using proxy %s", proxyURL)
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
	addr := getenv("CODEXTRA_PROXY_ADDR", "127.0.0.1:0")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen proxy: %w", err)
	}
	defer listener.Close()

	server, err := proxy.New(proxy.Config{
		Upstream: upstream,
		Store:    store,
	})
	if err != nil {
		return err
	}

	proxyURL := "http://" + listener.Addr().String()
	if err := writeProxyState(proxyState{
		URL:      proxyURL,
		PID:      os.Getpid(),
		Upstream: upstream,
	}); err != nil {
		return err
	}
	log.Printf("proxy listening on %s -> %s", proxyURL, upstream)

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type proxyState struct {
	URL      string `json:"url"`
	PID      int    `json:"pid"`
	Upstream string `json:"upstream"`
}

func ensureProxy() (string, error) {
	if state, err := readProxyState(); err == nil && healthy(state.URL) {
		return state.URL, nil
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
	args := make([]string, 0, len(userArgs)+2)
	args = append(args, "-c", "chatgpt_base_url="+proxyURL)
	args = append(args, userArgs...)
	return args
}

func codexEnv(base []string, proxyURL string) []string {
	env := make([]string, 0, len(base)+1)
	env = append(env, base...)
	env = append(env, "CODEXTRA_PROXY_URL="+proxyURL)
	return env
}
