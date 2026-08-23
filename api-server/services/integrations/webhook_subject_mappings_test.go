package integrations

import (
	"fmt"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAddServiceToList(t *testing.T) {
	tests := []struct {
		name        string
		existing    string
		service     string
		wantUpdated string
		wantChanged bool
	}{
		{"empty existing", "", "svc-a", "svc-a", true},
		{"new service appended", "svc-a", "svc-b", "svc-a,svc-b", true},
		{"exact duplicate is a no-op", "svc-a,svc-b", "svc-b", "svc-a,svc-b", false},
		{"duplicate with surrounding whitespace is a no-op", "svc-a, svc-b", "svc-b", "svc-a, svc-b", false},
		{"case-sensitive mismatch is appended", "svc-a", "SVC-A", "svc-a,SVC-A", true},
		{"appending under the cap keeps everything", "svc-1,svc-2,svc-3,svc-4", "svc-5", "svc-1,svc-2,svc-3,svc-4,svc-5", true},
		{"appending at the cap evicts the oldest", "svc-1,svc-2,svc-3,svc-4,svc-5", "svc-6", "svc-2,svc-3,svc-4,svc-5,svc-6", true},
		{"duplicate at the cap is still a no-op", "svc-1,svc-2,svc-3,svc-4,svc-5", "svc-3", "svc-1,svc-2,svc-3,svc-4,svc-5", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated, changed := addServiceToList(tt.existing, tt.service)
			assert.Equal(t, tt.wantUpdated, updated)
			assert.Equal(t, tt.wantChanged, changed)
		})
	}
}

// TestNormalizeDatadogServicesFieldValue covers the bug found while testing
// BackfillWebhookSubjectMappings: a legacy tenant_attrs row had a raw k8s
// facet string (cluster/namespace/pod tags) as its "service" value instead of
// the clean pod name, because fetchDatadogResolvedIncidentsWithFields
// trusted the Datadog incident's raw "Services" field verbatim. This asserts
// normalizeDatadogServicesFieldValue now routes a facet-shaped value through
// parseDatadogK8sSubject instead of storing it as-is.
func TestNormalizeDatadogServicesFieldValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "raw k8s facet string resolves to the pod name",
			input: "kube_cluster_name:example-cluster,kube_namespace:opensearch,pod_name:fluent-bit-69lnm",
			want:  "fluent-bit-69lnm",
		},
		{
			name:  "facet string with a resolvable controller prefers the controller",
			input: "kube_deployment:my-app,kube_namespace:ns,pod_name:my-app-6f9d8-abcde",
			want:  "my-app",
		},
		{
			name:  "already-clean service name is returned unchanged",
			input: "opensearch",
			want:  "opensearch",
		},
		{
			name:  "colon-containing value with no recognized facet key falls back to the original value",
			input: "arn:aws:lambda:us-east-1:12345:function:my-func",
			want:  "arn:aws:lambda:us-east-1:12345:function:my-func",
		},
		{
			name:  "empty input stays empty",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeDatadogServicesFieldValue(tt.input)
			t.Logf("======= %s =======", got)
			assert.Equal(t, tt.want, got)
		})
	}
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
	tenantId, _, userId := testenv.RequireTenant(t)

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
	t.Logf("Saved to webhook_subject_mappings: %d new mappings", resp.SyncedCount)
}

func TestUpsertSubjectMapping(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenantId := testenv.RequireTenantID(t)

	attrKey := "TEST_UPSERT_SUBJECT_MAPPING"
	title := "Test Upsert Title " + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(`DELETE FROM webhook_subject_mappings WHERE tenant_id = $1 AND attr_key = $2`, tenantId, attrKey)
	})

	isNew, changed, err := upsertSubjectMapping(dbms, tenantId, attrKey, title, "service-a")
	assert.NoError(t, err)
	assert.True(t, isNew, "first write for a title should be new")
	assert.True(t, changed)

	isNew, changed, err = upsertSubjectMapping(dbms, tenantId, attrKey, title, "service-a")
	assert.NoError(t, err)
	assert.False(t, isNew)
	assert.False(t, changed, "re-adding the same service should be a no-op")

	isNew, changed, err = upsertSubjectMapping(dbms, tenantId, attrKey, title, "service-b")
	assert.NoError(t, err)
	assert.False(t, isNew)
	assert.True(t, changed, "a second distinct service should be appended")

	var row subjectMappingRow
	err = dbms.Db.Get(&row,
		`SELECT id, services FROM webhook_subject_mappings WHERE tenant_id = $1 AND attr_key = $2 AND lower(title) = lower($3)`,
		tenantId, attrKey, title)
	assert.NoError(t, err)
	assert.Equal(t, "service-a,service-b", row.Services)
}

// TestUpsertSubjectMapping_ConcurrentNewTitle exercises the race the
// ON CONFLICT DO NOTHING + fallback-to-update path in upsertSubjectMapping
// exists for: many writers racing to be the first to record a brand-new
// title (e.g. LearnSubjectMapping's per-webhook goroutine firing concurrently
// with itself or with a sync job) must all succeed, with exactly one insert
// and the rest appending, rather than one erroring on a unique-violation.
// writers is kept below maxServicesPerTitle so eviction (covered separately
// by TestAddServiceToList) doesn't make the final count non-deterministic.
func TestUpsertSubjectMapping_ConcurrentNewTitle(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenantId := testenv.RequireTenantID(t)

	attrKey := "TEST_UPSERT_SUBJECT_MAPPING_CONCURRENT"
	title := "Concurrent Title " + time.Now().Format(time.RFC3339Nano)
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(`DELETE FROM webhook_subject_mappings WHERE tenant_id = $1 AND attr_key = $2`, tenantId, attrKey)
	})

	const writers = maxServicesPerTitle - 1
	var wg sync.WaitGroup
	errs := make([]error, writers)
	isNewFlags := make([]bool, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			isNew, _, err := upsertSubjectMapping(dbms, tenantId, attrKey, title, fmt.Sprintf("service-%d", i))
			errs[i] = err
			isNewFlags[i] = isNew
		}(i)
	}
	wg.Wait()

	newCount := 0
	for i, err := range errs {
		assert.NoError(t, err, "writer %d should not error", i)
		if isNewFlags[i] {
			newCount++
		}
	}
	assert.Equal(t, 1, newCount, "exactly one concurrent writer should have inserted the row")

	var row subjectMappingRow
	err := dbms.Db.Get(&row,
		`SELECT id, services FROM webhook_subject_mappings WHERE tenant_id = $1 AND attr_key = $2 AND lower(title) = lower($3)`,
		tenantId, attrKey, title)
	assert.NoError(t, err)
	assert.Len(t, strings.Split(row.Services, ","), writers, "every concurrent writer's service should be recorded")
}

// TestResolveIncidentsWithLLM_ExcludesDeterministicMatches asserts that
// resolveIncidentsWithLLM never returns a mapping for an incident that already
// had a deterministic Service (e.g. a Datadog "services" field or a PagerDuty
// Labels: block) — those came from structured alert metadata, not the title's
// wording, so they aren't a genuine title->service pattern and must not be
// learned into webhook_subject_mappings. The Service is only used to skip an
// unnecessary LLM call; every incident here already has one, so no LLM call
// happens regardless of the tenant's FEATURE_WEBHOOK_LLM_RESOLUTION setting,
// and the result must be empty either way.
func TestResolveIncidentsWithLLM_ExcludesDeterministicMatches(t *testing.T) {
	testenv.RequireMetastore(t)
	tenantId, accountId, userId := testenv.RequireTenant(t)
	sc := security.NewRequestContextForUserTenant(userId, tenantId, nil, nil, nil)

	incidents := []incidentWithFields{
		{Title: "Deterministically Resolved Title", Service: "already-known-service"},
	}

	result := resolveIncidentsWithLLM(sc, incidents, accountId, IntegrationDatadogWebhook, nil)

	assert.Empty(t, result, "deterministic-only incidents must not be saved as learned mappings")
}
