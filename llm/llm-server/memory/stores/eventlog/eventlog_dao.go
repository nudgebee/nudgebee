package eventlog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"time"
)

// Append inserts an event into the log. This is the synchronous write on the
// Observe hot path — a single Postgres INSERT, typically sub-millisecond.
// If an idempotency key is provided, a best-effort pre-check skips the insert
// when a prior event with the same (tenant_id, idempotency_key) exists.
// Postgres forbids unique constraints on partitioned tables that don't include
// the partition column, so we can't use ON CONFLICT here; a rare race could
// land two rows with the same idempotency key, but that's harmless because
// projections (soul / prefs upserts) are themselves idempotent.
func Append(evt Event) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("eventlog.Append: db: %w", err)
	}

	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now().UTC()
	}

	if evt.IdempotencyKey != nil && *evt.IdempotencyKey != "" {
		var exists bool
		if err := db.Db.Get(&exists, `
			SELECT EXISTS(
				SELECT 1 FROM llm_memory_events
				WHERE tenant_id = $1 AND idempotency_key = $2
				LIMIT 1
			)
		`, evt.TenantID, *evt.IdempotencyKey); err != nil {
			return fmt.Errorf("eventlog.Append: idem check: %w", err)
		}
		if exists {
			return nil
		}
	}

	query := `
		INSERT INTO llm_memory_events
			(tenant_id, user_id, agent_module, event_type, payload,
			 actor_kind, actor_id, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	args := []any{
		evt.TenantID, evt.UserID, evt.AgentModule, evt.EventType, evt.Payload,
		evt.ActorKind, evt.ActorID, evt.IdempotencyKey, evt.CreatedAt,
	}
	_, err = db.Db.Exec(query, args...)
	if err != nil {
		if !isPgCode(err, pgCheckViolation) {
			return fmt.Errorf("eventlog.Append: insert: %w", err)
		}
		// No partition covers this row's month. The scheduled
		// memory_partition_maint run should have created it, but a tenant
		// enabled at runtime (feature_flag, 30s cache TTL) can start writing
		// without any restart, so boot-time provisioning alone can't cover
		// every case. Create it now and retry once rather than dropping the
		// write.
		slog.Warn("eventlog.Append: no partition for row, provisioning now", "created_at", evt.CreatedAt)
		if healErr := provisionAndRetry(db, evt.CreatedAt, query, args); healErr != nil {
			return fmt.Errorf("eventlog.Append: insert: %w (%v)", err, healErr)
		}
	}
	return nil
}

// Scan reads events for a tenant+user within a time range. Used by projections
// and the replay path, not the hot path.
func Scan(tenantID, userID string, since time.Time, limit int) ([]Event, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("eventlog.Scan: db: %w", err)
	}
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT id, tenant_id, user_id, agent_module, event_type, payload,
		       actor_kind, actor_id, idempotency_key, created_at
		FROM llm_memory_events
		WHERE tenant_id = $1 AND user_id = $2 AND created_at >= $3
		ORDER BY created_at ASC
		LIMIT $4
	`
	var events []Event
	err = db.Db.Select(&events, query, tenantID, userID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("eventlog.Scan: select: %w", err)
	}
	return events, nil
}

// MarshalPayload is a helper to convert a map to JSON bytes for the Payload field.
func MarshalPayload(data map[string]any) []byte {
	b, err := json.Marshal(data)
	if err != nil {
		slog.Error("eventlog.MarshalPayload: marshal failed", "error", err)
		return []byte("{}")
	}
	return b
}

// UnmarshalPayload parses the stored JSON payload back into a map for
// projection handlers. Returns an empty map on nil / malformed input.
func UnmarshalPayload(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// provisionAndRetry creates the partition the rejected row needs and replays
// the insert once. Split out of Append so the timeout's cancel can be deferred
// at function scope: a defer inside Append's error branch would only fire when
// Append itself returns, which is correct today but silently wrong the moment
// anyone adds work after the branch.
//
// One context spans both steps, so a slow provision leaves the retry less time
// rather than doubling the ceiling.
func provisionAndRetry(db *common.DatabaseManager, createdAt time.Time, query string, args []any) error {
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	defer cancel()

	if err := EnsurePartitionForTime(ctx, createdAt); err != nil {
		return fmt.Errorf("partition provisioning failed: %w", err)
	}
	if _, err := db.Db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert after provisioning partition: %w", err)
	}
	return nil
}
