package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestUpsertPersistsAndReplacesAccount(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}

	first := Account{Alias: "work", AccessToken: "token-1", AccountID: "acct-1"}
	if err := store.Upsert(first); err != nil {
		t.Fatalf("Upsert(first) error = %v", err)
	}
	replacement := Account{Alias: "work", AccessToken: "token-2", AccountID: "acct-2"}
	if err := store.Upsert(replacement); err != nil {
		t.Fatalf("Upsert(replacement) error = %v", err)
	}

	loaded, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore(persisted) error = %v", err)
	}
	if loaded.Data.ActiveAlias != "work" {
		t.Fatalf("ActiveAlias = %q, want work", loaded.Data.ActiveAlias)
	}
	if len(loaded.Data.Accounts) != 1 {
		t.Fatalf("len(Accounts) = %d, want 1", len(loaded.Data.Accounts))
	}
	if !reflect.DeepEqual(loaded.Data.Accounts[0], replacement) {
		t.Fatalf("persisted account = %#v, want %#v", loaded.Data.Accounts[0], replacement)
	}
}

func TestCurrentSkipsDisabledAndTokenlessAccounts(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	store.Data = Data{
		ActiveAlias: "limited",
		Accounts: []Account{
			{
				Alias:           "limited",
				AccessToken:     "token-limited",
				DisabledUntil:   map[string]int64{"codex": now.Add(time.Hour).Unix()},
				LastLimitStatus: map[string]string{"codex": "limited"},
			},
			{Alias: "empty"},
			{Alias: "ready", AccessToken: "token-ready"},
		},
	}

	account, ok := store.Current(now)
	if !ok {
		t.Fatal("Current() ok = false, want true")
	}
	if account.Alias != "ready" {
		t.Fatalf("Current().Alias = %q, want ready", account.Alias)
	}
}

func TestRotateFromDisablesCurrentAndSelectsNextEligible(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	resetAt := now.Add(5 * time.Hour)
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	store.Data = Data{
		ActiveAlias: "personal",
		Accounts: []Account{
			{Alias: "personal", AccessToken: "token-personal"},
			{Alias: "work", AccessToken: "token-work"},
		},
	}

	next, rotated, err := store.RotateFrom("personal", "codex_weekly", resetAt, now)
	if err != nil {
		t.Fatalf("RotateFrom() error = %v", err)
	}
	if !rotated {
		t.Fatal("RotateFrom() rotated = false, want true")
	}
	if next.Alias != "work" {
		t.Fatalf("next.Alias = %q, want work", next.Alias)
	}
	if store.Data.ActiveAlias != "work" {
		t.Fatalf("ActiveAlias = %q, want work", store.Data.ActiveAlias)
	}
	if got := store.Data.Accounts[0].DisabledUntil["codex_weekly"]; got != resetAt.Unix() {
		t.Fatalf("DisabledUntil[codex_weekly] = %d, want %d", got, resetAt.Unix())
	}
}

func TestLoadStoreRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadStore(path); err == nil {
		t.Fatal("LoadStore(malformed) error = nil, want error")
	}
}

func TestLoadStoreRejectsUnreadablePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := LoadStore(dir); err == nil {
		t.Fatal("LoadStore(directory) error = nil, want error")
	}
}

func TestLoadStoreMissingFileStartsEmpty(t *testing.T) {
	t.Parallel()

	store, err := LoadStore(filepath.Join(t.TempDir(), "missing", "accounts.json"))
	if err != nil {
		t.Fatalf("LoadStore(missing) error = %v", err)
	}
	if store.Data.ActiveAlias != "" {
		t.Fatalf("ActiveAlias = %q, want empty", store.Data.ActiveAlias)
	}
	if len(store.Data.Accounts) != 0 {
		t.Fatalf("len(Accounts) = %d, want 0", len(store.Data.Accounts))
	}
}

func TestRotateFromWithoutResetStillRotates(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	store.Data = Data{
		ActiveAlias: "personal",
		Accounts: []Account{
			{Alias: "personal", AccessToken: "token-personal"},
			{Alias: "work", AccessToken: "token-work"},
		},
	}

	next, rotated, err := store.RotateFrom("personal", "codex", time.Time{}, now)
	if err != nil {
		t.Fatalf("RotateFrom() error = %v", err)
	}
	if !rotated {
		t.Fatal("RotateFrom() rotated = false, want true")
	}
	if next.Alias != "work" {
		t.Fatalf("next.Alias = %q, want work", next.Alias)
	}
	if len(store.Data.Accounts[0].DisabledUntil) != 0 {
		t.Fatalf("DisabledUntil = %#v, want empty map", store.Data.Accounts[0].DisabledUntil)
	}
}

func TestRotateFromWithoutNextEligiblePersistsLimit(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	resetAt := now.Add(time.Hour)
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	store.Data = Data{
		ActiveAlias: "personal",
		Accounts:    []Account{{Alias: "personal", AccessToken: "token-personal"}},
	}

	_, rotated, err := store.RotateFrom("personal", "codex", resetAt, now)
	if err != nil {
		t.Fatalf("RotateFrom() error = %v", err)
	}
	if rotated {
		t.Fatal("RotateFrom() rotated = true, want false")
	}
	loaded, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore(persisted) error = %v", err)
	}
	if got := loaded.Data.Accounts[0].DisabledUntil["codex"]; got != resetAt.Unix() {
		t.Fatalf("persisted DisabledUntil[codex] = %d, want %d", got, resetAt.Unix())
	}
}

func TestCurrentReturnsFalseWhenNoEligibleAccount(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	store, err := LoadStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	store.Data = Data{
		Accounts: []Account{
			{Alias: "empty"},
			{
				Alias:         "limited",
				AccessToken:   "token",
				DisabledUntil: map[string]int64{"codex": now.Add(time.Hour).Unix()},
			},
		},
	}

	if account, ok := store.Current(now); ok {
		t.Fatalf("Current() = %#v, true; want false", account)
	}
}

func TestCurrentReloadsAccountStoreFromDisk(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	data := Data{
		ActiveAlias: "work",
		Accounts:    []Account{{Alias: "work", AccessToken: "token-work"}},
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	account, ok := store.Current(time.Now())
	if !ok {
		t.Fatal("Current() ok = false, want true")
	}
	if account.Alias != "work" {
		t.Fatalf("Current().Alias = %q, want work", account.Alias)
	}
}

func TestReloadLockedReturnsParseError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store := &Store{path: path}
	if err := store.reloadLocked(); err == nil {
		t.Fatal("reloadLocked() error = nil, want error")
	}
}

func TestReloadLockedReturnsReadError(t *testing.T) {
	t.Parallel()

	store := &Store{path: t.TempDir()}
	if err := store.reloadLocked(); err == nil {
		t.Fatal("reloadLocked() error = nil, want error")
	}
}

func TestStoreFileIsJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Upsert(Account{Alias: "personal", AccessToken: "token"}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var data Data
	if err := json.Unmarshal(bytes, &data); err != nil {
		t.Fatalf("Unmarshal(persisted) error = %v", err)
	}
	if data.ActiveAlias != "personal" {
		t.Fatalf("ActiveAlias = %q, want personal", data.ActiveAlias)
	}
}
