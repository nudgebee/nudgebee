package recommendation

import (
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProactiveAccountSectionsSumEachAccount(t *testing.T) {
	recs := []proactiveRec{
		{ID: "r1", CloudAccountID: "a", AccountName: "prod", EstimatedSavings: 10},
		{ID: "r2", CloudAccountID: "a", AccountName: "prod", EstimatedSavings: 5},
		{ID: "r3", CloudAccountID: "b", AccountName: "dev", EstimatedSavings: 1},
	}
	sections := buildProactiveAccountSections(recs, map[string]string{})
	totals := sections["a"].(map[string]any)["totals"].(map[string]any)
	assert.Equal(t, 2, totals["total_recommendations"])
	assert.Equal(t, 15.0, totals["total_recoverable_savings"])
	assert.Equal(t, []string{"r1", "r2", "r3"}, proactiveRecIDs(recs, recs[:1]))
}

// Each account gets its own proactive nudge list, capped per account.
func TestProactiveAccountQueries_DB(t *testing.T) {
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

	const schema = "zz_proactive_accounts_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, account_name text)`)
	mustExec(`CREATE TABLE recommendation (
		id uuid PRIMARY KEY, tenant_id uuid, rule_name text, account_object_id text, finops_score int,
		finops_band text, estimated_savings double precision, severity text, category text,
		cloud_account_id uuid, created_at timestamp, status text, last_nudged_at timestamp, recommendation jsonb)`)
	mustExec(`CREATE TABLE agent (cloud_account_id uuid, type text, connection_status jsonb)`)
	mustExec(`CREATE TABLE cloud_account_attrs (cloud_account_id uuid, name text, value text)`)

	const tenant = "00000000-0000-0000-0000-0000000000f1"
	const prod = "00000000-0000-0000-0000-0000000000a1"
	const dev = "00000000-0000-0000-0000-0000000000a2"
	mustExec(`INSERT INTO cloud_accounts VALUES ($1, 'k8s-prod'), ($2, 'k8s-dev')`, prod, dev)
	insert := func(i int, account, rule string, score int) {
		mustExec(`INSERT INTO recommendation (id, tenant_id, rule_name, finops_score, finops_band, estimated_savings,
			category, cloud_account_id, created_at, status) VALUES ($1, $2, $3, $4, 'Act Now', 1, 'RightSizing', $5, now(), 'Open')`,
			fmt.Sprintf("00000000-0000-0000-0000-%012d", i), tenant, rule, score, account)
	}
	for i := 0; i < 15; i++ {
		insert(i, prod, fmt.Sprintf("rule_%d", i%6), 900-i)
	}
	insert(100, dev, "rule_x", 5)
	// k8s-dev lists one more namespace as NudgeBee's own; its recommendation is
	// left out.
	mustExec(`INSERT INTO cloud_account_attrs VALUES ($1, 'nudgebee_workloads', '{"namespaces": ["nudgebee"]}')`, dev)
	insert(101, dev, "rule_y", 999)
	mustExec(`UPDATE recommendation SET recommendation = '{"namespace": "nudgebee"}' WHERE id = '00000000-0000-0000-0000-000000000101'`)

	recs, err := queryProactiveAccountRecommendations(db, tenant)
	require.NoError(t, err)
	perAccount := map[string]int{}
	for _, r := range recs {
		perAccount[r.CloudAccountID]++
	}
	assert.Equal(t, proactiveAccountLimit, perAccount[prod])
	assert.Equal(t, 1, perAccount[dev])
}
