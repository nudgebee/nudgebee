package tools

import (
	"errors"
	"fmt"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

const (
	ToolNudgebeeAccountsList         = "nudgebee_accounts_list"
	ToolNudgebeeAccountsCount        = "nudgebee_accounts_count"
	ToolNudgebeeAccountGet           = "nudgebee_account_get"
	ToolNudgebeeIntegrationsList     = "nudgebee_integrations_list"
	ToolNudgebeeIntegrationsCount    = "nudgebee_integrations_count"
	ToolNudgebeeIntegrationGetStatus = "nudgebee_integration_get_status"
	ToolNudgebeeDocsSearch           = "nudgebee_docs_search"
)

var nudgebeeAccountColumns = []string{
	"id", "account_name", "account_type", "cloud_provider", "status",
	"sync_status", "synced_at", "agent_synced_at", "created_at",
}

var nudgebeeIntegrationColumns = []string{
	"id", "name", "type", "source", "status", "updated_at",
}

func init() {
	register := func(name string, factory func() core.NBTool) {
		core.RegisterNBToolFactory(name, func(string) (core.NBTool, error) {
			return factory(), nil
		})
	}
	register(ToolNudgebeeAccountsList, func() core.NBTool { return NudgebeeAccountsListTool{} })
	register(ToolNudgebeeAccountsCount, func() core.NBTool { return NudgebeeAccountsCountTool{} })
	register(ToolNudgebeeAccountGet, func() core.NBTool { return NudgebeeAccountGetTool{} })
	register(ToolNudgebeeIntegrationsList, func() core.NBTool { return NudgebeeIntegrationsListTool{} })
	register(ToolNudgebeeIntegrationsCount, func() core.NBTool { return NudgebeeIntegrationsCountTool{} })
	register(ToolNudgebeeIntegrationGetStatus, func() core.NBTool { return NudgebeeIntegrationGetStatusTool{} })
	register(ToolNudgebeeDocsSearch, func() core.NBTool { return NudgebeeDocsSearchTool{} })
}

func nudgebeeStringProperty(description string) core.ToolSchemaProperty {
	return core.ToolSchemaProperty{Type: core.ToolSchemaTypeString, Description: description}
}

func nudgebeeLimitProperty() core.ToolSchemaProperty {
	return core.ToolSchemaProperty{
		Type:        core.ToolSchemaTypeInteger,
		Description: "Maximum rows to return (default 20, maximum 100).",
	}
}

func nudgebeeReadRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func nudgebeeStringArg(input core.NBToolCallRequest, key string) string {
	return strings.TrimSpace(stringArg(input, key))
}

func nudgebeeLimit(input core.NBToolCallRequest) int {
	limit := 20
	switch value := input.Arguments["limit"].(type) {
	case float64:
		limit = int(value)
	case int:
		limit = value
	}
	if limit < 1 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// doNudgebeeQueryRequest is intentionally stricter than doRPCQueryRequest.
// api-server treats a tenant id without a user id as an internal tenant-admin
// call, which is valid for background jobs but would silently elevate an
// interactive Nubi request. Self-awareness tools therefore require a real
// requesting user before crossing the service boundary.
func doNudgebeeQueryRequest(nbCtx core.NbToolContext, action string, input map[string]any) (string, error) {
	if err := requireNudgebeeUser(nbCtx); err != nil {
		return "", err
	}
	return doRPCQueryRequest(nbCtx, action, input)
}

func requireNudgebeeUser(nbCtx core.NbToolContext) error {
	if nbCtx.Ctx == nil || nbCtx.Ctx.GetSecurityContext() == nil {
		return errors.New("nudgebee: authenticated user context is required")
	}
	if strings.TrimSpace(nbCtx.Ctx.GetSecurityContext().EffectiveUserIdForRPC()) == "" {
		return errors.New("nudgebee: requesting user is required; refusing tenant-admin fallback")
	}
	return nil
}

func nudgebeeToolResponse(nbCtx core.NbToolContext, toolName string, action string, input map[string]any) (core.NBToolResponse, error) {
	data, err := doNudgebeeQueryRequest(nbCtx, action, input)
	if err != nil {
		if nbCtx.Ctx != nil {
			nbCtx.Ctx.GetLogger().Warn("nudgebee: read tool failed", "tool", toolName, "action", action, "error", err)
		}
		return triageErrorResponse(err), nil
	}
	if nbCtx.Ctx != nil {
		nbCtx.Ctx.GetLogger().Info("nudgebee: read tool completed", "tool", toolName, "action", action)
	}
	return triageResponse(data), nil
}

type NudgebeeAccountsListTool struct{}

func (NudgebeeAccountsListTool) Name() string             { return ToolNudgebeeAccountsList }
func (NudgebeeAccountsListTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeAccountsListTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeAccountsListTool) Description() string {
	return "List Nudgebee accounts visible to the requesting user. Apply every status, provider and name constraint from the question in this first call; do not fetch an unfiltered list first. Returns only account identity, provider, status and synchronization metadata. Read-only."
}
func (NudgebeeAccountsListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"status":         nudgebeeStringProperty("Optional exact account status."),
		"cloud_provider": nudgebeeStringProperty("Optional exact cloud provider."),
		"name":           nudgebeeStringProperty("Optional case-insensitive account-name substring."),
		"limit":          nudgebeeLimitProperty(),
	}, Required: []string{}}
}
func (t NudgebeeAccountsListTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	where := map[string]any{}
	if value := nudgebeeStringArg(input, "status"); value != "" {
		where["status"] = map[string]any{"_eq": value}
	}
	if value := nudgebeeStringArg(input, "cloud_provider"); value != "" {
		where["cloud_provider"] = map[string]any{"_eq": value}
	}
	if value := nudgebeeStringArg(input, "name"); value != "" {
		where["account_name"] = map[string]any{"_ilike": "%" + value + "%"}
	}
	return nudgebeeToolResponse(nbCtx, t.Name(), "accounts_list", map[string]any{
		"columns": nudgebeeAccountColumns,
		"where":   where,
		"limit":   nudgebeeLimit(input),
		"order_by": []map[string]any{
			{"column": "account_name", "order": "asc"},
		},
	})
}

type NudgebeeAccountsCountTool struct{}

func (NudgebeeAccountsCountTool) Name() string             { return ToolNudgebeeAccountsCount }
func (NudgebeeAccountsCountTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeAccountsCountTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeAccountsCountTool) Description() string {
	return "Count Nudgebee accounts visible to the requesting user. Use status/cloud_provider filters for a specific subset; use group_by only when the user asks for a breakdown. Do not list accounts to calculate a count. Read-only."
}
func (NudgebeeAccountsCountTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"status":         nudgebeeStringProperty("Optional exact account status."),
		"cloud_provider": nudgebeeStringProperty("Optional exact cloud provider, for example K8s, AWS, GCP, or Azure."),
		"group_by":       nudgebeeStringProperty("Optional grouping: status or cloud_provider. Do not group when filtering for one provider."),
	}, Required: []string{}}
}
func (t NudgebeeAccountsCountTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	where := map[string]any{}
	if value := nudgebeeStringArg(input, "status"); value != "" {
		where["status"] = map[string]any{"_eq": value}
	}
	if value := nudgebeeStringArg(input, "cloud_provider"); value != "" {
		where["cloud_provider"] = map[string]any{"_eq": value}
	}
	columns := []string{"count"}
	query := map[string]any{"columns": columns, "where": where}
	if groupBy := nudgebeeStringArg(input, "group_by"); groupBy == "status" || groupBy == "cloud_provider" {
		query["columns"] = []string{groupBy, "count"}
		query["group_by"] = []string{groupBy}
	}
	return nudgebeeToolResponse(nbCtx, t.Name(), "accounts_aggregate", query)
}

type NudgebeeAccountGetTool struct{}

func (NudgebeeAccountGetTool) Name() string             { return ToolNudgebeeAccountGet }
func (NudgebeeAccountGetTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeAccountGetTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeAccountGetTool) Description() string {
	return "Get one Nudgebee account visible to the requesting user by exact id or exact name. Read-only."
}
func (NudgebeeAccountGetTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"id":   nudgebeeStringProperty("Exact Nudgebee account id."),
		"name": nudgebeeStringProperty("Exact Nudgebee account name."),
	}, Required: []string{}}
}
func (t NudgebeeAccountGetTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	where := map[string]any{}
	if id := nudgebeeStringArg(input, "id"); id != "" {
		where["id"] = map[string]any{"_eq": id}
	} else if name := nudgebeeStringArg(input, "name"); name != "" {
		where["account_name"] = map[string]any{"_eq": name}
	} else {
		return triageErrorResponse(errors.New("nudgebee_account_get requires id or name")), nil
	}
	return nudgebeeToolResponse(nbCtx, t.Name(), "accounts_list", map[string]any{
		"columns": nudgebeeAccountColumns, "where": where, "limit": 1,
	})
}

type NudgebeeIntegrationsListTool struct{}

func (NudgebeeIntegrationsListTool) Name() string             { return ToolNudgebeeIntegrationsList }
func (NudgebeeIntegrationsListTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeIntegrationsListTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeIntegrationsListTool) Description() string {
	return "List configured Nudgebee integrations visible to the requesting user. Apply every status, type and name constraint from the question in this first call; do not fetch an unfiltered list first. Includes type and current recorded status. Read-only."
}
func (NudgebeeIntegrationsListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"status": nudgebeeStringProperty("Optional exact integration status."),
		"type":   nudgebeeStringProperty("Optional exact integration type."),
		"name":   nudgebeeStringProperty("Optional case-insensitive integration-name substring."),
		"limit":  nudgebeeLimitProperty(),
	}, Required: []string{}}
}
func (t NudgebeeIntegrationsListTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	where := map[string]any{}
	for _, key := range []string{"status", "type"} {
		if value := nudgebeeStringArg(input, key); value != "" {
			where[key] = map[string]any{"_eq": value}
		}
	}
	if value := nudgebeeStringArg(input, "name"); value != "" {
		where["name"] = map[string]any{"_ilike": "%" + value + "%"}
	}
	return nudgebeeToolResponse(nbCtx, t.Name(), "integrations_list", map[string]any{
		"columns": nudgebeeIntegrationColumns,
		"where":   where,
		"limit":   nudgebeeLimit(input),
		"order_by": []map[string]any{
			{"column": "name", "order": "asc"},
		},
	})
}

type NudgebeeIntegrationsCountTool struct{}

func (NudgebeeIntegrationsCountTool) Name() string             { return ToolNudgebeeIntegrationsCount }
func (NudgebeeIntegrationsCountTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeIntegrationsCountTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeIntegrationsCountTool) Description() string {
	return "Count configured Nudgebee integrations visible to the requesting user. Use status/type filters for a specific subset; use group_by only when the user asks for a breakdown. Do not list integrations to calculate a count. Read-only."
}
func (NudgebeeIntegrationsCountTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"status":   nudgebeeStringProperty("Optional exact integration status."),
		"type":     nudgebeeStringProperty("Optional exact integration type, for example github, datadog, or slack."),
		"group_by": nudgebeeStringProperty("Optional grouping: status or type. Do not group when filtering for one type."),
	}, Required: []string{}}
}
func (t NudgebeeIntegrationsCountTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	where := map[string]any{}
	if value := nudgebeeStringArg(input, "status"); value != "" {
		where["status"] = map[string]any{"_eq": value}
	}
	if value := nudgebeeStringArg(input, "type"); value != "" {
		where["type"] = map[string]any{"_eq": value}
	}
	query := map[string]any{"columns": []string{"count"}, "where": where}
	if groupBy := nudgebeeStringArg(input, "group_by"); groupBy == "status" || groupBy == "type" {
		query["columns"] = []string{groupBy, "count"}
		query["group_by"] = []string{groupBy}
	}
	return nudgebeeToolResponse(nbCtx, t.Name(), "integrations_aggregate", query)
}

type NudgebeeIntegrationGetStatusTool struct{}

func (NudgebeeIntegrationGetStatusTool) Name() string             { return ToolNudgebeeIntegrationGetStatus }
func (NudgebeeIntegrationGetStatusTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeIntegrationGetStatusTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeIntegrationGetStatusTool) Description() string {
	return "Get the current recorded status of a configured Nudgebee integration by exact id or integration-name substring. Read-only."
}
func (NudgebeeIntegrationGetStatusTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"id":   nudgebeeStringProperty("Exact integration id."),
		"name": nudgebeeStringProperty("Integration-name substring, for example Datadog or Slack."),
	}, Required: []string{}}
}
func (t NudgebeeIntegrationGetStatusTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	where := map[string]any{}
	if id := nudgebeeStringArg(input, "id"); id != "" {
		where["id"] = map[string]any{"_eq": id}
	} else if name := nudgebeeStringArg(input, "name"); name != "" {
		where["name"] = map[string]any{"_ilike": "%" + name + "%"}
	} else {
		return triageErrorResponse(errors.New("nudgebee_integration_get_status requires id or name")), nil
	}
	return nudgebeeToolResponse(nbCtx, t.Name(), "integrations_list", map[string]any{
		"columns": []string{"id", "name", "type", "status", "updated_at"},
		"where":   where,
		"limit":   10,
	})
}

// NudgebeeDocsSearchTool gives the existing account-scoped documentation
// search an explicit Nudgebee-owned name without duplicating its RAG logic.
type NudgebeeDocsSearchTool struct{}

func (NudgebeeDocsSearchTool) Name() string             { return ToolNudgebeeDocsSearch }
func (NudgebeeDocsSearchTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeDocsSearchTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeDocsSearchTool) Description() string {
	return "Search indexed Nudgebee product documentation and the requesting account's authorized knowledge sources. Use for product concepts and instructions, never as evidence for current counts or status."
}
func (NudgebeeDocsSearchTool) InputSchema() core.ToolSchema {
	return DocsAgentTool{}.InputSchema()
}
func (NudgebeeDocsSearchTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if err := requireNudgebeeUser(nbCtx); err != nil {
		return triageErrorResponse(err), nil
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" {
		return triageErrorResponse(fmt.Errorf("%s requires a search query", ToolNudgebeeDocsSearch)), nil
	}
	return DocsAgentTool{}.Call(nbCtx, input)
}
