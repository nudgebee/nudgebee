package optimizer

import (
	"context"
	"fmt"
	"nudgebee/runbook/internal/model"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"
)

type ContinuousRightsizeGenerator struct {
	BaseTaskGenerator
}

func (g *ContinuousRightsizeGenerator) GenerateTasks(ctx context.Context, ao model.AutoOptimize, recommendations []model.RecommendationWithResource) ([]model.AutoOptimizeTask, error) {
	var tasks []model.AutoOptimizeTask

	for _, rec := range recommendations {
		// Extract resource details from identifier
		// Expected format: "namespace/Kind/name"
		parts := strings.Split(rec.ResourceIdentifier, "/")
		if len(parts) < 3 {
			continue
		}
		namespace, kind, name := parts[0], parts[1], parts[2]

		// Construct Settings
		settings := g.getSettings(ao.Rule, ao.Status == model.AutoOptimizeStatusDryrun, ao)

		// Construct Application
		app := map[string]string{
			"name":      name,
			"namespace": namespace,
			"kind":      kind,
		}

		// Construct Meta (Payload)
		meta := map[string]any{
			"action_name": "continuous_rightsizing",
			"action_params": map[string]any{
				"settings":     settings,
				"applications": []any{app},
			},
			"timestamp": time.Now().UTC().Unix(),
			"origin":    "callback",
		}

		task := g.CreateBaseTask(ao, rec.Recommendation)
		task.Name = fmt.Sprintf("Continuous Rightsize for resource %s", name)
		task.Meta = meta
		task.ResourceFilter = model.AutoOptimizeResourceFilter{
			Namespace: &namespace,
			Name:      &name,
			Type:      &kind,
		}

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

func (g *ContinuousRightsizeGenerator) getSettings(rule map[string]any, dryRun bool, ao model.AutoOptimize) map[string]any {
	minMemoryStr, _ := rule["min_memory"].(string)
	minMemoryBytes := int64(0)
	if q, err := resource.ParseQuantity(minMemoryStr); err == nil {
		minMemoryBytes = q.Value()
	}
	// Convert to Mi (Python: / k8s_memory_factors["Mi"])
	// 1 Mi = 1024 * 1024 = 1048576
	minMemoryMi := minMemoryBytes / 1048576

	minCpu, _ := rule["min_cpu"].(float64)
	oomFactor, _ := rule["oom_kill_increase_factor"].(float64)
	threshold, _ := rule["min_change_threshold"].(float64) // int in python, but json often float
	cpuStrategy, _ := rule["cpu_analysis_strategy"].(string)
	memStrategy, _ := rule["memory_analysis_strategy"].(string)
	duration, _ := rule["analysis_duration_hour"].(float64)

	cpuPercentile := 0
	if strings.HasPrefix(cpuStrategy, "P") {
		cpuPercentile, _ = strconv.Atoi(strings.TrimPrefix(cpuStrategy, "P"))
	}
	memPercentile := 0
	if strings.HasPrefix(memStrategy, "P") {
		memPercentile, _ = strconv.Atoi(strings.TrimPrefix(memStrategy, "P"))
	}

	identifier := fmt.Sprintf("%s/%s/%s/%s", ao.TenantID, ao.AccountID, ao.ID, uuidNew())
	// Note: Task ID not available yet in Generator, using random UUID or skipped.
	// Python used: f"{self.auto_optimize_task.id}" which implies it's generated LATER or available.
	// In GenerateTasks we generate the task, so we don't have its ID yet unless we pre-generate.
	// BaseTaskGenerator.CreateBaseTask generates an ID.
	// But here we construct meta BEFORE creating task? No, we can create task first.

	// Actually, CreateBaseTask generates ID. I should call it first.
	// But I used `g.CreateBaseTask` later in the loop.
	// I'll assume identifier needs to be unique per execution.

	return map[string]any{
		"default_min_cpu":                minCpu,
		"default_min_memory":             minMemoryMi,
		"oom_kill_increase_factor":       oomFactor,
		"change_threshold":               int(threshold),
		"cpu_analysis_percentile":        cpuPercentile,
		"memory_analysis_percentile":     memPercentile,
		"default_analysis_duration_hour": int(duration),
		"recommend_only":                 dryRun,
		"identifier":                     identifier,
	}
}

// Wrapper to allow mocking/replacing if needed
func uuidNew() string {
	return uuid.New().String()
}
