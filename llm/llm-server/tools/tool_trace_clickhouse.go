package tools

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolGetTracesClickhouse = "traces_execute"

// query traces using relay server
const tracesViewClickhouseFormat = `(select Timestamp as timestamp,ServiceName as service_name, SpanAttributes['source.workload_name'] as workload_name, SpanAttributes['source.workload_namespace'] as workload_namespace, TraceId as trace_id, SpanId as span_id, ParentSpanId as parent_span_id, TraceState as trace_state, SpanKind as span_kind, Duration as duration_ns, case when mapContains(SpanAttributes, 'db.statement') then SpanAttributes['db.statement'] else SpanAttributes['http.url'] end as resource, case when mapContains(SpanAttributes, 'destination.workload_namespace') then SpanAttributes['destination.workload_namespace'] else ResourceAttributes['k8s.namespace.name'] end as destination_workload_namespace, case when mapContains(SpanAttributes, 'destination.name') then SpanAttributes['destination.name'] else ResourceAttributes['service.name'] end as destination_name, case when mapContains(SpanAttributes, 'destination.workload_name') then SpanAttributes['destination.workload_name'] else ResourceAttributes['k8s.deployment.name'] end as destination_workload_name, toInt32OrZero(SpanAttributes['http.status_code']) as status_code, SpanName as span_name from %s) q`

func init() {
	core.RegisterNBToolFactory(ToolGetTracesClickhouse, func(accountId string) (core.NBTool, error) {
		return TracesExecuteClickhouseTool{
			accountId: accountId,
		}, nil
	})
}

type TracesExecuteClickhouseTool struct {
	accountId   string
	tracesTable string
}

func (m TracesExecuteClickhouseTool) Name() string {
	return ToolGetTracesClickhouse
}

func (m TracesExecuteClickhouseTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m TracesExecuteClickhouseTool) Description() string {
	return "Executes a Traces SQL query and returns the result."
}

type ClickhouseAgentRow struct {
	Timestamp                    string `json:"timestamp"`
	ServiceName                  string `json:"service_name"`
	WorkloadName                 string `json:"workload_name"`
	WorkloadNamespace            string `json:"workload_namespace"`
	TraceID                      string `json:"trace_id"`
	SpanID                       string `json:"span_id"`
	ParentSpanID                 string `json:"parent_span_id"`
	TraceState                   string `json:"trace_state"`
	SpanKind                     string `json:"span_kind"`
	DurationNs                   int64  `json:"duration_ns"`
	Resource                     string `json:"resource"`
	DestinationWorkloadNamespace string `json:"destination_workload_namespace"`
	DestinationName              string `json:"destination_name"`
	DestinationWorkloadName      string `json:"destination_workload_name"`
	StatusCode                   string `json:"status_code"`
	SpanName                     string `json:"span_name"`
}
type clickhouseTraceResponse struct {
	Traces   []ClickhouseAgentRow            `json:"traces"`
	Metadata core.ObservabilityTraceMetadata `json:"metadata"`
}

func (m TracesExecuteClickhouseTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Sql query to Execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m TracesExecuteClickhouseTool) cleanUpQuery(query string) string {
	query = strings.ReplaceAll(query, "\\", "")
	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")
	return query
}

func (m TracesExecuteClickhouseTool) getTracesView(accountId string) string {

	if m.tracesTable == "" {
		tracesTable := "otel_traces"
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			slog.Error("unable to fetch dbms", "error", err)
			return fmt.Sprintf(tracesViewClickhouseFormat, tracesTable)
		}
		rows, err := dbms.Db.Queryx("select connection_status::text from agent where cloud_account_id = $1", accountId)
		if err != nil {
			slog.Error("unable to fetch dbms", "error", err)
			return fmt.Sprintf(tracesViewClickhouseFormat, tracesTable)
		}
		defer func() {
			if err := rows.Close(); err != nil {
				slog.Error("tools: failed to close rows", "error", err)
			}
		}()

		for rows.Next() {
			var connectionStatusString *string
			err := rows.Scan(&connectionStatusString)
			if err != nil {
				slog.Error("unable to scan rows", "error", err)
				break
			}
			connectionStatus := map[string]any{}
			if connectionStatusString != nil {
				err = common.UnmarshalJson([]byte(*connectionStatusString), &connectionStatus)
				if err != nil {
					slog.Error("unable to unmarshal rows", "error", err)
					break
				}
			}
			tracesUrl := connectionStatus["tracesUrl"]
			if tracesUrl != nil {
				tracesUrlStr := tracesUrl.(string)
				if strings.Contains(tracesUrlStr, "last9.io") {
					tracesTable = "otel.traces"
				}
				break
			}
		}
		m.tracesTable = tracesTable
	}

	return fmt.Sprintf(tracesViewClickhouseFormat, m.tracesTable)
}

func (m TracesExecuteClickhouseTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("traces: executing getTraces tool call")
	finalQuery := m.cleanUpQuery(input.Command)
	targetView := " traces_view "
	if strings.Contains(finalQuery, " default.traces_view ") {
		targetView = " default.traces_view "
	} else if strings.Contains(finalQuery, " default.traces_view\n") {
		targetView = " default.traces_view\n"
	} else if strings.Contains(finalQuery, " traces_view\n") {
		targetView = " traces_view\n"
	}
	finalQuery = strings.Replace(finalQuery, targetView, " "+m.getTracesView(nbRequestContext.AccountId)+" ", 1)
	finalQuery = fmt.Sprintf("select * from (%s) as ql limit %d;", finalQuery, config.Config.LlmServerAgentMaxTracesRows)

	queryResponse, err := executeFetchTrace(nbRequestContext, "otel_clickhouse", "agent", finalQuery, core.TraceQueryBuilder{}, map[string]any{})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to get traces", "error", err.Error())
		responseData := "Trace data is unavailable for this request."
		return core.NBToolResponse{
			Data:   responseData,
			Status: core.NBToolResponseStatusError,
		}, err
	}

	agentTrace := []ClickhouseAgentRow{}
	for trace := range queryResponse.Traces {
		traceRow := ClickhouseAgentRow{
			Timestamp:                    queryResponse.Traces[trace].Timestamp,
			ServiceName:                  queryResponse.Traces[trace].ServiceName,
			WorkloadName:                 queryResponse.Traces[trace].WorkloadName,
			WorkloadNamespace:            queryResponse.Traces[trace].WorkloadNamespace,
			TraceID:                      queryResponse.Traces[trace].TraceID,
			SpanID:                       queryResponse.Traces[trace].SpanID,
			ParentSpanID:                 queryResponse.Traces[trace].ParentSpanID,
			TraceState:                   queryResponse.Traces[trace].TraceState,
			SpanKind:                     queryResponse.Traces[trace].SpanKind,
			DurationNs:                   queryResponse.Traces[trace].DurationNs,
			Resource:                     queryResponse.Traces[trace].Resource,
			DestinationWorkloadNamespace: queryResponse.Traces[trace].DestinationNamespace,
			DestinationName:              queryResponse.Traces[trace].DestinationName,
			DestinationWorkloadName:      queryResponse.Traces[trace].DestinationWorkload,
			StatusCode:                   queryResponse.Traces[trace].StatusCode,
			SpanName:                     queryResponse.Traces[trace].SpanName,
		}
		agentTrace = append(agentTrace, traceRow)
	}
	clickhouseTraceResponse := clickhouseTraceResponse{
		Traces:   agentTrace,
		Metadata: queryResponse.Metadata,
	}

	response, err := common.MarshalJson(clickhouseTraceResponse)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to serialize on json", "error", err.Error())
		return core.NBToolResponse{}, err
	}

	resp := core.NBToolResponse{
		Data:   string(response),
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}

	resp.References = []core.NBToolResponseReference{
		core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "traces"}, "Traces Details", nil, ""),
	}

	return resp, err
}
