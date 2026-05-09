package eventlog

import "time"

// Event represents a single memory event in the append-only log.
type Event struct {
	ID             string    `db:"id" json:"id"`
	TenantID       string    `db:"tenant_id" json:"tenant_id"`
	UserID         *string   `db:"user_id" json:"user_id,omitempty"`
	AgentModule    *string   `db:"agent_module" json:"agent_module,omitempty"`
	EventType      string    `db:"event_type" json:"event_type"`
	Payload        []byte    `db:"payload" json:"payload"`
	ActorKind      string    `db:"actor_kind" json:"actor_kind"`
	ActorID        *string   `db:"actor_id" json:"actor_id,omitempty"`
	IdempotencyKey *string   `db:"idempotency_key" json:"idempotency_key,omitempty"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
}

// Known event types for Phase 1.
const (
	EventTypeSoulUpdated       = "soul.updated"
	EventTypeSoulCleared       = "soul.cleared"
	EventTypePreferenceSet     = "preference.set"
	EventTypePreferenceCleared = "preference.cleared"
	EventTypeObservation       = "observation.recorded"
	// EventTypeFactExtracted is written when the legacy extractor hands a
	// fact to the Memory Module via BridgeFromLegacy. Projection routes it
	// to the typed store chosen by the classifier (patterns / decisions /
	// collective / preferences). Having this in the event log preserves the
	// audit / replay invariant.
	EventTypeFactExtracted = "fact.extracted"
)
