package triage

import (
	"context"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChronicStats_Chronic(t *testing.T) {
	assert.False(t, ChronicStats{WeeklyCount: 0}.Chronic())
	assert.False(t, ChronicStats{WeeklyCount: ChronicWeeklyThreshold - 1}.Chronic())
	assert.True(t, ChronicStats{WeeklyCount: ChronicWeeklyThreshold}.Chronic())
	assert.True(t, ChronicStats{WeeklyCount: 400}.Chronic())
}

func TestChronicStats_IsBursting(t *testing.T) {
	// Low-rate chronic pair (10/week ≈ 0.06/hour): the factor bar rounds to
	// nothing, so the min-count floor governs — 1 or 2 firings in the hour must
	// NOT escape, 3 must.
	low := ChronicStats{WeeklyCount: 10}
	assert.False(t, low.IsBursting(1))
	assert.False(t, low.IsBursting(2))
	assert.True(t, low.IsBursting(3))

	// High-rate chronic pair (7×24=168h → 336/week = 2/hour baseline): the
	// factor bar (3×2=6) exceeds the floor, so 3 firings is business as usual
	// and only ≥6 escapes.
	high := ChronicStats{WeeklyCount: 336}
	assert.False(t, high.IsBursting(3))
	assert.False(t, high.IsBursting(5))
	assert.True(t, high.IsBursting(6))
}

func TestChronicSubjectIdentity(t *testing.T) {
	ev := makeEvent("1", time.Now(),
		withNamespace("Demo"),
		withSubjectName("postgresql-84b866ff4f-mj7s6"),
		withSubjectOwner("PostgreSQL", "Deployment"),
	)
	ns, subj := chronicSubjectIdentity(ev)
	assert.Equal(t, "demo", ns)
	assert.Equal(t, "postgresql", subj, "owner wins over pod name, lowered")

	// Empty-string owner falls back to the name (sqlx scans empty TEXT into a
	// non-nil pointer at "").
	ev2 := makeEvent("2", time.Now(),
		withNamespace("demo"),
		withSubjectName("flagd"),
		withSubjectOwner("", ""),
	)
	_, subj2 := chronicSubjectIdentity(ev2)
	assert.Equal(t, "flagd", subj2)

	// Whitespace-only owner must also fall back to the name (trim happens
	// before the emptiness check).
	ev3 := makeEvent("3", time.Now(),
		withNamespace("demo"),
		withSubjectName("flagd"),
		withSubjectOwner("   ", ""),
	)
	_, subj3 := chronicSubjectIdentity(ev3)
	assert.Equal(t, "flagd", subj3)
}

// TestLoadChronicStats_E2E seeds firings at a fixed year-2020 timestamp inside
// an always-rolled-back transaction (the TestDetectAndRecordCorrelations_E2E
// isolation pattern) and asserts the trailing count, the self-exclusion, and
// the lookback bound.
func TestLoadChronicStats_E2E(t *testing.T) {
	if os.Getenv("TEST_LIVE_CORRELATION") != "1" {
		t.Skip("set TEST_LIVE_CORRELATION=1 to run (requires local Metastore connection)")
	}

	env := testenv.RequireEnv(t, "TEST_ACCOUNT_ID")
	account := env["TEST_ACCOUNT_ID"]

	dbms, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "failed to connect to Metastore — check .env")
	ctx := context.Background()

	var tenant string
	require.NoError(t, dbms.Db.GetContext(ctx, &tenant,
		`SELECT tenant::text FROM cloud_accounts WHERE id = $1`, account))

	tx, err := dbms.Db.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	const (
		nsChronic = "ns-e2e-chronic"
		aggKey    = "e2e-chronic-key"
	)
	anchor := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)

	insert := func(id string, startsAt time.Time, owner string) {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, tenant, cloud_account_id, aggregation_key,
				subject_namespace, subject_name, subject_owner, fingerprint,
				starts_at, created_at, source, title)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, 'e2e-test', 'chronic e2e')`,
			id, tenant, account, aggKey, nsChronic, owner+"-pod-abc12", owner, "fp-"+id, startsAt)
		require.NoError(t, err)
	}

	// 9 prior firings inside the lookback + 1 outside it + 1 for a different subject.
	for i := 0; i < 9; i++ {
		insert(uuid.NewString(), anchor.Add(-time.Duration(i+1)*6*time.Hour), "svc-a")
	}
	insert(uuid.NewString(), anchor.Add(-ChronicLookback-time.Hour), "svc-a") // outside lookback
	insert(uuid.NewString(), anchor.Add(-time.Hour), "svc-b")                 // different subject

	triagedID := uuid.NewString()
	insert(triagedID, anchor, "svc-a")

	triaged := &models.Event{
		Id:               triagedID,
		Tenant:           &tenant,
		CloudAccountId:   &account,
		AggregationKey:   strPtr(aggKey),
		SubjectNamespace: strPtr(nsChronic),
		SubjectName:      strPtr("svc-a-pod-abc12"),
		SubjectOwner:     strPtr("svc-a"),
		StartsAt:         &anchor,
	}

	stats, err := LoadChronicStats(ctx, tx, triaged)
	require.NoError(t, err)
	// 9 in-window prior firings; the outside-lookback row, the other subject's
	// row, and the triaged event itself are all excluded.
	assert.Equal(t, 9, stats.WeeklyCount)
	assert.False(t, stats.Chronic(), "9 prior firings is one short of the threshold")

	// One more prior firing tips it over.
	insert(uuid.NewString(), anchor.Add(-30*time.Minute), "svc-a")
	stats, err = LoadChronicStats(ctx, tx, triaged)
	require.NoError(t, err)
	assert.Equal(t, 10, stats.WeeklyCount)
	assert.True(t, stats.Chronic())
}
