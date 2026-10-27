package watch

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func primeInput(kind SourceKind, predicate PredicateKind, expr string) CreateInput {
	return CreateInput{
		ConversationID:  uuid.New(),
		AccountID:       uuid.New(),
		TenantID:        uuid.New(),
		SourceKind:      kind,
		SourceConfig:    json.RawMessage(`{}`),
		PredicateKind:   predicate,
		PredicateExpr:   expr,
		PollIntervalSec: 30,
		MaxDurationSec:  600,
	}
}

// TestPrime_DisabledWhenTimeoutIsZero keeps the operator escape hatch working:
// LLM_SERVER_WATCH_PRIMING_POLL_TIMEOUT_SEC=0 turns priming off entirely and
// must not touch the source.
func TestPrime_DisabledWhenTimeoutIsZero(t *testing.T) {
	src := &fakeSource{kind: SourceKind("prime_disabled"), observation: Observation{Data: "completed"}}
	installSource(t, src)

	out := Prime(superAdminCtx(), primeInput(src.kind, PredicateKindRegex, "completed"), 0)

	assert.True(t, out.Skipped)
	assert.False(t, out.Met)
	assert.NoError(t, out.Err)
	assert.Equal(t, 0, src.observeCount(), "priming must not run the source when disabled")
}

// TestPrime_AlreadyMet is the "answer now" case: the condition holds before any
// watch exists, so the caller can reply in the same turn instead of making the
// user wait out a poll interval.
func TestPrime_AlreadyMet(t *testing.T) {
	src := &fakeSource{kind: SourceKind("prime_met"), observation: Observation{Data: "deployment rolled out"}}
	installSource(t, src)

	out := Prime(superAdminCtx(), primeInput(src.kind, PredicateKindSubstring, "rolled out"), 5*time.Second)

	require.NoError(t, out.Err)
	assert.True(t, out.Met)
	assert.Contains(t, out.Summary, "rolled out")
	assert.Equal(t, "deployment rolled out", out.Observation)
}

// TestPrime_NotYetMet is the ordinary path — register the watch and let the
// dispatcher take it.
func TestPrime_NotYetMet(t *testing.T) {
	src := &fakeSource{kind: SourceKind("prime_unmet"), observation: Observation{Data: "in_progress"}}
	installSource(t, src)

	out := Prime(superAdminCtx(), primeInput(src.kind, PredicateKindRegex, "completed"), 5*time.Second)

	require.NoError(t, out.Err)
	assert.False(t, out.Met)
	assert.False(t, out.Skipped)
}

// TestPrime_NegateIsApplied — "wait until X disappears" must be evaluated the
// same way at priming time as it is on a real poll, or a watch whose condition
// already holds gets registered anyway.
func TestPrime_NegateIsApplied(t *testing.T) {
	src := &fakeSource{kind: SourceKind("prime_negate"), observation: Observation{Data: "nothing here"}}
	installSource(t, src)

	in := primeInput(src.kind, PredicateKindSubstring, "CrashLoopBackOff")
	in.PredicateNegate = true
	out := Prime(superAdminCtx(), in, 5*time.Second)

	require.NoError(t, out.Err)
	assert.True(t, out.Met, "substring absent + negate = condition already satisfied")
}

// TestPrime_ObserveFailureIsReportedAsFatal is the case the whole feature is
// for: every FAILED watch in the dev metastore is an observe failure, and the
// caller must be able to tell that apart from a slow source so it can refuse
// the registration instead of booking three doomed polls.
func TestPrime_ObserveFailureIsReportedAsFatal(t *testing.T) {
	src := &fakeSource{
		kind:       SourceKind("prime_observe_err"),
		observeErr: errors.New("no tool configs found for - github_execute, please configure"),
	}
	installSource(t, src)

	out := Prime(superAdminCtx(), primeInput(src.kind, PredicateKindRegex, "completed"), 5*time.Second)

	require.Error(t, out.Err)
	assert.Equal(t, PrimePhaseObserve, out.Phase)
	assert.False(t, out.TimedOut, "a hard failure must not be mistaken for slowness")
	assert.Contains(t, out.Err.Error(), "no tool configs found")
}

// TestPrime_SlowSourceIsNotFatal — a command that outruns the priming deadline
// is not a broken one. The outcome must be marked TimedOut so the caller
// registers the watch instead of rejecting it.
func TestPrime_SlowSourceIsNotFatal(t *testing.T) {
	src := &fakeSource{
		kind:        SourceKind("prime_slow"),
		observation: Observation{Data: "eventually"},
		observeWait: 200 * time.Millisecond,
	}
	installSource(t, src)

	out := Prime(superAdminCtx(), primeInput(src.kind, PredicateKindRegex, "eventually"), 20*time.Millisecond)

	require.Error(t, out.Err)
	assert.Equal(t, PrimePhaseObserve, out.Phase)
	assert.True(t, out.TimedOut, "slow source must be distinguishable from a broken one")
}

// TestPrime_UnknownSourceKindFailsFast guards the registration path against a
// source_kind that no longer exists in this build.
func TestPrime_UnknownSourceKindFailsFast(t *testing.T) {
	out := Prime(superAdminCtx(), primeInput(SourceKind("nope_not_registered"), PredicateKindRegex, "x"), 5*time.Second)
	require.Error(t, out.Err)
	assert.Equal(t, PrimePhaseValidate, out.Phase,
		"nothing was observed, so this belongs to the config gate, not the observe phase")
	assert.Contains(t, out.Err.Error(), "unknown source_kind")
}

// TestPrime_BadPredicateIsPredicatePhase — a predicate that cannot compile is
// reported against the predicate phase, which the caller treats as non-fatal.
// (Manager.Create rejects it immediately afterwards anyway.)
func TestPrime_BadPredicateIsPredicatePhase(t *testing.T) {
	src := &fakeSource{kind: SourceKind("prime_bad_pred"), observation: Observation{Data: "x"}}
	installSource(t, src)

	out := Prime(superAdminCtx(), primeInput(src.kind, PredicateKindRegex, "a(("), 5*time.Second)

	require.Error(t, out.Err)
	assert.Equal(t, PrimePhasePredicate, out.Phase)
}

// TestPrime_DoesNotWriteAnything — Prime runs before any row exists and must
// stay read-only; a stray write here would create rows the caller never
// intended. newMockManager fails the test on any unexpected statement.
func TestPrime_DoesNotWriteAnything(t *testing.T) {
	_, mock, _ := newMockManager(t)
	src := &fakeSource{kind: SourceKind("prime_nowrite"), observation: Observation{Data: "completed"}}
	installSource(t, src)

	_ = Prime(superAdminCtx(), primeInput(src.kind, PredicateKindRegex, "completed"), 5*time.Second)

	require.NoError(t, mock.ExpectationsWereMet(), "priming must not touch the database")
}

// countingTool is a primitive (non-agent) NBTool that records every
// invocation, so a test can prove a command was NOT executed.
type countingTool struct {
	name  string
	calls int32
	// writes makes the classifier call every command a delete.
	writes bool
}

func (c *countingTool) Name() string                 { return c.name }
func (c *countingTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (c *countingTool) Description() string          { return "counting test tool" }
func (c *countingTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{Type: toolcore.ToolSchemaTypeObject}
}
func (c *countingTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (toolcore.ToolRequestType, error) {
	if c.writes {
		return toolcore.ToolRequestTypeDelete, nil
	}
	return toolcore.ToolRequestTypeRead, nil
}
func (c *countingTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	atomic.AddInt32(&c.calls, 1)
	return toolcore.NBToolResponse{Data: "ran", Status: toolcore.NBToolResponseStatusSuccess}, nil
}
func (c *countingTool) callCount() int { return int(atomic.LoadInt32(&c.calls)) }

// TestPrime_MutatingCommandIsRejectedWithoutExecuting is the regression test
// for a safety defect introduced by the priming poll itself and caught in
// fresh-context review.
//
// toolSource's destructive-verb deny-list (`kubectl delete`, `rm -rf`,
// `helm uninstall`, ...) lives in ValidateConfig, which Manager.Create runs
// BEFORE writing a row — so a mutating source used to be refused without ever
// running. Priming executes the command for real. The first cut called
// Observe without ValidateConfig, which turned "registration refused" into
// "one live kubectl delete, then registration refused" — the guard firing
// against a cluster that had already lost the pod.
//
// Uses the REAL toolSource and the REAL deny-list; the fake exists only to
// count invocations that must never happen.
func TestPrime_MutatingCommandIsRejectedWithoutExecuting(t *testing.T) {
	tool := &countingTool{name: "prime_destructive_probe"}
	toolcore.RegisterNBToolFactory(tool.name, func(string) (toolcore.NBTool, error) { return tool, nil })

	for _, command := range []string{
		"kubectl delete pod foo -n bar",
		"rm -rf /var/lib/data",
		"helm uninstall my-release",
	} {
		t.Run(command, func(t *testing.T) {
			in := primeInput(SourceKindTool, PredicateKindRegex, "deleted")
			in.SourceConfig = json.RawMessage(`{"tool_name":"` + tool.name +
				`","tool_input":{"command":"` + command + `"}}`)

			out := Prime(superAdminCtx(), in, 5*time.Second)

			require.Error(t, out.Err, "a mutating source must be refused")
			assert.Equal(t, PrimePhaseValidate, out.Phase,
				"rejection must happen at the config gate, before anything runs")
			assert.False(t, out.Met)
		})
	}

	assert.Equal(t, 0, tool.callCount(),
		"the destructive command must NEVER have been executed by the priming poll")
}

// TestPrime_WriteTheDenyListMissesIsRejectedWithoutExecuting (#42460): the
// deny-list only sees a verb right after the executable, so `kubectl -n bar
// delete pod foo` passed ValidateConfig. The tool's classifier refuses it in
// Observe, before Call, and Prime reports it as a config rejection.
func TestPrime_WriteTheDenyListMissesIsRejectedWithoutExecuting(t *testing.T) {
	tool := &countingTool{name: "prime_classified_write_probe", writes: true}
	toolcore.RegisterNBToolFactory(tool.name, func(string) (toolcore.NBTool, error) { return tool, nil })

	in := primeInput(SourceKindTool, PredicateKindRegex, "deleted")
	in.SourceConfig = json.RawMessage(`{"tool_name":"` + tool.name + `","tool_input":{"command":"kubectl -n bar delete pod foo"}}`)

	out := Prime(superAdminCtx(), in, 5*time.Second)

	require.ErrorIs(t, out.Err, ErrNotReadOnly)
	assert.Equal(t, PrimePhaseValidate, out.Phase)
	assert.Equal(t, 0, tool.callCount(), "the write must never have been executed by the priming poll")
}

// TestPrime_AgentStyleToolIsRejectedWithoutExecuting: the other half of
// ValidateConfig. Agent tools cannot poll (they load conversation history),
// and priming must not invoke one to find that out.
func TestPrime_AgentStyleToolIsRejectedWithoutExecuting(t *testing.T) {
	agentTool := &agentStyleTool{countingTool: countingTool{name: "prime_agent_probe"}}
	toolcore.RegisterNBToolFactory(agentTool.name, func(string) (toolcore.NBTool, error) { return agentTool, nil })

	in := primeInput(SourceKindTool, PredicateKindRegex, "done")
	in.SourceConfig = json.RawMessage(`{"tool_name":"` + agentTool.name + `","tool_input":{"command":"kubectl get pods"}}`)

	out := Prime(superAdminCtx(), in, 5*time.Second)

	require.Error(t, out.Err)
	assert.Equal(t, PrimePhaseValidate, out.Phase)
	assert.Contains(t, out.Err.Error(), "agent-style tool")
	assert.Equal(t, 0, agentTool.callCount(), "an agent tool must not be invoked by priming")
}

type agentStyleTool struct{ countingTool }

func (a *agentStyleTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeAgent }

// TestPrime_BudgetMatchesPollBudget guards the coherence the live run showed
// was missing: priming executes the SAME command a dispatcher poll executes,
// so giving it a smaller budget means a command the dispatcher considers
// healthy gets classified "too slow to judge" at registration.
//
// Found on gke_nudgebee-dev: at the old 5s default, a `gh run view` against a
// nonexistent repo blew the priming deadline, was reported as TimedOut rather
// than a hard failure, and the watch was registered anyway — with the agent
// telling the user it would report back on a watch that could never succeed.
// That is the exact blind promise the priming poll exists to remove, and it
// was silently disabled for the most-used tool in the corpus.
func TestPrime_BudgetMatchesPollBudget(t *testing.T) {
	assert.Equal(t, config.Config.WatchPollTimeoutSec, config.Config.WatchPrimingPollTimeoutSec,
		"priming runs the same command as a poll; a smaller budget silently converts "+
			"deterministic source failures into 'slow', which re-opens blind registration")
}
