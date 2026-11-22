package triage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database/models"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The claim, the failed-row parking and the retry hour are SQL (an UPSERT with a conditional
// steal), which sqlmock cannot evaluate. Named _DB like the correlation suite; skipped locally
// unless a database is configured.
func signalClassTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but neither APP_DATABASE_URL nor TEST_POSTGRES_DSN is")
		}
		t.Skip("no database configured; export APP_DATABASE_URL to run this suite")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	// One connection, so the search_path set below holds for every later statement.
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("signal_class_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA %s; SET search_path TO %s`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	// Mirrors V768 + V770.
	_, err = db.Exec(`
		CREATE TABLE triage_signal_class (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id uuid NOT NULL, class_key text NOT NULL,
			category text, intrinsic text, blast text, recurrence_semantics text, env_sensitivity text,
			band_floor text, band_ceiling text, confidence numeric NOT NULL DEFAULT 0, reasoning text,
			source_model text, status text NOT NULL DEFAULT 'minting', hit_count bigint NOT NULL DEFAULT 0,
			created_at timestamp NOT NULL DEFAULT now(), updated_at timestamp NOT NULL DEFAULT now(),
			prompt_version int NOT NULL DEFAULT 0,
			CONSTRAINT uq_triage_signal_class_tenant_key UNIQUE (tenant_id, class_key)
		)`)
	require.NoError(t, err)
	return db
}

func insertSignalClass(t *testing.T, db *sqlx.DB, tenantID, classKey, status string, age time.Duration) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO triage_signal_class (tenant_id, class_key, status, category, intrinsic, blast,
			recurrence_semantics, env_sensitivity, band_floor, band_ceiling, confidence, updated_at)
		VALUES ($1, $2, $3, 'resource_exhaustion', 'medium', 'single_workload', 'neutral', 'none',
			'P3', 'P2', 0.9, now() - $4::interval)`,
		tenantID, classKey, status, fmt.Sprintf("%d seconds", int(age.Seconds())))
	require.NoError(t, err)
}

func signalClassStatus(t *testing.T, db *sqlx.DB, tenantID, classKey string) string {
	t.Helper()
	var status string
	require.NoError(t, db.Get(&status,
		`SELECT status FROM triage_signal_class WHERE tenant_id = $1 AND class_key = $2`, tenantID, classKey))
	return status
}

func TestClaimSignalClass_FailedRowWaitsAnHour_DB(t *testing.T) {
	db := signalClassTestDB(t)
	ctx := context.Background()
	tenantID := uuid.NewString()

	insertSignalClass(t, db, tenantID, "recent-failure", "failed", 10*time.Minute)
	assert.False(t, claimSignalClass(ctx, db, tenantID, "recent-failure"), "a class that failed 10 min ago is not retried yet")
	assert.Equal(t, "failed", signalClassStatus(t, db, tenantID, "recent-failure"))
	assert.True(t, isSignalClassFailed(ctx, db, tenantID, "recent-failure"))

	insertSignalClass(t, db, tenantID, "old-failure", "failed", 61*time.Minute)
	assert.True(t, claimSignalClass(ctx, db, tenantID, "old-failure"), "a class that failed over an hour ago is re-claimed")
	assert.Equal(t, "minting", signalClassStatus(t, db, tenantID, "old-failure"))

	// Unchanged behaviour: a fresh claim and an active verdict are not stolen, a stale lease is.
	insertSignalClass(t, db, tenantID, "fresh-lease", "minting", time.Minute)
	assert.False(t, claimSignalClass(ctx, db, tenantID, "fresh-lease"))
	insertSignalClass(t, db, tenantID, "stale-lease", "minting", 16*time.Minute)
	assert.True(t, claimSignalClass(ctx, db, tenantID, "stale-lease"))
	insertSignalClass(t, db, tenantID, "served", "active", 2*time.Hour)
	assert.False(t, claimSignalClass(ctx, db, tenantID, "served"))
	assert.Equal(t, "active", signalClassStatus(t, db, tenantID, "served"))
	assert.False(t, isSignalClassFailed(ctx, db, tenantID, "served"))
}

func TestMarkSignalClassFailed_OnlyParksAClaim_DB(t *testing.T) {
	db := signalClassTestDB(t)
	ctx := context.Background()
	tenantID := uuid.NewString()

	insertSignalClass(t, db, tenantID, "claimed", "minting", 0)
	markSignalClassFailed(ctx, db, tenantID, "claimed")
	assert.Equal(t, "failed", signalClassStatus(t, db, tenantID, "claimed"))

	// A verdict that is serving (or being re-minted) is never parked.
	insertSignalClass(t, db, tenantID, "serving", "reminting", 0)
	markSignalClassFailed(ctx, db, tenantID, "serving")
	assert.Equal(t, "reminting", signalClassStatus(t, db, tenantID, "serving"))
}

// A mint that errors parks the class instead of deleting the claim, so the next alert does not
// retry the LLM. The event has no account, so mintSignalVerdict fails before any LLM call.
func TestTriggerMint_FailedMintParksTheClass_DB(t *testing.T) {
	db := signalClassTestDB(t)
	withMintCap(t, 1000)
	tenantID := uuid.NewString()
	event := &models.Event{Tenant: &tenantID}

	assert.True(t, triggerMint(context.Background(), db, event, tenantID, "no-account"), "the first alert of a class starts a mint")
	require.Eventually(t, func() bool {
		return signalClassStatus(t, db, tenantID, "no-account") == "failed"
	}, 5*time.Second, 50*time.Millisecond)

	assert.False(t, triggerMint(context.Background(), db, event, tenantID, "no-account"), "the next alert within the hour gets no mint")
	assert.Equal(t, "failed", signalClassStatus(t, db, tenantID, "no-account"))
}

func TestTriggerMint_InFlightElsewhereKeepsFallback_DB(t *testing.T) {
	db := signalClassTestDB(t)
	withMintCap(t, 1000)
	tenantID := uuid.NewString()

	insertSignalClass(t, db, tenantID, "minting-elsewhere", "minting", time.Minute)
	assert.True(t, triggerMint(context.Background(), db, &models.Event{Tenant: &tenantID}, tenantID, "minting-elsewhere"),
		"a mint running on another replica still means a verdict is coming")
}

func TestComputeScoreLLM_NoVerdictPossible_DB(t *testing.T) {
	db := signalClassTestDB(t)
	ctx := context.Background()
	tenantID := uuid.NewString()
	event := &models.Event{Tenant: &tenantID, AggregationKey: strPtr("KubePodCrashLooping"), FindingType: strPtr("issue")}
	classKey := ClassKey(event)

	t.Run("class failed within the hour", func(t *testing.T) {
		withMintCap(t, 1000)
		insertSignalClass(t, db, tenantID, classKey, "failed", time.Minute)
		_, err := ComputeScoreLLM(ctx, db, event, "", 0, false)
		assert.True(t, errors.Is(err, ErrNoVerdictPossible))
	})

	t.Run("tenant out of mint attempts", func(t *testing.T) {
		withMintCap(t, 0)
		other := uuid.NewString()
		ev := &models.Event{Tenant: &other, AggregationKey: strPtr("KubePodCrashLooping"), FindingType: strPtr("issue")}
		_, err := ComputeScoreLLM(ctx, db, ev, "", 0, false)
		assert.True(t, errors.Is(err, ErrNoVerdictPossible))
		var rows int
		require.NoError(t, db.Get(&rows, `SELECT count(*) FROM triage_signal_class WHERE tenant_id = $1`, other))
		assert.Zero(t, rows, "no claim is made once the cap is reached")
	})
}

// With AI scoring on, an alert whose class can't get a verdict is scored by the legacy formula —
// a HIGH alert can still reach the top of the scale — and is tagged so a later mint rescores it.
func TestComputeScore_NoVerdictPossibleUsesLegacy_DB(t *testing.T) {
	db := signalClassTestDB(t)
	ctx := context.Background()
	prevOn, prevShadow := config.Config.FeatureLLMTriageScoringEnabled, config.Config.FeatureLLMTriageScoringShadow
	config.Config.FeatureLLMTriageScoringEnabled, config.Config.FeatureLLMTriageScoringShadow = true, false
	t.Cleanup(func() {
		config.Config.FeatureLLMTriageScoringEnabled, config.Config.FeatureLLMTriageScoringShadow = prevOn, prevShadow
	})
	withMintCap(t, 1000)

	tenantID := uuid.NewString()
	event := &models.Event{Tenant: &tenantID, AggregationKey: strPtr("KubePodCrashLooping"),
		FindingType: strPtr("issue"), Priority: strPtr("HIGH")}
	insertSignalClass(t, db, tenantID, ClassKey(event), "failed", time.Minute)

	got, err := ComputeScore(ctx, db, event, 1, "", 0, false)
	require.NoError(t, err)
	want, err := computeLegacyScore(ctx, db, event, 1, "", 0, false)
	require.NoError(t, err)

	assert.Equal(t, want.Score, got.Score)
	assert.Equal(t, want.Priority, got.Priority)
	assert.Nil(t, got.Factors["scoring_path"], "not presented as an AI verdict")
	assert.Equal(t, "fallback", got.Factors["verdict_source"])
	assert.Equal(t, ClassKey(event), got.Factors["class_key"])
}
