package playbooks

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testServiceName = "api-gateway"
)

func TestDatadogMonitorsSearchAction(t *testing.T) {
	action := &datadogMonitorsSearchAction{}
	ctx := NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil, PlaybookEvent{})

	// Test with default parameters (status: alert)
	params := map[string]any{}

	// This will fail with Datadog configuration error, which is expected
	// (requires database connection to fetch integration config)
	_, err := action.Execute(ctx, params)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "Datadog configuration")

	// Test with custom status, env, and service
	params = map[string]any{
		"status":   "alert",
		"env":      "production",
		"service":  testServiceName,
		"limit":    10,
		"duration": 2,
	}

	_, err = action.Execute(ctx, params)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "Datadog configuration")
}

func TestParseDatadogMonitorsParams(t *testing.T) {
	// Test with all parameters
	ctx := NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil, PlaybookEvent{})
	rawParams := map[string]any{
		"status":   "alert",
		"env":      "production",
		"query":    "status:alert env:prod",
		"limit":    float64(50),
		"duration": 2,
	}

	params, err := parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "alert", params.Status)
	assert.Equal(t, "production", params.Env)
	assert.Equal(t, "status:alert env:prod", params.Query)
	assert.Equal(t, 50, params.Limit)
	assert.Equal(t, 2, params.Duration)

	// Test with missing parameters
	rawParams = map[string]any{}
	params, err = parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "", params.Status)
	assert.Equal(t, "", params.Env)
	assert.Equal(t, "", params.Service)
	assert.Equal(t, 0, params.Limit)
	assert.Equal(t, 0, params.Duration)

	// Test with event labels (env and service from context)
	event := PlaybookEvent{
		Labels: map[string]string{
			"env":     "staging",
			"service": testServiceName,
		},
	}
	ctx = NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil, event)
	rawParams = map[string]any{
		"status": "warn",
	}
	params, err = parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "warn", params.Status)
	assert.Equal(t, "staging", params.Env)
	assert.Equal(t, testServiceName, params.Service)

	// Test that rawParams override event labels
	rawParams = map[string]any{
		"env": "production",
	}
	params, err = parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "production", params.Env) // rawParams should override
	assert.Equal(t, testServiceName, params.Service)
}

func TestBuildDatadogQuery(t *testing.T) {
	// Test with status and env
	params := datadogMonitorsSearchParams{
		Status: "alert",
		Env:    "production",
	}
	query := buildDatadogQuery(params)
	assert.Contains(t, query, "status:alert")
	assert.Contains(t, query, "env:production")

	// Test with status, env, and service
	params = datadogMonitorsSearchParams{
		Status:  "alert",
		Env:     "production",
		Service: "web-api",
	}
	query = buildDatadogQuery(params)
	assert.Contains(t, query, "status:alert")
	assert.Contains(t, query, "env:production")
	assert.Contains(t, query, "service:web-api")

	// Test with custom query
	params = datadogMonitorsSearchParams{
		Query: "custom query string",
	}
	query = buildDatadogQuery(params)
	assert.Equal(t, "custom query string", query)

	// Test with only status
	params = datadogMonitorsSearchParams{
		Status: "warn",
	}
	query = buildDatadogQuery(params)
	assert.Equal(t, "status:warn", query)

	// Test with only service
	params = datadogMonitorsSearchParams{
		Service: "payment-service",
	}
	query = buildDatadogQuery(params)
	assert.Equal(t, "service:payment-service", query)
}
