package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
)

// Secret values were stored in cloud_resourses.meta before the collector began
// redacting them (providers.RedactMetaSecrets). Live rows are rewritten by the
// next sync, but rows of deleted resources and of accounts that stopped syncing
// never are, and the knowledge graph keeps the Lambda environment variables it
// copied onto nodes that are no longer rebuilt. This backfill cleans both once,
// in small throttled batches, in every install (on-prem included), with the same
// redaction the live path uses.
//
// Each update is guarded by the value it read, so a sync or realtime write that
// lands in between wins and is never overwritten. Progress lives in
// data_backfill_runs: a restart resumes from the saved cursor (which only moves
// forward, should two replicas race), and a completed pass never reruns.
//
// The table's trigger stamps updated_at on every UPDATE and cannot be skipped
// without a table lock or superuser, so a redacted inactive row is deleted by the
// inactive-resource retention sweep (nb_retention_days_inactive_cloud_resources,
// 30 days by default) that much later, once.

const (
	metaRedactionBackfillName = "cloud_meta_redaction_v1"
	kgEnvBackfillName         = "kg_node_env_vars_cleanup_v1"
	backfillBatchSize         = 200
	backfillStartID           = "00000000-0000-0000-0000-000000000000"
)

type metaRow struct {
	Id   string `db:"id"`
	Meta string `db:"meta"`
}

type metaBackfillStore interface {
	Progress(name string) (lastID string, done bool, err error)
	SaveProgress(name, lastID string, done bool) error
	ResourceBatch(afterID string, limit int) ([]metaRow, error)
	UpdateResourceMeta(id, oldMeta, newMeta string) error
	InactiveKGNodesWithEnv(afterID string, limit int) ([]string, error)
	ClearKGNodeEnv(id string) error
}

// RunMetaRedactionBackfill runs the backfill until it completes, retrying with
// backoff on any error (data_backfill_runs is created by an api-server migration
// that may deploy after this collector). Returns when done or ctx is cancelled.
func RunMetaRedactionBackfill(ctx context.Context, logger *slog.Logger) {
	backoff := time.Minute
	for {
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err == nil {
			err = runMetaRedactionBackfill(ctx, &sqlMetaBackfillStore{dbms: dbms}, logger, 500*time.Millisecond)
		}
		if err == nil {
			return
		}
		logger.Warn("meta redaction backfill: will retry", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Minute)
	}
}

func runMetaRedactionBackfill(ctx context.Context, store metaBackfillStore, logger *slog.Logger, pause time.Duration) error {
	redacted := 0
	err := runBackfillPhase(ctx, store, metaRedactionBackfillName, pause, func(after string) (string, int, error) {
		rows, err := store.ResourceBatch(after, backfillBatchSize)
		if err != nil || len(rows) == 0 {
			return after, 0, err
		}
		for _, r := range rows {
			newMeta, changed, err := redactStoredMeta(r.Meta)
			if err != nil {
				logger.Warn("meta redaction backfill: unreadable meta, skipped", "id", r.Id, "error", err)
				continue
			}
			if !changed {
				continue
			}
			if err := store.UpdateResourceMeta(r.Id, r.Meta, newMeta); err != nil {
				return after, 0, err
			}
			redacted++
		}
		return rows[len(rows)-1].Id, len(rows), nil
	})
	if err != nil {
		return err
	}
	logger.Info("meta redaction backfill: cloud resources done", "redacted", redacted)

	cleared := 0
	err = runBackfillPhase(ctx, store, kgEnvBackfillName, pause, func(after string) (string, int, error) {
		ids, err := store.InactiveKGNodesWithEnv(after, backfillBatchSize)
		if err != nil || len(ids) == 0 {
			return after, 0, err
		}
		for _, id := range ids {
			if err := store.ClearKGNodeEnv(id); err != nil {
				return after, 0, err
			}
			cleared++
		}
		return ids[len(ids)-1], len(ids), nil
	})
	if err != nil {
		return err
	}
	logger.Info("meta redaction backfill: knowledge graph nodes done", "cleared", cleared)
	return nil
}

// runBackfillPhase walks one keyset-paginated pass, saving the cursor after each
// batch and marking the pass done when a batch comes back empty.
func runBackfillPhase(ctx context.Context, store metaBackfillStore, name string, pause time.Duration, batch func(after string) (string, int, error)) error {
	last, done, err := store.Progress(name)
	if err != nil {
		return fmt.Errorf("read progress of %s: %w", name, err)
	}
	if done {
		return nil
	}
	if last == "" {
		last = backfillStartID
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, n, err := batch(last)
		if err != nil {
			return fmt.Errorf("%s batch after %s: %w", name, last, err)
		}
		if n == 0 {
			return store.SaveProgress(name, last, true)
		}
		last = next
		if err := store.SaveProgress(name, last, false); err != nil {
			return fmt.Errorf("save progress of %s: %w", name, err)
		}
		if pause > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
		}
	}
}

// redactStoredMeta redacts a stored meta object and reports whether anything
// changed. Both sides are re-encoded the same way (numbers kept exact), so key
// order or number formatting never counts as a change.
func redactStoredMeta(stored string) (string, bool, error) {
	decode := func() (map[string]any, error) {
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader([]byte(stored)))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		return m, nil
	}
	original, err := decode()
	if err != nil || original == nil {
		return "", false, err
	}
	before, err := json.Marshal(original)
	if err != nil {
		return "", false, err
	}
	providers.RedactMetaSecrets(original)
	after, err := json.Marshal(original)
	if err != nil {
		return "", false, err
	}
	return string(after), !bytes.Equal(before, after), nil
}

type sqlMetaBackfillStore struct {
	dbms *common.DatabaseManager
}

func (s *sqlMetaBackfillStore) Progress(name string) (string, bool, error) {
	var rows []struct {
		LastID string `db:"last_id"`
		Done   bool   `db:"done"`
	}
	err := s.dbms.QueryAndScan(&rows, `SELECT coalesce(last_id, '') AS last_id, completed_at IS NOT NULL AS done FROM data_backfill_runs WHERE name = $1`, name)
	if err != nil || len(rows) == 0 {
		return "", false, err
	}
	return rows[0].LastID, rows[0].Done, nil
}

func (s *sqlMetaBackfillStore) SaveProgress(name, lastID string, done bool) error {
	_, err := s.dbms.Exec(`INSERT INTO data_backfill_runs (name, last_id, completed_at, updated_at)
		VALUES ($1, $2, CASE WHEN $3 THEN now() END, now())
		ON CONFLICT (name) DO UPDATE SET last_id = greatest(data_backfill_runs.last_id, EXCLUDED.last_id),
			completed_at = coalesce(data_backfill_runs.completed_at, EXCLUDED.completed_at),
			updated_at = now()`, name, lastID, done)
	return err
}

// The candidate filter names the top-level keys under which a redaction rule can
// find a secret; jsonb_exists_any avoids the `?|` operator, which the query
// helpers would read as a bind placeholder.
func (s *sqlMetaBackfillStore) ResourceBatch(afterID string, limit int) ([]metaRow, error) {
	var rows []metaRow
	err := s.dbms.QueryAndScan(&rows, `SELECT id::text AS id, meta::text AS meta FROM cloud_resourses
		WHERE id > $1::uuid
		  AND (jsonb_exists_any(meta, array['Environment','Overrides','ContainerDefinitions','DistributionConfig',
		        'VirtualInterfaces','AuthKey','BgpPeers','webhookReceivers','azureFunctionReceivers',
		        'logicAppReceivers','automationRunbookReceivers','metadata','master_auth','node_config','node_pools'])
		    OR jsonb_exists_any(meta->'properties', array['template','serviceKey','peerings','authorizations']))
		ORDER BY id LIMIT $2`, afterID, limit)
	return rows, err
}

func (s *sqlMetaBackfillStore) UpdateResourceMeta(id, oldMeta, newMeta string) error {
	_, err := s.dbms.Exec(`UPDATE cloud_resourses SET meta = $2::jsonb WHERE id = $1::uuid AND meta = $3::jsonb`, id, newMeta, oldMeta)
	return err
}

func (s *sqlMetaBackfillStore) InactiveKGNodesWithEnv(afterID string, limit int) ([]string, error) {
	var ids []string
	err := s.dbms.QueryAndScan(&ids, `SELECT id::text FROM knowledge_graph_node
		WHERE id > $1::uuid AND is_active = false AND jsonb_exists(properties, 'environment_variables')
		ORDER BY id LIMIT $2`, afterID, limit)
	return ids, err
}

func (s *sqlMetaBackfillStore) ClearKGNodeEnv(id string) error {
	_, err := s.dbms.Exec(`UPDATE knowledge_graph_node SET properties = properties - 'environment_variables' WHERE id = $1::uuid AND is_active = false`, id)
	return err
}
