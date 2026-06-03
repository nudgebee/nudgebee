package observability

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/testenv"
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
