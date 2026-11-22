package analytics

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
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

// analyticsGrantMockDB backs the metastore and the analytics store with a
// mock that holds the tenant's feature flag rows (the account report checks
// one first) and fails anything else, so a call let past the account check
// ends in a database error, never a forbidden one.
func analyticsGrantMockDB(t *testing.T) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`FROM feature_flag`).WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(analyticsGrantTenant))
	dbm := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) { return dbm, nil })
	storeMutex.Lock()
	saved := store
	store = &Store{dbManager: dbm}
	storeMutex.Unlock()
	t.Cleanup(func() {
		storeMutex.Lock()
		store = saved
		storeMutex.Unlock()
		common.RegisterDatabaseManagerHook(common.Metastore, nil)
		_ = db.Close()
	})
}

// Each cost-analyser action checks the requested account against its own
// custom-role module (action_keys.json): a built-in role on the account as
// before, or a Read or Write grant on the action's module. Another module's
// grant, an Execute grant and an account-bound grant on another account are
// refused.
func TestCostAnalyserAccountModuleGrants(t *testing.T) {
	const start, end = "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z"
	acct := []string{analyticsGrantAccountB}
	actions := []struct {
		name   string
		module string
		call   func(ctx *security.RequestContext) error
	}{
		{"ai_aggregate_tool_usage", "ai_tools", func(ctx *security.RequestContext) error {
			_, err := HandleListToolUsageApi(ctx, ListToolUsageRequest{AccountIds: acct, StartDate: start, EndDate: end})
			return err
		}},
		{"ai_list_tool_calls", "ai_tools", func(ctx *security.RequestContext) error {
			_, err := HandleListToolCallsApi(ctx, ListToolCallsRequest{AccountIds: acct, StartDate: start, EndDate: end, ToolName: "kubectl"})
			return err
		}},
		{"ai_list_agent_costs", "ai_agents", func(ctx *security.RequestContext) error {
			_, err := HandleListAgentCostsApi(ctx, ListAgentCallsRequest{AccountIds: acct, StartDate: start, EndDate: end})
			return err
		}},
		{"ai_aggregate_usage_metrics", "ai_misc", func(ctx *security.RequestContext) error {
			_, err := HandleUsageMetricsApi(ctx, UsageMetricsRequest{AccountIds: acct, StartDate: start, EndDate: end})
			return err
		}},
		{"ai_get_usage_filters", "ai_misc", func(ctx *security.RequestContext) error {
			_, err := HandleUsageFiltersApi(ctx, UsageFiltersRequest{AccountIds: acct, StartDate: start, EndDate: end})
			return err
		}},
		{"ai_aggregate_account_cost_report", "ai_misc", func(ctx *security.RequestContext) error {
			_, err := HandleAiCostAccountReportApi(ctx, AiCostAccountReportRequest{AccountIds: acct, ReferenceDate: "2026-01-02"})
			return err
		}},
		// Unchanged: the conversation explorer stays on ai_conversations.
		{"ai_list_conversation_costs", "ai_conversations", func(ctx *security.RequestContext) error {
			_, err := HandleListConversationCostsApi(ctx, ListConversationCostsRequest{AccountIds: acct, StartDate: start, EndDate: end})
			return err
		}},
	}
	for _, a := range actions {
		other := "ai_conversations"
		if a.module == other {
			other = "ai_misc"
		}
		cases := []struct {
			name  string
			ctx   *security.RequestContext
			allow bool
		}{
			{"tenant_admin", analyticsGrantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil), true},
			{"account_admin_readonly_b", analyticsGrantCaller(t, []string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE},
				map[string][]string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE: {analyticsGrantAccountB}}, nil, nil), true},
			{"account_admin_a", analyticsGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
				map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {analyticsGrantAccountA}}, nil, nil), false},
			{"module_read", analyticsGrantCaller(t, nil, nil, map[string]bool{a.module + ":Read": true}, nil), true},
			{"module_write", analyticsGrantCaller(t, nil, nil, map[string]bool{a.module + ":Write": true}, nil), true},
			{"module_read_scoped_b", analyticsGrantCaller(t, nil, nil, nil,
				map[string]map[string]bool{analyticsGrantAccountB: {a.module + ":Read": true}}), true},
			{"module_read_scoped_a", analyticsGrantCaller(t, nil, nil, nil,
				map[string]map[string]bool{analyticsGrantAccountA: {a.module + ":Read": true}}), false},
			{"module_execute", analyticsGrantCaller(t, nil, nil, map[string]bool{a.module + ":Execute": true}, nil), false},
			{"other_module_read", analyticsGrantCaller(t, nil, nil, map[string]bool{other + ":Read": true}, nil), false},
			{"no_role", analyticsGrantCaller(t, nil, nil, nil, nil), false},
		}
		for _, tc := range cases {
			t.Run(a.name+"/"+tc.name, func(t *testing.T) {
				analyticsGrantMockDB(t)
				err := a.call(tc.ctx)
				forbidden := err != nil && strings.Contains(err.Error(), "forbidden account_id")
				if tc.allow {
					assert.False(t, forbidden, "want allowed, got %v", err)
				} else {
					assert.True(t, forbidden, "want refused, got %v", err)
				}
			})
		}
	}
}
