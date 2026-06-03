package integrations

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

// fetchZendutyAlertsForIncident is the network-bound piece. Mock with httptest.

func TestZendutyEnrich_FetchAlerts_HappyPath(t *testing.T) {
	const incident = "ZDFAKEINC0001"
	const apiKey = "test-api-key"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/incidents/"+incident+"/alerts/", r.URL.Path)
		assert.Equal(t, "Token "+apiKey, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"count": 1, "next": null, "previous": null,
			"results": [{
				"entity_id": "KubeDeploymentReplicasMismatch",
				"integration_object": {"name": "vmalert", "unique_id": "intg-001"},
				"alert_type": "3"
			}]
		}`))
	}))
	defer srv.Close()

	prev := zendutyAPIBaseURL
	zendutyAPIBaseURL = srv.URL
	defer func() { zendutyAPIBaseURL = prev }()

	got, err := fetchZendutyAlertsForIncident(incident, apiKey)
	assert.NoError(t, err)
	assert.Equal(t, "KubeDeploymentReplicasMismatch", got.EntityID)
	assert.Equal(t, "vmalert", got.IntegrationName)
	assert.Equal(t, "intg-001", got.IntegrationUniqueID)
}

func TestZendutyEnrich_FetchAlerts_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count": 0, "results": []}`))
	}))
	defer srv.Close()

	prev := zendutyAPIBaseURL
	zendutyAPIBaseURL = srv.URL
	defer func() { zendutyAPIBaseURL = prev }()

	got, err := fetchZendutyAlertsForIncident("ZDFAKEINC0002", "key")
	assert.NoError(t, err, "empty results is not an error")
	assert.Equal(t, "", got.EntityID)
	assert.Equal(t, "", got.IntegrationName)
}

func TestZendutyEnrich_FetchAlerts_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "Invalid token."}`))
	}))
	defer srv.Close()

	prev := zendutyAPIBaseURL
	zendutyAPIBaseURL = srv.URL
	defer func() { zendutyAPIBaseURL = prev }()

	got, err := fetchZendutyAlertsForIncident("ZDFAKEINC0003", "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Equal(t, "", got.EntityID, "no enrichment on API error")
}

// applyZendutyEnrichResult is the merge logic. Pure data — no HTTP.

func TestZendutyEnrich_Apply_FillsMissingFields(t *testing.T) {
	alert := &core.EventIncomingWebhookInvestigation{
		Labels: map[string]string{
			"nb_zenduty_service_name": "Sample Service",
		},
	}
	result := zendutyEnrichResult{
		EntityID:            "KubeDeploymentReplicasMismatch",
		IntegrationName:     "vmalert",
		IntegrationUniqueID: "intg-001",
	}

	applyZendutyEnrichResult(alert, result)

	assert.Equal(t, "KubeDeploymentReplicasMismatch", alert.Labels["alertname"], "alertname filled from entity_id")
	assert.Equal(t, "KubeDeploymentReplicasMismatch", alert.Labels["nb_alert_entity_id"])
	assert.Equal(t, "prometheus", alert.Labels["nb_alert_source"], "vmalert normalized to prometheus")
	assert.Equal(t, "vmalert", alert.Labels["nb_zenduty_integration_name"])
	assert.Equal(t, "intg-001", alert.Labels["nb_zenduty_integration_id"])
	assert.Equal(t, "Sample Service", alert.Labels["nb_zenduty_service_name"], "existing labels preserved")
}

func TestZendutyEnrich_Apply_DoesNotClobberExistingValues(t *testing.T) {
	alert := &core.EventIncomingWebhookInvestigation{
		Labels: map[string]string{
			"alertname":       "DatasourceNoData", // came from summary firing labels
			"nb_alert_source": "grafana",          // already detected from summary
		},
	}
	result := zendutyEnrichResult{
		EntityID:        "KubeDeploymentReplicasMismatch",
		IntegrationName: "vmalert",
	}

	applyZendutyEnrichResult(alert, result)

	assert.Equal(t, "DatasourceNoData", alert.Labels["alertname"], "existing alertname must win")
	assert.Equal(t, "grafana", alert.Labels["nb_alert_source"], "existing source must win")
	// Audit labels are still populated since they have unique keys
	assert.Equal(t, "KubeDeploymentReplicasMismatch", alert.Labels["nb_alert_entity_id"])
	assert.Equal(t, "vmalert", alert.Labels["nb_zenduty_integration_name"])
}

func TestZendutyEnrich_Apply_EmptyResultIsNoop(t *testing.T) {
	alert := &core.EventIncomingWebhookInvestigation{
		Labels: map[string]string{"existing": "value"},
	}
	applyZendutyEnrichResult(alert, zendutyEnrichResult{})
	assert.Equal(t, map[string]string{"existing": "value"}, alert.Labels)
}

func TestZendutyEnrich_Apply_NilLabelsMapInitialized(t *testing.T) {
	alert := &core.EventIncomingWebhookInvestigation{Labels: nil}
	applyZendutyEnrichResult(alert, zendutyEnrichResult{EntityID: "X"})
	assert.NotNil(t, alert.Labels)
	assert.Equal(t, "X", alert.Labels["alertname"])
}

// normalizeZendutyIntegrationName is a pure lookup table.

func TestZendutyEnrich_NormalizeIntegrationName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"vmalert", "prometheus"},
		{"VictoriaMetrics", "prometheus"},
		{"Prometheus", "prometheus"},
		{"alertmanager", "prometheus"},
		{"Grafana", "grafana"},
		{"SigNoz Alert Manager", "signoz"},
		{"Chronosphere", "chronosphere"},
		{"AWS CloudWatch", "aws"},
		{"Azure Monitor", "azure"},
		{"Datadog", "datadog"},
		{"New Relic", "newrelic"},
		{"Dynatrace", "dynatrace"},
		{"Splunk", "splunk"},
		{"  vmalert  ", "prometheus"}, // trimming
		{"some-custom-thing", "some-custom-thing"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeZendutyIntegrationName(tc.in))
		})
	}
}
