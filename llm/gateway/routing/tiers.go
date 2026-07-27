package routing

// Tier aliases are provider-agnostic model names any client can send in the `model`
// field (e.g. "nb-fast"); the gateway resolves each to a concrete provider/model.
// They ship as GLOBAL (tenant_id="") default rules so they work with zero tenant
// config — the whole point of a tier name is that any tool can just send it.
//
// match.provider is empty (any lane) so a bare "nb-fast" resolves on the generic /v1
// endpoint (which has no addressed provider) as well as on a native mount. The rules
// carry a high priority number so they evaluate LAST: a tenant DB rule or an operator
// config-file rule with the same match wins (tenant rules are also tenant-scoped, so
// they beat these globals regardless). A caller lacking a credential for a tier's
// target provider fails cleanly at the resolver stage (403), so an unusable tier
// degrades per-request rather than being silently dropped.
const (
	TierFast  = "nb-fast"  // low-latency, cheap — light/interactive work
	TierCheap = "nb-cheap" // cheapest — bulk/background work
	TierSmart = "nb-smart" // highest quality — hard reasoning
)

// defaultTierPriority orders the platform tier rules last so tenant/config rules win.
const defaultTierPriority = 1000

// TierDef is one platform tier alias and the model it resolves to by default. It is
// the single source of truth for the shipped defaults, shared by the routing engine
// (DefaultTierRules) and the admin tier-mapping surface (list/description).
type TierDef struct {
	Alias       string `json:"alias"`
	Description string `json:"description"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
}

// TierCatalog is the shipped tier set: mixed best-of-breed across providers (the
// gateway's value is routing each need to the best model). Tenants remap freely (an
// override rule wins). Kept in code, not a migration — these are release defaults.
func TierCatalog() []TierDef {
	return []TierDef{
		{TierFast, "Low-latency, cheap — light/interactive work", "gemini", "gemini-3.6-flash"},
		{TierCheap, "Cheapest — bulk/background work", "gemini", "gemini-3.5-flash-lite"},
		{TierSmart, "Highest quality — hard reasoning", "anthropic", "claude-opus-4-8"},
	}
}

// IsTierAlias reports whether a model name is a known platform tier alias.
func IsTierAlias(model string) bool {
	for _, t := range TierCatalog() {
		if t.Alias == model {
			return true
		}
	}
	return false
}

// DefaultTierRules turns the catalog into global (tenant_id="") default rules.
func DefaultTierRules() []Rule {
	cat := TierCatalog()
	rules := make([]Rule, 0, len(cat))
	for _, t := range cat {
		rules = append(rules, Rule{
			ID:       "nb-tier-" + t.Alias,
			Priority: defaultTierPriority,
			Enabled:  true,
			Match:    Match{Model: t.Alias}, // any provider lane
			Target:   Target{Endpoint: Endpoint{Provider: t.Provider, Model: t.Model}},
		})
	}
	return rules
}
