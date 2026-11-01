package integrations

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Values are synthetic — real project/instance names must not be committed.
var sampleNormalizedCloudSQL = gcpNormalizedEvent{
	Title:            "cloudsql_database: slow-queries",
	Description:      "Alert policy 'slow-queries' is currently firing.",
	AggregationKey:   "slow-queries",
	SubjectName:      "example-project:sample-db",
	SubjectType:      "cloudsql_database",
	SubjectNamespace: "Cloud SQL",
	ServiceKey:       "arn:gcp:cloud sql:us-central:example-project:cloudsql_database:example-project:sample-db",
	Fingerprint:      "projects/example-project/alertPolicies/1001:cloudsql_database:example-project:sample-db",
	FindingId:        "projects/example-project/alerts/0.cloudsql1",
	CloudResourceId:  "00000000-0000-0000-0000-000000000001",
	Priority:         "MEDIUM",
	Status:           "FIRING",
	Labels:           map[string]string{"gcp_event_instance": "example-project:sample-db", "metric_log": "cloudsql.googleapis.com/postgres.log"},
}

const sampleCloudSQLWebhook = `{"version":"1.2","incident":{"incident_id":"0.cloudsql1","state":"open","started_at":1790670860,
	"url":"https://console.cloud.google.com/monitoring/alerting/alerts/0.cloudsql1?project=example-project",
	"resource":{"type":"cloudsql_database","labels":{"database_id":"example-project:sample-db"}},
	"resource_display_name":"sample-db",
	"metric":{"type":"logging.googleapis.com/user/slow-queries"},"policy_name":"slow-queries"}}`

// testTenantRequestContext carries a tenant without the DB lookup the tenant-admin
// constructor does.
func testTenantRequestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForAuditRelay("tenant-1", ""), slog.Default(), nil, nil)
}

// fakeCollector serves the normalize endpoint and, when asked, records the request it got.
func fakeCollector(t *testing.T, status int, body any, got *http.Request, gotBody *string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			*got = *r.Clone(r.Context())
		}
		b, _ := io.ReadAll(r.Body)
		if gotBody != nil {
			*gotBody = string(b)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	prevURL, prevHeader, prevToken := config.Config.CloudCollectorServerUrl, config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken
	config.Config.CloudCollectorServerUrl = srv.URL
	config.Config.CloudCollectorServerTokenHeader = "x-collector-token"
	config.Config.CloudCollectorServerToken = "secret"
	t.Cleanup(func() {
		config.Config.CloudCollectorServerUrl, config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken = prevURL, prevHeader, prevToken
	})
}

func TestNormalizeGCPIncidentAsksCollector(t *testing.T) {
	var got http.Request
	var gotBody string
	fakeCollector(t, http.StatusOK, map[string]any{"data": sampleNormalizedCloudSQL}, &got, &gotBody)

	sc := testTenantRequestContext()
	n, err := normalizeGCPIncident(sc, "acc-1", sampleCloudSQLWebhook)
	require.NoError(t, err)

	assert.Equal(t, "/v1/cloud/normalize_gcp_monitoring_webhook", got.URL.Path)
	assert.Equal(t, "acc-1", got.URL.Query().Get("account_id"))
	assert.Equal(t, "secret", got.Header.Get("x-collector-token"))
	assert.Equal(t, "tenant-1", got.Header.Get("x-tenant-id"))
	assert.JSONEq(t, sampleCloudSQLWebhook, gotBody, "the delivery is forwarded unchanged")
	assert.Equal(t, sampleNormalizedCloudSQL, n)
}

func TestNormalizeGCPIncidentFailsWithoutAnIdentity(t *testing.T) {
	sc := testTenantRequestContext()

	for name, tc := range map[string]struct {
		status int
		body   any
	}{
		"collector error status": {http.StatusBadRequest, map[string]any{"errors": []map[string]string{{"message": "account is not a GCP account"}}}},
		"error in envelope":      {http.StatusOK, map[string]any{"errors": []map[string]string{{"message": "boom"}}}},
		"no finding_id":          {http.StatusOK, map[string]any{"data": gcpNormalizedEvent{SubjectName: "x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			fakeCollector(t, tc.status, tc.body, nil, nil)
			_, err := normalizeGCPIncident(sc, "acc-1", sampleCloudSQLWebhook)
			assert.Error(t, err)
		})
	}
}

func TestBuildGCPMonitoringWebhookEventUsesCollectorIdentity(t *testing.T) {
	var payload GCPMonitoringWebhookPayload
	require.NoError(t, json.Unmarshal([]byte(sampleCloudSQLWebhook), &payload))
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(sampleCloudSQLWebhook), &raw))

	e := buildGCPMonitoringWebhookEvent(payload.Incident, raw, sampleNormalizedCloudSQL)

	// #34377: the traces enricher scopes Cloud SQL spans by the project:instance subject.
	assert.Equal(t, "example-project:sample-db", e.EventSubjectName)
	assert.Equal(t, "cloudsql_database", e.EventSubjectKind)
	assert.Equal(t, "Cloud SQL", e.EventSubjectNamespace)
	assert.Equal(t, sampleNormalizedCloudSQL.ServiceKey, e.ServiceKey)
	assert.Equal(t, sampleNormalizedCloudSQL.Fingerprint, e.Investigation.Fingerprint)
	assert.Equal(t, sampleNormalizedCloudSQL.FindingId, e.EventId)
	assert.Equal(t, sampleNormalizedCloudSQL.CloudResourceId, e.CloudResourceId)
	assert.Equal(t, "slow-queries", e.Investigation.RuleId)
	assert.Equal(t, sampleNormalizedCloudSQL.Labels, e.Investigation.Labels)
	assert.Equal(t, "triggered", e.EventStatus)
	assert.Equal(t, event.EventStatusFiring, e.Investigation.Status)
	assert.Equal(t, event.EventPriorityMedium, e.Investigation.Severity)
	assert.Equal(t, int64(1790670860), e.EventCreatedAt.Unix())

	require.Len(t, e.Investigation.Evidences, 1)
	evidence := e.Investigation.Evidences[0].Data.(map[string]any)
	assert.Equal(t, raw, evidence["data"], "the evidence keeps the whole delivery")

	resolved := sampleNormalizedCloudSQL
	resolved.Status = "RESOLVED"
	e = buildGCPMonitoringWebhookEvent(payload.Incident, raw, resolved)
	assert.Equal(t, "resolved", e.EventStatus)
	assert.Equal(t, event.EventStatusResolved, e.Investigation.Status)
}

func TestProcessEventWebookRejectsBeforeCallingCollector(t *testing.T) {
	var got http.Request
	var gotBody string
	fakeCollector(t, http.StatusOK, map[string]any{"data": sampleNormalizedCloudSQL}, &got, &gotBody)
	sc := testTenantRequestContext()

	_, err := GCPMonitoringWebhook{}.ProcessEventWebook(sc, nil, "acc-1", `{"incident":{"incident_id":" "}}`)
	assert.Error(t, err)

	_, err = GCPMonitoringWebhook{}.ProcessEventWebook(sc, nil, "", sampleCloudSQLWebhook)
	assert.Error(t, err)

	assert.Empty(t, gotBody, "collector must not be called")
}
