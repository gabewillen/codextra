package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gabewillen/codextra/internal/accounts"
)

type codexAuthFile struct {
	Tokens *codexTokenData `json:"tokens"`
}

type codexTokenData struct {
	IDToken      any    `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

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

	authPath, err := codexAuthPath()
	if err != nil {
		return err
	}
	account, err := importCodexAuth(alias, authPath)
	if err != nil {
		return err
	}
	if err := store.Upsert(account); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved Codex account %q in %s\n", alias, storePath)
	return nil
}

func codexAuthPath() (string, error) {
	if home := getenv("CODEX_HOME", ""); home != "" {
		return filepath.Join(home, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

func importCodexAuth(alias, path string) (accounts.Account, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return accounts.Account{}, fmt.Errorf("read codex auth: %w", err)
	}

	var auth codexAuthFile
	if err := json.Unmarshal(bytes, &auth); err != nil {
		return accounts.Account{}, fmt.Errorf("parse codex auth: %w", err)
	}
	if auth.Tokens == nil || auth.Tokens.AccessToken == "" {
		return accounts.Account{}, errors.New("codex login did not produce ChatGPT token auth")
	}

	claims := jwtClaims(auth.Tokens.AccessToken)
	accountID := firstNonEmpty(
		auth.Tokens.AccountID,
		stringClaim(claims, "https://api.openai.com/auth_account_id"),
		stringClaim(claims, "chatgpt_account_id"),
		stringClaim(claims, "account_id"),
	)

	return accounts.Account{
		Alias:        alias,
		AccessToken:  auth.Tokens.AccessToken,
		RefreshToken: auth.Tokens.RefreshToken,
		IDToken:      idTokenString(auth.Tokens.IDToken),
		AccountID:    accountID,
		Email:        firstNonEmpty(stringClaim(claims, "email"), stringClaim(claims, "https://api.openai.com/email")),
		PlanType:     firstNonEmpty(stringClaim(claims, "chatgpt_plan_type"), stringClaim(claims, "https://api.openai.com/plan_type")),
	}, nil
}

func idTokenString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		bytes, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(bytes)
	}
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	value, _ := claims[key].(string)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
