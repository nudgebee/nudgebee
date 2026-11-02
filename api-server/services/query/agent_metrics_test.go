package query

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessAgentMetricsDecodedInFiltersDoNotPanic(t *testing.T) {
	raw := []byte(`{
		"table": "cloud_metric_groupings_v2",
		"columns": [{"name": "timestamp"}],
		"where": {
			"_binary": {
				"account_id": {"_eq": "account-1"},
				"resource_id": {"_in": ["resource-a", "resource-b"]},
				"metric": {"_in": ["CPUUtilization", "NetworkIn"]},
				"service_name": {"_eq": "EC2"}
			}
		}
	}`)

	var input map[string]any
	require.NoError(t, json.Unmarshal(raw, &input))

	var request QueryRequest
	require.NoError(t, common.UnmarshalMapToStruct(input, &request))
	_, ok := request.Where.Binary["resource_id"][In].([]any)
	require.True(t, ok, "JSON _in values should be decoded as []any")
	_, ok = request.Where.Binary["metric"][In].([]any)
	require.True(t, ok, "JSON _in values should be decoded as []any")

	resourceIds, err := convertFilterValueToStrings(request.Where.Binary["resource_id"][In])
	require.NoError(t, err)
	assert.Equal(t, []string{"resource-a", "resource-b"}, resourceIds)
	metrics, err := convertFilterValueToStrings(request.Where.Binary["metric"][In])
	require.NoError(t, err)
	assert.Equal(t, []string{"CPUUtilization", "NetworkIn"}, metrics)

	assert.NotPanics(t, func() {
		processAgentMetrices(request, security.NewRequestContextForSuperAdmin(nil, nil, nil), time.Now())
	})
}

func TestProcessAgentMetricsRejectsInvalidResourceIDIn(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"resource_id":  {In: []any{"resource-a", 42}},
			"service_name": {Eq: "EC2"},
		}},
	}

	response, err := processAgentMetrices(request, security.NewRequestContextForSuperAdmin(nil, nil, nil), time.Now())
	require.NoError(t, err)
	require.Len(t, response.Errors, 1)
	assert.Equal(t, 400, response.Errors[0].Code)
}

func TestProcessAgentMetricsRejectsInvalidMetricIn(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"metric":       {In: []any{"CPUUtilization", false}},
			"service_name": {Eq: "EC2"},
		}},
	}

	response, err := processAgentMetrices(request, security.NewRequestContextForSuperAdmin(nil, nil, nil), time.Now())
	require.NoError(t, err)
	require.Len(t, response.Errors, 1)
	assert.Equal(t, 400, response.Errors[0].Code)
}

func TestProcessAgentMetricsRejectsInvalidResourceIDEq(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"resource_id":  {Eq: 42},
			"service_name": {Eq: "EC2"},
		}},
	}

	response, err := processAgentMetrices(request, security.NewRequestContextForSuperAdmin(nil, nil, nil), time.Now())
	require.NoError(t, err)
	require.Len(t, response.Errors, 1)
	assert.Equal(t, 400, response.Errors[0].Code)
}

func TestProcessAgentMetricsRejectsInvalidMetricEq(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"metric":       {Eq: true},
			"service_name": {Eq: "EC2"},
		}},
	}

	response, err := processAgentMetrices(request, security.NewRequestContextForSuperAdmin(nil, nil, nil), time.Now())
	require.NoError(t, err)
	require.Len(t, response.Errors, 1)
	assert.Equal(t, 400, response.Errors[0].Code)
}

func TestConvertFilterValueToStringsAcceptsNativeStrings(t *testing.T) {
	values, err := convertFilterValueToStrings([]string{"resource-a", "resource-b"})
	require.NoError(t, err)
	assert.Equal(t, []string{"resource-a", "resource-b"}, values)
}
