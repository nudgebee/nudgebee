package api

import (
	"context"
	"log/slog"
	"runtime/debug"
	"testing"

	"nudgebee/llm/agents"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/knowledge"
	"nudgebee/llm/tools/core"
)

// cacheInvalidationMessage is the wire format for messages published on
// RabbitMqLLMCacheInvalidationExchange. relay-server publishes one per
// agent-registered integration change with the account whose cache must be
// cleared (services-server sends its integration changes as the
// integration_changed replica broadcast instead, handled below).
// Both account_id and account_ids are accepted to allow callers to send a
// single value without wrapping it in a slice.
type cacheInvalidationMessage struct {
	AccountId  string   `json:"account_id"`
	AccountIds []string `json:"account_ids"`
}

// services-server sends integration_changed (keys: account ids) after every
// integration create, status change or delete; the replica broadcast delivers
// it to every llm-server replica.
func init() {
	common.OnReplicaBroadcast(common.ChangeIntegration, common.ReplicaHandler{
		OnKeys: func(ids []string) { handleIntegrationChanged(ids) },
		// Not resynced: these caches are account-keyed and spread over
		// registered invalidators, some in the shared Redis store, which a
		// resync on every pod must not wipe. A change missed while
		// disconnected waits for the caches' expiry, as on the fanout.
		OnAll: func() {},
	})
}

func init() {
	// Don't open RabbitMQ connections during unit tests — the consumer
	// goroutine would otherwise outlive the test binary and try to
	// reconnect indefinitely.
	if testing.Testing() {
		return
	}

	// Refuse to start the consumer if ServerName is unset. The fanout
	// queue name is "<exchange>_<ServerName>", so an empty ServerName
	// means every replica binds to the same queue ("<exchange>_") and
	// fanout collapses to round-robin — every cache invalidation message
	// reaches exactly one pod, the rest stay stale. A loud panic on
	// startup is strictly better than that silent failure mode.
	if config.Config.ServerName == "" {
		slog.Error("cache-invalidation: ServerName is empty — refusing to start fanout consumer (would round-robin instead of fan out)")
		panic("cache-invalidation: ServerName must be set; check the LLM_SERVER_NAME / HOSTNAME env var")
	}

	// MqFanoutSubscribe only registers a supervisor; connecting and consuming
	// happen in the background ("rbmq: consumer running" / GET /health/mq).
	slog.Info("cache-invalidation: registering fanout consumer",
		"exchange", config.Config.RabbitMqLLMCacheInvalidationExchange,
		"server_name", config.Config.ServerName)
	if err := common.MqFanoutSubscribe(
		config.Config.RabbitMqLLMCacheInvalidationExchange,
		processCacheInvalidationMessage,
	); err != nil {
		slog.Error("cache-invalidation: unable to register fanout consumer",
			"exchange", config.Config.RabbitMqLLMCacheInvalidationExchange, "error", err)
		return
	}
	slog.Info("cache-invalidation: fanout consumer supervisor started",
		"exchange", config.Config.RabbitMqLLMCacheInvalidationExchange)
}

// processCacheInvalidationMessage decodes a fanout message and clears the
// integration-derived caches for every account it lists. Returning nil
// always — invalid payloads are logged and dropped (Ack), since requeueing
// a malformed message would just loop forever.
func processCacheInvalidationMessage(_ context.Context, data []byte) error {
	common.MetricsApiRequestsTotal("cache_invalidation_mq")

	var msg cacheInvalidationMessage
	if err := common.UnmarshalJson(data, &msg); err != nil {
		slog.Error("cache-invalidation: unable to decode message — dropping",
			"error", err, "data", string(data))
		return nil
	}

	ids := msg.AccountIds
	if msg.AccountId != "" {
		ids = append(ids, msg.AccountId)
	}
	if len(ids) == 0 {
		slog.Warn("cache-invalidation: message had no account ids — dropping", "data", string(data))
		return nil
	}

	accounts := handleIntegrationChanged(ids)
	// relay-server's changes arrive only here, on every replica, so the
	// knowledge-base kick runs on every replica as it always has. services-
	// server's changes send it once, as a sync_integration_kbs task.
	for _, id := range accounts {
		go ensureIntegrationKBs(id, knowledge.EnsureIntegrationKBsForAccount)
	}
	slog.Info("cache-invalidation: account integration cache invalidated", "count", len(accounts))
	return nil
}

// handleIntegrationChanged drops this replica's integration-derived caches
// for each account and returns the distinct accounts. It serves both paths an
// integration change reaches every replica on: the integration_changed replica
// broadcast (services-server) and the RabbitMQ fanout above (relay-server).
// Only cache drops belong here, because it runs on every replica; the shared
// Redis entries go too, before this pod can re-read them.
func handleIntegrationChanged(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	accounts := make([]string, 0, len(ids))
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
		accounts = append(accounts, id)
	}
	return accounts
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
