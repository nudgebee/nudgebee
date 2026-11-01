package reports

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDigestAccountSectionsSumEachAccount(t *testing.T) {
	recs := []digestRecommendation{
		{ID: "r1", CloudAccountID: "a", AccountName: "prod", FinOpsBand: "Act Now", EstimatedSavings: 10},
		{ID: "r2", CloudAccountID: "a", AccountName: "prod", FinOpsBand: "High", EstimatedSavings: 5},
		{ID: "r3", CloudAccountID: "b", AccountName: "dev", FinOpsBand: "Critical", EstimatedSavings: 1},
	}
	deltas := map[string]*digestDeltas{"a": newDigestDeltas()}
	deltas["a"].CarryoverCount = 7

	sections := buildDigestAccountSections(recs, deltas, map[string]string{"a": "k8s-prod"}, "2026-09-26")

	a := sections["a"].(map[string]any)
	assert.Equal(t, "k8s-prod", a["account_name"])
	assert.Len(t, a["recommendations"], 2)
	totals := a["totals"].(map[string]any)
	assert.Equal(t, 15.0, totals["total_recoverable_savings"])
	assert.Equal(t, 1, totals["act_now_count"])
	assert.Equal(t, 1, totals["high_count"])
	assert.Equal(t, 7, totals["carryover_count"])

	b := sections["b"].(map[string]any)
	assert.Equal(t, "dev", b["account_name"])
	assert.Equal(t, 0, b["totals"].(map[string]any)["carryover_count"])
}

func TestAccountSectionsKeepAccountsWithOnlyHeaderNumbers(t *testing.T) {
	deltas := map[string]*digestDeltas{"quiet": newDigestDeltas()}
	deltas["quiet"].CarryoverCount = 40
	digest := buildDigestAccountSections(nil, deltas, map[string]string{"quiet": "k8s-quiet"}, "2026-09-26")
	quiet := digest["quiet"].(map[string]any)
	assert.Equal(t, "k8s-quiet", quiet["account_name"])
	assert.Empty(t, quiet["recommendations"])
	assert.Equal(t, 40, quiet["totals"].(map[string]any)["carryover_count"])

	security := buildSecurityPostureAccountSections(nil, map[string]securityPostureCounts{"cooling": {Critical: 5}}, nil, nil)
	cooling := security["cooling"].(map[string]any)
	assert.Equal(t, "cooling", cooling["account_name"])
	assert.Equal(t, 5, cooling["totals"].(map[string]any)["critical_count"])
}

func TestDigestRecommendationIDsListsEachOnce(t *testing.T) {
	ids := digestRecommendationIDs(
		[]digestRecommendation{{ID: "r1"}, {ID: "r2"}},
		[]digestRecommendation{{ID: "r2"}, {ID: "r3"}},
	)
	assert.Equal(t, []string{"r1", "r2", "r3"}, ids)
}

// Runs the per-account selection and rollups against Postgres: each account
// gets its own list, capped per account and per rule family, and each account's
// numbers add up to the tenant's.
func TestDigestAccountQueries_DB(t *testing.T) {
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

	const schema = "zz_digest_accounts_test"
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
		cloud_account_id uuid, created_at timestamp, updated_at timestamp, status text,
		last_nudged_at timestamp, recommendation jsonb)`)

	const tenant = "00000000-0000-0000-0000-0000000000f1"
	const prod = "00000000-0000-0000-0000-0000000000a1"
	const dev = "00000000-0000-0000-0000-0000000000a2"
	mustExec(`INSERT INTO cloud_accounts VALUES ($1, 'k8s-prod'), ($2, 'k8s-dev')`, prod, dev)
	now := time.Now().UTC()
	insert := func(i int, account, rule, band, status string, score int, savings float64, created time.Time) {
		mustExec(`INSERT INTO recommendation (id, tenant_id, rule_name, finops_score, finops_band, estimated_savings,
			severity, category, cloud_account_id, created_at, updated_at, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'High', 'RightSizing', $7, $8, $8, $9)`,
			fmt.Sprintf("00000000-0000-0000-0000-%012d", i), tenant, rule, score, band, savings, account, created, status)
	}
	// k8s-prod: 30 open recs over 10 rule families, all scoring above k8s-dev.
	for i := 0; i < 30; i++ {
		insert(i, prod, fmt.Sprintf("rule_%d", i%10), "Act Now", "Open", 900-i, 10, now.Add(-48*time.Hour))
	}
	// k8s-dev: 2 low-scoring open recs (new today) and one resolved today.
	insert(100, dev, "rule_x", "High", "Open", 10, 1, now)
	insert(101, dev, "rule_y", "Critical", "Open", 9, 2, now)
	insert(102, dev, "rule_z", "High", "Closed", 9, 4, now.Add(-72*time.Hour))
	mustExec(`UPDATE recommendation SET updated_at = now() WHERE id = '00000000-0000-0000-0000-000000000102'`)

	recs, err := queryDigestAccountRecommendations(db, tenant)
	require.NoError(t, err)
	perAccount := map[string]int{}
	perFamily := map[string]int{}
	for _, r := range recs {
		perAccount[r.CloudAccountID]++
		perFamily[r.CloudAccountID+"/"+r.RuleName]++
	}
	assert.Equal(t, digestAccountLimit, perAccount[prod], "k8s-prod is capped at its own limit")
	assert.Equal(t, 2, perAccount[dev], "k8s-dev keeps its recommendations even though k8s-prod outscores them")
	for family, n := range perFamily {
		assert.LessOrEqual(t, n, 3, family)
	}

	cutoff := now.Add(-24 * time.Hour)
	deltas, err := queryDigestDeltasByAccount(db, tenant, cutoff)
	require.NoError(t, err)
	assert.Equal(t, 30, deltas[prod].CarryoverCount)
	assert.Equal(t, 1, deltas[dev].NewCounts["high"])
	assert.Equal(t, 1, deltas[dev].NewCounts["critical"])
	assert.Equal(t, 1, deltas[dev].ResolvedCount)
	assert.Equal(t, 4.0, deltas[dev].ResolvedSavings)
	assert.Equal(t, 300.0, deltas[prod].CategorySavings["RightSizing"])

	tenantNew, tenantResolved, tenantSavings, tenantCarryover, err := queryDigestDeltas(db, tenant, cutoff)
	require.NoError(t, err)
	sumNew := map[string]int{}
	sumResolved, sumCarryover := 0, 0
	sumSavings := 0.0
	for _, d := range deltas {
		for band, n := range d.NewCounts {
			sumNew[band] += n
		}
		sumResolved += d.ResolvedCount
		sumSavings += d.ResolvedSavings
		sumCarryover += d.CarryoverCount
	}
	assert.Equal(t, tenantNew, sumNew)
	assert.Equal(t, tenantResolved, sumResolved)
	assert.Equal(t, tenantSavings, sumSavings)
	assert.Equal(t, tenantCarryover, sumCarryover)
}

func TestSecurityPostureAccountQueries_DB(t *testing.T) {
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

	const schema = "zz_security_posture_accounts_test"
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
		cloud_account_id uuid, created_at timestamp, status text, last_nudged_at timestamp)`)

	const tenant = "00000000-0000-0000-0000-0000000000f1"
	const prod = "00000000-0000-0000-0000-0000000000a1"
	const dev = "00000000-0000-0000-0000-0000000000a2"
	mustExec(`INSERT INTO cloud_accounts VALUES ($1, 'k8s-prod'), ($2, 'k8s-dev')`, prod, dev)
	insert := func(i int, account, rule, severity string, nudged bool) {
		var last any
		if nudged {
			last = time.Now().UTC()
		}
		mustExec(`INSERT INTO recommendation (id, tenant_id, rule_name, severity, category, cloud_account_id,
			created_at, status, last_nudged_at) VALUES ($1, $2, $3, $4, 'Security', $5, now(), 'Open', $6)`,
			fmt.Sprintf("00000000-0000-0000-0000-%012d", i), tenant, rule, severity, account, last)
	}
	for i := 0; i < 25; i++ {
		insert(i, prod, fmt.Sprintf("cve_%d", i%12), "Critical", false)
	}
	insert(100, dev, "cve_a", "High", false)
	insert(101, dev, "cve_b", "High", true) // counted, but within its reminder window

	recs, err := querySecurityPostureAccountFindings(db, tenant)
	require.NoError(t, err)
	perAccount := map[string]int{}
	for _, r := range recs {
		perAccount[r.CloudAccountID]++
	}
	assert.Equal(t, securityPostureAccountLimit, perAccount[prod])
	assert.Equal(t, 1, perAccount[dev])

	counts, err := querySecurityPostureCountsByAccount(db, tenant)
	require.NoError(t, err)
	assert.Equal(t, securityPostureCounts{Critical: 25}, counts[prod])
	assert.Equal(t, securityPostureCounts{High: 2}, counts[dev])
}
