package ownership

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ns builds a sql.NullString that is Valid only when non-empty (mirrors how the
// store loads optional rule columns).
func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// rule builds an OwnershipRuleRow. Per scope: label → key=label key, value=label
// value; namespace → value=namespace (key empty); workload → key=comma-joined
// names, value=namespace. account "" means "all accounts".
func rule(scope, key, value, account, ownerType, ownerID string) OwnershipRuleRow {
	return OwnershipRuleRow{
		MatchScope:     scope,
		MatchKey:       ns(key),
		MatchValue:     value,
		CloudAccountId: ns(account),
		OwnerType:      ownerType,
		OwnerId:        ownerID,
		Enabled:        true,
	}
}

func TestNameInCSV(t *testing.T) {
	tests := []struct {
		name string
		csv  string
		nm   string
		want bool
	}{
		{"present", "a,b,c", "b", true},
		{"whitespace trimmed", "a, b , c", "b", true},
		{"absent", "a,b,c", "d", false},
		{"empty csv", "", "a", false},
		{"single match", "only", "only", true},
		{"prefix is not a match", "team-a", "team", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nameInCSV(tt.csv, tt.nm))
		})
	}
}

func TestRuleMatches(t *testing.T) {
	wm := workloadMeta{
		CloudAccountId: "acct1",
		Namespace:      "payments",
		Name:           "api",
		Labels:         map[string]string{"team": "payments", "env": "prod"},
	}
	tests := []struct {
		name string
		r    OwnershipRuleRow
		want bool
	}{
		{"label key+value match", rule(MatchScopeLabel, "team", "payments", "", OwnerTypeUser, "u1"), true},
		{"label value mismatch", rule(MatchScopeLabel, "team", "billing", "", OwnerTypeUser, "u1"), false},
		{"label key absent", rule(MatchScopeLabel, "missing", "x", "", OwnerTypeUser, "u1"), false},
		{"label empty key", rule(MatchScopeLabel, "", "payments", "", OwnerTypeUser, "u1"), false},
		{"namespace match", rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "u1"), true},
		{"namespace mismatch", rule(MatchScopeNamespace, "", "billing", "", OwnerTypeUser, "u1"), false},
		{"workload name in set + namespace", rule(MatchScopeWorkload, "api,web", "payments", "", OwnerTypeUser, "u1"), true},
		{"workload name absent", rule(MatchScopeWorkload, "web,worker", "payments", "", OwnerTypeUser, "u1"), false},
		{"workload right name wrong namespace", rule(MatchScopeWorkload, "api", "billing", "", OwnerTypeUser, "u1"), false},
		{"unknown scope", rule("bogus", "", "", "", OwnerTypeUser, "u1"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ruleMatches(tt.r, wm))
		})
	}
}

func TestEvalRules_Specificity(t *testing.T) {
	wm := workloadMeta{
		CloudAccountId: "acct1",
		Namespace:      "payments",
		Name:           "api",
		Labels:         map[string]string{"team": "payments"},
	}

	t.Run("workload beats label and namespace", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "ns-owner"),
			rule(MatchScopeLabel, "team", "payments", "", OwnerTypeUser, "label-owner"),
			rule(MatchScopeWorkload, "api", "payments", "acct1", OwnerTypeGroup, "wl-owner"),
		}
		ot, oid, ok := evalRules(rules, wm)
		assert.True(t, ok)
		assert.Equal(t, OwnerTypeGroup, ot)
		assert.Equal(t, "wl-owner", oid)
	})

	t.Run("label beats namespace", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "ns-owner"),
			rule(MatchScopeLabel, "team", "payments", "", OwnerTypeUser, "label-owner"),
		}
		_, oid, ok := evalRules(rules, wm)
		assert.True(t, ok)
		assert.Equal(t, "label-owner", oid)
	})

	t.Run("only namespace matches", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeLabel, "nope", "x", "", OwnerTypeUser, "label-owner"),
			rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "ns-owner"),
		}
		_, oid, ok := evalRules(rules, wm)
		assert.True(t, ok)
		assert.Equal(t, "ns-owner", oid)
	})

	t.Run("rule scoped to a different account is skipped", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeLabel, "team", "payments", "acct2", OwnerTypeUser, "other-acct"),
		}
		_, _, ok := evalRules(rules, wm)
		assert.False(t, ok)
	})

	t.Run("account-scoped rule matches its own account", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeLabel, "team", "payments", "acct1", OwnerTypeUser, "same-acct"),
		}
		_, oid, ok := evalRules(rules, wm)
		assert.True(t, ok)
		assert.Equal(t, "same-acct", oid)
	})

	t.Run("oldest wins within a scope (slice is oldest-first)", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "older"),
			rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "newer"),
		}
		_, oid, ok := evalRules(rules, wm)
		assert.True(t, ok)
		assert.Equal(t, "older", oid)
	})

	t.Run("no match", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeNamespace, "", "other-ns", "", OwnerTypeUser, "x"),
			rule(MatchScopeLabel, "team", "billing", "", OwnerTypeUser, "y"),
		}
		_, _, ok := evalRules(rules, wm)
		assert.False(t, ok)
	})

	t.Run("empty rules", func(t *testing.T) {
		_, _, ok := evalRules(nil, wm)
		assert.False(t, ok)
	})
}

func TestEvalNamespaceRules(t *testing.T) {
	t.Run("matches namespace rule for value + account", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeLabel, "team", "payments", "", OwnerTypeUser, "label"),
			rule(MatchScopeNamespace, "", "payments", "acct1", OwnerTypeGroup, "ns-owner"),
		}
		ot, oid, ok := evalNamespaceRules(rules, "acct1", "payments")
		assert.True(t, ok)
		assert.Equal(t, OwnerTypeGroup, ot)
		assert.Equal(t, "ns-owner", oid)
	})

	t.Run("all-accounts rule matches any account", func(t *testing.T) {
		rules := []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "ns-owner")}
		_, oid, ok := evalNamespaceRules(rules, "acctX", "payments")
		assert.True(t, ok)
		assert.Equal(t, "ns-owner", oid)
	})

	t.Run("account-scoped rule skipped for other account", func(t *testing.T) {
		rules := []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "acct1", OwnerTypeUser, "ns-owner")}
		_, _, ok := evalNamespaceRules(rules, "acct2", "payments")
		assert.False(t, ok)
	})

	t.Run("ignores non-namespace scopes", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeLabel, "team", "payments", "", OwnerTypeUser, "label"),
			rule(MatchScopeWorkload, "api", "payments", "acct1", OwnerTypeUser, "wl"),
		}
		_, _, ok := evalNamespaceRules(rules, "acct1", "payments")
		assert.False(t, ok)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		rules := []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "ns-owner")}
		_, _, ok := evalNamespaceRules(rules, "acct1", "billing")
		assert.False(t, ok)
	})
}
