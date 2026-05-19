package codexauth

import (
	"os"
	"strings"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

// AdoptFromCodexAuth copies OAuth credentials from the live Codex auth.json file
// into a registry account when they refer to the same ChatGPT account and the
// on-disk session is strictly newer than the registry copy.
func AdoptFromCodexAuth(account accounts.Account, now time.Time) (accounts.Account, bool, error) {
	path, err := Path()
	if err != nil {
		return account, false, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return account, false, nil
		}
		return account, false, err
	}
	live, err := Import(account.Alias, path)
	if err != nil {
		return account, false, err
	}
	if !sameChatGPTAccount(account, live) {
		return account, false, nil
	}
	if !shouldAdoptCodexAuth(account, live, now) {
		return account, false, nil
	}
	return live, true, nil
}

func sameChatGPTAccount(registry, live accounts.Account) bool {
	if registry.AccountID != "" && live.AccountID != "" {
		return registry.AccountID == live.AccountID
	}
	if registry.Email != "" && live.Email != "" {
		return strings.EqualFold(registry.Email, live.Email)
	}
	return false
}

func shouldAdoptCodexAuth(registry, live accounts.Account, now time.Time) bool {
	registryStale := AccessTokenStale(registry.AccessToken, now)
	liveFresh := live.AccessToken != "" && !AccessTokenStale(live.AccessToken, now)
	if liveFresh && registryStale {
		return true
	}
	if live.RefreshToken == "" || live.RefreshToken == registry.RefreshToken {
		return false
	}
	if liveFresh {
		return true
	}
	return registryStale
}

// RefreshFailureMessage returns a short, non-secret explanation suitable for CLI or HTTP errors.
func RefreshFailureMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if msg == "" {
		return "token refresh failed"
	}
	return msg
}

// IsRecoverableRefreshFailure reports whether Codex may already hold a newer session on disk.
func IsRecoverableRefreshFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already used") ||
		strings.Contains(msg, "expired") ||
		strings.Contains(msg, "revoked") ||
		strings.Contains(msg, "rejected")
}
