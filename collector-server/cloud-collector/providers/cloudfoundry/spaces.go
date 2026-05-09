package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameSpaces = "spaces"

type cfSpacesService struct{}

func (s *cfSpacesService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	spaces, err := getPaginated[cfSpace](client, "/v3/spaces?per_page=200")
	if err != nil {
		return nil, fmt.Errorf("failed to list spaces: %w", err)
	}

	// Build org lookup for enrichment
	orgsMap := make(map[string]cfOrg)
	orgs, err := getPaginated[cfOrg](client, "/v3/organizations?per_page=200")
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch orgs for space enrichment", "error", err)
	} else {
		for _, o := range orgs {
			orgsMap[o.GUID] = o
		}
	}

	var resources []providers.Resource
	for _, space := range spaces {
		orgGUID := space.Relations.Organization.Data.GUID
		spaceOrgName := orgGUID
		if org, ok := orgsMap[orgGUID]; ok {
			spaceOrgName = org.Name
		}

		tags := make(map[string][]string)
		tags["org"] = []string{spaceOrgName}
		for k, v := range space.Metadata.Labels {
			tags[k] = []string{v}
		}

		resources = append(resources, providers.Resource{
			Id:          space.GUID,
			Name:        space.Name,
			Type:        "space",
			Arn:         fmt.Sprintf("cf://%s/%s", spaceOrgName, space.Name),
			ServiceName: ServiceNameSpaces,
			Status:      providers.ResourceStatusActive,
			Region:      spaceOrgName,
			Tags:        tags,
			Meta: map[string]any{
				"org_guid": orgGUID,
				"org_name": spaceOrgName,
			},
			CreatedAt: space.CreatedAt,
		})
	}

	return resources, nil
}
