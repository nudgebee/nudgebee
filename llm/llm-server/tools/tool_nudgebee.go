package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

const (
	nudgebeeDocsModule          = "knowledge_base"
	nudgebeeDocsSource          = "nudgebee_docs"
	nudgebeeDocsResultLimit     = 5
	nudgebeeDocsExcerptMaxRunes = 1600
	nudgebeeDocsOutputMaxRunes  = 6000
	nudgebeeDocsNoResult        = "No matching Nudgebee product documentation was found for this query."
)

var (
	queryNudgebeeDocs = core.QueryRAG
	nudgebeeHTMLTags  = regexp.MustCompile(`(?i)</?[a-z](?:[^'">]|"[^"]*"|'[^']*')*?>`)
)

const (
	ToolNudgebeeAccountsList         = "nudgebee_accounts_list"
	ToolNudgebeeAccountsCount        = "nudgebee_accounts_count"
	ToolNudgebeeAccountGet           = "nudgebee_account_get"
	ToolNudgebeeIntegrationsList     = "nudgebee_integrations_list"
	ToolNudgebeeIntegrationsCount    = "nudgebee_integrations_count"
	ToolNudgebeeIntegrationGetStatus = "nudgebee_integration_get_status"
	ToolNudgebeeIntegrationDiagnose  = "nudgebee_integration_diagnose"
	ToolNudgebeeAgentHealthGet       = "nudgebee_agent_health_get"
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
	register(ToolNudgebeeIntegrationDiagnose, func() core.NBTool { return NudgebeeIntegrationDiagnoseTool{} })
	register(ToolNudgebeeAgentHealthGet, func() core.NBTool { return NudgebeeAgentHealthGetTool{} })
	register(ToolNudgebeeDocsSearch, func() core.NBTool { return NudgebeeDocsSearchTool{} })
}

var nudgebeeAgentFeatureKeys = map[string]bool{
	"relayConnection": true, "prometheusConnection": true, "alertManagerConnection": true,
	"logsConnection": true, "nodeAgentConnection": true, "opencostConnection": true,
	"opencostServerSide": true, "tracesEnabled": true, "grafanaEnabled": true,
	"autoScalerEnabled": true, "nodeAgentCount": true, "logsConnectionProvider": true,
	"traceProvider": true, "prometheusRetentionTime": true, "installationNamespace": true,
}

// NudgebeeAgentHealthGetTool returns the platform-recorded collector heartbeat
// and a narrow feature-health allowlist. Raw connection_status includes URLs,
// provider configuration and schedules, so it must never be passed through.
type NudgebeeAgentHealthGetTool struct{}

func (NudgebeeAgentHealthGetTool) Name() string             { return ToolNudgebeeAgentHealthGet }
func (NudgebeeAgentHealthGetTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeAgentHealthGetTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeAgentHealthGetTool) Description() string {
	return "Get Nudgebee-recorded agent or collector health for one visible account: heartbeat, status, version, Kubernetes metadata, and sanitized feature connectivity. Use for Nudgebee agent disconnected, Prometheus disconnected, or collector health questions. Ready Kubernetes workloads alone do not prove this health. Read-only."
}
func (NudgebeeAgentHealthGetTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"account_id": nudgebeeStringProperty("Optional exact visible account id; defaults to the current account."),
	}, Required: []string{}}
}
func (t NudgebeeAgentHealthGetTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	accountID := nudgebeeStringArg(input, "account_id")
	if accountID == "" {
		accountID = strings.TrimSpace(nbCtx.AccountId)
	}
	if accountID == "" {
		return triageErrorResponse(errors.New("nudgebee_agent_health_get requires an account id")), nil
	}
	data, err := doNudgebeeQueryRequest(nbCtx, "agents_list_health", map[string]any{
		"columns": []string{"id", "cloud_account_id", "type", "version", "status_message", "status", "last_connected_at", "created_at", "k8s_version", "k8s_provider", "connection_status"},
		"where":   map[string]any{"cloud_account_id": map[string]any{"_eq": accountID}},
		"limit":   20,
	})
	if err != nil {
		return triageErrorResponse(err), nil
	}
	var result struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return triageErrorResponse(errors.New("nudgebee: agent health returned an invalid response")), nil
	}
	for _, row := range result.Rows {
		features := map[string]any{}
		connectionStatus, _ := row["connection_status"].(map[string]any)
		if encoded, ok := row["connection_status"].(string); ok {
			_ = json.Unmarshal([]byte(encoded), &connectionStatus)
		}
		for key, value := range connectionStatus {
			if nudgebeeAgentFeatureKeys[key] {
				features[key] = value
			}
		}
		row["features"] = features
		delete(row, "connection_status")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return triageErrorResponse(errors.New("nudgebee: could not format agent health")), nil
	}
	return triageResponse(string(encoded)), nil
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

// NudgebeeIntegrationDiagnoseTool runs the api-server's bounded connection
// diagnostic after the planner has resolved a visible integration to its id.
// api-server independently rechecks linked-account read access and returns only
// classified, sanitized failure details.
type NudgebeeIntegrationDiagnoseTool struct{}

func (NudgebeeIntegrationDiagnoseTool) Name() string { return ToolNudgebeeIntegrationDiagnose }
func (NudgebeeIntegrationDiagnoseTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}
func (NudgebeeIntegrationDiagnoseTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeIntegrationDiagnoseTool) Description() string {
	return "Safely diagnose one configured Nudgebee integration by exact id. Use only when the user explicitly asks why an integration is not working or connected. First resolve a name to an id with nudgebee_integration_get_status. Returns whether an active test passed, failed, or is unsupported, plus normalized health, failure stage, reason code, and sanitized summary; it never returns credentials or raw provider errors."
}
func (NudgebeeIntegrationDiagnoseTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"id": nudgebeeStringProperty("Exact integration id returned by nudgebee_integration_get_status."),
	}, Required: []string{"id"}}
}
func (NudgebeeIntegrationDiagnoseTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if err := requireNudgebeeUser(nbCtx); err != nil {
		return triageErrorResponse(err), nil
	}
	integrationID := nudgebeeStringArg(input, "id")
	if integrationID == "" {
		return triageErrorResponse(errors.New("nudgebee_integration_diagnose requires an exact integration id")), nil
	}
	data, err := doApiServerActionRequest(nbCtx, "/rpc/integration", "integrations_diagnose_connection", map[string]any{
		"request": map[string]any{"integration_id": integrationID},
	}, ToolNudgebeeIntegrationDiagnose)
	if err != nil {
		return triageErrorResponse(err), nil
	}
	return triageResponse(data), nil
}

// NudgebeeDocsSearchTool searches only the centrally indexed Nudgebee product
// documentation. It deliberately does not fall back to tenant knowledge bases.
type NudgebeeDocsSearchTool struct{}

func (NudgebeeDocsSearchTool) Name() string             { return ToolNudgebeeDocsSearch }
func (NudgebeeDocsSearchTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (NudgebeeDocsSearchTool) InferToolRequestType(ctx *security.RequestContext, input, conversation string) (core.ToolRequestType, error) {
	return nudgebeeReadRequestType(ctx, input, conversation)
}
func (NudgebeeDocsSearchTool) Description() string {
	return "Search indexed Nudgebee product documentation. Use once for product concepts, features, setup, and instructions; never use it as evidence for current tenant counts, configuration, or status."
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

	started := time.Now()
	userID := nbCtx.Ctx.GetSecurityContext().EffectiveUserIdForRPC()
	results := queryNudgebeeDocs(
		userID, nbCtx.AccountId, input.Command, nudgebeeDocsModule,
		nudgebeeDocsResultLimit, nbCtx.ConversationId, nbCtx.MessageId,
		nbCtx.ParentAgentId, true, map[string]any{"source": nudgebeeDocsSource},
	)
	data, references, selected, truncated := formatNudgebeeDocsResults(results)
	if nbCtx.Ctx != nil {
		nbCtx.Ctx.GetLogger().Info("nudgebee: product docs search completed",
			"source", nudgebeeDocsSource,
			"result_count", len(results),
			"selected_count", selected,
			"no_result", selected == 0,
			"truncated", truncated,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}
	return core.NBToolResponse{
		Data: data, Type: core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess, References: references,
	}, nil
}

func formatNudgebeeDocsResults(results core.RAGSearchResults) (string, []core.NBToolResponseReference, int, bool) {
	var blocks []string
	var references []core.NBToolResponseReference
	seenDocuments := make(map[string]bool)
	seenURLs := make(map[string]bool)
	totalRunes := utf8.RuneCountInString("Nudgebee product documentation:\n")
	truncated := false

	for _, result := range results {
		title := nudgebeeMetadataString(result.Metadata, "title")
		section := nudgebeeMetadataString(result.Metadata, "section")
		if section == "" {
			section = nudgebeeMetadataString(result.Metadata, "path")
		}
		sourceURL := nudgebeeDocsURL(result.Metadata)
		excerpt := normalizeNudgebeeDocsText(result.Document)
		if excerpt == "" {
			continue
		}
		if utf8.RuneCountInString(excerpt) > nudgebeeDocsExcerptMaxRunes {
			excerpt = truncateNudgebeeDocsText(excerpt, nudgebeeDocsExcerptMaxRunes)
			truncated = true
		}
		dedupeKey := strings.ToLower(sourceURL + "\x00" + title + "\x00" + section + "\x00" + excerpt)
		if seenDocuments[dedupeKey] {
			continue
		}
		seenDocuments[dedupeKey] = true

		var lines []string
		if title != "" {
			lines = append(lines, "Title: "+title)
		}
		if section != "" {
			lines = append(lines, "Section: "+section)
		}
		lines = append(lines, "Evidence: "+excerpt)
		if sourceURL != "" {
			lines = append(lines, "Source: "+sourceURL)
		}
		block := fmt.Sprintf("[%d] %s", len(blocks)+1, strings.Join(lines, "\n"))
		blockRunes := utf8.RuneCountInString(block) + 2
		if totalRunes+blockRunes > nudgebeeDocsOutputMaxRunes {
			truncated = true
			break
		}
		blocks = append(blocks, block)
		totalRunes += blockRunes

		if sourceURL != "" && !seenURLs[sourceURL] {
			seenURLs[sourceURL] = true
			label := title
			if label == "" {
				label = sourceURL
			}
			references = append(references, core.NBToolResponseReference{Text: label, Url: sourceURL, Type: "link"})
		}
	}
	if len(blocks) == 0 {
		return nudgebeeDocsNoResult, nil, 0, truncated
	}
	return "Nudgebee product documentation:\n" + strings.Join(blocks, "\n\n"), references, len(blocks), truncated
}

func normalizeNudgebeeDocsText(value string) string {
	unescaped := html.UnescapeString(value)
	stripped := nudgebeeHTMLTags.ReplaceAllString(unescaped, " ")
	return strings.Join(strings.Fields(stripped), " ")
}

func nudgebeeMetadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}
	return normalizeNudgebeeDocsText(value)
}

func nudgebeeDocsURL(metadata map[string]any) string {
	raw, ok := metadata["url"].(string)
	if !ok {
		return ""
	}
	raw = html.UnescapeString(strings.TrimSpace(raw))
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func truncateNudgebeeDocsText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	if maxRunes == 1 {
		return "…"
	}
	keptRunes := 0
	for byteIndex := range value {
		if keptRunes == maxRunes-1 {
			return strings.TrimSpace(value[:byteIndex]) + "…"
		}
		keptRunes++
	}
	return value
}
