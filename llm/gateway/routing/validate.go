package routing

import "fmt"

// knownProviders are the provider families the gateway mounts. A rule's providers
// must be one of these (or empty = the addressed provider).
var knownProviders = map[string]bool{"anthropic": true, "openai": true, "gemini": true}

// Validate checks a rule set for P1 correctness. The key invariant is
// endpoint-scoping: a rule's target (and every fallback) must stay in the same
// provider family as the match — cross-provider routing needs translation (P2).
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

		// Endpoint-scoping (P1): target/fallback provider must match the family.
		fam := r.Match.Provider
		if err := sameFamily(r.ID, "target", r.Target.Provider, fam); err != nil {
			return err
		}
		for _, fb := range r.Target.Fallbacks {
			if err := sameFamily(r.ID, "fallback", fb.Provider, fam); err != nil {
				return err
			}
		}
	}
	return nil
}

// sameFamily enforces that a target/fallback provider is empty (= addressed) or
// equal to the match provider. When the match provider is "" (any endpoint), a
// non-empty target provider would pin one family across all endpoints — also
// disallowed in P1 (it can't be cross-family-safe for every lane).
func sameFamily(ruleID, kind, targetProvider, matchProvider string) error {
	if targetProvider == "" {
		return nil // keep the addressed provider — always faithful
	}
	if !knownProviders[targetProvider] {
		return fmt.Errorf("routing rule %q: unknown %s.provider %q", ruleID, kind, targetProvider)
	}
	if matchProvider == "" {
		return fmt.Errorf("routing rule %q: %s.provider %q set but match.provider is any — pin match.provider to the same family (cross-provider routing is P2)", ruleID, kind, targetProvider)
	}
	if targetProvider != matchProvider {
		return fmt.Errorf("routing rule %q: cross-provider routing %s→%s requires translation (P2); P1 %s.provider must equal match.provider or be empty", ruleID, matchProvider, targetProvider, kind)
	}
	return nil
}
