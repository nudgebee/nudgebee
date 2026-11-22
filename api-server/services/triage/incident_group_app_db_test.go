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

// seedTagsTestDB is a throwaway schema holding the cloud_resourses columns
// loadSeedResourceTags reads. jsonb_object_agg's NULL on a resource with no
// matching tags is Postgres behaviour sqlmock cannot reproduce.
func seedTagsTestDB(t *testing.T) *sqlx.DB {
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
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("seed_tags_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA %s; SET search_path TO %s`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	_, err = db.Exec(`CREATE TABLE cloud_resourses (id uuid PRIMARY KEY, tags jsonb)`)
	require.NoError(t, err)
	return db
}

func TestLoadSeedResourceTags_DB(t *testing.T) {
	db := seedTagsTestDB(t)
	ctx := context.Background()
	source := "AWS_CloudWatch_Alarm"
	load := func(t *testing.T, tags string) (resourceTags, error) {
		t.Helper()
		id := uuid.NewString()
		_, err := db.Exec(`INSERT INTO cloud_resourses (id, tags) VALUES ($1, $2::jsonb)`, id, tags)
		require.NoError(t, err)
		return loadSeedResourceTags(ctx, db, &models.Event{CloudResourceId: &id, Source: &source})
	}

	t.Run("resource with no matching tags gives no tags, not an error", func(t *testing.T) {
		for _, tags := range []string{`{"Name": ["web-1"]}`, `{}`, `null`} {
			got, err := load(t, tags)
			require.NoError(t, err, tags)
			assert.Equal(t, resourceTags{}, got, tags)
		}
	})
	t.Run("matching tags are read", func(t *testing.T) {
		got, err := load(t, `{"Application": ["Billing"], "Environment": "prod"}`)
		require.NoError(t, err)
		assert.Equal(t, resourceTags{App: "billing", Env: "prod"}, got)
	})
}
