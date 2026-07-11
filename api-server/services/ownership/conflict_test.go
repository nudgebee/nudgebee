package ownership

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func upsertReq(scope, key, value, account string) UpsertRuleRequest {
	return UpsertRuleRequest{MatchScope: scope, MatchKey: key, MatchValue: value, CloudAccountId: account}
}

func TestAccountOverlap(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"", "", true},
		{"", "acct1", true},
		{"acct1", "", true},
		{"acct1", "acct1", true},
		{"acct1", "acct2", false},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, accountOverlap(tt.a, tt.b), "accountOverlap(%q,%q)", tt.a, tt.b)
	}
}

func TestSplitNames(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, splitNames("a, b ,c"))
	assert.Equal(t, []string{"a", "b"}, splitNames("a,,b"))
	assert.Empty(t, splitNames(""))
	assert.Empty(t, splitNames("  ,  "))
}

func TestSameScopeCandidates(t *testing.T) {
	mk := func(id, scope, account string) OwnershipRuleRow {
		r := rule(scope, "", "payments", account, OwnerTypeUser, "u")
		r.Id = id
		return r
	}
	disabled := mk("disabled", MatchScopeNamespace, "acct1")
	disabled.Enabled = false
	all := []OwnershipRuleRow{
		mk("a", MatchScopeNamespace, "acct1"),
		mk("b", MatchScopeNamespace, ""),      // all-accounts → overlaps acct1
		mk("c", MatchScopeNamespace, "acct2"), // different account → excluded
		mk("d", MatchScopeLabel, "acct1"),     // different scope → excluded
		disabled,                              // disabled → excluded (inert)
		mk("self", MatchScopeNamespace, "acct1"),
	}
	got := sameScopeCandidates(all, MatchScopeNamespace, "self", "acct1")

	ids := map[string]bool{}
	for _, r := range got {
		ids[r.Id] = true
	}
	assert.True(t, ids["a"], "same scope + same account")
	assert.True(t, ids["b"], "all-accounts overlaps")
	assert.False(t, ids["c"], "different account excluded")
	assert.False(t, ids["d"], "different scope excluded")
	assert.False(t, ids["disabled"], "disabled rule excluded")
	assert.False(t, ids["self"], "excludeId excluded")
}

func TestNamespaceConflict(t *testing.T) {
	candidates := []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "", OwnerTypeUser, "existing")}

	t.Run("same namespace conflicts", func(t *testing.T) {
		got := namespaceConflict(candidates, upsertReq(MatchScopeNamespace, "", "payments", ""))
		assert.NotNil(t, got)
		assert.Equal(t, "existing", got.OwnerId)
	})

	t.Run("different namespace does not conflict", func(t *testing.T) {
		got := namespaceConflict(candidates, upsertReq(MatchScopeNamespace, "", "billing", ""))
		assert.Nil(t, got)
	})
}

func TestWorkloadConflict(t *testing.T) {
	candidates := []OwnershipRuleRow{rule(MatchScopeWorkload, "api,web", "payments", "acct1", OwnerTypeUser, "existing")}

	t.Run("shared workload in same namespace conflicts", func(t *testing.T) {
		got := workloadConflict(candidates, upsertReq(MatchScopeWorkload, "web,worker", "payments", "acct1"))
		assert.NotNil(t, got)
		assert.Equal(t, "existing", got.OwnerId)
	})

	t.Run("disjoint workloads do not conflict", func(t *testing.T) {
		got := workloadConflict(candidates, upsertReq(MatchScopeWorkload, "db,cache", "payments", "acct1"))
		assert.Nil(t, got)
	})

	t.Run("same workload different namespace does not conflict", func(t *testing.T) {
		got := workloadConflict(candidates, upsertReq(MatchScopeWorkload, "api", "billing", "acct1"))
		assert.Nil(t, got)
	})
}

func TestLabelRulesOverlap(t *testing.T) {
	candidates := []OwnershipRuleRow{
		rule(MatchScopeLabel, "env", "prod", "", OwnerTypeUser, "existing"),
		{MatchScope: MatchScopeLabel, MatchValue: "x"}, // MatchKey invalid → skipped
	}

	t.Run("workload matching both rules conflicts", func(t *testing.T) {
		matched := []map[string]string{{"team": "payments", "env": "prod"}}
		got := labelRulesOverlap(candidates, matched)
		assert.NotNil(t, got)
		assert.Equal(t, "existing", got.OwnerId)
	})

	t.Run("no workload matches the existing rule", func(t *testing.T) {
		matched := []map[string]string{{"team": "payments", "env": "dev"}}
		got := labelRulesOverlap(candidates, matched)
		assert.Nil(t, got)
	})

	t.Run("no matched workloads", func(t *testing.T) {
		assert.Nil(t, labelRulesOverlap(candidates, nil))
	})
}

func TestCloudResourceConflict(t *testing.T) {
	candidates := []OwnershipRuleRow{rule(MatchScopeCloudResource, "r-1,r-2", "", "acct1", OwnerTypeUser, "existing")}

	t.Run("shared resource id conflicts", func(t *testing.T) {
		got := cloudResourceConflict(candidates, upsertReq(MatchScopeCloudResource, "r-2,r-3", "", "acct1"))
		assert.NotNil(t, got)
		assert.Equal(t, "existing", got.OwnerId)
	})

	t.Run("disjoint resource ids do not conflict", func(t *testing.T) {
		got := cloudResourceConflict(candidates, upsertReq(MatchScopeCloudResource, "r-8,r-9", "", "acct1"))
		assert.Nil(t, got)
	})
}

func TestCloudValueConflict_reusesNamespaceConflict(t *testing.T) {
	// cloud_region / cloud_type conflicts are exact match_value equality.
	candidates := []OwnershipRuleRow{rule(MatchScopeCloudRegion, "", "us-east-1", "", OwnerTypeUser, "existing")}
	assert.NotNil(t, namespaceConflict(candidates, upsertReq(MatchScopeCloudRegion, "", "us-east-1", "")))
	assert.Nil(t, namespaceConflict(candidates, upsertReq(MatchScopeCloudRegion, "", "us-west-2", "")))
}
