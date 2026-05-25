package common

import "strings"

// BuildExternalResourceId returns a synthetic ARN-like key used to look up a
// resource in cloud_resourses.external_resource_id. The format is:
//
//	arn:<provider>:<short-service>:<region>:<account>:<resource-type>:<resource-id>[/<sub-id>]
//
// Azure resources keep their native case-insensitive ARM ID. AWS service names
// are stripped of the "amazon"/"aws" prefix; GCP equivalents are stripped of
// "gcp"/"google".
func BuildExternalResourceId(provider, accountId, region, serviceName, resourceType, resourceId, resourceSubId string) string {
	serviceName = strings.ToLower(serviceName)

	if strings.HasPrefix(serviceName, "amazon") && len(serviceName) > 6 {
		serviceName = serviceName[6:]
	} else if strings.HasPrefix(serviceName, "aws") && len(serviceName) > 3 {
		serviceName = serviceName[3:]
	} else if strings.EqualFold(provider, "azure") {
		// Azure ARM resource IDs are case-insensitive but ARM and Event Grid
		// return inconsistent casing for the same resource. Normalize to
		// lowercase so realtime upserts and bulk-sync upserts collide on the
		// same row instead of creating duplicates.
		return strings.ToLower(resourceId)
	} else if strings.HasPrefix(serviceName, "gcp") && len(serviceName) > 3 {
		serviceName = serviceName[3:]
	} else if strings.HasPrefix(serviceName, "google") && len(serviceName) > 6 {
		serviceName = serviceName[6:]
	}
	if region == "" {
		region = "global"
	}
	region = strings.ToLower(region)
	resourceId = strings.ToLower(resourceId)
	resourceId = strings.ReplaceAll(resourceId, " ", "-")

	arn := "arn:" + strings.ToLower(provider) + ":" + serviceName + ":" + region + ":" + accountId

	resourceType = strings.ToLower(resourceType)
	resourceType = strings.ReplaceAll(resourceType, " ", "-")
	arn = arn + ":" + resourceType

	arn = arn + ":" + resourceId
	if resourceSubId != "" {
		resourceSubId = strings.ToLower(resourceSubId)
		resourceSubId = strings.ReplaceAll(resourceSubId, " ", "-")
		arn = arn + "/" + resourceSubId
	}
	return arn
}
