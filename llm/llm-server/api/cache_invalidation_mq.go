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
// RabbitMqLLMCacheInvalidationExchange. api-server publishes one message
// per integration mutation containing every cloud account whose cache
// must be cleared. Both account_id and account_ids are accepted to allow
// callers to send a single value without wrapping it in a slice.
type cacheInvalidationMessage struct {
	AccountId  string   `json:"account_id"`
	AccountIds []string `json:"account_ids"`
	// KnowledgeBases marks a change to a tenant's organisation knowledge
	// bases (published by llm-server). Its caches are keyed per account, so
	// every replica clears them for all accounts; no integration work runs.
	KnowledgeBases bool   `json:"knowledge_bases,omitempty"`
	TenantId       string `json:"tenant_id,omitempty"`
	// McpIntegrationId marks a change of one MCP integration's health
	// (published by llm-server, #40994). Replicas drop only the MCP tool lists
	// holding that integration — in AccountIds, or in every account when it is
	// empty — instead of running the full per-account refresh below.
	McpIntegrationId string `json:"mcp_integration_id,omitempty"`
}

// publishMCPIntegrationHealthChange tells every replica (this one included)
// that an MCP integration's health changed.
func publishMCPIntegrationHealthChange(integrationId string, accountIds []string) {
	msg := cacheInvalidationMessage{McpIntegrationId: integrationId, AccountIds: accountIds}
	if err := common.MqPublishFanout(config.Config.RabbitMqLLMCacheInvalidationExchange, msg); err != nil {
		slog.Error("cache-invalidation: unable to publish MCP integration health change", "error", err, "integration_id", integrationId)
	}
}

// publishOrgKnowledgeInvalidation tells every replica (this one included)
// that a tenant's organisation knowledge bases changed.
func publishOrgKnowledgeInvalidation(tenantId string) {
	msg := cacheInvalidationMessage{KnowledgeBases: true, TenantId: tenantId}
	if err := common.MqPublishFanout(config.Config.RabbitMqLLMCacheInvalidationExchange, msg); err != nil {
		slog.Error("cache-invalidation: unable to publish knowledge base invalidation", "error", err, "tenant_id", tenantId)
	}
}

func init() {
	// Don't open RabbitMQ connections during unit tests — the consumer
	// goroutine would otherwise outlive the test binary and try to
	// reconnect indefinitely.
	if testing.Testing() {
		return
	}

	knowledge.OrgKnowledgeCacheInvalidator = publishOrgKnowledgeInvalidation
	core.MCPIntegrationHealthChanged = publishMCPIntegrationHealthChange

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

	if msg.McpIntegrationId != "" {
		core.EvictMCPIntegrationTools(msg.McpIntegrationId, msg.AccountIds)
		return nil
	}

	if msg.KnowledgeBases {
		// Not an integration change: do not run the per-account integration
		// refresh and KB sync below.
		knowledge.ClearKnowledgeBaseCaches()
		slog.Info("cache-invalidation: knowledge base caches cleared", "tenant_id", msg.TenantId)
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

	seen := make(map[string]struct{}, len(ids))
	invalidated := 0
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
		// Best-effort: kick the per-account KB-sync fast path so a freshly-
		// saved integration with sync_knowledge_base=true (or any confluence
		// integration) starts indexing within seconds instead of waiting up
		// to KBSyncIntervalMinutes (default 30) for the next periodic tick.
		// Idempotent — see EnsureIntegrationKBsForAccount. The periodic tick
		// remains the safety net and is the only path that runs reconcile,
		// so disable/delete eventually consistent on the same window.
		// Dispatched in a goroutine because the call does an HTTP POST to
		// rag-server (triggerIntegrationKBSync); blocking the MQ consumer on
		// rag-server health would back up the fanout queue. The callee owns
		// its own context timeout.
		go ensureIntegrationKBsInBackground(id, knowledge.EnsureIntegrationKBsForAccount)
		invalidated++
	}
	slog.Info("cache-invalidation: account integration cache invalidated", "count", invalidated)
	return nil
}

// ensureIntegrationKBsInBackground runs the KB-sync fast path (ensure) on its
// own goroutine. A panic there would take down the process, so it is logged
// instead; the periodic sync tick remains the safety net.
func ensureIntegrationKBsInBackground(accountID string, ensure func(accountID string)) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("cache-invalidation: integration KB sync panicked", "account_id", accountID, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	ensure(accountID)
}
