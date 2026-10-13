package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/relay"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// MCP integration health (#40994).
//
// llm-server is the only writer of a health verdict: it records what it sees on
// the path Nubi itself uses (tools/list and tools/call through the relay). The
// verdicts live in integrations.health, keyed by scope (mcpHealthScope). Two
// database triggers reset the column to NULL ("not checked yet") whenever the
// integration's status or a config value changes; no other code writes it.
//
// A server that starts failing keeps its status for mcpHealthHold before it is
// reported down, so one slow moment doesn't swap the integration's tools for the
// placeholder (that changes the tool list sent to the model and discards the
// account's prompt cache). Once it is down, every pod skips its discovery on the
// shared retry curve (mcpDiscoveryRetryAfter) instead of a per-pod one that
// restarted on every rollout.

const (
	mcpHealthConnected   = "connected"
	mcpHealthUnreachable = "unreachable"
	mcpHealthAuthFailed  = "auth_failed"
	mcpHealthError       = "error"
)

// mcpHealthAllScopes is the scope of a direct integration: the relay calls the
// server itself, so the account is not on the path and one verdict covers all.
const mcpHealthAllScopes = "*"

// mcpHealthHold is how long an integration that was connected must keep failing
// before it is reported down.
const mcpHealthHold = 2 * time.Minute

// mcpSuspectProbeInterval caps the background discovery a timed-out tool call
// starts, per scope and across all pods.
const mcpSuspectProbeInterval = time.Minute

// mcpHealthReasonMax bounds the stored reason, which the Integrations page shows.
const mcpHealthReasonMax = 200

// mcpHealthScope returns the key a verdict is stored under: one for a direct
// integration, and one per account for a vm_agent integration, which is reached
// through that account's own agent.
func mcpHealthScope(cfg mcpIntegrationConfig, accountId string) string {
	if cfg.ConnectionMode == "vm_agent" {
		return accountId
	}
	return mcpHealthAllScopes
}

func isMCPHealthFailure(status string) bool {
	return status == mcpHealthUnreachable || status == mcpHealthAuthFailed || status == mcpHealthError
}

// mcpHealthEntry is one scope's verdict. An empty Status means none has been
// reached yet (an entry can exist only to rate-limit suspect probes).
type mcpHealthEntry struct {
	Status        string     `json:"status,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	ToolCount     *int       `json:"tool_count,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	FailingSince  *time.Time `json:"failing_since,omitempty"`
	LastProbeAt   *time.Time `json:"last_probe_at,omitempty"`
}

// mcpHealth is the integrations.health column: scope → verdict.
type mcpHealth map[string]mcpHealthEntry

type mcpOutcomeKind int

const (
	// mcpOutcomeIgnore says nothing about the server: the caller went away, or
	// the relay itself could not be reached.
	mcpOutcomeIgnore mcpOutcomeKind = iota
	mcpOutcomeSuccess
	mcpOutcomeFailure
	// mcpOutcomeSuspect is a tool call that timed out. A slow tool is not a
	// dead server, so it records nothing itself; it starts a probe instead.
	mcpOutcomeSuspect
)

type mcpOutcome struct {
	kind   mcpOutcomeKind
	status string
	reason string
	// toolCount is set (≥ 0) only for a successful discovery.
	toolCount int
	discovery bool
}

func mcpSuccess(discovery bool, toolCount int) mcpOutcome {
	return mcpOutcome{kind: mcpOutcomeSuccess, discovery: discovery, toolCount: toolCount}
}

func mcpFailure(status, reason string, discovery bool) mcpOutcome {
	return mcpOutcome{kind: mcpOutcomeFailure, status: status, reason: reason, discovery: discovery}
}

// mcpStatusError is an MCP server's own HTTP status, as the relay reports it in
// {"status_code": N, "data": ...}.
type mcpStatusError struct {
	Code int
	Body string
}

func (e *mcpStatusError) Error() string {
	return fmt.Sprintf("MCP server returned status %d: %s", e.Code, e.Body)
}

// classifyMCPOutcome turns the error of one tools/list (discovery) or
// tools/call through the relay into a health outcome. It matches error types
// and status codes; the one text match left is the relay's OAuth token-exchange
// failure, which reaches llm-server only inside a 502 body.
func classifyMCPOutcome(err error, discovery bool) mcpOutcome {
	if err == nil {
		return mcpOutcome{kind: mcpOutcomeSuccess, discovery: discovery}
	}
	if errors.Is(err, context.Canceled) {
		return mcpOutcome{kind: mcpOutcomeIgnore}
	}

	var relayErr *relay.StatusError
	if errors.As(err, &relayErr) {
		switch relayErr.Code {
		case http.StatusServiceUnavailable:
			return mcpFailure(mcpHealthUnreachable, "agent not connected", discovery)
		case http.StatusGatewayTimeout:
			if !discovery {
				return mcpOutcome{kind: mcpOutcomeSuspect}
			}
			return mcpFailure(mcpHealthUnreachable, "timeout waiting for agent", discovery)
		case http.StatusBadGateway:
			if strings.Contains(relayErr.Message, "OAuth token exchange failed") {
				return mcpFailure(mcpHealthAuthFailed, relayErr.Message, discovery)
			}
			return mcpFailure(mcpHealthUnreachable, relayErr.Message, discovery)
		case http.StatusUnauthorized, http.StatusForbidden:
			// The relay's own auth, not the MCP server's.
			return mcpOutcome{kind: mcpOutcomeIgnore}
		}
		if !discovery && relayErr.Code < 500 {
			// A rejected tool call (bad arguments) says nothing about the server.
			return mcpOutcome{kind: mcpOutcomeIgnore}
		}
		return mcpFailure(mcpHealthError, relayErr.Message, discovery)
	}

	var statusErr *mcpStatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.Code == http.StatusUnauthorized || statusErr.Code == http.StatusForbidden:
			return mcpFailure(mcpHealthAuthFailed, fmt.Sprintf("MCP server returned %d", statusErr.Code), discovery)
		case statusErr.Code >= 500 && !discovery:
			// Some servers answer a failed tool with 500. Counted as a failure,
			// one broken tool retried past the hold would report the whole
			// server down; the tools/list probe decides instead, as for a timeout.
			return mcpOutcome{kind: mcpOutcomeSuspect}
		case statusErr.Code >= 500:
			return mcpFailure(mcpHealthUnreachable, fmt.Sprintf("MCP server returned %d", statusErr.Code), discovery)
		case !discovery:
			// The server answered a tool call with a 4xx (bad arguments, unknown
			// tool): it is up, the call was wrong. Counting it would let two bad
			// calls two minutes apart report a working server down.
			return mcpOutcome{kind: mcpOutcomeIgnore}
		}
		return mcpFailure(mcpHealthError, fmt.Sprintf("MCP server returned %d", statusErr.Code), discovery)
	}

	// The relay could not be reached at all: not this integration's fault.
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return mcpOutcome{kind: mcpOutcomeIgnore}
	}

	// llm-server's own deadline. The relay waits longer for the MCP server
	// (120 s) than discovery does (15 s), so a server that accepts the
	// connection and never answers looks exactly like this.
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		if !discovery {
			return mcpOutcome{kind: mcpOutcomeSuspect}
		}
		return mcpFailure(mcpHealthUnreachable, "timeout", discovery)
	}

	// Any other transport failure of llm-server's own request to the relay
	// (connection reset or EOF when a relay pod stops mid-request, TLS): the
	// MCP server's failures reach us as relay status errors, handled above.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return mcpOutcome{kind: mcpOutcomeIgnore}
	}

	return mcpFailure(mcpHealthError, err.Error(), discovery)
}

// nextMCPHealth applies one outcome to a scope's verdict. write is false when
// nothing needs storing. authoritative (Check now) skips the hold.
func nextMCPHealth(prev mcpHealthEntry, o mcpOutcome, now time.Time, authoritative bool) (next mcpHealthEntry, write bool) {
	next = prev
	switch o.kind {
	case mcpOutcomeSuccess:
		if !o.discovery {
			// A successful tool call only matters while a failure is pending;
			// healthy traffic writes nothing.
			if prev.FailingSince == nil && !isMCPHealthFailure(prev.Status) {
				return prev, false
			}
		} else {
			count := o.toolCount
			next.ToolCount = &count
		}
		next.Status = mcpHealthConnected
		next.Reason = ""
		next.FailingSince = nil
		next.LastCheckedAt = &now
		return next, true

	case mcpOutcomeFailure:
		since := now
		if prev.FailingSince != nil {
			since = *prev.FailingSince
		}
		next.FailingSince = &since
		next.LastCheckedAt = &now
		next.Reason = sanitizeMCPHealthReason(o.reason)
		// A connected integration stays connected through the hold. One with no
		// verdict yet has no verified tools to protect, so it is reported at once.
		if prev.Status == mcpHealthConnected && now.Sub(since) < mcpHealthHold && !authoritative {
			return next, true
		}
		next.Status = o.status
		return next, true
	}
	return prev, false
}

var mcpReasonURL = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>]+`)

// sanitizeMCPHealthReason keeps a stored reason fit for every reader of the
// Integrations page: relay errors quote the request URL, whose path, query or
// user info can carry a token, so each URL is cut to its scheme and host.
func sanitizeMCPHealthReason(reason string) string {
	reason = mcpReasonURL.ReplaceAllStringFunc(reason, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "<url>"
		}
		return u.Scheme + "://" + u.Host
	})
	return truncate(reason, mcpHealthReasonMax)
}

// mcpHealthSkip reports whether discovery for a scope should not run now
// because the integration is down and its next retry isn't due, and if so how
// long the placeholder should be kept.
func mcpHealthSkip(e mcpHealthEntry, now time.Time) (time.Duration, bool) {
	if !isMCPHealthFailure(e.Status) || e.FailingSince == nil || e.LastCheckedAt == nil {
		return 0, false
	}
	wait := mcpDiscoveryRetryAfter(now.Sub(*e.FailingSince))
	elapsed := now.Sub(*e.LastCheckedAt)
	if elapsed >= wait {
		return 0, false
	}
	return wait - elapsed, true
}

// mcpHealthStore reads and writes integrations.health. A variable so tests can
// swap in an in-memory store.
type mcpHealthStore interface {
	// load returns the stored health of the given integrations; an
	// integration with none is absent from the map.
	load(integrationIDs []string) (map[string]mcpHealth, error)
	// pendingFailure reports whether the scope has a failure recorded, so a
	// successful tool call writes only when it clears one.
	pendingFailure(integrationID, scope string) (bool, error)
	// update locks the row, passes fn the scope's current entry, and stores
	// what fn returns when write is true. It does nothing for an integration
	// that is gone or no longer enabled.
	update(integrationID, scope string, fn func(prev mcpHealthEntry) (mcpHealthEntry, bool)) (prev, next mcpHealthEntry, written bool, err error)
}

var mcpHealthStoreInstance mcpHealthStore = dbMCPHealthStore{}

type dbMCPHealthStore struct{}

func (dbMCPHealthStore) load(integrationIDs []string) (map[string]mcpHealth, error) {
	out := map[string]mcpHealth{}
	if len(integrationIDs) == 0 {
		return out, nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := dbms.Db.Query(`SELECT id::text, health FROM integrations WHERE id = ANY($1::uuid[]) AND health IS NOT NULL`, pq.Array(integrationIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var h mcpHealth
		if err := json.Unmarshal(raw, &h); err != nil {
			slog.Warn("mcp-integration: unreadable health, ignoring it", "integration_id", id, "error", err)
			continue
		}
		out[id] = h
	}
	return out, rows.Err()
}

func (dbMCPHealthStore) pendingFailure(integrationID, scope string) (bool, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false, err
	}
	var pending bool
	err = dbms.Db.QueryRow(`SELECT COALESCE(health -> $2 ->> 'failing_since', '') <> '' FROM integrations WHERE id = $1::uuid`, integrationID, scope).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return pending, err
}

func (dbMCPHealthStore) update(integrationID, scope string, fn func(prev mcpHealthEntry) (mcpHealthEntry, bool)) (prev, next mcpHealthEntry, written bool, err error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return prev, next, false, err
	}
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		var status string
		var raw []byte
		// FOR NO KEY UPDATE, not FOR UPDATE: config-value inserts take a KEY
		// SHARE lock on this row for their foreign key and must not wait on us.
		err := tx.QueryRow(`SELECT status, health FROM integrations WHERE id = $1::uuid FOR NO KEY UPDATE`, integrationID).Scan(&status, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if status != "enabled" {
			return nil, nil
		}
		health := mcpHealth{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &health); err != nil {
				health = mcpHealth{}
			}
		}
		prev = health[scope]
		var write bool
		next, write = fn(prev)
		if !write {
			return nil, nil
		}
		health[scope] = next
		encoded, err := json.Marshal(health)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE integrations SET health = $2::jsonb WHERE id = $1::uuid`, integrationID, string(encoded)); err != nil {
			return nil, err
		}
		written = true
		return nil, nil
	})
	return prev, next, written, err
}

// MCPIntegrationHealthChanged tells every llm-server replica, this one
// included, to drop the MCP tools it cached for an integration whose health
// changed. Set by the api package (cache-invalidation fanout); nil in tests.
// accountIds is empty for a direct integration, meaning every account.
var MCPIntegrationHealthChanged func(integrationID string, accountIds []string)

// recordMCPHealth stores one outcome and handles a status change. It returns
// the scope's verdict after the outcome, and whether one is known.
func recordMCPHealth(accountId string, cfg mcpIntegrationConfig, o mcpOutcome, authoritative bool) (mcpHealthEntry, bool) {
	if o.kind == mcpOutcomeIgnore || o.kind == mcpOutcomeSuspect || cfg.IntegrationID == "" {
		return mcpHealthEntry{}, false
	}
	scope := mcpHealthScope(cfg, accountId)
	log := slog.With("integration_id", cfg.IntegrationID, "integration_name", cfg.IntegrationName, "account_id", accountId, "scope", scope)

	if o.kind == mcpOutcomeSuccess && !o.discovery {
		pending, err := mcpHealthStoreInstance.pendingFailure(cfg.IntegrationID, scope)
		if err != nil {
			log.Warn("mcp-integration: unable to read health", "error", err)
			return mcpHealthEntry{}, false
		}
		if !pending {
			return mcpHealthEntry{}, false
		}
	}

	now := time.Now()
	prev, next, written, err := mcpHealthStoreInstance.update(cfg.IntegrationID, scope, func(prev mcpHealthEntry) (mcpHealthEntry, bool) {
		return nextMCPHealth(prev, o, now, authoritative)
	})
	if err != nil {
		log.Warn("mcp-integration: unable to record health", "error", err)
		return mcpHealthEntry{}, false
	}
	if !written {
		return prev, prev.Status != ""
	}

	// Only crossing between up and down matters to the tool lists: a first
	// verdict of "connected" (new or reset integration) or a change of failure
	// kind leaves every cached list right.
	if isMCPHealthFailure(prev.Status) != isMCPHealthFailure(next.Status) {
		if isMCPHealthFailure(next.Status) {
			log.Error("mcp-integration: integration is down", "status", next.Status, "reason", next.Reason, "previous_status", prev.Status)
		} else {
			log.Info("mcp-integration: integration is back", "status", next.Status, "previous_status", prev.Status)
		}
		var accounts []string
		if scope != mcpHealthAllScopes {
			accounts = []string{scope}
		}
		evictMCPIntegrationTools(cfg.IntegrationID, accounts)
		if MCPIntegrationHealthChanged != nil {
			MCPIntegrationHealthChanged(cfg.IntegrationID, accounts)
		}
	} else if prev.FailingSince == nil && next.FailingSince != nil {
		// First failure of a connected integration: it is held as connected,
		// and this pod may still be serving its real tools for up to 30 min.
		// Rediscover within a minute, so the hold ends on this pod's own retry
		// even if the chat never calls the tool again.
		mcpIntegrationToolCacheInstance.shorten(accountId, mcpDiscoveryRetryMin)
	}
	return next, true
}

// evictMCPIntegrationTools drops every cached tool list that holds tools of the
// integration (only those accounts when accountIds is set), so the next lookup
// reads its health again.
func evictMCPIntegrationTools(integrationID string, accountIds []string) {
	mcpIntegrationToolCacheInstance.deleteIntegration(integrationID, accountIds)
}

// EvictMCPIntegrationTools is evictMCPIntegrationTools for the
// cache-invalidation consumer.
func EvictMCPIntegrationTools(integrationID string, accountIds []string) {
	evictMCPIntegrationTools(integrationID, accountIds)
}

// mcpSuspectProbes holds the scopes this pod is probing, so concurrent timed-out
// calls start one probe.
var mcpSuspectProbes sync.Map

// probeSuspectMCPIntegration runs one discovery in the background after a tool
// call timed out. Its 15 s tools/list decides: a hung server times out and is
// recorded as failing; a slow tool on a live server answers and stays connected.
// At most one probe per scope per mcpSuspectProbeInterval across all pods: the
// claim is made under the row lock.
func probeSuspectMCPIntegration(accountId string, cfg mcpIntegrationConfig) {
	key := cfg.IntegrationID + "/" + mcpHealthScope(cfg, accountId)
	if _, running := mcpSuspectProbes.LoadOrStore(key, struct{}{}); running {
		return
	}
	go func() {
		defer mcpSuspectProbes.Delete(key)
		defer func() {
			if r := recover(); r != nil {
				slog.Error("mcp-integration: suspect probe panicked", "integration_id", cfg.IntegrationID, "account_id", accountId, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		now := time.Now()
		_, _, claimed, err := mcpHealthStoreInstance.update(cfg.IntegrationID, mcpHealthScope(cfg, accountId), func(prev mcpHealthEntry) (mcpHealthEntry, bool) {
			if prev.LastProbeAt != nil && now.Sub(*prev.LastProbeAt) < mcpSuspectProbeInterval {
				return prev, false
			}
			prev.LastProbeAt = &now
			return prev, true
		})
		if err != nil || !claimed {
			return
		}
		discoverMCPTools(accountId, cfg)
	}()
}
