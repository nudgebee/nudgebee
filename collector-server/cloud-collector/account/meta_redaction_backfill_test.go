package account

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lambdaWithSecret = `{"Environment":{"Variables":{"DB_PASSWORD":"hunter2","QUEUE_URL":"https://sqs.us-east-1.amazonaws.com/123456789012/q"}},"nb_source":"api"}`

type fakeBackfillStore struct {
	rows      []metaRow
	kgNodes   []string
	progress  map[string]string
	done      map[string]bool
	updates   map[string]string
	cleared   []string
	failAfter string // ResourceBatch fails when asked to resume after this id
}

func newFakeStore() *fakeBackfillStore {
	return &fakeBackfillStore{progress: map[string]string{}, done: map[string]bool{}, updates: map[string]string{}}
}

func (f *fakeBackfillStore) Progress(name string) (string, bool, error) {
	return f.progress[name], f.done[name], nil
}

func (f *fakeBackfillStore) SaveProgress(name, lastID string, done bool) error {
	f.progress[name] = lastID
	f.done[name] = f.done[name] || done
	return nil
}

func (f *fakeBackfillStore) ResourceBatch(after string, limit int) ([]metaRow, error) {
	if f.failAfter != "" && after == f.failAfter {
		return nil, errors.New("connection reset")
	}
	var out []metaRow
	for _, r := range f.rows {
		if r.Id > after && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeBackfillStore) UpdateResourceMeta(id, _, newMeta string) error {
	f.updates[id] = newMeta
	return nil
}

func (f *fakeBackfillStore) InactiveKGNodesWithEnv(after string, limit int) ([]string, error) {
	var out []string
	for _, id := range f.kgNodes {
		if id > after && len(out) < limit {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *fakeBackfillStore) ClearKGNodeEnv(id string) error {
	f.cleared = append(f.cleared, id)
	return nil
}

func TestBackfillRedactsOnlyRowsThatHoldSecrets(t *testing.T) {
	store := newFakeStore()
	store.rows = []metaRow{
		{Id: "00000000-0000-0000-0000-000000000001", Meta: lambdaWithSecret},
		{Id: "00000000-0000-0000-0000-000000000002", Meta: `{"Environment":{"Variables":{"TOPIC_ARN":"arn:aws:sns:us-east-1:123456789012:t"}},"Size":9007199254740993}`},
	}
	store.kgNodes = []string{"00000000-0000-0000-0000-0000000000a1"}

	require.NoError(t, runMetaRedactionBackfill(context.Background(), store, slog.Default(), 0))

	require.Len(t, store.updates, 1, "a row with nothing to redact is not rewritten")
	updated := store.updates["00000000-0000-0000-0000-000000000001"]
	assert.NotContains(t, updated, "hunter2")
	assert.Contains(t, updated, "https://sqs.us-east-1.amazonaws.com/123456789012/q", "references are kept")
	assert.Equal(t, []string{"00000000-0000-0000-0000-0000000000a1"}, store.cleared)
	assert.True(t, store.done[metaRedactionBackfillName])
	assert.True(t, store.done[kgEnvBackfillName])
}

func TestBackfillResumesFromSavedCursorAndSkipsWhenDone(t *testing.T) {
	store := newFakeStore()
	store.rows = []metaRow{
		{Id: "00000000-0000-0000-0000-000000000001", Meta: lambdaWithSecret},
		{Id: "00000000-0000-0000-0000-000000000002", Meta: lambdaWithSecret},
	}
	store.progress[metaRedactionBackfillName] = "00000000-0000-0000-0000-000000000001"

	require.NoError(t, runMetaRedactionBackfill(context.Background(), store, slog.Default(), 0))
	assert.NotContains(t, store.updates, "00000000-0000-0000-0000-000000000001", "rows before the cursor are not revisited")
	assert.Contains(t, store.updates, "00000000-0000-0000-0000-000000000002")

	store.updates = map[string]string{}
	require.NoError(t, runMetaRedactionBackfill(context.Background(), store, slog.Default(), 0))
	assert.Empty(t, store.updates, "a completed pass never reruns")
}

func TestBackfillStopsOnErrorKeepingProgress(t *testing.T) {
	store := newFakeStore()
	for _, id := range []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"} {
		store.rows = append(store.rows, metaRow{Id: id, Meta: lambdaWithSecret})
	}
	// Fail on the second batch (batch size is larger than 2, so force it by
	// failing once the cursor has moved past the first row).
	store.failAfter = "00000000-0000-0000-0000-000000000002"

	err := runMetaRedactionBackfill(context.Background(), store, slog.Default(), 0)
	require.Error(t, err)
	assert.False(t, store.done[metaRedactionBackfillName])
	assert.Equal(t, "00000000-0000-0000-0000-000000000002", store.progress[metaRedactionBackfillName], "the next attempt resumes here")
}

// Runs the real SQL against a scratch Postgres database. Set
// NB_BACKFILL_TEST_PG_DSN to a database the test may create tables in.
func TestBackfillAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("NB_BACKFILL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("NB_BACKFILL_TEST_PG_DSN not set")
	}
	config.Config.CloudCollectorServerDBUrl = dsn
	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)

	migration, err := os.ReadFile("../../../api-server/migrations/migrations/app/1792647575430_V1053_data_backfill_runs.up.sql")
	require.NoError(t, err)
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS cloud_resourses, knowledge_graph_node, data_backfill_runs`,
		`CREATE TABLE cloud_resourses (id uuid PRIMARY KEY, meta jsonb)`,
		`CREATE TABLE knowledge_graph_node (id uuid PRIMARY KEY, properties jsonb NOT NULL, is_active boolean)`,
		string(migration),
		`INSERT INTO cloud_resourses VALUES
			('00000000-0000-0000-0000-000000000001', '` + lambdaWithSecret + `'),
			('00000000-0000-0000-0000-000000000002', '{"name":"bucket"}'),
			('00000000-0000-0000-0000-000000000003', '{"properties":{"serviceKey":"k3y"}}')`,
		`INSERT INTO knowledge_graph_node VALUES
			('00000000-0000-0000-0000-0000000000a1', '{"environment_variables":"{\"DB_PASSWORD\":\"hunter2\"}","name":"fn"}', false),
			('00000000-0000-0000-0000-0000000000a2', '{"environment_variables":"{}","name":"live"}', true)`,
	} {
		_, err := dbms.Exec(stmt)
		require.NoError(t, err, stmt)
	}

	require.NoError(t, runMetaRedactionBackfill(context.Background(), &sqlMetaBackfillStore{dbms: dbms}, slog.Default(), 0))

	var metas []metaRow
	require.NoError(t, dbms.QueryAndScan(&metas, `SELECT id::text AS id, meta::text AS meta FROM cloud_resourses ORDER BY id`))
	all, _ := json.Marshal(metas)
	assert.NotContains(t, string(all), "hunter2")
	assert.NotContains(t, string(all), "k3y")
	assert.Contains(t, string(all), "sqs.us-east-1.amazonaws.com")
	assert.Equal(t, `{"name": "bucket"}`, metas[1].Meta)

	var props []string
	require.NoError(t, dbms.QueryAndScan(&props, `SELECT properties::text FROM knowledge_graph_node ORDER BY id`))
	assert.False(t, strings.Contains(props[0], "environment_variables"), "inactive node cleared")
	assert.Contains(t, props[1], "environment_variables", "active node is left to the next graph build")

	var done []bool
	require.NoError(t, dbms.QueryAndScan(&done, `SELECT completed_at IS NOT NULL FROM data_backfill_runs ORDER BY name`))
	assert.Equal(t, []bool{true, true}, done)
}
