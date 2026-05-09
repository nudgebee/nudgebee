package tools

import (
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/relay"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"strings"
	"time"
)

const ToolGetLogsES = "elastic_search_execute"

func init() {
	core.RegisterNBToolFactory(ToolGetLogsES, func(accountId string) (core.NBTool, error) {
		return ElasticSearchExecuteTool{}, nil
	})
}

type ElasticSearchExecuteTool struct {
}

func (m ElasticSearchExecuteTool) Name() string {
	return ToolGetLogsES
}

func (m ElasticSearchExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ElasticSearchExecuteTool) Description() string {
	return `Executes a ElasticSearch query against a ElasticSearch instance and retrieves the corresponding log data. 

	Usage:

	* Input: Provide a well-formatted ElasticSearch query as input.
	* Output: The tool will return the log data retrieved from ElasticSearch based on your query.

	Important Notes:

	* Ensure the LogQL query is syntactically correct and adheres to the ElasticSearch query language specification.
	* The output may contain a large volume of log data. You should process and summarize the data appropriately before presenting it to the user.`
}

func (m ElasticSearchExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"query": {
				Type:        core.ToolSchemaTypeObject,
				Description: "The ElasticSearch query object to execute.",
			},
		},
		Required: []string{"query"},
	}
}

func (m ElasticSearchExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (m ElasticSearchExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("es: executing getResourceLogs tool call", "query", input.Command)

	// Determine the actual query. Expecting raw JSON object or object with "query" key.
	finalQuery := ""
	var temp map[string]any
	if err := common.UnmarshalJson([]byte(input.Command), &temp); err == nil {
		if queryObj, ok := temp["query"]; ok {
			if queryBytes, err := common.MarshalJson(queryObj); err == nil {
				finalQuery = fmt.Sprintf("{\"query\": %s}", string(queryBytes))
			}
		} else {
			// Assume the entire object is the query
			finalQuery = input.Command
		}
	} else {
		finalQuery = m.cleanupQuery(input.Command)
	}

	if finalQuery == "" {
		return core.NBToolResponse{
			Data:   "Invalid query format. Provide a valid ElasticSearch JSON query.",
			Status: core.NBToolResponseStatusError,
		}, errors.New("invalid query format")
	}

	endTime := time.Now()
	startTime := endTime.Add(-1 * time.Hour)
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}
	startTime, endTime = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), startTime, endTime)

	response, err := m.executeES(nbRequestContext, finalQuery, nbRequestContext.AccountId, map[string]any{
		"end_time":   endTime.Unix(),
		"start_time": startTime.Unix(),
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("es: unable to execute api call", "error", err.Error())
		responseData := ""
		if response != nil {
			if responseData1, ok := response.(string); ok {
				responseData = responseData1
			}
		}
		return core.NBToolResponse{
			Data:   responseData,
			Status: core.NBToolResponseStatusError,
		}, err
	}
	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("es: unable to serialize json", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}
	return core.NBToolResponse{
		Data:       string(data),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "query-logs"}, "Query Logs", nil, finalQuery)},
	}, nil
}

func (m ElasticSearchExecuteTool) executeES(ctx core.NbToolContext, query string, accountId string, configs map[string]any) (any, error) {
	queryObj := map[string]any{}

	err := common.UnmarshalJson([]byte(query), &queryObj)
	_, index_present := queryObj["index"]
	if !index_present {
		queryObj["index"] = utils.GetESAccountIndex(accountId)
	}
	if err != nil {
		return nil, err
	}
	actionParam := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "query_es",
		ActionParams: queryObj,
	}
	response, err := relay.Execute(actionParam)
	if err != nil {
		return nil, err
	}
	return m.getRelayESResponseData(response)
}

func (m ElasticSearchExecuteTool) getRelayESResponseData(relayResponse map[string]any) (map[string]any, error) {
	data1, ok := relayResponse["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, errors.New("unable to execute es query")
	}
	if data1["success"] != nil && !data1["success"].(bool) {
		return nil, errors.New("unable to execute es query")
	}
	type Hits struct {
		Total    map[string]any   `json:"total"`
		MaxScore float32          `json:"max_score"`
		Hits     []map[string]any `json:"hits"`
	}
	type FuncCall struct {
		Hits Hits `json:"hits"`
	}
	var funcCall FuncCall
	dataStr, ok := data1["data"].(string)
	if !ok {
		return nil, errors.New("data field is not a string")
	}
	err := common.UnmarshalJson([]byte(dataStr), &funcCall)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"result": funcCall.Hits.Hits,
	}
	return result, nil
}
