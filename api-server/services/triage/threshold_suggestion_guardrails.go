package triage

import (
	"fmt"
	"strings"
)

// checkThresholdEligibility determines whether an alert is eligible for threshold tuning
// BEFORE doing expensive metric queries and computation. Returns an empty string if eligible,
// or a human-readable reason if not eligible.
//
// This is the first line of defense — it catches cases where threshold tuning is fundamentally
// inappropriate, not just risky (which is what ValidateSuggestion handles post-computation).
func checkThresholdEligibility(alertDef *AlertDefinition) string {
	if alertDef == nil {
		return "no alert definition available"
	}

	operator := strings.ToLower(alertDef.Operator)
	metricName := strings.ToLower(alertDef.MetricName)

	// Equality operator alerts (== / !=) compare metric values to expected state.
	// Adjusting the threshold changes *which* state matches, not noise sensitivity.
	// Example: current_replicas == max_replicas fires when HPA is maxed out.
	if operator == "==" || operator == "!=" || operator == "eq" || operator == "ne" {
		return fmt.Sprintf("This alert uses an equality operator (%s). Threshold tuning is not applicable — consider adjusting the alert condition or adding a for-duration instead.", alertDef.Operator)
	}

	// Intentional sensitivity alerts: threshold is 0 with > operator.
	// These fire on *any* occurrence (e.g. error_count > 0, OOMKilled > 0).
	// Raising the threshold means "allow N errors before alerting" which needs explicit intent.
	if alertDef.CurrentThreshold == 0 && isGreaterThanOperator(alertDef.Operator) {
		return "This alert intentionally triggers on any non-zero value (threshold = 0). Raising the threshold would ignore some occurrences — this should be a deliberate decision, not an automated suggestion."
	}

	// absent() / vector(0) alerts check for the absence of a metric, not its value.
	// There's no threshold to tune — the alert fires when the metric disappears.
	if strings.Contains(metricName, "absent(") || strings.Contains(metricName, "absent_over_time(") {
		return "This alert uses absent() to detect missing metrics. There is no threshold to tune — consider adjusting the for-duration instead."
	}

	return ""
}

// SuggestionRisk indicates how risky a threshold suggestion is for the user.
// This is separate from confidence (which measures data quality) — risk measures
// whether applying the suggestion could cause harm (missed real incidents).
type SuggestionRisk struct {
	Level    string   `json:"level"`    // "safe", "review", "dangerous"
	Warnings []string `json:"warnings"` // human-readable warnings for the user
}

// metricHint captures semantic knowledge about a metric that the statistical
// algorithm can't infer on its own.
type metricHint struct {
	isBounded      bool    // true for percentage/ratio metrics (0-100 or 0-1)
	upperBound     float64 // maximum possible value (e.g., 100 for percentage)
	lowerBound     float64 // minimum possible value (e.g., 0)
	isMonotonic    bool    // true for time-since or counter metrics
	isBoolean      bool    // true for == comparisons that return matching values
	metricCategory string  // "percentage", "time_since", "counter", "rate", "gauge", "boolean", "unknown"
	humanUnit      string  // "%" , "seconds", "count", etc.
}

// detectMetricHints analyzes the metric expression and threshold to infer semantic properties.
// This is the key to not suggesting TargetDown > 100% or KubeJobNotCompleted > 70 hours.
func detectMetricHints(alertDef *AlertDefinition) *metricHint {
	hint := &metricHint{metricCategory: "unknown"}
	if alertDef == nil {
		return hint
	}

	metricName := strings.ToLower(alertDef.MetricName)
	metricNS := strings.ToLower(alertDef.MetricNamespace)
	operator := strings.ToLower(alertDef.Operator)

	// --- Percentage/ratio metrics (bounded 0-100 or 0-1) ---
	percentagePatterns := []string{
		"100 *", "100*",
		"_percent", "percentage",
		"cpu_utilization", "memory_utilization", "disk_utilization",
		"cpuutilization", "memoryutilization",
		"statuscheckfailed",
	}
	for _, p := range percentagePatterns {
		if strings.Contains(metricName, p) {
			hint.isBounded = true
			hint.upperBound = 100
			hint.lowerBound = 0
			hint.metricCategory = "percentage"
			hint.humanUnit = "%"
			break
		}
	}

	// Detect "100 * (count / count)" pattern — common for TargetDown, error rate alerts
	if strings.Contains(metricName, "100") && strings.Contains(metricName, "count") {
		hint.isBounded = true
		hint.upperBound = 100
		hint.lowerBound = 0
		hint.metricCategory = "percentage"
		hint.humanUnit = "%"
	}

	// Detect ratio metrics that are 0-1 bounded
	ratioPatterns := []string{
		"_ratio", "saturation",
		"process_cpu", "cpu_usage", "cpuusage",
	}
	for _, p := range ratioPatterns {
		if strings.Contains(metricName, p) && !hint.isBounded {
			// Only if threshold is in 0-1 range
			if alertDef.CurrentThreshold <= 1.0 && alertDef.CurrentThreshold >= 0 {
				hint.isBounded = true
				hint.upperBound = 1.0
				hint.lowerBound = 0
				hint.metricCategory = "ratio"
				hint.humanUnit = "ratio"
			}
		}
	}

	// --- Time-since metrics (monotonically increasing while condition holds) ---
	timePatterns := []string{
		"time() -", "time()-",
		"_age_seconds", "_last_seen",
		"time_since",
	}
	for _, p := range timePatterns {
		if strings.Contains(metricName, p) {
			hint.isMonotonic = true
			hint.metricCategory = "time_since"
			hint.humanUnit = "seconds"
			break
		}
	}

	// --- Boolean/equality comparisons ---
	// Alerts like `current_replicas == max_replicas` return the matching value, not 0/1.
	// A suggested threshold on these doesn't make semantic sense.
	// Only flag if not already categorized — PromQL like `100 * (count(up == 0) / count(up))`
	// contains " == " but is a percentage metric, not boolean.
	if hint.metricCategory == "unknown" && (strings.Contains(metricName, " == ") || operator == "==") {
		hint.isBoolean = true
		hint.metricCategory = "boolean"
	}

	// --- Rate metrics from Prometheus ---
	ratePatterns := []string{"rate(", "increase(", "irate("}
	for _, p := range ratePatterns {
		if strings.Contains(metricName, p) {
			if hint.metricCategory == "unknown" {
				hint.metricCategory = "rate"
			}
			break
		}
	}

	// --- CloudWatch namespace hints ---
	if metricNS != "" {
		switch {
		case strings.Contains(metricNS, "aws/ec2") && strings.Contains(metricName, "utilization"):
			hint.isBounded = true
			hint.upperBound = 100
			hint.metricCategory = "percentage"
			hint.humanUnit = "%"
		case strings.Contains(metricNS, "aws/elb") && strings.Contains(metricName, "count"):
			hint.metricCategory = "counter"
			hint.humanUnit = "count"
		}
	}

	return hint
}

// ValidateSuggestion runs post-computation guardrails on a threshold suggestion.
// It returns a SuggestionRisk assessment and may mutate the suggestion:
// - Caps suggested threshold to metric bounds
// - Downgrades confidence for dangerous suggestions
// - Adds human-readable warnings
// - Changes recommendation_type if the suggestion effectively disables the alert
//
// This function is the "safety net" that prevents impractical suggestions from reaching users.
func ValidateSuggestion(suggestion *ThresholdSuggestion, alertDef *AlertDefinition, alertQuality *AlertQualityScore) *SuggestionRisk {
	if suggestion == nil || alertDef == nil {
		return &SuggestionRisk{Level: "safe"}
	}

	hint := detectMetricHints(alertDef)
	risk := &SuggestionRisk{Level: "safe"}

	// Heuristic: if threshold is in 0-1 range AND all observed data is within 0-1,
	// it's very likely a ratio/fraction metric bounded at 1.0.
	// Run this before Gate 1 so the bounded check can cap the suggestion.
	if !hint.isBounded && alertDef.CurrentThreshold > 0 && alertDef.CurrentThreshold <= 1.0 {
		if suggestion.MetricP95 > 0 && suggestion.MetricP95 <= 1.05 && suggestion.MetricP50 >= 0 {
			hint.isBounded = true
			hint.upperBound = 1.0
			hint.lowerBound = 0
			hint.metricCategory = "ratio"
			hint.humanUnit = " (0-1)"
		}
	}

	// --- Gate 1: Bounded metric check ---
	// Never suggest a threshold outside a metric's natural range.
	if hint.isBounded {
		if isGreaterThanOperator(alertDef.Operator) && suggestion.SuggestedThreshold >= hint.upperBound {
			risk.Warnings = append(risk.Warnings,
				fmt.Sprintf("This metric is bounded at %.0f%s. Suggesting > %.0f%s would make this alert impossible to fire.",
					hint.upperBound, hint.humanUnit, hint.upperBound, hint.humanUnit))
			// Cap to a reasonable value within bounds
			suggestion.SuggestedThreshold = roundTo(hint.upperBound*0.95, 2)
			suggestion.Reason += " [Capped: metric is bounded, original suggestion exceeded maximum.]"
			risk.Level = "dangerous"
			suggestion.Confidence = "low"
		}
		if isLessThanOperator(alertDef.Operator) && suggestion.SuggestedThreshold <= hint.lowerBound {
			suggestion.SuggestedThreshold = roundTo(hint.lowerBound*1.05, 2)
			risk.Level = "dangerous"
			suggestion.Confidence = "low"
			risk.Warnings = append(risk.Warnings,
				fmt.Sprintf("This metric is bounded at %.0f. Suggesting < %.0f would make this alert impossible to fire.",
					hint.lowerBound, hint.lowerBound))
		}
	}

	// --- Gate 2: Monotonic time metric check ---
	// For time() - timestamp metrics, the value grows continuously while a condition persists.
	// A huge suggested threshold just means "wait longer before alerting" which is rarely what users want.
	if hint.isMonotonic {
		if suggestion.SuggestedThreshold > alertDef.CurrentThreshold*3 {
			risk.Level = "review"
			risk.Warnings = append(risk.Warnings,
				fmt.Sprintf("This is a time-based metric (duration in %s). Raising the threshold from %s to %s means waiting %.0fx longer before alerting. Consider if the current timeout is actually correct and the alert is firing because the underlying issue is real.",
					hint.humanUnit,
					formatHumanDuration(alertDef.CurrentThreshold),
					formatHumanDuration(suggestion.SuggestedThreshold),
					suggestion.SuggestedThreshold/alertDef.CurrentThreshold))
			suggestion.Confidence = "low"
		}
	}

	// --- Gate 3: Boolean/equality metric check ---
	if hint.isBoolean {
		risk.Level = "review"
		risk.Warnings = append(risk.Warnings,
			"This alert uses an equality comparison (==). The returned value represents a matching metric value, not a severity level. Adjusting the threshold may filter out valid matches unintentionally.")
		suggestion.Confidence = "low"
	}

	// --- Gate 4: Baseline mismatch detection ---
	// When the metric's median (p50) exceeds the current threshold for a ">" operator,
	// over half the data points fire the alert. This is a misconfigured threshold.
	// For ">=" operators, p50 == threshold also means ~50% fires.
	gtMismatch := suggestion.MetricP50 > alertDef.CurrentThreshold
	if alertDef.Operator == ">=" && suggestion.MetricP50 >= alertDef.CurrentThreshold {
		gtMismatch = true
	}
	if isGreaterThanOperator(alertDef.Operator) && alertDef.CurrentThreshold > 0 && gtMismatch {
		ratio := suggestion.MetricP50 / alertDef.CurrentThreshold
		risk.Level = "dangerous"
		suggestion.RecommendationType = "review_alert"
		suggestion.Confidence = "low"
		risk.Warnings = append(risk.Warnings,
			fmt.Sprintf("The metric's normal value (p50=%.2f) is %.1fx the current threshold (%.2f). "+
				"More than half of all data points fire this alert — it triggers on normal behavior. "+
				"The alert definition itself needs review.",
				suggestion.MetricP50, ratio, alertDef.CurrentThreshold))
	} else if isLessThanOperator(alertDef.Operator) && alertDef.CurrentThreshold > 0 &&
		suggestion.MetricP50 < alertDef.CurrentThreshold {
		ratio := alertDef.CurrentThreshold / suggestion.MetricP50
		risk.Level = "dangerous"
		suggestion.RecommendationType = "review_alert"
		suggestion.Confidence = "low"
		risk.Warnings = append(risk.Warnings,
			fmt.Sprintf("The metric's normal value (p50=%.2f) is %.1fx below the current threshold (%.2f). "+
				"More than half of all data points fire this alert — it triggers on normal behavior. "+
				"The alert definition itself needs review.",
				suggestion.MetricP50, ratio, alertDef.CurrentThreshold))
	}

	// --- Gate 5: Effective silencing detection ---
	// If the suggestion would eliminate >= 95% of alerts, flag it as potentially silencing.
	if suggestion.EstimatedReduction >= 95 && suggestion.RecommendationType != "disable" && suggestion.RecommendationType != "review_alert" {
		// Check if this is really just threshold tuning or effectively disabling
		if alertQuality != nil && alertQuality.EngagementRate > 0.2 {
			// People actually look at these alerts — silencing them is dangerous
			risk.Level = "dangerous"
			suggestion.RecommendationType = "review_alert"
			risk.Warnings = append(risk.Warnings,
				fmt.Sprintf("This change would suppress ~%.0f%% of alerts, but %.0f%% of current alerts are being actively engaged with. This suggests the alert is valuable and aggressive noise reduction may cause missed incidents.",
					suggestion.EstimatedReduction, alertQuality.EngagementRate*100))
			suggestion.Confidence = "low"
		} else if suggestion.EstimatedReduction == 100 {
			if risk.Level != "dangerous" {
				risk.Level = "review"
			}
			suggestion.RecommendationType = "review_alert"
			risk.Warnings = append(risk.Warnings,
				"This change would suppress all historical alerts for this rule. While noise reduction is the goal, a 100% reduction means this alert would have never fired — verify the new threshold still catches real issues.")
		}
	}

	// --- Gate 6: Extreme ratio change ---
	if alertDef.CurrentThreshold > 0 {
		ratio := suggestion.SuggestedThreshold / alertDef.CurrentThreshold
		if ratio > 10 || (ratio < 0.1 && ratio > 0) {
			if risk.Level != "dangerous" {
				risk.Level = "review"
			}
			risk.Warnings = append(risk.Warnings,
				fmt.Sprintf("This is a %.0fx change from the current threshold. Large adjustments like this may indicate the alert definition itself needs rethinking, not just the threshold value.",
					ratio))
			// Don't allow > medium confidence for extreme changes
			if suggestion.Confidence == "high" {
				suggestion.Confidence = "medium"
			}
		}
	}

	return risk
}

// formatHumanDuration converts seconds to a human-readable duration string.
func formatHumanDuration(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.0fs", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%.0fm", seconds/60)
	}
	hours := seconds / 3600
	if hours < 24 {
		return fmt.Sprintf("%.1fh", hours)
	}
	return fmt.Sprintf("%.1f days", hours/24)
}

// AdjustConfidenceForRisk takes the computed confidence and risk assessment,
// returning an adjusted confidence that factors in operational risk.
// A "high" confidence suggestion that's operationally dangerous should never
// appear as "high" to the user.
func AdjustConfidenceForRisk(confidence string, risk *SuggestionRisk) string {
	if risk == nil {
		return confidence
	}
	switch risk.Level {
	case "dangerous":
		return "low"
	case "review":
		if confidence == "high" {
			return "medium"
		}
		return confidence
	default:
		return confidence
	}
}

// BuildUserFacingReason constructs a concise, user-facing explanation.
//
// The frontend displays threshold values, reduction estimate, risk warnings,
// and guidance text in dedicated UI components, so this reason should only
// contain information not shown elsewhere:
//   - Impact summary (for non-100% reductions, since 100% is handled by the risk banner)
//   - Technical method details (statistical method, percentiles, MAD)
//
// Previously this function embedded threshold changes, risk warnings with emoji,
// and reduction text — all of which were duplicated by the frontend UI.
func BuildUserFacingReason(suggestion *ThresholdSuggestion, alertDef *AlertDefinition, risk *SuggestionRisk) string {
	if suggestion == nil {
		return ""
	}

	var parts []string

	// Impact statement — only for partial reductions.
	// 100% reduction is shown by the risk warning banner + "Suppresses all alerts" chip.
	if suggestion.EstimatedReduction > 0 && suggestion.EstimatedReduction < 100 {
		parts = append(parts, fmt.Sprintf("Expected to reduce alert volume by ~%.0f%%.", suggestion.EstimatedReduction))
	}

	// Technical details as a secondary note
	if suggestion.Method != "" {
		parts = append(parts, fmt.Sprintf("(Method: %s, p50=%.2f, p95=%.2f, MAD=%.2f)",
			suggestion.Method, suggestion.MetricP50, suggestion.MetricP95, suggestion.MetricMAD))
	}

	return strings.Join(parts, " ")
}
