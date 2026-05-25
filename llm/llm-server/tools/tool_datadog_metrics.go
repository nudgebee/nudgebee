package tools

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"slices"
	"strconv"
	"strings"
	"time"
)

const ToolExecuteDatadogMetrics = "datadog_metrics_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteDatadogMetrics, func(accountId string) (core.NBTool, error) {
		return DatadogMetricsExecuteTool{}, nil
	})
}

type DatadogMetricsExecuteTool struct{}

func (m DatadogMetricsExecuteTool) Name() string { return ToolExecuteDatadogMetrics }

func (m DatadogMetricsExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m DatadogMetricsExecuteTool) Description() string {
	return `Executes a Datadog metrics query and retrieves the corresponding metric data.`
}

func (m DatadogMetricsExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Datadog metrics query to execute"},
		},
		Required: []string{"command"},
	}
}

func (m DatadogMetricsExecuteTool) cleanupQuery(query string, args map[string]any) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	finalQuery = unwrapJSONQuery(finalQuery)
	return stripCLITimeFlags(finalQuery, args)
}

func (m DatadogMetricsExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing metrics tool call", "query", input.Command)
	if input.Arguments == nil {
		input.Arguments = map[string]any{}
	}
	finalQuery := m.cleanupQuery(input.Command, input.Arguments)

	endTime := time.Now()
	startTime := endTime.Add(-2 * time.Hour)
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	responseAny, err := m.executeDatadogMetrics(nbRequestContext, finalQuery, map[string]any{
		"end_time":   endTime.Unix(),
		"start_time": startTime.Unix(),
	})

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute api call", "error", err.Error())
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}

	responseMap := responseAny
	// Process responseMap to add stats
	if seriesData, ok := responseMap["series"].([]any); ok {
		for _, seriesItemAny := range seriesData {
			if seriesItem, ok := seriesItemAny.(map[string]any); ok {
				if pointlistAny, ok := seriesItem["pointlist"].([]any); ok {
					var valuesFloat []float64
					for _, pointAny := range pointlistAny {
						if point, ok := pointAny.([]any); ok && len(point) == 2 && point[1] != nil {
							// Datadog pointlist values are typically float64
							if value, ok := point[1].(float64); ok {
								valuesFloat = append(valuesFloat, value)
							} else if valueStr, ok := point[1].(string); ok { // Fallback if value is string
								f, e := strconv.ParseFloat(valueStr, 64)
								if e == nil {
									valuesFloat = append(valuesFloat, f)
								}
							}
						}
					}

					if len(valuesFloat) > 0 {
						slices.Sort(valuesFloat)

						minValue := valuesFloat[0]
						maxValue := valuesFloat[len(valuesFloat)-1]

						sum := 0.0
						for _, v := range valuesFloat {
							sum += v
						}
						avgValue := sum / float64(len(valuesFloat))

						var p99Value float64
						if len(valuesFloat) == 1 {
							p99Value = valuesFloat[0]
						} else {
							p99Index := int(math.Ceil(0.99*float64(len(valuesFloat)))) - 1
							// Ensure index is within bounds [0, len-1]
							if p99Index >= len(valuesFloat) {
								p99Index = len(valuesFloat) - 1
							}
							if p99Index < 0 { // Should only happen if len(valuesFloat) was 0, already guarded
								p99Index = 0
							}
							p99Value = valuesFloat[p99Index]
						}

						statsMap := map[string]float64{
							"min": minValue,
							"max": maxValue,
							"avg": avgValue,
							"p99": p99Value,
						}
						seriesItem["stats"] = statsMap
					}
				}
			}
		}
	}

	data, err := common.MarshalJson(responseMap)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize json after adding stats", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}
	// Add reference with the executed query so it shows in the UI
	var refs []core.NBToolResponseReference
	if seriesData, ok := responseMap["series"].([]any); ok && len(seriesData) > 0 {
		refs = []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "query"}, "Query Datadog Metrics", map[string]string{"tab": "4", "subtab": "2"}, finalQuery)}
	}

	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess, References: refs}, nil
}

func (m DatadogMetricsExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func (m DatadogMetricsExecuteTool) executeDatadogMetrics(ctx core.NbToolContext, query string, configs map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)

	if err != nil {
		return nil, fmt.Errorf("datadog: API keys not configured — please configure Datadog integration: %w", err)
	}

	now := time.Now().Unix()
	from := now - 3600*2
	if configs["end_time"] != nil {
		if now1, ok := configs["end_time"].(int64); ok {
			now = now1
		}
	}

	if configs["start_time"] != nil {
		if from1, ok := configs["start_time"].(int64); ok {
			from = from1
		}
	}

	q := url.QueryEscape(query)
	requestURL := fmt.Sprintf("https://%s/api/v1/query?query=%s&from=%d&to=%d", site, q, from, now)
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	return doDatadogRequest(req, apiKey, appKey)
}
