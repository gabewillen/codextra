package codexauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

type File struct {
	Tokens      *TokenData `json:"tokens"`
	LastRefresh string     `json:"last_refresh,omitempty"`
}

type TokenData struct {
	IDToken      any    `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

func Path() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

func Import(alias, path string) (accounts.Account, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return accounts.Account{}, fmt.Errorf("read codex auth: %w", err)
	}

	var auth File
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

func Write(path string, account accounts.Account) error {
	if account.AccessToken == "" {
		return fmt.Errorf("account %q has no access token", account.Alias)
	}
	auth := File{
		Tokens: &TokenData{
			IDToken:      idTokenValue(account.IDToken),
			AccessToken:  account.AccessToken,
			RefreshToken: account.RefreshToken,
			AccountID:    account.AccountID,
		},
		LastRefresh: time.Now().UTC().Format(time.RFC3339Nano),
	}
	bytes, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("encode codex auth: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create codex auth directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(bytes, '\n'), 0600); err != nil {
		return fmt.Errorf("write codex auth: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace codex auth: %w", err)
	}
	return nil
}

func idTokenValue(token string) any {
	if token == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(token), &value); err == nil {
		return value
	}
	return token
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
