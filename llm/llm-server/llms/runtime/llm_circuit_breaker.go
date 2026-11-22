package runtime

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/huggingface/huggingfaceclient"
	"sync"
	"time"
)

// init wires the HF client's breaker hooks to this package's per-pod breaker, letting
// the client skip/trip the breaker without importing agents/core (which would be a
// cycle). The breaker is in-memory per pod — its only job is to fail fast when the
// endpoint is not ready, so a request doesn't hang through the full retry budget.
func init() {
	huggingfaceclient.IsCircuitOpen = func(model string) bool {
		return IsModelCircuitOpen("huggingface", model)
	}
	huggingfaceclient.RecordUnavailable = func(model string) {
		RecordModelFailure("huggingface", model)
	}
}

// circuitBreakerState represents the state of a circuit breaker
type circuitBreakerState string

const (
	circuitBreakerStateClosed   circuitBreakerState = "closed"
	circuitBreakerStateOpen     circuitBreakerState = "open"
	circuitBreakerStateHalfOpen circuitBreakerState = "half_open"

	defaultCircuitBreakerCooldownSeconds = 60
	maxCircuitBreakerCooldownSeconds     = 300 // 5 minutes max

	// circuitBreakerProbeExtension is how long the first caller past an expired cooldown
	// reserves the single probe slot (half-open), so concurrent callers keep failing fast
	// instead of stampeding the recovering endpoint.
	circuitBreakerProbeExtension = 15 * time.Second
)

// circuitBreakerEntry is the per-pod in-memory breaker state for one provider+model.
// TripCause records WHY the breaker opened. Without it the circuit-open fail-fast
// path defaults every trip to "network or timeout", so a user throttled by a 429 is
// told to check the network and an operator goes looking for an outage that isn't
// there — the same mislabelling fallbackCause already guards against downstream.
type circuitBreakerEntry struct {
	State         circuitBreakerState
	OpenedAt      time.Time
	CooldownUntil time.Time
	FailureCount  int
	TripCause     fallbackCause
	// TripDetail is which limit a quota trip exhausted, so every fail-fast during
	// the cooldown reports the same reason as the 429 that opened the circuit.
	// Most failures in a throttling incident are these, not fresh 429s.
	TripDetail common.RateLimitDetail
}

// circuitStatus is the breaker's answer about one provider+model: whether to skip
// it, and everything needed to explain why.
type circuitStatus struct {
	Open   bool
	Cause  fallbackCause
	Detail common.RateLimitDetail
}

var (
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

// IsModelCircuitOpen reports whether the provider+model circuit is open (skip it).
// While within the cooldown the common path is a cheap read under RLock. Once the
// cooldown expires, the first caller takes the write lock and extends the cooldown to
// reserve a single probe (half-open); concurrent callers re-read the extended cooldown
// and keep failing fast, so a recovering endpoint isn't stampeded.
func IsModelCircuitOpen(provider, model string) bool {
	return modelCircuitStatus(provider, model).Open
}

// logAttrs renders the reason the breaker is open as slog key/value pairs, with
// the rate-limit detail included only when the provider actually gave one.
func (s circuitStatus) logAttrs() []any {
	attrs := []any{"cause", s.Cause.reason()}
	if s.Detail.Known() {
		attrs = append(attrs, "rate_limit_scope", string(s.Detail.Scope), "rate_limit_window", string(s.Detail.Window))
	}
	return attrs
}

// modelCircuitStatus is IsModelCircuitOpen plus the reason the breaker opened,
// read under the same lock acquisition so the caller can't race the reason
// against the open/closed decision. Callers needing only the boolean use
// IsModelCircuitOpen.
func modelCircuitStatus(provider, model string) circuitStatus {
	key := getCircuitBreakerKey(provider, model)
	closed := circuitStatus{Cause: fallbackCauseUnavailable, Detail: common.UnknownRateLimitDetail()}

	circuitBreakerMutex.RLock()
	entry, ok := circuitBreakerMap[key]
	if !ok || entry.State == circuitBreakerStateClosed {
		circuitBreakerMutex.RUnlock()
		return closed
	}
	stillCoolingDown := time.Now().Before(entry.CooldownUntil)
	open := circuitStatus{Open: true, Cause: entry.TripCause, Detail: entry.TripDetail}
	circuitBreakerMutex.RUnlock()
	if stillCoolingDown {
		return open
	}

	circuitBreakerMutex.Lock()
	defer circuitBreakerMutex.Unlock()
	entry, ok = circuitBreakerMap[key]
	if !ok || entry.State == circuitBreakerStateClosed {
		return closed
	}
	if time.Now().After(entry.CooldownUntil) {
		entry.CooldownUntil = time.Now().Add(circuitBreakerProbeExtension) // reserve the probe
		return circuitStatus{Cause: entry.TripCause, Detail: entry.TripDetail}
	}
	// Another caller already took the probe.
	return circuitStatus{Open: true, Cause: entry.TripCause, Detail: entry.TripDetail}
}

// RecordModelFailure opens the circuit for a provider+model after a trip-worthy
// failure (rate limit, 503, connection error, timeout). Uses an escalating cooldown:
// base doubled per consecutive failure, capped at max. The whole read-modify-write is
// done under the write lock so concurrent failures can't lose an increment.
func RecordModelFailure(provider, model string) {
	recordModelFailure(provider, model, fallbackCauseUnavailable, common.UnknownRateLimitDetail())
}

func recordModelFailure(provider, model string, cause fallbackCause, detail common.RateLimitDetail) {
	key := getCircuitBreakerKey(provider, model)
	baseCooldown := getCircuitBreakerCooldownSeconds()
	now := time.Now()

	circuitBreakerMutex.Lock()
	entry, ok := circuitBreakerMap[key]
	if !ok {
		entry = &circuitBreakerEntry{}
		circuitBreakerMap[key] = entry
	}
	entry.FailureCount++
	entry.State = circuitBreakerStateOpen
	entry.OpenedAt = now
	entry.TripCause = cause
	entry.TripDetail = detail

	cooldownSeconds := baseCooldown
	for i := 1; i < entry.FailureCount; i++ {
		cooldownSeconds *= 2
		if cooldownSeconds > maxCircuitBreakerCooldownSeconds {
			cooldownSeconds = maxCircuitBreakerCooldownSeconds
			break
		}
	}
	entry.CooldownUntil = now.Add(time.Duration(cooldownSeconds) * time.Second)
	failureCount, cooldownUntil := entry.FailureCount, entry.CooldownUntil
	circuitBreakerMutex.Unlock()

	logAttrs := []any{
		"provider", provider, "model", model, "cause", cause.reason(),
		"failureCount", failureCount, "cooldownSeconds", cooldownSeconds,
		"cooldownUntil", cooldownUntil.Format(time.RFC3339),
	}
	if detail.Known() {
		logAttrs = append(logAttrs, "rate_limit_scope", string(detail.Scope), "rate_limit_window", string(detail.Window))
	}
	slog.Warn("Circuit breaker opened for model", logAttrs...)
	common.MetricsLLMCircuitBreakerTripped(provider, model)
}

// RecordModelRateLimitHit is the 429 entry point — a rate limit is one trip cause.
// cause is the provider error that returned the 429; the limit it names is kept on
// the breaker so fail-fast responses during the cooldown can repeat the reason.
func RecordModelRateLimitHit(provider, model string, err error) {
	recordModelFailure(provider, model, fallbackCauseQuota, common.RateLimitDetailFromError(err))
}

// RecordModelSuccess closes the circuit after a successful request (absent = closed).
func RecordModelSuccess(provider, model string) {
	key := getCircuitBreakerKey(provider, model)

	circuitBreakerMutex.Lock()
	entry, ok := circuitBreakerMap[key]
	if !ok || entry.State == circuitBreakerStateClosed {
		circuitBreakerMutex.Unlock()
		return
	}
	previousState := entry.State
	delete(circuitBreakerMap, key)
	circuitBreakerMutex.Unlock()

	slog.Info("Circuit breaker closed for model after successful request",
		"provider", provider, "model", model, "previousState", previousState)
}

// ResetCircuitBreakers clears in-memory state. Intended for testing.
func ResetCircuitBreakers() {
	circuitBreakerMutex.Lock()
	defer circuitBreakerMutex.Unlock()
	circuitBreakerMap = make(map[string]*circuitBreakerEntry)
}
