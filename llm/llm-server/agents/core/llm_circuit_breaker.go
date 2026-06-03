package core

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"sync"
	"time"
)

// circuitBreakerState represents the state of a circuit breaker
type circuitBreakerState string

const (
	circuitBreakerStateClosed   circuitBreakerState = "closed"
	circuitBreakerStateOpen     circuitBreakerState = "open"
	circuitBreakerStateHalfOpen circuitBreakerState = "half_open"

	defaultCircuitBreakerCooldownSeconds = 60
	maxCircuitBreakerCooldownSeconds     = 300 // 5 minutes max
)

// circuitBreakerEntry tracks the circuit breaker state for a provider+model combination
type circuitBreakerEntry struct {
	state         circuitBreakerState
	openedAt      time.Time
	failureCount  int
	cooldownUntil time.Time
}

var (
	// circuitBreakerMap grows unbounded — entries are never evicted.
	// TODO: add TTL-based eviction or a max-size cap if the number of distinct
	// provider+model combinations grows significantly (e.g., dynamic model names).
	circuitBreakerMap   = make(map[string]*circuitBreakerEntry)
	circuitBreakerMutex sync.RWMutex
)

// getCircuitBreakerKey returns the key for a provider+model combination
func getCircuitBreakerKey(provider, model string) string {
	return fmt.Sprintf("%s:%s", provider, model)
}

// getCircuitBreakerCooldownSeconds returns the configured cooldown duration
func getCircuitBreakerCooldownSeconds() int {
	if config.Config.LlmCircuitBreakerCooldownSeconds > 0 {
		return config.Config.LlmCircuitBreakerCooldownSeconds
	}
	return defaultCircuitBreakerCooldownSeconds
}

// IsModelCircuitOpen checks if the circuit breaker for a provider+model is open (should skip this model).
// Returns true if the model should be skipped, false if it can be used.
// When the cooldown has expired, the circuit transitions to half-open to allow a probe request.
func IsModelCircuitOpen(provider, model string) bool {
	key := getCircuitBreakerKey(provider, model)

	// Hold the read lock across all field reads to eliminate the data race.
	// circuitBreakerState is a string (2 words) and time.Time is a 3-word struct —
	// neither is atomically readable without synchronisation.
	circuitBreakerMutex.RLock()
	entry, exists := circuitBreakerMap[key]
	if !exists || entry.state == circuitBreakerStateClosed {
		circuitBreakerMutex.RUnlock()
		return false
	}
	cooldownExpired := time.Now().After(entry.cooldownUntil)
	circuitBreakerMutex.RUnlock()

	if cooldownExpired {
		// Transition to half-open: allow one probe request.
		// Upgrade to write lock and re-check — a concurrent RecordModelRateLimitHit
		// may have re-opened the circuit between our RUnlock and this Lock, in which
		// case the re-check below will find state != Open and skip the transition.
		circuitBreakerMutex.Lock()
		entry, exists = circuitBreakerMap[key]
		if exists && entry.state == circuitBreakerStateOpen && time.Now().After(entry.cooldownUntil) {
			entry.state = circuitBreakerStateHalfOpen
			slog.Info("Circuit breaker transitioning to half-open, allowing probe request",
				"provider", provider,
				"model", model,
				"failureCount", entry.failureCount)
		}
		circuitBreakerMutex.Unlock()
		return false // Allow probe request
	}

	// Circuit is open and cooldown hasn't expired
	return true
}

// RecordModelRateLimitHit opens the circuit breaker for a provider+model after a rate limit hit.
// Uses escalating cooldown: base cooldown doubled each consecutive failure, capped at max.
func RecordModelRateLimitHit(provider, model string) {
	key := getCircuitBreakerKey(provider, model)
	baseCooldown := getCircuitBreakerCooldownSeconds()
	now := time.Now()

	circuitBreakerMutex.Lock()
	defer circuitBreakerMutex.Unlock()

	entry, exists := circuitBreakerMap[key]
	if !exists {
		entry = &circuitBreakerEntry{}
		circuitBreakerMap[key] = entry
	}

	entry.failureCount++
	entry.state = circuitBreakerStateOpen
	entry.openedAt = now

	// Escalating cooldown: base * 2^(failures-1), capped at max
	cooldownSeconds := baseCooldown
	for i := 1; i < entry.failureCount; i++ {
		cooldownSeconds *= 2
		if cooldownSeconds > maxCircuitBreakerCooldownSeconds {
			cooldownSeconds = maxCircuitBreakerCooldownSeconds
			break
		}
	}
	entry.cooldownUntil = now.Add(time.Duration(cooldownSeconds) * time.Second)

	slog.Warn("Circuit breaker opened for model due to rate limit",
		"provider", provider,
		"model", model,
		"failureCount", entry.failureCount,
		"cooldownSeconds", cooldownSeconds,
		"cooldownUntil", entry.cooldownUntil.Format(time.RFC3339))

	// Record metric
	common.MetricsLLMCircuitBreakerTripped(provider, model)
}

// RecordModelSuccess closes the circuit breaker for a provider+model after a successful request.
// Resets the failure count to allow normal operation.
func RecordModelSuccess(provider, model string) {
	key := getCircuitBreakerKey(provider, model)

	// Hold RLock across the state read — entry.state is a string (2 words) and must
	// not be read concurrently with a write from RecordModelRateLimitHit.
	circuitBreakerMutex.RLock()
	entry, exists := circuitBreakerMap[key]
	if !exists || entry.state == circuitBreakerStateClosed {
		circuitBreakerMutex.RUnlock()
		return // Nothing to do
	}
	circuitBreakerMutex.RUnlock()

	circuitBreakerMutex.Lock()
	defer circuitBreakerMutex.Unlock()

	// Re-check after acquiring write lock
	entry, exists = circuitBreakerMap[key]
	if !exists || entry.state == circuitBreakerStateClosed {
		return
	}

	previousState := entry.state
	entry.state = circuitBreakerStateClosed
	entry.failureCount = 0

	slog.Info("Circuit breaker closed for model after successful request",
		"provider", provider,
		"model", model,
		"previousState", previousState)
}

// ResetCircuitBreakers clears all circuit breaker state. Intended for testing only.
func ResetCircuitBreakers() {
	circuitBreakerMutex.Lock()
	defer circuitBreakerMutex.Unlock()
	circuitBreakerMap = make(map[string]*circuitBreakerEntry)
}
