package observability

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/testenv"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrometheusEnricherAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	prometheusEnricherAction := prometheusAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{
		Name:        "HighFileSystemUtilizationNbDev",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartedAt:   nil,
		EndedAt:     nil,
	})
	response, err := prometheusEnricherAction.Execute(defaultPlaybookActionContext, map[string]any{
		"instant":      false,
		"duration":     map[string]any{},
		"step":         "1m",
		"promql_query": "",
		"promql_queries": []playbooks.NamedQuery{
			{
				Key:   "A",
				Query: `system.filesystem.utilization{host.name="nb-dev-db"}`,
			},
		},
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

func TestJsonSerialization(t *testing.T) {
	a1 := []playbooks.PlaybookActionResponse{}
	a1 = append(a1, playbooks.NewPlaybookActionResponseJson("test", map[string]any{}, nil, nil))
	a1 = append(a1, playbooks.PrometheusActionResponse{Metadata: map[string]any{}, Data: map[string]any{}, AdditionalInfo: map[string]any{}})
	bytes, err := common.MarshalJson(a1)
	assert.NoError(t, err)
	assert.Greater(t, len(bytes), 0)
	print(string(bytes))
}

func TestFormatRelativeTime(t *testing.T) {
	tests := []struct {
		name     string
		duration int
		expected string
	}{
		{"10 minutes", 10, "10m"},
		{"45 minutes", 45, "45m"},
		{"59 minutes", 59, "59m"},
		{"60 minutes (1 hour)", 60, "1h"},
		{"120 minutes (2 hours)", 120, "2h"},
		{"1439 minutes", 1439, "23h"},
		{"1440 minutes (1 day)", 1440, "1d"},
		{"2880 minutes (2 days)", 2880, "2d"},
		{"10080 minutes (7 days)", 10080, "7d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRelativeTime(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildChronosphereMetricsExplorerURL_SingleQuery(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{
		{
			Key:   "A",
			Query: `rate(rails_requests_total{status=~"^[4].*",environment=~"production"}[5m])`,
		},
	}
	durationMinutes := 60

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.Nil(t, err, "Should not return an error")
	assert.NotEmpty(t, url, "URL should not be empty")
	assert.Contains(t, url, baseURL, "URL should contain base URL")
	assert.Contains(t, url, "/metrics/explorer-v2", "URL should contain metrics explorer path")
	assert.Contains(t, url, "queries=", "URL should contain queries parameter")
	assert.Contains(t, url, "start=1h", "URL should contain start=1h for 60 minutes")
	assert.Contains(t, url, "formulas=[]", "URL should contain formulas parameter")

	t.Logf("Generated URL: %s", url)
}

func TestBuildChronosphereMetricsExplorerURL_MultipleQueries(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{
		{
			Key:   "A",
			Query: `rate(rails_requests_total{status=~"^[4].*"}[5m])`,
		},
		{
			Key:   "B",
			Query: `rate(rails_requests_total{status=~"^[5].*"}[5m])`,
		},
	}
	durationMinutes := 15

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.Nil(t, err, "Should not return an error")
	assert.NotEmpty(t, url, "URL should not be empty")
	assert.Contains(t, url, "start=15m", "URL should contain start=15m for 15 minutes")

	t.Logf("Generated URL with multiple queries: %s", url)
}

func TestBuildChronosphereMetricsExplorerURL_NoQueries(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{}
	durationMinutes := 60

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.NotNil(t, err, "Should return an error")
	assert.Empty(t, url, "URL should be empty on error")
	assert.Contains(t, err.Error(), "no PromQL queries", "Error should mention no queries")
}

func TestBuildChronosphereMetricsExplorerURL_ComplexQuery(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{
		{
			Key:   "A",
			Query: ` (sum(rate(rails_requests_total{status=~"^[4].*",environment=~"production", service_name=~"geo-service"}[5m])) by (service_name,environment, status)/sum(rate(rails_requests_total{environment=~"production", service_name=~"geo-service"}[5m])) by (service_name,environment,status)) *100 > 1`,
		},
	}
	durationMinutes := 60

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.Nil(t, err, "Should not return an error")
	assert.NotEmpty(t, url, "URL should not be empty")
	assert.Contains(t, url, baseURL, "URL should contain base URL")
	assert.Contains(t, url, "/metrics/explorer-v2", "URL should contain metrics explorer path")
	assert.Contains(t, url, "queries=", "URL should contain queries parameter")
	assert.Contains(t, url, "start=1h", "URL should contain start=1h for 60 minutes")
	assert.Contains(t, url, "formulas=[]", "URL should contain formulas parameter")

	// Verify the query structure is correct
	assert.Contains(t, url, "DataQuery", "URL should contain DataQuery kind")
	assert.Contains(t, url, "PrometheusTimeSeriesQuery", "URL should contain PrometheusTimeSeriesQuery kind")
	assert.Contains(t, url, "rails_requests_total", "URL should contain the actual query metric")

	t.Logf("Generated URL for complex query: %s", url)
}
