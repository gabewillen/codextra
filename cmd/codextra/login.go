package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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
	authPath, err := codexauth.Path()
	if err != nil {
		return err
	}
	return importAuthFromPath(alias, inferAlias, authPath)
}

func importAuthFromPath(alias string, inferAlias bool, authPath string) error {
	storePath, err := defaultStorePath()
	if err != nil {
		return err
	}
	store, err := accounts.LoadStore(storePath)
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
	saved, err := saveImportedAccount(store, account)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved Codex account %q in %s\n", saved.Alias, storePath)
	return nil
}

func saveImportedAccount(store *accounts.Store, account accounts.Account) (accounts.Account, error) {
	if account.Alias == "" {
		return accounts.Account{}, errors.New("account alias must be a non-empty name")
	}
	if existing, ok := store.Get(account.Alias); ok {
		if !hasAccountIdentity(account) && hasAccountIdentity(existing) {
			return accounts.Account{}, fmt.Errorf("codex login returned credentials without account identity; left alias %q unchanged", account.Alias)
		}
		if !hasAccountIdentity(account) && !hasAccountIdentity(existing) {
			otherAccounts, err := hasOtherStoredAccounts(store, account.Alias)
			if err != nil {
				return accounts.Account{}, err
			}
			if otherAccounts {
				return accounts.Account{}, fmt.Errorf("codex login returned credentials without account identity; left alias %q unchanged", account.Alias)
			}
		}
		if hasAccountIdentity(account) && !sameAccountIdentity(existing, account) {
			matching, ok, err := matchingStoredAccount(store, account, account.Alias)
			if err != nil {
				return accounts.Account{}, err
			}
			if ok {
				updated, err := saveImportedCredentials(store, matching, accountWithAlias(account, matching.Alias))
				if err != nil {
					return accounts.Account{}, err
				}
				return updated, fmt.Errorf("codex login returned credentials for existing alias %q instead of %q; updated %q and left %q unchanged", matching.Alias, account.Alias, matching.Alias, account.Alias)
			}
			if !hasAccountIdentity(existing) {
				return saveImportedCredentials(store, existing, account)
			}
			if identitiesOverlap(existing, account) {
				return accounts.Account{}, fmt.Errorf("codex login returned credentials that do not match existing alias %q; left registry unchanged", account.Alias)
			}
			return accounts.Account{}, fmt.Errorf("codex login returned credentials with ambiguous account identity; left alias %q unchanged", account.Alias)
		}
		if accountIdentityConflicts(existing, account) {
			matching, ok, err := matchingStoredAccount(store, account, account.Alias)
			if err != nil {
				return accounts.Account{}, err
			}
			if ok {
				updated, err := saveImportedCredentials(store, matching, accountWithAlias(account, matching.Alias))
				if err != nil {
					return accounts.Account{}, err
				}
				return updated, fmt.Errorf("codex login returned credentials for existing alias %q instead of %q; updated %q and left %q unchanged", matching.Alias, account.Alias, matching.Alias, account.Alias)
			}
			return accounts.Account{}, fmt.Errorf("codex login returned credentials that do not match existing alias %q; left registry unchanged", account.Alias)
		}
		return saveImportedCredentials(store, existing, account)
	}
	matching, ok, err := matchingStoredAccount(store, account, "")
	if err != nil {
		return accounts.Account{}, err
	}
	if ok {
		updated, err := saveImportedCredentials(store, matching, accountWithAlias(account, matching.Alias))
		if err != nil {
			return accounts.Account{}, err
		}
		return updated, fmt.Errorf("codex login returned credentials for existing alias %q instead of new alias %q; updated %q and left %q unused", matching.Alias, account.Alias, matching.Alias, account.Alias)
	}
	if !hasAccountIdentity(account) {
		snapshot, err := store.Snapshot(time.Now())
		if err != nil {
			return accounts.Account{}, err
		}
		if len(snapshot.Accounts) != 0 {
			return accounts.Account{}, fmt.Errorf("codex login returned credentials without account identity; left new alias %q unused", account.Alias)
		}
	}
	if err := store.Upsert(account); err != nil {
		return accounts.Account{}, err
	}
	if saved, ok := store.Get(account.Alias); ok {
		return saved, nil
	}
	return account, nil
}

func hasOtherStoredAccounts(store *accounts.Store, alias string) (bool, error) {
	snapshot, err := store.Snapshot(time.Now())
	if err != nil {
		return false, err
	}
	for _, account := range snapshot.Accounts {
		if account.Alias != alias {
			return true, nil
		}
	}
	return false, nil
}

func saveImportedCredentials(store *accounts.Store, existing, imported accounts.Account) (accounts.Account, error) {
	credentials := accounts.Account{
		Alias:        existing.Alias,
		AccessToken:  imported.AccessToken,
		RefreshToken: imported.RefreshToken,
	}
	if imported.IDToken != "" {
		credentials.IDToken = imported.IDToken
	}
	if imported.AccountID != "" {
		credentials.AccountID = imported.AccountID
	}
	if imported.Email != "" {
		credentials.Email = imported.Email
	}
	if imported.PlanType != "" {
		credentials.PlanType = imported.PlanType
	}
	return store.ReplaceCredentials(existing.Alias, credentials)
}

func matchingStoredAccount(store *accounts.Store, account accounts.Account, excludeAlias string) (accounts.Account, bool, error) {
	snapshot, err := store.Snapshot(time.Now())
	if err != nil {
		return accounts.Account{}, false, err
	}
	for _, candidate := range snapshot.Accounts {
		if candidate.Alias == excludeAlias {
			continue
		}
		if sameAccountIdentity(candidate, account) {
			return candidate, true, nil
		}
	}
	return accounts.Account{}, false, nil
}

func accountWithAlias(account accounts.Account, alias string) accounts.Account {
	account.Alias = alias
	return account
}

func sameAccountIdentity(a, b accounts.Account) bool {
	aID := effectiveAccountIdentity(a)
	bID := effectiveAccountIdentity(b)
	if aID.AccountID != "" && bID.AccountID != "" {
		return aID.AccountID == bID.AccountID
	}
	if aID.Email != "" && bID.Email != "" {
		return strings.EqualFold(aID.Email, bID.Email)
	}
	return false
}

func accountIdentityConflicts(existing, imported accounts.Account) bool {
	existingID := effectiveAccountIdentity(existing)
	importedID := effectiveAccountIdentity(imported)
	if existingID.AccountID != "" && importedID.AccountID != "" {
		return existingID.AccountID != importedID.AccountID
	}
	if existingID.Email != "" && importedID.Email != "" {
		return !strings.EqualFold(existingID.Email, importedID.Email)
	}
	return false
}

func identitiesOverlap(a, b accounts.Account) bool {
	aID := effectiveAccountIdentity(a)
	bID := effectiveAccountIdentity(b)
	return (aID.AccountID != "" && bID.AccountID != "") ||
		(aID.Email != "" && bID.Email != "")
}

func hasAccountIdentity(account accounts.Account) bool {
	identity := effectiveAccountIdentity(account)
	return identity.AccountID != "" || identity.Email != ""
}

func effectiveAccountIdentity(account accounts.Account) codexauth.Identity {
	identity := codexauth.Identity{
		AccountID: account.AccountID,
		Email:     account.Email,
		PlanType:  account.PlanType,
	}
	if identity.AccountID != "" && identity.Email != "" && identity.PlanType != "" {
		return identity
	}
	derived := codexauth.IdentityFromAccessToken(account.AccessToken)
	if identity.AccountID == "" {
		identity.AccountID = derived.AccountID
	}
	if identity.Email == "" {
		identity.Email = derived.Email
	}
	if identity.PlanType == "" {
		identity.PlanType = derived.PlanType
	}
	return identity
}
