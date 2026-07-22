//go:build e2e

package tools

import (
	"encoding/json"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestClickhouseTraceTool(t *testing.T) {

	tool := TracesExecuteClickhouseTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId  string
			Query      string
			AccountId  string
			UserId     string
			ToolConfig string
		}{
			{
				SessionId:  "ut-tool-chain-1",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `SELECT * FROM traces_view WHERE workload_name = 'services-server' AND http_status_code >= 500 AND timestamp >= now() - interval 1 hour LIMIT 10`,
				ToolConfig: "",
			},
		}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

// TestClickhouseTraceTool_AggregationRawTable exercises the full agent chain
// (tool -> services-server traces_query with include_raw_result -> relay -> ClickHouse)
// for an aggregation query, and asserts the agent-facing payload is the raw
// {columns, column_types, rows} table with real values — not the zeroed span
// schema the fixed-mapping path produced for GROUP BY queries. Requires a live
// account (TEST_ACCOUNT/TEST_USER) and a services-server + relay-server reachable
// from the configured SERVICE_API_SERVER_URL / relay endpoint.
//
// Run: TEST_ACCOUNT=<id> TEST_USER=<id> go test -tags e2e -run TestClickhouseTraceTool_AggregationRawTable ./tools/
func TestClickhouseTraceTool_AggregationRawTable(t *testing.T) {
	account := os.Getenv("TEST_ACCOUNT")
	user := os.Getenv("TEST_USER")
	if account == "" || user == "" {
		t.Skip("set TEST_ACCOUNT and TEST_USER (requires services-server + relay-server reachable)")
	}

	tool := TracesExecuteClickhouseTool{}
	sc := security.NewRequestContextForSuperAdmin()

	// Aggregation: GROUP BY service with a count. The tool expands `traces_view`
	// into the otel_traces projection before sending it on.
	query := `SELECT service_name, count(*) AS request_count ` +
		`FROM traces_view WHERE timestamp >= now() - toIntervalHour(48) ` +
		`GROUP BY service_name ORDER BY request_count DESC LIMIT 20`

	ntc := core.NewNbToolContext(sc, &tool, account, user, uuid.NewString(), uuid.NewString(), uuid.NewString(), query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
	resp, err := tool.Call(ntc, core.NBToolCallRequest{Command: query})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Data)

	// New capability: the payload is the raw table carrying the aggregation columns.
	var got clickhouseRawTraceResponse
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &got),
		"agent payload must be the raw {columns, rows} table")
	assert.Contains(t, got.Columns, "service_name")
	assert.Contains(t, got.Columns, "request_count")
	require.NotEmpty(t, got.Rows, "aggregation should return at least one group")

	// The aggregation values must be real — not the zeroed span shape. The
	// regression produced rows of empty span structs with `"duration_ns":0`.
	assert.NotContains(t, resp.Data, `"duration_ns":0`,
		"raw aggregation output must not fall back to the zeroed span schema")
	t.Logf("raw aggregation table: columns=%v, %d groups; first row=%v", got.Columns, len(got.Rows), got.Rows[0])
}

// TestClickhouseTraceTool_SpanListingRawTable confirms a plain span listing also
// flows through the raw table (real columns/values), so the non-aggregation path
// is not regressed by the new shape.
//
// Run: TEST_ACCOUNT=<id> TEST_USER=<id> go test -tags e2e -run TestClickhouseTraceTool_SpanListingRawTable ./tools/
func TestClickhouseTraceTool_SpanListingRawTable(t *testing.T) {
	account := os.Getenv("TEST_ACCOUNT")
	user := os.Getenv("TEST_USER")
	if account == "" || user == "" {
		t.Skip("set TEST_ACCOUNT and TEST_USER (requires services-server + relay-server reachable)")
	}

	tool := TracesExecuteClickhouseTool{}
	sc := security.NewRequestContextForSuperAdmin()

	query := `SELECT service_name, duration_ns, span_name FROM traces_view ` +
		`WHERE timestamp >= now() - toIntervalHour(48) ORDER BY timestamp DESC LIMIT 10`

	ntc := core.NewNbToolContext(sc, &tool, account, user, uuid.NewString(), uuid.NewString(), uuid.NewString(), query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
	resp, err := tool.Call(ntc, core.NBToolCallRequest{Command: query})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Data)

	var got clickhouseRawTraceResponse
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &got))
	assert.Contains(t, got.Columns, "duration_ns")
	require.NotEmpty(t, got.Rows, "span listing should return rows")
	t.Logf("raw span table: columns=%v, %d rows", got.Columns, len(got.Rows))
}
