package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
)

const ToolSignozLogExecute = "signoz_log_execute"

func init() {
	core.RegisterNBToolFactory(ToolSignozLogExecute, func(accountId string) (core.NBTool, error) {
		return SignozExecuteTool{}, nil
	})
}

type SignozExecuteTool struct{}

func (m SignozExecuteTool) Name() string { return ToolSignozLogExecute }

func (m SignozExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m SignozExecuteTool) Description() string {
	return `Executes a Signoz log query and retrieves the corresponding log data.`
}

func (m SignozExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Signoz log query to execute"},
		},
		Required: []string{"command"},
	}
}

func (m SignozExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (m SignozExecuteTool) buildSignozAPI(nbRequestContext core.NbToolContext, agent_query map[string]any) (string, time.Time, time.Time) {
	end := time.Now()
	start := end.Add(-1 * time.Hour)
	t0, t1, err := ExtractStartEndtimeFromLabels(nbRequestContext, agent_query)
	if err == nil {
		start = t0
		end = t1
	}
	start, end = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), start, end)

	filters := agent_query["filters"]
	jsonData, err := common.MarshalJson(filters)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("signoz: unable to marshal final query", "error", err.Error())
		return "", start, end
	}
	finalQueryresp := string(jsonData)
	return finalQueryresp, start, end
}

func (m SignozExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("signoz: executing logs tool call", "query", input.Command)
	finalQuery := m.cleanupQuery(input.Command)
	var result map[string]any
	err := common.UnmarshalJson([]byte(finalQuery), &result)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("signoz: unable to unmarshal query", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}
	finalSignozQuery, start, end := m.buildSignozAPI(nbRequestContext, result)

	response, err := m.executeSignozLogs(nbRequestContext, finalSignozQuery, nbRequestContext.AccountId, map[string]any{}, start, end)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("signoz: unable to execute api call", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}
	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("signoz: unable to serialize json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (m SignozExecuteTool) executeSignozLogs(ctx core.NbToolContext, query string, accountId string, configs map[string]any, start, end time.Time) (core.ObservabilityLogResponse, error) {
	if configs == nil {
		configs = map[string]any{}
	}
	if !start.IsZero() {
		configs["start_time"] = start.UnixMilli()
	}
	if !end.IsZero() {
		configs["end_time"] = end.UnixMilli()
	}
	if _, ok := configs["limit"]; !ok {
		configs["limit"] = 100
	}
	return executeFetchLogs(ctx, services_server.ObservabilityProvider{
		IntegrationSource: "agent",
		Provider:          "signoz",
	}, query, configs)
}
