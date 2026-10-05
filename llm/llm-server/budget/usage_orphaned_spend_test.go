package budget

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/events"
)

// Spend from a deleted conversation stays in llm_conversation_token_usage with
// conversation_id NULL and the conversation's tenant/session copied onto the
// row (V986). The budget query must count those rows for the right entity and
// module, and must not count calls that never had a conversation (no tenant).
func TestGetEntityTokenUsage_CountsDeletedConversationSpend(t *testing.T) {
	cases := []struct {
		name, entityType, module   string
		orphanEntity, orphanModule string
	}{
		{"tenant chat", EntityTypeTenant, ModuleUserInvestigation,
			"t.tenant_id = $1", "t.session_id NOT LIKE '" + events.SessionIdPrefixEvent + "%'"},
		{"tenant investigation", EntityTypeTenant, ModuleInvestigation,
			"t.tenant_id = $1", "t.session_id LIKE '" + events.SessionIdPrefixEvent + "%'"},
		{"account chat", EntityTypeAccount, ModuleUserInvestigation,
			"t.account_id = $1", "t.session_id NOT LIKE '" + events.SessionIdPrefixEvent + "%'"},
		{"account investigation", EntityTypeAccount, ModuleInvestigation,
			"t.account_id = $1", "t.session_id LIKE '" + events.SessionIdPrefixEvent + "%'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dm, mock := newMockMetastoreDM(t)
			orphanBranch := regexp.QuoteMeta("WHERE t.conversation_id IS NULL AND t.tenant_id IS NOT NULL") +
				`\s+AND ` + regexp.QuoteMeta(tc.orphanEntity) +
				`\s+AND t\.created_at >= DATE_TRUNC\('month', CURRENT_TIMESTAMP\)` +
				`[\s\S]*?` + regexp.QuoteMeta(tc.orphanModule)
			mock.ExpectQuery(orphanBranch).
				WithArgs("entity-1").
				WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(12.5))

			cost, err := getEntityTokenUsage(dm, tc.entityType, "entity-1", tc.module, "month")

			require.NoError(t, err)
			assert.Equal(t, 12.5, cost)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// The live-conversation branch keeps its existing shape: joined to the
// conversation, bucketed by the conversation's creation time.
func TestGetEntityTokenUsage_LiveBranchUnchanged(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery(regexp.QuoteMeta("INNER JOIN llm_conversations c ON c.id = t.conversation_id") +
		`\s+WHERE c\.tenant_id = \$1\s+AND c\.created_at >= DATE_TRUNC\('day', CURRENT_TIMESTAMP\)`).
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(nil))

	cost, err := getEntityTokenUsage(dm, EntityTypeTenant, "tenant-1", ModuleUserInvestigation, "day")

	require.NoError(t, err)
	assert.Equal(t, 0.0, cost)
	assert.NoError(t, mock.ExpectationsWereMet())
}
