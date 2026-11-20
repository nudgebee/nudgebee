//go:build e2e

package agents

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetricsQueryAgentSingleQuery_E2E covers issue #293 end to end: a direct
// "give me a query for X" ask through the real metrics_query chain must yield
// exactly one runnable provider-native query — never a markdown explainer with
// headings, commentary, or a menu of example queries.
func TestMetricsQueryAgentSingleQuery_E2E(t *testing.T) {
	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	if accountID == "" || userID == "" {
		t.Skip("skipping: TEST_ACCOUNT / TEST_USER not set")
	}

	ctx := security.NewRequestContextForSuperAdmin()
	agent, err := NewMetricsQueryAgent(ctx, accountID, "")
	require.NoError(t, err)

	sessionID := "ut-metrics-query-e2e-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	t.Cleanup(func() { _ = core.DeleteConversationBySession(sessionID, accountID, userID) })

	resp, err := core.HandleConversationSessionRequest(ctx, agent, userID, accountID, sessionID, "give me a query for cpu usage of pods in the default namespace")
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Response, "metrics query generation must return an answer")
	if resp.Status != core.ConversationStatusCompleted {
		t.Fatalf("expected a completed query response, got status=%s response=%s", resp.Status, resp.Response[0])
	}

	var envelope metricsQueryResult
	require.NoError(t, json.Unmarshal([]byte(resp.Response[0]), &envelope), "response must be the query envelope")
	require.NotEmpty(t, strings.TrimSpace(envelope.Query), "query must be non-empty")

	lower := strings.ToLower(envelope.Query)
	assert.NotContains(t, lower, "```", "query must not contain markdown fences")
	assert.NotContains(t, lower, "###", "query must not contain markdown headings")

	switch strings.ToLower(envelope.Provider) {
	case "datadog":
		// Bare-query contract; there is no Go parser for Datadog syntax to assert with.
	case "es", "elasticsearch", "opensearch":
		require.True(t, json.Valid([]byte(envelope.Query)), "ES query must be valid JSON DSL, got: %s", envelope.Query)
	default:
		// PromQL-language provider: the query must parse as one expression.
		clean := strings.ReplaceAll(envelope.Query, "__CLUSTER__", "")
		_, perr := promQLParser.ParseExpr(clean)
		require.NoError(t, perr, "query must parse as a single PromQL expression, got: %s", envelope.Query)
	}
}
