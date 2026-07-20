package memory

import (
	"context"
	"time"
)

// Memory is the public interface consumed by agents/, api/, and tests.
// Implementation details (stores, caching, ranking) are internal to this package.
// The interface is deliberately shaped for future extraction to a service boundary.
type Memory interface {
	// Compose builds the memory slab for a prompt. Hot path — called once per turn.
	Compose(ctx context.Context, req ComposeRequest) (MemorySlab, error)

	// Observe records what happened in a turn. Writes the event synchronously
	// (single DB insert) and returns; projection work runs async.
	Observe(ctx context.Context, req ObserveRequest) error

	// Get retrieves memory entries for inspection/management UI.
	Get(ctx context.Context, req GetRequest) (GetResponse, error)

	// Mutate applies an admin or user mutation (set preference, update soul, correct a fact).
	Mutate(ctx context.Context, req MutateRequest) (MutateResponse, error)

	// Erase removes all user-scoped memory for GDPR compliance.
	Erase(ctx context.Context, req EraseRequest) error

	// Export returns all user-scoped memory as a portable bundle.
	Export(ctx context.Context, req ExportRequest) (ExportResponse, error)
}

// ComposeRequest is the input to the read path.
type ComposeRequest struct {
	TenantID    string `json:"tenant_id"`
	UserID      string `json:"user_id"`
	AgentModule string `json:"agent_module"` // "observability" | "k8s_ops" | "cloud_ops" | "finops" | "automation" | "generic"
	SessionID   string `json:"session_id"`
	Query       string `json:"query"`        // user's latest message
	TokenBudget int    `json:"token_budget"` // max tokens the caller can afford
}

// MemorySlab is the structured output of Compose. Each field is a pre-rendered
// prompt block (with XML tags) or empty string if the layer is disabled/empty.
type MemorySlab struct {
	Soul        string       `json:"soul"`
	Preferences string       `json:"preferences"`
	Patterns    string       `json:"patterns"`   // Phase 2+
	Decisions   string       `json:"decisions"`  // Phase 2+
	Policy      string       `json:"policy"`     // Phase 3
	Account     string       `json:"account"`    // Phase 3
	Session     string       `json:"session"`    // Phase 4
	Heartbeat   string       `json:"heartbeat"`  // Phase 5
	Collective  string       `json:"collective"` // Phase 6
	Trace       ComposeTrace `json:"trace"`

	// Injected lists every memory row that actually made it into the prompt
	// blocks above, in the order they were rendered. Callers persist this to
	// llm_conversation_references so the UI can show "which memory rows the
	// agent saw on this turn" and the audit story (points 6 & 7 of the memory
	// data-audit baseline) has a concrete trail.
	Injected []InjectedItem `json:"injected,omitempty"`
}

// InjectedItem is a single memory row that Compose rendered into the slab.
// One per layer row — Soul and Session emit one; the RAG-ranked layers emit
// one per reranked survivor. Reference_id in the persisted row uses .ID.
type InjectedItem struct {
	Layer   string  `json:"layer"`             // "soul" | "preferences" | "patterns" | "decisions" | "collective" | "session"
	ID      string  `json:"id"`                // memory row identifier (UUID for row-keyed layers; composite for soul/session)
	Subject string  `json:"subject,omitempty"` // short display label (subject/key/session_id)
	Rank    int     `json:"rank"`              // 0-indexed position within the layer's rendered order
	Score   float32 `json:"score,omitempty"`   // rag_similarity or decayed_score depending on Source
	Source  string  `json:"source"`            // "rag" | "static"
	// Content is the rendered block the UI should show when the row is opened.
	// Populated for layers without a UUID PK (soul, session) — hydration SQL
	// can't JOIN them so their real text has to ride in the reference row's
	// metadata. Layers with a UUID PK (patterns / decisions / collective /
	// preferences) leave this empty and rely on the SQL JOIN to project their
	// content at read time, so we don't duplicate large row bodies.
	Content string `json:"content,omitempty"`
}

// Render concatenates all non-empty blocks into a single string for prompt injection.
func (s MemorySlab) Render() string {
	blocks := []string{
		s.Soul, s.Preferences, s.Patterns, s.Decisions,
		s.Policy, s.Account, s.Session, s.Heartbeat, s.Collective,
	}
	var result string
	for _, b := range blocks {
		if b != "" {
			if result != "" {
				result += "\n"
			}
			result += b
		}
	}
	return result
}

// ComposeTrace captures observability data from a Compose call.
type ComposeTrace struct {
	FlagsApplied  map[string]bool          `json:"flags_applied"`
	LayerLatency  map[string]time.Duration `json:"layer_latency"`
	TokenUsage    map[string]int           `json:"token_usage"`
	RankerVariant string                   `json:"ranker_variant,omitempty"`
}

// ObserveRequest is the input to the write path.
type ObserveRequest struct {
	TenantID       string         `json:"tenant_id"`
	UserID         string         `json:"user_id"`
	AgentModule    string         `json:"agent_module"`
	EventType      string         `json:"event_type"` // e.g. "soul.updated", "preference.set"
	Payload        map[string]any `json:"payload"`
	ActorKind      string         `json:"actor_kind"` // "user" | "system" | "agent" | "admin"
	ActorID        string         `json:"actor_id"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

// GetRequest scopes a read for the management API.
type GetRequest struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Layer    string `json:"layer"` // "soul" | "preferences" | "events"
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

// GetResponse returns entries from a specific layer.
type GetResponse struct {
	Layer   string `json:"layer"`
	Entries []any  `json:"entries"`
	Total   int    `json:"total"`
}

// MutateRequest describes a write to a specific layer.
type MutateRequest struct {
	TenantID  string         `json:"tenant_id"`
	UserID    string         `json:"user_id"`
	Layer     string         `json:"layer"`  // "soul" | "preferences"
	Action    string         `json:"action"` // "set" | "clear" | "delete"
	Key       string         `json:"key,omitempty"`
	Value     map[string]any `json:"value,omitempty"`
	ActorKind string         `json:"actor_kind"`
	ActorID   string         `json:"actor_id"`
}

// MutateResponse confirms the mutation.
type MutateResponse struct {
	Layer   string `json:"layer"`
	Action  string `json:"action"`
	Success bool   `json:"success"`
}

// EraseRequest identifies a user whose memory should be erased.
type EraseRequest struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
}

// ExportRequest identifies a user whose memory should be exported.
type ExportRequest struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Format   string `json:"format"` // "json"
}

// ExportResponse contains the portable memory bundle.
type ExportResponse struct {
	Format string `json:"format"`
	Data   []byte `json:"data"`
}
