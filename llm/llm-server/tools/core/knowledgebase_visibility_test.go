package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// The cases are shared with rag-server's live-KB gate, so both services apply
// one visibility rule.
const kbVisibilityFixture = "../../../rag-server/tests/fixtures/kb_visibility_cases.json"

type kbVisibilityFixtureFile struct {
	Accounts map[string]string `json:"accounts"`
	Cases    []struct {
		Name     string            `json:"name"`
		Account  *string           `json:"account"`
		Rows     []kbVisibilityRow `json:"rows"`
		Expected []string          `json:"expected"`
	} `json:"cases"`
}

func TestVisibleKBCollectionsMatchesSharedCases(t *testing.T) {
	raw, err := os.ReadFile(kbVisibilityFixture)
	require.NoError(t, err)
	var fixture kbVisibilityFixtureFile
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.NotEmpty(t, fixture.Cases)

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			account := ""
			if c.Account != nil {
				account = fixture.Accounts[*c.Account]
			}
			rows := make([]kbVisibilityRow, len(c.Rows))
			for i, r := range c.Rows {
				if r.AccountId != nil {
					id := fixture.Accounts[*r.AccountId]
					r.AccountId = &id
				}
				rows[i] = r
			}
			var got []string
			for name := range visibleKBCollections(rows, account) {
				got = append(got, name)
			}
			sort.Strings(got)
			want := append([]string{}, c.Expected...)
			sort.Strings(want)
			if len(want) == 0 {
				want = nil
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestKBAccountScopeIsUnchangedWithoutOrganisationKBs(t *testing.T) {
	clause, args := KBAccountScope("kb", 1, 3, nil)
	assert.Equal(t, "kb.account_id = $1", clause)
	assert.Empty(t, args)

	clause, args = KBAccountScope("kb", 2, 4, []string{"o1"})
	assert.Equal(t, "(kb.account_id = $2 OR kb.id = ANY($4::uuid[]))", clause)
	assert.Len(t, args, 1)

	clause, _ = KBAccountScope("", 1, 2, []string{"o1"})
	assert.Equal(t, "(account_id = $1 OR id = ANY($2::uuid[]))", clause)
}

func mockVisibilityDatabase(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	old := visibilityDatabase
	visibilityDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { visibilityDatabase = old; _ = db.Close() })
	return mock
}

func TestLoadVisibleOrgKnowledgebaseIDs(t *testing.T) {
	mock := mockVisibilityDatabase(t)
	columns := []string{"id", "account_id", "integration_id", "enabled", "name", "visibility", "override_enabled"}
	mock.ExpectQuery(`FROM llm_knowledgebases kb\s+JOIN cloud_accounts ca ON ca.id = \$1 AND ca.tenant = kb.tenant_id`).
		WithArgs("account-a").
		WillReturnRows(sqlmock.NewRows(columns).
			AddRow("own", "account-a", nil, true, "DB failover", "account", nil).
			AddRow("shadowed", nil, nil, true, "db failover", "organisation", nil).
			AddRow("shared", nil, nil, true, "Payments", "organisation", nil).
			AddRow("switched-off", nil, nil, true, "Network", "organisation", false).
			AddRow("restricted", nil, nil, true, "Billing", "selected_accounts", nil).
			AddRow("selected", nil, nil, true, "Storage", "selected_accounts", true))

	ids := loadVisibleOrgKnowledgebaseIDs(context.Background(), "account-a")

	assert.ElementsMatch(t, []string{"shared", "selected"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadVisibleOrgKnowledgebaseIDsHidesAllOnQueryFailure(t *testing.T) {
	mock := mockVisibilityDatabase(t)
	mock.ExpectQuery(`FROM llm_knowledgebases kb`).WillReturnError(errors.New("connection reset"))

	assert.Empty(t, loadVisibleOrgKnowledgebaseIDs(context.Background(), "account-a"))
}

func TestResolveKnowledgeSelectionReadsAVisibleOrganisationKB(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase, oldVisible := knowledgeDatabase, VisibleOrgKnowledgebaseIDs
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	VisibleOrgKnowledgebaseIDs = func(context.Context, string) []string { return []string{"org-kb"} }
	t.Cleanup(func() { knowledgeDatabase, VisibleOrgKnowledgebaseIDs = oldDatabase, oldVisible })

	mock.ExpectQuery(`FROM llm_knowledgebases WHERE \(account_id = \$1 OR id = ANY\(\$4::uuid\[\]\)\) AND status='active'`).
		WithArgs("account-a", "", "DB failover", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "kb_type", "integration_id", "md5", "octet_length", "note_category", "data_format"}).
			AddRow("org-kb", "DB failover", "manual", nil, "v1", 10, "sop", "text"))

	c, _, err := ResolveKnowledgeSelection(security.NewRequestContextForSuperAdmin(), "account-a", "DB failover", "")

	require.NoError(t, err)
	assert.Equal(t, "org-kb", c.KBID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Each widened reader must add the organisation ids as its last argument,
// numbered after its own. A wrong placeholder only fails when ids exist, so
// these run every reader with a visible organisation KB.
func TestReadersQueryVisibleOrganisationKBs(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	db := sqlx.NewDb(raw, "postgresql")
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) { return &common.DatabaseManager{Db: db}, nil })
	manager, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	oldDB, oldKnowledge, oldVisible := manager.Db, knowledgeDatabase, VisibleOrgKnowledgebaseIDs
	manager.Db = db
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	}
	VisibleOrgKnowledgebaseIDs = func(context.Context, string) []string { return []string{"org-kb"} }
	t.Cleanup(func() {
		manager.Db, knowledgeDatabase, VisibleOrgKnowledgebaseIDs = oldDB, oldKnowledge, oldVisible
		common.RegisterDatabaseManagerHook(common.Metastore, nil)
		_ = raw.Close()
	})
	sc := security.NewRequestContextForSuperAdmin()

	t.Run("GetKnowledgebase", func(t *testing.T) {
		mock.ExpectQuery(`WHERE kb.id = \$1 AND \(kb.account_id = \$2 OR kb.id = ANY\(\$3::uuid\[\]\)\)`).
			WithArgs("org-kb", "account-a", sqlmock.AnyArg()).
			WillReturnError(errors.New("stop"))
		_, err := GetKnowledgebase(sc, "account-a", "org-kb")
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("ListKnowledgebases", func(t *testing.T) {
		mock.ExpectQuery(`WHERE \(kb.account_id = \$1 OR kb.id = ANY\(\$2::uuid\[\]\)\)\s+ORDER BY kb.created_at DESC`).
			WithArgs("account-a", sqlmock.AnyArg()).
			WillReturnError(errors.New("stop"))
		_, err := ListKnowledgebases(sc, "account-a")
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("LoadAgentSkillContentsByIDs", func(t *testing.T) {
		mock.ExpectQuery(`WHERE \(kb.account_id = \$1 OR kb.id = ANY\(\$3::uuid\[\]\)\)\s+AND kb.id = ANY\(\$2::uuid\[\]\)`).
			WithArgs("account-a", sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnError(errors.New("stop"))
		_, _, err := LoadAgentSkillContentsByIDs(sc, "account-a", []string{"org-kb"})
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("ListKBAgents", func(t *testing.T) {
		mock.ExpectQuery(`WHERE id = \$1 AND \(account_id = \$2 OR id = ANY\(\$3::uuid\[\]\)\)`).
			WithArgs("org-kb", "account-a", sqlmock.AnyArg()).
			WillReturnError(errors.New("stop"))
		_, err := ListKBAgents(sc, "account-a", "org-kb")
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("GetKBLoadHistory", func(t *testing.T) {
		mock.ExpectQuery(`FROM llm_knowledgebases WHERE id = \$1 AND \(account_id = \$2 OR id = ANY\(\$3::uuid\[\]\)\)`).
			WithArgs("org-kb", "account-a", sqlmock.AnyArg()).
			WillReturnError(errors.New("stop"))
		_, err := GetKBLoadHistory(sc, "account-a", "org-kb")
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("ResolveManualKnowledge", func(t *testing.T) {
		mock.ExpectQuery(`WHERE \(account_id = \$1 OR id = ANY\(\$3::uuid\[\]\)\) AND id::text = ANY\(\$2::text\[\]\)`).
			WithArgs("account-a", sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "data_format", "note_category"}))
		ResolveManualKnowledge(sc, "account-a", RAGSearchResults{{Document: "x", Metadata: map[string]any{"collection": "kb_org-kb"}}})
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
