package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gabewillen/codextra/internal/codexauth"
)

func runLogin(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: codextra login <alias> [codex login args...]")
	}
	alias := strings.TrimSpace(args[0])
	if alias == "" || strings.HasPrefix(alias, "-") {
		return errors.New("login alias must be a non-empty name")
	}

	storePath, err := defaultStorePath()
	if err != nil {
		return err
	}
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		return err
	}

	codexArgs := append([]string{"login"}, args[1:]...)
	cmd := exec.CommandContext(ctx, getenv("CODEXTRA_CODEX_BIN", "codex"), codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	authPath, err := codexauth.Path()
	if err != nil {
		return err
	}
	account, err := codexauth.Import(alias, authPath)
	if err != nil {
		return err
	}
	if err := store.Upsert(account); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved Codex account %q in %s\n", alias, storePath)
	return nil
}
