package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameServiceInstances = "service_instances"

type cfServiceInstancesService struct{}

func (s *cfServiceInstancesService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	instances, err := getPaginated[cfServiceInstance](client, "/v3/service_instances?per_page=200")
	if err != nil {
		return nil, fmt.Errorf("failed to list service instances: %w", err)
	}

	// Build space/org lookup maps for enrichment
	spaces, orgs := buildLookupMaps(ctx, client)

	var resources []providers.Resource
	for _, si := range instances {
		spaceGUID := si.Relations.Space.Data.GUID
		spaceName := spaceGUID
		orgNameVal := ""
		orgGUID := ""

		if space, ok := spaces[spaceGUID]; ok {
			spaceName = space.Name
			orgGUID = space.Relations.Organization.Data.GUID
			if org, ok := orgs[orgGUID]; ok {
				orgNameVal = org.Name
			}
		}

		status := providers.ResourceStatusActive
		if si.LastOperation.State == "failed" {
			status = providers.ResourceStatusInactive
		}

		tags := make(map[string][]string)
		tags["space"] = []string{spaceName}
		if orgNameVal != "" {
			tags["org"] = []string{orgNameVal}
		}
		if si.Type != "" {
			tags["service_type"] = []string{si.Type}
		}
		if len(si.Tags) > 0 {
			tags["service_tags"] = si.Tags
		}
		for k, v := range si.Metadata.Labels {
			tags[k] = []string{v}
		}

		arn := fmt.Sprintf("cf://%s/%s/service_instance/%s", orgNameVal, spaceName, si.Name)

		meta := map[string]any{
			"space_guid":        spaceGUID,
			"org_guid":          orgGUID,
			"type":              si.Type,
			"service_plan_guid": si.Relations.ServicePlan.Data.GUID,
			"upgrade_available": si.UpgradeAvailable,
		}
		if si.DashboardURL != "" {
			meta["dashboard_url"] = si.DashboardURL
		}
		if si.LastOperation.Type != "" {
			meta["last_operation_type"] = si.LastOperation.Type
			meta["last_operation_state"] = si.LastOperation.State
		}
		if si.MaintenanceInfo.Version != "" {
			meta["maintenance_version"] = si.MaintenanceInfo.Version
		}

		resources = append(resources, providers.Resource{
			Id:          si.GUID,
			Name:        si.Name,
			Type:        "service_instance",
			Arn:         arn,
			ServiceName: ServiceNameServiceInstances,
			Status:      status,
			Region:      orgNameVal,
			Tags:        tags,
			Meta:        meta,
			CreatedAt:   si.CreatedAt,
		})
	}

	return resources, nil
}
