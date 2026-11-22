package dashboard

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two saves of one dashboard against Postgres. Whether the second waits for the
// first and then sees its revision is decided by the row lock Save takes, which
// sqlmock cannot show: without it, a save that starts while another is still
// committing passes the version check and overwrites it.
func TestSaveConflict_DB(t *testing.T) {
	base := os.Getenv("APP_DATABASE_URL")
	if base == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	admin, err := sqlx.Connect("postgres", base)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	schema := fmt.Sprintf("dashboard_conflict_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) })

	// Save runs on a pool, and these saves need a connection each, so the schema
	// rides on the connection string rather than a per-session SET.
	dsn, err := url.Parse(base)
	require.NoError(t, err)
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	for _, stmt := range []string{
		`CREATE TABLE users (id uuid PRIMARY KEY, username text, display_name text)`,
		`CREATE TABLE dashboards (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL, slug text NOT NULL,
			title text NOT NULL, description text, definition jsonb NOT NULL DEFAULT '{}',
			schema_version int NOT NULL DEFAULT 1, tags jsonb NOT NULL DEFAULT '[]', status text NOT NULL,
			is_builtin boolean NOT NULL DEFAULT false, created_by uuid, updated_by uuid,
			created_at timestamp NOT NULL DEFAULT now(), updated_at timestamp NOT NULL DEFAULT now())`,
		`CREATE TABLE dashboard_versions (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(), dashboard_id uuid NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
			version int NOT NULL, definition jsonb NOT NULL, message text, created_by uuid,
			created_at timestamp NOT NULL DEFAULT now(), UNIQUE (dashboard_id, version))`,
		`INSERT INTO users VALUES ('` + conflictUser + `', 'ada', 'Ada Lovelace'), ('` + otherUser + `', 'grace', NULL)`,
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err, stmt)
	}

	ada := conflictCtx(t)
	grace := userCtx(t, otherUser)
	panels := func(titles ...string) Definition {
		def := Definition{Panels: []Panel{}}
		for i, title := range titles {
			def.Panels = append(def.Panels, Panel{Id: i + 1, Title: title, Type: VizText, GridPos: GridPos{W: 6, H: 4}})
		}
		return def
	}

	created, err := Save(ada, SaveRequest{Title: "Checkout", Definition: panels("intro")})
	require.NoError(t, err)
	require.Equal(t, 1, created.Version)

	t.Run("a copy someone saved over is refused, and their save stands", func(t *testing.T) {
		adaCopy, err := Get(ada, GetRequest{Id: created.Id})
		require.NoError(t, err)
		graceCopy, err := Get(grace, GetRequest{Id: created.Id})
		require.NoError(t, err)

		saved, err := Save(ada, SaveRequest{Id: created.Id, Title: "Checkout", ExpectedVersion: adaCopy.Version, Definition: panels("intro", "latency")})
		require.NoError(t, err)

		_, err = Save(grace, SaveRequest{Id: created.Id, Title: "Checkout (grace)", ExpectedVersion: graceCopy.Version, Definition: graceCopy.Definition})
		assert.ErrorIs(t, err, ErrConflict)

		now, err := Get(grace, GetRequest{Id: created.Id})
		require.NoError(t, err)
		assert.Equal(t, saved.Version, now.Version)
		assert.Equal(t, "Checkout", now.Title)
		assert.Len(t, now.Definition.Panels, 2)
		assert.Equal(t, "Ada Lovelace", now.UpdatedByName)

		// Overwrite: the same copy, checked against the version just read.
		over, err := Save(grace, SaveRequest{Id: created.Id, Title: "Checkout (grace)", ExpectedVersion: now.Version, Definition: graceCopy.Definition})
		require.NoError(t, err)
		assert.Equal(t, now.Version+1, over.Version)
		// No display name falls back to the username.
		assert.Equal(t, "grace", over.UpdatedByName)
	})

	t.Run("a save that starts while another is committing waits, then is refused", func(t *testing.T) {
		current, err := Get(ada, GetRequest{Id: created.Id})
		require.NoError(t, err)

		// Another save, mid-transaction: the row is updated and its revision
		// written, but nothing is committed yet.
		inFlight, err := db.Beginx()
		require.NoError(t, err)
		_, err = inFlight.Exec(`UPDATE dashboards SET title = 'in flight' WHERE id = $1`, created.Id)
		require.NoError(t, err)
		_, err = inFlight.Exec(`INSERT INTO dashboard_versions (dashboard_id, version, definition) VALUES ($1, $2, '{}')`, created.Id, current.Version+1)
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() {
			_, err := Save(grace, SaveRequest{Id: created.Id, Title: "over the top", ExpectedVersion: current.Version, Definition: panels("intro")})
			done <- err
		}()
		// Long enough for Save to reach the dashboard row and block on it.
		time.Sleep(300 * time.Millisecond)
		require.NoError(t, inFlight.Commit())

		select {
		case err = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("save never returned")
		}
		assert.True(t, errors.Is(err, ErrConflict), "want ErrConflict, got %v", err)
		now, err := Get(ada, GetRequest{Id: created.Id})
		require.NoError(t, err)
		assert.Equal(t, "in flight", now.Title)
	})
}
