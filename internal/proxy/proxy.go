package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gabewillen/codextra/internal/codexauth"
)

type Config struct {
	Upstream        string
	APIUpstream     string
	Store           *accounts.Store
	Logger          *slog.Logger
	OnAccountUpdate func(accounts.Account) error
}

func New(config Config) (*http.Server, error) {
	upstream, err := url.Parse(config.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parse upstream: %w", err)
	}
	if err := validateUpstreamURL(upstream); err != nil {
		return nil, fmt.Errorf("validate upstream: %w", err)
	}
	apiUpstreamValue := config.APIUpstream
	if apiUpstreamValue == "" {
		apiUpstreamValue = config.Upstream
	}
	apiUpstream, err := url.Parse(apiUpstreamValue)
	if err != nil {
		return nil, fmt.Errorf("parse api upstream: %w", err)
	}
	if err := validateUpstreamURL(apiUpstream); err != nil {
		return nil, fmt.Errorf("validate api upstream: %w", err)
	}
	handler := &handler{
		upstream:        upstream,
		apiUpstream:     apiUpstream,
		store:           config.Store,
		client:          http.DefaultClient,
		logger:          config.Logger,
		onAccountUpdate: config.OnAccountUpdate,
		refreshLocks:    newRefreshLocks(),
		refreshBurn:     newRefreshBurnGuard(),
	}
	if handler.logger == nil {
		handler.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}, nil
}

func validateUpstreamURL(upstream *url.URL) error {
	if upstream.Host == "" {
		return fmt.Errorf("missing host")
	}
	switch upstream.Scheme {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("unsupported scheme %q", upstream.Scheme)
	}
}

type handler struct {
	upstream        *url.URL
	apiUpstream     *url.URL
	store           *accounts.Store
	client          *http.Client
	logger          *slog.Logger
	onAccountUpdate func(accounts.Account) error
	refreshLocks    *refreshLocks
	refreshBurn     *refreshBurnGuard
}

// refreshBurnWindow is how long a reactively-refreshed account is barred from
// another forced refresh after the refreshed token still failed upstream.
const refreshBurnWindow = 60 * time.Second

// refreshBurnGuard prevents a doomed account from burning its single-use OAuth
// refresh token on every request. Each reactive 401 forces a refresh, which
// rotates the refresh token; if the freshly-issued token is also rejected by
// upstream, refreshing again only consumes another token without helping. The
// guard records such an unhelpful refresh per alias and suppresses further
// forced refreshes for refreshBurnWindow. Suppression keys on the refresh token
// value, so a rotation or a re-login (which changes the token) lifts it at once.
type refreshBurnGuard struct {
	mu      sync.Mutex
	byAlias map[string]refreshBurnRecord
}

type refreshBurnRecord struct {
	refreshToken string
	at           time.Time
}

func newRefreshBurnGuard() *refreshBurnGuard {
	return &refreshBurnGuard{byAlias: map[string]refreshBurnRecord{}}
}

// suppressed reports whether a forced refresh for alias should be skipped: a
// prior forced refresh produced this same refresh token, upstream still rejected
// it, and the window has not elapsed. An expired entry is dropped and a token
// mismatch (rotation/re-login) is never suppressed.
func (g *refreshBurnGuard) suppressed(alias, refreshToken string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.byAlias[alias]
	if !ok || rec.refreshToken != refreshToken {
		return false
	}
	if now.Sub(rec.at) >= refreshBurnWindow {
		delete(g.byAlias, alias)
		return false
	}
	return true
}

// markUnhelpful records that a forced refresh produced refreshToken yet upstream
// still returned 401, starting a suppression window for alias.
func (g *refreshBurnGuard) markUnhelpful(alias, refreshToken string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.byAlias[alias] = refreshBurnRecord{refreshToken: refreshToken, at: now}
}

// clear lifts any suppression for alias, called once it serves a non-401
// response again.
func (g *refreshBurnGuard) clear(alias string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.byAlias[alias]; ok {
		delete(g.byAlias, alias)
	}
}

type refreshLocks struct {
	mu      sync.Mutex
	byAlias map[string]*sync.Mutex
}

func newRefreshLocks() *refreshLocks {
	return &refreshLocks{byAlias: map[string]*sync.Mutex{}}
}

func (l *refreshLocks) withLock(alias string, fn func() (accounts.Account, error)) (accounts.Account, error) {
	l.mu.Lock()
	lock, ok := l.byAlias[alias]
	if !ok {
		lock = &sync.Mutex{}
		l.byAlias[alias] = lock
	}
	l.mu.Unlock()

	lock.Lock()
	defer lock.Unlock()
	return fn()
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__codextra/health" {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	if isWebSocket(r) {
		h.serveWebSocket(w, r)
		return
	}
	h.serveHTTP(w, r)
}

// proxyAttempt is a single authenticated transport round-trip awaiting
// disposition by serveProxied.
type proxyAttempt struct {
	resp *http.Response
	// onRetry releases the transport resources held by this attempt before the
	// loop retries (after a token refresh or account rotation).
	onRetry func()
	// deliver hands the final (non-retried) response to the client. It owns resp
	// and is responsible for closing it.
	deliver func()
}

// serveProxied runs the shared auth-aware proxy loop used by both the HTTP and
// WebSocket paths: it selects the active account, refreshes tokens proactively,
// runs the transport attempt, and then either recovers a 401 by refreshing the
// token once, rotates to another account on a usage-limit 429, or delivers the
// response. makeAttempt performs the transport-specific round-trip for the given
// account; on a transport-level failure it must log the cause and return a
// non-nil error, which becomes a 502.
func (h *handler) serveProxied(w http.ResponseWriter, r *http.Request, makeAttempt func(account accounts.Account) (*proxyAttempt, error)) {
	account, ok := h.store.Current(time.Now())
	if !ok {
		h.logger.Warn("no_eligible_account", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "codextra has no eligible account", http.StatusServiceUnavailable)
		return
	}

	tokenRefreshed := false
	for {
		var err error
		account, err = h.ensureFreshTokens(r.Context(), account)
		if err != nil {
			h.logger.Warn("token_refresh_proactive_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", err)
		}

		attempt, err := makeAttempt(account)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		if attempt.resp.StatusCode == http.StatusUnauthorized && !tokenRefreshed &&
			!h.refreshBurn.suppressed(account.Alias, account.RefreshToken, time.Now()) {
			attempt.onRetry()
			updated, refreshErr := h.refreshAccountTokens(r.Context(), account, true)
			if refreshErr != nil {
				h.logger.Warn("token_refresh_reactive_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", refreshErr)
				http.Error(w, refreshFailureResponse(refreshErr), http.StatusUnauthorized)
				return
			}
			h.logger.Info("token_refreshed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias)
			account = updated
			tokenRefreshed = true
			continue
		}

		// A 401 that still reports an invalidated session after we tried to
		// refresh cannot be fixed by codextra: the account was signed out
		// server-side and needs a fresh login. Its access token may not be
		// expired, so this is the only point we can detect it. Drop it from
		// rotation (which surfaces it in the tray as "Needs sign-in") and fail
		// over to another eligible account so requests keep working.
		if attempt.resp.StatusCode == http.StatusUnauthorized && tokenRefreshed && isSessionInvalidated(attempt.resp) {
			attempt.onRetry()
			h.refreshBurn.clear(account.Alias)
			next, rotated, markErr := h.store.MarkNeedsLogin(account.Alias, time.Now())
			if markErr != nil {
				h.logger.Warn("account_mark_needs_login_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", markErr)
				http.Error(w, "codextra account "+account.Alias+" needs sign-in: run codextra login "+account.Alias, http.StatusUnauthorized)
				return
			}
			h.logger.Warn("account_session_invalidated", "method", r.Method, "path", r.URL.Path, "alias", account.Alias)
			if !rotated {
				http.Error(w, "codextra account "+account.Alias+" needs sign-in: run codextra login "+account.Alias, http.StatusUnauthorized)
				return
			}
			if adopted, ok, adoptErr := h.adoptFromCodexAuthWithoutNotify(next, time.Now()); adoptErr != nil {
				h.logger.Warn("codex_auth_sync_failed", "alias", next.Alias, "error", adoptErr)
			} else if ok {
				h.logger.Info("codex_auth_synced", "alias", next.Alias)
				next = adopted
			}
			if err := h.notifyAccountUpdate(next); err != nil {
				h.logger.Warn("account_sync_failed", "method", r.Method, "path", r.URL.Path, "from", account.Alias, "to", next.Alias, "error", err)
			}
			h.logger.Info("account_failed_over", "method", r.Method, "path", r.URL.Path, "from", account.Alias, "to", next.Alias)
			account = next
			tokenRefreshed = false
			continue
		}

		// Update the burn guard before the final disposition: a 401 that survived
		// a forced refresh means the refresh was unhelpful, so suppress further
		// forced refreshes for this account; any non-401 response means its
		// credentials work again, so lift any suppression.
		if attempt.resp.StatusCode == http.StatusUnauthorized {
			if tokenRefreshed {
				h.refreshBurn.markUnhelpful(account.Alias, account.RefreshToken, time.Now())
				h.logger.Warn("token_refresh_unhelpful", "method", r.Method, "path", r.URL.Path, "alias", account.Alias)
			} else {
				h.logger.Warn("token_refresh_suppressed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias)
			}
		} else {
			h.refreshBurn.clear(account.Alias)
		}

		if attempt.resp.StatusCode != http.StatusTooManyRequests || !isUsageLimit(attempt.resp) {
			attempt.deliver()
			return
		}

		limit, resetAt := limitInfo(attempt.resp)
		attempt.onRetry()
		h.logger.Info("usage_limit_detected", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit, "reset_at", resetAt.Format(time.RFC3339))
		next, rotated, err := h.store.RotateFrom(account.Alias, limit, resetAt, time.Now())
		if err != nil {
			h.logger.Warn("account_rotation_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !rotated {
			h.logger.Warn("account_rotation_exhausted", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "limit", limit)
			http.Error(w, "all codextra accounts are usage limited", http.StatusTooManyRequests)
			return
		}
		// Attempt to adopt any fresher tokens from Codex's auth.json for the
		// rotation target before notifying. This prevents overwriting Codex's
		// copy of a secondary account with a stale refresh token from the
		// registry (which would make subsequent refresh attempts fail with
		// "refresh token already used" and prevent recovery via adopt).
		if adopted, ok, adoptErr := h.adoptFromCodexAuthWithoutNotify(next, time.Now()); adoptErr != nil {
			h.logger.Warn("codex_auth_sync_failed", "alias", next.Alias, "error", adoptErr)
		} else if ok {
			h.logger.Info("codex_auth_synced", "alias", next.Alias)
			next = adopted
		}
		if err := h.notifyAccountUpdate(next); err != nil {
			h.logger.Warn("account_sync_failed", "method", r.Method, "path", r.URL.Path, "from", account.Alias, "to", next.Alias, "limit", limit, "error", err)
		}
		h.logger.Info("account_rotated", "method", r.Method, "path", r.URL.Path, "from", account.Alias, "to", next.Alias, "limit", limit)
		account = next
		tokenRefreshed = false
	}
}

func (h *handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn("request_body_read_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	h.serveProxied(w, r, func(account accounts.Account) (*proxyAttempt, error) {
		resp, err := h.forward(r.Context(), r, body, account)
		if err != nil {
			h.logger.Warn("upstream_request_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", err)
			return nil, err
		}
		return &proxyAttempt{
			resp:    resp,
			onRetry: func() { drainAndClose(resp) },
			deliver: func() {
				captured, copyErr := copyResponse(w, resp, responseCaptureLimit(resp))
				h.logResponse(r, resp, account, time.Since(start), captured, copyErr)
			},
		}, nil
	})
}

func (h *handler) ensureFreshTokens(ctx context.Context, account accounts.Account) (accounts.Account, error) {
	if !codexauth.AccessTokenStale(account.AccessToken, time.Now()) {
		return account, nil
	}
	return h.refreshAccountTokens(ctx, account, false)
}

func (h *handler) refreshAccountTokens(ctx context.Context, account accounts.Account, force bool) (accounts.Account, error) {
	startingRefresh := account.RefreshToken
	return h.refreshLocks.withLock(account.Alias, func() (accounts.Account, error) {
		if latest, ok := h.store.Get(account.Alias); ok {
			account = latest
		}
		if startingRefresh != "" && account.RefreshToken != startingRefresh {
			return account, nil
		}
		now := time.Now()
		if !force && accessTokenReady(account.AccessToken, now) {
			return account, nil
		}

		if adopted, ok, err := h.adoptFromCodexAuth(account, now); err != nil {
			h.logger.Warn("codex_auth_sync_failed", "alias", account.Alias, "error", err)
		} else if ok {
			h.logger.Info("codex_auth_synced", "alias", account.Alias)
			account = adopted
			if accessTokenReady(account.AccessToken, now) {
				return account, nil
			}
		}

		if account.RefreshToken == "" {
			return account, fmt.Errorf("account %q has no refresh token; run codextra login %s", account.Alias, account.Alias)
		}

		tokens, err := codexauth.Refresh(ctx, h.client, account.RefreshToken)
		if err != nil {
			if accessTokenReady(account.AccessToken, now) {
				h.logger.Info("token_refresh_skipped_using_adopted", "alias", account.Alias)
				return account, nil
			}
			if codexauth.IsRecoverableRefreshFailure(err) {
				if adopted, ok, adoptErr := h.adoptFromCodexAuth(account, now); adoptErr != nil {
					h.logger.Warn("codex_auth_sync_failed", "alias", account.Alias, "error", adoptErr)
				} else if ok && accessTokenReady(adopted.AccessToken, now) {
					h.logger.Info("codex_auth_synced_after_refresh_failure", "alias", account.Alias)
					return adopted, nil
				}
			}
			return account, err
		}
		updated := codexauth.MergeRefresh(account, tokens)
		persisted, err := h.store.UpdateTokens(account.Alias, updated)
		if err != nil {
			h.logger.Warn("token_refresh_persist_failed", "alias", account.Alias, "error", err)
			persisted = updated
		}
		if err := h.notifyAccountUpdate(persisted); err != nil {
			h.logger.Warn("account_sync_failed", "alias", account.Alias, "error", err)
		}
		return persisted, nil
	})
}

func (h *handler) adoptFromCodexAuth(account accounts.Account, now time.Time) (accounts.Account, bool, error) {
	return h.adoptFromCodexAuthWithNotify(account, now, true)
}

func (h *handler) adoptFromCodexAuthWithoutNotify(account accounts.Account, now time.Time) (accounts.Account, bool, error) {
	return h.adoptFromCodexAuthWithNotify(account, now, false)
}

func (h *handler) adoptFromCodexAuthWithNotify(account accounts.Account, now time.Time, notify bool) (accounts.Account, bool, error) {
	adopted, ok, err := codexauth.AdoptFromCodexAuth(account, now)
	if err != nil || !ok {
		return account, ok, err
	}
	persisted, err := h.store.UpdateTokens(account.Alias, adopted)
	if err != nil {
		h.logger.Warn("codex_auth_sync_persist_failed", "alias", account.Alias, "error", err)
		persisted = adopted
	}
	if notify {
		if err := h.notifyAccountUpdate(persisted); err != nil {
			h.logger.Warn("account_sync_failed", "alias", account.Alias, "error", err)
		}
	}
	return persisted, true, nil
}

func accessTokenReady(accessToken string, now time.Time) bool {
	return codexauth.AccessTokenExpiresKnown(accessToken) && !codexauth.AccessTokenStale(accessToken, now)
}

func refreshFailureResponse(err error) string {
	msg := codexauth.RefreshFailureMessage(err)
	if msg == "" {
		return "codextra could not refresh expired account token"
	}
	return "codextra could not refresh expired account token: " + msg
}

func (h *handler) notifyAccountUpdate(account accounts.Account) error {
	if h.onAccountUpdate == nil {
		return nil
	}
	return h.onAccountUpdate(account)
}

func (h *handler) logResponse(r *http.Request, resp *http.Response, account accounts.Account, elapsed time.Duration, body []byte, copyErr error) {
	args := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"query_present", r.URL.RawQuery != "",
		"alias", account.Alias,
		"upstream_host", h.upstreamFor(r.URL.Path).Host,
		"status", resp.StatusCode,
		"content_type", resp.Header.Get("content-type"),
		"duration_ms", elapsed.Milliseconds(),
	}
	if len(body) > 0 {
		args = append(args,
			"usage_marker", usageLimitMarker(resp.Header, body),
			"body_prefix", compactPrefix(body, 512),
		)
	}
	if copyErr != nil {
		args = append(args, "copy_error", copyErr)
	}
	h.logger.Info("upstream_response", args...)
}

func (h *handler) forward(ctx context.Context, original *http.Request, body []byte, account accounts.Account) (*http.Response, error) {
	upstream := h.upstreamFor(original.URL.Path)
	target := *upstream
	target.Path = singleJoiningSlash(upstream.Path, original.URL.Path)
	target.RawQuery = original.URL.RawQuery

	req, err := http.NewRequestWithContext(ctx, original.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = original.Header.Clone()
	req.Host = upstream.Host
	applyAuthHeaders(req.Header, account)
	return h.client.Do(req)
}

func applyAuthHeaders(header http.Header, account accounts.Account) {
	header.Set("Authorization", "Bearer "+account.AccessToken)
	if account.AccountID != "" {
		header.Set("ChatGPT-Account-ID", account.AccountID)
	} else {
		header.Del("ChatGPT-Account-ID")
	}
}

func (h *handler) upstreamFor(path string) *url.URL {
	if path == "/v1" || strings.HasPrefix(path, "/v1/") {
		return h.apiUpstream
	}
	return h.upstream
}

func (h *handler) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	h.serveProxied(w, r, func(account accounts.Account) (*proxyAttempt, error) {
		upstreamConn, upstreamReader, upstreamReq, resp, err := h.openWebSocket(r.Context(), r, account)
		if err != nil {
			h.logger.Warn("websocket_upstream_failed", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "error", err)
			return nil, err
		}
		return &proxyAttempt{
			resp: resp,
			onRetry: func() {
				resp.Body.Close()
				upstreamConn.Close()
			},
			deliver: func() {
				// A successful upgrade (101) is the WebSocket terminal case:
				// hand the hijacked connection to the bidirectional tunnel,
				// which owns and closes upstreamConn.
				if resp.StatusCode == http.StatusSwitchingProtocols {
					h.logger.Info("websocket_upgraded", "method", r.Method, "path", r.URL.Path, "alias", account.Alias, "upstream_host", upstreamReq.Host, "duration_ms", time.Since(start).Milliseconds())
					h.tunnelWebSocket(w, r, upstreamConn, upstreamReader, resp)
					return
				}
				captured, copyErr := copyResponse(w, resp, responseCaptureLimit(resp))
				h.logResponse(r, resp, account, time.Since(start), captured, copyErr)
				upstreamConn.Close()
			},
		}, nil
	})
}

func (h *handler) openWebSocket(ctx context.Context, original *http.Request, account accounts.Account) (net.Conn, *bufio.Reader, *http.Request, *http.Response, error) {
	upstream := h.upstreamFor(original.URL.Path)
	target := *upstream
	target.Path = singleJoiningSlash(upstream.Path, original.URL.Path)
	target.RawQuery = original.URL.RawQuery

	req := original.Clone(ctx)
	req.URL = &target
	req.RequestURI = ""
	req.Host = upstream.Host
	req.Header = original.Header.Clone()
	applyAuthHeaders(req.Header, account)

	conn, err := dialURL(ctx, upstream)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, nil, nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		conn.Close()
		return nil, nil, nil, nil, err
	}
	return conn, reader, req, resp, nil
}

func dialURL(ctx context.Context, target *url.URL) (net.Conn, error) {
	addr := target.Host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		switch target.Scheme {
		case "https", "wss":
			addr = net.JoinHostPort(target.Host, "443")
		default:
			addr = net.JoinHostPort(target.Host, "80")
		}
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "https" && target.Scheme != "wss" {
		return conn, nil
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: target.Hostname(), MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (h *handler) tunnelWebSocket(w http.ResponseWriter, r *http.Request, upstreamConn net.Conn, upstreamReader *bufio.Reader, resp *http.Response) {
	defer upstreamConn.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		resp.Body.Close()
		http.Error(w, "response writer cannot hijack connection", http.StatusInternalServerError)
		return
	}
	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		resp.Body.Close()
		h.logger.Warn("websocket_hijack_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return
	}
	defer clientConn.Close()

	if err := resp.Write(clientRW); err != nil {
		h.logger.Warn("websocket_response_write_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return
	}
	if err := clientRW.Flush(); err != nil {
		h.logger.Warn("websocket_response_flush_failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return
	}
	resp.Body.Close()

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = clientConn.Close()
			_ = upstreamConn.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer closeBoth()
		if upstreamReader.Buffered() > 0 {
			_, _ = io.CopyN(clientConn, upstreamReader, int64(upstreamReader.Buffered()))
		}
		_, _ = io.Copy(clientConn, upstreamConn)
	}()
	go func() {
		defer wg.Done()
		defer closeBoth()
		if clientRW.Reader.Buffered() > 0 {
			_, _ = io.CopyN(upstreamConn, clientRW.Reader, int64(clientRW.Reader.Buffered()))
		}
		_, _ = io.Copy(upstreamConn, clientConn)
	}()
	wg.Wait()
}

// maxDrainOnRetry bounds how much of an unused upstream body drainAndClose
// reads back before closing. net/http only returns a connection to its pool
// once the body is read to EOF, but we cap the drain so an oversized or hostile
// body can't stall the retry.
const maxDrainOnRetry = 16 << 10

// drainAndClose discards up to maxDrainOnRetry bytes of an unread response body
// and then closes it, letting the transport reuse the connection for the retry
// instead of tearing it down and re-dialing.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.CopyN(io.Discard, resp.Body, maxDrainOnRetry)
	resp.Body.Close()
}

func copyResponse(w http.ResponseWriter, resp *http.Response, captureLimit int) ([]byte, error) {
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	capture := &prefixCapture{limit: captureLimit}
	_, err := io.Copy(io.MultiWriter(w, capture), resp.Body)
	return capture.bytes, err
}

type prefixCapture struct {
	limit int
	bytes []byte
}

func (c *prefixCapture) Write(p []byte) (int, error) {
	if c.limit > 0 && len(c.bytes) < c.limit {
		remaining := c.limit - len(c.bytes)
		if len(p) < remaining {
			remaining = len(p)
		}
		c.bytes = append(c.bytes, p[:remaining]...)
	}
	return len(p), nil
}

func copyHeader(dst, src http.Header) {
	for k, values := range src {
		for _, value := range values {
			dst.Add(k, value)
		}
	}
}

func isUsageLimit(resp *http.Response) bool {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return usageLimitMarker(resp.Header, body)
}

// isSessionInvalidated reports whether a 401 response says the account was
// signed out server-side (e.g. signing in elsewhere revoked the session). Such
// a token cannot be recovered by refreshing — even though its JWT may not be
// expired — so the only fix is to re-authenticate the account. The body is
// rebuffered so callers can still read it.
func isSessionInvalidated(resp *http.Response) bool {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return false
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	switch responseErrorCode(body) {
	case "token_invalidated", "token_revoked":
		return true
	default:
		return false
	}
}

func responseErrorCode(body []byte) string {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return payload.Error.Code
}

func limitInfo(resp *http.Response) (string, time.Time) {
	limit := resp.Header.Get("x-codex-active-limit")
	if limit == "" {
		limit = "codex"
	}
	resetAt := time.Time{}
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		var payload struct {
			Error struct {
				ResetsAt int64 `json:"resets_at"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &payload) == nil && payload.Error.ResetsAt > 0 {
			resetAt = time.Unix(payload.Error.ResetsAt, 0)
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}
	return limit, resetAt
}

func usageLimitMarker(header http.Header, body []byte) bool {
	if limit := strings.TrimSpace(header.Get("x-codex-active-limit")); limit != "" {
		return jsonHasStringValue(body, "usage_limit_reached")
	}
	return jsonHasStringValue(body, "usage_limit_reached")
}

func jsonHasStringValue(body []byte, want string) bool {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return false
	}
	return hasStringValue(value, want)
}

func hasStringValue(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []any:
		for _, item := range typed {
			if hasStringValue(item, want) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if hasStringValue(item, want) {
				return true
			}
		}
	}
	return false
}

func responseCaptureLimit(resp *http.Response) int {
	if resp.StatusCode >= http.StatusBadRequest {
		return 4 * 1024
	}
	return 0
}

func compactPrefix(body []byte, max int) string {
	if len(body) > max {
		body = body[:max]
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > max {
		return text[:max]
	}
	return text
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}
