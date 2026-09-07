//go:build liveMimir

// Live verification of the ruler contract against a REAL Mimir, not an httptest
// fake: the paths this source builds are ones the Cortex ruler API actually
// serves, a JSON body is accepted where the endpoint advertises YAML, the group
// written comes back from /api/v1/rules with the namespace in `file`, and delete
// is idempotent. Run with a Mimir reachable at MIMIR_LIVE_URL (multitenancy off,
// or set MIMIR_LIVE_TENANT), e.g.
//
//	MIMIR_LIVE_URL=http://localhost:9009/prometheus \
//	  go test -tags liveMimir ./observability/alertrule/ -run TestLiveMimir -v
package alertrule

import (
	"os"
	"testing"
	"time"

	"nudgebee/services/integrations/promclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func liveMimirConfig(t *testing.T) promclient.PrometheusUserConfig {
	t.Helper()
	url := os.Getenv("MIMIR_LIVE_URL")
	if url == "" {
		t.Skip("MIMIR_LIVE_URL not set")
	}
	values := map[string]string{
		promclient.PrometheusURLKey:       url,
		promclient.PrometheusRulerTypeKey: promclient.PrometheusRulerMimirCortex,
	}
	if tenant := os.Getenv("MIMIR_LIVE_TENANT"); tenant != "" {
		values[promclient.PrometheusExtraHeadersKey] = `{"X-Scope-OrgID": "` + tenant + `"}`
	}
	cfg, err := promclient.NewPrometheusUserConfig(values)
	require.NoError(t, err)
	return cfg
}

func TestLiveMimir_RuleGroupRoundTrip(t *testing.T) {
	cfg := liveMimirConfig(t)
	ruler, err := prometheusRulerFromConfig(cfg)
	require.NoError(t, err)

	const name = "NbLiveTestHighErrorRate"
	rule := AlertRuleConfig{
		Name:        name,
		Query:       `vector(1) > 0`,
		Duration:    "1m",
		Severity:    "warning",
		Annotations: map[string]string{"summary": "live test rule", "description": "written by the live test"},
	}

	// create — the ruler answers 202 and stores the group under the nudgebee namespace
	require.NoError(t, postPrometheusRuleGroup(nil, ruler, buildRuleGroup(rule, name)))

	// the rules API shows it, namespace in `file`, once the ruler has polled the store
	var listed []ExternalAlertRule
	require.Eventually(t, func() bool {
		rules, err := listPrometheusRules(nil, cfg)
		if err != nil {
			t.Logf("list: %v", err)
			return false
		}
		for _, r := range rules {
			if r.Name == name {
				listed = []ExternalAlertRule{r}
				return true
			}
		}
		return false
	}, 30*time.Second, 2*time.Second, "rule never appeared in /api/v1/rules")
	require.Len(t, listed, 1)
	assert.Equal(t, "nudgebee/"+name, listed[0].ExternalRuleId)
	assert.Equal(t, true, listed[0].ProviderConfig["managed_by_nudgebee"])
	assert.Equal(t, "1m", listed[0].Duration)
	assert.Equal(t, "warning", listed[0].Severity)
	assert.Equal(t, "live test rule", listed[0].Annotations["summary"])

	// update — re-POST under the same group name replaces it
	rule.Duration = "2m"
	require.NoError(t, postPrometheusRuleGroup(nil, ruler, buildRuleGroup(rule, name)))
	require.Eventually(t, func() bool {
		rules, err := listPrometheusRules(nil, cfg)
		if err != nil {
			return false
		}
		for _, r := range rules {
			if r.Name == name && r.Duration == "2m" {
				return true
			}
		}
		return false
	}, 30*time.Second, 2*time.Second, "updated duration never appeared")

	// delete, then delete again: absent is the state asked for, not an error
	require.NoError(t, deletePrometheusRuleGroup(nil, ruler, name))
	require.NoError(t, deletePrometheusRuleGroup(nil, ruler, name))
	require.Eventually(t, func() bool {
		rules, err := listPrometheusRules(nil, cfg)
		if err != nil {
			return false
		}
		for _, r := range rules {
			if r.Name == name {
				return false
			}
		}
		return true
	}, 30*time.Second, 2*time.Second, "rule still listed after delete")
}
