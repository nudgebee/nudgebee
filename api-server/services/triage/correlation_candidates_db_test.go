package triage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Which alerts the correlation step gets to score is decided by DISTINCT ON,
// ORDER BY and LIMIT, which sqlmock cannot evaluate. Named _DB like the mute
// suite; skipped locally unless a database is configured.
func correlationTestDB(t *testing.T) *sqlx.DB {
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
	schema := fmt.Sprintf("correlation_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA %s; SET search_path TO %s`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	// Mirrors the real events and event_correlations columns the correlation step
	// reads and writes.
	_, err = db.Exec(`
		CREATE TABLE events (
			id uuid PRIMARY KEY, tenant uuid, cloud_account_id uuid, fingerprint text,
			starts_at timestamp, ends_at timestamp, status text,
			subject_name text, subject_namespace text, subject_owner text, subject_owner_kind text,
			service_key text, cloud_resource_id uuid
		);
		CREATE TABLE event_correlations (
			event_id uuid NOT NULL, related_event_id uuid NOT NULL, cloud_account_id uuid NOT NULL,
			tenant_id uuid, correlation_type text NOT NULL, correlation_score numeric NOT NULL,
			correlation_reason text NOT NULL, time_offset_minutes int NOT NULL,
			dependency_distance int NOT NULL, created_at timestamp NOT NULL DEFAULT now(),
			UNIQUE (related_event_id, event_id, cloud_account_id, correlation_type)
		)`)
	require.NoError(t, err)
	return db
}

// In an account with more distinct alerts in the window than the candidate cap,
// the alert that fired right before the triaged one must still be scored. The
// cap used to keep the fingerprints that sort first as strings, so a related
// alert with a late-sorting fingerprint was dropped and never correlated.
func TestCorrelationCandidatesKeepNearestInTime_DB(t *testing.T) {
	db := correlationTestDB(t)
	ctx := context.Background()

	tenantID := "11111111-1111-1111-1111-111111111111"
	accountID := "22222222-2222-2222-2222-222222222222"
	base := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)

	seed := func(fp string, startsAt time.Time, namespace, serviceKey string) string {
		id := uuid.New().String()
		_, err := db.Exec(`INSERT INTO events
			(id, tenant, cloud_account_id, fingerprint, starts_at, subject_name, subject_namespace, service_key)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, tenantID, accountID, fp, startsAt, serviceKey, namespace, serviceKey)
		require.NoError(t, err, "seed event %s", fp)
		return id
	}

	// Enough unrelated alerts to fill the cap on their own, at the far edge of
	// the window, every one sorting before the related alert by fingerprint.
	for i := 0; i < MaxRecentEventsToCheck; i++ {
		seed(fmt.Sprintf("a-far-%03d", i), base.Add(-9*time.Minute), "other-ns", "other-svc")
	}
	// The related alert fired twice: same namespace and service as the triaged
	// event, the later firing one minute before it -> 0.30 + 0.10 + 0.15 = 0.55.
	seed("z-near", base.Add(-6*time.Minute), "shop", "checkout")
	nearID := seed("z-near", base.Add(-1*time.Minute), "shop", "checkout")
	triagedID := seed("fp-triaged", base, "shop", "checkout")

	triaged := &models.Event{
		Id:               triagedID,
		Tenant:           &tenantID,
		CloudAccountId:   &accountID,
		Fingerprint:      strPtr("fp-triaged"),
		StartsAt:         &base,
		SubjectNamespace: strPtr("shop"),
		ServiceKey:       strPtr("checkout"),
	}

	corrType, corrScore, err := detectAndRecordCorrelations(ctx, db, triaged)
	require.NoError(t, err)
	assert.Equal(t, "same_service", corrType, "the alert nearest in time must be scored")
	assert.InDelta(t, 0.55, corrScore, 1e-9)

	// Still one candidate per alert, and still its latest firing in the window.
	var related []string
	require.NoError(t, db.Select(&related,
		`SELECT related_event_id FROM event_correlations WHERE event_id = $1`, triagedID))
	assert.Equal(t, []string{nearID}, related)
}
