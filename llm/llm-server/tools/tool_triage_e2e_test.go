//go:build e2e

package tools

import (
	"encoding/json"
	"os"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// Integration tests for the read-only triage / event-rule tools.
//
// These hit a LIVE api-server and are excluded from the default `make test`
// run by the `e2e` build tag. Run with:
//
//	SERVICE_API_SERVER_URL=http://localhost:8120 \
//	ACTION_API_SERVER_TOKEN=<token> \
//	TEST_TENANT=<tenant-uuid> TEST_ACCOUNT=<account-uuid> TEST_USER=<user-uuid> \
//	go test -tags e2e -run TestTriageToolsE2E ./tools/ -v
//
// Optional: TEST_TRIAGE_EVENT_ID=<event-uuid> exercises get_triage_explanation.
// Each subtest asserts the live call succeeds and returns parseable JSON; the
// number of rows depends on the target account's data.

func newE2EToolContext(t *testing.T, tool core.NBTool) core.NbToolContext {
	t.Helper()
	tenant := os.Getenv("TEST_TENANT")
	account := os.Getenv("TEST_ACCOUNT")
	user := os.Getenv("TEST_USER")
	if tenant == "" || account == "" || user == "" {
		t.Skip("set TEST_TENANT, TEST_ACCOUNT and TEST_USER to run triage e2e tests")
	}
	if os.Getenv("SERVICE_API_SERVER_URL") == "" {
		t.Skip("set SERVICE_API_SERVER_URL (and ACTION_API_SERVER_TOKEN) to reach a live api-server")
	}

	// Pass the account explicitly so the security context is built in-memory —
	// an empty account list makes the constructor do a DB lookup, which we don't
	// need here (api-server does all DB work server-side from the tenant header).
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{account})
	require.NotNil(t, sc, "failed to build security context")
	return core.NewNbToolContext(
		sc, tool, account, user,
		uuid.NewString(), uuid.NewString(), uuid.NewString(),
		"", []llms.MessageContent{}, "", core.NBQueryConfig{}, "",
	)
}

// assertJSONSuccess checks the tool returned a success response with a JSON body.
func assertJSONSuccess(t *testing.T, resp core.NBToolResponse, err error) {
	t.Helper()
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, "tool error: %s", resp.Data)

	var anyJSON any
	assert.NoError(t, json.Unmarshal([]byte(resp.Data), &anyJSON), "response is not valid JSON: %s", resp.Data)
}

func TestTriageToolsE2E(t *testing.T) {
	t.Run("get_triage_rules", func(t *testing.T) {
		tool := TriageRulesTool{}
		resp, err := tool.Call(newE2EToolContext(t, tool), core.NBToolCallRequest{Arguments: map[string]any{}})
		assertJSONSuccess(t, resp, err)
	})

	t.Run("list_threshold_suggestions", func(t *testing.T) {
		tool := ThresholdSuggestionsTool{}
		resp, err := tool.Call(newE2EToolContext(t, tool), core.NBToolCallRequest{
			Arguments: map[string]any{"limit": float64(5)},
		})
		assertJSONSuccess(t, resp, err)
	})

	t.Run("get_event_rules", func(t *testing.T) {
		tool := EventRulesTool{}
		resp, err := tool.Call(newE2EToolContext(t, tool), core.NBToolCallRequest{
			Arguments: map[string]any{"limit": float64(10)},
		})
		assertJSONSuccess(t, resp, err)
		// event_rules_v2 returns a {"rows":[...]} envelope.
		var env struct {
			Rows []map[string]any `json:"rows"`
		}
		require.NoError(t, json.Unmarshal([]byte(resp.Data), &env))
		t.Logf("get_event_rules returned %d rules", len(env.Rows))
	})

	t.Run("dryrun_triage_rule", func(t *testing.T) {
		tool := TriageDryRunTool{}
		resp, err := tool.Call(newE2EToolContext(t, tool), core.NBToolCallRequest{
			Arguments: map[string]any{
				"rule_type":       "suppression",
				"action":          "suppress",
				"match_alertname": "RabbitmqNoQueueConsumer",
			},
		})
		assertJSONSuccess(t, resp, err)
		t.Logf("dryrun preview: %s", resp.Data)
	})

	t.Run("get_triage_explanation", func(t *testing.T) {
		eventID := os.Getenv("TEST_TRIAGE_EVENT_ID")
		if eventID == "" {
			t.Skip("set TEST_TRIAGE_EVENT_ID to exercise get_triage_explanation")
		}
		tool := TriageExplanationTool{}
		resp, err := tool.Call(newE2EToolContext(t, tool), core.NBToolCallRequest{
			Arguments: map[string]any{"event_id": eventID},
		})
		assertJSONSuccess(t, resp, err)
		// Bounded payload must stay well under the scratchpad observation cap.
		assert.Less(t, len(resp.Data), 8192, "explanation payload should be bounded")
		t.Logf("triage explanation (%d bytes): %s", len(resp.Data), resp.Data)
	})

	t.Run("get_event_classification", func(t *testing.T) {
		eventID := os.Getenv("TEST_TRIAGE_EVENT_ID")
		if eventID == "" {
			t.Skip("set TEST_TRIAGE_EVENT_ID to exercise get_event_classification")
		}
		tool := EventClassificationTool{}
		resp, err := tool.Call(newE2EToolContext(t, tool), core.NBToolCallRequest{
			Arguments: map[string]any{"event_id": eventID},
		})
		assertJSONSuccess(t, resp, err)
		t.Logf("classification: %s", resp.Data)
	})

	t.Run("get_triage_rule_events", func(t *testing.T) {
		// Derive a real rule_id from get_triage_rules so the test is self-contained.
		rulesResp, err := TriageRulesTool{}.Call(newE2EToolContext(t, TriageRulesTool{}), core.NBToolCallRequest{Arguments: map[string]any{}})
		require.NoError(t, err)
		var rules struct {
			Rules []struct {
				ID string `json:"id"`
			} `json:"rules"`
		}
		require.NoError(t, json.Unmarshal([]byte(rulesResp.Data), &rules))
		if len(rules.Rules) == 0 {
			t.Skip("no triage rules configured for this account")
		}

		tool := TriageRuleEventsTool{}
		resp, err := tool.Call(newE2EToolContext(t, tool), core.NBToolCallRequest{
			Arguments: map[string]any{"rule_id": rules.Rules[0].ID, "limit": float64(5)},
		})
		assertJSONSuccess(t, resp, err)
		t.Logf("rule %s matched events: %s", rules.Rules[0].ID, resp.Data)
	})
}
