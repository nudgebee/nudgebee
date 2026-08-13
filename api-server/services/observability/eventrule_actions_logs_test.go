package observability

import (
	"encoding/json"
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/query"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignozAction(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	signozAction := signozLogsAction{}
	startTime := time.UnixMilli(1755775266000)
	endTime := time.UnixMilli(1755778866404)
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(env[testenv.Tenant], env[testenv.Account], slog.Default(), playbooks.PlaybookEvent{
		Name:        "TestSignozAlert",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartedAt:   &startTime,
		EndedAt:     &endTime,
	})
	response, err := signozAction.Execute(defaultPlaybookActionContext, map[string]any{
		"query": []map[string]any{
			{
				"value": "dc-manager-indexer-service",
				"op":    "=",
				"key": map[string]any{
					"key": "service.name",
				},
			},
			{
				"op": "contains",
				"key": map[string]any{
					"key": "body",
				},
				"value": "status=500",
			},
		},
		"regex_extractors": []RegexLabelExtractor{
			{
				Pattern:   `([a-zA-Z0-9]{20})\s`,
				LabelName: "task_id",
			},
			{
				Pattern:   `status=(\d+)`,
				LabelName: "status_code",
			},
			{
				Pattern:   `method=(\w+)`,
				LabelName: "http_method",
			},
			{
				Pattern:   `path=([^\s]+)`,
				LabelName: "request_path",
			},
		},
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)

	// Test that the response implements PlaybookActionResponseLabelExtractor
	labelExtractor, ok := response.(playbooks.PlaybookActionResponseLabelExtractor)
	assert.True(t, ok, "Response should implement PlaybookActionResponseLabelExtractor")

	// Test label extraction capability
	extractedLabels := labelExtractor.ExtractLabels()
	assert.NotNil(t, extractedLabels, "ExtractLabels should return a map")

	// Log extracted labels for debugging
	t.Logf("Extracted labels: %+v", extractedLabels)
}

func TestBuildSignozLogsExplorerURL(t *testing.T) {
	baseURL := "https://telemetry.example.com"
	startTime := time.UnixMilli(1766406993000)
	endTime := time.UnixMilli(1766410593000)

	rawQuery := map[string]any{
		"compositeQuery": map[string]any{
			"builderQueries": map[string]any{
				"A": map[string]any{
					"aggregateAttribute": map[string]any{},
					"aggregateOperator":  "noop",
					"dataSource":         "logs",
					"filters": map[string]any{
						"items": []map[string]any{
							{
								"id":    "5736e4f9",
								"key":   map[string]any{"key": "service.name"},
								"op":    "=",
								"value": "tracking-service",
							},
						},
						"op": "AND",
					},
					"orderBy": []map[string]any{
						{"columnName": "timestamp", "order": "desc"},
					},
				},
			},
			"fillGaps":  false,
			"panelType": "list",
			"queryType": "builder",
		},
		"end":       endTime.UnixMilli(),
		"start":     startTime.UnixMilli(),
		"step":      60,
		"variables": map[string]any{},
	}

	url, err := buildSignozLogsExplorerURL(baseURL, rawQuery, startTime, endTime)

	assert.Nil(t, err, "Should not return an error")
	assert.NotEmpty(t, url, "URL should not be empty")
	assert.Contains(t, url, baseURL, "URL should contain base URL")
	assert.Contains(t, url, "/logs/logs-explorer", "URL should contain logs-explorer path")
	assert.Contains(t, url, "compositeQuery=", "URL should contain compositeQuery parameter")
	assert.Contains(t, url, "timeRange=", "URL should contain timeRange parameter")
	assert.Contains(t, url, "options=", "URL should contain options parameter")

	t.Logf("Generated URL: %s", url)
}

func TestBuildSignozLogsExplorerURL_MissingCompositeQuery(t *testing.T) {
	baseURL := "https://telemetry.example.com"
	startTime := time.UnixMilli(1766406993000)
	endTime := time.UnixMilli(1766410593000)

	rawQuery := map[string]any{
		// Missing compositeQuery
		"end":       endTime.UnixMilli(),
		"start":     startTime.UnixMilli(),
		"step":      60,
		"variables": map[string]any{},
	}

	url, err := buildSignozLogsExplorerURL(baseURL, rawQuery, startTime, endTime)

	assert.NotNil(t, err, "Should return an error")
	assert.Empty(t, url, "URL should be empty on error")
	assert.Contains(t, err.Error(), "compositeQuery not found", "Error should mention compositeQuery")
}

// TestFetchLogsViaRelay_DeploymentUsesKubectl is an integration test that verifies
// fetchLogsViaRelay routes Deployment-kind events through kubectl_command_executor
// instead of logs_enricher.
//
// Required env vars:
//
//	TEST_TENANT     - tenant ID
//	TEST_ACCOUNT    - account ID with a connected agent
//	TEST_DEPLOYMENT - name of an existing deployment in the cluster (default: relay-server)
//	TEST_NAMESPACE  - namespace of that deployment (default: nudgebee)
func TestFetchLogsViaRelay_DeploymentUsesKubectl(t *testing.T) {
	tenant := os.Getenv("TEST_TENANT")
	accountID := os.Getenv("TEST_ACCOUNT")
	deploymentName := os.Getenv("TEST_DEPLOYMENT")
	namespace := os.Getenv("TEST_NAMESPACE")

	if tenant == "" || accountID == "" {
		t.Skip("Skipping integration test: TEST_TENANT, TEST_ACCOUNT must be set")
	}
	if deploymentName == "" {
		deploymentName = "relay-server"
	}
	if namespace == "" {
		namespace = "nudgebee"
	}

	action := &observabilityLogAction{}
	ctx := playbooks.NewPlaybookActionContext(tenant, accountID, slog.Default(), playbooks.PlaybookEvent{
		Name:        "TestDeploymentLogFetch",
		SubjectName: deploymentName,
		Labels: map[string]string{
			"kind":      "Deployment",
			"namespace": namespace,
		},
	})

	response, err := action.fetchLogsViaRelay(ctx, deploymentName, namespace)
	require.NoError(t, err, "fetchLogsViaRelay should not return error for Deployment")
	require.NotNil(t, response, "response should not be nil")

	t.Logf("Response type: %T", response)
	t.Logf("Response: %+v", response)
}

// TestExtractKubectlOutput covers the response-parser that unwraps the JsonBlock
// shape emitted by the agent's kubectl_command_executor action.
//
// The agent returns:
//
//	{"type":"json","data":"{\"command\":...,\"stdout\":...,\"stderr\":...}","additional_info":...}
//
// where stdout/stderr live one level deep inside a stringified JSON. Previously,
// fetchLogsViaKubectl looked for "stdout" at the top level and always treated the
// reply as empty, logging "kubectl logs returned no output" even when the command
// succeeded.
func TestExtractKubectlOutput(t *testing.T) {
	const payload = `{"command":"kubectl logs deployment/ad -n demo --tail=5 --all-containers=true","stdout":"line-1\nline-2\n","stderr":""}`

	tests := []struct {
		name           string
		response       map[string]any
		expectedStdout string
		expectedStderr string
	}{
		{
			name: "JsonBlock shape from kubectl_command_executor",
			response: map[string]any{
				"type":            "json",
				"data":            payload,
				"additional_info": map[string]any{},
			},
			expectedStdout: "line-1\nline-2\n",
			expectedStderr: "",
		},
		{
			name: "fast-path: stdout already at top level",
			response: map[string]any{
				"stdout": "already-flat",
				"stderr": "warn",
			},
			expectedStdout: "already-flat",
			expectedStderr: "warn",
		},
		{
			name: "nested carries stderr too",
			response: map[string]any{
				"type": "json",
				"data": `{"command":"kubectl ...","stdout":"out","stderr":"boom"}`,
			},
			expectedStdout: "out",
			expectedStderr: "boom",
		},
		{
			name: "empty nested payload yields empty strings without error",
			response: map[string]any{
				"type": "json",
				"data": `{"command":"kubectl ...","stdout":"","stderr":""}`,
			},
			expectedStdout: "",
			expectedStderr: "",
		},
		{
			name: "missing data field returns empty without panic",
			response: map[string]any{
				"type": "json",
			},
			expectedStdout: "",
			expectedStderr: "",
		},
		{
			name: "invalid inner JSON silently yields empty",
			response: map[string]any{
				"type": "json",
				"data": "not-json",
			},
			expectedStdout: "",
			expectedStderr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := extractKubectlOutput(tc.response)
			assert.Equal(t, tc.expectedStdout, stdout, "stdout")
			assert.Equal(t, tc.expectedStderr, stderr, "stderr")
		})
	}
}

func TestParseLogTextToOutputLogs(t *testing.T) {
	t.Run("json lines lift timestamp/level/message; full object retained in labels", func(t *testing.T) {
		text := `{"time":"2026-06-09T03:08:15.378710783Z","level":"INFO","msg":"anomaly: no anomalies found","account":"ff87"}`
		entries := parseLogTextToOutputLogs(text)
		require.Len(t, entries, 1)
		assert.Equal(t, "2026-06-09T03:08:15.378710783Z", entries[0].Timestamp)
		assert.Equal(t, "anomaly: no anomalies found", entries[0].Message)
		assert.Equal(t, "INFO", entries[0].Severity)
		assert.Equal(t, "ff87", entries[0].Labels["account"])
	})

	t.Run("plain text line becomes message with guessed level and no timestamp", func(t *testing.T) {
		entries := parseLogTextToOutputLogs("10.14.128.5:5432 - accepting connections")
		require.Len(t, entries, 1)
		assert.Empty(t, entries[0].Timestamp)
		assert.Equal(t, "10.14.128.5:5432 - accepting connections", entries[0].Message)
		assert.Nil(t, entries[0].Labels)
	})

	t.Run("blank lines are skipped and mixed lines parse independently", func(t *testing.T) {
		text := "first plain line\n\n   \n{\"time\":\"t1\",\"message\":\"second\",\"severity\":\"error\"}\n"
		entries := parseLogTextToOutputLogs(text)
		require.Len(t, entries, 2)
		assert.Equal(t, "first plain line", entries[0].Message)
		assert.Equal(t, "second", entries[1].Message)
		assert.Equal(t, "t1", entries[1].Timestamp)
		assert.Equal(t, "ERROR", entries[1].Severity)
	})

	t.Run("numeric timestamp/level fields are coerced, not dropped", func(t *testing.T) {
		entries := parseLogTextToOutputLogs(`{"ts":1717900000,"level":3,"message":"numeric fields"}`)
		require.Len(t, entries, 1)
		assert.Equal(t, "1717900000", entries[0].Timestamp)
		assert.Equal(t, "3", entries[0].Severity)
		assert.Equal(t, "numeric fields", entries[0].Message)
	})

	t.Run("empty input yields empty slice", func(t *testing.T) {
		assert.Empty(t, parseLogTextToOutputLogs(""))
	})

	t.Run("malformed json falls back to raw line as message", func(t *testing.T) {
		entries := parseLogTextToOutputLogs(`{"time":"t1","msg":`)
		require.Len(t, entries, 1)
		assert.Equal(t, `{"time":"t1","msg":`, entries[0].Message)
		assert.Empty(t, entries[0].Timestamp)
	})
}

// signozFilterItem builds one SigNoz filter item in the {key:{key},op,value} shape.
func signozFilterItem(key, op string, value any) map[string]any {
	return map[string]any{"key": map[string]any{"key": key}, "op": op, "value": value}
}

func TestSignozFilterItemsToWhereClause(t *testing.T) {
	t.Run("array form ANDs items and round-trips through FlattenSignozQuery", func(t *testing.T) {
		items := []any{
			signozFilterItem("k8s.pod.name", "=", "checkout-abc"),
			signozFilterItem("k8s.namespace.name", "=", "shop"),
		}
		where, err := signozFilterItemsToWhereClause(items)
		require.NoError(t, err)

		flattened, err := FlattenSignozQuery(where)
		require.NoError(t, err)
		require.Len(t, flattened, 2)
		assert.Equal(t, "k8s.pod.name", flattened[0].Key["key"])
		assert.Equal(t, "=", flattened[0].Op)
		assert.Equal(t, "checkout-abc", flattened[0].Value)
		assert.Equal(t, "k8s.namespace.name", flattened[1].Key["key"])
	})

	t.Run("map form with items key", func(t *testing.T) {
		q := map[string]any{"op": "AND", "items": []any{signozFilterItem("service.name", "=", "cart")}}
		where, err := signozFilterItemsToWhereClause(q)
		require.NoError(t, err)
		flattened, err := FlattenSignozQuery(where)
		require.NoError(t, err)
		require.Len(t, flattened, 1)
		assert.Equal(t, "service.name", flattened[0].Key["key"])
	})

	t.Run("[]map[string]any form", func(t *testing.T) {
		items := []map[string]any{signozFilterItem("trace_id", "=", "xyz")}
		where, err := signozFilterItemsToWhereClause(items)
		require.NoError(t, err)
		flattened, err := FlattenSignozQuery(where)
		require.NoError(t, err)
		require.Len(t, flattened, 1)
		assert.Equal(t, "trace_id", flattened[0].Key["key"])
	})

	t.Run("contains op is preserved through the round-trip", func(t *testing.T) {
		where, err := signozFilterItemsToWhereClause([]any{signozFilterItem("k8s.deployment.name", "contains", "cart")})
		require.NoError(t, err)
		flattened, err := FlattenSignozQuery(where)
		require.NoError(t, err)
		require.Len(t, flattened, 1)
		assert.Equal(t, "contains", flattened[0].Op)
	})

	t.Run("unsupported op errors", func(t *testing.T) {
		_, err := signozFilterItemsToWhereClause([]any{signozFilterItem("x", "weird_op", "y")})
		assert.Error(t, err)
	})

	t.Run("no valid items errors", func(t *testing.T) {
		_, err := signozFilterItemsToWhereClause([]any{})
		assert.Error(t, err)
	})
}

func TestBuildSignozRawQueryFromWhere(t *testing.T) {
	startTime := time.UnixMilli(1766406993000)
	endTime := time.UnixMilli(1766410593000)
	where := query.QueryWhereClause{And: []query.QueryWhereClause{
		{Binary: query.BinaryWhereClause{"k8s.pod.name": {Eq: "checkout-abc"}}},
		{Binary: query.BinaryWhereClause{"k8s.namespace.name": {Eq: "shop"}}},
	}}

	raw, err := buildSignozRawQueryFromWhere(where, startTime, endTime)
	require.NoError(t, err)

	cq := raw["compositeQuery"].(map[string]any)
	a := cq["builderQueries"].(map[string]any)["A"].(map[string]any)
	items := a["filters"].(map[string]any)["items"].([]map[string]any)
	require.Len(t, items, 2)
	assert.Equal(t, "k8s.pod.name", items[0]["key"].(map[string]any)["key"])
	assert.Equal(t, "=", items[0]["op"])
	assert.Equal(t, "checkout-abc", items[0]["value"])

	// The reconstructed composite must feed the Logs Explorer URL builder cleanly.
	url, err := buildSignozLogsExplorerURL("https://telemetry.example.com", raw, startTime, endTime)
	require.NoError(t, err)
	assert.Contains(t, url, "compositeQuery=")
}

func TestExtractLogInsights(t *testing.T) {
	t.Run("severity-first surfaces ERROR/FATAL lines as Critical", func(t *testing.T) {
		logs := []OutputLog{
			{Message: "all good", Severity: "INFO"},
			{Message: "boom happened", Severity: "ERROR"},
			{Message: "kaboom", Severity: "FATAL"},
		}
		insights := extractLogInsights(logs, 10)
		require.Len(t, insights, 2)
		assert.Equal(t, "ERROR: boom happened", insights[0].Message)
		assert.Equal(t, "Critical", insights[0].Severity)
		assert.Equal(t, "FATAL: kaboom", insights[1].Message)
	})

	t.Run("caps at maxErrors", func(t *testing.T) {
		logs := []OutputLog{
			{Message: "e1", Severity: "ERROR"},
			{Message: "e2", Severity: "ERROR"},
			{Message: "e3", Severity: "ERROR"},
		}
		assert.Len(t, extractLogInsights(logs, 2), 2)
	})

	t.Run("falls back to pattern extraction when no severity-tagged errors", func(t *testing.T) {
		logs := []OutputLog{{Message: "connection error: timeout dialing upstream", Severity: "INFO"}}
		assert.NotEmpty(t, extractLogInsights(logs, 10))
	})

	t.Run("clean logs yield no insights", func(t *testing.T) {
		logs := []OutputLog{{Message: "request served in 3ms", Severity: "INFO"}}
		assert.Empty(t, extractLogInsights(logs, 10))
	})
}

func TestBuildLogsActionResponse(t *testing.T) {
	logs := []OutputLog{
		{Timestamp: "t1", Message: "status=500 error serving request", Severity: "ERROR", Labels: map[string]any{"pod": "cart-1"}},
		{Timestamp: "t2", Message: "ok", Severity: "INFO", Labels: map[string]any{"pod": "cart-1"}},
	}

	resp := buildLogsActionResponse(
		logs, "My Logs", map[string]any{"action_name": "cloud_logs"}, 5, map[string]any{"q": 1},
		[]RegexLabelExtractor{{Pattern: `status=(\d+)`, LabelName: "status_code"}}, nil,
	)
	require.NotNil(t, resp)

	// Data marshals to {"data": [ {timestamp,message,labels,severity}, ... ]}
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp.GetData().(string)), &payload))
	arr, ok := payload["data"].([]any)
	require.True(t, ok)
	assert.Len(t, arr, 2)

	ai := resp.GetAdditionalInfo()
	assert.Equal(t, "My Logs", ai["title"])
	assert.Equal(t, "cloud_logs", ai["action_name"])
	assert.NotNil(t, ai["log_summary"])

	assert.NotEmpty(t, resp.GetInsights())

	assert.Contains(t, resp.ExtractLabels(), "status_code")
}

func TestAddExecutedQueryInfo(t *testing.T) {
	t.Run("records query and provider", func(t *testing.T) {
		info := addExecutedQueryInfo(map[string]any{"action_name": "cloud_logs"}, FetchLogsResult{Query: `{service.name="cart"}`, Provider: "loki"})
		assert.Equal(t, `{service.name="cart"}`, info["executed_query"])
		assert.Equal(t, "loki", info["provider"])
		assert.Equal(t, "cloud_logs", info["action_name"])
	})

	t.Run("skips empty values and tolerates nil map", func(t *testing.T) {
		info := addExecutedQueryInfo(nil, FetchLogsResult{})
		_, hasQuery := info["executed_query"]
		_, hasProvider := info["provider"]
		assert.False(t, hasQuery)
		assert.False(t, hasProvider)
	})
}

// traceIgnoringKeyFilter declares trace_id as an ignored (unfilterable) query key.
type traceIgnoringKeyFilter struct{}

func (traceIgnoringKeyFilter) GetIgnoredQueryRequestKeys() []string {
	return []string{"trace_id"}
}

func TestLogSourceSupportsTraceIDFilter(t *testing.T) {
	// The wrapper short-circuits before any mapping lookup for sources that
	// don't implement QueryRequestKeyFilter (getMergedLabelMapping needs a live
	// security context, so the mapping-dependent cases test the pure helper).
	t.Run("source without QueryRequestKeyFilter supports trace_id", func(t *testing.T) {
		assert.True(t, logSourceSupportsTraceIDFilter(nil, "", &LokiSource{}))
	})

	t.Run("source that strips trace_id does not support it", func(t *testing.T) {
		nr := &NewRelicLogSource{}
		assert.False(t, traceIDFilterSurvivesStrip(nr.GetLabelMapping(), nr))
		dt := &DynatraceLogSource{}
		assert.False(t, traceIDFilterSurvivesStrip(dt.GetLabelMapping(), dt))
	})

	t.Run("label mapping to a filterable field overrides the ignore", func(t *testing.T) {
		// The strip runs after label-mapping, so trace_id mapped to traceId
		// survives GetIgnoredQueryRequestKeys()=["trace_id"].
		mapping := map[string]string{"trace_id": "traceId"}
		assert.True(t, traceIDFilterSurvivesStrip(mapping, traceIgnoringKeyFilter{}))
	})

	t.Run("nil mapping keeps the canonical name and the ignore applies", func(t *testing.T) {
		assert.False(t, traceIDFilterSurvivesStrip(nil, traceIgnoringKeyFilter{}))
	})
}
