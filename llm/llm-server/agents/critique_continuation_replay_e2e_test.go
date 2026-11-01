//go:build e2e

package agents

import (
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"
)

// TestCritiqueContinuationReplay replays stored investigations in-process with
// LLM_CRITIQUE_MODE=continuation, so the post-answer critiques run through this
// branch's code against real models and the TEST_ACCOUNT cluster.
//
// Cases come from a local JSON file (CRITIQUE_REPLAY_CASES), never the repo,
// because stored investigation text is tenant data:
//
//	[{"message_id": "...", "query": "Identify RootCause ...", "config": {...}, "title": "..."}]
//
// The test only runs the investigations and logs each new conversation id; the
// critique rows (llm_conversation_agent_critiques: audit_metadata mode,
// continuation_error, guidance) and cached tokens are read from the database.
func TestCritiqueContinuationReplay(t *testing.T) {
	path := os.Getenv("CRITIQUE_REPLAY_CASES")
	if path == "" {
		t.Skip("set CRITIQUE_REPLAY_CASES to a JSON file of stored investigations")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cases []struct {
		MessageID string                 `json:"message_id"`
		Query     string                 `json:"query"`
		Config    toolcore.NBQueryConfig `json:"config"`
		Title     string                 `json:"title"`
	}
	require.NoError(t, json.Unmarshal(data, &cases))
	require.NotEmpty(t, cases)

	previous := []bool{config.Config.ClaimCritiqueFeedbackEnabled, config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled}
	t.Cleanup(func() {
		config.Config.ClaimCritiqueFeedbackEnabled, config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled = previous[0], previous[1], previous[2]
	})
	config.Config.ClaimCritiqueFeedbackEnabled = true
	config.Config.ClaimCritiqueActionableFeedbackEnabled = true
	config.Config.CritiqueContinuationEnabled = true

	var mu sync.Mutex
	results := map[string]string{}
	for _, c := range cases {
		c := c
		t.Run(c.MessageID, func(t *testing.T) {
			t.Parallel()
			// A replay must never inherit approvals granted in the original run.
			c.Config.ToolConfirmations = nil
			tc := defaultCase(c.Title, uuid.NewString(), c.Query)
			agent := newK8sOrchestratorAgent(tc.AccountId)
			resp, err := core.HandleConversationSessionRequest(newSC(tc), agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query,
				core.ConversationSessionRequestWithConfig(c.Config),
				core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation))
			require.NoError(t, err)
			mu.Lock()
			results[c.MessageID] = resp.ConversationId
			mu.Unlock()
			t.Logf("replayed %s (%s): conversation=%s status=%s", c.MessageID, c.Title, resp.ConversationId, resp.Status)
		})
	}
}
