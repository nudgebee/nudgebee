package tools

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// Skill lookups read the organisation KBs visible to the account. The ids are
// the last argument, numbered after the query's own (search_skills numbers its
// word patterns at runtime).
func TestSkillQueriesIncludeVisibleOrganisationKBs(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(raw, "postgresql")}
	old := core.VisibleOrgKnowledgebaseIDs
	core.VisibleOrgKnowledgebaseIDs = func(context.Context, string) []string { return []string{"org-kb"} }
	t.Cleanup(func() { core.VisibleOrgKnowledgebaseIDs = old })
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "account-a"}

	mock.ExpectQuery(`WHERE \(kb.account_id = \$1 OR kb.id = ANY\(\$3::uuid\[\]\)\)\s+AND LOWER\(BTRIM\(kb.name\)\) = ANY\(\$2::text\[\]\)`).
		WithArgs("account-a", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "description", "kb_type", "kb_source", "integration_id", "note_category", "data_format"}))
	_, _ = (LoadSkillsTool{}).fetchSkillsBatch(ctx, dbms, []string{"DB failover"})

	mock.ExpectQuery(`WHERE \(kb.account_id = \$1 OR kb.id = ANY\(\$4::uuid\[\]\)\)\s+AND kb.status = 'active'`).
		WithArgs("account-a", "%db%", "%failover%", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "description", "data", "note_category"}))
	_ = (SearchSkillsTool{}).searchManualKBs(ctx, dbms, "db failover")

	require.NoError(t, mock.ExpectationsWereMet())
}
