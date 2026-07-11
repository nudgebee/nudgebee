package ownership

import "strings"

// scopeSpecificity is the precedence order, most specific first: a rule pinning
// specific workloads beats a label rule, which beats a whole-namespace rule.
var scopeSpecificity = []string{MatchScopeWorkload, MatchScopeLabel, MatchScopeNamespace}

// evalRules returns the owner of the matching rule with the highest specificity.
// No priority: precedence is by scope (workload > label > namespace); within a
// scope the first match wins (rules are sorted oldest-first by loadEnabledRules,
// and same-scope overlaps are blocked at write time). Pure: no DB, no side effects.
func evalRules(rules []OwnershipRuleRow, w workloadMeta) (ownerType, ownerId string, matched bool) {
	for _, scope := range scopeSpecificity {
		for _, r := range rules {
			if r.MatchScope != scope {
				continue
			}
			// Account-scoped rule: skip if it targets a different cloud account.
			if r.CloudAccountId.Valid && r.CloudAccountId.String != "" && r.CloudAccountId.String != w.CloudAccountId {
				continue
			}
			if ruleMatches(r, w) {
				return r.OwnerType, r.OwnerId, true
			}
		}
	}
	return "", "", false
}

// workloadScopeRules returns the rules that own a workload directly (workload +
// label scope). Namespace-scope rules are excluded — they are evaluated at the
// namespace level so a manual namespace owner can take precedence over them.
func workloadScopeRules(rules []OwnershipRuleRow) []OwnershipRuleRow {
	var out []OwnershipRuleRow
	for _, r := range rules {
		if r.MatchScope != MatchScopeNamespace {
			out = append(out, r)
		}
	}
	return out
}

// ruleMatches reports whether a single (already account-scoped) rule matches the
// workload, per its match scope.
func ruleMatches(r OwnershipRuleRow, w workloadMeta) bool {
	switch r.MatchScope {
	case MatchScopeLabel:
		return r.MatchKey.Valid && r.MatchKey.String != "" && w.Labels[r.MatchKey.String] == r.MatchValue
	case MatchScopeNamespace:
		return w.Namespace == r.MatchValue
	case MatchScopeWorkload:
		// Pinned workloads: match by namespace + name. match_key holds the
		// comma-joined name set; account scope is enforced by the caller.
		return w.Namespace == r.MatchValue && r.MatchKey.Valid && nameInCSV(r.MatchKey.String, w.Name)
	}
	return false
}

// evalNamespaceRules returns the owner of the first namespace-scoped rule that
// matches the given namespace (honoring account scope). Used when resolving a
// namespace resource so a namespace rule shows up at the namespace level, not
// only on its workloads. `rules` MUST be pre-sorted in evaluation order.
func evalNamespaceRules(rules []OwnershipRuleRow, account, namespace string) (ownerType, ownerId string, matched bool) {
	for _, r := range rules {
		if r.CloudAccountId.Valid && r.CloudAccountId.String != "" && r.CloudAccountId.String != account {
			continue
		}
		if r.MatchScope == MatchScopeNamespace && r.MatchValue == namespace {
			return r.OwnerType, r.OwnerId, true
		}
	}
	return "", "", false
}

// nameInCSV reports whether name is one of the comma-separated entries in csv
// (whitespace around each entry is trimmed).
func nameInCSV(csv, name string) bool {
	for _, part := range strings.Split(csv, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}
