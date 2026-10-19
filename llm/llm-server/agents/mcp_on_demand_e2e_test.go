//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mcpRecallCase is one real MCP tool call: the tool the orchestrator called,
// the user question it was answering, and the planner's thought for the call.
type mcpRecallCase struct {
	Tool        string `json:"tool_name"`
	Integration string `json:"integration"`
	Question    string `json:"question"`
	Thought     string `json:"thought"`
}

// TestMCPOnDemand_Recall checks that search_tools still finds the MCP
// tools the orchestrator really called, now that their integration's
// instructions are no longer part of each tool's description. For each case
// it ranks the account's live catalog (TEST_ACCOUNT) with on demand off and on,
// for three queries: the user's question, the planner's thought for the call,
// and the integration name followed by that thought (what the prompt line
// steers the model to write). A hit is a rank inside the result cap.
//
// MCP_RECALL_CASES is a JSON array of mcpRecallCase; MCP_RECALL_OUT, when set,
// receives the per-case ranks. Reads only.
func TestMCPOnDemand_Recall(t *testing.T) {
	account := os.Getenv("TEST_ACCOUNT")
	path := os.Getenv("MCP_RECALL_CASES")
	if account == "" || path == "" {
		t.Skip("TEST_ACCOUNT / MCP_RECALL_CASES not set")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var cases []mcpRecallCase
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.NotEmpty(t, cases)

	sc := security.NewRequestContextForSuperAdmin()
	catalog := func(on bool) []searchToolCandidate {
		prev := config.Config.LlmServerMCPToolsOnDemandEnabled
		config.Config.LlmServerMCPToolsOnDemandEnabled = on
		defer func() { config.Config.LlmServerMCPToolsOnDemandEnabled = prev }()
		return collectSearchToolCandidates(core.ListAgents(sc, account, true), toolcore.GetEnabledNBTools(sc, account))
	}
	off, on := catalog(false), catalog(true)

	rank := func(cands []searchToolCandidate, query, tool string) int {
		for i, c := range rankSearchTools(query, cands) {
			if strings.EqualFold(c.name, tool) {
				return i + 1
			}
		}
		return 0
	}
	hit := func(r int) bool { return r > 0 && r <= searchToolsMaxResults }

	type result struct {
		Tool                    string
		QuestionOff, QuestionOn int
		ThoughtOff, ThoughtOn   int
		NamedOff, NamedOn       int
	}
	var results []result
	var qOff, qOn, thOff, thOn, nOff, nOn int
	for _, c := range cases {
		named := c.Integration + " " + c.Thought
		r := result{
			Tool:        c.Tool,
			QuestionOff: rank(off, c.Question, c.Tool), QuestionOn: rank(on, c.Question, c.Tool),
			ThoughtOff: rank(off, c.Thought, c.Tool), ThoughtOn: rank(on, c.Thought, c.Tool),
			NamedOff: rank(off, named, c.Tool), NamedOn: rank(on, named, c.Tool),
		}
		results = append(results, r)
		for _, p := range []struct {
			r   int
			ctr *int
		}{{r.QuestionOff, &qOff}, {r.QuestionOn, &qOn}, {r.ThoughtOff, &thOff}, {r.ThoughtOn, &thOn}, {r.NamedOff, &nOff}, {r.NamedOn, &nOn}} {
			if hit(p.r) {
				*p.ctr++
			}
		}
	}
	n := len(cases)
	fmt.Printf("recall@%d over %d real MCP calls (off -> on):\n  question: %d -> %d\n  thought:  %d -> %d\n  integration + thought: %d -> %d\n",
		searchToolsMaxResults, n, qOff, qOn, thOff, thOn, nOff, nOn)
	if out := os.Getenv("MCP_RECALL_OUT"); out != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		require.NoError(t, os.WriteFile(out, b, 0o600))
	}
	assert.GreaterOrEqual(t, qOn, qOff, "recall by question dropped")
	assert.GreaterOrEqual(t, thOn, thOff, "recall by thought dropped")
	assert.GreaterOrEqual(t, nOn, nOff, "recall by integration + thought dropped")
}

// TestMCPOnDemand_Replay replays real questions that used MCP tools
// through k8s_orchestrator with MCP tools on demand, end to end. Each
// question runs in a fresh session; the conversation ids are printed so the
// tool calls (search_tools -> delegate_agent -> MCP tool) and the first
// call's input tokens can be read from llm_conversation_tool_calls and
// llm_conversation_token_usage. MCP_REPLAY_CASES is a JSON array of
// questions; MCP_ON_DEMAND=false runs them with on demand off, for a baseline
// under the same local setup. Needs the standard local service set
// (CLAUDE.md); never point TEST_ACCOUNT at a production account.
func TestMCPOnDemand_Replay(t *testing.T) {
	account := os.Getenv("TEST_ACCOUNT")
	user := os.Getenv("TEST_USER")
	path := os.Getenv("MCP_REPLAY_CASES")
	if account == "" || user == "" || path == "" {
		t.Skip("TEST_ACCOUNT / TEST_USER / MCP_REPLAY_CASES not set")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var questions []string
	require.NoError(t, json.Unmarshal(raw, &questions))
	prev := config.Config.LlmServerMCPToolsOnDemandEnabled
	config.Config.LlmServerMCPToolsOnDemandEnabled = os.Getenv("MCP_ON_DEMAND") != "false"
	defer func() { config.Config.LlmServerMCPToolsOnDemandEnabled = prev }()

	agent, ok := core.GetNBAgent(security.NewRequestContextForSuperAdmin(), AgentK8sOrchestratorName, account, core.AgentStatusEnabled)
	require.True(t, ok)
	for i, q := range questions {
		t.Run(fmt.Sprintf("q%d", i+1), func(t *testing.T) {
			if only := os.Getenv("MCP_REPLAY_ONLY"); only != "" && !strings.Contains(","+only+",", fmt.Sprintf(",%d,", i+1)) {
				t.Skip("not in MCP_REPLAY_ONLY")
			}
			tc := defaultCase(fmt.Sprintf("mcp-replay-%d", i+1), "mcp-on-demand-replay-"+uuid.NewString(), q)
			resp := runTestMinimal(t, agent, tc)
			fmt.Printf("REPLAY q%d conversation_id=%s status=%s steps=%d\n", i+1, resp.ConversationId, resp.Status, len(resp.AgentStepResponse))
		})
	}
}
