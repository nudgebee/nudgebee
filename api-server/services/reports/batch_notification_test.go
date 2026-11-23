package reports

import (
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Consecutive runs a few minutes past each hour must cover each hour once:
// no overlap, no gap.
func TestHourlyBatchWindow(t *testing.T) {
	run := time.Date(2026, 10, 16, 10, 5, 3, 0, time.UTC)
	start, end := hourlyBatchWindow(run)
	assert.Equal(t, time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), start)
	assert.Equal(t, time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC), end)

	nextStart, _ := hourlyBatchWindow(run.Add(time.Hour + 40*time.Second))
	assert.Equal(t, end, nextStart)

	ist := time.FixedZone("UTC+5:30", 5*3600+1800)
	start, end = hourlyBatchWindow(run.In(ist))
	assert.Equal(t, time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), start)
	assert.Equal(t, time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC), end)
}

// Runs the rule scope query against Postgres: a rule for all accounts covers
// each active account of its own tenant, and every rule covering an account
// keeps its severity.
func TestQueryBatchRuleAccounts_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	db, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	const schema = "zz_batch_rule_accounts_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, tenant uuid, account_name text, status text)`)
	mustExec(`CREATE TABLE notification_rules (id uuid PRIMARY KEY, tenant_id uuid, severity text,
		delivery_mode text, scope_mode text NOT NULL, is_active bool, is_suppressed bool)`)
	mustExec(`CREATE TABLE notification_rule_accounts (rule_id uuid, account_id uuid, tenant_id uuid)`)

	const (
		tenantA   = "00000000-0000-0000-0000-0000000000f1"
		tenantB   = "00000000-0000-0000-0000-0000000000f2"
		prod      = "00000000-0000-0000-0000-0000000000a1"
		dev       = "00000000-0000-0000-0000-0000000000a2"
		disabled  = "00000000-0000-0000-0000-0000000000a3"
		otherProd = "00000000-0000-0000-0000-0000000000b1"
	)
	mustExec(`INSERT INTO cloud_accounts VALUES
		($1, $5, 'prod', 'active'), ($2, $5, 'dev', 'active'), ($3, $5, 'old', 'disabled'), ($4, $6, 'other', 'active')`,
		prod, dev, disabled, otherProd, tenantA, tenantB)

	rule := func(id, tenant, severity, delivery, scope string, active, suppressed bool, accounts ...string) {
		mustExec(`INSERT INTO notification_rules VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, tenant, severity, delivery, scope, active, suppressed)
		for _, acc := range accounts {
			mustExec(`INSERT INTO notification_rule_accounts VALUES ($1, $2, $3)`, id, acc, tenant)
		}
	}
	// Tenant A: an all-accounts High rule and an Any rule on prod alone.
	rule("00000000-0000-0000-0000-000000000001", tenantA, "high", "batch", "all", true, false)
	rule("00000000-0000-0000-0000-000000000002", tenantA, "", "batch", "accounts", true, false, prod)
	// Rules that must not reach the batch: real-time, inactive, suppressed.
	rule("00000000-0000-0000-0000-000000000003", tenantA, "", "", "all", true, false)
	rule("00000000-0000-0000-0000-000000000004", tenantA, "medium", "batch", "all", false, false)
	rule("00000000-0000-0000-0000-000000000005", tenantA, "low", "batch", "accounts", true, true, dev)

	got, err := queryBatchRuleAccounts(db)
	require.NoError(t, err)

	require.ElementsMatch(t, []string{prod, dev}, lo.Keys(got),
		"the all-accounts rule covers tenant A's active accounts, not its disabled one or tenant B's")
	assert.Equal(t, tenantA, got[prod].TenantID)
	assert.Equal(t, "prod", got[prod].AccountName)
	assert.ElementsMatch(t, []string{"high", ""}, got[prod].Severities, "both rules on prod keep their severity")
	assert.Equal(t, []string{"high"}, got[dev].Severities)
}
