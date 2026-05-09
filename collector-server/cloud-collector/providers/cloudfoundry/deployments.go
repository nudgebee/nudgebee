package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameDeployments = "deployments"

type cfDeploymentsService struct{}

func (s *cfDeploymentsService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	deployments, err := getPaginated[cfDeployment](client, "/v3/deployments?per_page=200&order_by=-created_at")
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}

	var resources []providers.Resource
	for _, d := range deployments {
		status := providers.ResourceStatusActive
		if d.Status.Reason == "CANCELED" || d.State == "CANCELED" {
			status = providers.ResourceStatusInactive
		}

		tags := make(map[string][]string)
		tags["state"] = []string{d.State}
		tags["strategy"] = []string{d.Strategy}
		if d.Status.Value != "" {
			tags["status_value"] = []string{d.Status.Value}
		}
		for k, v := range d.Metadata.Labels {
			tags[k] = []string{v}
		}

		appGUID := d.Relations.App.Data.GUID
		meta := map[string]any{
			"state":         d.State,
			"strategy":      d.Strategy,
			"status_value":  d.Status.Value,
			"status_reason": d.Status.Reason,
			"app_guid":      appGUID,
			"droplet_guid":  d.Droplet.GUID,
		}
		if d.PreviousDroplet.GUID != "" {
			meta["previous_droplet_guid"] = d.PreviousDroplet.GUID
		}

		arn := fmt.Sprintf("cf://deployment/%s/%s", appGUID, d.GUID)

		resources = append(resources, providers.Resource{
			Id:          d.GUID,
			Name:        fmt.Sprintf("deployment-%s", shortGUID(d.GUID)),
			Type:        "deployment",
			Arn:         arn,
			ServiceName: ServiceNameDeployments,
			Status:      status,
			Tags:        tags,
			Meta:        meta,
			CreatedAt:   d.CreatedAt,
		})
	}

	return resources, nil
}
