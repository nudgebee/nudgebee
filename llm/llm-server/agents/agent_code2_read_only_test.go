package agents

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// A read-only run (an automatic investigation, a planning turn, a follow-up check) never writes to a repository:
// raise_pr is proposed as a diff instead and a commit to a pull request is refused. Other runs are unchanged.
func TestReadOnlyRunRaisesNoPullRequest(t *testing.T) {
	loopOn(t, true)
	ask := CodeAgent2Request{Query: "fix it", RaisePr: true}
	for name, run := range map[string]struct {
		ctx   *security.RequestContext
		query core.NBAgentRequest
	}{
		"read-only context":            {core.WithReadOnlyRun(security.NewRequestContextForSuperAdmin()), core.NBAgentRequest{}},
		"read-only turn config":        {security.NewRequestContextForSuperAdmin(), core.NBAgentRequest{QueryConfig: toolcore.NBQueryConfig{ReadOnlyRun: true}}},
		"planning turn (strict reads)": {security.NewRequestContextForSuperAdmin(), core.NBAgentRequest{QueryConfig: toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true, RemediationPlanTurn: "auto"}}},
	} {
		got, refusal := readOnlyCodeRequest(run.ctx, run.query, ask)
		assert.Empty(t, refusal, name)
		assert.False(t, got.RaisePr, name)
		assert.Equal(t, "fix", got.Mode, name+": the fix is proposed as a diff")
		_, refusal = readOnlyCodeRequest(run.ctx, run.query, CodeAgent2Request{Query: "address the review", Followup: true, PRURL: "https://example.com/pr/1"})
		assert.Contains(t, refusal, "does not commit to a pull request", name)
	}
	got, refusal := readOnlyCodeRequest(security.NewRequestContextForSuperAdmin(), core.NBAgentRequest{}, ask)
	assert.Empty(t, refusal)
	assert.True(t, got.RaisePr, "a person's chat or the investigation's code-fix stage: unchanged")
	assert.Empty(t, got.Mode)
}

// With the remediation loop off for the account, a read-only run is as before: raise_pr and a pull request
// follow-up go through unchanged.
func TestReadOnlyRunLoopOffUnchanged(t *testing.T) {
	loopOn(t, false)
	ctx := core.WithReadOnlyRun(security.NewRequestContextForSuperAdmin())
	query := core.NBAgentRequest{AccountId: "account-1", QueryConfig: toolcore.NBQueryConfig{ReadOnlyRun: true}}
	got, refusal := readOnlyCodeRequest(ctx, query, CodeAgent2Request{Query: "fix it", RaisePr: true})
	assert.Empty(t, refusal)
	assert.True(t, got.RaisePr)
	assert.Empty(t, got.Mode)
	_, refusal = readOnlyCodeRequest(ctx, query, CodeAgent2Request{Query: "address the review", Followup: true, PRURL: "https://example.com/pr/1"})
	assert.Empty(t, refusal)
}

func loopOn(t *testing.T, on bool) {
	t.Helper()
	orig, origRead := events.RemediationLoopOn, events.RemediationLoopRead
	events.RemediationLoopOn = func(string) bool { return on }
	events.RemediationLoopRead = func(string) (bool, error) { return on, nil }
	t.Cleanup(func() { events.RemediationLoopOn, events.RemediationLoopRead = orig, origRead })
}

// A flag that cannot be read is not off: a read-only run still raises no pull request.
func TestReadOnlyRunFlagUnreadStaysReadOnly(t *testing.T) {
	loopOn(t, false)
	events.RemediationLoopRead = func(string) (bool, error) { return false, errors.New("feature flags unavailable") }
	ctx := core.WithReadOnlyRun(security.NewRequestContextForSuperAdmin())
	got, refusal := readOnlyCodeRequest(ctx, core.NBAgentRequest{AccountId: "account-1"}, CodeAgent2Request{Query: "fix it", RaisePr: true})
	assert.Empty(t, refusal)
	assert.False(t, got.RaisePr)
}
