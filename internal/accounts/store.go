package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Account struct {
	Alias           string            `json:"alias"`
	AccessToken     string            `json:"access_token"`
	RefreshToken    string            `json:"refresh_token,omitempty"`
	IDToken         string            `json:"id_token,omitempty"`
	AccountID       string            `json:"account_id,omitempty"`
	Email           string            `json:"email,omitempty"`
	PlanType        string            `json:"plan_type,omitempty"`
	DisabledUntil   map[string]int64  `json:"disabled_until,omitempty"`
	LastLimitStatus map[string]string `json:"last_limit_status,omitempty"`
	UsagePercent    int               `json:"usage_percent,omitempty"`
	UsageResetAt    int64             `json:"usage_reset_at,omitempty"`
}

type Store struct {
	path string
	mu   sync.Mutex
	Data Data
}

type Data struct {
	ActiveAlias string    `json:"active_alias,omitempty"`
	Accounts    []Account `json:"accounts"`
}

type Snapshot struct {
	ActiveAlias  string
	CurrentAlias string
	Accounts     []Account
}

func LoadStore(path string) (*Store, error) {
	store := &Store{path: path}
	bytes, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		store.Data = Data{Accounts: []Account{}}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read account store: %w", err)
	}
	if err := json.Unmarshal(bytes, &store.Data); err != nil {
		return nil, fmt.Errorf("parse account store: %w", err)
	}
	return store, nil
}

func (s *Store) Current(now time.Time) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.reloadLocked()
	if s.Data.ActiveAlias != "" {
		if account, ok := s.findEligibleLocked(s.Data.ActiveAlias, now); ok {
			return account, true
		}
	}
	for _, account := range s.Data.Accounts {
		if eligible(account, now) {
			return account, true
		}
	}
	return Account{}, false
}

func (s *Store) Snapshot(now time.Time) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.reloadLocked(); err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		ActiveAlias: s.Data.ActiveAlias,
		Accounts:    make([]Account, len(s.Data.Accounts)),
	}
	for i := range s.Data.Accounts {
		snapshot.Accounts[i] = cloneAccount(s.Data.Accounts[i])
	}

	if alias, ok := s.currentAliasLocked(now); ok {
		snapshot.CurrentAlias = alias
	}
	return snapshot, nil
}

func (s *Store) currentAliasLocked(now time.Time) (string, bool) {
	if account, ok := s.findEligibleLocked(s.Data.ActiveAlias, now); ok {
		return account.Alias, true
	}
	for _, account := range s.Data.Accounts {
		if eligible(account, now) {
			return account.Alias, true
		}
	}
	return "", false
}

func cloneAccount(account Account) Account {
	cloned := account
	cloned.DisabledUntil = cloneInt64Map(account.DisabledUntil)
	cloned.LastLimitStatus = cloneStringMap(account.LastLimitStatus)
	return cloned
}

func cloneInt64Map(values map[string]int64) map[string]int64 {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]int64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (s *Store) Get(alias string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.reloadLocked()
	for _, account := range s.Data.Accounts {
		if account.Alias == alias {
			return account, true
		}
	}
	return Account{}, false
}

func (s *Store) RotateFrom(alias string, limit string, resetAt time.Time, now time.Time) (Account, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.reloadLocked()
	for i := range s.Data.Accounts {
		if s.Data.Accounts[i].Alias == alias {
			if s.Data.Accounts[i].DisabledUntil == nil {
				s.Data.Accounts[i].DisabledUntil = map[string]int64{}
			}
			if !resetAt.IsZero() {
				s.Data.Accounts[i].DisabledUntil[limit] = resetAt.Unix()
			}
			break
		}
	}

	for _, account := range s.Data.Accounts {
		if account.Alias != alias && eligible(account, now) {
			s.Data.ActiveAlias = account.Alias
			return account, true, s.saveLocked()
		}
	}
	return Account{}, false, s.saveLocked()
}

// MarkNeedsLogin records that alias was signed out server-side and cannot be
// recovered by refreshing: it clears the stored access token so the account is
// no longer eligible (rotation skips it and the tray surfaces it under "Needs
// sign-in"). If alias was the active account, it switches the active account to
// another eligible one. It returns the new active account and whether a switch
// occurred. Re-running login restores the account.
func (s *Store) MarkNeedsLogin(alias string, now time.Time) (Account, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.reloadLocked()
	for i := range s.Data.Accounts {
		if s.Data.Accounts[i].Alias == alias {
			s.Data.Accounts[i].AccessToken = ""
			break
		}
	}

	if s.Data.ActiveAlias == alias {
		for _, account := range s.Data.Accounts {
			if account.Alias != alias && eligible(account, now) {
				s.Data.ActiveAlias = account.Alias
				return account, true, s.saveLocked()
			}
		}
	}
	return Account{}, false, s.saveLocked()
}

func (s *Store) UpdateUsage(alias string, percent int, resetAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.reloadLocked()
	for i := range s.Data.Accounts {
		if s.Data.Accounts[i].Alias == alias {
			s.Data.Accounts[i].UsagePercent = percent
			s.Data.Accounts[i].UsageResetAt = resetAt
			return s.saveLocked()
		}
	}
	return fmt.Errorf("account %q not found", alias)
}

func (s *Store) UpdateTokens(alias string, tokens Account) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.reloadLocked(); err != nil {
		return Account{}, err
	}
	for i := range s.Data.Accounts {
		if s.Data.Accounts[i].Alias != alias {
			continue
		}
		acc := &s.Data.Accounts[i]
		acc.AccessToken = tokens.AccessToken
		if tokens.RefreshToken != "" {
			acc.RefreshToken = tokens.RefreshToken
		}
		if tokens.IDToken != "" {
			acc.IDToken = tokens.IDToken
		}
		if tokens.AccountID != "" {
			acc.AccountID = tokens.AccountID
		}
		if tokens.Email != "" {
			acc.Email = tokens.Email
		}
		if tokens.PlanType != "" {
			acc.PlanType = tokens.PlanType
		}
		updated := *acc
		return updated, s.saveLocked()
	}
	return Account{}, fmt.Errorf("account %q not found", alias)
}

func (s *Store) Upsert(account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Data.Accounts {
		if s.Data.Accounts[i].Alias == account.Alias {
			s.Data.Accounts[i] = account
			if s.Data.ActiveAlias == "" {
				s.Data.ActiveAlias = account.Alias
			}
			return s.saveLocked()
		}
	}

	s.Data.Accounts = append(s.Data.Accounts, account)
	if s.Data.ActiveAlias == "" {
		s.Data.ActiveAlias = account.Alias
	}
	return s.saveLocked()
}

func (s *Store) SetActive(alias string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.reloadLocked()
	for i := range s.Data.Accounts {
		if s.Data.Accounts[i].Alias == alias {
			if s.Data.Accounts[i].AccessToken == "" {
				return fmt.Errorf("account %q has no access token", alias)
			}
			s.Data.Accounts[i].DisabledUntil = nil
			s.Data.Accounts[i].LastLimitStatus = nil
			s.Data.ActiveAlias = alias
			return s.saveLocked()
		}
	}
	return fmt.Errorf("account %q not found", alias)
}

func (s *Store) findEligibleLocked(alias string, now time.Time) (Account, bool) {
	for _, account := range s.Data.Accounts {
		if account.Alias == alias && eligible(account, now) {
			return account, true
		}
	}
	return Account{}, false
}

func eligible(account Account, now time.Time) bool {
	for _, unix := range account.DisabledUntil {
		if unix > now.Unix() {
			return false
		}
	}
	return account.AccessToken != ""
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create account store directory: %w", err)
	}
	bytes, err := json.MarshalIndent(s.Data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode account store: %w", err)
	}
	return os.WriteFile(s.path, append(bytes, '\n'), 0600)
}

func (s *Store) reloadLocked() error {
	bytes, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read account store: %w", err)
	}
	var data Data
	if err := json.Unmarshal(bytes, &data); err != nil {
		return fmt.Errorf("parse account store: %w", err)
	}
	s.Data = data
	return nil
}
