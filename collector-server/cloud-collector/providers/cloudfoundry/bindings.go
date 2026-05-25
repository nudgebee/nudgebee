package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const ServiceNameServiceBindings = "service_credential_bindings"

type cfServiceBindingsService struct{}

func (s *cfServiceBindingsService) GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error) {
	bindings, err := getPaginated[cfServiceBinding](client, "/v3/service_credential_bindings?per_page=200")
	if err != nil {
		return nil, fmt.Errorf("failed to list service credential bindings: %w", err)
	}

	var resources []providers.Resource
	for _, b := range bindings {
		status := providers.ResourceStatusActive
		if b.LastOperation.State == "failed" {
			status = providers.ResourceStatusInactive
		}

		tags := make(map[string][]string)
		tags["binding_type"] = []string{b.Type}
		if b.LastOperation.State != "" {
			tags["last_operation_state"] = []string{b.LastOperation.State}
		}
		for k, v := range b.Metadata.Labels {
			tags[k] = []string{v}
		}

		name := b.Name
		if name == "" {
			name = fmt.Sprintf("binding-%s", shortGUID(b.GUID))
		}

		appGUID := b.Relations.App.Data.GUID
		siGUID := b.Relations.ServiceInstance.Data.GUID

		meta := map[string]any{
			"binding_type":          b.Type,
			"app_guid":              appGUID,
			"service_instance_guid": siGUID,
		}
		if b.LastOperation.Type != "" {
			meta["last_operation_type"] = b.LastOperation.Type
			meta["last_operation_state"] = b.LastOperation.State
		}

		arn := fmt.Sprintf("cf://binding/%s/%s", siGUID, b.GUID)

		resources = append(resources, providers.Resource{
			Id:          b.GUID,
			Name:        name,
			Type:        "service_credential_binding",
			Arn:         arn,
			ServiceName: ServiceNameServiceBindings,
			Status:      status,
			Tags:        tags,
			Meta:        meta,
			CreatedAt:   b.CreatedAt,
		})
	}

	return resources, nil
}
