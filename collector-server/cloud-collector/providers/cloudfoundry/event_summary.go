package cloudfoundry

import (
	"nudgebee/collector/cloud/providers"
	"strings"
)

// computeEventSummary analyzes audit events and produces EventSummary entries
// that track resource creation, deletion, and update counts per service.
// This enables the ETL layer to trigger targeted resource re-discovery
// when resources are created or deleted.
func computeEventSummary(events []providers.Event) []providers.EventSummary {
	// key: serviceName -> summary
	summaryMap := make(map[string]*providers.EventSummary)

	for _, ev := range events {
		serviceName := ev.ResourceServiceName
		if serviceName == "" {
			continue
		}

		summary, ok := summaryMap[serviceName]
		if !ok {
			summary = &providers.EventSummary{
				ServiceName: serviceName,
				Region:      "cloudfoundry", // CF is regionless; use provider name as region
			}
			summaryMap[serviceName] = summary
		}

		lower := strings.ToLower(ev.EventName)
		switch {
		case strings.Contains(lower, "create"):
			summary.ResourcesCreated++
		case strings.Contains(lower, "delete"):
			summary.ResourceDeleted++
		case strings.Contains(lower, "update") || strings.Contains(lower, "restage"):
			summary.ResourceUpdated++
		}
	}

	summaries := make([]providers.EventSummary, 0, len(summaryMap))
	for _, s := range summaryMap {
		// Only include summaries with actual changes
		if s.ResourcesCreated > 0 || s.ResourceDeleted > 0 || s.ResourceUpdated > 0 {
			summaries = append(summaries, *s)
		}
	}

	return summaries
}
