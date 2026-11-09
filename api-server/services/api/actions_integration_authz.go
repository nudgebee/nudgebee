package api

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"slices"
	"strings"
)

// integrationActionClass is the custom-role class each /rpc/integration action
// needs. It mirrors what app/src/lib/permissionCatalog.ts classifyAction gives
// the routed actions (all of module `integrations`), so the gateway and this
// handler agree on one (module, class) per action.
//
// Until this table existed the gateway was the only gate on these handlers: a
// request that reached the API server another way (any holder of the service
// token, a server-to-server caller acting as a user) ran every write with
// tenant isolation as its only check. This is the half that holds if the
// gateway decision is ever wrong or bypassed.
//
// Unknown action names are refused (fail closed), so a new case in
// handleIntegrationAction must be added here too —
// TestIntegrationActionClassCoversEveryRoutedAction pins the table against
// actions.yaml.
var integrationActionClass = map[string]string{
	"integrations_create_config":           "Write",
	"integrations_delete_config":           "Write",
	"integrations_update_status":           "Write",
	"integrations_update_mcp_write_tools":  "Write",
	"integrations_upsert_discovery_target": "Write",
	"integrations_check_llm_defaults":      "Read",
	"integrations_get_schema":              "Read",
	"integrations_autogen_options":         "Read",
	"integrations_check_connection":        "Read",
	"integrations_check_connection_config": "Read",
	"integrations_list_es_indexes":         "Read",
	// Legacy aliases of the check_* actions, still matched by the switch but no
	// longer routed by actions.yaml.
	"integrations_test_connection":        "Read",
	"integrations_test_connection_config": "Read",
	// Not routed by actions.yaml, so reachable only server-to-server. Held to
	// the same bar as the routed actions rather than left open.
	"integration_list_config":          "Read",
	"integrations_trigger_config_push": "Write",
}

// integrationActionsAuthorizedInHandler do their own authorization inside the
// handler and are deliberately not gated by integrationActionClass:
//   - integrations_diagnose_connection is open to account admins (actions.yaml)
//     and checks read access to a linked account per integration.
//   - webhook_subject_mappings_sync requires a tenant or super admin.
var integrationActionsAuthorizedInHandler = map[string]bool{
	"integrations_diagnose_connection": true,
	"webhook_subject_mappings_sync":    true,
}

// workflowWebhookIntegration is the integration type runbook-server creates and
// deletes on the user's behalf when a workflow with a webhook trigger is saved
// or removed (runbook-server/services/integrations/service.go). Those calls
// carry the end user's identity, and runbook-server has already authorized the
// workflow write — with built-in account access OR a workflows:Write grant, not
// an integrations grant — so the same rule is applied here.
const workflowWebhookIntegration = "workflow_webhook"

// integrationActionTarget is the part of an action's request the authorization
// decision reads. Fields are "" / nil when the request does not carry them.
type integrationActionTarget struct {
	IntegrationName       string
	IntegrationConfigName string
	IntegrationId         string
	Source                string
	AccountIds            []string
	// Ambiguous is set when the request spells one of these fields more than
	// once in different case ("integration_name" and "INTEGRATION_NAME"). The
	// handlers' decoder takes the exact-case key, this check would take
	// whichever it met last, so no decision that reads the target is safe.
	Ambiguous bool
}

// listWorkflowWebhookAccountIDs returns the accounts the tenant's workflow
// webhook of that name and source is linked to. Swapped in tests.
var listWorkflowWebhookAccountIDs = func(ctx *security.RequestContext, configName, source string) ([]string, error) {
	return core.ListLinkedCloudAccountIDs(ctx, workflowWebhookIntegration, configName, source)
}

// authorizeIntegrationAction reports whether the caller may run actionName.
// It reads only the fields it needs, leniently; validating the request is left
// to the handler, so an authorized caller still gets the handler's 400 for a
// malformed one.
func authorizeIntegrationAction(ctx *security.RequestContext, actionName string, input map[string]any) bool {
	var target integrationActionTarget
	if raw, ok := input["request"].(map[string]any); ok {
		target = integrationTargetFromRequest(raw)
	}
	return canRunIntegrationAction(ctx, actionName, target)
}

// integrationTargetFromRequest matches keys case-insensitively, as the
// handlers' decoder (common.UnmarshalMapToStruct, mapstructure) does, so a key
// spelled "Integration_Id" cannot reach the handler unseen by this check. Two
// spellings of the same field mark the target Ambiguous: mapstructure prefers
// the exact-case key, and this loop has no such order.
func integrationTargetFromRequest(raw map[string]any) integrationActionTarget {
	var target integrationActionTarget
	seen := map[string]bool{}
	for key, value := range raw {
		field := strings.ToLower(key)
		switch field {
		case "integration_name", "integration_config_name", "integration_id", "source", "account_ids":
			if seen[field] {
				target.Ambiguous = true
			}
			seen[field] = true
		}
		switch field {
		case "integration_name":
			target.IntegrationName, _ = value.(string)
		case "integration_config_name":
			target.IntegrationConfigName, _ = value.(string)
		case "integration_id":
			target.IntegrationId, _ = value.(string)
		case "source":
			target.Source, _ = value.(string)
		case "account_ids":
			list, _ := value.([]any)
			for _, a := range list {
				if s, ok := a.(string); ok {
					target.AccountIds = append(target.AccountIds, s)
				}
			}
		}
	}
	return target
}

func canRunIntegrationAction(ctx *security.RequestContext, actionName string, target integrationActionTarget) bool {
	if integrationActionsAuthorizedInHandler[actionName] {
		return true
	}
	class, ok := integrationActionClass[actionName]
	if !ok {
		return false
	}
	sc := ctx.GetSecurityContext()
	if sc.CanManage("integrations", class) {
		return true
	}
	// The gateway lets an integrations:Write grant satisfy a Read action, so
	// this side does too.
	if class == "Read" && sc.HasPermission("integrations", "Write") {
		return true
	}
	// Everything below decides by what the request targets.
	if target.Ambiguous {
		return false
	}
	if target.IntegrationName == workflowWebhookIntegration {
		return canManageWorkflowWebhook(ctx, actionName, target)
	}
	return false
}

// canManageWorkflowWebhook applies runbook-server's workflow-write rule to every
// account the webhook is or will be linked to.
//   - create: no integration_id. A create whose name already exists is promoted
//     to an update of that row by core.CreateIntegrationConfig, which also
//     unlinks every account the request does not name — so the accounts the row
//     is linked to now must pass as well as the ones the request names.
//   - delete: the row is found by type, name and source, so it cannot reach
//     another type; the accounts are the ones it is linked to now. Only a row
//     with no links falls back to the accounts the request names.
//
// The source lookup defaults to "user", as the handlers do.
func canManageWorkflowWebhook(ctx *security.RequestContext, actionName string, target integrationActionTarget) bool {
	source := target.Source
	if source == "" {
		source = "user"
	}
	linked, err := listWorkflowWebhookAccountIDs(ctx, target.IntegrationConfigName, source)
	if err != nil {
		ctx.GetLogger().Error("integrations: failed to list workflow webhook accounts for authorization",
			"error", err, "integration_config_name", target.IntegrationConfigName)
		return false
	}

	var accountIds []string
	var access security.SecurityAccessType
	switch actionName {
	case "integrations_create_config":
		if target.IntegrationId != "" || len(target.AccountIds) == 0 {
			return false
		}
		accountIds, access = append(slices.Clone(target.AccountIds), linked...), security.SecurityAccessTypeCreate
	case "integrations_delete_config":
		accountIds, access = linked, security.SecurityAccessTypeDelete
		if len(accountIds) == 0 {
			accountIds = target.AccountIds
		}
	default:
		return false
	}
	if len(accountIds) == 0 {
		return false
	}
	sc := ctx.GetSecurityContext()
	for _, accountId := range accountIds {
		if !sc.HasAccountAccess(accountId, access) && !sc.HasScopedPermission(accountId, "workflows", "Write") {
			return false
		}
	}
	return true
}
