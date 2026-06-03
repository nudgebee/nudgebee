package cloudfoundry

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"
)

// getAuditEvents fetches audit events from CF API v3 and maps them to providers.Event.
func getAuditEvents(ctx providers.CloudProviderContext, client *cfClient, query providers.ListEventRequest) ([]providers.Event, error) {
	return getAuditEventsV3(ctx, client, query)
}

// getAuditEventsV3 fetches events from the CF API v3 /audit_events endpoint.
func getAuditEventsV3(ctx providers.CloudProviderContext, client *cfClient, query providers.ListEventRequest) ([]providers.Event, error) {
	logger := ctx.GetLogger()

	// Build query path with filters
	path := "/v3/audit_events?per_page=200&order_by=-created_at"
	if query.StartDate != nil {
		path += "&created_ats[gte]=" + query.StartDate.Format(time.RFC3339)
	}
	if query.EndDate != nil {
		path += "&created_ats[lte]=" + query.EndDate.Format(time.RFC3339)
	}

	logger.Info("CloudFoundry: fetching v3 audit events", "path", path)

	cfEvents, err := getPaginated[cfAuditEvent](client, path)
	if err != nil {
		if isAuthError(err) {
			logger.Error("CloudFoundry: v3 audit events authorization error. Ensure the token/client has 'cloud_controller.admin_read_only' or 'audit_events.read' scope", "error", err.Error())
			return nil, fmt.Errorf("failed to list audit events: unauthorized - please verify token scopes: %w", err)
		}
		return nil, fmt.Errorf("v3 audit_events failed: %w", err)
	}

	if len(cfEvents) == 0 {
		logger.Warn("CloudFoundry: no v3 audit events returned",
			"startDate", query.StartDate,
			"endDate", query.EndDate)
	} else {
		logger.Info("CloudFoundry: fetched v3 audit events", "count", len(cfEvents))
	}

	return mapV3Events(cfEvents, query), nil
}

// mapV3Events converts v3 cfAuditEvent items to providers.Event, applying filters.
func mapV3Events(cfEvents []cfAuditEvent, query providers.ListEventRequest) []providers.Event {
	var events []providers.Event
	for _, ev := range cfEvents {
		severity := eventSeverity(ev.Type)

		if !matchesResourceFilter(ev.Target.GUID, query.ResourceIds) {
			continue
		}
		if isExcluded(ev.Type, query.ExcludeEvents) {
			continue
		}

		resourceServiceName := eventTargetTypeToServiceName(ev.Target.Type)

		events = append(events, providers.Event{
			Title:               fmt.Sprintf("%s: %s", ev.Type, ev.Target.Name),
			Description:         fmt.Sprintf("Actor %s (%s) performed %s on %s %s", ev.Actor.Name, ev.Actor.Type, ev.Type, ev.Target.Type, ev.Target.Name),
			EventName:           ev.Type,
			Date:                ev.CreatedAt,
			Username:            ev.Actor.Name,
			EventSource:         "cloudfoundry",
			EventId:             fmt.Sprintf("cf-%s-%s", ev.Type, ev.Target.GUID),
			FindingId:           ev.GUID, // Source-native per-event UUID for traceability
			EventStatus:         providers.EventStatusClosed,
			EventSeverity:       severity,
			ResourceType:        ev.Target.Type,
			ResourceId:          ev.Target.GUID,
			ResourceServiceName: resourceServiceName,
			Raw:                 ev.Data,
			Labels: map[string]string{
				"actor_type": ev.Actor.Type,
				"actor_guid": ev.Actor.GUID,
				"space_guid": ev.Space.GUID,
				"org_guid":   ev.Organization.GUID,
			},
		})
	}
	return events
}

// matchesResourceFilter checks if a resource ID matches the filter. Empty filter matches all.
func matchesResourceFilter(resourceID string, filterIDs []string) bool {
	if len(filterIDs) == 0 {
		return true
	}
	for _, rid := range filterIDs {
		if rid == resourceID {
			return true
		}
	}
	return false
}

// isExcluded checks if an event type is in the exclusion list.
func isExcluded(eventType string, excludeList []string) bool {
	for _, excl := range excludeList {
		if excl == eventType {
			return true
		}
	}
	return false
}

// isAuthError checks if an error indicates an authorization failure.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "401") || strings.Contains(errStr, "Unauthorized") ||
		strings.Contains(errStr, "403") || strings.Contains(errStr, "Forbidden")
}

// eventTargetTypeToServiceName maps CF event target types to the corresponding service name.
func eventTargetTypeToServiceName(targetType string) string {
	switch strings.ToLower(targetType) {
	case "app":
		return ServiceNameApps
	case "space":
		return ServiceNameSpaces
	case "organization":
		return ServiceNameOrganizations
	case "route":
		return ServiceNameRoutes
	case "service_instance":
		return ServiceNameServiceInstances
	case "build":
		return ServiceNameBuilds
	case "deployment":
		return ServiceNameDeployments
	case "task":
		return ServiceNameTasks
	case "service_binding", "service_credential_binding":
		return ServiceNameServiceBindings
	default:
		return ServiceNameApps
	}
}

// eventSeverity maps CF audit event types to severity levels.
func eventSeverity(eventType string) providers.EventSeverity {
	lower := strings.ToLower(eventType)
	switch {
	case strings.Contains(lower, "crash"):
		return providers.EventSeverityHigh
	case strings.Contains(lower, "delete"):
		return providers.EventSeverityMedium
	case strings.Contains(lower, "stop"):
		return providers.EventSeverityMedium
	case strings.Contains(lower, "ssh"):
		return providers.EventSeverityMedium
	case strings.Contains(lower, "map_route"), strings.Contains(lower, "unmap_route"):
		return providers.EventSeverityMedium
	case strings.Contains(lower, "environment_variables"):
		return providers.EventSeverityMedium
	case strings.Contains(lower, "scale"):
		return providers.EventSeverityLow
	case strings.Contains(lower, "update"):
		return providers.EventSeverityLow
	case strings.Contains(lower, "create"):
		return providers.EventSeverityInfo
	case strings.Contains(lower, "start"):
		return providers.EventSeverityInfo
	default:
		return providers.EventSeverityInfo
	}
}
