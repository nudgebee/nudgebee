package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameOrganizations = "organizations"

type cfOrganizationsService struct{}

func (s *cfOrganizationsService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	orgs, err := getPaginated[cfOrg](client, "/v3/organizations?per_page=200")
	if err != nil {
		return nil, fmt.Errorf("failed to list organizations: %w", err)
	}

	var resources []providers.Resource
	for _, org := range orgs {
		status := providers.ResourceStatusActive
		if org.Suspended {
			status = providers.ResourceStatusInactive
		}

		tags := make(map[string][]string)
		for k, v := range org.Metadata.Labels {
			tags[k] = []string{v}
		}

		resources = append(resources, providers.Resource{
			Id:          org.GUID,
			Name:        org.Name,
			Type:        "organization",
			Arn:         fmt.Sprintf("cf://%s", org.Name),
			ServiceName: ServiceNameOrganizations,
			Status:      status,
			Region:      org.Name,
			Tags:        tags,
			Meta: map[string]any{
				"suspended": org.Suspended,
			},
			CreatedAt: org.CreatedAt,
		})
	}

	return resources, nil
}
