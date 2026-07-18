package routing

import "sort"

// Input is what the edge knows about a request when routing is resolved.
type Input struct {
	Provider string // addressed provider (anthropic|openai|gemini) — the "lane"
	Model    string // requested model or an alias/tier token
	TenantID string
	UserID   string
}

// Engine holds the compiled rule set and resolves requests to decisions.
// Rules are grouped by tenant (each list sorted by priority asc); global rules
// (TenantID=="") are evaluated after tenant-specific ones.
type Engine struct {
	byTenant map[string][]Rule
	global   []Rule
}

// NewEngine compiles rules into an Engine. Rules must already be validated
// (see Validate); NewEngine only groups and sorts them.
func NewEngine(rules []Rule) *Engine {
	e := &Engine{byTenant: map[string][]Rule{}}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if r.TenantID == "" {
			e.global = append(e.global, r)
		} else {
			e.byTenant[r.TenantID] = append(e.byTenant[r.TenantID], r)
		}
	}
	// Order within a scope: priority asc; then a deny (block) wins a same-priority tie
	// (an admin can block a model regardless of a competing redirect). For every other
	// tie the STABLE sort preserves input order — the Store depends on this (DB rules
	// are merged before config-file rules, so DB wins ties), and the DB loader's
	// ORDER BY makes that input order itself deterministic (no coin-flip on a tie).
	byPriority := func(rs []Rule) {
		sort.SliceStable(rs, func(i, j int) bool {
			if rs[i].Priority != rs[j].Priority {
				return rs[i].Priority < rs[j].Priority
			}
			return rs[i].Target.Deny && !rs[j].Target.Deny // deny first; else keep input order
		})
	}
	byPriority(e.global)
	for _, rs := range e.byTenant {
		byPriority(rs)
	}
	return e
}

// Resolve returns the routing decision for a request: the first matching rule
// (tenant-specific before global, then priority), or passthrough when none match.
// P1 is endpoint-scoped: the resolved provider is always the addressed provider.
func (e *Engine) Resolve(in Input) Decision {
	d := Decision{
		RequestedProvider: in.Provider,
		RequestedModel:    in.Model,
		ResolvedProvider:  in.Provider,
		ResolvedModel:     in.Model,
		Reason:            ReasonPassthrough,
	}
	if e == nil {
		return d
	}
	for _, r := range e.candidates(in.TenantID) {
		if !r.Match.matches(in) {
			continue
		}
		d.RuleID = r.ID
		if r.Target.Deny {
			// Block: reject the request. ResolvedModel carries the suggested
			// alternative (empty when the rule sets none). The proxy turns this into a 403.
			d.Reason = ReasonBlocked
			d.Denied = true
			d.ResolvedModel = r.Target.Model
			return d
		}
		if r.Target.Model != "" {
			d.ResolvedModel = r.Target.Model
		}
		// P2: a rule may resolve a DIFFERENT provider (cross-provider substitution).
		// The proxy then takes the translate path (parse client-native → unified →
		// dispatch to the resolved provider → re-encode to the client's native shape).
		if r.Target.Provider != "" {
			d.ResolvedProvider = r.Target.Provider
		}
		d.Fallbacks = r.Target.Fallbacks
		d.Strategy = r.Target.Affinity
		switch {
		case d.ResolvedProvider != d.RequestedProvider:
			d.Reason = ReasonSubstitute // cross-provider — needs translation
		case d.ResolvedModel != d.RequestedModel:
			d.Reason = ReasonAlias // tier is a labelled alias; rule id disambiguates
		default:
			d.Reason = ReasonLoadBalance // same model, but a rule selected a key/pool/fallback set
		}
		return d
	}
	return d
}

// candidates returns tenant rules followed by global rules (both priority-sorted).
func (e *Engine) candidates(tenantID string) []Rule {
	tenant := e.byTenant[tenantID]
	if len(tenant) == 0 {
		return e.global
	}
	out := make([]Rule, 0, len(tenant)+len(e.global))
	out = append(out, tenant...)
	out = append(out, e.global...)
	return out
}

// matches reports whether the request satisfies the rule's conditions. Empty
// fields are wildcards.
func (m Match) matches(in Input) bool {
	if m.Provider != "" && m.Provider != in.Provider {
		return false
	}
	if m.Model != "" && m.Model != in.Model {
		return false
	}
	if m.UserID != "" && m.UserID != in.UserID {
		return false
	}
	return true
}
