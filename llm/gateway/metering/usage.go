// Package metering captures one usage row per proxied request and writes it to a
// pluggable sink (Postgres or ClickHouse). Token/cache usage comes from Bifrost
// core's passthrough extraction (BifrostPassthroughUsage); identity comes from the
// auth middleware (task 3, empty until then); cost is computed on read by joining
// the pricing catalog, so only raw usage is stored here.
package metering

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
)

// Attributes is the extensible, structure-only metadata for a request, split into
// what the client/provider actually sent (immutable source truth) and NB's
// normalized/derived view. Stored as a JSON document so new attributes need no
// migration; the derived view can be re-computed/backfilled from actual later.
// Never carries message content or tool arguments (data custody).
type Attributes struct {
	Actual  map[string]any `json:"actual,omitempty"`
	Derived map[string]any `json:"derived,omitempty"`
}

// UsageEvent is one proxied request. db tags match the llm_gateway_usage columns.
type UsageEvent struct {
	ID        string    `db:"id"`
	CreatedAt time.Time `db:"created_at"`

	// Identity — populated by auth (task 3); empty until then.
	TenantID  string `db:"tenant_id"`
	AccountID string `db:"account_id"`
	UserID    string `db:"user_id"`
	TokenID   string `db:"token_id"`

	// SessionID groups requests into a round/conversation. Client-supplied only
	// (correlation header or body metadata.user_id); empty when absent.
	SessionID string `db:"session_id"`
	// Attributes is a JSON document ({"actual":…,"derived":…}); "{}" when none.
	Attributes string `db:"attributes"`

	// Request. Provider/Model are what actually RAN (resolved); Requested* are what
	// the client sent; RespondedModel is the id the PROVIDER echoed back (e.g. a dated
	// snapshot) — closes the audit loop + exposes silent snapshot/alias drift.
	// RoutingReason/Rule explain any change (see routing pkg).
	Provider          string `db:"provider"`
	Model             string `db:"model"`
	RequestedProvider string `db:"requested_provider"`
	RequestedModel    string `db:"requested_model"`
	RespondedModel    string `db:"responded_model"`
	RoutingReason     string `db:"routing_reason"`
	RoutingRule       string `db:"routing_rule"`
	Method            string `db:"method"`
	Path              string `db:"path"`
	Streaming         bool   `db:"streaming"`

	// Response.
	StatusCode int    `db:"status_code"`
	LatencyMS  int64  `db:"latency_ms"`
	RequestID  string `db:"request_id"`

	// Usage (raw). Zero when core extracted no billable usage.
	InputTokens      int    `db:"input_tokens"`
	OutputTokens     int    `db:"output_tokens"`
	TotalTokens      int    `db:"total_tokens"`
	CacheReadTokens  int    `db:"cache_read_tokens"`
	CacheWriteTokens int    `db:"cache_write_tokens"`
	ServiceTier      string `db:"service_tier"`

	// CostUsd is the cost SNAPSHOTTED at write time (tokens × the catalog price when
	// the call ran), so historical spend is immutable and a later price change never
	// retroactively rewrites it. Set by the handler from the pricer. Read paths sum
	// this column instead of re-joining the pricing catalog.
	CostUsd float64 `db:"cost_usd"`
}

// EventInput is what the edge knows at capture time, independent of core internals.
type EventInput struct {
	ID         string // stable request id (links to a body-log row); generated if empty
	Provider   schemas.ModelProvider
	Model      string
	Method     string
	Path       string
	Streaming  bool
	StatusCode int
	LatencyMS  int64
	Headers    map[string]string // provider response headers (for request-id)
	Usage      *schemas.BifrostPassthroughUsage
	SessionID  string
	Attributes *Attributes // structure-only; nil => stored as "{}"

	// Routing decision (resolved = Provider/Model above).
	RequestedProvider string
	RequestedModel    string
	RespondedModel    string // the model id the provider echoed in its response
	RoutingReason     string
	RoutingRule       string

	// Identity — resolved by auth; empty in auth mode "none".
	TenantID  string
	AccountID string
	UserID    string
	TokenID   string
}

// NewEvent builds a UsageEvent from what the edge observed plus core's extracted
// usage. Usage may be nil (errors, GET /models) — the row is still recorded for
// traffic/audit with zero tokens.
func NewEvent(in EventInput) UsageEvent {
	id := in.ID
	if id == "" {
		id = uuid.NewString()
	}
	ev := UsageEvent{
		ID:         id,
		CreatedAt:  time.Now().UTC(),
		Provider:   string(in.Provider),
		Model:      in.Model,
		Method:     in.Method,
		Path:       in.Path,
		Streaming:  in.Streaming,
		StatusCode: in.StatusCode,
		LatencyMS:  in.LatencyMS,
		RequestID:  requestIDFromHeaders(in.Headers),
		SessionID:  in.SessionID,
		Attributes: marshalAttributes(in.Attributes),
		TenantID:   in.TenantID,
		AccountID:  in.AccountID,
		UserID:     in.UserID,
		TokenID:    in.TokenID,

		RequestedProvider: in.RequestedProvider,
		RequestedModel:    in.RequestedModel,
		RespondedModel:    in.RespondedModel,
		RoutingReason:     in.RoutingReason,
		RoutingRule:       in.RoutingRule,
	}
	if u := in.Usage; u != nil {
		if u.ServiceTier != nil {
			ev.ServiceTier = string(*u.ServiceTier)
		}
		if l := u.LLMUsage; l != nil {
			ev.InputTokens = l.PromptTokens
			ev.OutputTokens = l.CompletionTokens
			ev.TotalTokens = l.TotalTokens
			if d := l.PromptTokensDetails; d != nil {
				ev.CacheReadTokens = d.CachedReadTokens
				ev.CacheWriteTokens = d.CachedWriteTokens
			}
		}
	}
	return ev
}

// marshalAttributes serializes attributes to a JSON document, defaulting to "{}"
// when nil or on error so the column is always valid JSON.
func marshalAttributes(a *Attributes) string {
	if a == nil {
		return "{}"
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// requestIDFromHeaders pulls the provider's request id (Anthropic: request-id,
// OpenAI: x-request-id) for cross-referencing with provider dashboards/support.
func requestIDFromHeaders(h map[string]string) string {
	for k, v := range h {
		switch strings.ToLower(k) {
		case "request-id", "x-request-id":
			return v
		}
	}
	return ""
}
