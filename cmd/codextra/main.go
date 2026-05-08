package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

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

	storePath, err := defaultStorePath()
	if err != nil {
		return err
	}
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		return err
	}

	upstream := getenv("CODEXTRA_UPSTREAM", "https://chatgpt.com")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	defer server.Shutdown(context.Background())

	proxyURL := "http://" + listener.Addr().String()
	codexArgs := append([]string{"-c", "chatgpt_base_url=" + proxyURL}, os.Args[1:]...)
	cmd := exec.CommandContext(ctx, getenv("CODEXTRA_CODEX_BIN", "codex"), codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"CODEXTRA_PROXY_URL="+proxyURL,
		"CODEXTRA_UPSTREAM="+upstream,
	)

	log.Printf("proxy listening on %s -> %s", proxyURL, upstream)
	if err := cmd.Run(); err != nil {
		return err
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	default:
	}
	return nil
}

func defaultStorePath() (string, error) {
	if path := os.Getenv("CODEXTRA_STORE"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".codextra", "accounts.json"), nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
