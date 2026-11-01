package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/server/metrics"
)

// Session lifecycle for direct-mode MCP. Design: docs/mcp-sessions.md.

// mcpSessionTTL is an idle timeout: every use of a session pushes its close out
// again, so a long-running chat or run keeps its upstream session.
const mcpSessionTTL = 30 * time.Minute

const (
	mcpSweepInterval   = 5 * time.Minute
	mcpSweepBatch      = 100
	mcpStoreOpTimeout  = 2 * time.Second
	mcpTerminateTimout = 10 * time.Second
)

// mcpTerminateSlots caps concurrent upstream DELETEs, so a sweep that ends
// many sessions at once doesn't flood MCP servers or, for oauth2, their token
// endpoint.
var mcpTerminateSlots = make(chan struct{}, 8)

// mcpSessions is the session directory in use. InitMCPSessions switches it to
// Redis when configured; the default is per-pod memory.
var mcpSessions mcpSessionStore = newMemMCPSessionStore()

// mcpCredentialLoader reloads a direct-mode MCP integration's credentials, for
// ending a session whose opening request this pod never saw.
type mcpCredentialLoader interface {
	GetMCPIntegrationAuth(ctx context.Context, accountID, integrationID string) (db.MCPIntegrationAuth, error)
}

var mcpCredentials mcpCredentialLoader

// mcpLocalAuth keeps, per key, the auth fields of the latest request this pod
// served for it, so this pod can end that session without a DB round trip.
// Credentials stay in process memory, as before; they are never written to
// the shared store.
var mcpLocalAuth = struct {
	sync.Mutex
	m map[mcpSessionKey]mcpAuthEntry
}{m: map[mcpSessionKey]mcpAuthEntry{}}

type mcpAuthEntry struct {
	body []byte
	at   time.Time
}

// InitMCPSessions selects the session store and starts the idle sweeper. By
// default (MCP_SESSION_STORE=redis) the directory is shared by all relay pods,
// which a multi-pod relay needs for a chat to keep one session across calls.
func InitMCPSessions(cfg *config.Config, store any, logger *slog.Logger) {
	if l, ok := store.(mcpCredentialLoader); ok {
		mcpCredentials = l
	}
	if strings.EqualFold(strings.TrimSpace(cfg.MCP.SessionStore), "redis") {
		addr := fmt.Sprintf("%s:%d", cfg.Cache.Redis.Host, cfg.Cache.Redis.Port)
		client := redis.NewClient(&redis.Options{
			Addr:     addr,
			Username: cfg.Cache.Redis.Username,
			Password: cfg.Cache.Redis.Password,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := client.Ping(ctx).Err(); err != nil {
			logger.Error("MCP session store: redis unreachable at startup; calls fall back to per-pod memory until it is",
				"addr", addr, "error", err)
		}
		cancel()
		mcpSessions = &fallbackMCPSessionStore{
			primary:  newRedisMCPSessionStore(client, mcpSessionTTL),
			fallback: newMemMCPSessionStore(),
			logger:   logger,
		}
		logger.Info("MCP session store: redis (shared by relay pods)", "addr", addr)
	} else if strings.EqualFold(strings.TrimSpace(cfg.MCP.SessionStore), "memory") {
		logger.Info("MCP session store: memory (per pod; MCP_SESSION_STORE=memory); correct only with one relay pod")
	} else {
		logger.Warn("MCP session store: unknown MCP_SESSION_STORE value, using per-pod memory; correct only with one relay pod",
			"configured", cfg.MCP.SessionStore)
	}
	go mcpSessionSweeper(logger)
}

// ensureMCPSession returns the session to use for key, opening one with the
// initialize handshake when there is none. "" means the server is sessionless.
func ensureMCPSession(ctx context.Context, key mcpSessionKey, integrationID string, rawBody []byte, logger *slog.Logger) (string, error) {
	rememberMCPAuth(key, rawBody)

	if rec, ok, _ := mcpSessions.Get(ctx, key); ok {
		_ = mcpSessions.Touch(ctx, key)
		return rec.SessionID, nil
	}

	sessionID, err := performMCPInitialize(ctx, key.URL, rawBody)
	if err != nil {
		return "", err
	}
	ours := mcpSessionRecord{SessionID: sessionID, IntegrationID: integrationID}
	cur, stored, _ := mcpSessions.PutIfAbsent(ctx, key, ours)
	if !stored {
		// Another request (possibly on another pod) stored a session first:
		// share theirs, and end the one we just opened.
		if sessionID != "" && cur.SessionID != sessionID {
			go terminateMCPSession(key, ours, mcpAuthSnapshot(rawBody), logger)
		}
		return cur.SessionID, nil
	}
	if sessionID != "" {
		logger.Info("MCP session initialized", "url", key.URL)
	}
	return sessionID, nil
}

// dropMCPSession removes key only if it still holds sessionID, so a session
// another request has just stored is not discarded.
func dropMCPSession(ctx context.Context, key mcpSessionKey, sessionID string) {
	_, _ = mcpSessions.DeleteIf(ctx, key, sessionID)
}

// mcpSessionSweeper ends sessions idle past mcpSessionTTL.
func mcpSessionSweeper(logger *slog.Logger) {
	ticker := time.NewTicker(mcpSweepInterval)
	defer ticker.Stop()
	for range ticker.C {
		sweepIdleMCPSessions(context.Background(), time.Now(), logger)
	}
}

// sweepIdleMCPSessions claims idle sessions (each one by exactly one relay pod)
// and ends them upstream, then forgets stale local credentials. It returns
// once those DELETEs finish, which mcpTerminateSlots and the per-DELETE
// timeout bound.
func sweepIdleMCPSessions(ctx context.Context, now time.Time, logger *slog.Logger) {
	cutoff := now.Add(-mcpSessionTTL)
	var wg sync.WaitGroup
	for {
		claimed, err := mcpSessions.ClaimIdle(ctx, cutoff, mcpSweepBatch)
		if err != nil {
			logger.Error("MCP session sweep failed", "error", err)
		}
		for _, c := range claimed {
			wg.Add(1)
			go func() {
				defer wg.Done()
				terminateMCPSession(c.Key, c.Record, nil, logger)
			}()
		}
		if err != nil || len(claimed) < mcpSweepBatch {
			break
		}
	}
	wg.Wait()

	mcpLocalAuth.Lock()
	for k, e := range mcpLocalAuth.m {
		if e.at.Before(cutoff.Add(-5 * time.Minute)) {
			delete(mcpLocalAuth.m, k)
		}
	}
	mcpLocalAuth.Unlock()
}

// terminateMCPSession asks the upstream MCP server to end a session (MCP spec:
// HTTP DELETE with the Mcp-Session-Id header). authBody may be nil: the pod's
// own copy of the credentials is used, else they are reloaded from the
// integration. Servers may answer 404 (already gone) or 405 (client
// termination unsupported); anything that leaves the session open is logged
// and counted, and the server's own timeout is the backstop.
func terminateMCPSession(key mcpSessionKey, rec mcpSessionRecord, authBody []byte, logger *slog.Logger) {
	if rec.SessionID == "" {
		return // sessionless server: nothing to end upstream
	}
	mcpTerminateSlots <- struct{}{}
	defer func() { <-mcpTerminateSlots }()

	ctx, cancel := context.WithTimeout(context.Background(), mcpTerminateTimout)
	defer cancel()

	if authBody == nil {
		// Prefer the integration's stored credentials: this pod's copy may hold
		// a short-lived token (runbook-server sends OAuth2 as a bearer token).
		local := takeMCPAuth(key)
		b, err := loadMCPAuth(ctx, key, rec.IntegrationID)
		switch {
		case err == nil:
			authBody = b
		case local != nil:
			authBody = local
		case rec.IntegrationID == "":
			// Sessions opened by a caller that doesn't send integration_id yet
			// (e.g. during a rollout): expected, so not an error.
			logger.Warn("MCP session not ended upstream: no credentials on this pod and no integration id",
				"url", key.URL, "account_id", key.Account)
			metrics.MCPSessionCloseFailures.Add(ctx, 1, metric.WithAttributes(
				metrics.AttrAccount(key.Account), attribute.String("reason", "no_integration_id")))
			return
		default:
			recordMCPCloseFailure(ctx, logger, key, "credentials", err)
			return
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, key.URL, nil)
	if err != nil {
		recordMCPCloseFailure(ctx, logger, key, "request", err)
		return
	}
	req.Header.Set("Mcp-Session-Id", rec.SessionID)
	if err := injectMCPAuth(req, authBody); err != nil {
		recordMCPCloseFailure(ctx, logger, key, "credentials", err)
		return
	}
	resp, err := mcpHTTPClient.Do(req)
	if err != nil {
		recordMCPCloseFailure(ctx, logger, key, "request", err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		recordMCPCloseFailure(ctx, logger, key, "rejected", fmt.Errorf("DELETE returned HTTP %d", resp.StatusCode))
		return
	}
	// Info, not Debug: one line per closed session is how an operator sees closes
	// reach the MCP server (a 404 means it had already ended the session).
	sid := rec.SessionID
	if len(sid) > 8 {
		sid = sid[:8]
	}
	logger.Info("MCP session ended upstream", "url", key.URL, "account_id", key.Account,
		"mcp_session", sid, "status", resp.StatusCode)
}

// loadMCPAuth rebuilds the auth fields for an integration from Postgres. The
// integration must belong to the key's account and point at the key's URL, so
// stored credentials only ever go to the server they were configured for.
func loadMCPAuth(ctx context.Context, key mcpSessionKey, integrationID string) ([]byte, error) {
	if integrationID == "" {
		return nil, errors.New("no credentials on this pod and no integration id to reload them")
	}
	if mcpCredentials == nil {
		return nil, errors.New("no credential loader configured")
	}
	auth, err := mcpCredentials.GetMCPIntegrationAuth(ctx, key.Account, integrationID)
	if err != nil {
		return nil, err
	}
	if auth.URL != key.URL {
		return nil, errors.New("integration URL does not match the session's URL")
	}
	creds := map[string]string{}
	for k, v := range auth.AuthCredentials {
		creds[k] = v
	}
	b, err := json.Marshal(map[string]any{"body": map[string]any{"action_params": map[string]any{
		"auth_type":        auth.AuthType,
		"auth_credentials": creds,
	}}})
	if err != nil {
		return nil, err
	}
	return b, nil
}

func recordMCPCloseFailure(ctx context.Context, logger *slog.Logger, key mcpSessionKey, reason string, err error) {
	logger.Error("MCP session not ended upstream; the server's own timeout will end it",
		"url", key.URL, "account_id", key.Account, "reason", reason, "error", err)
	metrics.MCPSessionCloseFailures.Add(ctx, 1, metric.WithAttributes(
		metrics.AttrAccount(key.Account), attribute.String("reason", reason)))
}

func rememberMCPAuth(key mcpSessionKey, rawBody []byte) {
	snap := mcpAuthSnapshot(rawBody)
	mcpLocalAuth.Lock()
	mcpLocalAuth.m[key] = mcpAuthEntry{body: snap, at: time.Now()}
	mcpLocalAuth.Unlock()
}

func takeMCPAuth(key mcpSessionKey) []byte {
	mcpLocalAuth.Lock()
	defer mcpLocalAuth.Unlock()
	e, ok := mcpLocalAuth.m[key]
	if !ok {
		return nil
	}
	delete(mcpLocalAuth.m, key)
	return e.body
}

// fallbackMCPSessionStore runs on Redis and falls back to this pod's memory
// for any operation Redis fails. While Redis is down a multi-pod relay
// behaves as before the shared store (a chat may get a session per pod),
// which is why every fallback is logged and counted.
type fallbackMCPSessionStore struct {
	primary  mcpSessionStore
	fallback mcpSessionStore
	logger   *slog.Logger
	// downUntil (unix nanos) skips Redis for a while after a failure, so an
	// outage costs one timeout, not one per operation on every MCP call.
	downUntil atomic.Int64
}

// mcpStoreRetryAfter is how long Redis is skipped after it fails. Short: while
// skipped, chats on this pod open per-pod sessions (losing state), so a single
// blip should not cost long.
const mcpStoreRetryAfter = 10 * time.Second

func (f *fallbackMCPSessionStore) primaryDown() bool {
	return time.Now().UnixNano() < f.downUntil.Load()
}

func (f *fallbackMCPSessionStore) degrade(ctx context.Context, op string, err error) {
	if err != nil {
		f.downUntil.Store(time.Now().Add(mcpStoreRetryAfter).UnixNano())
		f.logger.Error("MCP session store: redis failed, using per-pod memory for 10s", "op", op, "error", err)
	}
	metrics.MCPSessionStoreFallbacks.Add(ctx, 1, metric.WithAttributes(attribute.String("op", op)))
}

func opCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, mcpStoreOpTimeout)
}

func (f *fallbackMCPSessionStore) Get(ctx context.Context, key mcpSessionKey) (mcpSessionRecord, bool, error) {
	if f.primaryDown() {
		f.degrade(ctx, "get", nil)
		return f.fallback.Get(ctx, key)
	}
	c, cancel := opCtx(ctx)
	defer cancel()
	rec, ok, err := f.primary.Get(c, key)
	if err != nil {
		f.degrade(ctx, "get", err)
		return f.fallback.Get(ctx, key)
	}
	return rec, ok, nil
}

func (f *fallbackMCPSessionStore) PutIfAbsent(ctx context.Context, key mcpSessionKey, rec mcpSessionRecord) (mcpSessionRecord, bool, error) {
	if f.primaryDown() {
		f.degrade(ctx, "put", nil)
		return f.fallback.PutIfAbsent(ctx, key, rec)
	}
	c, cancel := opCtx(ctx)
	defer cancel()
	cur, stored, err := f.primary.PutIfAbsent(c, key, rec)
	if err != nil {
		f.degrade(ctx, "put", err)
		return f.fallback.PutIfAbsent(ctx, key, rec)
	}
	return cur, stored, nil
}

func (f *fallbackMCPSessionStore) Touch(ctx context.Context, key mcpSessionKey) error {
	if f.primaryDown() {
		f.degrade(ctx, "touch", nil)
		return f.fallback.Touch(ctx, key)
	}
	c, cancel := opCtx(ctx)
	defer cancel()
	if err := f.primary.Touch(c, key); err != nil {
		f.degrade(ctx, "touch", err)
		return f.fallback.Touch(ctx, key)
	}
	return nil
}

func (f *fallbackMCPSessionStore) DeleteIf(ctx context.Context, key mcpSessionKey, sessionID string) (bool, error) {
	if f.primaryDown() {
		f.degrade(ctx, "delete", nil)
		return f.fallback.DeleteIf(ctx, key, sessionID)
	}
	c, cancel := opCtx(ctx)
	defer cancel()
	removed, err := f.primary.DeleteIf(c, key, sessionID)
	if err != nil {
		f.degrade(ctx, "delete", err)
		return f.fallback.DeleteIf(ctx, key, sessionID)
	}
	// A session stored in memory during an outage is removed there too.
	if r, _ := f.fallback.DeleteIf(ctx, key, sessionID); r {
		removed = true
	}
	return removed, nil
}

// ClaimIdle sweeps both, so sessions opened in memory during a Redis outage
// are still ended.
func (f *fallbackMCPSessionStore) ClaimIdle(ctx context.Context, cutoff time.Time, limit int) ([]claimedMCPSession, error) {
	local, _ := f.fallback.ClaimIdle(ctx, cutoff, limit)
	if f.primaryDown() {
		return local, nil
	}
	shared, err := f.primary.ClaimIdle(ctx, cutoff, limit)
	if err != nil {
		f.degrade(ctx, "claim", err)
	}
	return append(local, shared...), err
}
