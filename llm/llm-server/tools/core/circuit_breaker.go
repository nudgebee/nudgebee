package core

import (
	"fmt"
	"sync"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

// CircuitBreakerKey identifies the failure-bucketing scope for a single
// tool call. AccountId is mandatory (the breaker is always tenant-scoped).
// InstanceId distinguishes targets within an account — integration UUID
// for MCP, tool UUID for custom containers, cluster name for kubectl
// (once it opts in), etc. Two tool types may safely use overlapping
// InstanceId strings: the breaker namespaces state by tool type as well.
type CircuitBreakerKey struct {
	AccountId  string
	InstanceId string
}

// CircuitBreakerKeyer is an optional interface a tool implements to
// participate in the per-tool circuit breaker. The returned `on` bool
// lets the tool opt out per-call (e.g. dry runs, calls without a
// resolvable target). Tools that don't implement this interface never
// touch the breaker, regardless of their failure mode.
type CircuitBreakerKeyer interface {
	CircuitBreakerKey(ctx NbToolContext, input NBToolCallRequest) (key CircuitBreakerKey, on bool)
}

// CircuitBreakerFailureClassifier is an optional interface a tool can
// implement to decide whether a Call's outcome counts as an
// infrastructure failure (opens the circuit) vs a logical failure
// (does not). Tools that opt into the breaker but don't implement this
// fall back to the default: any non-nil err counts. The default is
// correct only for tools whose failure path is purely transport
// (MCP, custom_container). CLI-wrapping tools (kubectl, aws_execute)
// must implement this before opting in, otherwise their NotFound /
// AccessDenied / typo'd-argument errors would all trip the breaker.
type CircuitBreakerFailureClassifier interface {
	IsInfrastructureFailure(err error, response NBToolResponse) bool
}

// toolCircuitBreaker tracks consecutive-failure counts per
// (toolType, account, instance) tuple. Generalized from the original
// MCP-only breaker; the keying choices and rationale that applied to
// MCP apply to any participating tool:
//
//   - State is in-memory and per-pod. Each replica forms its own view;
//     cross-pod coordination would be over-engineering for a resilience
//     optimization. A genuinely-broken target quickly hits threshold on
//     every replica that talks to it; a transient failure on one pod
//     shouldn't block other pods.
//   - Threshold is consecutive, not windowed. A flaky-but-recovering
//     target never opens the circuit. By design — fast-fail is for
//     unambiguously broken targets.
//   - Cooldown elapsed = half-open: the next call proceeds; success
//     closes the circuit, failure re-arms it.
//   - The state map is one package-level singleton shared by every
//     concurrent conversation in the pod. Tripping a (toolType, account,
//     instance) tuple from conversation A blocks conversation B's calls
//     against that same tuple too. Deliberate: the breaker's unit of
//     isolation is "is this target broken", which is a property of the
//     target, not of any one conversation.
type toolCircuitBreaker struct {
	mu                 sync.RWMutex
	state              map[circuitStateKey]*circuitStateEntry
	defaultThreshold   int
	defaultCooldownSec int
	// forceEnabled lets tests bypass LlmServerToolCircuitBreakerEnabled, which defaults to false.
	forceEnabled bool
	// overrideConfig pins threshold()/cooldown() to defaultThreshold/
	// defaultCooldownSec instead of reading config.Config. Without it, a
	// test-constructed breaker's requested threshold/cooldown is silently
	// masked whenever it happens to differ from the (always-registered)
	// viper defaults for LlmServerToolCircuitBreakerFailureThreshold/
	// CooldownSeconds, since config.Config wins whenever its value is > 0.
	overrideConfig bool
}

type circuitStateKey struct {
	toolType   string
	accountId  string
	instanceId string
}

type circuitStateEntry struct {
	consecutiveFails int
	lastFailure      time.Time
}

const (
	defaultToolCircuitBreakerFailureThreshold = 3
	defaultToolCircuitBreakerCooldownSeconds  = 60

	// staleEntryEvictionAge bounds the memory the state map can hold.
	// RecordSuccess is the normal eviction path (a real call closes the
	// circuit and deletes the entry) but a tuple that fails and is never
	// called again — integration deleted, account churned — would
	// otherwise linger forever. snapshot() opportunistically evicts any
	// healthy entry whose last failure is older than this; it's already
	// walking the whole map for the observable gauge, so the sweep is free.
	// Caveat: this only runs when snapshot() is actually called, i.e. the
	// nb_llm_tool_healthy gauge's OTel callback fires, which requires both
	// observable gauges to have been created AND a metric reader collecting.
	// In a pod without a working OTLP exporter, nothing evicts. Bounded by
	// (account, integration) pairs that failed at least once and never
	// succeeded again — a slow drip, not a runaway — so this is left as a
	// documented gap rather than duplicating the sweep on RecordFailure's
	// hot path.
	staleEntryEvictionAge = 24 * time.Hour
)

var toolCircuitBreakerInstance = &toolCircuitBreaker{
	state:              make(map[circuitStateKey]*circuitStateEntry),
	defaultThreshold:   defaultToolCircuitBreakerFailureThreshold,
	defaultCooldownSec: defaultToolCircuitBreakerCooldownSeconds,
}

func init() {
	// Wire the breaker into the nb_llm_tool_healthy observable gauge.
	// Common can't import tools/core (cycle), so the snapshot function
	// flows through a registration hook.
	common.RegisterToolHealthSnapshot(toolCircuitBreakerInstance.snapshot)
}

// threshold and cooldown read live so config reloads / test overrides
// take effect on the next call without restarting.
func (b *toolCircuitBreaker) threshold() int {
	if !b.overrideConfig && config.Config.LlmServerToolCircuitBreakerFailureThreshold > 0 {
		return config.Config.LlmServerToolCircuitBreakerFailureThreshold
	}
	return b.defaultThreshold
}

func (b *toolCircuitBreaker) cooldown() time.Duration {
	if !b.overrideConfig && config.Config.LlmServerToolCircuitBreakerCooldownSeconds > 0 {
		return time.Duration(config.Config.LlmServerToolCircuitBreakerCooldownSeconds) * time.Second
	}
	return time.Duration(b.defaultCooldownSec) * time.Second
}

func (b *toolCircuitBreaker) enabled() bool {
	return b.forceEnabled || config.Config.LlmServerToolCircuitBreakerEnabled
}

// IsHealthy reports whether a Call against the given (toolType, account,
// instance) should proceed. Returns false only when the breaker is enabled,
// consecutive failures have crossed the threshold, AND the cooldown window
// has not yet elapsed.
func (b *toolCircuitBreaker) IsHealthy(toolType string, key CircuitBreakerKey) bool {
	if !b.enabled() {
		return true
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.state[circuitStateKey{toolType, key.AccountId, key.InstanceId}]
	if !ok {
		return true
	}
	if entry.consecutiveFails < b.threshold() {
		return true
	}
	return time.Since(entry.lastFailure) >= b.cooldown()
}

// RecordSuccess clears the failure history for the given tuple. A single
// successful call closes the circuit immediately.
func (b *toolCircuitBreaker) RecordSuccess(toolType string, key CircuitBreakerKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.state, circuitStateKey{toolType, key.AccountId, key.InstanceId})
}

// RecordFailure increments the consecutive-failure count and stamps the
// last-failure time. Once the count crosses the configured threshold,
// IsHealthy returns false until the cooldown elapses. No-op when the
// breaker is disabled.
func (b *toolCircuitBreaker) RecordFailure(toolType string, key CircuitBreakerKey) {
	if !b.enabled() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	sk := circuitStateKey{toolType, key.AccountId, key.InstanceId}
	entry, ok := b.state[sk]
	if !ok {
		entry = &circuitStateEntry{}
		b.state[sk] = entry
	}
	entry.consecutiveFails++
	entry.lastFailure = time.Now()
}

// Threshold exposes the currently-effective failure threshold so callers
// can include it in user-facing circuit-open messages.
func (b *toolCircuitBreaker) Threshold() int { return b.threshold() }

// reset wipes all in-memory state. Exported only for tests.
func (b *toolCircuitBreaker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = make(map[circuitStateKey]*circuitStateEntry)
}

// snapshot reports current health for every tuple the breaker has at
// least one observed failure for. Pairs that have never failed don't
// appear (the breaker has no state for them) — by design, to avoid
// reporting a tool's first failure as "newly unhealthy" when in reality
// we just had no prior data. Also evicts entries that have been healthy
// for longer than staleEntryEvictionAge, see its doc comment.
func (b *toolCircuitBreaker) snapshot() []common.ToolHealthEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	threshold := b.threshold()
	cooldown := b.cooldown()
	now := time.Now()
	out := make([]common.ToolHealthEntry, 0, len(b.state))
	for sk, entry := range b.state {
		healthy := entry.consecutiveFails < threshold || now.Sub(entry.lastFailure) >= cooldown
		if healthy && now.Sub(entry.lastFailure) >= staleEntryEvictionAge {
			delete(b.state, sk)
			continue
		}
		out = append(out, common.ToolHealthEntry{
			Type:          sk.toolType,
			AccountId:     sk.accountId,
			IntegrationId: sk.instanceId,
			Healthy:       healthy,
		})
	}
	return out
}

// ToolCircuitBreakerAPI is the subset of *toolCircuitBreaker the planner
// consumes. Exported so ToolCircuitBreaker() can return an interface a
// caller outside this package can actually name — an exported func
// returning an unexported type can be called but not held in a field,
// passed as a parameter, or substituted with a fake.
type ToolCircuitBreakerAPI interface {
	IsHealthy(toolType string, key CircuitBreakerKey) bool
	RecordSuccess(toolType string, key CircuitBreakerKey)
	RecordFailure(toolType string, key CircuitBreakerKey)
	Threshold() int
}

// ToolCircuitBreaker exposes the package-level breaker singleton to the
// planner. Returning the interface keeps callers free to substitute a
// fake; today the only implementation is the package-private struct.
func ToolCircuitBreaker() ToolCircuitBreakerAPI { return toolCircuitBreakerInstance }

// ClassifyAsInfraFailure asks the tool's classifier (if any) whether the
// Call outcome should open the breaker. Default policy when the tool
// doesn't implement CircuitBreakerFailureClassifier: any non-nil err
// counts. This default is safe only for tools whose Call returns err
// solely on infra issues — opt-in tools that wrap CLIs MUST implement
// the classifier interface before participating in the breaker.
func ClassifyAsInfraFailure(tool NBTool, err error, resp NBToolResponse) bool {
	if classifier, ok := tool.(CircuitBreakerFailureClassifier); ok {
		return classifier.IsInfrastructureFailure(err, resp)
	}
	return err != nil
}

// NewCircuitOpenResponse builds the NBToolResponse the planner returns
// when a tool's circuit is open. Centralized so every tool type yields
// the same shape and message — the LLM sees a consistent signal that
// the upstream is temporarily unavailable rather than a tool-specific
// bespoke string.
func NewCircuitOpenResponse(toolName string, threshold int) NBToolResponse {
	return NBToolResponse{
		Status: NBToolResponseStatusError,
		Data: fmt.Sprintf(
			"Tool %q is temporarily unavailable: circuit open after %d consecutive failures, will retry after the cooldown elapses.",
			toolName, threshold,
		),
		Type:          NBToolResponseTypeText,
		IsCircuitOpen: true,
	}
}
