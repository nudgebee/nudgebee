package api

import (
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"testing"
)

// cacheInvalidationMessage is the wire format for messages published on
// RabbitMqLLMCacheInvalidationExchange. api-server publishes one message
// per integration mutation containing every cloud account whose cache
// must be cleared. Both account_id and account_ids are accepted to allow
// callers to send a single value without wrapping it in a slice.
type cacheInvalidationMessage struct {
	AccountId  string   `json:"account_id"`
	AccountIds []string `json:"account_ids"`
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

	// "starting" log so a hang inside MqFanoutSubscribe (TCP dial / AMQP
	// handshake / queue declare) is visible in container logs even if
	// the "registered" line below never prints.
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
	slog.Info("cache-invalidation: fanout consumer registered",
		"exchange", config.Config.RabbitMqLLMCacheInvalidationExchange)
}

// processCacheInvalidationMessage decodes a fanout message and clears the
// integration-derived caches for every account it lists. Returning nil
// always — invalid payloads are logged and dropped (Ack), since requeueing
// a malformed message would just loop forever.
func processCacheInvalidationMessage(data []byte) error {
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
		go core.EnsureIntegrationKBsForAccount(id)
		invalidated++
	}
	slog.Info("cache-invalidation: account integration cache invalidated", "count", invalidated)
	return nil
}
