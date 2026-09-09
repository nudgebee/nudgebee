package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Real-Postgres coverage for which recommendations an auto optimize run picks
// up (GetFullRecommendationsForOptimizerCategory).
//
// Raising a rightsizing pull request flips its recommendation to InProgress. On
// `status = 'Open'` alone the workload is then never recomputed again, so the
// api-server's refresh guard is never reached and the pull request goes stale —
// which is the entirety of #34959: "nobody merges it, the recommendation
// underneath keeps moving". Nothing else frees it; the reopen sweep only touches
// recommendations with no resolutions at all.
//
// The widening has to stay narrow, so most of what follows is the negatives.

func selectionTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	db, err := sqlx.Connect("postgres", requireTestDSN(t))
	require.NoError(t, err, "connect to TEST_POSTGRES_DSN")

	schema := fmt.Sprintf("selection_test_%d", os.Getpid())
	_, err = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s`,
		schema, schema, schema))
	require.NoError(t, err)

	// Only the columns the selection query reads.
	_, err = db.Exec(`
		CREATE TABLE cloud_resourses (
			id uuid PRIMARY KEY, resourse_id text, status text, meta jsonb
		);
		CREATE TABLE recommendation (
			id uuid PRIMARY KEY,
			created_at timestamp NOT NULL DEFAULT now(),
			updated_at timestamp NOT NULL DEFAULT now(),
			tenant_id uuid NOT NULL,
			cloud_account_id uuid NOT NULL,
			resource_id uuid,
			recommendation jsonb,
			recommendation_action text,
			severity text,
			rule_name text,
			estimated_savings double precision,
			status text NOT NULL,
			category text,
			is_dismissed boolean NOT NULL DEFAULT false,
			dismissed_reason text,
			finops_score double precision,
			finops_band text,
			finops_score_breakdown jsonb,
			last_nudged_at timestamp,
			dedupe_group text,
			note text,
			account_object_id text,
			updated_by uuid
		);
		CREATE TABLE recommendation_resolution (
			id uuid PRIMARY KEY,
			recommendation_id uuid NOT NULL,
			type text NOT NULL,
			status text NOT NULL,
			type_reference_id text NOT NULL DEFAULT '',
			resolver_type text NOT NULL,
			pr_lifecycle_state text,
			value_refresh_count integer NOT NULL DEFAULT 0,
			last_value_refresh_at timestamp
		)`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return db
}

// selectionCase is one recommendation plus the resolutions hanging off it, and
// whether an auto optimize run should pick it up.
type selectionCase struct {
	name       string
	recStatus  string
	resolution []testResolution
	wantPicked bool
	because    string
}

type testResolution struct {
	rType     string
	status    string
	refID     string
	resolver  string
	lifecycle *string
	refreshed *time.Time // last_value_refresh_at
	refreshes int        // value_refresh_count
}

func TestGetFullRecommendations_SelectionAcrossRecommendationStatus(t *testing.T) {
	db := selectionTestDB(t)
	dao := &OptimizerDao{db: db}

	accountID, tenantID := uuid.New(), uuid.New()
	lifecycle := func(s string) *string { return &s }
	ago := func(d time.Duration) *time.Time { v := time.Now().UTC().Add(-d); return &v }
	openOwnPR := testResolution{"PullRequest", "InProgress", "https://github.com/acme/infra/pull/950", "AutoOptimize", lifecycle("created"), nil, 0}

	cases := []selectionCase{
		{
			name:       "open recommendation",
			recStatus:  "Open",
			wantPicked: true,
			because:    "the ordinary case, unchanged by this predicate",
		},
		{
			name:       "in progress behind its own open pull request",
			recStatus:  "InProgress",
			resolution: []testResolution{openOwnPR},
			wantPicked: true,
			because:    "the run must recompute so the guard can decide whether to rewrite that PR (#34959)",
		},
		{
			name:       "in progress behind its own open PR, refreshed long ago",
			recStatus:  "InProgress",
			resolution: []testResolution{{"PullRequest", "InProgress", "https://github.com/acme/infra/pull/951", "AutoOptimize", lifecycle("created"), ago(model.ValueRefreshCooldown + time.Hour), 0}},
			wantPicked: true,
			because:    "past the cooldown, so it is due another look",
		},
		{
			name:       "in progress behind its own open PR, just refreshed",
			recStatus:  "InProgress",
			resolution: []testResolution{{"PullRequest", "InProgress", "https://github.com/acme/infra/pull/952", "AutoOptimize", lifecycle("created"), ago(time.Minute), 0}},
			wantPicked: false,
			because: "inside the cooldown. The generator cannot throttle this itself — it compares " +
				"against the live cluster allocation, which does not move while the PR is unmerged, " +
				"so it would report a large change on every run forever",
		},
		{
			name:       "in progress behind its own open PR, refreshed three hours ago",
			recStatus:  "InProgress",
			resolution: []testResolution{{"PullRequest", "InProgress", "https://github.com/acme/infra/pull/953", "AutoOptimize", lifecycle("created"), ago(3 * time.Hour), 0}},
			wantPicked: false,
			because: "half way into the 6h cooldown. This is the case a timezone-skewed comparison " +
				"gets wrong: last_value_refresh_at is naive UTC, so comparing it against bare now() " +
				"reinterprets it in the session timezone and shrinks the window by the offset — " +
				"under Asia/Kolkata the cooldown collapses to about half an hour and this row is " +
				"reselected, which is the hourly churn the cooldown exists to prevent",
		},
		{
			name:      "in progress behind its own open PR that has used its rewrite budget",
			recStatus: "InProgress",
			resolution: []testResolution{
				{"PullRequest", "InProgress", "https://github.com/acme/infra/pull/954", "AutoOptimize", lifecycle("created"), nil, model.ValueRefreshCap},
			},
			wantPicked: false,
			because: "the guard refuses at valueRefreshBlocked BEFORE claiming, so nothing ever " +
				"re-stamps last_value_refresh_at — it reads as due forever and this workload " +
				"would be reselected on every run for the rest of the pull request's life",
		},
		{
			name:       "in progress behind a pull request a person raised",
			recStatus:  "InProgress",
			resolution: []testResolution{{"PullRequest", "InProgress", "https://github.com/acme/infra/pull/901", "User", lifecycle("created"), nil, 0}},
			wantPicked: false,
			because:    "we never rewrite a human's pull request, so recomputing behind one is pointless churn",
		},
		{
			name:       "in progress behind a ticket",
			recStatus:  "InProgress",
			resolution: []testResolution{{"Ticket", "InProgress", "35838", "User", nil, nil, 0}},
			wantPicked: false,
			because:    "a delegated ticket is genuinely still in flight; re-running would duplicate real work",
		},
		{
			name:       "in progress behind a deployment change",
			recStatus:  "InProgress",
			resolution: []testResolution{{"DeploymentChange", "InProgress", "task-1", "AutoOptimize", nil, nil, 0}},
			wantPicked: false,
			because:    "an apply is mid-flight; this predicate is only about an open pull request",
		},
		{
			name:       "in progress behind its own PR AND a ticket",
			recStatus:  "InProgress",
			resolution: []testResolution{openOwnPR, {"Ticket", "InProgress", "35839", "User", nil, nil, 0}},
			wantPicked: false,
			because:    "the pull request is not the ONLY blocker, so something else is still running",
		},
		{
			name:       "in progress behind a merged pull request",
			recStatus:  "InProgress",
			resolution: []testResolution{{"PullRequest", "Success", "https://github.com/acme/infra/pull/900", "AutoOptimize", lifecycle("merged"), nil, 0}},
			wantPicked: false,
			because:    "the PR landed; the recommendation is settled by the coordinator, not re-run here",
		},
		{
			name:       "in progress behind a creation that never produced a URL",
			recStatus:  "InProgress",
			resolution: []testResolution{{"PullRequest", "InProgress", "", "AutoOptimize", nil, nil, 0}},
			wantPicked: false,
			because:    "creation is still in flight; there is no pull request to refresh yet",
		},
		{
			name:       "closed recommendation",
			recStatus:  "Closed",
			resolution: []testResolution{openOwnPR},
			wantPicked: false,
			because:    "only Open and InProgress are ever candidates",
		},
	}

	recIDs := make([]uuid.UUID, len(cases))
	for i, c := range cases {
		resID := uuid.New()
		_, err := db.Exec(`INSERT INTO cloud_resourses (id, resourse_id, status, meta)
			VALUES ($1, $2, 'Active', '{"cpu":"100m"}')`, resID, fmt.Sprintf("default/Deployment/wl-%d", i))
		require.NoError(t, err)

		recIDs[i] = uuid.New()
		_, err = db.Exec(`INSERT INTO recommendation
			(id, tenant_id, cloud_account_id, resource_id, recommendation, status, category,
			 rule_name, recommendation_action, is_dismissed)
			VALUES ($1,$2,$3,$4,'{}'::jsonb,$5,'RightSizing','pod_right_sizing','apply',false)`,
			recIDs[i], tenantID, accountID, resID, c.recStatus)
		require.NoError(t, err, "seed %q", c.name)

		for _, r := range c.resolution {
			_, err = db.Exec(`INSERT INTO recommendation_resolution
				(id, recommendation_id, type, status, type_reference_id, resolver_type,
				 pr_lifecycle_state, last_value_refresh_at, value_refresh_count)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				uuid.New(), recIDs[i], r.rType, r.status, r.refID, r.resolver, r.lifecycle, r.refreshed, r.refreshes)
			require.NoError(t, err, "seed resolution for %q", c.name)
		}
	}

	got, err := dao.GetFullRecommendationsForOptimizerCategory(context.Background(), accountID, "vertical_rightsize")
	require.NoError(t, err)

	picked := make(map[uuid.UUID]bool, len(got))
	for _, r := range got {
		picked[r.ID] = true
	}

	for i, c := range cases {
		if c.wantPicked {
			require.Truef(t, picked[recIDs[i]], "%q must be picked up — %s", c.name, c.because)
			continue
		}
		require.Falsef(t, picked[recIDs[i]], "%q must NOT be picked up — %s", c.name, c.because)
	}
}
