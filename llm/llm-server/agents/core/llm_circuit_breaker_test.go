package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/llms/huggingface/huggingfaceclient"

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
	circuitBreakerMap[key].CooldownUntil = time.Now().Add(-1 * time.Second)
	circuitBreakerMutex.Unlock()

	// Cooldown expired → the first caller takes the probe (returns false)...
	assert.False(t, IsModelCircuitOpen(provider, model))
	// ...and immediately reserves it, so the next caller fails fast (probe lock) rather
	// than stampeding the endpoint. State stays "open" throughout.
	assert.True(t, IsModelCircuitOpen(provider, model))

	circuitBreakerMutex.RLock()
	entry := circuitBreakerMap[key]
	assert.Equal(t, circuitBreakerStateOpen, entry.State)
	assert.True(t, entry.CooldownUntil.After(time.Now()), "probe reserved: cooldown extended into the future")
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
	cooldown1 := entry1.CooldownUntil.Sub(entry1.OpenedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 60, cooldown1.Seconds(), 1.0, "First failure should have ~60s cooldown")

	// Second failure: doubled (120s)
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry2 := circuitBreakerMap[key]
	cooldown2 := entry2.CooldownUntil.Sub(entry2.OpenedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 120, cooldown2.Seconds(), 1.0, "Second failure should have ~120s cooldown")

	// Third failure: doubled again (240s)
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry3 := circuitBreakerMap[key]
	cooldown3 := entry3.CooldownUntil.Sub(entry3.OpenedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 240, cooldown3.Seconds(), 1.0, "Third failure should have ~240s cooldown")

	// Fourth failure: would be 480s but capped at 300s
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry4 := circuitBreakerMap[key]
	cooldown4 := entry4.CooldownUntil.Sub(entry4.OpenedAt)
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

	// Simulate a probe in flight: an entry left half-open with an expired cooldown
	// (e.g. written by an older pod before the read-only change). It still allows a probe.
	key := getCircuitBreakerKey(provider, model)
	circuitBreakerMutex.Lock()
	circuitBreakerMap[key].State = circuitBreakerStateHalfOpen
	circuitBreakerMap[key].CooldownUntil = time.Now().Add(-1 * time.Second)
	circuitBreakerMutex.Unlock()
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
	assert.Equal(t, 3, circuitBreakerMap[key].FailureCount)
	circuitBreakerMutex.RUnlock()

	// Success clears the entry (absent == closed).
	RecordModelSuccess(provider, model)

	circuitBreakerMutex.RLock()
	_, present := circuitBreakerMap[key]
	circuitBreakerMutex.RUnlock()
	assert.False(t, present, "success should clear the breaker entry")

	// Next failure should start from base cooldown again
	RecordModelRateLimitHit(provider, model)
	circuitBreakerMutex.RLock()
	entry := circuitBreakerMap[key]
	cooldown := entry.CooldownUntil.Sub(entry.OpenedAt)
	circuitBreakerMutex.RUnlock()

	assert.InDelta(t, 60, cooldown.Seconds(), 1.0, "After success reset, cooldown should be back to base")
}

func TestIsCircuitTrippingError(t *testing.T) {
	trips := []string{
		"unexpected status code: 503 SERVICE_UNAVAILABLE",
		"dial tcp: connect: connection refused",
		"read: connection reset by peer",
		"Post \"https://x\": context deadline exceeded",
		"request timed out after 120s",
		"dial tcp 10.0.0.5:443: connect: connection refused", // :443 port must NOT be read as a 4xx
		"connection refused to model claude-401",             // 4xx in a model name must NOT be read as a status
		"request failed after 400 ms: i/o timeout",           // a 4xx-looking duration must NOT be read as a status
	}
	for _, m := range trips {
		assert.True(t, isCircuitTrippingError(errors.New(m)), "should trip: %q", m)
	}
	noTrip := []string{
		"unexpected status code: 400 bad request",
		"unexpected status code: 404 not found",
		"unexpected status code: 401 unauthorized",
		"unexpected status code: 409 conflict", // client-side state conflict must NOT trip a shared breaker
		"unexpected status code: 429 too many requests",
		"unexpected status code: 400 bad request: request timed out", // 4xx wins over the "timed out" substring
		"429: request timed out",                                     // raw 4xx code + timeout marker must not trip
		"400 timed out",
		"HTTP 400 bad request: request timed out", // 4xx anywhere in the message must not trip
		"returned 429: request timed out",

		"llm returned empty content",
		"",
	}
	for _, m := range noTrip {
		if m == "" {
			assert.False(t, isCircuitTrippingError(nil))
			continue
		}
		assert.False(t, isCircuitTrippingError(errors.New(m)), "should NOT trip: %q", m)
	}

	// The HF client's own breaker errors must always classify as tripping (→ fallback),
	// even when wrapping a 4xx like a repeated 429.
	assert.True(t, isCircuitTrippingError(huggingfaceclient.ErrCircuitOpen))
	assert.True(t, isCircuitTrippingError(
		fmt.Errorf("%w after 3 attempts: %w", huggingfaceclient.ErrCircuitTripped, errors.New("429 too many requests"))))
}

// The googleai first-token watchdog abandons a stream that produced nothing and wraps
// context.DeadlineExceeded. That error must classify as circuit-tripping so the retry
// router sends it straight to a fallback model: same-model retries are wasted on a
// stream that never started, and each wasted attempt costs another full deadline.
func TestFirstTokenTimeoutRoutesToFallback(t *testing.T) {
	err := fmt.Errorf("first token timeout after %s on %s: %w",
		35*time.Second, "gemini-3.5-flash", context.DeadlineExceeded)

	assert.True(t, isCircuitTrippingError(err),
		"first-token timeout must route to the fallback path, not same-model retries")

	// It must NOT be mistaken for an empty-content response: that path burns two
	// same-model retries first, which is exactly the 5-minute stall we are removing.
	assert.False(t, isEmptyResponseError(err),
		"first-token timeout is a hung stream, not an empty response")
}
