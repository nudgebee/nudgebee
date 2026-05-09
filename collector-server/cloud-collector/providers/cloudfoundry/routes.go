package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameRoutes = "routes"

type cfRoutesService struct{}

func (s *cfRoutesService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	routes, err := getPaginated[cfRoute](client, "/v3/routes?per_page=200")
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}

	var resources []providers.Resource
	for _, route := range routes {
		name := route.URL
		if name == "" {
			name = route.Host
		}

		// Collect destination app GUIDs
		var destApps []string
		for _, dest := range route.Destinations {
			destApps = append(destApps, dest.App.GUID)
		}

		tags := make(map[string][]string)
		for k, v := range route.Metadata.Labels {
			tags[k] = []string{v}
		}
		if route.Protocol != "" {
			tags["protocol"] = []string{route.Protocol}
		}

		resources = append(resources, providers.Resource{
			Id:          route.GUID,
			Name:        name,
			Type:        "route",
			Arn:         fmt.Sprintf("cf://route/%s", route.URL),
			ServiceName: ServiceNameRoutes,
			Status:      providers.ResourceStatusActive,
			Tags:        tags,
			Meta: map[string]any{
				"host":             route.Host,
				"path":             route.Path,
				"url":              route.URL,
				"protocol":         route.Protocol,
				"space_guid":       route.Relations.Space.Data.GUID,
				"domain_guid":      route.Relations.Domain.Data.GUID,
				"destination_apps": destApps,
			},
			CreatedAt: route.CreatedAt,
		})
	}

	return resources, nil
}
