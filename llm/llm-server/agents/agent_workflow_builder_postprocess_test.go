package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// postProcessCtx builds a DB-free request context for the pass-through tests.
func postProcessCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct-1"})
}

// PostProcessResponse must be a pure pass-through: the builder's terminal output is
// already final (raw workflow JSON, clean json.MarshalIndent from toolFinalize, or a
// markdown summary). Any transformation here is pure risk — it once mangled prose
// answers that legitimately contained a fenced JSON example (#31499).
func TestWorkflowBuilderAgent_PostProcessResponse_PassThrough(t *testing.T) {
	agent := newWorkflowBuilderAgent("acct-1")
	ctx := postProcessCtx()

	cases := []struct {
		name string
		in   string
	}{
		{"fenced json", "```json\n{\"name\":\"x\"}\n```"},
		{"prose with fenced json example", "Here is the definition:\n```json\n{\"name\":\"x\"}\n```\nIt runs on a schedule."},
		{"markdown summary with braces", "**Built and saved**\n\nThe automation `{x}` is ready."},
		{"raw json", `{"name":"x","definition":{"version":"v1"}}`},
		{"plain prose", "Here is what I found."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := core.NBAgentResponse{Response: []string{tc.in}, IsTerminal: true}
			out := agent.PostProcessResponse(ctx, core.NBAgentRequest{}, resp)
			assert.Equal(t, []string{tc.in}, out.Response, "PostProcessResponse must not mutate the response")
		})
	}
}

func TestWorkflowBuilderAgent_PostProcessResponse_EmptyResponse(t *testing.T) {
	agent := newWorkflowBuilderAgent("acct-1")
	resp := core.NBAgentResponse{IsTerminal: true}
	out := agent.PostProcessResponse(postProcessCtx(), core.NBAgentRequest{}, resp)
	assert.Empty(t, out.Response)
}
