package services_server

import (
	"nudgebee/llm/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestServiceDependencyGraph(t *testing.T) {
	response, error := GetServiceDependencyGraph(*security.NewRequestContextForSuperAdmin(), os.Getenv("TEST_ACCOUNT"), "nudgebee", "services-server")
	if error != nil {
		t.Error(error)
	}
	assert.Greater(t, len(response.Dependency), 0)
	t.Log(response)
}

func TestServiceQueryLogs(t *testing.T) {
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
	response, error := ListMetricsSeries(*security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")}), os.Getenv("TEST_ACCOUNT"), "prometheus", "memory")
	assert.Nil(t, error)
	assert.NotNil(t, response)
}

func TestServiceQueryMetricsSeriesLabels(t *testing.T) {
	response, error := ListMetricsSeriesLabels(*security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")}), os.Getenv("TEST_ACCOUNT"), "prometheus", "container_info")
	if error != nil {
		t.Error(error)
	}
	assert.Greater(t, len(response.Labels), 0)
	t.Log(response)
}

func TestGetObservabilityProvider(t *testing.T) {
	rc := *security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	data, err := GetObservabilityProvider(rc, os.Getenv("TEST_ACCOUNT"), "logs")
	assert.NotEmpty(t, data)
	assert.Nil(t, err)
}
