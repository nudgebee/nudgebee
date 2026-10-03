package alertrule

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/promclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mimirConfig(t *testing.T, rulerURL string) promclient.PrometheusUserConfig {
	t.Helper()
	cfg, err := promclient.NewPrometheusUserConfig(map[string]string{
		promclient.PrometheusURLKey:          "http://query.invalid/prometheus",
		promclient.PrometheusAuthTypeKey:     promclient.PrometheusAuthBearerToken,
		promclient.PrometheusBearerTokenKey:  "glc-token",
		promclient.PrometheusExtraHeadersKey: `{"X-Scope-OrgID": "team-a"}`,
		promclient.PrometheusRulerTypeKey:    promclient.PrometheusRulerMimirCortex,
		promclient.PrometheusRulerURLKey:     rulerURL,
	})
	require.NoError(t, err)
	ruler, err := prometheusRulerFromConfig(cfg)
	require.NoError(t, err)
	return ruler
}

func TestPrometheusSaasAlertRule_CreateWritesGroupNamedAfterRule(t *testing.T) {
	var gotPath, gotMethod, gotContentType, gotBody, gotTenant, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotTenant = r.Header.Get("X-Scope-OrgID")
		gotAuth = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		gotBody = string(bodyBytes)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	// The ruler sits under a prefix, as Mimir (/prometheus) and Grafana Cloud (/api/prom) do.
	cfg := mimirConfig(t, server.URL+"/prometheus")
	err := postPrometheusRuleGroup(nil, cfg, buildRuleGroup(AlertRuleConfig{
		Name:        "PaymentsHighErrorRate",
		Query:       `sum(rate(http_requests_total{code=~"5.."}[5m])) > 0.05`,
		Duration:    "10m",
		Severity:    "critical",
		Annotations: map[string]string{"summary": "Payments 5xx above 5%", "description": "too many errors"},
	}, "PaymentsHighErrorRate"))
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/prometheus/config/v1/rules/nudgebee", gotPath)
	assert.Equal(t, "application/yaml", gotContentType)
	assert.Equal(t, "team-a", gotTenant, "the tenant header rides on ruler calls too")
	assert.Equal(t, "Bearer glc-token", gotAuth)

	name, rule := decodeRuleGroup(t, gotBody)
	assert.Equal(t, "PaymentsHighErrorRate", name)
	assert.Equal(t, "10m", rule["for"])
	assert.Equal(t, "critical", rule["labels"].(map[string]any)["severity"])
	assert.Equal(t, "Payments 5xx above 5%", rule["annotations"].(map[string]any)["summary"], "the caller's summary is the alert title downstream")
}

// Without a declared ruler the source refuses before any request: a plain
// Prometheus would otherwise take the POST and answer 404, which reads as a
// transient fault rather than a configuration gap.
func TestPrometheusSaasAlertRule_NoRulerRefusesWithoutRequest(t *testing.T) {
	cfg, err := promclient.NewPrometheusUserConfig(map[string]string{
		promclient.PrometheusURLKey: "http://prometheus.invalid",
	})
	require.NoError(t, err)

	_, err = prometheusRulerFromConfig(cfg)
	require.ErrorIs(t, err, errPrometheusNoRuler)
	assert.Contains(t, err.Error(), "Ruler type")
}

func TestPrometheusSaasAlertRule_RulerURLFallsBackToQueryURL(t *testing.T) {
	cfg, err := promclient.NewPrometheusUserConfig(map[string]string{
		promclient.PrometheusURLKey:       "http://mimir.invalid/prometheus/",
		promclient.PrometheusRulerTypeKey: promclient.PrometheusRulerMimirCortex,
	})
	require.NoError(t, err)
	ruler, err := prometheusRulerFromConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, "http://mimir.invalid/prometheus", ruler.URL)
}

func TestPrometheusSaasAlertRule_NoRulerAPIIsNamed(t *testing.T) {
	// Each engine says "I don't serve this path" differently: Mimir/Cortex and a
	// plain Prometheus with 404/405, VictoriaMetrics with a 400 that names the
	// reason in its body (observed on vmsingle 1.x).
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"404", http.StatusNotFound, ""},
		{"405", http.StatusMethodNotAllowed, ""},
		{"victoriametrics 400", http.StatusBadRequest, `requestURI: /config/v1/rules; unsupported path requested: "/config/v1/rules"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			err := postPrometheusRuleGroup(nil, mimirConfig(t, server.URL), "{}")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "no ruler API")
			assert.Contains(t, err.Error(), "/config/v1/rules/nudgebee")
		})
	}

	// A 400 that is NOT about the path is a real bad request and must not be
	// reported as a missing ruler.
	t.Run("unrelated 400 stays a bad request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid rule group: duplicate alert name"))
		}))
		defer server.Close()
		err := postPrometheusRuleGroup(nil, mimirConfig(t, server.URL), "{}")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "no ruler API")
		assert.Contains(t, err.Error(), "duplicate alert name")
	})
}

func TestPrometheusSaasAlertRule_RejectedCredentialsAreNamed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("missing rules:write"))
	}))
	defer server.Close()

	err := postPrometheusRuleGroup(nil, mimirConfig(t, server.URL), "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected the credentials")
	assert.Contains(t, err.Error(), "missing rules:write")
}

func TestPrometheusSaasAlertRule_Delete(t *testing.T) {
	t.Run("addresses the group under the nudgebee namespace", func(t *testing.T) {
		var gotPath, gotMethod string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotMethod = r.URL.EscapedPath(), r.Method
			w.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()

		require.NoError(t, deletePrometheusRuleGroup(nil, mimirConfig(t, server.URL), "Payments High/Error"))
		assert.Equal(t, http.MethodDelete, gotMethod)
		assert.Equal(t, "/config/v1/rules/nudgebee/Payments%20High%2FError", gotPath)
	})
	t.Run("already absent is success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()
		require.NoError(t, deletePrometheusRuleGroup(nil, mimirConfig(t, server.URL), "Gone"))
	})
	t.Run("server error is reported", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("store unavailable"))
		}))
		defer server.Close()
		err := deletePrometheusRuleGroup(nil, mimirConfig(t, server.URL), "X")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store unavailable")
	})
}

const prometheusRulesPayload = `{"status":"success","data":{"groups":[
  {"name":"PaymentsHighErrorRate","file":"nudgebee","rules":[
    {"type":"alerting","name":"PaymentsHighErrorRate","query":"sum(rate(x[5m])) > 0.05","duration":300,
     "labels":{"severity":"critical","source":"nudgebee"},"annotations":{"summary":"Payments 5xx above 5%"}}]},
  {"name":"kubernetes-apps","file":"/etc/prometheus/rules/kube.yaml","rules":[
    {"type":"alerting","name":"KubePodCrashLooping","query":"increase(kube_pod_container_status_restarts_total[10m]) > 0","duration":900,
     "labels":{"severity":"warning"},"annotations":{}},
    {"type":"recording","name":"job:up:avg","query":"avg(up) by (job)"}]}]}}`

func TestPrometheusSaasAlertRule_ListParsesAlertingRules(t *testing.T) {
	var gotPath, gotType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotType = r.URL.Path, r.URL.Query().Get("type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(prometheusRulesPayload))
	}))
	defer server.Close()

	cfg, err := promclient.NewPrometheusUserConfig(map[string]string{promclient.PrometheusURLKey: server.URL + "/prometheus"})
	require.NoError(t, err)
	rules, err := listPrometheusRules(nil, cfg)
	require.NoError(t, err)

	assert.Equal(t, "/prometheus/api/v1/rules", gotPath, "inventory comes from the query endpoint, not the ruler")
	assert.Equal(t, "alert", gotType)
	require.Len(t, rules, 2, "recording rules are not alert rules")

	ours := rules[0]
	assert.Equal(t, "nudgebee/PaymentsHighErrorRate", ours.ExternalRuleId)
	assert.Equal(t, "PaymentsHighErrorRate", ours.Name)
	assert.Equal(t, "metric", ours.AlertType)
	assert.Equal(t, "critical", ours.Severity)
	assert.Equal(t, "5m", ours.Duration)
	assert.True(t, ours.Enabled)
	assert.Equal(t, true, ours.ProviderConfig["managed_by_nudgebee"])

	theirs := rules[1]
	assert.Equal(t, "/etc/prometheus/rules/kube.yaml/kubernetes-apps", theirs.ExternalRuleId)
	assert.Equal(t, "15m", theirs.Duration)
	assert.Equal(t, false, theirs.ProviderConfig["managed_by_nudgebee"])
	assert.Equal(t, "kubernetes-apps", theirs.ProviderConfig["group"])
}

// An endpoint that does not serve /api/v1/rules is a configuration problem the
// operator can fix (usually a missing path prefix), so the error says which call
// failed rather than reporting an empty inventory.
//
// Deliberately not naming VictoriaMetrics here: vmsingle answers this endpoint
// with 200 and the rule groups vmalert holds (verified against 1.x on the dev
// cluster: 57 groups), so its rules ARE listable.
func TestPrometheusSaasAlertRule_ListNamesMissingRulesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg, err := promclient.NewPrometheusUserConfig(map[string]string{promclient.PrometheusURLKey: server.URL})
	require.NoError(t, err)
	_, err = listPrometheusRules(nil, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not serve /api/v1/rules")
	assert.Contains(t, err.Error(), "path prefix")
}

func TestPrometheusDuration(t *testing.T) {
	assert.Equal(t, "", prometheusDuration(0))
	assert.Equal(t, "30s", prometheusDuration(30))
	assert.Equal(t, "5m", prometheusDuration(300))
	assert.Equal(t, "1h30m", prometheusDuration(5400))
}

func TestGetAlertRuleSource_PrometheusUserOnly(t *testing.T) {
	src, err := getAlertRuleSource("prometheus", "user")
	require.NoError(t, err)
	assert.IsType(t, &PrometheusSaasAlertRuleSource{}, src)
	_, isLister := src.(AlertRuleLister)
	assert.True(t, isLister)

	// The agent's rules travel as a PrometheusRule CR over the relay, not through here.
	_, err = getAlertRuleSource("prometheus", "agent")
	require.Error(t, err)
}
