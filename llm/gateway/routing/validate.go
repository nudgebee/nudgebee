package routing

import "fmt"

// knownProviders are the provider families the gateway mounts. A rule's providers
// must be one of these (or empty = the addressed provider).
var knownProviders = map[string]bool{"anthropic": true, "openai": true, "gemini": true}

// Validate checks a rule set. A target (or fallback) provider may differ from the
// addressed/match provider — that is P2 cross-provider substitution, where the proxy
// translates the request into the target's schema and the response back to the
// client's shape. The only provider constraint is that a set provider is a known one.
func Validate(rules []Rule) error {
	seen := map[string]bool{}
	for i := range rules {
		r := rules[i]
		if r.ID == "" {
			return fmt.Errorf("routing rule #%d: id is required", i)
		}
		if seen[r.ID] {
			return fmt.Errorf("routing rule %q: duplicate id", r.ID)
		}
		seen[r.ID] = true

		if r.Match.Provider != "" && !knownProviders[r.Match.Provider] {
			return fmt.Errorf("routing rule %q: unknown match.provider %q", r.ID, r.Match.Provider)
		}
		if r.Target.Affinity != "" && r.Target.Affinity != AffinitySingle && r.Target.Affinity != AffinityPrefixHash {
			return fmt.Errorf("routing rule %q: invalid affinity %q (want %q or %q)", r.ID, r.Target.Affinity, AffinitySingle, AffinityPrefixHash)
		}

		if err := knownProviderOrEmpty(r.ID, "target", r.Target.Provider); err != nil {
			return err
		}
		for _, fb := range r.Target.Fallbacks {
			if err := knownProviderOrEmpty(r.ID, "fallback", fb.Provider); err != nil {
				return err
			}
		}

		// A cross-provider substitution needs an explicit target model: the client's
		// native model name won't exist on the target (e.g. claude-opus-4-8 on gemini),
		// so an empty model would forward a not-found id. Blocks don't route, so skip.
		if !r.Target.Deny && r.Target.Provider != "" && r.Target.Provider != r.Match.Provider && r.Target.Model == "" {
			return fmt.Errorf("routing rule %q: cross-provider target %q requires target.model (the requested model won't exist on the target provider)", r.ID, r.Target.Provider)
		}

		// A deprecation shield rewrites a retired model to a replacement — that
		// replacement model is required. A block (deny) doesn't route, so it's exempt.
		if !r.Target.Deny && r.Target.Deprecated && r.Target.Model == "" {
			return fmt.Errorf("routing rule %q: a deprecation needs target.model (the replacement to rewrite to)", r.ID)
		}
	}
	return nil
}

// knownProviderOrEmpty enforces that a target/fallback provider is empty (= keep the
// addressed provider) or one of the known providers.
func knownProviderOrEmpty(ruleID, kind, provider string) error {
	if provider == "" {
		return nil // keep the addressed provider
	}
	if !knownProviders[provider] {
		return fmt.Errorf("routing rule %q: unknown %s.provider %q", ruleID, kind, provider)
	}
	return nil
}
