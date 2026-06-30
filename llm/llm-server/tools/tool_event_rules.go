package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// ToolGetEventRules exposes the alert/event RULE DEFINITIONS — the rules that
// generate events (alert name, condition/threshold expr, duration, severity,
// source, enabled flag) — as opposed to triage rules (which process events
// after they fire). Lets the events agent cross-reference a noisy alert or a
// threshold suggestion to its actual rule definition and report
// disabled/misconfigured rules.
const ToolGetEventRules = "get_event_rules"

// eventRuleColumns is the curated, useful subset of event_rules columns returned
// to the LLM. Mirrors the names in the api-server query metadata for
// "event_rules_v2".
var eventRuleColumns = []string{
	"id", "alert", "name", "expr", "duration", "severity", "source",
	"category", "alert_type", "metric_provider", "namespace", "enabled",
	"is_editable", "updated_at",
}

func init() {
	core.RegisterNBToolFactory(ToolGetEventRules, func(accountId string) (core.NBTool, error) {
		return EventRulesTool{}, nil
	})
}

// EventRulesTool lists alert/event rule definitions for the current account via
// the api-server generic query engine (event_rules_v2 over /rpc/query). The
// engine enforces tenant isolation from the x-tenant-id header; this tool
// additionally narrows results to the agent's account.
type EventRulesTool struct{}

func (t EventRulesTool) Name() string             { return ToolGetEventRules }
func (t EventRulesTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t EventRulesTool) Description() string {
	return "Lists the alert/event RULE DEFINITIONS that generate events (NOT triage rules). Each rule has " +
		"alert (name), expr (the condition/threshold expression), duration, severity, source, " +
		"metric_provider, namespace and enabled. Use this to inspect how an alert is defined, " +
		"cross-reference a threshold suggestion or noisy alert to its rule, and find disabled or " +
		"misconfigured rules. Optional filters: alert (substring match), source, severity, " +
		"alert_type, namespace, enabled, limit. Read-only: recommend changes, do not apply them."
}

func (t EventRulesTool) InputSchema() core.ToolSchema {
	str := func(desc string) core.ToolSchemaProperty {
		return core.ToolSchemaProperty{Type: core.ToolSchemaTypeString, Description: desc}
	}
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"alert":      str("Optional: case-insensitive substring match on the alert name."),
			"source":     str("Optional: exact match on rule source (e.g. prometheus, datadog, pagerduty)."),
			"severity":   str("Optional: exact match on severity."),
			"alert_type": str("Optional: exact match on alert_type."),
			"namespace":  str("Optional: exact match on namespace."),
			"enabled": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Optional: only enabled (true) or only disabled (false) rules.",
			},
			"limit": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Maximum number of rules to return (default 20).",
			},
		},
		Required: []string{},
	}
}

func (t EventRulesTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t EventRulesTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if nbCtx.AccountId == "" {
		return triageErrorResponse(errors.New("an account context is required to list event rules")), nil
	}

	// Build the Hasura-style where clause. account_id narrows to the agent's
	// account; the query engine additionally enforces tenant isolation.
	where := map[string]any{
		"account_id": map[string]any{"_eq": nbCtx.AccountId},
	}
	if v := stringArg(input, "alert"); v != "" {
		where["alert"] = map[string]any{"_ilike": "%" + v + "%"}
	}
	if v := stringArg(input, "source"); v != "" {
		where["source"] = map[string]any{"_eq": v}
	}
	if v := stringArg(input, "severity"); v != "" {
		where["severity"] = map[string]any{"_eq": v}
	}
	if v := stringArg(input, "alert_type"); v != "" {
		where["alert_type"] = map[string]any{"_eq": v}
	}
	if v := stringArg(input, "namespace"); v != "" {
		where["namespace"] = map[string]any{"_eq": v}
	}
	if v, ok := input.Arguments["enabled"].(bool); ok {
		where["enabled"] = map[string]any{"_eq": v}
	}

	limit := 20
	if v, ok := input.Arguments["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	queryInput := map[string]any{
		"columns": eventRuleColumns,
		"where":   where,
		"limit":   limit,
		"order_by": []map[string]any{
			{"column": "updated_at", "order": "desc"},
		},
	}

	data, err := doRPCQueryRequest(nbCtx, "event_rules_v2", queryInput)
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("event_rules: get_event_rules failed", "error", err)
		return triageErrorResponse(err), nil
	}
	return triageResponse(data), nil
}

// doRPCQueryRequest calls an api-server /rpc/query table action (the generic
// Hasura-style query engine) and returns the raw JSON response body
// ({"rows":[...]}). Tenant isolation is enforced server-side from the
// x-tenant-id header. Mirrors the /rpc/triage call pattern in tool_triage.go.
func doRPCQueryRequest(nbCtx core.NbToolContext, tableAction string, queryInput map[string]any) (string, error) {
	tenant := ""
	if nbCtx.Ctx != nil {
		if sc := nbCtx.Ctx.GetSecurityContext(); sc != nil {
			tenant = sc.GetTenantId()
		}
	}
	if tenant == "" {
		// Fail fast on an empty account rather than issuing a pointless
		// (and tenant-ambiguous) DB lookup with id = ''.
		if nbCtx.AccountId == "" {
			return "", errors.New("query: account id is empty and tenant id is missing")
		}
		t, err := security.GetTenantIdFromAccountId(nbCtx.AccountId)
		if err != nil {
			return "", fmt.Errorf("query: resolving tenant from account %s: %w", nbCtx.AccountId, err)
		}
		tenant = t
	}
	if tenant == "" {
		return "", errors.New("query: tenant id is empty")
	}

	userId := ""
	if nbCtx.Ctx != nil {
		if sc := nbCtx.Ctx.GetSecurityContext(); sc != nil {
			userId = sc.GetUserId()
		}
	}

	payload := map[string]any{
		"action": map[string]any{"name": tableAction},
		"input":  queryInput,
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/rpc/query", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   "application/json",
			"Accept":         "application/json",
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    tenant,
			"x-user-id":      userId,
		}),
		common.HttpWithJsonBody(payload),
	)
	if err != nil {
		return "", fmt.Errorf("query: %s, unable to process request: %w", tableAction, err)
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				slog.Info("query: failed to close response body", "error", cerr)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("query: %s, reading response body: %w", tableAction, err)
	}

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("query: %s unauthorized: %s", tableAction, string(jsonBody))
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("query: %s failed (status %d): %s", tableAction, resp.StatusCode, string(jsonBody))
	}

	trimmed := bytes.TrimLeft(jsonBody, " \t\r\n")
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", fmt.Errorf("query: %s unexpected response shape: %s", tableAction, string(jsonBody))
	}

	return string(jsonBody), nil
}
