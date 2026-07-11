package ownership

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// cloudMeta is a representative active cloud resource (reuses the rule()/ns()
// helpers from rules_test.go — same package).
func cloudMeta() cloudResourceMeta {
	return cloudResourceMeta{
		Account:     "acct1",
		Region:      "us-east-1",
		Type:        "ec2_instance",
		ServiceName: "AmazonEC2",
		Tags:        map[string]string{"team": "payments", "env": "prod"},
	}
}

func TestCloudRuleMatches(t *testing.T) {
	m := cloudMeta()
	const id = "r-1"
	tests := []struct {
		name string
		r    OwnershipRuleRow
		want bool
	}{
		{"tag key+value match", rule(MatchScopeCloudTag, "team", "payments", "", OwnerTypeUser, "u1"), true},
		{"tag value mismatch", rule(MatchScopeCloudTag, "team", "billing", "", OwnerTypeUser, "u1"), false},
		{"tag key absent", rule(MatchScopeCloudTag, "missing", "x", "", OwnerTypeUser, "u1"), false},
		{"type match", rule(MatchScopeCloudType, "", "ec2_instance", "", OwnerTypeUser, "u1"), true},
		{"type matches service_name as fallback", rule(MatchScopeCloudType, "", "AmazonEC2", "", OwnerTypeUser, "u1"), true},
		{"type mismatch", rule(MatchScopeCloudType, "", "s3_bucket", "", OwnerTypeUser, "u1"), false},
		{"region match", rule(MatchScopeCloudRegion, "", "us-east-1", "", OwnerTypeUser, "u1"), true},
		{"region mismatch", rule(MatchScopeCloudRegion, "", "us-west-2", "", OwnerTypeUser, "u1"), false},
		{"resource id in set", rule(MatchScopeCloudResource, "r-1,r-2", "", "", OwnerTypeUser, "u1"), true},
		{"resource id absent", rule(MatchScopeCloudResource, "r-9,r-8", "", "", OwnerTypeUser, "u1"), false},
		{"unknown scope", rule("bogus", "", "", "", OwnerTypeUser, "u1"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cloudRuleMatches(tt.r, m, id))
		})
	}
}

func TestEvalCloudRules_Specificity(t *testing.T) {
	m := cloudMeta()
	const id = "r-1"

	t.Run("resource beats tag beats type beats region", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeCloudRegion, "", "us-east-1", "", OwnerTypeUser, "region-owner"),
			rule(MatchScopeCloudType, "", "ec2_instance", "", OwnerTypeUser, "type-owner"),
			rule(MatchScopeCloudTag, "team", "payments", "", OwnerTypeUser, "tag-owner"),
			rule(MatchScopeCloudResource, "r-1", "", "acct1", OwnerTypeGroup, "res-owner"),
		}
		_, oid, ok := evalCloudRules(rules, m, id)
		assert.True(t, ok)
		assert.Equal(t, "res-owner", oid)
	})

	t.Run("tag beats type beats region", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeCloudRegion, "", "us-east-1", "", OwnerTypeUser, "region-owner"),
			rule(MatchScopeCloudType, "", "ec2_instance", "", OwnerTypeUser, "type-owner"),
			rule(MatchScopeCloudTag, "team", "payments", "", OwnerTypeUser, "tag-owner"),
		}
		_, oid, ok := evalCloudRules(rules, m, id)
		assert.True(t, ok)
		assert.Equal(t, "tag-owner", oid)
	})

	t.Run("only region matches", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeCloudTag, "nope", "x", "", OwnerTypeUser, "tag"),
			rule(MatchScopeCloudRegion, "", "us-east-1", "", OwnerTypeUser, "region-owner"),
		}
		_, oid, ok := evalCloudRules(rules, m, id)
		assert.True(t, ok)
		assert.Equal(t, "region-owner", oid)
	})

	t.Run("rule scoped to a different account is skipped", func(t *testing.T) {
		rules := []OwnershipRuleRow{rule(MatchScopeCloudTag, "team", "payments", "acct2", OwnerTypeUser, "other")}
		_, _, ok := evalCloudRules(rules, m, id)
		assert.False(t, ok)
	})

	t.Run("oldest wins within a scope", func(t *testing.T) {
		rules := []OwnershipRuleRow{
			rule(MatchScopeCloudTag, "team", "payments", "", OwnerTypeUser, "older"),
			rule(MatchScopeCloudTag, "env", "prod", "", OwnerTypeUser, "newer"),
		}
		_, oid, ok := evalCloudRules(rules, m, id)
		assert.True(t, ok)
		assert.Equal(t, "older", oid)
	})

	t.Run("no match", func(t *testing.T) {
		rules := []OwnershipRuleRow{rule(MatchScopeCloudRegion, "", "eu-west-1", "", OwnerTypeUser, "x")}
		_, _, ok := evalCloudRules(rules, m, id)
		assert.False(t, ok)
	})
}
