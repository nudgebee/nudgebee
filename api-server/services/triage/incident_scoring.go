package triage

// incident_scoring.go holds the single correlation term both scoring paths use,
// so the legacy scorer and the LLM scorer can never disagree about what
// "related" means (#34715).
//
// Two eras of "related" exist in event_correlations. The legacy pairwise engine
// (±10-minute window, no rarity gate) wrote same_resource / same_service /
// upstream_dependency / downstream_impact / likely_root_cause rows and still
// feeds scoring; the incident view stopped rendering any of it in #34658
// because it was too loose. Incident grouping (#34655) writes same_incident
// child -> leader links, and those ARE what the UI shows.
//
// Once grouping is on, the term is:
//
//	group child                  -> IncidentChildPenalty (-10)
//	pairwise likely_root_cause   -> CorrelationBonusRootCause (+15)
//	anything else                -> 0
//
// The weak co-occurrence types are dropped. On dev they were 3,660 of the 3,465
// adjusted events' worth of firings and moved 351 alerts a whole priority band
// on evidence no more specific than "something else happened on this pod".
// likely_root_cause survives on measured value: sampling shows real
// config-change -> failure and dependency-cascade links that grouping's
// same-subject / calls-edge rules do not reach (only 8 of 581 boosted alerts
// were group leaders). Retiring it needs the assembly's cause tier available at
// triage time, which is read-time only today — tracked as the real close of
// #34715.
//
// A child that is ALSO a pairwise likely_root_cause is scored as a child: being
// a symptom inside a group the operator can see beats a relationship they
// cannot.
// resolveCorrelationAdjustment is the entry point both scorers call. The grouping kill
// switch governs BOTH halves: with grouping off no same_incident links are being written,
// so each scorer stays on the exact mapping it has always used and
// INCIDENT_GROUPING_ENABLED reverts the feature completely rather than leaving scoring
// half-migrated.
//
// fallback is the caller's own pre-grouping mapping, and the two are NOT the same
// function: the legacy scorer never scored same_resource (it fell through to 0) while the
// LLM scorer dampens it by -5. Converging them is a scoring change in its own right, so it
// happens only on the grouping-on path where both drop the type entirely.
func resolveCorrelationAdjustment(
	isIncidentChild bool,
	correlationType string,
	correlationScore float64,
	fallback func(string, float64) (int, string),
) (int, string) {
	if !incidentGroupingEnabled() {
		return fallback(correlationType, correlationScore)
	}
	return incidentCorrelationAdjustment(isIncidentChild, correlationType, correlationScore)
}

// llmCorrelationAdjustment is the LLM scorer's pre-grouping mapping: the same confidence
// gate correlationDampening applied before its DB read, then the LLM-path type table.
func llmCorrelationAdjustment(correlationType string, correlationScore float64) (int, string) {
	if correlationScore < MinCorrelationScoreForAdjustment {
		return 0, correlationType
	}
	return correlationTypeAdjustment(correlationType), correlationType
}

func incidentCorrelationAdjustment(isIncidentChild bool, correlationType string, correlationScore float64) (int, string) {
	if isIncidentChild {
		return IncidentChildPenalty, SameIncidentCorrelationType
	}
	if correlationScore >= MinCorrelationScoreForAdjustment && correlationType == "likely_root_cause" {
		return CorrelationBonusRootCause, correlationType
	}
	return 0, correlationType
}
