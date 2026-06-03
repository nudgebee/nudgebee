package triage

import (
	"encoding/json"
	"fmt"
	"math"

	"nudgebee/services/internal/database/models"
)

// Configuration constants for correlation detection
const (
	MaxDependencyDistance     = 4    // Check up to 4 hops in service graph
	CorrelationScoreThreshold = 0.50 // Only store correlations with score >= 0.50
	MaxTimeWindowMinutes      = 10   // Only correlate events within 10 minutes
	MaxRecentEventsToCheck    = 50   // Limit number of events to check for performance
)

// CorrelationResult represents the result of correlation analysis
type CorrelationResult struct {
	IsCorrelated       bool
	CorrelationType    string
	CorrelationScore   float64
	CorrelationReason  string
	TimeOffsetMinutes  int
	DependencyDistance int
}

// calculateCorrelationScore computes correlation score between two events.
//
// Parameters:
//   - event1: candidate related event pulled from the recent-events window
//   - event2: the triaged event that correlation is running for
//   - serviceMap: dependency graph parsed from the triaged event's evidence,
//     or nil when no ServiceMap/knowledge_graph is attached
//   - traceServices: set of service names that participated in the triaged
//     event's failing trace(s), derived from trace evidence span data. Used as
//     a fallback signal when serviceMap either didn't find a path or isn't
//     available — a candidate whose subject appears in this set is by
//     definition a participant in the same failing request, which is a
//     stronger signal than a historical ServiceMap edge. Pass nil when the
//     triaged event has no trace evidence.
func calculateCorrelationScore(event1, event2 *models.Event, serviceMap *DependencyGraph, traceServices map[string]bool) CorrelationResult {
	result := CorrelationResult{
		IsCorrelated: false,
	}

	// Prevent self-correlation
	if event1.Id == event2.Id {
		return result
	}

	// Check for nil StartsAt
	if event1.StartsAt == nil || event2.StartsAt == nil {
		return result
	}

	score := 0.0
	reasons := []string{}

	// 1. Time proximity scoring (0.05-0.30)
	timeDiff := event2.StartsAt.Sub(*event1.StartsAt)
	timeDiffMinutes := int(math.Abs(timeDiff.Minutes()))
	result.TimeOffsetMinutes = int(timeDiff.Minutes()) // Negative if event2 fired before event1

	if timeDiffMinutes <= 2 {
		score += 0.30
		reasons = append(reasons, "fired within 2 minutes")
	} else if timeDiffMinutes <= 5 {
		score += 0.15
		reasons = append(reasons, "fired within 5 minutes")
	} else if timeDiffMinutes <= 10 {
		score += 0.05
		reasons = append(reasons, "fired within 10 minutes")
	} else {
		// Too far apart in time
		return result
	}

	// 2. Service dependency scoring (0.05-0.40)
	service1Key := getServiceKeyFromEvent(event1)
	service2Key := getServiceKeyFromEvent(event2)

	var dependencyDistance int
	var correlationType string

	if serviceMap != nil && service1Key != "" && service2Key != "" {
		dependencyDistance = serviceMap.getDependencyDistance(service1Key, service2Key)
		result.DependencyDistance = dependencyDistance

		if dependencyDistance > 0 && dependencyDistance <= MaxDependencyDistance {
			// Per-source weight prevents a single user-declared (manual)
			// edge with no observed-source corroboration from carrying a
			// distance=1 hop across CorrelationScoreThreshold (0.50). When
			// the direct edge has any observed-source contributor (k8s,
			// traces, ebpf, APM, aws, ...) or when KG evidence didn't
			// populate edge contributors, weight is 1.0 (no behavior
			// change).
			hopWeight := serviceMap.dependencyHopWeight(service1Key, service2Key, dependencyDistance)
			var hopScore float64
			switch dependencyDistance {
			case 1:
				hopScore = 0.40 * hopWeight
				score += hopScore
				if hopWeight < 1.0 {
					reasons = append(reasons, "direct service dependency (manual-only edge)")
				} else {
					reasons = append(reasons, "direct service dependency")
				}
			case 2:
				hopScore = 0.25 * hopWeight
				score += hopScore
				reasons = append(reasons, fmt.Sprintf("%d hops in service graph", dependencyDistance))
			case 3:
				hopScore = 0.15 * hopWeight
				score += hopScore
				reasons = append(reasons, fmt.Sprintf("%d hops in service graph", dependencyDistance))
			case 4:
				hopScore = 0.05 * hopWeight
				score += hopScore
				reasons = append(reasons, fmt.Sprintf("%d hops in service graph", dependencyDistance))
			}

			// Determine correlation type based on dependency direction
			if serviceMap.isUpstream(service1Key, service2Key) {
				correlationType = "upstream_dependency"

				// 3. Causality bonus (0.15) - downstream fires AFTER upstream
				if event2.StartsAt.After(*event1.StartsAt) {
					score += 0.15
					reasons = append(reasons, "downstream fired after upstream (causal)")
				}
			} else if serviceMap.isDownstream(service1Key, service2Key) {
				correlationType = "downstream_impact"
			} else {
				correlationType = "temporal_proximity"
			}
		}
	} else {
		// No service map available, use temporal proximity
		correlationType = "temporal_proximity"
		dependencyDistance = -1
	}

	// 3b. Trace-anchored fallback (0.40 + 0.15 causality).
	// When ServiceMap couldn't produce a direct dependency — either because the
	// graph is missing (e.g. configuration_change events carry no
	// knowledge_graph evidence) or because the two services aren't connected
	// in the aggregate graph — fall back to per-request causality: if event1's
	// subject (the candidate) appears in traceServices, the candidate was a
	// participant in the triaged event's failing request, which is a stronger
	// signal than any historical graph edge.
	//
	// traceServices is extracted from the triaged event (event2) by the caller
	// and represents the services named in its trace spans. Checking event1's
	// subject against that set answers "did the candidate participate in the
	// triaged event's failing request?". Checking event2's subject would be
	// tautological — the triaged event's own service is in its own trace.
	//
	// Direction convention mirrors the existing ServiceMap block: event1
	// (candidate) firing before event2 (triaged) awards the causality bonus.
	if dependencyDistance <= 0 && len(traceServices) > 0 {
		participantName := traceParticipantName(event1)
		if participantName != "" && traceServices[participantName] {
			score += 0.40
			reasons = append(reasons, "trace co-participant")
			dependencyDistance = 1
			result.DependencyDistance = 1
			if event2.StartsAt.After(*event1.StartsAt) {
				correlationType = "upstream_dependency"
				score += 0.15
				reasons = append(reasons, "trace co-participant fired before (causal)")
			} else {
				correlationType = "downstream_impact"
			}
		}
	}

	// 4. Same cloud resource (0.25) - strongest non-service-map signal
	// Two different alerts on the same resource (e.g., RDS high-cpu + slow-queries) are very likely related
	sameResource := false
	if event1.CloudResourceId != nil && event2.CloudResourceId != nil &&
		*event1.CloudResourceId != "" && *event2.CloudResourceId != "" &&
		*event1.CloudResourceId == *event2.CloudResourceId {
		score += 0.25
		reasons = append(reasons, "same cloud resource")
		sameResource = true
		if correlationType == "" || correlationType == "temporal_proximity" {
			correlationType = "same_resource"
		}
	}

	// 5. Same namespace bonus (0.10)
	if event1.SubjectNamespace != nil && event2.SubjectNamespace != nil &&
		*event1.SubjectNamespace == *event2.SubjectNamespace {
		score += 0.10
		reasons = append(reasons, "same namespace")
	}

	// 6. Same service exact match (0.15)
	if service1Key != "" && service2Key != "" && service1Key == service2Key {
		score += 0.15
		reasons = append(reasons, "same service")
		if !sameResource {
			correlationType = "same_service"
		}
	}

	// Check if score meets threshold
	if score >= CorrelationScoreThreshold {
		result.IsCorrelated = true
		result.CorrelationScore = math.Min(score, 1.0) // Cap at 1.0
		result.CorrelationType = correlationType
		result.CorrelationReason = buildReasonString(reasons)

		// Determine if this is likely root cause
		if correlationType == "upstream_dependency" &&
			dependencyDistance == 1 &&
			event1.StartsAt.Before(*event2.StartsAt) &&
			score > 0.80 {
			result.CorrelationType = "likely_root_cause"
		}
	} else {
		// Below threshold - still set score for debugging
		result.CorrelationScore = score
		result.CorrelationReason = buildReasonString(reasons)
	}

	return result
}

// traceParticipantName returns the service identifier used to match an event
// against a trace-participant service set. Prefers SubjectName (e.g. "flagd",
// "checkout") since that's the form used by OTel's `service.name` attribute in
// demo/ebpf/k8s workloads. Falls back to SubjectOwner when SubjectName is blank
// (some collectors only populate the owner).
func traceParticipantName(e *models.Event) string {
	if e == nil {
		return ""
	}
	if e.SubjectName != nil && *e.SubjectName != "" {
		return *e.SubjectName
	}
	if e.SubjectOwner != nil && *e.SubjectOwner != "" {
		return *e.SubjectOwner
	}
	return ""
}

// traceEvidenceQueryModes identifies evidence entries produced by the trace
// auto-action. The `metadata.query.mode` field is unique to that producer —
// the three values below cover error-centric expansion, attribute-error
// recovery, and the recent-sample fallback path.
var traceEvidenceQueryModes = map[string]bool{
	"error_plus_expansion":      true,
	"error_plus_expansion_attr": true,
	"fallback_recent_sample":    true,
}

// extractTraceServiceSet returns the set of service names that participated in
// the triaged event's failing trace(s). These services are causally connected
// by the specific failing request that produced this event — a candidate event
// whose subject appears in this set is, by construction, part of the same
// request chain.
//
// The trace auto-action stores span data in an evidence entry shaped as:
//
//	{
//	  "type": "json",
//	  "format": "json",
//	  "metadata": { "query": { "mode": "error_plus_expansion", ... } },
//	  "data": "{\"data\": [ {\"service_name\": ..., ...}, ... ]}"
//	}
//
// We scan for that specific evidence, parse the stringified span array, and
// collect distinct service_name (OTel) / workload_name (fallback) values.
//
// Returns nil when no trace evidence is present or parsing fails — callers
// treat a nil map as "no trace signal available" and fall through to other
// correlation paths.
func extractTraceServiceSet(event *models.Event) map[string]bool {
	if event == nil || event.Evidences == nil || !event.Evidences.IsArray() {
		return nil
	}
	out := make(map[string]bool)
	for _, ev := range event.Evidences.Array() {
		evMap, ok := ev.(map[string]interface{})
		if !ok {
			continue
		}
		meta, _ := evMap["metadata"].(map[string]interface{})
		query, _ := meta["query"].(map[string]interface{})
		mode, _ := query["mode"].(string)
		if !traceEvidenceQueryModes[mode] {
			continue
		}
		dataStr, ok := evMap["data"].(string)
		if !ok || dataStr == "" {
			continue
		}
		var payload struct {
			Data []map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal([]byte(dataStr), &payload); err != nil {
			continue
		}
		for _, span := range payload.Data {
			if svc, _ := span["service_name"].(string); svc != "" {
				out[svc] = true
				continue
			}
			if wn, _ := span["workload_name"].(string); wn != "" {
				out[wn] = true
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildReasonString creates a human-readable reason from multiple factors
func buildReasonString(reasons []string) string {
	if len(reasons) == 0 {
		return "correlated"
	}
	if len(reasons) == 1 {
		return reasons[0]
	}
	if len(reasons) == 2 {
		return reasons[0] + " and " + reasons[1]
	}

	// Join first N-1 with commas, last with "and"
	result := ""
	for i := 0; i < len(reasons)-1; i++ {
		if i > 0 {
			result += ", "
		}
		result += reasons[i]
	}
	result += " and " + reasons[len(reasons)-1]
	return result
}
