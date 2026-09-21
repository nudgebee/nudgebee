package core

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Uses a connection-local temporary table, never application tables.
// KNOWLEDGE_TEST_POSTGRES_URL enables the real PostgreSQL regression.
func TestManualKnowledgePostgresChunks(t *testing.T) {
	dsn := os.Getenv("KNOWLEDGE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set KNOWLEDGE_TEST_POSTGRES_URL to run PostgreSQL regression")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TEMP TABLE llm_knowledgebases (id text, account_id text, name text, kb_type text, integration_id text, data text NOT NULL, status text, enabled boolean)`)
	require.NoError(t, err)
	old := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	}
	defer func() { knowledgeDatabase = old }()
	ctx := security.NewRequestContextForSuperAdmin()
	for _, body := range []string{"small document", strings.Repeat("界🙂\n", 300000) + "FINAL-CANARY-92817"} {
		_, err = db.Exec(`TRUNCATE llm_knowledgebases`)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO llm_knowledgebases VALUES ('kb', 'account', 'fixture', 'manual', NULL, $1, 'active', true)`, body)
		require.NoError(t, err)
		c, _, err := ResolveKnowledgeSelection(ctx, "account", "fixture", "")
		require.NoError(t, err)
		var out bytes.Buffer
		require.NoError(t, WriteManualKnowledge(ctx, "account", c, &out))
		require.Equal(t, body, out.String(), "all Unicode chunks and the tail must survive")
		out.Reset()
		require.Error(t, WriteManualKnowledge(ctx, "other-account", c, &out))
		require.Zero(t, out.Len())
		_, err = db.Exec(`UPDATE llm_knowledgebases SET data = data || 'changed'`)
		require.NoError(t, err)
		require.Error(t, WriteManualKnowledge(ctx, "account", c, &out))
		require.Zero(t, out.Len())
	}
}
