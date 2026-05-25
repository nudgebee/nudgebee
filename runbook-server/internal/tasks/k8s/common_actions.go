package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/service"
	"nudgebee/runbook/services/ticket"
	"time"
)

// HandleGitOpsOrTicket checks for GitOps or Ticket configuration and handles them if enabled.
// Returns (result, handled, error). If handled is true, the caller should return result immediately.
func HandleGitOpsOrTicket(
	taskCtx types.TaskContext,
	params map[string]any,
	kind, namespace, name string,
	description string,
	changeType string,
	prData map[string]any,
	baseResult map[string]any,
	resolverType string,
	resolverID string,
	recommendationID string,
) (map[string]any, bool, error) {

	// 1. GitOps Handling
	gitopsConfigMap, _ := params["gitops_config"].(map[string]any)
	gitopsEnabled := false
	if gitopsConfigMap != nil {
		if enabled, ok := gitopsConfigMap["enabled"].(bool); ok {
			gitopsEnabled = enabled
		}
	}

	if gitopsEnabled {
		taskCtx.GetLogger().Info("GitOps enabled: Creating Pull Request instead of applying patch")

		providerType := "github"
		providerConfigName := ""
		integrationID := ""
		if gitopsConfigMap != nil {
			if p, ok := gitopsConfigMap["provider"].(string); ok && p != "" {
				providerType = p
			}
			if p, ok := gitopsConfigMap["name"].(string); ok && p != "" {
				providerConfigName = p
			}
			if p, ok := gitopsConfigMap["repository_name"].(string); ok && p != "" {
				providerConfigName = p
			}
			if p, ok := gitopsConfigMap["integration_id"].(string); ok && p != "" {
				integrationID = p
			}
		}

		userID := taskCtx.GetUserID()
		tenantID := taskCtx.GetTenantID()
		accountID := taskCtx.GetAccountID()
		if id, ok := params["account_id"].(string); ok && id != "" {
			accountID = id
		}

		// gitops_config.integration_id is the UUID saved by the schema's
		// PropertyTypeTicket dropdown. RaisePR / RecommendationResolve expect
		// the integration *name* in ProviderConfig.Name, so resolve the UUID
		// to a name when provided. Falls through silently when name/
		// repository_name was already supplied (legacy callers).
		if providerConfigName == "" && integrationID != "" {
			if cfg, err := integrations.GetIntegration(taskCtx.GetNewRequestContext(), accountID, providerType, integrationID); err == nil && cfg.Name != "" {
				providerConfigName = cfg.Name
			} else if err != nil {
				return nil, false, fmt.Errorf("failed to resolve %s integration %s: %w", providerType, integrationID, err)
			}
		}

		resourceID, ok := params["resource_id"].(string)
		if !ok || resourceID == "" {
			// Try to query resource ID
			id, err := service.GetResourceID(taskCtx.GetNewRequestContext(), accountID, namespace, name, kind)
			if err == nil && id != "" {
				resourceID = id
			} else {
				return nil, false, errors.New("unable to find resource - " + kind + " " + namespace + "/" + name)
			}
		}

		// Ensure rich_description is in PR data
		if prData == nil {
			prData = make(map[string]any)
		}
		if resolverType == "" {
			resolverType = "AutoRunbook"
		}

		result := map[string]any{}
		if resolverType == "AutoOptimize" {
			resolutionID, err := service.RecommendationResolve(taskCtx.GetNewRequestContext(), service.RecommendationResolutionRequest{
				AccountID:        accountID,
				RecommendationID: recommendationID,
				Data:             prData,
				Provider:         providerType,
				ProviderConfig: service.ProviderConfig{
					Name: providerConfigName,
				},
				ResolverType: resolverType,
				ResolverID:   resolverID,
			})
			if err != nil {
				return nil, false, fmt.Errorf("failed to resolve recommendation: %w", err)
			}
			result["status"] = "resolved"
			result["resolution_id"] = resolutionID
			result["description"] = description
		} else {
			prInput := service.GitPushRequest{
				CreatedBy:    userID,
				TenantID:     tenantID,
				ChangeType:   changeType,
				AccountID:    accountID,
				ResolverType: resolverType,
				ResourceID:   resourceID,
				Data:         prData,
				Provider:     providerType,
				ProviderConfig: service.ProviderConfig{
					Name: providerConfigName,
				},
				ReferenceLink: getWorkflowBaseLink(taskCtx),
				ResolverID:    resolverID,
			}

			resolutionID, err := service.RaisePR(taskCtx.GetNewRequestContext(), prInput)
			if err != nil {
				return nil, false, fmt.Errorf("failed to create pull request: %w", err)
			}
			result := copyMap(baseResult)
			result["status"] = "pr_created"
			result["resolution_id"] = resolutionID
			result["description"] = description
		}

		return result, true, nil
	}

	// 2. Ticket Handling
	ticketConfigMap, _ := params["ticket_config"].(map[string]any)
	ticketEnabled := false
	if ticketConfigMap != nil {
		if enabled, ok := ticketConfigMap["enabled"].(bool); ok {
			ticketEnabled = enabled
		}
	}

	if ticketEnabled {
		taskCtx.GetLogger().Info("Ticket enabled: Creating Ticket instead of applying patch")

		configID := ""
		if val, ok := ticketConfigMap["configuration_id"].(string); ok {
			configID = val
		}

		accountID := taskCtx.GetAccountID()
		if id, ok := params["account_id"].(string); ok && id != "" {
			accountID = id
		}

		finalDescription := description
		if userDesc, ok := ticketConfigMap["description"].(string); ok && userDesc != "" {
			finalDescription = userDesc + "\n\n" + description
		}

		ticketReq := ticket.CreateTicketRequest{
			Title:         fmt.Sprintf("AutoOptimize: %s %s/%s", kind, namespace, name),
			Description:   finalDescription,
			TicketType:    "Task",
			IntegrationId: configID,
			AccountId:     accountID,
			ReferenceId:   taskCtx.GetWorkflowRunID(),
			Source:        "auto_optimize",
		}
		if val, ok := ticketConfigMap["ticket_type"].(string); ok && val != "" {
			ticketReq.TicketType = val
		}
		if val, ok := ticketConfigMap["assignee"].(string); ok {
			ticketReq.Assignee = val
		}
		if val, ok := ticketConfigMap["severity"].(string); ok {
			ticketReq.Severity = val
		}
		if val, ok := ticketConfigMap["project_key"].(string); ok {
			ticketReq.ProjectKey = val
		}
		if val, ok := ticketConfigMap["source"].(string); ok && val != "" {
			ticketReq.Source = val
		}
		if val, ok := ticketConfigMap["additional_fields"].(map[string]any); ok {
			ticketReq.AdditionalFields = val
		}

		ticketResp, err := ticket.CreateTicket(taskCtx.GetNewRequestContext(), ticketReq)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create ticket: %w", err)
		}

		result := copyMap(baseResult)
		result["status"] = "ticket_created"
		result["ticket_id"] = ticketResp.Id
		result["resolution_id"] = ticketResp.Id // Ensure it's picked up by activities.go
		result["description"] = description
		if ticketResp.URL != "" {
			result["ticket_url"] = ticketResp.URL
		}
		return result, true, nil
	}

	return nil, false, nil
}

func copyMap(m map[string]any) map[string]any {
	cp := make(map[string]any)
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func GetTraceabilityAnnotation(taskCtx types.TaskContext, resolverType, resolverID, moduleName string) (string, string, error) {
	annotationKey := fmt.Sprintf("workloads.nudgebee.com/v1.%s", moduleName)

	val := map[string]string{
		"tenant_id":    taskCtx.GetTenantID(),
		"account_id":   taskCtx.GetAccountID(),
		"time":         time.Now().UTC().Format(time.RFC3339),
		"module_name":  resolverType,
		"id":           resolverID,
		"execution_id": taskCtx.GetWorkflowRunID(),
	}

	// If it's AutoOptimize, module_name might be the rule name?
	// User said: "module_name": "<optimizer_name, workflow_name>"
	// If AutoRunbook, it's workflow name.
	if resolverType == "AutoRunbook" {
		val["module_name"] = taskCtx.GetWorkflowName()
	}

	jsonBytes, err := common.MarshalJson(val)
	if err != nil {
		return "", "", err
	}

	return annotationKey, string(jsonBytes), nil
}
