package cloudfoundry

import (
	"nudgebee/collector/cloud/providers"
)

// cfService defines the interface for Cloud Foundry service implementations.
// Each service (apps, spaces, organizations, routes) implements this interface.
type cfService interface {
	GetResources(ctx providers.CloudProviderContext, client *cfClient, orgName string) ([]providers.Resource, error)
}

// shortGUID returns the first 8 characters of a GUID for display names,
// or the full string if it is shorter than 8 characters.
func shortGUID(guid string) string {
	if len(guid) < 8 {
		return guid
	}
	return guid[:8]
}
