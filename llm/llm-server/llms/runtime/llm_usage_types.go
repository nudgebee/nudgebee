package runtime

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/security"
)

type TierModelPick struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// ConfigSource is the credential this task resolves through. Empty for
	// picks made before per-tier credentials existed; those fall back to the
	// conversation-wide pin.
	ConfigSource string `json:"llm_config_source,omitempty"`
}

// Typed view of the llm_conversations.llm_tier_overrides jsonb column.
// Map keys are ModelTier names; empty Picks → SQL NULL via Value below.
type ConversationTierOverrides struct {
	Picks map[string]TierModelPick `json:"picks,omitempty"`
}

// usableTierPick reports whether a pick carries an instruction the resolver can
// act on: a config source (the slot supplies provider and model), or a complete
// provider+model pair. Requiring provider AND model made source-only picks —
// "use this config's own model" — invisible here, so they were neither persisted
// nor returned, and the tier silently fell back to a lower-precedence layer.
func usableTierPick(p TierModelPick) bool {
	return p.ConfigSource != "" || (p.Provider != "" && p.Model != "")
}

func (c *ConversationTierOverrides) HasAny() bool {
	if c == nil {
		return false
	}
	for _, p := range c.Picks {
		if usableTierPick(p) {
			return true
		}
	}
	return false
}

// Half-set picks (a provider or model alone, with no config source) carry no
// usable instruction and fall through to lower precedence layers — never
// returned as present.
func (c *ConversationTierOverrides) Get(tier string) (TierModelPick, bool) {
	if c == nil {
		return TierModelPick{}, false
	}
	p, ok := c.Picks[tier]
	if !ok || !usableTierPick(p) {
		return TierModelPick{}, false
	}
	return p, true
}

func (c ConversationTierOverrides) Value() (driver.Value, error) {
	if len(c.Picks) == 0 {
		return nil, nil
	}
	return json.Marshal(c)
}

func (c *ConversationTierOverrides) Scan(src any) error {
	if src == nil {
		c.Picks = nil
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("ConversationTierOverrides.Scan: unsupported source type %T", src)
	}
	if len(raw) == 0 {
		c.Picks = nil
		return nil
	}
	return json.Unmarshal(raw, c)
}

// TokenUsageRecord represents a single LLM API call token usage
type TokenUsageRecord struct {
	ID                  string // Optional explicit ID for a single-call audit; other callers use the DB default.
	ConversationID      string
	MessageID           string
	AgentID             *string // Nullable
	AgentName           string
	AccountID           string
	UserID              string // Nullable
	LLMProvider         string
	LLMModel            string
	InputTokens         int
	OutputTokens        int
	CachedInputTokens   int
	CacheCreationTokens int
	// CostUsd is the USD cost of this call, computed via GetConversationCost at
	// insert time. Nil when the (provider, model) has no llm_model_pricing entry —
	// distinguishes "not priced" from "$0" for readers, which fall back to a live pricing JOIN when nil.
	CostUsd           *float64
	IsCacheHit        bool
	CacheHitRate      *float64 // Nullable
	RetryAttempt      int
	FallbackFromModel *string  // Nullable - immediate previous model
	FallbackChain     *string  // Nullable - JSON array of all fallback models
	LatencySeconds    *float64 // Nullable
	RequestStatus     string   // 'success' or 'failure'
	ErrorMessage      *string  // Nullable
	ContentLength     *int     // Nullable
	StopReason        *string  // Nullable
	CacheTTLMinutes   *int     // Nullable - cache TTL in minutes at time of request
	PromptMessages    *string  // Nullable - serialized prompt messages JSON (when LLM trace is enabled)
	ResponseContent   *string  // Nullable - full LLM response text (when LLM trace is enabled)
	ThinkingTokens    *int     // Nullable - Gemini 2.5+ hidden chain-of-thought tokens (usage.ThoughtsTokenCount). NULL when 0/unavailable.
	// Streaming-latency breakdown. ttft_ms / itl_ms_avg / tokens_per_second
	// are NULL on non-streaming or legacy rows. was_streaming is FALSE on new
	// non-streaming rows and NULL only on legacy rows pre-V722.
	TTFTMs          *int64   // Time-to-first-token in ms.
	ITLMsAvg        *float64 // Average inter-token latency in ms ((latency_ms - ttft_ms) / output_tokens). float to preserve sub-ms precision on fast models.
	TokensPerSecond *float64 // Steady-state output throughput.
	WasStreaming    *bool    // Whether the call streamed (≥1 chunk observed).
	// Configured LLM slot this call resolved through (V887). NULL on legacy rows.
	LLMConfigSource *string
	// Tier-experiment attribution (V836). Both NULL on legacy rows.
	ModelTier *string // Resolved ModelTier this call ran on (reasoning/retrieval/summary). NULL when unstamped.
	TaskType  *string // Top-level turn classification (query/investigation). NULL for sub-agent calls.
}

// tenantForPricing resolves the tenant whose price overrides apply to an
// account's usage. Pricing is tenant-scoped (V843): a tenant's own row for a
// (model, provider) beats the built-in one.
//
// A failure degrades to "" — built-in pricing only — rather than failing the
// read: a cost display that falls back to list prices is far better than a
// conversation view that will not load.
// PricingTenantFromContext is the request-context form of tenantForPricing:
// the tenant is already on the security context, so no lookup is needed.
func PricingTenantFromContext(ctx *security.RequestContext) string {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return ""
	}
	return ctx.GetSecurityContext().GetTenantId()
}

func TenantForPricing(accountId string) string {
	if accountId == "" {
		return ""
	}
	tenantId, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil {
		slog.Warn("pricing: could not resolve tenant for account, using built-in pricing",
			"error", err, "account_id", accountId)
		return ""
	}
	return tenantId
}

// ModelTierDecision is the configuration slot of the decision model. It is not
// a model tier: no chat call resolves through it and it cannot be pinned.
const ModelTierDecision ModelTier = "decision"
