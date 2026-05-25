package azure

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
	"github.com/google/uuid"
)

// AlertsFilter provides filtering options for Azure fired alerts
type AlertsFilter struct {
	ResourceIds     []string   `json:"resource_ids"`
	ServiceName     string     `json:"service_name"`
	Region          string     `json:"region"`
	TimeRange       string     `json:"time_range"` // "1h", "1d", "7d", "30d"
	StartDate       *time.Time `json:"start_date"` // Filter alerts after this date
	OnlyFiredAlerts bool       `json:"only_fired"` // If true, skip resolved alerts
}

// getAzureAlerts fetches actual fired Azure alert instances and converts them to events.
// Uses the AlertsManagement API which returns real fired/resolved alerts,
// not alert rule definitions.
func getAzureAlerts(ctx providers.CloudProviderContext, account providers.Account, filter AlertsFilter) (providers.ListEventResponse, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Error("azure:getAzureAlerts failed to create azure credential", "error", err, "accountNumber", account.AccountNumber)
		return providers.ListEventResponse{}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Memory safety: limit max alerts per collection run to prevent OOM.
	// For 1-hour windows (10-min cron job), we expect 50-500 alerts max.
	// Setting conservative limit at 2000 for safety.
	const maxAlertsPerRun = 2000
	alertsProcessed := 0
	alertsSkippedDuplicate := 0
	alertsSkippedResolved := 0

	// Pre-allocate events slice with reasonable capacity to reduce allocations.
	// For 1-hour window, typical runs process 50-200 alerts.
	events := make([]providers.Event, 0, 100)
	subscriptionIDs := strings.Split(session.SubscriptionID, ",")

	for _, subID := range subscriptionIDs {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}

		// NewAlertsClient takes a scope (not a bare subscription ID).
		// The URL template is "/{scope}/providers/Microsoft.AlertsManagement/alerts",
		// so the scope must include the "subscriptions/" prefix.
		scope := "subscriptions/" + subID
		client, err := armalertsmanagement.NewAlertsClient(scope, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Error("azure:getAzureAlerts failed to create alerts client", "error", err, "subscriptionID", subID)
			return providers.ListEventResponse{}, fmt.Errorf("failed to create alerts client: %w", err)
		}

		opts := &armalertsmanagement.AlertsClientGetAllOptions{}

		// Determine time range based on StartDate or explicit TimeRange filter
		timeRange := armalertsmanagement.TimeRangeOneH // Default to 1 hour (changed from 7 days)

		if filter.TimeRange != "" {
			// Explicit TimeRange takes precedence
			timeRange = armalertsmanagement.TimeRange(filter.TimeRange)
		} else if filter.StartDate != nil {
			// Map StartDate to appropriate Azure TimeRange enum
			// Azure SDK only supports fixed time ranges (1h, 1d, 7d, 30d)
			timeSinceStart := time.Since(*filter.StartDate)

			if timeSinceStart <= 1*time.Hour {
				timeRange = armalertsmanagement.TimeRangeOneH
			} else if timeSinceStart <= 24*time.Hour {
				timeRange = armalertsmanagement.TimeRangeOneD
			} else if timeSinceStart <= 7*24*time.Hour {
				timeRange = armalertsmanagement.TimeRangeSevenD
			} else {
				timeRange = armalertsmanagement.TimeRangeThirtyD
			}

			ctx.GetLogger().Debug("azure:getAzureAlerts mapped StartDate to TimeRange",
				"startDate", filter.StartDate,
				"timeSinceStart", timeSinceStart,
				"timeRange", timeRange)
		}

		opts.TimeRange = &timeRange

		pager := client.NewGetAllPager(opts)
		for pager.More() {
			// Check memory limit before fetching next page
			if alertsProcessed >= maxAlertsPerRun {
				ctx.GetLogger().Warn(
					"azure:getAzureAlerts reached max alerts limit, stopping pagination to prevent OOM",
					"alertsProcessed", alertsProcessed,
					"maxAlertsPerRun", maxAlertsPerRun,
					"subscriptionID", subID,
				)
				break
			}

			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Error("azure:getAzureAlerts failed to get next page", "error", err, "subscriptionID", subID)
				return providers.ListEventResponse{}, fmt.Errorf("failed to get next page: %w", err)
			}

			for _, alert := range page.Value {
				// Check limit per alert as well (for very large pages)
				if alertsProcessed >= maxAlertsPerRun {
					break
				}
				if alert == nil || alert.Properties == nil || alert.Properties.Essentials == nil || alert.ID == nil || alert.Name == nil {
					continue
				}

				essentials := alert.Properties.Essentials

				// Detect resolved state early to skip processing if only fired alerts requested
				isResolved := essentials.MonitorCondition != nil &&
					*essentials.MonitorCondition == armalertsmanagement.MonitorConditionResolved

				// Skip resolved alerts if OnlyFiredAlerts filter is set (major memory optimization)
				if filter.OnlyFiredAlerts && isResolved {
					alertsSkippedResolved++
					continue
				}

				// CRITICAL: Filter alerts by LastModifiedDateTime to avoid fetching duplicates on every 10-min run.
				// For a 10-min job with 1-hour window, we'd fetch the same alerts 6 times without this.
				//
				// Why LastModifiedDateTime instead of StartDateTime:
				// - StartDateTime: When alert first fired (never changes)
				// - LastModifiedDateTime: When alert state last changed (firing/resolved/updated)
				//
				// Example: Alert fires at 10:00, still active at 10:10, 10:20, 10:30
				// - Using StartDateTime: Only fetched at 10:10, then missed ❌
				// - Using LastModifiedDateTime: Fetched when state changes ✅
				//
				// This ensures we capture:
				// 1. New alerts (StartDateTime = LastModifiedDateTime)
				// 2. Status changes (LastModifiedDateTime updated)
				// 3. Resolved alerts (LastModifiedDateTime = resolution time)
				if filter.StartDate != nil && essentials.LastModifiedDateTime != nil {
					// Skip alerts that haven't been modified since our last collection
					if essentials.LastModifiedDateTime.Before(*filter.StartDate) {
						alertsSkippedDuplicate++
						continue
					}
				}

				// Apply client-side filters
				if !matchesAlertFilter(essentials, filter) {
					continue
				}

				// GetAll typically returns full Essentials (TargetResource, AlertRule, etc.).
				// Only call GetByID when fingerprinting fields are missing, to avoid N+1 API calls.
				// Previously this called GetByID for every fired alert, causing ~16min processing
				// for accounts with thousands of alerts.
				needsGetByID := !isResolved && alert.ID != nil &&
					(essentials.TargetResource == nil || essentials.AlertRule == nil)
				if needsGetByID {
					idParts := strings.Split(*alert.ID, "/")
					alertGUID := idParts[len(idParts)-1]

					// Resource Health alerts use display names (not UUIDs) as IDs,
					// which cause 400 Bad Request from GetByID. Skip these.
					if _, parseErr := uuid.Parse(alertGUID); parseErr != nil {
						ctx.GetLogger().Debug(
							"azure:getAzureAlerts skipping GetByID for non-UUID alert ID",
							"alertGUID", alertGUID,
						)
					} else {
						fullAlert, err := client.GetByID(ctx.GetContext(), alertGUID, nil)
						if err != nil {
							ctx.GetLogger().Warn(
								"azure:getAzureAlerts GetByID failed, using partial data",
								"error", err,
								"alertGUID", alertGUID,
								"alertFullID", *alert.ID,
							)
						} else if fullAlert.Properties != nil &&
							fullAlert.Properties.Essentials != nil {

							essentials = fullAlert.Properties.Essentials
							if alert.Properties != nil {
								alert.Properties.Context = fullAlert.Properties.Context
							}
						}
					}
				}

				// Resource info from essentials (needed before building eventID)
				resourceID := safeDeref(essentials.TargetResource)

				// Build stable fingerprint from alert rule + target resource.
				// alert.ID is a per-firing instance GUID — useless for dedup.
				// AlertRule identifies the rule definition, combined with TargetResource
				// it uniquely identifies "this alert on this resource".
				alertRule := safeDeref(essentials.AlertRule)
				var eventID string
				if alertRule != "" && resourceID != "" {
					eventID = fmt.Sprintf("%s:%s", alertRule, resourceID)
				} else {
					// Fall back to instance ID when resource scope is unavailable.
					// Multi-resource alert rules can fire for different targets,
					// so using alertRule alone would over-deduplicate.
					eventID = *alert.ID
				}

				// Stable date - when the alert actually fired
				eventDate := time.Now().UTC()
				if essentials.StartDateTime != nil {
					eventDate = *essentials.StartDateTime
				}

				// Actual monitor condition (Fired/Resolved)
				eventStatus := providers.EventStatusFiring
				if essentials.MonitorCondition != nil && *essentials.MonitorCondition == armalertsmanagement.MonitorConditionResolved {
					eventStatus = providers.EventStatusResolved
				}

				// Severity: Sev0->High, Sev1->High, Sev2->Medium, Sev3->Low, Sev4->Low
				severity := mapAlertSeverity(essentials.Severity)
				serviceName := extractFullResourceType(resourceID)
				if serviceName == "" {
					serviceName = strings.ToLower(safeDeref(essentials.TargetResourceType))
				}
				resourceType := extractShortResourceType(serviceName)
				region := safeDeref(essentials.TargetResourceGroup)

				alertName := *alert.Name

				// Build labels
				labels := map[string]string{
					"azure_alert_id":              *alert.ID,
					"azure_alert_name":            alertName,
					"azure_alert_rule":            safeDeref(essentials.AlertRule),
					"azure_region":                region,
					"azure_subscription_id":       subID,
					"azure_service_name":          serviceName,
					"azure_alert_target_resource": resourceID,
				}

				if essentials.SignalType != nil {
					labels["signal_type"] = string(*essentials.SignalType)
				}
				if essentials.MonitorCondition != nil {
					labels["azure_monitor_condition"] = string(*essentials.MonitorCondition)
				}
				if essentials.Severity != nil {
					labels["azure_alert_severity"] = string(*essentials.Severity)
				}
				if essentials.MonitorService != nil {
					labels["azure_monitor_service"] = string(*essentials.MonitorService)
				}
				if essentials.Description != nil && *essentials.Description != "" {
					labels["azure_alert_description"] = *essentials.Description
				}

				// NOTE: Alert context is already included in the Raw map via buildAlertRawMap.
				// Storing it again in labels causes memory duplication (30-40% overhead).
				// Context can be large (10-50KB per alert), so we only store it once in Raw.

				// Build title and description
				title := alertName
				description := fmt.Sprintf("Azure alert '%s' fired", alertName)
				if essentials.Description != nil && *essentials.Description != "" {
					description = *essentials.Description
				}

				event := providers.Event{
					Title:               title,
					EventName:           alertName,
					Description:         description,
					Date:                eventDate,
					EventSource:         "Azure_Monitor_Alert",
					EventId:             eventID,
					FindingId:           *alert.ID, // Source-native per-firing GUID for traceability
					EventStatus:         eventStatus,
					EventSeverity:       severity,
					ResourceType:        resourceType,
					ResourceId:          resourceID,
					ResourceRegion:      region,
					ResourceServiceName: serviceName,
					Raw:                 buildAlertRawMap(alert, essentials),
					Labels:              labels,
				}

				events = append(events, event)
				alertsProcessed++
			}
		}
	}

	if alertsProcessed >= maxAlertsPerRun {
		ctx.GetLogger().Warn(
			"azure:getAzureAlerts completed with max alerts limit reached",
			"totalAlertsProcessed", alertsProcessed,
			"maxAlertsPerRun", maxAlertsPerRun,
		)
	}

	// Log deduplication stats for visibility
	if alertsSkippedDuplicate > 0 || alertsSkippedResolved > 0 {
		ctx.GetLogger().Info(
			"azure:getAzureAlerts deduplication summary",
			"alertsProcessed", alertsProcessed,
			"alertsSkippedDuplicate", alertsSkippedDuplicate,
			"alertsSkippedResolved", alertsSkippedResolved,
			"totalSeen", alertsProcessed+alertsSkippedDuplicate+alertsSkippedResolved,
		)
	}

	return providers.ListEventResponse{
		Items: events,
	}, nil
}

// matchesAlertFilter checks if a fired alert matches the provided filter criteria
func matchesAlertFilter(essentials *armalertsmanagement.Essentials, filter AlertsFilter) bool {
	// Filter by region (resource group)
	if filter.Region != "" && essentials.TargetResourceGroup != nil {
		if !strings.EqualFold(*essentials.TargetResourceGroup, filter.Region) {
			return false
		}
	}

	// Filter by specific resource IDs
	if len(filter.ResourceIds) > 0 && essentials.TargetResource != nil {
		matchesResource := false
		for _, filterResourceID := range filter.ResourceIds {
			if strings.EqualFold(*essentials.TargetResource, filterResourceID) {
				matchesResource = true
				break
			}
		}
		if !matchesResource {
			return false
		}
	}

	// Filter by service name (target resource type)
	if filter.ServiceName != "" && essentials.TargetResourceType != nil {
		if !strings.EqualFold(*essentials.TargetResourceType, filter.ServiceName) {
			return false
		}
	}

	return true
}

// mapAlertSeverity maps Azure alert severity to providers.EventSeverity
func mapAlertSeverity(sev *armalertsmanagement.Severity) providers.EventSeverity {
	if sev == nil {
		return providers.EventSeverityMedium
	}
	switch *sev {
	case armalertsmanagement.SeveritySev0, armalertsmanagement.SeveritySev1:
		return providers.EventSeverityHigh
	case armalertsmanagement.SeveritySev2:
		return providers.EventSeverityMedium
	case armalertsmanagement.SeveritySev3, armalertsmanagement.SeveritySev4:
		return providers.EventSeverityLow
	default:
		return providers.EventSeverityMedium
	}
}

// extractFullResourceType extracts the full resource type from an Azure resource ID.
// Example: "/subscriptions/.../providers/Microsoft.Compute/virtualMachines/myvm" -> "microsoft.compute/virtualmachines"
func extractFullResourceType(resourceID string) string {
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")
	for i, part := range parts {
		if strings.EqualFold(part, "providers") && i+2 < len(parts) {
			return strings.ToLower(parts[i+1] + "/" + parts[i+2])
		}
	}
	return ""
}

// extractShortResourceType extracts the short resource type name from a full Azure resource type.
// Example: "microsoft.compute/virtualmachines" -> "virtualmachines"
func extractShortResourceType(resourceType string) string {
	if resourceType == "" {
		return "resource"
	}
	parts := strings.Split(resourceType, "/")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return resourceType
}

// safeDeref safely dereferences a string pointer, returning empty string if nil
func safeDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// buildAlertRawMap extracts only the operationally relevant fields from an Azure Alert
// for the Raw/evidences column. The full SDK struct can be 50-120KB when serialized
// via structToMap; this produces ~2-3KB, avoiding the major OOM allocation hotspot.
func buildAlertRawMap(alert *armalertsmanagement.Alert, essentials *armalertsmanagement.Essentials) map[string]any {
	raw := map[string]any{
		"id":   safeDeref(alert.ID),
		"name": safeDeref(alert.Name),
		"type": safeDeref(alert.Type),
	}

	if essentials != nil {
		ess := map[string]any{}
		if essentials.TargetResource != nil {
			ess["targetResource"] = *essentials.TargetResource
		}
		if essentials.TargetResourceName != nil {
			ess["targetResourceName"] = *essentials.TargetResourceName
		}
		if essentials.TargetResourceType != nil {
			ess["targetResourceType"] = *essentials.TargetResourceType
		}
		if essentials.TargetResourceGroup != nil {
			ess["targetResourceGroup"] = *essentials.TargetResourceGroup
		}
		if essentials.AlertRule != nil {
			ess["alertRule"] = *essentials.AlertRule
		}
		if essentials.Severity != nil {
			ess["severity"] = string(*essentials.Severity)
		}
		if essentials.MonitorCondition != nil {
			ess["monitorCondition"] = string(*essentials.MonitorCondition)
		}
		if essentials.MonitorService != nil {
			ess["monitorService"] = string(*essentials.MonitorService)
		}
		if essentials.SignalType != nil {
			ess["signalType"] = string(*essentials.SignalType)
		}
		if essentials.Description != nil {
			ess["description"] = *essentials.Description
		}
		if essentials.StartDateTime != nil {
			ess["startDateTime"] = essentials.StartDateTime.Format(time.RFC3339)
		}
		if essentials.LastModifiedDateTime != nil {
			ess["lastModifiedDateTime"] = essentials.LastModifiedDateTime.Format(time.RFC3339)
		}
		raw["essentials"] = ess
	}

	// Include alert context if available (metric values, thresholds).
	// Context is only present for fired alerts (resolved alerts skip the copy).
	// Apply size limits to prevent OOM when context payloads are large.
	if alert.Properties != nil && alert.Properties.Context != nil {
		if ctxBytes, err := json.Marshal(alert.Properties.Context); err == nil {
			const maxContextSize = 50 * 1024 // 50KB limit per alert context

			if len(ctxBytes) > maxContextSize {
				// Store metadata instead of full context to avoid OOM
				raw["context"] = map[string]any{
					"_truncated": true,
					"_size":      len(ctxBytes),
					"_note":      "Context exceeds 50KB limit and was truncated to prevent memory issues",
				}
			} else {
				// Reuse ctxBytes directly instead of marshal→unmarshal round-trip
				var ctxMap map[string]any
				if json.Unmarshal(ctxBytes, &ctxMap) == nil {
					raw["context"] = ctxMap
				}
			}
		}
	}

	return raw
}
