// Package routing resolves an inbound request to a concrete target
// (provider/model/key + fallbacks) via first-match-by-priority rules, and records
// an explainable decision. P1 is static + faithful: it selects
// provider/deployment/key, ordered fallbacks, and same-family aliases/tiers only —
// cross-provider substitution (needs translation) is P2. See docs/routing.md.
package routing

// Reason explains why a request resolved to its target (captured for audit/trace).
type Reason string

const (
	ReasonPassthrough Reason = "passthrough"  // no rule changed the request
	ReasonAlias       Reason = "alias"        // logical model/alias → concrete model
	ReasonTier        Reason = "tier"         // NB tier hint → a model
	ReasonFallback    Reason = "fallback"     // primary failed, a fallback was used
	ReasonLoadBalance Reason = "load_balance" // selected from a weighted pool
	ReasonBlocked     Reason = "blocked"      // a deny rule rejected the request (403)
	ReasonSubstitute  Reason = "substitute"   // resolved to a DIFFERENT provider (P2 translation)
)

// Affinity controls cache-safety when a target is a pool. "single" keeps one target
// (provider prompt cache preserved for free). "prefix_hash" pins the same prompt
// prefix to the same key/deployment (preserves cache without needing session_id).
const (
	AffinitySingle     = "single"
	AffinityPrefixHash = "prefix_hash"
)

// Match is a rule's condition set. Empty fields mean "any".
type Match struct {
	Provider string `json:"provider,omitempty"` // addressed provider: anthropic|openai|gemini
	Model    string `json:"model,omitempty"`    // requested model OR an alias/tier token (e.g. nb-reasoning)
	UserID   string `json:"user_id,omitempty"`
}

// Endpoint is a concrete routing destination. Provider may differ from the
// addressed provider (P2 cross-provider substitution): the proxy then translates the
// request into the target's schema and the response back to the client's shape.
type Endpoint struct {
	Provider string `json:"provider,omitempty"` // resolved provider ("" = keep addressed)
	Model    string `json:"model,omitempty"`    // resolved model ("" = keep requested)
	KeyRef   string `json:"key_ref,omitempty"`  // named credential/pool ("" = tenant default)
	Weight   int    `json:"weight,omitempty"`   // weighted load-balance within a pool
}

// Target is what a matched rule routes to.
type Target struct {
	Endpoint             // primary destination
	Fallbacks []Endpoint `json:"fallbacks,omitempty"` // ordered; tried on failure only
	Affinity  string     `json:"affinity,omitempty"`  // AffinitySingle (default) | AffinityPrefixHash
	// Deny turns the rule into a BLOCK: a matching request is rejected (403) instead
	// of forwarded. Endpoint.Model, if set, is surfaced as a suggested alternative in
	// the error. A deny wins over a redirect rule at the same priority (see the engine
	// sort), so an admin can block a model regardless of other rules' ordering.
	Deny bool `json:"deny,omitempty"`
}

// Rule is one routing rule. Rules are ordered by (tenant-specific first, then
// Priority asc); the first whose Match matches wins.
type Rule struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id,omitempty"` // "" = global default rule
	Priority int    `json:"priority"`            // lower evaluated first
	Enabled  bool   `json:"enabled"`
	Match    Match  `json:"match"`
	Target   Target `json:"target"`
}

// Decision is the captured routing outcome for one request. Populated on EVERY
// request (Reason=passthrough when unchanged) and written to the usage row,
// attributes.derived.routing, and an OTel span.
type Decision struct {
	RequestedProvider string
	RequestedModel    string
	ResolvedProvider  string
	ResolvedModel     string
	RuleID            string // "" when passthrough
	Reason            Reason
	Fallbacks         []Endpoint // chain actually attempted, if any
	Strategy          string     // affinity / weighted / …
	// Denied is set when a block rule matched: the request must be rejected (403), not
	// forwarded. ResolvedModel then carries the suggested alternative (if the rule set one).
	Denied bool
}
