package core

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
)

// A document's search hit and its read share one reference key. When the hit is saved first, the read still leaves its
// handle and text on the row: a fix step's runbook line is checked against them.
func TestSaveAgentReferencesReadFillsInTheSearchHit(t *testing.T) {
	dsn := os.Getenv("REMEDIATION_RUN_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set REMEDIATION_RUN_TEST_DB_URL to a disposable PostgreSQL database")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	schema := "refs_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE") })
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE llm_conversation_references (id uuid PRIMARY KEY, account_id text, conversation_id text, message_id text,
		agent_id uuid, reference_id text, reference_type varchar, metadata jsonb, created_at timestamptz);
		CREATE UNIQUE INDEX idx_conversation_refs_unique ON llm_conversation_references (agent_id, reference_id, reference_type, message_id)`)
	require.NoError(t, err)
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: db}}

	agent, kb := uuid.NewString(), uuid.NewString()
	save := func(message string, metadata map[string]any) {
		t.Helper()
		require.NoError(t, dao.SaveAgentReferences("acc", "conv", message, agent,
			[]AgentReference{{Type: AgentReferenceTypeKB, ReferenceID: kb, Metadata: metadata}}))
	}
	rows := func(message string) []map[string]any {
		t.Helper()
		var raw []string
		require.NoError(t, db.Select(&raw, `SELECT metadata::text FROM llm_conversation_references WHERE message_id = $1`, message))
		out := make([]map[string]any, len(raw))
		for i, r := range raw {
			require.NoError(t, json.Unmarshal([]byte(r), &out[i]))
		}
		return out
	}
	hit := map[string]any{"via": "search_skills", "kind": "skill", "name": "Runbook", "description": "# Runbook"}
	read := map[string]any{"via": "load_skills", "kind": "kb_document", "name": "Runbook", "handle": "knowledge:a", "content": "1: Step 4"}

	save("hit-then-read", hit)
	save("hit-then-read", read)
	got := rows("hit-then-read")
	require.Len(t, got, 1, "still one row for the document")
	assert.Equal(t, "knowledge:a", got[0]["handle"])
	assert.Equal(t, "1: Step 4", got[0]["content"])
	assert.Equal(t, "load_skills", got[0]["via"])

	save("read-then-hit", read)
	save("read-then-hit", hit)
	got = rows("read-then-hit")
	require.Len(t, got, 1)
	assert.Equal(t, "knowledge:a", got[0]["handle"], "a later hit does not take the read's place")
	assert.Equal(t, "load_skills", got[0]["via"])

	save("two-reads", read)
	save("two-reads", map[string]any{"via": "load_skills", "name": "Runbook", "handle": "knowledge:b", "content": "1: Step 5"})
	got = rows("two-reads")
	require.Len(t, got, 1)
	assert.Equal(t, "knowledge:a", got[0]["handle"], "the first read keeps its row")

	save("hit-only", hit)
	assert.Nil(t, rows("hit-only")[0]["handle"])
}
