package optimizer

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/runbook/internal/model"
	"strings"
	"time"
)

type VerticalRightsizeGenerator struct {
	BaseTaskGenerator
}

func (g *VerticalRightsizeGenerator) GenerateTasks(ctx context.Context, ao model.AutoOptimize, recommendations []model.RecommendationWithResource) ([]model.AutoOptimizeTask, error) {
	var tasks []model.AutoOptimizeTask

	for _, rec := range recommendations {
		resIDStr := rec.ResourceIdentifier
		if resIDStr == "" {
			resIDStr, _ = rec.Recommendation.Recommendation["resource_id"].(string)
		}

		if resIDStr == "" {
			continue
		}

		parts := strings.Split(resIDStr, "/")
		if len(parts) < 3 {
			continue
		}
		namespace, kind, name := parts[0], parts[1], parts[2]
		if namespace == "" || kind == "" || name == "" {
			slog.Error("Invalid resource identifier", "resource_id", resIDStr)
			continue
		}

		// for kind overwrite type values, else it may result errors like pod not ofund and so on
		if strings.ToLower(kind) == "pod" && rec.AccountObjectID != nil {
			parts2 := strings.Split(*rec.AccountObjectID, "/")
			if len(parts2) < 3 {
				continue
			}
			namespace, kind, name = parts2[0], parts2[1], parts2[2]
		}

		// Basic validation: ensure we have something to update
		// Support both top-level cpu/mem AND container-level (Python style)
		hasValidUpdate := false
		if _, ok := rec.Recommendation.Recommendation["cpu"]; ok {
			hasValidUpdate = true
		} else if _, ok := rec.Recommendation.Recommendation["memory"]; ok {
			hasValidUpdate = true
		} else {
			// Check for container-level recommendations
			for _, val := range rec.Recommendation.Recommendation {
				if _, ok := val.([]any); ok {
					hasValidUpdate = true
					break
				}
			}
		}

		if !hasValidUpdate {
			continue
		}

		params := map[string]any{
			"namespace":      namespace,
			"name":           name,
			"kind":           kind,
			"account_id":     ao.AccountID.String(),
			"scale_up":       ao.Rule["scale_up"],
			"cpu":            ao.Rule["cpu"],
			"memory":         ao.Rule["memory"],
			"recommendation": rec.Recommendation.Recommendation,
			"ticket_config":  ao.Attributes.TicketConfig,
			"gitops_config":  ao.Attributes.GitOpsConfig,
		}

		task := g.CreateBaseTask(ao, rec.Recommendation)
		task.Name = fmt.Sprintf("Vertical Rightsize %s %s/%s", kind, namespace, name)

		task.Meta = params

		if ao.NextScheduleTime != nil {
			task.ScheduledTime = *ao.NextScheduleTime
		} else {
			task.ScheduledTime = time.Now().UTC()
		}

		if ao.Status == model.AutoOptimizeStatusDryrun {
			task.Attributes.DryRun = true
			task.Status = string(model.AutoOptimizeStatusDryrun)
			params["dry_run"] = true
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}
