package api

import (
	"log/slog"
	"runtime/debug"

	"nudgebee/llm/agents"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
)

// services-server (after every integration create, status change or delete)
// and relay-server (after registering an agent's datasources or changing their
// labels) send integration_changed (keys: account ids); the replica broadcast
// delivers it to every llm-server replica.
func init() {
	common.OnReplicaBroadcast(common.ChangeIntegration, common.ReplicaHandler{
		OnKeys: handleIntegrationChanged,
		// Not resynced: these caches are account-keyed and spread over
		// registered invalidators, some in the shared Redis store, which a
		// resync on every pod must not wipe. A change missed while
		// disconnected waits for the caches' expiry, as on the fanout.
		OnAll: func() {},
	})
}

// handleIntegrationChanged drops this replica's integration-derived caches
// for each distinct account. Only cache drops belong
// here, because it runs on every replica; the shared Redis entries go too,
// before this pod can re-read them. Once-per-service work for the same change
// comes as an llm-server task (services-server's sync_integration_kbs).
func handleIntegrationChanged(ids []string) {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		core.InvalidateAccountIntegrationCache(id)
		// The FinOps system prompt embeds the account's footprint (providers,
		// integrations, K8s agent connected?) in a block cached for hours, so it
		// needs busting here too — the AccountConfigSummary invalidation above
		// does not reach a render that already happened.
		agents.InvalidateFinOpsAccountContext(id)
	}
}

// ensureIntegrationKBs runs the KB-sync fast path (ensure) for one account or
// tenant (id) so a freshly saved integration with sync_knowledge_base=true (or any confluence
// integration) starts indexing within seconds instead of at the next periodic
// tick (KBSyncIntervalMinutes, default 30). A panic is logged instead of
// taking down the process; the periodic tick remains the safety net and the
// only path that runs reconcile. Idempotent, see EnsureIntegrationKBsForAccount
// and EnsureIntegrationKBsForTenant.
func ensureIntegrationKBs(id string, ensure func(id string)) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cache-invalidation: integration KB sync panicked", "id", id, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	ensure(id)
}
