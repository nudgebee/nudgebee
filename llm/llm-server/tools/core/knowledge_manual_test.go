package core

import (
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"strings"
	"testing"
)

func TestResolveManualKnowledgeCanonicalIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT id, name, LEFT.* FROM llm_knowledgebases WHERE account_id = \$1.*status = 'active' AND enabled`).WithArgs("account-a", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data"}).AddRow("manual-a", "Runbook", "FULL-CONTENT-TAIL"))
	docs := RAGSearchResults{
		{Document: "partial", Metadata: map[string]any{"collection": "kb_manual-a", "source": "/tmp/file.json"}},
		{Document: "other chunk", Metadata: map[string]any{"collection": "kb_manual-a"}},
		{Document: "foreign or disabled", Metadata: map[string]any{"collection": "kb_denied"}},
		{Document: "external article", Metadata: map[string]any{"collection": "integration_knowledge_base", "url": "https://example.test"}},
	}
	got := ResolveManualKnowledge(security.NewRequestContextForSuperAdmin(), "account-a", docs)
	require.Len(t, got, 2)
	assert.Equal(t, "FULL-CONTENT-TAIL", got[0].Document)
	assert.Equal(t, "manual-a", got[0].Metadata["kb_id"])
	assert.Equal(t, "Runbook", got[0].Metadata["title"])
	assert.Equal(t, "manual", got[0].Metadata["source"])
	assert.Equal(t, docs[3], got[1])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeCandidateRejectsOversizedEntry(t *testing.T) {
	candidate := KnowledgeCandidate{ID: "knowledge:oversized", Content: strings.Repeat("x", MaxKnowledgeCandidateBytes)}
	require.ErrorContains(t, StoreKnowledgeCandidate(t.Name(), "conv", "msg", candidate), "exceeds")
	_, ok := LoadKnowledgeCandidate(t.Name(), "conv", "msg", candidate.ID)
	assert.False(t, ok)
}
