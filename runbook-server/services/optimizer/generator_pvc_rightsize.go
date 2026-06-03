package optimizer

import (
	"context"
	"fmt"
	"nudgebee/runbook/internal/model"
)

type PVCRightsizeGenerator struct {
	BaseTaskGenerator
}

func (g *PVCRightsizeGenerator) GenerateTasks(ctx context.Context, ao model.AutoOptimize, recommendations []model.RecommendationWithResource) ([]model.AutoOptimizeTask, error) {
	var tasks []model.AutoOptimizeTask

	thresholdPct := 80.0 // Default threshold
	if val, ok := ao.Rule["rightsize_threshold_pct"].(float64); ok {
		thresholdPct = val
	}

	increaseByPct := 0.0
	if val, ok := ao.Rule["increase_by_pct"].(float64); ok {
		increaseByPct = val
	}

	for _, rec := range recommendations {
		// Extract recommendation details
		spec, ok := rec.Recommendation.Recommendation["spec"].(map[string]any)
		if !ok {
			continue
		}
		claimRef, ok := spec["claimRef"].(map[string]any)
		if !ok {
			continue
		}
		claimName, _ := claimRef["name"].(string)
		claimNs, _ := claimRef["namespace"].(string)

		if claimName == "" || claimNs == "" {
			continue
		}

		// Check usage threshold
		recommData, ok := rec.Recommendation.Recommendation["recommendation"].(map[string]any)
		if ok {
			capacity, _ := recommData["capacity"].(float64)
			usageMap, _ := recommData["usage"].(map[string]any)
			currentUsage, _ := usageMap["current"].(float64)

			if capacity > 0 {
				usagePct := (currentUsage / capacity) * 100.0
				if usagePct < thresholdPct {
					continue // Skip if usage is below threshold
				}
			}
		}

		// Create Task
		task := g.CreateBaseTask(ao, rec.Recommendation)
		task.Name = fmt.Sprintf("PVC rightsize for resource %s", claimName)
		task.ResourceFilter = model.AutoOptimizeResourceFilter{
			Namespace: &claimNs,
			Name:      &claimName,
			Type:      pvcStringPtr("PersistentVolumeClaim"),
		}

		// Construct Meta (Payload) for the task execution
		// Note: The Python code constructs "payload" -> "action_params"
		// The Go executor expects task.Meta to be passed to Execute(taskCtx, task.Meta)
		// For PVRightsizeTask (k8s.pv_rightsize), it expects flat params:
		// namespace, name, kind, change_by (string %), change_to, etc.

		meta := map[string]any{
			"namespace":  claimNs,
			"name":       claimName,
			"kind":       "PersistentVolumeClaim",
			"change_by":  fmt.Sprintf("%.0f%%", increaseByPct), // Format as "10%"
			"account_id": ao.AccountID.String(),
		}

		// Add GitOps/Ticket config from AutoOptimize attributes
		if ao.Attributes.GitOpsConfig.Enabled {
			meta["gitops_config"] = map[string]any{
				"enabled":         true,
				"provider":        ao.Attributes.GitOpsConfig.Provider,
				"repository_name": ao.Attributes.GitOpsConfig.RepositoryName,
			}
		}
		if ao.Attributes.TicketConfig.Enabled {
			meta["ticket_config"] = map[string]any{
				"enabled":          true,
				"configuration_id": ao.Attributes.TicketConfig.ConfigurationID,
				"project_key":      ao.Attributes.TicketConfig.ProjectKey,
				"ticket_type":      ao.Attributes.TicketConfig.TicketType,
				"assignee":         ao.Attributes.TicketConfig.Assignee,
				"severity":         ao.Attributes.TicketConfig.Severity,
			}
		}

		task.Meta = meta

		if ao.Status == model.AutoOptimizeStatusDryrun {
			task.Status = string(model.AutoOptimizeStatusDryrun)
			task.Attributes.DryRun = true
		} else {
			task.Status = string(model.AutopilotTaskStatusScheduled)
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}

func pvcStringPtr(s string) *string {
	return &s
}
