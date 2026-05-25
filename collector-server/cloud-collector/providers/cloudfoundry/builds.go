package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameBuilds = "builds"

type cfBuildsService struct{}

func (s *cfBuildsService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	builds, err := getPaginated[cfBuild](client, "/v3/builds?per_page=200&order_by=-created_at")
	if err != nil {
		return nil, fmt.Errorf("failed to list builds: %w", err)
	}

	var resources []providers.Resource
	for _, b := range builds {
		status := providers.ResourceStatusActive
		if b.State == "FAILED" {
			status = providers.ResourceStatusInactive
		}

		tags := make(map[string][]string)
		tags["state"] = []string{b.State}
		if b.Lifecycle.Type != "" {
			tags["lifecycle_type"] = []string{b.Lifecycle.Type}
		}
		for k, v := range b.Metadata.Labels {
			tags[k] = []string{v}
		}

		meta := map[string]any{
			"state":        b.State,
			"lifecycle":    b.Lifecycle.Type,
			"package_guid": b.Package.GUID,
			"app_guid":     b.Relations.App.Data.GUID,
			"created_by":   b.CreatedBy.Name,
		}
		if b.Error != nil {
			meta["error"] = *b.Error
		}
		if b.Droplet != nil {
			meta["droplet_guid"] = b.Droplet.GUID
		}

		appGUID := b.Relations.App.Data.GUID
		arn := fmt.Sprintf("cf://build/%s/%s", appGUID, b.GUID)

		resources = append(resources, providers.Resource{
			Id:          b.GUID,
			Name:        fmt.Sprintf("build-%s", shortGUID(b.GUID)),
			Type:        "build",
			Arn:         arn,
			ServiceName: ServiceNameBuilds,
			Status:      status,
			Tags:        tags,
			Meta:        meta,
			CreatedAt:   b.CreatedAt,
		})
	}

	return resources, nil
}
