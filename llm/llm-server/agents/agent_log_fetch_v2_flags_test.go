package agents

import (
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestFetchLogsAgentV2_canonicalEnabled covers the deploy-time env-var gate that
// picks the canonical v2 path vs the v1 fallback: canonicalEnabled simply
// reflects config.Config.LogAgentV2Enabled (LLM_SERVER_LOG_AGENT_V2_ENABLED).
func TestFetchLogsAgentV2_canonicalEnabled(t *testing.T) {
	orig := config.Config.LogAgentV2Enabled
	t.Cleanup(func() { config.Config.LogAgentV2Enabled = orig })

	cases := []struct {
		name    string
		enabled bool
		want    bool
	}{
		{"enabled", true, true},
		{"disabled", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.Config.LogAgentV2Enabled = tc.enabled

			a := &FetchLogsAgentV2{FetchLogsAgent: &FetchLogsAgent{accountId: "acct"}}
			ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "", []string{"acct"})

			assert.Equal(t, tc.want, a.canonicalEnabled(ctx))
		})
	}
}
