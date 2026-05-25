package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolNewRelicInfrastructureHosts = "newrelic_infrastructure_hosts_execute"

func init() {
	core.RegisterNBToolFactory(ToolNewRelicInfrastructureHosts, func(accountId string) (core.NBTool, error) {
		return NewRelicInfrastructureHostsTool{}, nil
	})
}

type NewRelicInfrastructureHostsTool struct{}

func (t NewRelicInfrastructureHostsTool) Name() string { return ToolNewRelicInfrastructureHosts }

func (t NewRelicInfrastructureHostsTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t NewRelicInfrastructureHostsTool) Description() string {
	return `Searches for infrastructure host entities in New Relic with system metrics including CPU, memory, and disk utilization.`
}

func (t NewRelicInfrastructureHostsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Search query for infrastructure hosts (e.g., 'name:prod-web-01', 'tags.environment:production')",
			},
		},
		Required: []string{"command"},
	}
}

func (t NewRelicInfrastructureHostsTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (t NewRelicInfrastructureHostsTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("newrelic: executing infrastructure hosts tool call", "query", input.Command)
	finalQuery := t.cleanupQuery(input.Command)

	// Execute the entity search
	response, err := t.executeHostEntitySearch(nbRequestContext, finalQuery)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("newrelic: unable to execute host entity search", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("newrelic: unable to serialize host entities json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	return core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (t NewRelicInfrastructureHostsTool) executeHostEntitySearch(ctx core.NbToolContext, query string) (map[string]any, error) {
	apiKey, _, region, err := getNewRelicConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Build entity search query string
	searchQuery := buildEntitySearchQuery("INFRA", "HOST", query)

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
						... on InfrastructureHostEntity {
							hostSummary {
								cpuUtilizationPercent
								memoryUsedPercent
								diskUsedPercent
							}
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

func (t NewRelicInfrastructureHostsTool) parseEntitySearchResponse(response map[string]any) ([]map[string]any, int) {
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

func (t NewRelicInfrastructureHostsTool) simplifyEntity(entity map[string]any) map[string]any {
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

	// Extract host summary metrics
	if hostSummary, ok := entity["hostSummary"].(map[string]any); ok {
		if cpuUtil, ok := hostSummary["cpuUtilizationPercent"].(float64); ok {
			simplified["cpu_utilization_percent"] = cpuUtil
		}
		if memUsed, ok := hostSummary["memoryUsedPercent"].(float64); ok {
			simplified["memory_used_percent"] = memUsed
		}
		if diskUsed, ok := hostSummary["diskUsedPercent"].(float64); ok {
			simplified["disk_used_percent"] = diskUsed
		}
	}

	return simplified
}

func (t NewRelicInfrastructureHostsTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getNewRelicConfigSchema()
}
