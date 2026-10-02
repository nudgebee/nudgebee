//go:build e2e

package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassifyE2E runs Classify through the production LLM path
// (GenerateAndTrackLLMContent: account config resolution, tier routing, thinking
// clamp, token tracking) for TEST_ACCOUNT, once per tier in CLASSIFY_E2E_TIERS
// (default "default,retrieval,summary"; "default" leaves the tier unset, so the
// classification tier applies, resolving as summary when it is not configured).
//
//	make test-e2e-one TEST=TestClassifyE2E PKG=./agents/core/
func TestClassifyE2E(t *testing.T) {
	env := RequireEnv(t, "TEST_ACCOUNT", "TEST_USER")
	tiers := os.Getenv("CLASSIFY_E2E_TIERS")
	if tiers == "" {
		tiers = "default,retrieval,summary"
	}
	for tier := range strings.SplitSeq(tiers, ",") {
		tier = strings.TrimSpace(tier)
		t.Run(tier, func(t *testing.T) {
			ctx := security.NewRequestContext(context.Background(), security.NewRequestContextForSuperAdmin().GetSecurityContext(), testRequestContext().GetLogger(), nil, nil)
			call := ClassificationCall{
				UserId:    env["TEST_USER"],
				AccountId: env["TEST_ACCOUNT"],
				AgentId:   "classification_e2e",
			}
			if tier != "default" {
				call.Tier = ModelTier(tier)
			} else {
				t.Logf("default tier resolves as %s", classificationTier(ctx, call))
			}
			for _, suite := range []struct {
				task  ClassificationTask
				cases []liveClassifyCase
			}{{liveIntentTask(), liveIntentCases}, {liveCategoryTask(), liveCategoryCases}} {
				correct := 0
				for _, c := range suite.cases {
					d, err := Classify(ctx, call, suite.task, c.input)
					require.NoError(t, err, c.input)
					require.True(t, d.Valid, "invalid answer %q for %q", d.Raw, c.input)
					if d.Label() == c.want {
						correct++
					} else {
						t.Logf("%s: got %s want %s for %q", suite.task.Name, d.Label(), c.want, c.input)
					}
				}
				t.Logf("%s on tier %s: %d/%d correct", suite.task.Name, tier, correct, len(suite.cases))
				assert.GreaterOrEqual(t, correct, len(suite.cases)-1, "at most one miss per task")
			}
		})
	}
}
