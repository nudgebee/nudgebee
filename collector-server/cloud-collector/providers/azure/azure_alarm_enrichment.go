package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// enrichResourcesWithAlertDetails fetches all metric alerts for the subscription
// and attaches matching alert details to each resource's Meta["AlertDetails"]
func enrichResourcesWithAlertDetails(ctx providers.CloudProviderContext, account providers.Account, resources []providers.Resource) []providers.Resource {
	if len(resources) == 0 {
		return resources
	}

	logger := ctx.GetLogger()

	// Fetch all metric alerts
	alertsByResourceID, err := fetchAllMetricAlerts(ctx, account)
	if err != nil {
		logger.Warn("azure: failed to fetch metric alerts for enrichment, alarm recommendations will assume no existing alerts",
			"error", err)
		return resources
	}

	if len(alertsByResourceID) == 0 {
		return resources
	}

	// Enrich each resource with matching alert details
	enrichedResources := make([]providers.Resource, len(resources))
	for i, resource := range resources {
		enrichedResources[i] = resource

		// Look up alerts scoped to this resource
		resourceID := strings.ToLower(resource.Id)
		if alerts, ok := alertsByResourceID[resourceID]; ok {
			// Clone Meta to avoid mutating the original
			newMeta := make(map[string]any, len(resource.Meta)+1)
			for k, v := range resource.Meta {
				newMeta[k] = v
			}
			newMeta["AlertDetails"] = alerts
			enrichedResources[i].Meta = newMeta
		}
	}

	logger.Info("azure: enriched resources with alert details",
		"totalResources", len(resources),
		"alertMappings", len(alertsByResourceID),
	)

	return enrichedResources
}

// fetchAllMetricAlerts fetches all metric alerts across all subscriptions
// and returns a map of lowercase resource ID -> list of alert info
func fetchAllMetricAlerts(ctx providers.CloudProviderContext, account providers.Account) (map[string][]AzureAlertInfo, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	alertsByResourceID := make(map[string][]AzureAlertInfo)

	subscriptionIDs := strings.Split(session.SubscriptionID, ",")
	for _, subID := range subscriptionIDs {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}

		client, err := armmonitor.NewMetricAlertsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Warn("azure: failed to create metric alerts client for subscription",
				"subscriptionId", subID, "error", err)
			continue
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Warn("azure: failed to list metric alerts for subscription",
					"subscriptionId", subID, "error", err)
				break
			}

			for _, alert := range page.Value {
				if alert.Properties == nil {
					continue
				}

				// Extract alert info
				alertName := ""
				if alert.Name != nil {
					alertName = *alert.Name
				}

				enabled := false
				if alert.Properties.Enabled != nil {
					enabled = *alert.Properties.Enabled
				}

				severity := int(0)
				if alert.Properties.Severity != nil {
					severity = int(*alert.Properties.Severity)
				}

				// Extract metric criteria from the alert
				metricInfos := extractMetricCriteriaFromAlert(alert)

				// Map each alert to each of its scoped resources
				for _, scope := range alert.Properties.Scopes {
					if scope == nil {
						continue
					}
					resourceID := strings.ToLower(*scope)

					for _, mi := range metricInfos {
						alertInfo := AzureAlertInfo{
							AlertName:       alertName,
							MetricNamespace: mi.namespace,
							MetricName:      mi.metricName,
							Severity:        severity,
							Enabled:         enabled,
							ResourceID:      *scope,
						}
						alertsByResourceID[resourceID] = append(alertsByResourceID[resourceID], alertInfo)
					}

					// If no metric criteria extracted, still record the alert with empty metric info
					if len(metricInfos) == 0 {
						alertInfo := AzureAlertInfo{
							AlertName:  alertName,
							Severity:   severity,
							Enabled:    enabled,
							ResourceID: *scope,
						}
						alertsByResourceID[resourceID] = append(alertsByResourceID[resourceID], alertInfo)
					}
				}
			}
		}
	}

	return alertsByResourceID, nil
}

// metricInfo holds extracted metric namespace and name from alert criteria
type metricInfo struct {
	namespace  string
	metricName string
}

// extractMetricCriteriaFromAlert extracts metric namespace and name from alert criteria
func extractMetricCriteriaFromAlert(alert *armmonitor.MetricAlertResource) []metricInfo {
	if alert.Properties == nil || alert.Properties.Criteria == nil {
		return nil
	}

	var results []metricInfo

	// The Criteria field is an interface - we need to check the type
	criteriaMap := structToMap(alert.Properties.Criteria)
	if criteriaMap == nil {
		return nil
	}

	// Try to extract from "allOf" array (standard metric criteria)
	allOf, ok := criteriaMap["allOf"].([]interface{})
	if !ok {
		return nil
	}

	for _, criterion := range allOf {
		criterionMap, ok := criterion.(map[string]interface{})
		if !ok {
			continue
		}

		namespace, _ := criterionMap["metricNamespace"].(string)
		metricName, _ := criterionMap["metricName"].(string)

		if metricName != "" {
			results = append(results, metricInfo{
				namespace:  namespace,
				metricName: metricName,
			})
		}
	}

	return results
}
