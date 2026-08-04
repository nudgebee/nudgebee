package core

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// newTestBreaker returns a toolCircuitBreaker with deterministic threshold
// and cooldown that don't depend on viper / config.Config defaults. Each
// test gets its own instance so state doesn't bleed between cases.
// overrideConfig is required here: config.Config.LlmServerToolCircuitBreaker
// FailureThreshold/CooldownSeconds are always registered via viper.SetDefault
// (3, 60), so without it threshold()/cooldown() would silently prefer those
// over whatever this constructor was asked for.
func newTestBreaker(threshold int, cooldown time.Duration) *toolCircuitBreaker {
	return &toolCircuitBreaker{
		state:              make(map[circuitStateKey]*circuitStateEntry),
		defaultThreshold:   threshold,
		defaultCooldownSec: int(cooldown.Seconds()),
		forceEnabled:       true,
		overrideConfig:     true,
	}
}

const testTypeA = "type-a"

func tk(acc, instance string) CircuitBreakerKey {
	return CircuitBreakerKey{AccountId: acc, InstanceId: instance}
}

// ---------- Primitive breaker behaviour ----------

func TestBreaker_HealthyByDefault(t *testing.T) {
	b := newTestBreaker(3, time.Minute)
	assert.True(t, b.IsHealthy(testTypeA, tk("acc1", "int1")))
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	b := newTestBreaker(3, time.Minute)
	k := tk("acc1", "int1")

	for range 2 {
		b.RecordFailure(testTypeA, k)
	}
	assert.True(t, b.IsHealthy(testTypeA, k), "below threshold must remain healthy")

	b.RecordFailure(testTypeA, k)
	assert.False(t, b.IsHealthy(testTypeA, k), "circuit must open at threshold")
}

func TestBreaker_SuccessResetsCount(t *testing.T) {
	b := newTestBreaker(3, time.Minute)
	k := tk("acc1", "int1")

	for range 3 {
		b.RecordFailure(testTypeA, k)
	}
	assert.False(t, b.IsHealthy(testTypeA, k))

	b.RecordSuccess(testTypeA, k)
	assert.True(t, b.IsHealthy(testTypeA, k), "single success must close the circuit")

	// Counter must restart from zero — partial failures stay healthy.
	for range 2 {
		b.RecordFailure(testTypeA, k)
	}
	assert.True(t, b.IsHealthy(testTypeA, k), "counter must restart from zero after success")
}

func TestBreaker_CooldownElapsedReAllowsCall(t *testing.T) {
	b := newTestBreaker(3, time.Hour)
	k := tk("acc1", "int1")
	for range 3 {
		b.RecordFailure(testTypeA, k)
	}
	assert.False(t, b.IsHealthy(testTypeA, k), "precondition: open")

	// Backdate the lastFailure to simulate cooldown elapsing.
	b.mu.Lock()
	b.state[circuitStateKey{testTypeA, k.AccountId, k.InstanceId}].lastFailure = time.Now().Add(-2 * time.Hour)
	b.mu.Unlock()

	assert.True(t, b.IsHealthy(testTypeA, k), "circuit must re-allow probe after cooldown")
}

func TestBreaker_FailureAfterCooldownReArmsCircuit(t *testing.T) {
	b := newTestBreaker(3, time.Hour)
	k := tk("acc1", "int1")
	for range 3 {
		b.RecordFailure(testTypeA, k)
	}
	b.mu.Lock()
	b.state[circuitStateKey{testTypeA, k.AccountId, k.InstanceId}].lastFailure = time.Now().Add(-2 * time.Hour)
	b.mu.Unlock()
	assert.True(t, b.IsHealthy(testTypeA, k))

	b.RecordFailure(testTypeA, k)
	assert.False(t, b.IsHealthy(testTypeA, k), "post-cooldown failure must re-arm")
}

// ---------- Isolation properties ----------

func TestBreaker_MultiAccountIsolation(t *testing.T) {
	b := newTestBreaker(3, time.Minute)
	for range 3 {
		b.RecordFailure(testTypeA, tk("acc1", "int1"))
	}
	assert.False(t, b.IsHealthy(testTypeA, tk("acc1", "int1")))
	assert.True(t, b.IsHealthy(testTypeA, tk("acc2", "int1")), "same instance id, different account must be unaffected")
	assert.True(t, b.IsHealthy(testTypeA, tk("acc1", "int2")), "same account, different instance must be unaffected")
}

// Two tool types may use overlapping instance-id strings (e.g. a UUID
// happens to collide with a container name); the breaker namespaces by
// tool type so they don't interfere. This is the property that makes the
// generic breaker safe to share across MCP, custom_container, and any
// future opt-in tool.
func TestBreaker_MultiToolTypeIsolation(t *testing.T) {
	b := newTestBreaker(3, time.Minute)
	const otherType = "type-b"
	k := tk("acc1", "same-instance-id")

	for range 3 {
		b.RecordFailure(testTypeA, k)
	}
	assert.False(t, b.IsHealthy(testTypeA, k))
	assert.True(t, b.IsHealthy(otherType, k), "different tool type must be unaffected by failures on type-a")
}

// ---------- Misc primitives ----------

func TestBreaker_RecordSuccessOnUnknownIsNoOp(t *testing.T) {
	b := newTestBreaker(3, time.Minute)
	assert.NotPanics(t, func() {
		b.RecordSuccess(testTypeA, tk("acc-never-seen", "int-never-seen"))
	})
	assert.True(t, b.IsHealthy(testTypeA, tk("acc-never-seen", "int-never-seen")))
}

func TestBreaker_ResetClearsAllState(t *testing.T) {
	b := newTestBreaker(3, time.Minute)
	for range 3 {
		b.RecordFailure(testTypeA, tk("acc1", "int1"))
	}
	assert.False(t, b.IsHealthy(testTypeA, tk("acc1", "int1")))

	b.reset()
	assert.True(t, b.IsHealthy(testTypeA, tk("acc1", "int1")), "reset must clear the open circuit")
}

func TestBreaker_ConcurrentAccess(t *testing.T) {
	b := newTestBreaker(100, time.Minute)
	k := tk("acc1", "int1")

	done := make(chan struct{})
	for range 50 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 100 {
				b.RecordFailure(testTypeA, k)
				_ = b.IsHealthy(testTypeA, k)
			}
		}()
	}
	for range 50 {
		<-done
	}

	// 50 × 100 = 5000 failures, threshold 100 → open. The win is that the
	// race detector finds no data races and the test exits.
	assert.False(t, b.IsHealthy(testTypeA, k))
}

func TestBreakerInstance_UsesConfigDefaults(t *testing.T) {
	assert.Equal(t, defaultToolCircuitBreakerFailureThreshold, toolCircuitBreakerInstance.defaultThreshold)
	assert.Equal(t, defaultToolCircuitBreakerCooldownSeconds, toolCircuitBreakerInstance.defaultCooldownSec)
}

// ---------- Snapshot output ----------

func TestBreaker_SnapshotReportsHealthyFlagAndType(t *testing.T) {
	b := newTestBreaker(3, time.Hour)

	for range 2 {
		b.RecordFailure(testTypeA, tk("acc1", "int1"))
	}
	for range 3 {
		b.RecordFailure(testTypeA, tk("acc2", "int2"))
	}
	for range 3 {
		b.RecordFailure("type-b", tk("acc1", "int-other"))
	}

	snap := b.snapshot()
	type sig struct{ typ, acc, intg string }
	byKey := make(map[sig]bool, len(snap))
	for _, e := range snap {
		byKey[sig{e.Type, e.AccountId, e.IntegrationId}] = e.Healthy
	}

	if assert.Contains(t, byKey, sig{testTypeA, "acc1", "int1"}) {
		assert.True(t, byKey[sig{testTypeA, "acc1", "int1"}], "below-threshold entry must report Healthy=true")
	}
	if assert.Contains(t, byKey, sig{testTypeA, "acc2", "int2"}) {
		assert.False(t, byKey[sig{testTypeA, "acc2", "int2"}], "at-threshold entry must report Healthy=false")
	}
	if assert.Contains(t, byKey, sig{"type-b", "acc1", "int-other"}) {
		assert.False(t, byKey[sig{"type-b", "acc1", "int-other"}], "other tool type must be independently reported")
	}
}

func TestBreaker_SnapshotIsEmptyWhenNoFailures(t *testing.T) {
	b := newTestBreaker(3, time.Hour)
	snap := b.snapshot()
	assert.Empty(t, snap, "snapshot should be empty when no failures have been recorded")
}

// ---------- Helper functions ----------

func TestClassifyAsInfraFailure_DefaultRule(t *testing.T) {
	var tool NBTool = stubTool{} // doesn't implement CircuitBreakerFailureClassifier
	assert.True(t, ClassifyAsInfraFailure(tool, errors.New("boom"), NBToolResponse{}),
		"default rule: any non-nil err counts as infra")
	assert.False(t, ClassifyAsInfraFailure(tool, nil, NBToolResponse{Status: NBToolResponseStatusError}),
		"default rule: nil err is not infra even if the response is shaped as error")
}

func TestClassifyAsInfraFailure_ClassifierWins(t *testing.T) {
	// Tool says "yes infra" even on nil err — the classifier wins, the
	// default is bypassed.
	custom := classifierTool{infra: true}
	assert.True(t, ClassifyAsInfraFailure(custom, nil, NBToolResponse{}))

	// Tool says "no infra" even on non-nil err — classifier wins.
	custom = classifierTool{infra: false}
	assert.False(t, ClassifyAsInfraFailure(custom, errors.New("logical"), NBToolResponse{}))
}

func TestImplTypeFor(t *testing.T) {
	assert.Equal(t, ToolImplTypeBuiltin, ImplTypeFor(nil), "nil tool must default to builtin")
	assert.Equal(t, ToolImplTypeBuiltin, ImplTypeFor(stubTool{}), "stub without provider must default to builtin")
	assert.Equal(t, "mcp", ImplTypeFor(mcpIntegrationTool{}), "MCP must self-identify")
}

func TestNewCircuitOpenResponse_MessageShape(t *testing.T) {
	resp := NewCircuitOpenResponse("my_tool", 3)
	assert.Equal(t, NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "my_tool")
	assert.Contains(t, resp.Data, "temporarily unavailable")
	assert.Contains(t, resp.Data, "3 consecutive failures")
}

// ---------- Opt-in interface wiring on real tools ----------

// MCP must satisfy both interfaces; otherwise the planner won't route
// the circuit check through this tool.
func TestMCPIntegrationTool_ImplementsBreakerInterfaces(t *testing.T) {
	var tool NBTool = mcpIntegrationTool{
		toolName: "myserver_list_users",
		config: mcpIntegrationConfig{
			IntegrationID:   "int-abc",
			IntegrationName: "myserver",
		},
	}

	keyer, ok := tool.(CircuitBreakerKeyer)
	if assert.True(t, ok, "mcpIntegrationTool must implement CircuitBreakerKeyer") {
		key, on := keyer.CircuitBreakerKey(NbToolContext{AccountId: "acc-xyz"}, NBToolCallRequest{})
		assert.True(t, on)
		assert.Equal(t, "acc-xyz", key.AccountId)
		assert.Equal(t, "int-abc", key.InstanceId, "MCP keys by integration UUID, not name")

		_, on = keyer.CircuitBreakerKey(NbToolContext{AccountId: ""}, NBToolCallRequest{})
		assert.False(t, on, "missing account must opt out")
	}

	classifier, ok := tool.(CircuitBreakerFailureClassifier)
	if assert.True(t, ok, "mcpIntegrationTool must implement CircuitBreakerFailureClassifier") {
		assert.True(t, classifier.IsInfrastructureFailure(errors.New("relay broken"), NBToolResponse{}))
		assert.False(t, classifier.IsInfrastructureFailure(nil, NBToolResponse{Status: NBToolResponseStatusSuccess}))
	}
}

func TestNbCustomContainerTool_ImplementsBreakerInterfaces(t *testing.T) {
	var tool NBTool = nbCustomContainerTool{tool: ToolDto{Id: "tool-uuid", Name: "myscript"}}

	keyer, ok := tool.(CircuitBreakerKeyer)
	if assert.True(t, ok, "nbCustomContainerTool must implement CircuitBreakerKeyer") {
		key, on := keyer.CircuitBreakerKey(NbToolContext{AccountId: "acc-xyz"}, NBToolCallRequest{})
		assert.True(t, on)
		assert.Equal(t, "acc-xyz", key.AccountId)
		assert.Equal(t, "tool-uuid", key.InstanceId, "container keys by tool UUID, not user-facing name")
	}

	classifier, ok := tool.(CircuitBreakerFailureClassifier)
	if assert.True(t, ok, "nbCustomContainerTool must implement CircuitBreakerFailureClassifier") {
		assert.True(t, classifier.IsInfrastructureFailure(errors.New("relay unreachable"), NBToolResponse{}))
		assert.False(t, classifier.IsInfrastructureFailure(nil, NBToolResponse{}))
	}

	assert.Equal(t, "container", ImplTypeFor(tool))
}

// ---------- Test doubles ----------

type stubTool struct{}

func (stubTool) Name() string        { return "stub" }
func (stubTool) Description() string { return "" }
func (stubTool) Call(_ NbToolContext, _ NBToolCallRequest) (NBToolResponse, error) {
	return NBToolResponse{}, nil
}
func (stubTool) GetType() NBToolType     { return NBToolTypeTool }
func (stubTool) InputSchema() ToolSchema { return ToolSchema{} }

type classifierTool struct {
	stubTool
	infra bool
}

func (c classifierTool) IsInfrastructureFailure(_ error, _ NBToolResponse) bool { return c.infra }
