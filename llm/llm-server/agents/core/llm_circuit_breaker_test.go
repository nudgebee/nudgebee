package core

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCircuitBreakerOpensOnRateLimit(t *testing.T) {
	ResetCircuitBreakers()

	provider := "bedrock"
	model := "claude-3-sonnet"

	// Initially circuit should be closed
	assert.False(t, IsModelCircuitOpen(provider, model))

	// Record a rate limit hit
	RecordModelRateLimitHit(provider, model)

	// Circuit should now be open
	assert.True(t, IsModelCircuitOpen(provider, model))
}

func TestCircuitBreakerClosesOnSuccess(t *testing.T) {
	ResetCircuitBreakers()

	provider := "bedrock"
	model := "claude-3-sonnet"

	// Open the circuit
	RecordModelRateLimitHit(provider, model)
	assert.True(t, IsModelCircuitOpen(provider, model))

	// Record a success
	RecordModelSuccess(provider, model)

	// Circuit should now be closed
	assert.False(t, IsModelCircuitOpen(provider, model))
}

func TestCircuitBreakerCooldownExpiry(t *testing.T) {
	ResetCircuitBreakers()

	provider := "bedrock"
	model := "claude-3-sonnet"

	// Open the circuit
	RecordModelRateLimitHit(provider, model)
	assert.True(t, IsModelCircuitOpen(provider, model))

	// Manually set cooldown to the past to simulate expiry
	key := getCircuitBreakerKey(provider, model)
	circuitBreakerMutex.Lock()
	circuitBreakerMap[key].cooldownUntil = time.Now().Add(-1 * time.Second)
	circuitBreakerMutex.Unlock()

	// Circuit should transition to half-open (returns false = allow probe)
	assert.False(t, IsModelCircuitOpen(provider, model))

	// Verify state is half-open
	circuitBreakerMutex.RLock()
	entry := circuitBreakerMap[key]
	assert.Equal(t, circuitBreakerStateHalfOpen, entry.state)
	circuitBreakerMutex.RUnlock()
}

func TestCircuitBreakerEscalatingCooldown(t *testing.T) {
	ResetCircuitBreakers()

	provider := "openai"
	model := "gpt-4"

	// First failure: base cooldown (60s)
	RecordModelRateLimitHit(provider, model)
	key := getCircuitBreakerKey(provider, model)

	circuitBreakerMutex.RLock()
	entry1 := circuitBreakerMap[key]
	cooldown1 := entry1.cooldownUntil.Sub(entry1.openedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 60, cooldown1.Seconds(), 1.0, "First failure should have ~60s cooldown")

	// Second failure: doubled (120s)
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry2 := circuitBreakerMap[key]
	cooldown2 := entry2.cooldownUntil.Sub(entry2.openedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 120, cooldown2.Seconds(), 1.0, "Second failure should have ~120s cooldown")

	// Third failure: doubled again (240s)
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry3 := circuitBreakerMap[key]
	cooldown3 := entry3.cooldownUntil.Sub(entry3.openedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 240, cooldown3.Seconds(), 1.0, "Third failure should have ~240s cooldown")

	// Fourth failure: would be 480s but capped at 300s
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry4 := circuitBreakerMap[key]
	cooldown4 := entry4.cooldownUntil.Sub(entry4.openedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 300, cooldown4.Seconds(), 1.0, "Fourth failure should be capped at 300s")
}

func TestCircuitBreakerKeyIsolation(t *testing.T) {
	ResetCircuitBreakers()

	// Open circuit for one model
	RecordModelRateLimitHit("bedrock", "claude-3-sonnet")

	// Different provider+model should be unaffected
	assert.False(t, IsModelCircuitOpen("openai", "gpt-4"))
	assert.False(t, IsModelCircuitOpen("bedrock", "claude-3-haiku"))
	assert.False(t, IsModelCircuitOpen("azure", "claude-3-sonnet"))

	// Original should still be open
	assert.True(t, IsModelCircuitOpen("bedrock", "claude-3-sonnet"))
}

func TestCircuitBreakerConcurrency(t *testing.T) {
	ResetCircuitBreakers()

	provider := "bedrock"
	model := "claude-3-sonnet"
	iterations := 100

	var wg sync.WaitGroup

	// Concurrent writes (rate limit hits)
	for i := range iterations {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			RecordModelRateLimitHit(provider, model)
		}(i)
	}

	// Concurrent reads
	for range iterations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = IsModelCircuitOpen(provider, model)
		}()
	}

	// Concurrent success recordings
	for range iterations / 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RecordModelSuccess(provider, model)
		}()
	}

	// Should complete without races or panics
	wg.Wait()
}

func TestCircuitBreakerHalfOpenReOpensOnFailure(t *testing.T) {
	ResetCircuitBreakers()

	provider := "bedrock"
	model := "claude-3-sonnet"

	// Open the circuit
	RecordModelRateLimitHit(provider, model)

	// Simulate cooldown expiry
	key := getCircuitBreakerKey(provider, model)
	circuitBreakerMutex.Lock()
	circuitBreakerMap[key].cooldownUntil = time.Now().Add(-1 * time.Second)
	circuitBreakerMutex.Unlock()

	// Check triggers half-open
	assert.False(t, IsModelCircuitOpen(provider, model))

	// Another rate limit hit should re-open the circuit
	RecordModelRateLimitHit(provider, model)
	assert.True(t, IsModelCircuitOpen(provider, model))
}

func TestCircuitBreakerSuccessOnClosedIsNoOp(t *testing.T) {
	ResetCircuitBreakers()

	// Recording success on a model that was never rate-limited should be a no-op
	RecordModelSuccess("bedrock", "claude-3-sonnet")
	assert.False(t, IsModelCircuitOpen("bedrock", "claude-3-sonnet"))
}

func TestCircuitBreakerSuccessResetsFailureCount(t *testing.T) {
	ResetCircuitBreakers()

	provider := "bedrock"
	model := "claude-3-sonnet"
	key := getCircuitBreakerKey(provider, model)

	// Accumulate multiple failures
	RecordModelRateLimitHit(provider, model)
	RecordModelRateLimitHit(provider, model)
	RecordModelRateLimitHit(provider, model)

	circuitBreakerMutex.RLock()
	assert.Equal(t, 3, circuitBreakerMap[key].failureCount)
	circuitBreakerMutex.RUnlock()

	// Success should reset failure count
	RecordModelSuccess(provider, model)

	circuitBreakerMutex.RLock()
	assert.Equal(t, 0, circuitBreakerMap[key].failureCount)
	circuitBreakerMutex.RUnlock()

	// Next failure should start from base cooldown again
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry := circuitBreakerMap[key]
	cooldown := entry.cooldownUntil.Sub(entry.openedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 60, cooldown.Seconds(), 1.0, "After success reset, cooldown should be back to base")
}
