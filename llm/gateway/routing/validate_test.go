package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateForSave(t *testing.T) {
	ep := func(provider, model string) Endpoint { return Endpoint{Provider: provider, Model: model} }
	cases := []struct {
		name string
		rule Rule
		noOp bool
	}{
		// Rejected: the target changes nothing, whatever the rule matches.
		{"any to any", Rule{}, true},
		{"any to any, disabled", Rule{Enabled: false}, true},
		{"specific match, keep everything", Rule{Match: Match{Provider: "anthropic", Model: "claude-opus-4-8", UserID: "u1"}}, true},
		{"target provider equals match provider", Rule{Match: Match{Provider: "openai"}, Target: Target{Endpoint: ep("openai", "")}}, true},
		{"affinity only", Rule{Target: Target{Affinity: AffinityPrefixHash}}, true},
		{"weight only", Rule{Target: Target{Endpoint: Endpoint{Weight: 5}}}, true},
		{"empty fallback entry", Rule{Target: Target{Fallbacks: []Endpoint{{}}}}, true},

		// Accepted: every real action shape, including match-everything ones.
		{"match all, route to model", Rule{Target: Target{Endpoint: ep("", "gpt-5")}}, false},
		{"match all, route to provider+model", Rule{Target: Target{Endpoint: ep("gemini", "gemini-3.6-flash")}}, false},
		{"alias rewrite", Rule{Match: Match{Model: "nb-db-alias"}, Target: Target{Endpoint: ep("", "claude-haiku-4-5")}}, false},
		{"cross-provider substitution", Rule{Match: Match{Provider: "anthropic"}, Target: Target{Endpoint: ep("gemini", "gemini-3.6-flash")}}, false},
		{"lane switch with any match", Rule{Target: Target{Endpoint: ep("openai", "gpt-5")}}, false},
		{"block", Rule{Match: Match{Model: "claude-fable-5"}, Target: Target{Deny: true}}, false},
		{"block everything", Rule{Target: Target{Deny: true}}, false},
		{"deprecation shield", Rule{Match: Match{Model: "old"}, Target: Target{Deprecated: true, Endpoint: ep("", "new")}}, false},
		{"fallback only", Rule{Target: Target{Fallbacks: []Endpoint{ep("", "gpt-5-mini")}}}, false},
		{"key_ref only", Rule{Target: Target{Endpoint: Endpoint{KeyRef: "pool-b"}}}, false},
		{"affinity with a fallback", Rule{Target: Target{Affinity: AffinitySingle, Fallbacks: []Endpoint{ep("gemini", "gemini-3.6-flash")}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.rule.ID = "r1"
			err := ValidateForSave(tc.rule)
			if tc.noOp {
				assert.ErrorContains(t, err, "does nothing")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Stored rules and config files keep loading through Validate: the no-op check is write-path only.
func TestValidate_StillAcceptsNoOpOnLoad(t *testing.T) {
	assert.NoError(t, Validate([]Rule{{ID: "legacy"}}))
}

// ValidateForSave still enforces the base checks.
func TestValidateForSave_RunsBaseChecks(t *testing.T) {
	err := ValidateForSave(Rule{ID: "r1", Target: Target{Endpoint: Endpoint{Provider: "nope", Model: "x"}}})
	assert.ErrorContains(t, err, "unknown target.provider")
}
