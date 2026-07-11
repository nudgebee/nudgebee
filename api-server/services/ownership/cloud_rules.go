package ownership

// cloudScopeSpecificity is the cloud precedence order, most specific first: a rule
// pinning specific resources beats a tag rule, which beats a type rule, which
// beats a whole-region rule.
var cloudScopeSpecificity = []string{MatchScopeCloudResource, MatchScopeCloudTag, MatchScopeCloudType, MatchScopeCloudRegion}

// cloudResourceMeta is the minimal cloud_resourses shape the resolver needs.
type cloudResourceMeta struct {
	Account     string
	Region      string
	Type        string
	ServiceName string
	Tags        map[string]string
}

// evalCloudRules returns the owner of the matching cloud rule with the highest
// specificity. No priority; within a scope the first (oldest) match wins (rules
// are sorted oldest-first by loadEnabledCloudRules, and same-scope overlaps are
// blocked at write time). resourceId is the cloud_resourses.id, used by the
// cloud_resource scope. Pure: no DB, no side effects.
func evalCloudRules(rules []OwnershipRuleRow, m cloudResourceMeta, resourceId string) (ownerType, ownerId string, matched bool) {
	for _, scope := range cloudScopeSpecificity {
		for _, r := range rules {
			if r.MatchScope != scope {
				continue
			}
			// Account-scoped rule: skip if it targets a different cloud account.
			if r.CloudAccountId.Valid && r.CloudAccountId.String != "" && r.CloudAccountId.String != m.Account {
				continue
			}
			if cloudRuleMatches(r, m, resourceId) {
				return r.OwnerType, r.OwnerId, true
			}
		}
	}
	return "", "", false
}

// cloudRuleMatches reports whether a single (already account-scoped) cloud rule
// matches the resource, per its match scope. MatchValue is a bare string; MatchKey
// is a NullString.
func cloudRuleMatches(r OwnershipRuleRow, m cloudResourceMeta, resourceId string) bool {
	switch r.MatchScope {
	case MatchScopeCloudTag:
		return r.MatchKey.Valid && r.MatchKey.String != "" && m.Tags[r.MatchKey.String] == r.MatchValue
	case MatchScopeCloudType:
		// picker stores the type; service_name is matched as a safety net.
		return m.Type == r.MatchValue || m.ServiceName == r.MatchValue
	case MatchScopeCloudRegion:
		return m.Region == r.MatchValue
	case MatchScopeCloudResource:
		// match_key holds the comma-joined id set (uuids never contain commas).
		return r.MatchKey.Valid && nameInCSV(r.MatchKey.String, resourceId)
	}
	return false
}
