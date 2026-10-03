package alertrule

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeRuleGroup parses a rule-group body back into its group name and first rule.
func decodeRuleGroup(t *testing.T, body string) (string, map[string]any) {
	t.Helper()
	var group struct {
		Name  string           `json:"name"`
		Rules []map[string]any `json:"rules"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &group))
	require.Len(t, group.Rules, 1)
	return group.Name, group.Rules[0]
}

// The relay path passes the rule name to the agent separately, so its group name is a
// constant and must stay one — the agent action depends on it.
func TestBuildLokiAlertRule_RelayGroupNameUnchanged(t *testing.T) {
	name, rule := decodeRuleGroup(t, buildLokiAlertRule(AlertRuleConfig{
		Name:     "HighErrorRate",
		Query:    `sum(count_over_time({namespace="demo"} |= "error" [5m])) > 0`,
		Severity: "critical",
		Duration: "10m",
	}))

	assert.Equal(t, "nudgebee-alerts", name)
	assert.Equal(t, "HighErrorRate", rule["alert"])
	assert.Equal(t, "10m", rule["for"])
	assert.Equal(t, "critical", rule["labels"].(map[string]any)["severity"])
}

// On the ruler API the group name IS the group's identity: writing every rule under one
// group name would make each create overwrite the previous rule.
func TestBuildLokiAlertRuleGroup_NamesGroupPerRule(t *testing.T) {
	name, rule := decodeRuleGroup(t, buildRuleGroup(AlertRuleConfig{
		Name:  "PodCrashLoop",
		Query: `sum(count_over_time({namespace="demo"} |= "CrashLoopBackOff" [5m])) > 0`,
	}, "PodCrashLoop"))

	assert.Equal(t, "PodCrashLoop", name)
	assert.Equal(t, "PodCrashLoop", rule["alert"])
	// Unset duration keeps the shared default.
	assert.Equal(t, "5m", rule["for"])
	assert.Equal(t, "warning", rule["labels"].(map[string]any)["severity"])
	assert.Equal(t, "nudgebee", rule["labels"].(map[string]any)["source"])
}

func TestBuildLokiAlertRuleGroup_MergesCustomLabels(t *testing.T) {
	_, rule := decodeRuleGroup(t, buildRuleGroup(AlertRuleConfig{
		Name:        "Custom",
		Labels:      map[string]string{"team": "platform"},
		Annotations: map[string]string{"description": "too many errors"},
	}, "Custom"))

	labels := rule["labels"].(map[string]any)
	assert.Equal(t, "platform", labels["team"])
	assert.Equal(t, "nudgebee", labels["source"])
	assert.Equal(t, "too many errors", rule["annotations"].(map[string]any)["description"])
}

func TestLokiSaasAlertRule_CreateWritesGroupNamedAfterRule(t *testing.T) {
	var gotPath, gotMethod, gotContentType, gotBody, gotTenant string
	var gotUser, gotPass string
	var gotAuthOK bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotTenant = r.Header.Get("X-Scope-OrgID")
		gotUser, gotPass, gotAuthOK = r.BasicAuth()
		bodyBytes, _ := io.ReadAll(r.Body)
		gotBody = string(bodyBytes)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	cfg := &lokiRulerConfig{
		Url:      server.URL,
		AuthType: "basic",
		Username: "1234",
		Password: "token",
		TenantID: "team-a",
		Headers:  map[string]string{"X-Extra": "yes"},
	}

	err := (&LokiSaasAlertRuleSource{}).postRuleGroup(cfg, buildRuleGroup(AlertRuleConfig{
		Name:  "HighErrorRate",
		Query: `sum(count_over_time({namespace="demo"} |= "error" [5m])) > 0`,
	}, "HighErrorRate"))
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/loki/api/v1/rules/nudgebee", gotPath)
	// The ruler parses the body as YAML; JSON is a valid subset.
	assert.Equal(t, "application/yaml", gotContentType)
	assert.True(t, gotAuthOK)
	assert.Equal(t, "1234", gotUser)
	assert.Equal(t, "token", gotPass)
	assert.Equal(t, "team-a", gotTenant)

	name, _ := decodeRuleGroup(t, gotBody)
	assert.Equal(t, "HighErrorRate", name)
}

// A separate ruler service is addressed by loki_rules_url; without one, ruler calls go
// to the query endpoint.
func TestLokiRulerConfig_BaseURLFallback(t *testing.T) {
	assert.Equal(t, "http://loki", (&lokiRulerConfig{Url: "http://loki"}).RulerBaseURL())
	assert.Equal(t, "http://ruler", (&lokiRulerConfig{Url: "http://loki", RulesUrl: "http://ruler"}).RulerBaseURL())
}

func TestLokiSaasAlertRule_ReadOnlyRulerIsNamed(t *testing.T) {
	// Some deployments and proxies answer 405 for a non-writable ruler.
	t.Run("405", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}))
		defer server.Close()

		err := (&LokiSaasAlertRuleSource{}).postRuleGroup(&lokiRulerConfig{Url: server.URL}, "{}")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})

	// What Loki 3.4.1 actually does, verified live: a local rule store rejects writes
	// with a 500 whose body names the real cause. Left as a bare 500 this reads as a
	// transient server fault, when the fix is a ruler config change.
	t.Run("500 from a local rule store", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error","error":"SetRuleGroup unsupported in rule local store"}`))
		}))
		defer server.Close()

		err := (&LokiSaasAlertRuleSource{}).postRuleGroup(&lokiRulerConfig{Url: server.URL}, "{}")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
		assert.Contains(t, err.Error(), "writable rule store")
	})
}

func TestLokiSaasAlertRule_WriteErrorCarriesRulerBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid rule group"))
	}))
	defer server.Close()

	err := (&LokiSaasAlertRuleSource{}).postRuleGroup(&lokiRulerConfig{Url: server.URL}, "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.Contains(t, err.Error(), "invalid rule group")
}

func TestLokiSaasAlertRule_NoBasicAuthWithoutBothCredentials(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, sawAuth = r.BasicAuth()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	err := (&LokiSaasAlertRuleSource{}).postRuleGroup(&lokiRulerConfig{Url: server.URL, AuthType: "basic", Username: "only-user"}, "{}")
	require.NoError(t, err)
	assert.False(t, sawAuth)
}

func TestResolveProviderSource(t *testing.T) {
	// (hasUser, hasAgent) per lookup, in call order.
	patch := func(t *testing.T, hasUser, hasAgent bool, userErr, agentErr error) {
		t.Helper()
		p := gomonkey.NewPatches()
		p.ApplyFunc(hasIntegrationWithSource,
			func(_ *security.RequestContext, _, _, source string) (bool, error) {
				if source == "user" {
					return hasUser, userErr
				}
				return hasAgent, agentErr
			})
		t.Cleanup(p.Reset)
	}

	t.Run("user integration only overrides to user", func(t *testing.T) {
		patch(t, true, false, nil, nil)
		assert.Equal(t, "user", resolveProviderSource(nil, "loki", "agent", "acc-1"))
	})

	t.Run("agent integration only keeps agent", func(t *testing.T) {
		patch(t, false, false, nil, nil)
		assert.Equal(t, "agent", resolveProviderSource(nil, "loki", "agent", "acc-1"))
	})

	// Nothing records which backend stored a given external_rule_id, so retargeting an
	// account that has both would send deletes to a ruler that never held the rule —
	// a 404 reported as success, leaving the real rule firing on the other backend.
	t.Run("both integrations keep the caller's source", func(t *testing.T) {
		patch(t, true, true, nil, nil)
		assert.Equal(t, "agent", resolveProviderSource(nil, "loki", "agent", "acc-1"))
	})

	t.Run("a failed lookup keeps the caller's source", func(t *testing.T) {
		patch(t, false, false, errors.New("db down"), nil)
		assert.Equal(t, "agent", resolveProviderSource(nil, "loki", "agent", "acc-1"))

		patch(t, true, false, nil, errors.New("db down"))
		assert.Equal(t, "agent", resolveProviderSource(nil, "loki", "agent", "acc-1"))
	})

	t.Run("other providers and empty accounts pass through untouched", func(t *testing.T) {
		assert.Equal(t, "user", resolveProviderSource(nil, "signoz", "user", "acc-1"))
		assert.Equal(t, "agent", resolveProviderSource(nil, "loki", "agent", ""))
	})
}

// getAlertRuleSource must send user-configured Loki to the ruler API and agent-managed
// Loki to the relay.
func TestGetAlertRuleSource_LokiBySource(t *testing.T) {
	userSource, err := getAlertRuleSource("loki", "user")
	require.NoError(t, err)
	assert.IsType(t, &LokiSaasAlertRuleSource{}, userSource)

	agentSource, err := getAlertRuleSource("loki", "agent")
	require.NoError(t, err)
	assert.IsType(t, &LokiAlertRuleSource{}, agentSource)
}
