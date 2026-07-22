package azure

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	"nudgebee/collector/cloud/providers"
)

// UpdateAzureMetricAlertThreshold patches the static threshold (and optional window size for
// duration) on every metric criterion of an existing Azure metric alert, then writes it back
// via CreateOrUpdate using the account's credentials.
//
// ruleRef is the alert_rule_key carried from the suggestion. It is either a full ARM resource
// id (/subscriptions/<sub>/resourceGroups/<rg>/providers/microsoft.insights/metricAlerts/<name>)
// or — as the threshold suggestions currently store — just the alert name, in which case the
// alert is located by listing metric alerts across the account's subscription(s). The existing
// rule is fetched first so scopes, actions, evaluation frequency and every other field are
// preserved; only the threshold (and optionally windowSize) is mutated.
func UpdateAzureMetricAlertThreshold(ctx providers.CloudProviderContext, account providers.Account, ruleRef string, newThreshold float64, newDurationMin int) error {
	if ruleRef == "" {
		return fmt.Errorf("alert reference (resource id or name) is required")
	}

	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Full ARM resource id → patch directly.
	if strings.Contains(strings.ToLower(ruleRef), "/subscriptions/") {
		subID, _ := extractSubscriptionID(ruleRef)
		if subID == "" {
			subID = strings.TrimSpace(strings.Split(session.SubscriptionID, ",")[0])
		}
		rg, err := extractResourceGroup(ruleRef)
		if err != nil {
			return fmt.Errorf("failed to extract resource group from resource id: %w", err)
		}
		name := azureResourceNameFromID(ruleRef)
		if name == "" {
			return fmt.Errorf("could not extract alert name from resource id %q", ruleRef)
		}
		client, err := armmonitor.NewMetricAlertsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return fmt.Errorf("failed to create metric alerts client: %w", err)
		}
		return patchAndUpdateAzureAlert(ctx, client, rg, name, newThreshold, newDurationMin)
	}

	// Otherwise ruleRef is the alert name → find it across the account's subscriptions.
	name := ruleRef
	for _, subID := range strings.Split(session.SubscriptionID, ",") {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}
		client, err := armmonitor.NewMetricAlertsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Warn("azure: failed to create metric alerts client", "subscriptionId", subID, "error", err)
			continue
		}
		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Warn("azure: failed to list metric alerts", "subscriptionId", subID, "error", err)
				break
			}
			for _, alert := range page.Value {
				if alert.Name == nil || !strings.EqualFold(*alert.Name, name) {
					continue
				}
				rg := ""
				if alert.ID != nil {
					rg, _ = extractResourceGroup(*alert.ID)
				}
				if rg == "" {
					return fmt.Errorf("could not resolve resource group for azure alert %q", name)
				}
				return patchAndUpdateAzureAlert(ctx, client, rg, name, newThreshold, newDurationMin)
			}
		}
	}
	return fmt.Errorf("azure metric alert %q not found in any subscription", name)
}

// patchAndUpdateAzureAlert fetches the metric alert, mutates its threshold/window in place,
// and writes it back. Get-then-CreateOrUpdate keeps every other field intact.
func patchAndUpdateAzureAlert(ctx providers.CloudProviderContext, client *armmonitor.MetricAlertsClient, rg, name string, newThreshold float64, newDurationMin int) error {
	resp, err := client.Get(ctx.GetContext(), rg, name, nil)
	if err != nil {
		return fmt.Errorf("failed to get metric alert %q: %w", name, err)
	}
	rule := resp.MetricAlertResource
	if rule.Properties == nil {
		return fmt.Errorf("metric alert %q has no properties", name)
	}

	patched := patchAzureCriteriaThreshold(rule.Properties.Criteria, newThreshold)
	if patched == 0 {
		return fmt.Errorf("metric alert %q has no static threshold criterion to update", name)
	}
	if newDurationMin > 0 {
		rule.Properties.WindowSize = to.Ptr(fmt.Sprintf("PT%dM", newDurationMin))
	}

	if _, err := client.CreateOrUpdate(ctx.GetContext(), rg, name, rule, nil); err != nil {
		return fmt.Errorf("failed to update metric alert %q: %w", name, err)
	}

	ctx.GetLogger().Info("updated Azure metric alert threshold", "alert", name, "threshold", newThreshold, "criteria", patched)
	return nil
}

// patchAzureCriteriaThreshold sets the static threshold on every MetricCriteria within the
// alert's criteria, handling both single-resource and multi-resource criteria shapes.
// Returns the number of criteria patched.
func patchAzureCriteriaThreshold(criteria armmonitor.MetricAlertCriteriaClassification, newThreshold float64) int {
	count := 0
	switch c := criteria.(type) {
	case *armmonitor.MetricAlertSingleResourceMultipleMetricCriteria:
		for _, m := range c.AllOf {
			if m != nil {
				m.Threshold = to.Ptr(newThreshold)
				count++
			}
		}
	case *armmonitor.MetricAlertMultipleResourceMultipleMetricCriteria:
		for _, mc := range c.AllOf {
			if m, ok := mc.(*armmonitor.MetricCriteria); ok {
				m.Threshold = to.Ptr(newThreshold)
				count++
			}
		}
	}
	return count
}

// azureResourceNameFromID returns the final path segment of an ARM resource ID.
func azureResourceNameFromID(resourceID string) string {
	trimmed := strings.TrimRight(resourceID, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 || idx == len(trimmed)-1 {
		return ""
	}
	return trimmed[idx+1:]
}
