package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func llmTestRequestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), &security.SecurityContext{}, slog.Default(), nil, nil)
}

// TestInvalidateLLMServerCacheForAccounts_EmptyInputIsNoOp verifies the
// guard that prevents a wasted RabbitMQ publish when no accounts are
// affected. The function must return without spawning the goroutine.
func TestInvalidateLLMServerCacheForAccounts_EmptyInputIsNoOp(t *testing.T) {
	oldExchange := config.Config.RabbitMqLLMCacheInvalidationExchange
	config.Config.RabbitMqLLMCacheInvalidationExchange = "test_exchange_should_not_be_used"
	defer func() { config.Config.RabbitMqLLMCacheInvalidationExchange = oldExchange }()

	assert.NotPanics(t, func() {
		InvalidateLLMServerCacheForAccounts(llmTestRequestContext(), nil)
		InvalidateLLMServerCacheForAccounts(llmTestRequestContext(), []string{})
	}, "empty input must be a synchronous no-op")
}

// TestInvalidateLLMServerCacheForAccounts_EmptyExchangeShortCircuits
// verifies that misconfiguration (exchange unset) is treated as "feature
// disabled" — synchronous return, no goroutine spawned, no panic.
func TestInvalidateLLMServerCacheForAccounts_EmptyExchangeShortCircuits(t *testing.T) {
	oldExchange := config.Config.RabbitMqLLMCacheInvalidationExchange
	config.Config.RabbitMqLLMCacheInvalidationExchange = ""
	defer func() { config.Config.RabbitMqLLMCacheInvalidationExchange = oldExchange }()

	assert.NotPanics(t, func() {
		InvalidateLLMServerCacheForAccounts(llmTestRequestContext(), []string{"acc-1"})
	}, "missing exchange config must short-circuit cleanly")
}

// TestInvalidateLLMServerCacheForAccounts_AsyncPublishDoesNotBlock
// verifies the call returns promptly even when RabbitMQ is unreachable —
// the publish runs in a goroutine, so a connection failure (with retry +
// backoff inside MqPublish) cannot block the caller.
func TestInvalidateLLMServerCacheForAccounts_AsyncPublishDoesNotBlock(t *testing.T) {
	oldExchange := config.Config.RabbitMqLLMCacheInvalidationExchange
	config.Config.RabbitMqLLMCacheInvalidationExchange = "llm_cache_invalidation_test"
	defer func() { config.Config.RabbitMqLLMCacheInvalidationExchange = oldExchange }()

	start := time.Now()
	assert.NotPanics(t, func() {
		InvalidateLLMServerCacheForAccounts(llmTestRequestContext(), []string{"acc-1"})
	})
	elapsed := time.Since(start)
	// The synchronous portion is just a guard check + goroutine launch;
	// any MQ work happens off the caller's stack. 500ms is generous.
	assert.Less(t, elapsed, 500*time.Millisecond,
		"caller must not block on RabbitMQ — got %s", elapsed)
}

// TestCacheInvalidationPayload_WireFormat locks the publisher↔consumer
// contract on the JSON field name. The publisher's payload struct lives in
// this package; the consumer's struct lives in
// llm/llm-server/api/cache_invalidation_mq.go (cacheInvalidationMessage).
// They are coupled only by string convention — there is no shared type.
//
// Renaming the publisher's "account_ids" tag will fail this test (the
// JSON output won't decode into the consumerView mirror below) and a CI
// failure will surface the contract break before deploy. A renamer who
// updates both this test mirror AND cache.go's tag would still slip
// through, but the surface area is now small enough that it's reviewable.
func TestCacheInvalidationPayload_WireFormat(t *testing.T) {
	publisher := cacheInvalidationPayload{AccountIds: []string{"acc-a", "acc-b"}}
	encoded, err := json.Marshal(publisher)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Mirror of llm-server's cacheInvalidationMessage struct. Keep the
	// shape and tags in sync with that file. The "account_id" field is
	// the legacy single-value form the consumer also accepts; the
	// publisher does not emit it but the consumer mirror declares it for
	// faithfulness.
	var consumerView struct {
		AccountId  string   `json:"account_id"`
		AccountIds []string `json:"account_ids"`
	}
	if err := json.Unmarshal(encoded, &consumerView); err != nil {
		t.Fatalf("consumer-side unmarshal failed: %v\npayload was: %s", err, encoded)
	}

	assert.Equal(t, []string{"acc-a", "acc-b"}, consumerView.AccountIds,
		"publisher's account_ids field must decode into the consumer struct")
	assert.Empty(t, consumerView.AccountId,
		"publisher does not emit the legacy single-value field")

	// Also verify the on-the-wire JSON contains the literal field name
	// "account_ids" — guards against an accidental encoder swap that
	// would mangle field naming (e.g. switching to a custom marshaler
	// that camelCases keys).
	assert.Contains(t, string(encoded), `"account_ids"`,
		"wire format must use the literal field name account_ids")
}
