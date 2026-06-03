package tickets

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/integrations"
)

// incidentPlatforms is the set of integration types accepted by incident-only
// tasks (acknowledge / escalate / resolve). Mirrors the gate in
// validateIncidentPlatform.
var incidentPlatforms = []string{"pagerduty", "zenduty"}

// ticketPlatforms is the union of all integration types that ticket-flavored
// tasks (create / update / transition / comment / assign / get / get_comments)
// can target — the registered ticket and incident managers in
// ticket-server/services/tools.
var ticketPlatforms = []string{"jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow"}

// resolveTicketIntegrationID converts a frontend-supplied integration_id (which
// may be a UUID or a name produced by template substitution like
// `{{ Inputs.jira_env }}`) into a real UUID, scoped to the supported platforms.
// Empty input passes through so callers using extractOptionalString keep their
// "field is optional" behavior.
func resolveTicketIntegrationID(taskCtx types.TaskContext, integrationId string, expectedTypes []string) (string, error) {
	if integrationId == "" {
		return "", nil
	}
	return integrations.ResolveIntegrationID(taskCtx.GetNewRequestContext(), integrationId, expectedTypes)
}

// extractRequiredString extracts a required string parameter from the params map.
// Returns an error if the parameter is missing or not a string.
func extractRequiredString(params map[string]any, field string) (string, error) {
	val, ok := params[field].(string)
	if !ok || val == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	return val, nil
}

// extractOptionalString extracts an optional string parameter from the params map.
// Returns empty string if not present, or an error if the value is not a string.
func extractOptionalString(params map[string]any, field string) (string, error) {
	if params[field] == nil {
		return "", nil
	}
	val, ok := params[field].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return val, nil
}

// extractOptionalStringSlice extracts an optional string slice parameter from the params map.
// Accepts []any with string elements (from JSON unmarshal) or []string.
// Returns nil if not present, or an error if the value is not a valid string slice.
func extractOptionalStringSlice(params map[string]any, field string) ([]string, error) {
	if params[field] == nil {
		return nil, nil
	}

	switch v := params[field].(type) {
	case []string:
		return v, nil
	case []any:
		result := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a string", field, i)
			}
			result = append(result, s)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", field)
	}
}

// extractAssignee pulls a platform user identifier out of additional_fields.
// Accepts raw strings (Jira accountId, ServiceNow sys_id, email) and the
// { id: "..." } / { name: "..." } object shape Jira custom fields sometimes use.
// Returns "" when no assignee is set so the caller can apply its own fallback.
// additionalFieldString reads a string value from additional_fields, unwrapping the
// {id}/{name} select shape that platform fields can carry.
func additionalFieldString(additionalFields map[string]any, key string) string {
	if additionalFields == nil {
		return ""
	}
	raw, ok := additionalFields[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		if id, ok := v["id"].(string); ok && id != "" {
			return id
		}
		if name, ok := v["name"].(string); ok && name != "" {
			return name
		}
	}
	return ""
}

func extractAssignee(additionalFields map[string]any) string {
	return additionalFieldString(additionalFields, "assignee")
}

// extractAccountId extracts account_id from params, falling back to the task context value.
func extractAccountId(params map[string]any, taskCtx types.TaskContext) (string, error) {
	accountId, err := extractOptionalString(params, "account_id")
	if err != nil {
		return "", err
	}
	if accountId == "" {
		accountId = taskCtx.GetAccountID()
	}
	return accountId, nil
}

// validateIncidentPlatform validates that the selected integration is an incident management
// platform (PagerDuty or ZenDuty). Returns an error if the platform is not supported.
func validateIncidentPlatform(taskCtx types.TaskContext, integrationId string) error {
	platform, err := getIntegrationPlatform(taskCtx, integrationId)
	if err != nil {
		return err
	}
	if platform != "pagerduty" && platform != "zenduty" {
		return fmt.Errorf("this action only supports PagerDuty and ZenDuty integrations, got: %s", platform)
	}
	return nil
}

// getIntegrationPlatform looks up the platform/tool type for a given integration ID.
func getIntegrationPlatform(taskCtx types.TaskContext, integrationId string) (string, error) {
	if integrationId == "" {
		return "", errors.New("integration_id is required to determine platform")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("unable to connect to database: %w", err)
	}

	var tool string
	err = dbms.QueryRowAndScan(&tool,
		"SELECT type FROM integrations WHERE id = $1 AND tenant_id = $2",
		integrationId, taskCtx.GetTenantID(),
	)
	if err != nil {
		return "", fmt.Errorf("integration with id '%s' not found", integrationId)
	}

	return tool, nil
}
