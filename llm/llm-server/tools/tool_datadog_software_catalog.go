package tools

import (
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

const ToolDatadogSoftwareCatalog = "datadog_software_catalog_execute"

func init() {
	toolcore.RegisterNBToolFactory(ToolDatadogSoftwareCatalog, func(accountId string) (toolcore.NBTool, error) {
		return DatadogSoftwareCatalogTool{}, nil
	})
}

// NewDatadogSoftwareCatalogTool creates a new instance of DatadogSoftwareCatalogTool.
func NewDatadogSoftwareCatalogTool() toolcore.NBTool {
	return DatadogSoftwareCatalogTool{}
}

type DatadogSoftwareCatalogTool struct {
}

func (m DatadogSoftwareCatalogTool) Name() string {
	return ToolDatadogSoftwareCatalog
}

func (m DatadogSoftwareCatalogTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeTool
}

func (m DatadogSoftwareCatalogTool) Description() string {
	return `Retrieves a list of entities from the Datadog Software Catalog. Input can be a query string to filter entities.`
}

func (m DatadogSoftwareCatalogTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"query": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Optional: A query string to filter entities by name or tag.",
			},
		},
		Required: []string{},
	}
}

func (m DatadogSoftwareCatalogTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing datadog_software_catalog tool call", "query", input.Command)
	response, err := m.executeDatadogSoftwarecatalog(nbRequestContext, input.Command, nil)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute api call for datadog_software_catalog", "error", err.Error())
		return toolcore.NBToolResponse{
			Data:   "",
			Status: toolcore.NBToolResponseStatusError,
		}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize json for datadog_software_catalog", "error", err.Error())
		return toolcore.NBToolResponse{
			Data:   "",
			Status: toolcore.NBToolResponseStatusError,
		}, err
	}

	return toolcore.NBToolResponse{
		Data:       string(data),
		Type:       toolcore.NBToolResponseTypeJson,
		Status:     toolcore.NBToolResponseStatusSuccess,
		References: []toolcore.NBToolResponseReference{}, // Add relevant references if available
	}, nil
}

func (m DatadogSoftwareCatalogTool) ConfigSchema(ctx *security.RequestContext) toolcore.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func (m DatadogSoftwareCatalogTool) executeDatadogSoftwarecatalog(ctx toolcore.NbToolContext, query string, configs map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Datadog Software Catalog API v2 endpoint
	// See: https://docs.datadoghq.com/api/latest/software-catalog/#get-all-software-catalog-entities
	baseURL := fmt.Sprintf("https://%s/api/v2/catalog/entity", site)
	queryParams := url.Values{}
	if query != "" {
		queryTags := strings.SplitSeq(query, " ")
		for tag := range queryTags {
			tags := strings.Split(tag, ":")
			if len(tags) != 2 {
				continue
			}
			key := strings.TrimSpace(tags[0])
			value := strings.TrimSpace(tags[1])
			queryParams.Add(fmt.Sprintf("filter[%s]", key), value)
		}
	}

	requestURL := baseURL + "?" + queryParams.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	return doDatadogRequest(req, apiKey, appKey)
}
