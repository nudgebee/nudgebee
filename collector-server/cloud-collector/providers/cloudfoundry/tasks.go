package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameTasks = "tasks"

type cfTasksService struct{}

func (s *cfTasksService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	tasks, err := getPaginated[cfTask](client, "/v3/tasks?per_page=200&order_by=-created_at")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	var resources []providers.Resource
	for _, t := range tasks {
		status := providers.ResourceStatusActive
		if t.State == "FAILED" || t.State == "SUCCEEDED" {
			status = providers.ResourceStatusInactive
		}

		tags := make(map[string][]string)
		tags["state"] = []string{t.State}
		for k, v := range t.Metadata.Labels {
			tags[k] = []string{v}
		}

		appGUID := t.Relations.App.Data.GUID
		name := t.Name
		if name == "" {
			name = fmt.Sprintf("task-%d", t.SequenceID)
		}

		meta := map[string]any{
			"state":        t.State,
			"app_guid":     appGUID,
			"sequence_id":  t.SequenceID,
			"memory_in_mb": t.MemoryInMB,
			"disk_in_mb":   t.DiskInMB,
		}
		if t.Result.FailureReason != "" {
			meta["failure_reason"] = t.Result.FailureReason
		}

		arn := fmt.Sprintf("cf://task/%s/%s", appGUID, t.GUID)

		resources = append(resources, providers.Resource{
			Id:          t.GUID,
			Name:        name,
			Type:        "task",
			Arn:         arn,
			ServiceName: ServiceNameTasks,
			Status:      status,
			Tags:        tags,
			Meta:        meta,
			CreatedAt:   t.CreatedAt,
		})
	}

	return resources, nil
}
