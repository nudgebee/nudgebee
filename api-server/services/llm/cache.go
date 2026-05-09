package llm

import (
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
)

// cacheInvalidationPayload is the wire format for messages published to
// RabbitMqLLMCacheInvalidationExchange. The JSON shape MUST stay in sync
// with llm-server's consumer struct in
// llm/llm-server/api/cache_invalidation_mq.go (cacheInvalidationMessage).
//
// A typed struct is preferred over a map[string]any literal so the field
// name "account_ids" is grep-able and a round-trip test can lock the
// contract — see TestCacheInvalidationPayload_WireFormat in cache_test.go.
type cacheInvalidationPayload struct {
	AccountIds []string `json:"account_ids"`
}

// InvalidateLLMServerCacheForAccounts publishes a fan-out RabbitMQ message
// asking every llm-server replica to bust its integration-derived caches
// (MCP tool list, LLM provider credentials, etc.) for the supplied
// accounts.
//
// Fan-out via RabbitMQ is required because llm-server's CacheProvider
// defaults to in_memory and Redis is opt-in — an HTTP-based invalidation
// would only land on whichever single pod the load balancer happened to
// pick, leaving every other replica's cache stale.
//
// Best-effort: the publish runs asynchronously so a transient RabbitMQ
// outage cannot block (or fail) the originating integration mutation.
// Worst case the user waits for the existing per-cache TTL.
//
// Triggered by api-server after any successful integration mutation
// (create / update status / delete), regardless of integration type.
func InvalidateLLMServerCacheForAccounts(sc *security.RequestContext, accountIds []string) {
	if len(accountIds) == 0 {
		return
	}

	exchange := config.Config.RabbitMqLLMCacheInvalidationExchange
	if exchange == "" {
		sc.GetLogger().Debug("llm: cache invalidation skipped — no RabbitMqLLMCacheInvalidationExchange configured")
		return
	}

	// Capture the request logger for use inside the goroutine — sc may be
	// canceled by the time the goroutine runs. slog.Default works equally
	// well here and avoids holding the request context.
	logger := sc.GetLogger()
	payload := cacheInvalidationPayload{AccountIds: accountIds}

	go func() {
		// Recover from any panic in MqPublish or the publish path so a
		// surprise (nil-deref, malformed-config interface conversion, slog
		// handler panic) cannot bring down the api-server pod and drop
		// every in-flight HTTP request along with it. Mirrors the pattern
		// used by the Mq consumer paths.
		defer func() {
			if r := recover(); r != nil {
				logger.Error("llm: cache invalidation goroutine panicked — recovered",
					"panic", r,
					"exchange", exchange,
					"account_count", len(accountIds),
					"account_ids", accountIds)
			}
		}()

		if err := common.MqPublish(
			exchange,
			"", // routing key irrelevant for fanout exchanges
			payload,
			common.MqPublishWithExchangeType("fanout"),
		); err != nil {
			// Error level (not Warn): a publish failure means every
			// llm-server replica's caches stay stale for up to the per-cache
			// TTL — that's exactly the silent staleness this hook exists to
			// prevent. Including account_ids so support can correlate a
			// "agent showed me stale tools" report to the failing publish.
			logger.Error("llm: failed to publish cache invalidation (best-effort)",
				"error", err,
				"exchange", exchange,
				"account_count", len(accountIds),
				"account_ids", accountIds)
			return
		}
		logger.Info("llm: cache invalidation published",
			"exchange", exchange,
			"account_count", len(accountIds))
	}()
}
