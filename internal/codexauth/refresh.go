package codexauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

const (
	defaultRefreshURL     = "https://auth.openai.com/oauth/token"
	clientID              = "app_EMoamEEZ73f0CkXaXp7hran"
	tokenRefreshSkew      = 30 * time.Second
	defaultRefreshTimeout = 30 * time.Second
)

var errRefreshTokenUnavailable = errors.New("refresh token unavailable")

type refreshRequest struct {
	ClientID     string `json:"client_id"`
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// AccessTokenExpiresKnown reports whether the access token carries a JWT expiry claim.
func AccessTokenExpiresKnown(accessToken string) bool {
	claims := jwtClaims(accessToken)
	if claims == nil {
		return false
	}
	_, ok := claims["exp"].(float64)
	return ok
}

// AccessTokenStale reports whether the access token is expired or near expiry.
func AccessTokenStale(accessToken string, now time.Time) bool {
	claims := jwtClaims(accessToken)
	if claims == nil {
		return false
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return false
	}
	return now.Unix() >= int64(exp)-int64(tokenRefreshSkew.Seconds())
}

// Refresh exchanges a refresh token for updated OAuth credentials.
func Refresh(ctx context.Context, client *http.Client, refreshToken string) (TokenData, error) {
	if refreshToken == "" {
		return TokenData{}, errRefreshTokenUnavailable
	}
	if client == nil {
		client = http.DefaultClient
	}
	refreshCtx, cancel := context.WithTimeout(ctx, defaultRefreshTimeout)
	defer cancel()

	body, err := json.Marshal(refreshRequest{
		ClientID:     clientID,
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
	})
	if err != nil {
		return TokenData{}, fmt.Errorf("encode refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(refreshCtx, http.MethodPost, refreshTokenURL(), bytes.NewReader(body))
	if err != nil {
		return TokenData{}, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return TokenData{}, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return TokenData{}, fmt.Errorf("read refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return TokenData{}, classifyRefreshFailure(resp.StatusCode, respBody)
	}

	var refreshed refreshResponse
	if err := json.Unmarshal(respBody, &refreshed); err != nil {
		return TokenData{}, fmt.Errorf("parse refresh response: %w", err)
	}
	if refreshed.AccessToken == "" {
		return TokenData{}, errors.New("refresh response missing access token")
	}

	return TokenData{
		IDToken:      refreshed.IDToken,
		AccessToken:  refreshed.AccessToken,
		RefreshToken: firstNonEmpty(refreshed.RefreshToken, refreshToken),
	}, nil
}

// MergeRefresh applies refreshed token data onto an account record.
func MergeRefresh(account accounts.Account, tokens TokenData) accounts.Account {
	if tokens.AccessToken != "" {
		account.AccessToken = tokens.AccessToken
	}
	if tokens.RefreshToken != "" {
		account.RefreshToken = tokens.RefreshToken
	}
	if idToken := idTokenString(tokens.IDToken); idToken != "" {
		account.IDToken = idToken
	}

	claims := jwtClaims(account.AccessToken)
	account.AccountID = firstNonEmpty(
		tokens.AccountID,
		account.AccountID,
		stringClaim(claims, "https://api.openai.com/auth_account_id"),
		stringClaim(claims, "chatgpt_account_id"),
		stringClaim(claims, "account_id"),
	)
	account.Email = firstNonEmpty(
		account.Email,
		stringClaim(claims, "email"),
		stringClaim(claims, "https://api.openai.com/email"),
	)
	account.PlanType = firstNonEmpty(
		account.PlanType,
		stringClaim(claims, "chatgpt_plan_type"),
		stringClaim(claims, "https://api.openai.com/plan_type"),
	)
	return account
}

func refreshTokenURL() string {
	if value := os.Getenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE"); value != "" {
		return value
	}
	return defaultRefreshURL
}

func classifyRefreshFailure(status int, body []byte) error {
	code := refreshErrorCode(body)
	switch code {
	case "refresh_token_expired":
		return errors.New("refresh token expired; sign in again")
	case "refresh_token_reused":
		return errors.New("refresh token already used; sign in again")
	case "refresh_token_invalidated":
		return errors.New("refresh token revoked; sign in again")
	}
	if status == http.StatusUnauthorized {
		return errors.New("refresh token rejected; sign in again")
	}
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "unknown status"
	}
	return fmt.Errorf("refresh request returned HTTP %d (%s)", status, statusText)
}

func refreshErrorCode(body []byte) string {
	var payload struct {
		Error json.RawMessage `json:"error"`
		Code  string          `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if payload.Code != "" {
		return payload.Code
	}
	if len(payload.Error) == 0 {
		return ""
	}
	var asObject struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(payload.Error, &asObject); err == nil && asObject.Code != "" {
		return asObject.Code
	}
	var asString string
	if err := json.Unmarshal(payload.Error, &asString); err == nil {
		return asString
	}
	return ""
}
