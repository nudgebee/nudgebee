package integrations

import (
	"encoding/json"
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
	assert.Len(t, got, 1)
	assert.Equal(t, "KubeDeploymentReplicasMismatch", got[0].EntityID)
	assert.Equal(t, "vmalert", got[0].IntegrationObject.Name)
	assert.Equal(t, "intg-001", got[0].IntegrationObject.UniqueID)
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
	assert.Empty(t, got)
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
	assert.Empty(t, got, "no enrichment on API error")
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

// selectZendutyAlert — Zenduty collates several alerts onto one incident and
// the API returns them newest-first, so index 0 is frequently the wrong alert.

func TestZendutySelectAlert_MatchesOnIncidentSummary(t *testing.T) {
	// Shape observed on incident kGtQKSvJd4ZaksWKwhtxs4: four collated alerts
	// spanning two workloads and two alertnames, newest first.
	alerts := []zendutyAlert{
		{UniqueID: "a4", EntityID: "HighP95Latency", Summary: "High P95 latency for cloud-collector-server-64945c7bc6 in nudgebee"},
		{UniqueID: "a3", EntityID: "ApplicationAPIFailures", Summary: "High error rate for relay-server in nudgebee"},
		{UniqueID: "a2", EntityID: "HighP95Latency", Summary: "High P95 latency for relay-server in nudgebee"},
		{UniqueID: "a1", EntityID: "HighP95Latency", Summary: "High P95 latency for cloud-collector-server-64945c7bc6 in nudgebee"},
	}

	got, ok := selectZendutyAlert(alerts, "High error rate for relay-server in nudgebee")
	assert.True(t, ok)
	assert.Equal(t, "a3", got.UniqueID, "picks the alert the webhook is about, not the newest")
	assert.Equal(t, "ApplicationAPIFailures", got.EntityID)
}

func TestZendutySelectAlert_FallsBackToNewest(t *testing.T) {
	alerts := []zendutyAlert{{UniqueID: "a2"}, {UniqueID: "a1"}}

	got, ok := selectZendutyAlert(alerts, "a summary matching nothing")
	assert.True(t, ok)
	assert.Equal(t, "a2", got.UniqueID)

	got, ok = selectZendutyAlert(alerts, "")
	assert.True(t, ok)
	assert.Equal(t, "a2", got.UniqueID, "empty summary falls back rather than mismatching")
}

func TestZendutySelectAlert_EmptyList(t *testing.T) {
	_, ok := selectZendutyAlert(nil, "anything")
	assert.False(t, ok)
}

// alertmanagerPayloadToLabels — must emit the same key vocabulary
// parseFiringLabels produces for PagerDuty, so both feed the shared resolver.

func TestAlertmanagerPayloadToLabels_RealPayload(t *testing.T) {
	// Verbatim shape of the archived payload for alert NMuDzu85TaSuCJFPGNYaXw.
	var p alertmanagerPayload
	err := json.Unmarshal([]byte(`{
		"alerts": [{
			"labels": {
				"alertgroup": "custom-alerts",
				"alertname": "HighP95Latency",
				"destination_workload_name": "cloud-collector-server-64945c7bc6",
				"destination_workload_namespace": "nudgebee",
				"severity": "warning"
			},
			"annotations": {"summary": "High P95 latency for cloud-collector-server-64945c7bc6 in nudgebee"},
			"generatorURL": "http://vmalert:8080/vmalert/alert?group_id=97568&alert_id=17669",
			"fingerprint": "4cde56d671cfaf25"
		}],
		"commonLabels": {
			"alertgroup": "custom-alerts",
			"alertname": "HighP95Latency",
			"destination_workload_name": "cloud-collector-server-64945c7bc6",
			"destination_workload_namespace": "nudgebee",
			"severity": "warning"
		},
		"commonAnnotations": {"summary": "High P95 latency for cloud-collector-server-64945c7bc6 in nudgebee"}
	}`), &p)
	assert.NoError(t, err)

	labels := alertmanagerPayloadToLabels(p)

	// The keys resolveSubjectFromLabels walks — identical to the PagerDuty path.
	assert.Equal(t, "cloud-collector-server-64945c7bc6", labels["destination_workload_name"])
	assert.Equal(t, "nudgebee", labels["destination_workload_namespace"])
	assert.Equal(t, "HighP95Latency", labels["alertname"])
	assert.Equal(t, "warning", labels["severity"])
	// PagerDuty parity + the extras only this path can supply.
	assert.Equal(t, "http://vmalert:8080/vmalert/alert?group_id=97568&alert_id=17669", labels["source_url"])
	assert.Equal(t, "4cde56d671cfaf25", labels["nb_alert_fingerprint"])
	assert.Equal(t, "1", labels["nb_alert_firing_count"])
	// Annotations flatten unprefixed, matching parseFiringLabels.
	assert.Equal(t, "High P95 latency for cloud-collector-server-64945c7bc6 in nudgebee", labels["summary"])
}

func TestAlertmanagerPayloadToLabels_FallsBackToFirstAlertWhenNoCommon(t *testing.T) {
	p := alertmanagerPayload{}
	p.Alerts = append(p.Alerts, struct {
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		GeneratorURL string            `json:"generatorURL"`
		Fingerprint  string            `json:"fingerprint"`
	}{
		Labels:      map[string]string{"k8s_deployment_name": "cloud-collector-server", "namespace": "nudgebee"},
		Annotations: map[string]string{"description": "Collector High Latency"},
	})

	labels := alertmanagerPayloadToLabels(p)
	assert.Equal(t, "cloud-collector-server", labels["k8s_deployment_name"])
	assert.Equal(t, "nudgebee", labels["namespace"])
	assert.Equal(t, "Collector High Latency", labels["description"])
}

func TestAlertmanagerPayloadToLabels_EmptyPayload(t *testing.T) {
	assert.Empty(t, alertmanagerPayloadToLabels(alertmanagerPayload{}))
}

// applyZendutyEnrichResult must not let recovered payload labels clobber
// anything already derived from the webhook summary.

func TestZendutyEnrich_Apply_PayloadLabelsAreNonClobber(t *testing.T) {
	alert := &core.EventIncomingWebhookInvestigation{
		Labels: map[string]string{"severity": "critical"}, // from the webhook
	}
	applyZendutyEnrichResult(alert, zendutyEnrichResult{
		PayloadLabels: map[string]string{
			"severity":                  "warning", // must NOT win
			"destination_workload_name": "relay-server",
		},
	})

	assert.Equal(t, "critical", alert.Labels["severity"], "webhook value survives")
	assert.Equal(t, "relay-server", alert.Labels["destination_workload_name"])
	assert.Equal(t, "true", alert.Labels["nb_zenduty_payload_recovered"])
}

// zendutyAlertPayloadURL — the date segments are the subtle part: UTC, and NOT
// zero-padded. A padded path 404s against the real endpoint.

func TestZendutyAlertPayloadURL(t *testing.T) {
	mkAlert := func(created string) zendutyAlert {
		a := zendutyAlert{UniqueID: "NMuDzu85TaSuCJFPGNYaXw", CreationDate: created}
		a.IntegrationObject.Team = "team-1"
		a.IntegrationObject.Service = "svc-1"
		a.IntegrationObject.UniqueID = "intg-1"
		return a
	}

	prev := zendutyPayloadBaseURL
	zendutyPayloadBaseURL = "https://zd.test/api/v2"
	defer func() { zendutyPayloadBaseURL = prev }()

	got, err := zendutyAlertPayloadURL(mkAlert("2026-07-29T05:24:24.678962Z"), "acct-1")
	assert.NoError(t, err)
	assert.Equal(t,
		"https://zd.test/api/v2/incidents/get_alert_payload/alert_payload/acct-1/team-1/svc-1/intg-1/2026/7/29/NMuDzu85TaSuCJFPGNYaXw.json/",
		got)

	t.Run("single digit day and month are not padded", func(t *testing.T) {
		got, err := zendutyAlertPayloadURL(mkAlert("2026-01-09T00:00:00Z"), "acct-1")
		assert.NoError(t, err)
		assert.Contains(t, got, "/2026/1/9/", "month/day must not be zero-padded")
	})

	t.Run("date is normalized to UTC", func(t *testing.T) {
		// 2026-07-30T02:00:00+05:30 is 2026-07-29 in UTC.
		got, err := zendutyAlertPayloadURL(mkAlert("2026-07-30T02:00:00+05:30"), "acct-1")
		assert.NoError(t, err)
		assert.Contains(t, got, "/2026/7/29/", "path uses the UTC date")
	})

	t.Run("errors on unparseable date", func(t *testing.T) {
		_, err := zendutyAlertPayloadURL(mkAlert("not-a-date"), "acct-1")
		assert.Error(t, err)
	})

	t.Run("errors on missing ids", func(t *testing.T) {
		_, err := zendutyAlertPayloadURL(mkAlert("2026-07-29T05:24:24Z"), "")
		assert.Error(t, err, "no account id")

		bare := zendutyAlert{UniqueID: "x", CreationDate: "2026-07-29T05:24:24Z"}
		_, err = zendutyAlertPayloadURL(bare, "acct-1")
		assert.Error(t, err, "no team/service/integration")
	})
}
