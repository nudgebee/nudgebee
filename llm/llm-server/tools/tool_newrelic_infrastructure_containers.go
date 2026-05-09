package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolNewRelicInfrastructureContainers = "newrelic_infrastructure_containers_execute"

func init() {
	core.RegisterNBToolFactory(ToolNewRelicInfrastructureContainers, func(accountId string) (core.NBTool, error) {
		return NewRelicInfrastructureContainersTool{}, nil
	})
}

type NewRelicInfrastructureContainersTool struct{}

func (t NewRelicInfrastructureContainersTool) Name() string {
	return ToolNewRelicInfrastructureContainers
}

func (t NewRelicInfrastructureContainersTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t NewRelicInfrastructureContainersTool) Description() string {
	return `Searches for infrastructure container entities in New Relic including Kubernetes pods and containers.`
}

func (t NewRelicInfrastructureContainersTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Search query for containers (e.g., 'name:nginx', 'tags.k8s.namespaceName:production')",
			},
		},
		Required: []string{"command"},
	}
}

func (t NewRelicInfrastructureContainersTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (t NewRelicInfrastructureContainersTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("newrelic: executing infrastructure containers tool call", "query", input.Command)
	finalQuery := t.cleanupQuery(input.Command)

	// Execute the entity search
	response, err := t.executeContainerEntitySearch(nbRequestContext, finalQuery)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("newrelic: unable to execute container entity search", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("newrelic: unable to serialize container entities json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	return core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (t NewRelicInfrastructureContainersTool) executeContainerEntitySearch(ctx core.NbToolContext, query string) (map[string]any, error) {
	apiKey, _, region, err := getNewRelicConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Build entity search query string
	searchQuery := buildEntitySearchQuery("INFRA", "CONTAINER", query)

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

func (t NewRelicInfrastructureContainersTool) parseEntitySearchResponse(response map[string]any) ([]map[string]any, int) {
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

func (t NewRelicInfrastructureContainersTool) simplifyEntity(entity map[string]any) map[string]any {
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

	return simplified
}

func (t NewRelicInfrastructureContainersTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getNewRelicConfigSchema()
}
