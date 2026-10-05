package event

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertEventTestDB replaces the enrichment fixture's events table with every
// column InsertEvent writes, and the unique key its upsert conflicts on.
func insertEventTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db := enrichmentTestDB(t)
	_, err := db.Exec(`DROP TABLE IF EXISTS events; CREATE TABLE events (
		id uuid PRIMARY KEY, finding_id text, title text, description text, source text,
		aggregation_key text, priority text, subject_type text, subject_name text,
		subject_namespace text, subject_node text, service_key text, fingerprint text,
		evidences jsonb, tenant uuid, cloud_account_id uuid, status text, finding_type text,
		cluster text, starts_at timestamp, labels jsonb, updated_at timestamp, ends_at timestamp,
		cloud_resource_id uuid, category text, created_at timestamp, urgency text,
		subject_owner text, subject_owner_kind text, nb_status text, principal text,
		UNIQUE (tenant, cloud_account_id, finding_id))`)
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	return db
}

func storedEventId(t *testing.T, db *sqlx.DB, findingId string) string {
	t.Helper()
	var id string
	require.NoError(t, db.QueryRowx(`SELECT id FROM events WHERE finding_id = $1`, findingId).Scan(&id))
	return id
}

// The scheduled jobs (anomaly, spend anomaly, SLO) pass no id, so InsertEvent
// generates one. When the finding is already stored the upsert updates that
// row and keeps its id; the generated one names no event. The update has to
// return, and publish for, the row it actually wrote.
func TestInsertEvent_UpdateReturnsAndPublishesTheStoredId_DB(t *testing.T) {
	db := insertEventTestDB(t)

	var published []string
	orig := publishResolved
	publishResolved = func(_ context.Context, ids []string) { published = append(published, ids...) }
	t.Cleanup(func() { publishResolved = orig })

	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	firing := Event{
		Tenant: resolveTestTenant, AccountId: resolveTestAccount, FindingId: "spend-anomaly-1",
		Title: "Cloud Spend Anomaly", Source: "anomaly", Priority: EventPriorityHigh,
		Status: EventStatusFiring, StartsAt: &start,
	}
	firstId, err := InsertEvent(firing, "")
	require.NoError(t, err)
	stored := storedEventId(t, db, "spend-anomaly-1")
	require.Equal(t, stored, firstId, "a first insert stores the id it returns")

	t.Run("a repeat firing returns the stored id", func(t *testing.T) {
		id, err := InsertEvent(firing, "")
		require.NoError(t, err)
		assert.Equal(t, stored, id)
	})

	t.Run("a resolve returns, and is published for, the stored id", func(t *testing.T) {
		end := start.Add(72 * time.Hour)
		resolved := firing
		resolved.Status = EventStatusResolved
		resolved.EndsAt = &end

		id, err := InsertEvent(resolved, "")
		require.NoError(t, err)
		assert.Equal(t, stored, id)
		assert.Equal(t, []string{stored}, published)
	})
}

// A scheduled job's re-detection of a still-firing alert re-runs event-trigger
// workflows only when FeatureScheduledAlertWorkflowRefireEnabled is on; webhook
// repeats never re-run them, so off is the default.
func TestScheduledDetection_SkipsTheWorkflowRefireUnlessEnabled(t *testing.T) {
	orig := config.Config.FeatureScheduledAlertWorkflowRefireEnabled
	t.Cleanup(func() { config.Config.FeatureScheduledAlertWorkflowRefireEnabled = orig })

	for _, enabled := range []bool{false, true} {
		config.Config.FeatureScheduledAlertWorkflowRefireEnabled = enabled
		var cfg insertConfig
		ScheduledDetection()(&cfg)
		assert.Equal(t, !enabled, cfg.skipWorkflowRefire, "flag %v", enabled)

		// It only ever adds the skip: it never undoes WithoutWorkflowRefire.
		cfg = insertConfig{}
		WithoutWorkflowRefire()(&cfg)
		ScheduledDetection()(&cfg)
		assert.True(t, cfg.skipWorkflowRefire, "flag %v after WithoutWorkflowRefire", enabled)
	}
}
