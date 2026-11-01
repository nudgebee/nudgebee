//go:build e2e

package api

import (
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventAnalyzer_KnowledgeForEventCategory runs the event investigation flow
// on the account's most recent event of one kind (default: CI failures) and
// reports what knowledge discovery did for it, from the rows the run wrote.
//
// It always asserts that discovery searched with the event's own title rather
// than only the head of the investigation prompt. Set TEST_EVENT_SKILL_NAME to
// the KB expected for that kind of event to also assert it was retrieved, and
// TEST_EVENT_SKILL_REQUIRE_LOAD=1 to assert the planner loaded it (a model
// choice, so it is reported but not asserted by default).
func TestEventAnalyzer_KnowledgeForEventCategory(t *testing.T) {
	accountID, tenantID, userID := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER")
	if accountID == "" || tenantID == "" || userID == "" {
		t.Skip("skipping: TEST_ACCOUNT, TEST_TENANT or TEST_USER unset")
	}
	aggregationKey := envOrDefault("TEST_EVENT_AGGREGATION_KEY", "github_ci_failure")
	skillName := os.Getenv("TEST_EVENT_SKILL_NAME")

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Skipf("skipping: db unavailable: %v", err)
	}
	var eventID, title, fingerprint string
	err = dbms.Db.QueryRow(
		`SELECT id, title, COALESCE(fingerprint, '') FROM events
		 WHERE cloud_account_id = $1 AND aggregation_key = $2
		 ORDER BY created_at DESC LIMIT 1`,
		accountID, aggregationKey,
	).Scan(&eventID, &title, &fingerprint)
	if err != nil {
		t.Skipf("skipping: no %s event for account: %v", aggregationKey, err)
	}
	if fingerprint == "" {
		fingerprint = eventID
	}
	t.Logf("event %s %q", eventID, title)

	started := time.Now()
	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{})
	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventID,
		AccountId:  accountID,
		UserId:     userID,
		Regenerate: true,
	})
	require.NoError(t, err)
	require.Equal(t, "COMPLETED", resp.Status)

	var conversationIDs []string
	require.NoError(t, dbms.Db.Select(&conversationIDs,
		`SELECT id FROM llm_conversations WHERE account_id = $1 AND session_id = $2`,
		accountID, events.SessionIdPrefixEvent+fingerprint))
	require.NotEmpty(t, conversationIDs, "investigation conversation not found")

	var searches []struct {
		Query     string `db:"query"`
		NInjected int    `db:"n_injected"`
	}
	require.NoError(t, dbms.Db.Select(&searches,
		`SELECT query, COALESCE(n_injected, 0) AS n_injected FROM llm_retrieval_trace
		 WHERE module = 'knowledge_base' AND conversation_id = ANY($1) AND created_at >= $2
		 ORDER BY created_at`,
		pq.Array(conversationIDs), started))
	withTitle := 0
	for _, s := range searches {
		if strings.Contains(s.Query, title) {
			withTitle++
		}
		t.Logf("search injected=%d query=%q", s.NInjected, s.Query)
	}
	assert.NotZero(t, withTitle, "no knowledge search carried the event title")

	var retrieved int
	if skillName != "" {
		require.NoError(t, dbms.Db.Get(&retrieved,
			`SELECT count(*) FROM llm_conversation_references
			 WHERE reference_type = 'knowledge_base' AND conversation_id = ANY($1) AND created_at >= $2
			   AND metadata->>'name' = $3`,
			pq.Array(conversationIDs), started, skillName))
		assert.NotZero(t, retrieved, "skill %q was not retrieved for this event", skillName)
	}

	var loads []struct {
		Parameters string `db:"parameters"`
		Response   string `db:"response"`
	}
	require.NoError(t, dbms.Db.Select(&loads,
		`SELECT COALESCE(parameters::text, '') AS parameters, COALESCE(response::text, '') AS response
		 FROM llm_conversation_tool_calls
		 WHERE tool_name = 'load_skills' AND conversation_id = ANY($1) AND created_at >= ($2::timestamptz AT TIME ZONE 'UTC')`, // created_at here is timestamp without time zone, stored in UTC
		pq.Array(conversationIDs), started))
	loadedSkill := 0
	for _, l := range loads {
		if skillName != "" && strings.Contains(l.Response, skillName) {
			loadedSkill++
		}
	}
	t.Logf("summary: searches=%d with_title=%d skill_references=%d load_skills_calls=%d loads_of_skill=%d",
		len(searches), withTitle, retrieved, len(loads), loadedSkill)
	if os.Getenv("TEST_EVENT_SKILL_REQUIRE_LOAD") == "1" {
		assert.NotZero(t, loadedSkill, "skill %q was not loaded", skillName)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
