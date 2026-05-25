package eventlog

import (
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
		evt.CreatedAt = time.Now()
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
	_, err = db.Db.Exec(query,
		evt.TenantID, evt.UserID, evt.AgentModule, evt.EventType, evt.Payload,
		evt.ActorKind, evt.ActorID, evt.IdempotencyKey, evt.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("eventlog.Append: insert: %w", err)
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
