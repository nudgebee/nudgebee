package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolNewRelicAPMServices = "newrelic_apm_services_execute"

func init() {
	core.RegisterNBToolFactory(ToolNewRelicAPMServices, func(accountId string) (core.NBTool, error) {
		return NewRelicAPMServicesTool{}, nil
	})
}

type NewRelicAPMServicesTool struct{}

func (t NewRelicAPMServicesTool) Name() string { return ToolNewRelicAPMServices }

func (t NewRelicAPMServicesTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t NewRelicAPMServicesTool) Description() string {
	return `Searches for APM application entities in New Relic with performance metrics including error rate, response time, throughput, and apdex score.`
}

func (t NewRelicAPMServicesTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Search query for APM services (e.g., 'name:api-server', 'tags.environment:production')",
			},
		},
		Required: []string{"command"},
	}
}

func (t NewRelicAPMServicesTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (t NewRelicAPMServicesTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("newrelic: executing APM services tool call", "query", input.Command)
	finalQuery := t.cleanupQuery(input.Command)

	// Execute the entity search
	response, err := t.executeAPMEntitySearch(nbRequestContext, finalQuery)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("newrelic: unable to execute APM entity search", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("newrelic: unable to serialize APM entities json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	return core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (t NewRelicAPMServicesTool) executeAPMEntitySearch(ctx core.NbToolContext, query string) (map[string]any, error) {
	apiKey, _, region, err := getNewRelicConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Build entity search query string
	searchQuery := buildEntitySearchQuery("APM", "APPLICATION", query)

	// Build GraphQL query
	graphqlQuery := fmt.Sprintf(`{
		actor {
			entitySearch(query: "%s") {
				results {
					entities {
						guid
						name
						type
						domain
						accountId
						... on ApmApplicationEntity {
							language
							applicationId
							apmSummary {
								errorRate
								responseTimeAverage
								throughput
								apdexScore
							}
							alertSeverity
						}
					}
				}
				count
			}
		}
	}`, searchQuery)

	// Execute GraphQL request
	result, err := doNewRelicGraphQLRequest(apiKey, region, graphqlQuery, nil)
	if err != nil {
		return nil, err
	}

	// Parse response and extract entities
	entities, count := t.parseEntitySearchResponse(result)

	return map[string]any{
		"data":  entities,
		"count": count,
	}, nil
}

func (t NewRelicAPMServicesTool) parseEntitySearchResponse(response map[string]any) ([]map[string]any, int) {
	entities := []map[string]any{}
	count := 0

	// Navigate through nested structure: data.actor.entitySearch.results.entities
	if data, ok := response["data"].(map[string]any); ok {
		if actor, ok := data["actor"].(map[string]any); ok {
			if entitySearch, ok := actor["entitySearch"].(map[string]any); ok {
				// Get count
				if countVal, ok := entitySearch["count"].(float64); ok {
					count = int(countVal)
				}

				// Get results
				if results, ok := entitySearch["results"].(map[string]any); ok {
					if entitiesList, ok := results["entities"].([]any); ok {
						for _, entity := range entitiesList {
							if entityMap, ok := entity.(map[string]any); ok {
								simplifiedEntity := t.simplifyEntity(entityMap)
								entities = append(entities, simplifiedEntity)
							}
						}
					}
				}
			}
		}
	}

	return entities, count
}

func (t NewRelicAPMServicesTool) simplifyEntity(entity map[string]any) map[string]any {
	simplified := map[string]any{
		"guid":   getStringValue(entity, "guid"),
		"name":   getStringValue(entity, "name"),
		"type":   getStringValue(entity, "type"),
		"domain": getStringValue(entity, "domain"),
	}

	// Add account ID if present
	if accountId, ok := entity["accountId"].(float64); ok {
		simplified["account_id"] = int(accountId)
	}

	// Add language if present
	if language := getStringValue(entity, "language"); language != "" {
		simplified["language"] = language
	}

	// Add application ID if present
	if appId, ok := entity["applicationId"].(float64); ok {
		simplified["application_id"] = int(appId)
	}

	// Add alert severity if present
	if alertSeverity := getStringValue(entity, "alertSeverity"); alertSeverity != "" {
		simplified["alert_severity"] = alertSeverity
	}

	// Extract APM summary metrics
	if apmSummary, ok := entity["apmSummary"].(map[string]any); ok {
		if errorRate, ok := apmSummary["errorRate"].(float64); ok {
			simplified["error_rate"] = errorRate
		}
		if responseTime, ok := apmSummary["responseTimeAverage"].(float64); ok {
			simplified["response_time_avg"] = responseTime
		}
		if throughput, ok := apmSummary["throughput"].(float64); ok {
			simplified["throughput"] = throughput
		}
		if apdex, ok := apmSummary["apdexScore"].(float64); ok {
			simplified["apdex_score"] = apdex
		}
	}

	return simplified
}

// Helper function to safely extract string values from map
func getStringValue(m map[string]any, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func (t NewRelicAPMServicesTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getNewRelicConfigSchema()
}
