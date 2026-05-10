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
	alias, tagOnly, codexLoginArgs, err := parseLoginArgs(args)
	if err != nil {
		return err
	}
	if tagOnly {
		return importCurrentAuth(alias, true)
	}

	codexArgs := append([]string{"login"}, codexLoginArgs...)
	cmd := exec.CommandContext(ctx, getenv("CODEXTRA_CODEX_BIN", "codex"), codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return importCurrentAuth(alias, false)
}

func parseLoginArgs(args []string) (string, bool, []string, error) {
	var alias string
	var tagOnly bool
	codexArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--tag" {
			tagOnly = true
			continue
		}
		if alias == "" && !strings.HasPrefix(arg, "-") {
			alias = arg
			continue
		}
		codexArgs = append(codexArgs, arg)
	}
	if alias == "" && !tagOnly {
		return "", false, nil, errors.New("usage: codextra login [--tag] <alias> [codex login args...]")
	}
	if tagOnly && len(codexArgs) != 0 {
		return "", false, nil, errors.New("usage: codextra login --tag [alias]")
	}
	if alias != "" {
		var err error
		alias, err = accountAlias(alias)
		if err != nil {
			return "", false, nil, err
		}
	}
	return alias, tagOnly, codexArgs, nil
}

func accountAlias(value string) (string, error) {
	alias := strings.TrimSpace(value)
	if alias == "" || strings.HasPrefix(alias, "-") {
		return "", errors.New("account alias must be a non-empty name")
	}
	return alias, nil
}

func importCurrentAuth(alias string, inferAlias bool) error {
	storePath, err := defaultStorePath()
	if err != nil {
		return err
	}
	store, err := accounts.LoadStore(storePath)
	if err != nil {
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
	if inferAlias && account.Alias == "" {
		inferred := account.Email
		if inferred == "" {
			inferred = account.AccountID
		}
		account.Alias, err = accountAlias(inferred)
		if err != nil {
			return errors.New("current Codex auth has no email or account ID; use codextra login --tag <alias>")
		}
	}
	if err := store.Upsert(account); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved Codex account %q in %s\n", alias, storePath)
	return nil
}
