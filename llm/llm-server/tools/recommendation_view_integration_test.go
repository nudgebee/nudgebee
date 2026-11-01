package tools

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecommendationViewPrimaryFlagEqualsWindow runs the view against a real
// Postgres and checks that is_primary_recommendation marks exactly the rows
// PrimaryRecommendationRank ranks first, on fixtures built to hit every
// partition branch, NULL savings, NULL updated_at, NULL category, groups with
// only retired rows, and ties broken by id. Set
// RECOMMENDATION_VIEW_TEST_DB_URL to a disposable database to run it; the test
// works in its own schema and drops it afterwards.
func TestRecommendationViewPrimaryFlagEqualsWindow(t *testing.T) {
	dsn := os.Getenv("RECOMMENDATION_VIEW_TEST_DB_URL")
	if dsn == "" {
		t.Skip("RECOMMENDATION_VIEW_TEST_DB_URL not set")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	schema := fmt.Sprintf("recommendation_view_test_%d", time.Now().UnixNano())
	db.MustExec("CREATE SCHEMA " + schema)
	t.Cleanup(func() { db.MustExec("DROP SCHEMA " + schema + " CASCADE") })
	db.MustExec("SET search_path TO " + schema)
	db.MustExec(`
		CREATE TABLE tenant (id uuid PRIMARY KEY, name text);
		CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, account_name text, cloud_provider text);
		CREATE TABLE cloud_resourses (id uuid PRIMARY KEY, name text, service_name text, meta jsonb);
		CREATE TABLE recommendation (
			id uuid PRIMARY KEY, tenant_id uuid, cloud_account_id uuid, resource_id uuid,
			recommendation jsonb, estimated_savings double precision,
			created_at timestamp DEFAULT now(), updated_at timestamp,
			recommendation_action text, category text, note text, severity text, status text, rule_name text,
			dismissed_reason text, is_dismissed boolean, snoozed_until timestamp, account_object_id text,
			updated_by uuid, finops_score int, finops_band text, finops_score_breakdown jsonb, dedupe_group text);
		CREATE INDEX ON recommendation (resource_id, cloud_account_id);
		CREATE INDEX ON recommendation (cloud_account_id, status, estimated_savings DESC NULLS LAST);`)

	f := newRecommendationFixtures(db)
	aws := f.account("aws")
	azure := f.account("azure")
	gcp := f.account("gcp")

	for _, acct := range []uuid.UUID{aws, azure, gcp} {
		for g := 0; g < 25; g++ {
			group := fmt.Sprintf("commitment:%s:%d", acct, g)
			for v := 0; v < 1+f.rnd.Intn(5); v++ {
				f.row(acct, rowOpts{dedupeGroup: group})
			}
		}
		for res := 0; res < 40; res++ {
			resource := f.resource()
			for v := 0; v < 1+f.rnd.Intn(6); v++ {
				o := rowOpts{resourceID: &resource}
				if f.rnd.Intn(10) == 0 {
					// A dedupe_group outranks the resource in the partition key.
					o.dedupeGroup = fmt.Sprintf("commitment:%s:%d", acct, f.rnd.Intn(25))
				}
				f.row(acct, o)
			}
		}
		for s := 0; s < 60; s++ {
			f.row(acct, rowOpts{})
		}
	}
	// Resource-less rows carrying the Azure key: grouped on the Azure account,
	// each its own group anywhere else.
	for _, acct := range []uuid.UUID{azure, gcp} {
		for g := 0; g < 30; g++ {
			key := map[string]any{"recommendation_type_id": fmt.Sprintf("type-%d", g%7), "ext_sku": fmt.Sprintf("sku-%d", g%3)}
			if g%2 == 0 {
				key["ext_subid"] = fmt.Sprintf("sub-%d", g%4)
			} else {
				key["subscription_id"] = fmt.Sprintf("sub-%d", g%4)
			}
			for v := 0; v < 1+f.rnd.Intn(6); v++ {
				f.row(acct, rowOpts{payload: key})
			}
		}
	}
	db.MustExec("ANALYZE")

	for _, acct := range []uuid.UUID{aws, azure, gcp} {
		want := primaryFlags(t, db, `SELECT r.id::text, (`+PrimaryRecommendationRank("r", "ca")+` = 1)
			FROM recommendation r JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
			WHERE r.cloud_account_id = $1`, acct)
		got := primaryFlags(t, db, `SELECT id, is_primary_recommendation FROM (`+recommendationViewFor(acct.String())+`) v`)

		require.NotEmpty(t, want)
		assert.Equal(t, want, got, "is_primary_recommendation must equal the window's rank-1 row on every row")
		nonPrimary := 0
		for _, primary := range want {
			if !primary {
				nonPrimary++
			}
		}
		assert.Greater(t, nonPrimary, 0, "fixtures must contain groups with more than one row")
	}

	// The dedupe_group and Azure rankings must be hashed subplans, built once
	// per query, under the work_mem sqlExecuteQuery sets.
	tx := db.MustBegin()
	defer func() { _ = tx.Rollback() }()
	tx.MustExec("SET LOCAL work_mem = '32MB'")
	var plan []string
	require.NoError(t, tx.Select(&plan, `EXPLAIN SELECT id FROM (`+recommendationViewFor(azure.String())+`) v
		WHERE status = 'Open' AND estimated_saving > 0 AND is_primary_recommendation`))
	assert.Equal(t, 2, strings.Count(strings.Join(plan, "\n"), "(hashed SubPlan"),
		"the dedupe_group and Azure arms must each be a hashed subplan")
}

// primaryFlags maps each row id a query returns to its primary flag.
func primaryFlags(t *testing.T, db *sqlx.DB, query string, args ...any) map[string]bool {
	t.Helper()
	rows, err := db.Queryx(query, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	flags := map[string]bool{}
	for rows.Next() {
		var id string
		var primary bool
		require.NoError(t, rows.Scan(&id, &primary))
		flags[id] = primary
	}
	require.NoError(t, rows.Err())
	return flags
}

type recommendationFixtures struct {
	db     *sqlx.DB
	rnd    *rand.Rand
	tenant uuid.UUID
	base   time.Time
}

type rowOpts struct {
	dedupeGroup string
	resourceID  *uuid.UUID
	payload     map[string]any
}

func newRecommendationFixtures(db *sqlx.DB) *recommendationFixtures {
	f := &recommendationFixtures{db: db, rnd: rand.New(rand.NewSource(7)), tenant: uuid.New(), base: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	db.MustExec(`INSERT INTO tenant (id, name) VALUES ($1, 'tenant')`, f.tenant)
	return f
}

func (f *recommendationFixtures) account(provider string) uuid.UUID {
	id := uuid.New()
	f.db.MustExec(`INSERT INTO cloud_accounts (id, account_name, cloud_provider) VALUES ($1, $2, $3)`, id, provider+"-account", strings.ToUpper(provider[:1])+provider[1:])
	return id
}

func (f *recommendationFixtures) resource() uuid.UUID {
	id := uuid.New()
	f.db.MustExec(`INSERT INTO cloud_resourses (id, name, service_name, meta) VALUES ($1, 'workload', 'kubernetes', '{"namespace":"default"}')`, id)
	return id
}

// row inserts one recommendation whose ranking inputs are drawn from small
// sets, so NULLs and exact ties between siblings are common.
func (f *recommendationFixtures) row(acct uuid.UUID, o rowOpts) {
	statuses := []string{"Open", "Open", "InProgress", "Dismissed", "Archive", "Closed"}
	categories := []any{"RightSizing", "Cost", "RightSizing", nil}
	var savings any
	if s := f.rnd.Intn(5); s > 0 {
		savings = float64(s * 10)
	}
	var updatedAt any
	if u := f.rnd.Intn(4); u > 0 {
		updatedAt = f.base.Add(time.Duration(u) * time.Hour)
	}
	payload := "{}"
	if o.payload != nil {
		b := make([]string, 0, len(o.payload))
		for k, v := range o.payload {
			b = append(b, fmt.Sprintf("%q:%q", k, v))
		}
		payload = "{" + strings.Join(b, ",") + "}"
	}
	var dedupe any
	if o.dedupeGroup != "" {
		dedupe = o.dedupeGroup
	} else if f.rnd.Intn(8) == 0 {
		dedupe = ""
	}
	f.db.MustExec(`INSERT INTO recommendation
		(id, tenant_id, cloud_account_id, resource_id, recommendation, estimated_savings, updated_at, category, status, rule_name, dedupe_group)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, 'rule', $10)`,
		uuid.New(), f.tenant, acct, o.resourceID, payload, savings, updatedAt,
		categories[f.rnd.Intn(len(categories))], statuses[f.rnd.Intn(len(statuses))], dedupe)
}
