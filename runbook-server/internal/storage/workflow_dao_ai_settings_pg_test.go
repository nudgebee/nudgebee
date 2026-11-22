package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	aiTenant     = "11111111-1111-1111-1111-111111111111"
	aiOtherTen   = "22222222-2222-2222-2222-222222222222"
	aiAccount    = "33333333-3333-3333-3333-333333333333"
	aiOtherAcct  = "44444444-4444-4444-4444-444444444444"
	aiUser       = "55555555-5555-5555-5555-555555555555"
	aiLiveDefRef = "66666666-6666-6666-6666-666666666666"
)

func aiSettingsTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("postgres", requireTestDSN(t))
	require.NoError(t, err, "connect to TEST_POSTGRES_DSN")

	schema := fmt.Sprintf("ai_settings_test_%d", os.Getpid())
	_, err = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s`, schema, schema, schema))
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // keep search_path on the one connection

	_, err = db.Exec(`
		CREATE TABLE users (id uuid PRIMARY KEY, display_name text, username text);
		CREATE TABLE workflow_versions (id uuid PRIMARY KEY, definition jsonb NOT NULL);
		CREATE TABLE workflows (
			id uuid PRIMARY KEY,
			tenant_id uuid NOT NULL,
			account_id uuid NOT NULL,
			name text NOT NULL,
			description text,
			status text NOT NULL,
			definition jsonb NOT NULL DEFAULT '{}'::jsonb,
			ai_invocable boolean NOT NULL DEFAULT false,
			ai_auto_approve boolean NOT NULL DEFAULT false,
			bee jsonb,
			live_version_id uuid,
			created_by uuid,
			updated_by uuid,
			updated_at timestamp DEFAULT now()
		)`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return db
}

func insertAIWorkflow(t *testing.T, db *sqlx.DB, id, tenant, account, name string, invocable bool, draftDescription, bee string, live bool) {
	t.Helper()
	var beeVal, liveVal any
	if bee != "" {
		beeVal = bee
	}
	if live {
		liveVal = aiLiveDefRef
	}
	_, err := db.Exec(`INSERT INTO workflows (id, tenant_id, account_id, name, status, definition, ai_invocable, bee, live_version_id, created_by)
		VALUES ($1, $2, $3, $4, 'ACTIVE', jsonb_build_object('llm_description', $5::text), $6, $7::jsonb, $8, $9)`,
		id, tenant, account, name, draftDescription, invocable, beeVal, liveVal, aiUser)
	require.NoError(t, err)
}

// The list includes what is or could be offered — opted in, or described for
// the AI in the draft — and never an agent's automation or another tenant's.
func TestListAIToolCandidatesFiltersAndScopes(t *testing.T) {
	db := aiSettingsTestDB(t)
	_, err := db.Exec(`INSERT INTO users VALUES ($1, 'Pat', 'pat')`, aiUser)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO workflow_versions VALUES ($1, '{"llm_description":"Restarts.","triggers":[{"type":"manual"}]}')`, aiLiveDefRef)
	require.NoError(t, err)

	insertAIWorkflow(t, db, "a0000000-0000-0000-0000-000000000001", aiTenant, aiAccount, "opted-in", true, "", "", true)
	insertAIWorkflow(t, db, "a0000000-0000-0000-0000-000000000002", aiTenant, aiAccount, "described-only", false, "Collects logs.", "", false)
	insertAIWorkflow(t, db, "a0000000-0000-0000-0000-000000000003", aiTenant, aiAccount, "blank-description", false, "  ", "", false)
	insertAIWorkflow(t, db, "a0000000-0000-0000-0000-000000000004", aiTenant, aiAccount, "agent-owned", true, "Agent.", `{"role":"trigger"}`, true)
	insertAIWorkflow(t, db, "a0000000-0000-0000-0000-000000000005", aiOtherTen, aiAccount, "other-tenant", true, "Other.", "", true)
	insertAIWorkflow(t, db, "a0000000-0000-0000-0000-000000000006", aiTenant, aiOtherAcct, "other-account", true, "Other.", "", true)

	dao := &WorkflowDao{db: db}
	got, err := dao.ListAIToolCandidates(context.Background(), aiTenant, []string{aiAccount})
	require.NoError(t, err)

	names := []string{}
	for _, c := range got {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"described-only", "opted-in"}, names)
	require.NotNil(t, got[1].Live, "a published automation carries its live definition")
	assert.Equal(t, "Restarts.", got[1].Live.LLMDescription)
	assert.Nil(t, got[0].Live, "an unpublished one has none")
	require.NotNil(t, got[1].CreatedBy)
	assert.Equal(t, "Pat", got[1].CreatedBy.DisplayName)
}

// The update changes only the two flags, and only on the caller's own tenant
// and account.
func TestUpdateAISettingsIsScoped(t *testing.T) {
	db := aiSettingsTestDB(t)
	const id = "b0000000-0000-0000-0000-000000000001"
	insertAIWorkflow(t, db, id, aiTenant, aiAccount, "restart", true, "Restarts.", "", false)
	dao := &WorkflowDao{db: db}

	err := dao.UpdateAISettings(context.Background(), aiTenant, aiOtherAcct, id, aiUser, false, false)
	assert.ErrorIs(t, err, sql.ErrNoRows, "another account's id matches nothing")
	err = dao.UpdateAISettings(context.Background(), aiOtherTen, aiAccount, id, aiUser, false, false)
	assert.ErrorIs(t, err, sql.ErrNoRows, "another tenant's id matches nothing")

	require.NoError(t, dao.UpdateAISettings(context.Background(), aiTenant, aiAccount, id, aiUser, true, true))
	var row struct {
		Invocable bool   `db:"ai_invocable"`
		Auto      bool   `db:"ai_auto_approve"`
		UpdatedBy string `db:"updated_by"`
		Name      string `db:"name"`
	}
	require.NoError(t, db.Get(&row, `SELECT ai_invocable, ai_auto_approve, updated_by::text, name FROM workflows WHERE id = $1`, id))
	assert.True(t, row.Invocable)
	assert.True(t, row.Auto)
	assert.Equal(t, aiUser, row.UpdatedBy)
	assert.Equal(t, "restart", row.Name)
}
