package relay

import (
	"log/slog"
	"nudgebee/runbook/common"
	"sync"
	"time"
)

// k8sAgentType is the agent row the relay routes a request to when the caller
// leaves ActionExecuteBody.AgentType empty (the relay defaults the missing
// X-NB-Agent-Type header to "k8s"). An account's other agent rows — proxy, and
// the AWS/Azure/GCP cloud-sync rows — are separate registrations that cannot
// serve an in-cluster action, so connectivity is per (account, type), never
// per account.
const k8sAgentType = "k8s"

// agentStatusConnected is the one `agent.status` value the relay treats as
// routable; every other value (NOT_CONNECTED, an empty status, and any future
// state) is not. Compared case-insensitively for the same reason as `type`
// below: both are free-form text columns, and a row ever written `Connected`
// must not read as "no agent" and gate traffic that would have worked.
const agentStatusConnected = "CONNECTED"

// Agent-connectivity cache TTLs. The relay server keeps the same answer in its
// own cache for 30 min (connected) / 2 min (not connected), so a caller can
// never observe a *fresher* answer by making the round-trip anyway. Staying
// under the relay's 2-minute negative TTL means this gate cannot delay an
// agent's reconnect beyond what the relay itself already imposes.
const (
	agentConnectedCacheTTL    = 60 * time.Second
	agentNotConnectedCacheTTL = 30 * time.Second
)

type agentStatusCacheEntry struct {
	connected bool
	expiresAt time.Time
}

// agentStatusCache is intentionally unevicted. Its key space is closed, not
// open: keys are the cloud-account ids this process serves, and repeat lookups
// overwrite their entry rather than adding one.
var agentStatusCache = struct {
	sync.RWMutex
	entries map[string]agentStatusCacheEntry
}{entries: make(map[string]agentStatusCacheEntry)}

// AgentConnected reports whether the account's in-cluster k8s agent is currently
// registered with the relay.
func AgentConnected(accountID string) bool {
	if accountID == "" {
		return false
	}

	agentStatusCache.RLock()
	if entry, ok := agentStatusCache.entries[accountID]; ok && time.Now().Before(entry.expiresAt) {
		agentStatusCache.RUnlock()
		return entry.connected
	}
	agentStatusCache.RUnlock()

	connected := queryAgentConnected(accountID)

	ttl := agentNotConnectedCacheTTL
	if connected {
		ttl = agentConnectedCacheTTL
	}
	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{
		connected: connected,
		expiresAt: time.Now().Add(ttl),
	}
	agentStatusCache.Unlock()

	return connected
}

type queryGetter interface {
	Get(dest any, query string, args ...any) error
}

func queryAgentConnected(accountID string) bool {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("relay: agent status lookup: no database manager", "account_id", accountID, "error", err)
		return true // fail open
	}
	return agentConnectedInDb(dbms.Db, accountID)
}

func agentConnectedInDb(db queryGetter, accountID string) bool {
	var connectedCount int
	if err := db.Get(&connectedCount,
		`select count(*) from agent where upper(status) = $1 and lower(type) = $2 and cloud_account_id = $3`,
		agentStatusConnected, k8sAgentType, accountID); err != nil {
		slog.Warn("relay: agent status lookup failed; assuming connected", "account_id", accountID, "error", err)
		return true // fail open
	}
	return connectedCount > 0
}
