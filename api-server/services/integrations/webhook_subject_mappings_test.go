package integrations

import (
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatSubjectMappingsForPrompt_Empty(t *testing.T) {
	result := FormatSubjectMappingsForPrompt(nil, 50)
	assert.Equal(t, "(No historical data available)", result)
}

func TestFormatSubjectMappingsForPrompt_NilServiceSkipped(t *testing.T) {
	mappings := []HistoricalIncident{
		{Title: "Alert A", Service: nil},
		{Title: "Alert B", Service: strPtr("")},
	}
	result := FormatSubjectMappingsForPrompt(mappings, 50)
	assert.Equal(t, "(No historical data available)", result)
}

func TestFormatSubjectMappingsForPrompt_FormatsCorrectly(t *testing.T) {
	mappings := []HistoricalIncident{
		{Title: "Rating Down", Service: strPtr("rating-service")},
		{Title: "STS 500 errors", Service: strPtr("shipment-tracking-service")},
	}
	result := FormatSubjectMappingsForPrompt(mappings, 50)
	assert.Contains(t, result, `"Rating Down" → "rating-service"`)
	assert.Contains(t, result, `"STS 500 errors" → "shipment-tracking-service"`)
}

func TestFormatSubjectMappingsForPrompt_RespectsLimit(t *testing.T) {
	mappings := []HistoricalIncident{
		{Title: "Alert 1", Service: strPtr("svc-1")},
		{Title: "Alert 2", Service: strPtr("svc-2")},
		{Title: "Alert 3", Service: strPtr("svc-3")},
	}
	result := FormatSubjectMappingsForPrompt(mappings, 2)
	assert.Contains(t, result, "svc-1")
	assert.Contains(t, result, "svc-2")
	assert.NotContains(t, result, "svc-3")
}

func TestExtractSubjectFromZendutySummary_NudgebeeFormat(t *testing.T) {
	summary := `**Title**: Job Failed
**Priority**: HIGH
**Aggregation Key**: job_failure
**Subject Type**: job
**Subject Name**: nudgebee-image-scanner-d3067c75
**Subject Namespace**: nudgebee-agent

[Created by: User(user@example.com)]`

	result := extractSubjectFromZendutySummary(summary)
	assert.Equal(t, "nudgebee-image-scanner-d3067c75", result)
}

func TestExtractSubjectFromZendutySummary_AlertmanagerFormat(t *testing.T) {
	summary := `Labels:
- alertname = KubeDeploymentReplicasMismatch
- cluster = cluster-name
- deployment = accounting
- namespace = demo
- pod = victoria-kube-state-metrics-c868b7fdb-d565g`

	result := extractSubjectFromZendutySummary(summary)
	assert.Equal(t, "accounting", result)
}

func TestExtractSubjectFromZendutySummary_PodFallback(t *testing.T) {
	summary := `Labels:
- alertname = KubePodCrashLooping
- namespace = zulu-backend-prod
- pod = zulu-backend-group2-prod-5fc57d8856-bbvls`

	result := extractSubjectFromZendutySummary(summary)
	assert.Equal(t, "zulu-backend-group2-prod-5fc57d8856-bbvls", result)
}

func TestExtractSubjectFromZendutySummary_Empty(t *testing.T) {
	assert.Equal(t, "", extractSubjectFromZendutySummary(""))
	assert.Equal(t, "", extractSubjectFromZendutySummary("some random text with no labels"))
}

// TestSyncPagerDutyFetchAndMerge is an integration test that directly calls
// fetchPagerDutyResolvedIncidents + mergeAndSaveMappings synchronously.
// Requires a running database with a valid PagerDuty integration.
func TestSyncPagerDutyFetchAndMerge(t *testing.T) {
	userId := "5b195690-b393-4937-ab07-690a751f40c5"
	tenantId := "890cad87-c452-4aa7-b84a-742cee0454a1"

	sc := security.NewRequestContextForUserTenant(userId, tenantId, nil, nil, nil)

	apiToken, err := getPagerDutyAPIToken(sc, tenantId)
	if err != nil {
		t.Logf("No PagerDuty integration configured, skipping: %v", err)
		return
	}

	fetched, err := fetchPagerDutyResolvedIncidents(sc, apiToken, 90)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	t.Logf("Fetched %d mappings from PagerDuty", len(fetched))

	for i, m := range fetched {
		if i >= 10 {
			t.Logf("  ... and %d more", len(fetched)-10)
			break
		}
		t.Logf("  %q → %q", m.Title, m.Service)
	}

	if len(fetched) == 0 {
		t.Log("No mappings found — incidents may not have deployment labels in body.details")
		return
	}

	// Convert to incidentMapping for saving
	var mappings []incidentMapping
	for _, f := range fetched {
		if f.Service != "" {
			mappings = append(mappings, incidentMapping{Title: f.Title, Service: f.Service})
		}
	}

	resp, err := mergeAndSaveMappings(sc, tenantId, TenantAttrPagerDutyIncidentsKey, mappings)
	assert.NoError(t, err)
	assert.Equal(t, "success", resp.Status)
	t.Logf("Saved to tenant_attrs: %d new mappings", resp.SyncedCount)
}

func strPtr(s string) *string {
	return &s
}
