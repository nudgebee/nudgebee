package tools

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"time"

	"github.com/pkg/errors"
)

// ToolLogsExecuteV2 is the canonical, provider-independent log executor used by
// FetchLogsAgentV2. Unlike v1's logs_execute (which translates the where clause
// to a provider-native query string inside llm-server, per provider), this tool
// forwards the structured canonical where clause to services-server's FetchLogs,
// which resolves canonical→provider labels and builds the native query
// server-side — the same path the UI log query builder uses.
//
// It is registered so FetchLogsAgentV2 can invoke it by name via callTool; it is
// NOT added to any agent's GetSupportedTools, so generic planners never see it as
// a separately-callable tool.
const ToolLogsExecuteV2 = "logs_execute_v2"

// NoLogsFoundPrefix is the leading phrase of the human-readable message both
// logs_execute and logs_execute_v2 return when a query succeeds but matches zero
// rows. Exported so FetchLogsAgentV2 can detect "no results" (to trigger its
// kubectl fallback) without coupling to the full message text.
const NoLogsFoundPrefix = "No logs found"

func init() {
	core.RegisterNBToolFactory(ToolLogsExecuteV2, func(accountId string) (core.NBTool, error) {
		return NewNBLogToolV2(accountId)
	})
}

func NewNBLogToolV2(accountId string) (*NBLogToolV2, error) {
	logProvider, err := GetLogProvider(accountId)
	if err != nil {
		slog.Error("logs_v2: unable to get log provider", "error", err)
		return nil, err
	}
	return &NBLogToolV2{
		accountId:   accountId,
		logProvider: logProvider,
	}, nil
}

type NBLogToolV2 struct {
	accountId   string
	logProvider services_server.ObservabilityProvider
}

func (t *NBLogToolV2) Name() string { return ToolLogsExecuteV2 }

func (t *NBLogToolV2) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t *NBLogToolV2) Description() string {
	return "Executes a provider-independent canonical log query and returns the result."
}

// InputSchema mirrors v1's logs_execute schema — the agent emits the same
// `{"where": {...}}` JSON `command`, plus optional time-window controls.
func (t *NBLogToolV2) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON query to Execute",
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Start Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "End Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time range for the query (e.g., '2d', '1w', '1h'). If provided, start_time is calculated relative to end_time.",
			},
			"index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Elasticsearch index name or pattern to query (e.g., 'app-logs-*'). If not specified, the account's default index is used.",
			},
		},
		Required: []string{"command"},
	}
}

// Call parses the LLM's canonical `{"where": {...}}` JSON into the shared
// QueryBuilder and forwards the structured where clause to services-server with
// an empty Query (executeFetchLogsCanonical). One path serves every provider;
// services-server owns the canonical→provider resolution and native query build.
func (t *NBLogToolV2) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("logs_v2: executing canonical getLogs tool call", "query", input.Command, "provider", t.logProvider.Provider)

	queryBuilder, err := core.BuildLogQueryBuilder(nbRequestContext, input.Command)
	if err != nil {
		return core.NBToolResponse{}, err
	}
	if queryBuilder.Limit == 0 {
		queryBuilder.Limit = 1000
	}

	args := input.Arguments
	if args == nil {
		args = make(map[string]any)
	}
	if _, ok := args["range"]; !ok && queryBuilder.TimeRange != "" {
		args["range"] = queryBuilder.TimeRange
	}
	if _, ok := args["start_time"]; !ok && queryBuilder.StartTime != "" {
		args["start_time"] = queryBuilder.StartTime
	}
	if _, ok := args["end_time"]; !ok && queryBuilder.EndTime != "" {
		args["end_time"] = queryBuilder.EndTime
	}

	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	if t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, args); err == nil {
		start = t1
		end = t2
	}
	start, end = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), start, end)

	configs := map[string]any{
		"end_time":   end.UnixMilli(),
		"start_time": start.UnixMilli(),
		"limit":      queryBuilder.Limit,
	}
	if queryBuilder.Index != "" {
		configs["index"] = queryBuilder.Index
	}

	response, err := executeFetchLogsCanonical(nbRequestContext, t.logProvider, queryBuilder.Where, configs)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("logs_v2: unable to execute canonical query", "provider", t.logProvider.Provider, "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, errors.Wrap(core.ErrUnableToFetchData, err.Error())
	}

	if len(response.Logs) == 0 {
		whereJSON, _ := common.MarshalJson(queryBuilder.Where)
		noLogsMsg := fmt.Sprintf(
			NoLogsFoundPrefix+" for %s (canonical where: %s; time range: %s to %s, limit: %d). "+
				"The query executed successfully but returned no results. "+
				"Suggestions: check if label names/values are correct, try broader filters, or expand the time range.",
			t.logProvider.Provider, string(whereJSON), start.Format(time.RFC3339), end.Format(time.RFC3339), queryBuilder.Limit,
		)
		return core.NBToolResponse{
			Data:   noLogsMsg,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("logs_v2: unable to serialize json", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, core.ErrUnableToFetchData
	}
	return core.NBToolResponse{
		Data:       string(data),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{logsUIRef(nbRequestContext, queryBuilder)},
	}, nil
}
