package anomoly

import (
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stableCloseTestDB is a throwaway schema holding only the events columns
// closeAnomalyEventIfOpen reads and writes, so the hold's timestamp comparison
// and interval arithmetic run on a real Postgres. Skipped locally unless a
// database is configured.
func stableCloseTestDB(t *testing.T) *sqlx.DB {
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
	db, err := sqlx.Open("postgres", dsn)
	require.NoError(t, err)
	// One connection, so the search_path set below holds for every later statement.
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("anomaly_stable_close_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA %s; SET search_path TO %s;
		CREATE TABLE events (
			id uuid PRIMARY KEY, tenant uuid, cloud_account_id uuid, finding_id text,
			status text, starts_at timestamp, ends_at timestamp)`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})

	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	return db
}

func withStableClose(t *testing.T, enabled bool, hours int) {
	t.Helper()
	origEnabled, origHours := config.Config.FeatureAnomalyStableCloseEnabled, config.Config.NBAnomalyCloseStableHours
	config.Config.FeatureAnomalyStableCloseEnabled, config.Config.NBAnomalyCloseStableHours = enabled, hours
	t.Cleanup(func() {
		config.Config.FeatureAnomalyStableCloseEnabled, config.Config.NBAnomalyCloseStableHours = origEnabled, origHours
	})
}

const (
	stableCloseTenant  = "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"
	stableCloseAccount = "7a2e3d4c-5b6f-4071-9b8c-0d1e2f3a4b5c"
)

// firingAnomalyDetectedAgo stores a FIRING anomaly event whose latest
// anomalous detection (starts_at) was `ago` before now.
func firingAnomalyDetectedAgo(t *testing.T, db *sqlx.DB, name string, ago time.Duration) time.Time {
	t.Helper()
	detected := time.Now().UTC().Add(-ago).Truncate(time.Microsecond)
	_, err := db.Exec(`INSERT INTO events (id, tenant, cloud_account_id, finding_id, status, starts_at)
		VALUES (gen_random_uuid(), $1, $2, $3, 'FIRING', $4)`,
		stableCloseTenant, stableCloseAccount, anomalyFingerprint(stableCloseAccount, MetricAnomolyTypeCPU, name, "prod"), detected)
	require.NoError(t, err)
	return detected
}

func anomalyRow(t *testing.T, db *sqlx.DB, name string) (status string, endsAt *time.Time) {
	t.Helper()
	require.NoError(t, db.QueryRowx(`SELECT status, ends_at FROM events WHERE finding_id = $1`,
		anomalyFingerprint(stableCloseAccount, MetricAnomolyTypeCPU, name, "prod")).Scan(&status, &endsAt))
	return status, endsAt
}

// Without the flag a clean run closes the anomaly at once, as before.
func TestCloseAnomalyEventIfOpen_FlagOffClosesAtTheFirstCleanRun_DB(t *testing.T) {
	db := stableCloseTestDB(t)
	withStableClose(t, false, 3)
	firingAnomalyDetectedAgo(t, db, "checkout", time.Hour)

	before := time.Now().UTC().Add(-time.Second)
	closeAnomalyEventIfOpen(stableCloseAccount, stableCloseTenant, MetricAnomolyTypeCPU, "checkout", "prod")

	status, endsAt := anomalyRow(t, db, "checkout")
	assert.Equal(t, "RESOLVED", status)
	require.NotNil(t, endsAt)
	assert.True(t, endsAt.After(before), "ends_at is the close time, %v", endsAt)
}

// With the flag, a clean run inside the window leaves the anomaly open, so an
// instance that flaps hour to hour is never resolved and reopened.
func TestCloseAnomalyEventIfOpen_HoldsAnAnomalySeenWithinTheWindow_DB(t *testing.T) {
	db := stableCloseTestDB(t)
	withStableClose(t, true, 3)
	for name, ago := range map[string]time.Duration{
		"seen-last-hour":     time.Hour,
		"seen-just-inside":   3*time.Hour - time.Minute,
		"seen-two-hours-ago": 2 * time.Hour,
	} {
		firingAnomalyDetectedAgo(t, db, name, ago)
		closeAnomalyEventIfOpen(stableCloseAccount, stableCloseTenant, MetricAnomolyTypeCPU, name, "prod")

		status, endsAt := anomalyRow(t, db, name)
		assert.Equal(t, "FIRING", status, name)
		assert.Nil(t, endsAt, name)
	}
}

// Once the latest detection is older than the window, a clean run closes it.
// ends_at is the first clean run (one detection cycle after the last
// anomalous one), not the hour the hold ran out.
func TestCloseAnomalyEventIfOpen_ClosesOnceTheWindowHasPassed_DB(t *testing.T) {
	db := stableCloseTestDB(t)
	withStableClose(t, true, 3)
	for name, ago := range map[string]time.Duration{
		"seen-just-outside": 3*time.Hour + time.Minute,
		"seen-a-day-ago":    24 * time.Hour,
	} {
		detected := firingAnomalyDetectedAgo(t, db, name, ago)
		closeAnomalyEventIfOpen(stableCloseAccount, stableCloseTenant, MetricAnomolyTypeCPU, name, "prod")

		status, endsAt := anomalyRow(t, db, name)
		assert.Equal(t, "RESOLVED", status, name)
		require.NotNil(t, endsAt, name)
		assert.True(t, endsAt.Equal(detected.Add(anomalyDetectionInterval)),
			"%s: ends_at = %v, want one detection cycle after %v", name, endsAt, detected)
	}
}

// A window shorter than one detection cycle must not record an end in the
// future.
func TestCloseAnomalyEventIfOpen_EndIsNeverAfterTheClose_DB(t *testing.T) {
	db := stableCloseTestDB(t)
	withStableClose(t, true, 0)
	firingAnomalyDetectedAgo(t, db, "checkout", 10*time.Minute)

	closeAnomalyEventIfOpen(stableCloseAccount, stableCloseTenant, MetricAnomolyTypeCPU, "checkout", "prod")
	after := time.Now().UTC()

	status, endsAt := anomalyRow(t, db, "checkout")
	assert.Equal(t, "RESOLVED", status)
	require.NotNil(t, endsAt)
	assert.False(t, endsAt.After(after), "ends_at = %v is after the close", endsAt)
}
