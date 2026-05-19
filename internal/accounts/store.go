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
