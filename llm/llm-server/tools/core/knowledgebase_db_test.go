package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// These tests run the knowledge-base queries against a real, fully migrated
// Postgres. Each test seeds its own tenant and removes it afterwards, so they
// can share a database with other data. CI runs them in migrations-validate
// against the database it has just migrated:
//
//	KB_TEST_DATABASE_URL=postgres://... REQUIRE_DB_TESTS=true go test ./tools/core/ -run '_DB$' -count=1
//
// KB_TEST_DATABASE_URL, not APP_DATABASE_URL: the latter often points at a live
// database, and these tests write to the one they are given.

const kbMigrationsDir = "../../../../api-server/migrations/migrations/app"

func kbTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("KB_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but KB_TEST_DATABASE_URL is not")
		}
		t.Skip("set KB_TEST_DATABASE_URL to a migrated database to run the knowledge-base SQL tests")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() {
		common.RegisterDatabaseManagerHook(common.Metastore, nil)
		_ = db.Close()
	})
	return db
}

type kbExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// kbTenant is one seeded tenant: accounts A and B, plus account C in a second
// tenant that must never see anything of the first.
type kbTenant struct {
	user, tenant, otherTenant string
	a, b, c                   string
}

func newKBTenant() kbTenant {
	return kbTenant{
		user: uuid.NewString(), tenant: uuid.NewString(), otherTenant: uuid.NewString(),
		a: uuid.NewString(), b: uuid.NewString(), c: uuid.NewString(),
	}
}

// seedKBTenant inserts a tenant in tx, which the caller rolls back.
func seedKBTenant(t *testing.T, db kbExecer) kbTenant {
	t.Helper()
	s := newKBTenant()
	insertKBTenant(t, db, s)
	return s
}

// seedKBTenantCommitted inserts a tenant that stays until the test ends. The
// cleanup is registered first, so a seed that fails halfway leaves nothing.
func seedKBTenantCommitted(t *testing.T, db *sqlx.DB) kbTenant {
	t.Helper()
	s := newKBTenant()
	cleanupKBTenant(t, db, s)
	insertKBTenant(t, db, s)
	return s
}

func insertKBTenant(t *testing.T, db kbExecer, s kbTenant) {
	t.Helper()
	mustExec(t, db, `INSERT INTO users (id, username, display_name) VALUES ($1, $2, 'KB test user')`, s.user, "kb-test-"+s.user)
	for _, tenant := range []string{s.tenant, s.otherTenant} {
		mustExec(t, db, `INSERT INTO tenant (id, name, created_by, updated_by) VALUES ($1, $2, $3, $3)`, tenant, "kb-test-"+tenant, s.user)
	}
	for id, tenant := range map[string]string{s.a: s.tenant, s.b: s.tenant, s.c: s.otherTenant} {
		mustExec(t, db, `INSERT INTO cloud_accounts (id, account_name, account_type, tenant, created_by, updated_by)
			VALUES ($1, $2, 'K8s', $3, $4, $4)`, id, "kb-test-"+id, tenant, s.user)
	}
}

// cleanupKBTenant removes everything seedKBTenant and the tests created. Rows
// referencing the knowledge bases cascade with them.
func cleanupKBTenant(t *testing.T, db *sqlx.DB, s kbTenant) {
	t.Cleanup(func() {
		tenants := []any{s.tenant, s.otherTenant}
		for _, q := range []string{
			`DELETE FROM llm_kb_agent_mappings WHERE account_id IN (SELECT id FROM cloud_accounts WHERE tenant IN ($1, $2))`,
			`DELETE FROM llm_knowledgebases WHERE tenant_id IN ($1, $2)`,
			`DELETE FROM integrations_cloud_accounts WHERE tenant_id IN ($1, $2)`,
			`DELETE FROM integration_config_values WHERE integration_id IN (SELECT id FROM integrations WHERE tenant_id IN ($1, $2))`,
			`DELETE FROM integrations WHERE tenant_id IN ($1, $2)`,
			`DELETE FROM cloud_accounts WHERE tenant IN ($1, $2)`,
			`DELETE FROM tenant WHERE id IN ($1, $2)`,
		} {
			if _, err := db.Exec(q, tenants...); err != nil {
				t.Errorf("cleanup %q: %v", q, err)
			}
		}
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, s.user); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
}

func mustExec(t *testing.T, db kbExecer, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	require.NoError(t, err, query)
}

// seedIntegration creates a Confluence integration of tenant, linked to the
// given accounts.
func seedIntegration(t *testing.T, db kbExecer, tenant, status string, accounts ...string) string {
	t.Helper()
	id := uuid.NewString()
	mustExec(t, db, `INSERT INTO integrations (id, tenant_id, type, source, name, status)
		VALUES ($1, $2, 'confluence', 'user', $3, $4)`, id, tenant, "Confluence "+id[:8], status)
	for _, account := range accounts {
		mustExec(t, db, `INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id)
			VALUES ($1, $2, $3)`, id, account, tenant)
	}
	return id
}

type kbSeedRow struct {
	account     string // "" = organisation KB
	integration string // "" = manual KB
	name        string
	enabled     bool
	visibility  string
	status      string
}

func seedKB(t *testing.T, db kbExecer, tenant string, r kbSeedRow) string {
	t.Helper()
	id := uuid.NewString()
	kbType, source := "manual", sql.NullString{}
	if r.integration != "" {
		kbType, source = "integration", sql.NullString{String: "confluence", Valid: true}
	}
	if r.status == "" {
		r.status = "active"
	}
	mustExec(t, db, `INSERT INTO llm_knowledgebases
		(id, tenant_id, account_id, name, data, data_format, data_filename, status, kb_type, kb_source, integration_id, enabled, visibility)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4, '', 'text', 'kb.txt', $5, $6, $7, NULLIF($8, '')::uuid, $9, $10)`,
		id, tenant, r.account, r.name, r.status, kbType, source, r.integration, r.enabled, r.visibility)
	return id
}

func seedOverride(t *testing.T, db kbExecer, tenant, kb, account string, enabled bool) {
	t.Helper()
	mustExec(t, db, `INSERT INTO llm_kb_account_overrides (kb_id, account_id, tenant_id, enabled) VALUES ($1, $2, $3, $4)`,
		kb, account, tenant, enabled)
}

func seedMapping(t *testing.T, db kbExecer, kb, agent, account string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO llm_kb_agent_mappings (kb_id, agent_id, account_id) VALUES ($1, $2, $3)`, kb, agent, account)
}

func seedMark(t *testing.T, db kbExecer, tenant, account, kb, documentKey, category string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO llm_kb_capability_entries (entry_type, tenant_id, account_id, kb_id, document_key, note_category)
		VALUES ('kb_document_mark', $1, NULLIF($2, '')::uuid, $3, $4, $5)`, tenant, account, kb, documentKey, category)
}

// The shared visibility cases, run through the real visibility query instead
// of hand-built rows: the SQL decides which rows reach the rule, so a query
// change that loads the wrong rows fails here.
func TestKBVisibilitySharedCases_DB(t *testing.T) {
	db := kbTestDB(t)
	raw, err := os.ReadFile(kbVisibilityFixture)
	require.NoError(t, err)
	var fixture kbVisibilityFixtureFile
	require.NoError(t, json.Unmarshal(raw, &fixture))

	s := seedKBTenantCommitted(t, db)
	accounts := map[string]string{"A": s.a, "B": s.b}
	integrations := map[string]string{}

	ran := 0
	for _, c := range fixture.Cases {
		if c.Account == nil {
			continue // tenant-only scope has no account to query with
		}
		ran++
		t.Run(c.Name, func(t *testing.T) {
			account := accounts[*c.Account]
			names := map[string]string{} // seeded collection -> fixture collection
			for _, r := range c.Rows {
				row := kbSeedRow{enabled: r.Enabled, visibility: r.Visibility}
				if r.AccountId != nil {
					row.account = accounts[*r.AccountId]
				}
				if r.Name != nil {
					row.name = *r.Name
				}
				if r.IntegrationId != nil {
					if integrations[*r.IntegrationId] == "" {
						integrations[*r.IntegrationId] = seedIntegration(t, db, s.tenant, "enabled", s.a)
					}
					row.integration = integrations[*r.IntegrationId]
				}
				id := seedKB(t, db, s.tenant, row)
				if r.OverrideEnabled != nil {
					seedOverride(t, db, s.tenant, id, account, *r.OverrideEnabled)
				}
				seeded := kbVisibilityRow{Id: id}
				if row.integration != "" {
					seeded.IntegrationId = &row.integration
				}
				names[seeded.collectionName()] = r.collectionName()
			}
			t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM llm_knowledgebases WHERE tenant_id = $1`, s.tenant) })

			got := []string{}
			for name := range visibleKBCollections(loadOrgKBVisibilityRows(context.Background(), account), account) {
				got = append(got, names[name])
			}
			sort.Strings(got)
			want := append([]string{}, c.Expected...)
			sort.Strings(want)
			assert.Equal(t, want, got)

			// The other tenant's account sees none of it.
			assert.Empty(t, loadVisibleOrgKnowledgebaseIDs(context.Background(), s.c))
		})
	}
	require.NotZero(t, ran)
}

// The KB list and the agent-mapping join: organisation KBs reach every agent of
// the accounts that see them, an integration's organisation KB only through the
// account's own mappings, and a switched-off KB stays listed (read-only) but
// reaches no agent.
func TestKBListAndAgentMappings_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	integration := seedIntegration(t, db, s.tenant, "enabled", s.a, s.b)

	org := seedKB(t, db, s.tenant, kbSeedRow{name: "DB failover", enabled: true, visibility: "organisation"})
	selected := seedKB(t, db, s.tenant, kbSeedRow{name: "Payments", enabled: true, visibility: "selected_accounts"})
	integ := seedKB(t, db, s.tenant, kbSeedRow{integration: integration, name: "Confluence", enabled: true, visibility: "organisation"})
	ownA := seedKB(t, db, s.tenant, kbSeedRow{account: s.a, name: "Runbook", enabled: true, visibility: "account"})
	ownB := seedKB(t, db, s.tenant, kbSeedRow{account: s.b, name: "Runbook", enabled: true, visibility: "account"})
	legacyA := seedKB(t, db, s.tenant, kbSeedRow{account: s.a, integration: integration, name: "Confluence", enabled: true, visibility: "account"})
	seedOverride(t, db, s.tenant, selected, s.a, true)
	seedOverride(t, db, s.tenant, org, s.b, false)
	seedMapping(t, db, ownA, "agent-x", s.a)
	seedMapping(t, db, integ, "agent-x", s.a)
	seedMapping(t, db, integ, "agent-y", s.b)

	sc := security.NewRequestContextForSuperAdmin()
	list := func(account string) map[string]bool {
		kbs, err := ListKnowledgebases(sc, account)
		require.NoError(t, err)
		out := map[string]bool{}
		for _, kb := range kbs {
			out[kb.Id] = kb.SwitchedOff
		}
		return out
	}
	// The agent page lists explicit mappings only ("*" is how it shows the
	// all-agents ones); skill loading adds the wildcard to the agent's name.
	agentKBs := func(account, agent string) []string {
		kbs, err := ListAgentKBs(sc, account, agent)
		require.NoError(t, err)
		var ids []string
		for _, kb := range kbs {
			ids = append(ids, kb.Id)
		}
		sort.Strings(ids)
		return ids
	}
	skills := func(account, agent string) []string {
		candidates, err := ListActiveAgentSkillCandidates(sc, account, WithKBAgentWildcard([]string{agent}))
		require.NoError(t, err)
		var ids []string
		for _, c := range candidates {
			ids = append(ids, c.ID)
		}
		sort.Strings(ids)
		return ids
	}
	sorted := func(ids ...string) []string { sort.Strings(ids); return ids }

	assert.Equal(t, map[string]bool{org: false, selected: false, integ: false, ownA: false}, list(s.a),
		"A lists its own KB and every organisation KB it sees, never the per-account integration row")
	assert.Equal(t, map[string]bool{org: true, integ: false, ownB: false}, list(s.b),
		"B lists the KB it switched off as switched off, and not the selected-accounts KB")
	assert.Empty(t, list(s.c), "another tenant's account lists nothing")
	assert.NotContains(t, list(s.a), legacyA)

	assert.Equal(t, sorted(ownA, integ), agentKBs(s.a, "agent-x"))
	assert.Equal(t, sorted(org, selected), agentKBs(s.a, KBAgentWildcard),
		"manual organisation KBs A sees are mapped to all of its agents")
	assert.Equal(t, []string{integ}, agentKBs(s.b, "agent-y"))
	assert.Empty(t, agentKBs(s.b, KBAgentWildcard), "B switched the organisation KB off")

	assert.Equal(t, sorted(ownA, integ, org, selected), skills(s.a, "agent-x"))
	assert.Equal(t, sorted(org, selected), skills(s.a, "agent-y"),
		"B's mapping of the integration KB does not reach A's agents")
	assert.Equal(t, []string{integ}, skills(s.b, "agent-y"),
		"a switched-off organisation KB reaches none of B's agents")
}

// Sync reconcile archives the organisation row of an integration that is no
// longer eligible, reactivates one that is again, refreshes a renamed
// integration's KB name, and leaves per-account rows and other tenants alone.
func TestKBSyncReconcile_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	ctx := context.Background()

	live := seedIntegration(t, db, s.tenant, "enabled", s.a)
	disabled := seedIntegration(t, db, s.tenant, "disabled", s.a)
	returning := seedIntegration(t, db, s.tenant, "enabled", s.b)
	otherTenant := seedIntegration(t, db, s.otherTenant, "disabled", s.c)
	liveKB := seedKB(t, db, s.tenant, kbSeedRow{integration: live, name: "Old name", enabled: true, visibility: "organisation"})
	disabledKB := seedKB(t, db, s.tenant, kbSeedRow{integration: disabled, name: "Disabled", enabled: true, visibility: "organisation"})
	returningKB := seedKB(t, db, s.tenant, kbSeedRow{integration: returning, name: "Returning", enabled: true, visibility: "organisation", status: "archived"})
	legacyKB := seedKB(t, db, s.tenant, kbSeedRow{account: s.a, integration: disabled, name: "Disabled", enabled: true, visibility: "account"})
	otherKB := seedKB(t, db, s.otherTenant, kbSeedRow{integration: otherTenant, name: "Other", enabled: true, visibility: "organisation"})

	var eligible []integrationKBRow
	require.NoError(t, db.SelectContext(ctx, &eligible, eligibleIntegrationsQuery, s.tenant))
	ids := integrationIDs(eligible)
	assert.ElementsMatch(t, []string{live, returning}, ids)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	archived, reactivated := reconcileIntegrationKBs(ctx, dbms, ids, s.tenant)
	assert.Equal(t, 1, archived)
	assert.Equal(t, 1, reactivated)

	status := func(id string) string {
		var st string
		require.NoError(t, db.Get(&st, `SELECT status FROM llm_knowledgebases WHERE id = $1`, id))
		return st
	}
	assert.Equal(t, "active", status(liveKB))
	assert.Equal(t, "archived", status(disabledKB))
	assert.Equal(t, "active", status(returningKB))
	assert.Equal(t, "active", status(legacyKB), "per-account rows are never reconciled")
	assert.Equal(t, "active", status(otherKB), "a tenant-scoped reconcile leaves other tenants alone")

	for _, row := range eligible {
		if row.IntegrationID == live {
			assert.Equal(t, kbEnsureSkipped, ensureKBForIntegration(ctx, dbms, row))
			var name string
			require.NoError(t, db.Get(&name, `SELECT name FROM llm_knowledgebases WHERE id = $1`, liveKB))
			assert.Equal(t, row.IntegrationName, name, "a renamed integration renames its KB")
		}
	}
}

// Marks resolve account first, then tenant; a tenant mark set from the
// organisation KB replaces every account's own mark on that document.
func TestKBDocumentMarks_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	ctx := context.Background()
	integration := seedIntegration(t, db, s.tenant, "enabled", s.a, s.b)
	kb := seedKB(t, db, s.tenant, kbSeedRow{integration: integration, name: "Confluence", enabled: true, visibility: "organisation"})
	collection := integration + "_knowledge_base"
	doc1, doc2 := collection+"|page-1", collection+"|page-2"
	seedMark(t, db, s.tenant, "", kb, doc1, "sop")
	seedMark(t, db, s.tenant, s.a, kb, doc1, "fact")
	seedMark(t, db, s.tenant, "", kb, doc2, "fact")

	categories := func(account string) map[string]string {
		got, err := kbDocumentCategories(ctx, account, []string{doc1, doc2})
		require.NoError(t, err)
		return got
	}
	assert.Equal(t, map[string]string{doc1: "fact", doc2: "fact"}, categories(s.a), "A's own mark wins")
	assert.Equal(t, map[string]string{doc1: "sop", doc2: "fact"}, categories(s.b), "B gets the tenant marks")
	assert.Empty(t, categories(s.c), "another tenant's account gets none")

	counts, err := kbDocumentAccountMarkCounts(ctx, s.tenant, []string{doc1, doc2})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{doc1: 1}, counts)

	require.NoError(t, SetKBDocumentCategory(security.NewRequestContextForSuperAdmin(), s.a, kb, doc1, "sop"))
	assert.Equal(t, map[string]string{doc1: "sop", doc2: "fact"}, categories(s.a),
		"setting the mark on the organisation KB replaces A's own mark")
	counts, err = kbDocumentAccountMarkCounts(ctx, s.tenant, []string{doc1})
	require.NoError(t, err)
	assert.Empty(t, counts)
}

// runMigrationFile executes one migration's up.sql in tx, as atlas does.
func runMigrationFile(t *testing.T, tx *sqlx.Tx, version string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(kbMigrationsDir, "*_"+version+"_*.up.sql"))
	require.NoError(t, err)
	require.Len(t, files, 1, "migration %s", version)
	body, err := os.ReadFile(files[0])
	require.NoError(t, err)
	mustExec(t, tx, string(body))
}

// V1013 replayed on the old shape: per-account integration rows fold into one
// organisation row per integration, keeping each account's switch, mappings and
// marks. Runs in a transaction that is rolled back, so it leaves nothing behind.
// The migration has no tenant filter: while it runs it holds ACCESS EXCLUSIVE
// locks on llm_knowledgebases and llm_kb_agent_mappings, so point it at a
// database no live llm-server is using.
func TestKBMigrationV1013FoldsIntegrationRows_DB(t *testing.T) {
	db := kbTestDB(t)
	tx, err := db.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	s := seedKBTenant(t, tx)
	integration := seedIntegration(t, tx, s.tenant, "enabled", s.a, s.b)
	rowA := seedKB(t, tx, s.tenant, kbSeedRow{account: s.a, integration: integration, name: "Confluence", enabled: true, visibility: "account"})
	rowB := seedKB(t, tx, s.tenant, kbSeedRow{account: s.b, integration: integration, name: "Confluence", enabled: false, visibility: "account"})
	mustExec(t, tx, `UPDATE llm_knowledgebases SET last_loaded_at = NOW() WHERE id = $1`, rowA)
	seedMapping(t, tx, rowA, "agent-x", s.a)
	seedMapping(t, tx, rowB, "agent-x", s.b)
	collection := integration + "_knowledge_base"
	agreed, disputed := collection+"|agreed", collection+"|disputed"
	seedMark(t, tx, s.tenant, s.a, rowA, agreed, "sop")
	seedMark(t, tx, s.tenant, s.b, rowB, agreed, "sop")
	seedMark(t, tx, s.tenant, s.a, rowA, disputed, "sop")
	seedMark(t, tx, s.tenant, s.b, rowB, disputed, "fact")

	runMigrationFile(t, tx, "V1013")

	var org struct {
		Id         string `db:"id"`
		Enabled    bool   `db:"enabled"`
		Visibility string `db:"visibility"`
	}
	require.NoError(t, tx.Get(&org, `SELECT id, enabled, visibility FROM llm_knowledgebases
		WHERE integration_id = $1 AND account_id IS NULL`, integration))
	assert.True(t, org.Enabled, "on if any account had it on")
	assert.Equal(t, "organisation", org.Visibility)

	var perAccount int
	require.NoError(t, tx.Get(&perAccount, `SELECT count(*) FROM llm_knowledgebases WHERE integration_id = $1 AND account_id IS NOT NULL`, integration))
	assert.Zero(t, perAccount)

	var overrides []string
	require.NoError(t, tx.Select(&overrides, `SELECT account_id::text || ':' || enabled::text FROM llm_kb_account_overrides WHERE kb_id = $1`, org.Id))
	assert.Equal(t, []string{s.b + ":false"}, overrides, "B had its copy off and stays switched off")

	var mappings []string
	require.NoError(t, tx.Select(&mappings, `SELECT account_id::text FROM llm_kb_agent_mappings WHERE kb_id = $1 AND agent_id = 'agent-x' ORDER BY 1`, org.Id))
	assert.ElementsMatch(t, []string{s.a, s.b}, mappings)

	var marks []string
	require.NoError(t, tx.Select(&marks, `SELECT document_key || ':' || COALESCE(account_id::text, 'tenant') || ':' || note_category
		FROM llm_kb_capability_entries WHERE entry_type = 'kb_document_mark' AND kb_id = $1`, org.Id))
	assert.ElementsMatch(t, []string{
		agreed + ":tenant:sop",
		disputed + ":" + s.a + ":sop",
		disputed + ":" + s.b + ":fact",
	}, marks, "a mark every account agreed on becomes one tenant mark; the rest stay per account")
}

// V1014 deletes per-account integration rows an older llm-server re-created
// after V1013, and nothing else.
func TestKBMigrationV1014DeletesLeftoverRows_DB(t *testing.T) {
	db := kbTestDB(t)
	tx, err := db.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	s := seedKBTenant(t, tx)
	integration := seedIntegration(t, tx, s.tenant, "enabled", s.a)
	org := seedKB(t, tx, s.tenant, kbSeedRow{integration: integration, name: "Confluence", enabled: true, visibility: "organisation"})
	leftover := seedKB(t, tx, s.tenant, kbSeedRow{account: s.a, integration: integration, name: "Confluence", enabled: true, visibility: "account"})
	manual := seedKB(t, tx, s.tenant, kbSeedRow{account: s.a, name: "Runbook", enabled: true, visibility: "account"})

	runMigrationFile(t, tx, "V1014")

	var remaining []string
	require.NoError(t, tx.Select(&remaining, `SELECT id::text FROM llm_knowledgebases WHERE id = ANY($1::uuid[])`,
		"{"+strings.Join([]string{org, leftover, manual}, ",")+"}"))
	assert.ElementsMatch(t, []string{org, manual}, remaining)
}

// "/skill-name" resolves a user-defined text skill the account can see, by its
// name or with spaces typed as hyphens; the account's own skill wins over an
// organisation one, and switched-off, disabled, PDF, integration and other
// tenants' skills are never found.
func TestGetSkillsForCommand_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	integration := seedIntegration(t, db, s.tenant, "enabled", s.a)
	setData := func(id, data, format string) {
		mustExec(t, db, `UPDATE llm_knowledgebases SET data = $2, data_format = $3 WHERE id = $1`, id, data, format)
	}

	org := seedKB(t, db, s.tenant, kbSeedRow{name: "DB failover", enabled: true, visibility: "organisation"})
	setData(org, "org steps", "text")
	ownA := seedKB(t, db, s.tenant, kbSeedRow{account: s.a, name: "db failover", enabled: true, visibility: "account"})
	setData(ownA, "A's steps", "text")
	off := seedKB(t, db, s.tenant, kbSeedRow{account: s.a, name: "Old runbook", enabled: false, visibility: "account"})
	setData(off, "old", "text")
	pdf := seedKB(t, db, s.tenant, kbSeedRow{account: s.a, name: "Manual", enabled: true, visibility: "account"})
	setData(pdf, "JVBERi0=", "pdf")
	seedKB(t, db, s.tenant, kbSeedRow{integration: integration, name: "Confluence", enabled: true, visibility: "organisation"})
	seedOverride(t, db, s.tenant, org, s.b, false)

	sc := security.NewRequestContextForSuperAdmin()
	find := func(account, name string) string {
		skills, err := GetSkillsForCommand(sc, account, []string{name})
		require.NoError(t, err)
		return skills[strings.ToLower(name)].Data
	}

	assert.Equal(t, "A's steps", find(s.a, "db-failover"), "the account's own skill wins")
	assert.Equal(t, "A's steps", find(s.a, "DB FAILOVER"))
	assert.Empty(t, find(s.b, "db-failover"), "B switched the organisation skill off")
	assert.Empty(t, find(s.c, "db-failover"), "another tenant sees nothing")
	assert.Empty(t, find(s.a, "old-runbook"), "disabled")
	assert.Empty(t, find(s.a, "manual"), "PDF")
	assert.Empty(t, find(s.a, "confluence"), "integration knowledge base")
	assert.Empty(t, find(s.a, "var/log/syslog"))

	both, err := GetSkillsForCommand(sc, s.a, []string{"DB-Failover", "nope", "db failover"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"db-failover": "A's steps", "db failover": "A's steps"},
		map[string]string{"db-failover": both["db-failover"].Data, "db failover": both["db failover"].Data})
	assert.Len(t, both, 2, "one entry per matching name, keyed lower-case")
}

// V1037 copies each saved function into its account as a text skill, skipping
// names the account already uses and functions with no account.
func TestKBMigrationV1037FunctionsToSkills_DB(t *testing.T) {
	db := kbTestDB(t)
	tx, err := db.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	s := seedKBTenant(t, tx)
	seedKB(t, tx, s.tenant, kbSeedRow{account: s.a, name: "Pod triage", enabled: true, visibility: "account"})
	fn := func(account any, name, prompt, variables string) {
		mustExec(t, tx, `INSERT INTO llm_functions (tenant_id, account_id, name, description, prompt, variables)
			VALUES ($1, $2, $3, 'desc', $4, NULLIF($5, '')::jsonb)`, s.tenant, account, name, prompt, variables)
	}
	fn(s.a, "pod_triage", "check pods", "")
	fn(s.a, "deploy_check", "check deploys", `["ns"]`)
	fn(s.a, "POD TRIAGE", "clashes with the skill", "")
	fn(nil, "tenant_wide", "no account", "")

	runMigrationFile(t, tx, "V1037")

	var got []string
	require.NoError(t, tx.Select(&got, `SELECT name || '=' || data || ':' || kb_type || ':' || status || ':' || enabled::text
		FROM llm_knowledgebases WHERE tenant_id = $1 AND account_id = $2 AND name <> 'Pod triage' ORDER BY name`, s.tenant, s.a))
	assert.Equal(t, []string{
		"deploy_check=check deploys\n\nInputs: [\"ns\"]:manual:active:true",
		"pod_triage=check pods:manual:active:true",
	}, got)

	var orgRows int
	require.NoError(t, tx.Get(&orgRows, `SELECT count(*) FROM llm_knowledgebases WHERE tenant_id = $1 AND account_id IS NULL`, s.tenant))
	assert.Zero(t, orgRows, "a function with no account is not widened to the organisation")

	runMigrationFile(t, tx, "V1037")
	var again int
	require.NoError(t, tx.Get(&again, `SELECT count(*) FROM llm_knowledgebases WHERE tenant_id = $1`, s.tenant))
	assert.Equal(t, 3, again, "re-running adds nothing")
}
