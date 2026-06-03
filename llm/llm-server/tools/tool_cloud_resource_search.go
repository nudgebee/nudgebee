package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
)

const ToolCloudResourceSearch = "cloud_resource_search_execute"

func init() {
	core.RegisterNBToolFactory(ToolCloudResourceSearch, func(accountId string) (core.NBTool, error) {
		return CloudResourceSearchTool{}, nil
	})
}

type CloudResourceSearchTool struct{}

type CloudResourceSearchRequest struct {
	ResourceName string `json:"resource_name,omitempty"`
	Service      string `json:"service,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	Region       string `json:"region,omitempty"`
}

type CloudResourceSearchResponse struct {
	Resources []CloudResourceInfo `json:"resources,omitempty"`
	Message   string              `json:"message,omitempty"`
}

type CloudResourceInfo struct {
	Name        string `json:"name" db:"name"`
	Type        string `json:"type" db:"type"`
	Service     string `json:"service" db:"service"`
	Region      string `json:"region" db:"region"`
	AccountID   string `json:"account_id" db:"account_id"`
	AccountName string `json:"account_name,omitempty" db:"account_name"`
}

func (r CloudResourceSearchTool) Name() string {
	return ToolCloudResourceSearch
}

func (r CloudResourceSearchTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (r CloudResourceSearchTool) Description() string {
	return `Searches for cloud resources (AWS, GCP, Azure) indexed in the database.

**Usage:**
* **Resource Discovery:** Find cloud resources by name, service, or type.
* **Service-specific Search:** Search for resources within a specific service (e.g., S3, EC2, CloudSQL).

**Input:** JSON with the following fields:
* resource_name (optional): Name or partial name of the resource.
* service (optional): Cloud service name (e.g., 'EC2', 'S3', 'Lambda').
* resource_type (optional): Type of resource (e.g., 'instance', 'bucket').
* region (optional): Region to search in.

**Output:** JSON with matching resource details.`
}

func (r CloudResourceSearchTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"resource_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name or partial name of the resource",
			},
			"service": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cloud service name",
			},
			"resource_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Type of resource",
			},
			"region": {
				Type:        core.ToolSchemaTypeString,
				Description: "Region to search in",
			},
		},
	}
}

func (r CloudResourceSearchTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	var request CloudResourceSearchRequest
	inputData := input.Command
	if inputData == "" && input.Arguments != nil {
		if b, err := common.MarshalJson(input.Arguments); err == nil {
			inputData = string(b)
		}
	}

	if err := common.UnmarshalJson([]byte(inputData), &request); err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}

	// 1. Get Tenant ID from the current Account
	var tenantId string
	_ = dbms.Db.Get(&tenantId, "SELECT tenant FROM cloud_accounts WHERE id = $1", nbRequestContext.AccountId)

	// 2. Query resources across all accounts in the tenant OR fallback to current account
	// Require at least one search criterion to avoid returning all cloud resources
	if request.ResourceName == "" && request.Service == "" && request.ResourceType == "" && request.Region == "" {
		response := CloudResourceSearchResponse{
			Message: "Please provide at least one search criterion (resource_name, service, resource_type, or region).",
		}
		responseJSON, _ := common.MarshalJson(response)
		return core.NBToolResponse{
			Data:   string(responseJSON),
			Type:   core.NBToolResponseTypeJson,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	var query string
	var args []interface{}
	if tenantId != "" {
		query = `
			SELECT cr.name, cr.type, cr.service_name as service, cr.region, cr.account as account_id, ca.account_name
			FROM cloud_resourses cr
			JOIN cloud_accounts ca ON cr.account = ca.id
			WHERE ca.tenant = $1 AND cr.is_active = true AND ca.status = 'active'
		`
		args = append(args, tenantId)
	} else {
		query = `
			SELECT cr.name, cr.type, cr.service_name as service, cr.region, cr.account as account_id, ca.account_name
			FROM cloud_resourses cr
			LEFT JOIN cloud_accounts ca ON cr.account = ca.id
			WHERE cr.account = $1 AND cr.is_active = true
		`
		args = append(args, nbRequestContext.AccountId)
	}
	argIdx := 2

	if request.ResourceName != "" {
		query += fmt.Sprintf(" AND cr.name ILIKE $%d", argIdx)
		args = append(args, "%"+request.ResourceName+"%")
		argIdx++
	}
	if request.Service != "" {
		query += fmt.Sprintf(" AND cr.service_name ILIKE $%d", argIdx)
		args = append(args, "%"+request.Service+"%")
		argIdx++
	}
	if request.ResourceType != "" {
		query += fmt.Sprintf(" AND cr.type ILIKE $%d", argIdx)
		args = append(args, "%"+request.ResourceType+"%")
		argIdx++
	}
	if request.Region != "" {
		query += fmt.Sprintf(" AND cr.region = $%d", argIdx)
		args = append(args, request.Region)
	}

	query += " ORDER BY ca.account_name ASC, cr.name ASC LIMIT 20"

	var resources []CloudResourceInfo
	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			nbRequestContext.Ctx.GetLogger().Warn("cloud_resource_search: failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var res CloudResourceInfo
		if err := rows.StructScan(&res); err == nil {
			resources = append(resources, res)
		}
	}

	nbRequestContext.Ctx.GetLogger().Info("cloud_resource_search: db lookup result", "name", request.ResourceName, "count", len(resources), "tenant", tenantId)

	response := CloudResourceSearchResponse{
		Resources: resources,
		Message:   fmt.Sprintf("Found %d cloud resources matching your query.", len(resources)),
	}

	responseJSON, _ := common.MarshalJson(response)
	return core.NBToolResponse{
		Data:   string(responseJSON),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
