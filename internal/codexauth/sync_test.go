package codexauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

func TestAdoptFromCodexAuthCopiesFreshSession(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	fresh := fakeJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "chatgpt_account_id": "acct-work"})
	stale := fakeJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix(), "chatgpt_account_id": "acct-work"})
	auth := File{
		Tokens: &TokenData{
			AccessToken:  fresh,
			RefreshToken: "refresh-live",
		},
	}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	path := filepath.Join(codexHome, "auth.json")
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	registry := accounts.Account{
		Alias:        "work",
		AccessToken:  stale,
		RefreshToken: "refresh-old",
		AccountID:    "acct-work",
	}
	adopted, ok, err := AdoptFromCodexAuth(registry, time.Now())
	if err != nil {
		t.Fatalf("AdoptFromCodexAuth() error = %v", err)
	}
	if !ok {
		t.Fatal("AdoptFromCodexAuth() ok = false, want true")
	}
	if adopted.AccessToken != fresh {
		t.Fatalf("AccessToken mismatch (got %s, want %s)", redactSecret(adopted.AccessToken), redactSecret(fresh))
	}
	if adopted.RefreshToken != "refresh-live" {
		t.Fatalf("RefreshToken mismatch (got %s, want %s)", redactSecret(adopted.RefreshToken), redactSecret("refresh-live"))
	}
}

func TestAdoptFromCodexAuthSkipsDifferentAccount(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	fresh := fakeJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "chatgpt_account_id": "acct-other"})
	auth := File{Tokens: &TokenData{AccessToken: fresh, RefreshToken: "refresh-live"}}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	registry := accounts.Account{
		Alias:       "work",
		AccessToken: "stale",
		AccountID:   "acct-work",
	}
	if _, ok, err := AdoptFromCodexAuth(registry, time.Now()); err != nil || ok {
		t.Fatalf("AdoptFromCodexAuth() = ok %v err %v, want false nil", ok, err)
	}
}

func TestClientIDMatchesCodex(t *testing.T) {
	t.Parallel()

	const want = "app_EMoamEEZ73f0CkXaXp7hrann"
	if clientID != want {
		t.Fatalf("clientID = %q, want %q", clientID, want)
	}
}

func TestAdoptFromCodexAuthMatchesEmail(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	fresh := fakeJWT(t, map[string]any{
		"exp":   time.Now().Add(time.Hour).Unix(),
		"email": "work@example.com",
	})
	stale := fakeJWT(t, map[string]any{
		"exp":   time.Now().Add(-time.Hour).Unix(),
		"email": "work@example.com",
	})
	auth := File{Tokens: &TokenData{AccessToken: fresh, RefreshToken: "refresh-live"}}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	registry := accounts.Account{
		Alias:        "work",
		AccessToken:  stale,
		RefreshToken: "refresh-old",
		Email:        "work@example.com",
	}
	if _, ok, err := AdoptFromCodexAuth(registry, time.Now()); err != nil || !ok {
		t.Fatalf("AdoptFromCodexAuth() = ok %v err %v, want true nil", ok, err)
	}
}

func TestAdoptFromCodexAuthAdoptsNewerRefreshToken(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	stale := fakeJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix(), "chatgpt_account_id": "acct-work"})
	liveStale := fakeJWT(t, map[string]any{"exp": time.Now().Add(-2 * time.Hour).Unix(), "chatgpt_account_id": "acct-work"})
	auth := File{Tokens: &TokenData{AccessToken: liveStale, RefreshToken: "refresh-live"}}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	registry := accounts.Account{
		Alias:        "work",
		AccessToken:  stale,
		RefreshToken: "refresh-old",
		AccountID:    "acct-work",
	}
	if _, ok, err := AdoptFromCodexAuth(registry, time.Now()); err != nil || !ok {
		t.Fatalf("AdoptFromCodexAuth() = ok %v err %v, want true nil", ok, err)
	}
}

func TestAdoptFromCodexAuthMissingFile(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	registry := accounts.Account{Alias: "work", AccessToken: "stale"}
	if _, ok, err := AdoptFromCodexAuth(registry, time.Now()); err != nil || ok {
		t.Fatalf("AdoptFromCodexAuth() = ok %v err %v, want false nil", ok, err)
	}
}

func TestAdoptFromCodexAuthSkipsWhenRegistryAlreadyFresh(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	fresh := fakeJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "chatgpt_account_id": "acct-work"})
	auth := File{Tokens: &TokenData{AccessToken: fresh, RefreshToken: "refresh-live"}}
	bytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	registry := accounts.Account{
		Alias:        "work",
		AccessToken:  fresh,
		RefreshToken: "refresh-live",
		AccountID:    "acct-work",
	}
	if _, ok, err := AdoptFromCodexAuth(registry, time.Now()); err != nil || ok {
		t.Fatalf("AdoptFromCodexAuth() = ok %v err %v, want false nil", ok, err)
	}
}

func TestRefreshFailureHelpers(t *testing.T) {
	t.Parallel()

	if got := RefreshFailureMessage(nil); got != "" {
		t.Fatalf("RefreshFailureMessage(nil) = %q, want empty", got)
	}
	err := errors.New("refresh token expired; sign in again")
	if got := RefreshFailureMessage(err); got != err.Error() {
		t.Fatalf("RefreshFailureMessage() = %q, want %q", got, err.Error())
	}
	if !IsRecoverableRefreshFailure(err) {
		t.Fatal("IsRecoverableRefreshFailure(expired) = false, want true")
	}
	if IsRecoverableRefreshFailure(errors.New("network down")) {
		t.Fatal("IsRecoverableRefreshFailure(network) = true, want false")
	}
	if !IsRecoverableRefreshFailure(errors.New("refresh token already used; sign in again")) {
		t.Fatal("IsRecoverableRefreshFailure(reused) = false, want true")
	}
	if RefreshFailureMessage(errors.New("")) != "token refresh failed" {
		t.Fatal("RefreshFailureMessage(empty) did not return default message")
	}
}

func TestSyncHelpersCoverRemainingBranches(t *testing.T) {
	t.Parallel()

	if sameChatGPTAccount(accounts.Account{}, accounts.Account{}) {
		t.Fatal("sameChatGPTAccount(empty) = true, want false")
	}
	if !sameChatGPTAccount(accounts.Account{Email: "WORK@example.com"}, accounts.Account{Email: "work@example.com"}) {
		t.Fatal("sameChatGPTAccount(email case-insensitive) = false, want true")
	}

	now := time.Now()
	stale := fakeJWT(t, map[string]any{"exp": now.Add(-time.Hour).Unix()})
	fresh := fakeJWT(t, map[string]any{"exp": now.Add(time.Hour).Unix()})

	if shouldAdoptCodexAuth(
		accounts.Account{AccessToken: fresh, RefreshToken: "refresh-old"},
		accounts.Account{AccessToken: stale, RefreshToken: "refresh-live"},
		now,
	) {
		t.Fatal("shouldAdoptCodexAuth(live stale, registry fresh) = true, want false")
	}
	if shouldAdoptCodexAuth(
		accounts.Account{AccessToken: fresh, RefreshToken: "refresh-old"},
		accounts.Account{AccessToken: fresh, RefreshToken: ""},
		now,
	) {
		t.Fatal("shouldAdoptCodexAuth(empty live refresh) = true, want false")
	}
	if IsRecoverableRefreshFailure(nil) {
		t.Fatal("IsRecoverableRefreshFailure(nil) = true, want false")
	}
	if !IsRecoverableRefreshFailure(errors.New("refresh token revoked")) {
		t.Fatal("IsRecoverableRefreshFailure(revoked) = false, want true")
	}
	if !IsRecoverableRefreshFailure(errors.New("refresh token rejected")) {
		t.Fatal("IsRecoverableRefreshFailure(rejected) = false, want true")
	}
}
