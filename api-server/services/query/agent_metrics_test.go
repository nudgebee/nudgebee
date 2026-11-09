package query

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessAgentMetricsDecodedInFiltersPassToCloud(t *testing.T) {
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

	var captured cloud.QueryMetricsRequest
	patches := gomonkey.NewPatches()
	t.Cleanup(patches.Reset)
	patches.ApplyFunc(cloud.QueryMetrics, func(_ *security.RequestContext, request cloud.QueryMetricsRequest) (cloud.QueryMetricsResponse, error) {
		captured = request
		return cloud.QueryMetricsResponse{}, nil
	})

	response, err := processAgentMetrices(request, security.NewRequestContextForSuperAdmin(nil, nil, nil), time.Now())
	require.NoError(t, err)
	assert.Empty(t, response.Errors)
	assert.Equal(t, []string{"resource-a", "resource-b"}, captured.Query.ResourceIds)
	assert.Equal(t, []string{"CPUUtilization", "NetworkIn"}, captured.Query.MetricNames)
}

func assertAgentMetricsRejectsWithoutCloud(t *testing.T, request QueryRequest, expectedCode int) {
	t.Helper()
	cloudCalled := false
	patches := gomonkey.NewPatches()
	t.Cleanup(patches.Reset)
	patches.ApplyFunc(cloud.QueryMetrics, func(_ *security.RequestContext, _ cloud.QueryMetricsRequest) (cloud.QueryMetricsResponse, error) {
		cloudCalled = true
		return cloud.QueryMetricsResponse{}, nil
	})

	response, err := processAgentMetrices(request, security.NewRequestContextForSuperAdmin(nil, nil, nil), time.Now())
	require.NoError(t, err)
	require.Len(t, response.Errors, 1)
	assert.Equal(t, expectedCode, response.Errors[0].Code)
	assert.False(t, cloudCalled)
}

func TestProcessAgentMetricsRejectsInvalidResourceIDIn(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"resource_id":  {In: []any{"resource-a", 42}},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidMetricIn(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"metric":       {In: []any{"CPUUtilization", false}},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidResourceIDEq(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"resource_id":  {Eq: 42},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidMetricEq(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"metric":       {Eq: true},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidResourceIDInScalar(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"resource_id":  {In: "resource-a"},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidMetricInScalar(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"metric":       {In: "CPUUtilization"},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidResourceIDEqArray(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"resource_id":  {Eq: []any{"resource-a", "resource-b"}},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidMetricEqArray(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"metric":       {Eq: []any{"CPUUtilization", "NetworkIn"}},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsEmptyResourceIDIn(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"resource_id":  {In: []any{}},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsEmptyMetricIn(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"metric":       {In: []string{}},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidAccountID(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: 42},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 403)
}

func TestProcessAgentMetricsRejectsInvalidResourceType(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":    {Eq: "account-1"},
			"resource_type": {Eq: 42},
			"service_name":  {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidRegionName(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"region_name":  {Eq: 42},
			"service_name": {Eq: "EC2"},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestProcessAgentMetricsRejectsInvalidServiceName(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"account_id":   {Eq: "account-1"},
			"service_name": {Eq: 42},
		}},
	}

	assertAgentMetricsRejectsWithoutCloud(t, request, 400)
}

func TestConvertFilterValueToStringsAcceptsNativeStrings(t *testing.T) {
	values, err := convertFilterValueToStrings([]string{"resource-a", "resource-b"})
	require.NoError(t, err)
	assert.Equal(t, []string{"resource-a", "resource-b"}, values)
}
