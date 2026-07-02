package services_server

import (
	"nudgebee/llm/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// requireLiveServices skips tests that need a live services-server / observability
// backend and a populated tenant+account. They read TEST_TENANT / TEST_ACCOUNT and
// would otherwise hit a refused DB connection (or nil-deref on an empty tenant).
func requireLiveServices(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("requires a live services-server backend; set TEST_TENANT and TEST_ACCOUNT to run")
	}
}

func TestServiceDependencyGraph(t *testing.T) {
	requireLiveServices(t)
	response, error := GetServiceDependencyGraph(*security.NewRequestContextForSuperAdmin(), os.Getenv("TEST_ACCOUNT"), "nudgebee", "services-server")
	if error != nil {
		t.Error(error)
	}
	assert.Greater(t, len(response.Dependency), 0)
	t.Log(response)
}

func TestServiceQueryLogs(t *testing.T) {
	requireLiveServices(t)
	startTime := time.Now().UTC().Add(-1 * time.Hour)
	endTime := time.Now().UTC()

	response, error := QueryLogs(*security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT")), LogQueryRequest{
		AccountId:   os.Getenv("TEST_ACCOUNT"),
		LogProvider: "loki",
		Query:       "query={stream=\"stdout\"}",
		StartTime:   startTime.UnixMilli(),
		EndTime:     endTime.UnixMilli(),
		Limit:       1000,
		Offset:      0,
		Request:     map[string]any{},
	})
	if error != nil {
		t.Error(error)
	}
	assert.Greater(t, len(response.Logs), 0)
	t.Log(response)
}

func TestServiceQueryLogLabels(t *testing.T) {
	requireLiveServices(t)
	response, error := QueryLogLabels(*security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")}), os.Getenv("TEST_ACCOUNT"), ObservabilityProvider{
		Provider:          "ES",
		IntegrationSource: "agent",
	})
	if error != nil {
		t.Error(error)
	}
	assert.Greater(t, len(response.Labels), 0)
	t.Log(response)
}

func TestServiceQueryMetricsSeries(t *testing.T) {
	requireLiveServices(t)
	response, error := ListMetricsSeries(*security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")}), os.Getenv("TEST_ACCOUNT"), "prometheus", "memory")
	assert.Nil(t, error)
	assert.NotNil(t, response)
}

func TestServiceQueryMetricsSeriesLabels(t *testing.T) {
	requireLiveServices(t)
	response, error := ListMetricsSeriesLabels(*security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")}), os.Getenv("TEST_ACCOUNT"), "prometheus", "container_info")
	if error != nil {
		t.Error(error)
	}
	assert.Greater(t, len(response.Labels), 0)
	t.Log(response)
}

func TestGetObservabilityProvider(t *testing.T) {
	requireLiveServices(t)
	rc := *security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	data, err := GetObservabilityProvider(rc, os.Getenv("TEST_ACCOUNT"), "logs")
	assert.NotEmpty(t, data)
	assert.Nil(t, err)
}
