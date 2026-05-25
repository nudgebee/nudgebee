package optimizer

import (
	"context"
	"fmt"
	"nudgebee/runbook/internal/model"
	"strings"
	"time"
)

type HorizontalRightsizeGenerator struct {
	BaseTaskGenerator
}

func (g *HorizontalRightsizeGenerator) GenerateTasks(ctx context.Context, ao model.AutoOptimize, recommendations []model.RecommendationWithResource) ([]model.AutoOptimizeTask, error) {
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

		// Extract recommendation details
		targetReplicas, ok := g.extractReplicas(rec.Recommendation)
		if !ok {
			// Skip if data is missing or invalid type
			continue
		}

		params := map[string]interface{}{
			"namespace":     namespace,
			"name":          name,
			"kind":          kind,
			"account_id":    ao.AccountID.String(),
			"change_to":     targetReplicas,
			"scale_up":      ao.Rule["scale_up"],
			"min":           ao.Rule["min_replicas"],
			"max":           ao.Rule["max_replicas"],
			"ticket_config": ao.Attributes.TicketConfig,
			"gitops_config": ao.Attributes.GitOpsConfig,
		}

		task := g.CreateBaseTask(ao, rec.Recommendation)
		task.Name = fmt.Sprintf("Horizontal Rightsize %s %s/%s", kind, namespace, name)
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

func (g *HorizontalRightsizeGenerator) extractReplicas(rec model.Recommendation) (float64, bool) {
	// 1. Simple Top-level
	if val, ok := rec.Recommendation["recommended_replicas"].(float64); ok {
		return val, true
	}

	// 2. Nested Recommendation Blob (Python Parity)
	if replicaData, ok := rec.Recommendation["recommendation"].(map[string]any); ok {
		// 2a. Normal nested
		if val, ok := replicaData["recommended_replica"].(float64); ok {
			return val, true
		}

		// 2b. ML Based (Time-series)
		if recommended, ok := replicaData["recommended"].(map[string]any); ok {
			// Look for current hour + 1 (Python logic)
			nextHour := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
			timeKey := nextHour.Format("2006-01-02T15:04:05")
			if val, ok := recommended[timeKey].(float64); ok {
				return val, true
			}
		}
	}

	return 0, false
}
