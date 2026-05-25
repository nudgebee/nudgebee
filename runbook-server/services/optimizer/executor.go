package optimizer

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/runbook/internal/model"
	"strings"
	"time"

	"nudgebee/runbook/services/notification"
	"nudgebee/runbook/services/security"

	"github.com/google/uuid"
	"github.com/robfig/cron"
	"go.temporal.io/sdk/client"
)

// GenerateTasks generates tasks based on recommendations and rules.
func (s *optimizerService) GenerateTasks(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeTask, error) {
	ao, err := s.dao.GetAutoOptimize(ctx, autoOptimizeID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch auto optimize %s: %w", autoOptimizeID, err)
	}

	// 1. Pre-flight Checks
	if err := s.checkPreFlight(ctx, ao); err != nil {
		return nil, err
	}

	ao.ExecutionStatus = string(model.AutopilotExecutionStatusInProgress)
	if err := s.dao.SaveAutoOptimize(ctx, *ao); err != nil {
		return nil, fmt.Errorf("failed to set execution status to InProgress: %w", err)
	}

	recommendations, err := s.dao.GetFullRecommendationsForOptimizerCategory(ctx, ao.AccountID, ao.Category)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recommendations: %w", err)
	}

	// Filter recommendations based on ResourceFilters
	var filteredRecs []model.RecommendationWithResource
	if len(ao.ResourceFilters) > 0 {
		for i := range recommendations {
			rec := &recommendations[i]
			parts := strings.Split(rec.ResourceIdentifier, "/")
			if len(parts) < 3 {
				continue
			}
			namespace, kind, name := parts[0], parts[1], parts[2]

			match := false
			for _, f := range ao.ResourceFilters {
				if f.Matches(namespace, kind, name) {
					match = true
					break
				}

				// Check match via AccountObjectID (the parent workload)
				if rec.AccountObjectID != nil && *rec.AccountObjectID != "" {
					aoParts := strings.Split(*rec.AccountObjectID, "/")
					if len(aoParts) >= 3 {
						if f.Matches(aoParts[0], aoParts[1], aoParts[2]) {
							// Promote to Workload identifier so Generator targets the controller
							rec.ResourceIdentifier = *rec.AccountObjectID
							match = true
							break
						}
					}
				}

				// Special case: If recommendation is for a Pod, but filter is for a controller (Deployment, etc.),
				if strings.EqualFold(kind, "Pod") && f.Type != nil && isControllerType(*f.Type) {
					// Check if namespace matches
					if f.Namespace != nil && *f.Namespace != "" && *f.Namespace != namespace {
						continue
					}
					// If filter has NO name (namespace wide), we should definitely match.
					if f.Name == nil || *f.Name == "" {
						match = true
						break
					}

					if rec.ResourceMetadata != nil {
						if val, ok := rec.ResourceMetadata["controller"].(string); ok && val == *f.Name {
							match = true
							break
						}
					}

					// Fallback: Prefix matching (Pod name starts with Controller name)
					if strings.HasPrefix(name, *f.Name) {
						// Update the recommendation identifier to point to the controller
						// so the Generator treats it as a controller task.
						rec.ResourceIdentifier = fmt.Sprintf("%s/%s/%s", namespace, *f.Type, *f.Name)
						match = true
						break
					}
				}
			}
			if match {
				filteredRecs = append(filteredRecs, *rec)
			}
		}
	} else {
		filteredRecs = recommendations
	}

	if len(filteredRecs) > 0 {
		recIDs := make([]uuid.UUID, len(filteredRecs))
		for i, r := range filteredRecs {
			recIDs[i] = r.ID
		}

		activeTasks, err := s.dao.GetActiveTasksForRecommendations(ctx, recIDs)
		if err != nil {
			slog.Error("Failed to fetch active tasks for recommendations", "error", err)
			return nil, fmt.Errorf("failed to check for active tasks: %w", err)
		}

		if len(activeTasks) > 0 {
			var uniqueRecs []model.RecommendationWithResource
			skippedCount := 0
			for _, r := range filteredRecs {
				if _, exists := activeTasks[r.ID]; !exists {
					uniqueRecs = append(uniqueRecs, r)
				} else {
					skippedCount++
				}
			}
			if skippedCount > 0 {
				slog.Info("Skipped recommendations as active tasks already exist", "count", skippedCount)
			}
			filteredRecs = uniqueRecs
		}
	}

	generator, err := s.factory.GetGenerator(ao.Category)
	if err != nil {
		return nil, fmt.Errorf("no generator for category %s: %w", ao.Category, err)
	}

	tasks, err := generator.GenerateTasks(ctx, *ao, filteredRecs)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tasks: %w", err)
	}

	// Mark tasks as skipped if there is an active resolution (PR/Ticket)
	if len(tasks) > 0 {
		var recIDs []uuid.UUID
		for _, t := range tasks {
			if t.RecommendationID != nil {
				recIDs = append(recIDs, *t.RecommendationID)
			}
		}

		if len(recIDs) > 0 {
			activeResolutions, err := s.dao.GetActiveResolutionsForRecommendations(ctx, recIDs)
			if err != nil {
				slog.Error("Failed to fetch active resolutions for recommendations", "error", err)
				return nil, fmt.Errorf("failed to fetch active resolutions: %w", err)
			}

			if len(activeResolutions) > 0 {
				for i := range tasks {
					task := &tasks[i]
					if task.RecommendationID != nil {
						if resolutions, exists := activeResolutions[*task.RecommendationID]; exists && len(resolutions) > 0 {
							task.Status = string(model.AutopilotTaskStatusSkipped)
							var details []string
							for _, res := range resolutions {
								details = append(details, fmt.Sprintf("%s: %s", res.Type, res.TypeReferenceID))
							}
							reason := fmt.Sprintf("Recommendation already has active resolutions: %s", strings.Join(details, ", "))
							task.Reason = &reason
						}
					}
				}
			}
		}
	}

	tasks, err = s.prioritizeTasks(ctx, ao, tasks)
	if err != nil {
		return nil, fmt.Errorf("failed to prioritize tasks: %w", err)
	}

	if len(tasks) > 0 {
		slog.Info("Generated tasks", "count", len(tasks), "auto_optimize_id", ao.ID)
		if err := s.dao.SaveAutoOptimizeTasks(ctx, tasks); err != nil {
			return nil, fmt.Errorf("failed to save tasks: %w", err)
		}

		s.sendNotifications(ctx, ao, tasks)
	} else {
		slog.Info("No tasks generated", "auto_optimize_id", ao.ID)
		ao.ExecutionStatus = string(model.AutopilotExecutionStatusIdle)
	}

	if err := s.updateExecutionState(ctx, ao); err != nil {
		return nil, fmt.Errorf("failed to update execution state: %w", err)
	}

	return tasks, nil
}

func isControllerType(kind string) bool {
	k := strings.ToLower(kind)
	return k == "deployment" || k == "statefulset" || k == "replicaset" || k == "daemonset" || k == "rollout"
}

func (s *optimizerService) sendNotifications(ctx context.Context, ao *model.AutoOptimize, tasks []model.AutoOptimizeTask) {
	if len(tasks) == 0 {
		return
	}

	sc := security.NewRequestContextForTenantAdmin(ao.TenantID.String())

	for platform, config := range ao.Notification {
		cfgMap, ok := config.(map[string]interface{})
		if !ok {
			continue
		}

		enabled, _ := cfgMap["enabled"].(bool)
		if !enabled {
			continue
		}

		channelID, _ := cfgMap["channel_id"].(string)
		teamID, _ := cfgMap["team_id"].(string)

		if channelID == "" {
			continue
		}

		var name string
		if ao.Name != nil {
			name = *ao.Name
		} else {
			name = "Auto Optimize"
		}
		body := fmt.Sprintf("Scheduled %d tasks for %s", len(tasks), name)

		req := notification.SendImNotificationRequest{
			Platform:  platform,
			Channel:   channelID,
			TeamId:    teamID,
			Body:      body,
			AccountID: ao.AccountID.String(),
		}

		_, err := notification.SendNotification(sc, req)
		if err != nil {
			slog.Error("Failed to send notification", "platform", platform, "error", err)
		}
	}
}

func (s *optimizerService) checkPreFlight(ctx context.Context, ao *model.AutoOptimize) error {
	if ao.Status != model.AutoOptimizeStatusActive && ao.Status != model.AutoOptimizeStatusDryrun {
		return fmt.Errorf("auto optimize %s is not active (status: %s)", ao.ID, ao.Status)
	}

	now := time.Now().UTC()
	if ao.EndAt != nil && ao.EndAt.Before(now) {
		slog.Info("Auto optimize expired, disabling", "id", ao.ID, "end_at", *ao.EndAt)
		ao.Status = model.AutoOptimizeStatusDisabled
		if err := s.dao.SaveAutoOptimize(ctx, *ao); err != nil {
			return fmt.Errorf("failed to disable expired auto optimize %s: %w", ao.ID, err)
		}

		err := s.temporalClient.ScheduleClient().GetHandle(ctx, s.scheduleID(ao.ID)).Pause(ctx, client.SchedulePauseOptions{
			Note: "Expired and disabled by system",
		})
		if err != nil {
			slog.Error("failed to pause schedule for expired auto optimize", "id", ao.ID, "error", err)
		}

		return fmt.Errorf("auto optimize %s has expired (EndAt: %s) and is now disabled", ao.ID, *ao.EndAt)
	}

	agent, err := s.dao.GetAgent(ctx, ao.AccountID)
	if err != nil {
		return fmt.Errorf("failed to fetch agent for account %s: %w", ao.AccountID, err)
	}

	if agent.Status == "NotConnected" {
		return fmt.Errorf("agent for account %s is not connected", ao.AccountID)
	}

	return nil
}

func (s *optimizerService) updateExecutionState(ctx context.Context, ao *model.AutoOptimize) error {
	now := time.Now().UTC()

	if ao.NextScheduleTime != nil {
		ao.LastScheduleTime = ao.NextScheduleTime
	} else {
		ao.LastScheduleTime = &now
	}

	sched, err := cron.ParseStandard(ao.ScheduleTime)
	if err == nil {
		// Calculate next from now to ensure we don't get a past time if execution was delayed
		next := sched.Next(now)
		ao.NextScheduleTime = &next
	} else {
		slog.Error("Error parsing cron", "schedule_time", ao.ScheduleTime, "auto_optimize_id", ao.ID, "error", err)
	}

	ao.LastExecutedTime = &now

	return s.dao.SaveAutoOptimize(ctx, *ao)
}

func (s *optimizerService) prioritizeTasks(ctx context.Context, ao *model.AutoOptimize, tasks []model.AutoOptimizeTask) ([]model.AutoOptimizeTask, error) {
	var workloadFilters []model.AutoOptimizeResourceFilter

	for _, filter := range ao.ResourceFilters {
		if filter.OnlyNamespace() {
			ns := ""
			if filter.Namespace != nil {
				ns = *filter.Namespace
			}
			if ns == "" {
				continue
			}

			filters, err := s.dao.GetWorkloadFiltersForNamespace(ctx, ao.AccountID, ao.TenantID, ns, ao.Category)
			if err != nil {
				return nil, err
			}
			workloadFilters = append(workloadFilters, filters...)
		}
	}

	if len(workloadFilters) == 0 {
		return tasks, nil
	}

	for i := range tasks {
		task := &tasks[i]
		tNs := ""
		if task.ResourceFilter.Namespace != nil {
			tNs = *task.ResourceFilter.Namespace
		}
		tKind := ""
		if task.ResourceFilter.Type != nil {
			tKind = *task.ResourceFilter.Type
		}
		tName := ""
		if task.ResourceFilter.Name != nil {
			tName = *task.ResourceFilter.Name
		}

		for _, wf := range workloadFilters {
			if wf.Matches(tNs, tKind, tName) {
				task.Status = string(model.AutopilotTaskStatusSkipped)
				reason := "Will be handled by workload level auto optimize"
				task.Reason = &reason
				break
			}
		}
	}

	return tasks, nil
}
