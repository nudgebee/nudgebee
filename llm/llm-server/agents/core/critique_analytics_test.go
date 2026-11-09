package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	analyticsGrantTenant   = "88888888-8888-8888-8888-888888888888"
	analyticsGrantAccountA = "88888888-0000-0000-0000-00000000000a"
	analyticsGrantAccountB = "88888888-0000-0000-0000-00000000000b"
)

func analyticsGrantCaller(t *testing.T, roles []string, scoped map[string][]string, perms map[string]bool, scopedPerms map[string]map[string]bool) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	raw, err := json.Marshal(map[string]any{
		"TenantId": analyticsGrantTenant, "UserId": "u1", "AccountIds": []string{analyticsGrantAccountA, analyticsGrantAccountB},
		"Roles": roles, "ScopedEntityIds": scoped,
		"CustomPermissions": perms, "ScopedCustomPermissions": scopedPerms,
	})
	require.NoError(t, err)
	require.NoError(t, sc.UnmarshalJSON(raw))
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

// TestGetCritiqueList_ConversationLink is a regression test for #35815: the
// Browse view's row now needs account_id/conversation_id/message_id (already
// columns on llm_conversation_agent_critiques, just not selected) plus
// session_id (looked up from llm_conversations) to build a "Go to
// conversation" deep-link.
func TestGetCritiqueList_ConversationLink(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT session_id FROM llm_conversations`).WillReturnRows(
		sqlmock.NewRows([]string{
			"id", "agent_name", "decision", "input", "critiqued_content", "feedback", "created_at",
			"account_id", "conversation_id", "message_id", "session_id",
		}).
			AddRow("crit-1", "k8s_debug", "refine", "why is pod crashing", "### Node Status\n- Ready", "use the logs tool",
				now, "acc-1", "conv-1", "msg-1", "sess-abc"))

	filter := CritiqueFilter{StartDate: now.Add(-24 * time.Hour), EndDate: now}
	out, err := dao.GetCritiqueList(filter, 50, 0)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, out.Rows, 1)
	row := out.Rows[0]
	assert.Equal(t, "acc-1", row.AccountID)
	assert.Equal(t, "conv-1", row.ConversationID)
	assert.Equal(t, "msg-1", row.MessageID)
	assert.Equal(t, "sess-abc", row.SessionID)
}

// TestGetCritiqueSummary_ByAgentAcceptedCount is a regression test for #35817:
// the Agents table's by-agent breakdown now also carries the accepted count
// (previously only judged/refined, forcing the UI to derive it).
func TestGetCritiqueSummary_ByAgentAcceptedCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	mock.ExpectQuery("judged").WillReturnRows(sqlmock.NewRows([]string{"judged", "refined"}).AddRow(10, 4))
	mock.ExpectQuery("GROUP BY agent_name").WillReturnRows(
		sqlmock.NewRows([]string{"agent_name", "judged", "refined", "accepted"}).
			AddRow("k8s_orchestrator", 10, 4, 6))
	mock.ExpectQuery("root_cause_not_verified").WillReturnRows(
		sqlmock.NewRows([]string{
			"root_cause_not_verified", "incomplete", "manual_action", "evidence",
			"verify", "guessing", "hallucination", "symptom_not_cause", "schema_validation",
		}).AddRow(0, 0, 0, 0, 0, 0, 0, 0, 0))

	filter := CritiqueFilter{StartDate: now.Add(-24 * time.Hour), EndDate: now}
	summary, err := dao.GetCritiqueSummary(filter)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, summary.ByAgent, 1)
	assert.Equal(t, int64(6), summary.ByAgent[0].Accepted)
	assert.Equal(t, int64(10), summary.ByAgent[0].Judged)
	assert.Equal(t, int64(4), summary.ByAgent[0].Refined)
}

func TestCritiqueAnalyticsExcludeShadow(t *testing.T) {
	// Summary, trend, theme examples and browse all share this predicate. It
	// excludes shadow accept/refine as well as unfinished/skipped shadow rows.
	for _, filter := range []CritiqueFilter{
		{}, {Decisions: []string{"accept", "refine"}}, {AgentNames: []string{"aws"}}, {Theme: "evidence"},
	} {
		where, _ := filter.buildWhere()
		require.Contains(t, where, "critique_type IS DISTINCT FROM 'claim_shadow'")
	}
}

// A tenant admin's critique reads are limited to their own tenant's accounts;
// only a platform super admin reads every tenant.
func TestCritiqueScopeFor(t *testing.T) {
	tenantAccounts := []string{analyticsGrantAccountA, analyticsGrantAccountB}
	for _, role := range []string{"tenant_admin", "tenant_admin_readonly"} {
		scope := CritiqueScopeFor(analyticsGrantCaller(t, []string{role}, nil, nil, nil).GetSecurityContext())
		assert.False(t, scope.AllTenants, role)
		assert.ElementsMatch(t, tenantAccounts, scope.AccountIDs, role)
	}
	for _, role := range []string{"super_admin", "super_admin_readonly"} {
		scope := CritiqueScopeFor(analyticsGrantCaller(t, []string{role}, nil, nil, nil).GetSecurityContext())
		assert.True(t, scope.AllTenants, role)
	}
	assert.Equal(t, CritiqueScope{}, CritiqueScopeFor(nil))
}

func TestCritiqueFilter_ScopeRestrictsAccounts(t *testing.T) {
	scoped := CritiqueFilter{Scope: CritiqueScope{AccountIDs: []string{analyticsGrantAccountA}}}
	where, args := scoped.buildWhere()
	assert.Contains(t, where, "account_id = ANY($3::uuid[])")
	assert.Equal(t, pq.Array([]string{analyticsGrantAccountA}), args[2])

	// No accounts still adds the restriction, so the read returns nothing.
	where, _ = CritiqueFilter{}.buildWhere()
	assert.Contains(t, where, "account_id = ANY($3::uuid[])")

	where, _ = CritiqueFilter{Scope: CritiqueScope{AllTenants: true}}.buildWhere()
	assert.NotContains(t, where, "account_id")
}

// Every query behind the summary carries the caller's accounts.
func TestGetCritiqueSummary_ScopedToCallerAccounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()
	accounts := `{"` + analyticsGrantAccountA + `"}`

	mock.ExpectQuery("account_id = ANY").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), accounts).
		WillReturnRows(sqlmock.NewRows([]string{"judged", "refined"}).AddRow(1, 0))
	mock.ExpectQuery("account_id = ANY").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), accounts).
		WillReturnRows(sqlmock.NewRows([]string{"agent_name", "judged", "refined", "accepted"}))
	// The themes query appends its pattern arguments after the scope.
	mock.ExpectQuery(`account_id = ANY\(\$3::uuid\[\]\)`).
		WillReturnRows(sqlmock.NewRows([]string{
			"root_cause_not_verified", "incomplete", "manual_action", "evidence",
			"verify", "guessing", "hallucination", "symptom_not_cause", "schema_validation",
		}).AddRow(0, 0, 0, 0, 0, 0, 0, 0, 0))

	_, err = dao.GetCritiqueSummary(CritiqueFilter{
		StartDate: now.Add(-24 * time.Hour), EndDate: now,
		Scope: CritiqueScope{AccountIDs: []string{analyticsGrantAccountA}},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
