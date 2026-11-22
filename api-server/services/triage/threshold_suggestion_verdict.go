package triage

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// What a threshold suggestion is allowed to say. The engine has several paths that produce a
// suggestion (baseline diagnosis, legacy statistics, firing-history verdicts) and several readers
// that serve one (list, investigation card, apply options). The rules below are applied once on the
// way out of the engine and once on the way out of storage, so no path or reader has its own idea
// of when a number may be shown or what a recommendation is called.

// recommendationLabels is the wording for every recommendation type the engine emits. A type
// missing here is served as unknownRecommendationLabel, never as a tuning.
var recommendationLabels = map[string]string{
	"tune_threshold":     "Tune Threshold",
	"tune_both":          "Tune Both",
	"increase_duration":  "Increase Duration",
	"disable":            "Disable Alert",
	"none":               "No Change",
	"review_alert":       "Review Alert",
	"investigate_signal": "Investigate",
	"insufficient_data":  "Not Enough Data",
	"not_eligible":       "Nothing to Tune",
}

const unknownRecommendationLabel = "Review"

// recommendationLabel returns the wording shown for a recommendation type.
func recommendationLabel(recType string) string {
	if label, ok := recommendationLabels[recType]; ok {
		return label
	}
	return unknownRecommendationLabel
}

// UnavailableSuggestion is what a reader serves for a stored row that carries no suggestion.
// A rule that has nothing to tune (not_eligible) is a different answer from one that could not
// be analysed this week (skipped, error), and each gets its own label.
func UnavailableSuggestion(storedStatus, reason string) *ThresholdSuggestion {
	recType := "insufficient_data"
	if storedStatus == "not_eligible" {
		recType = "not_eligible"
	}
	return &ThresholdSuggestion{
		RecommendationType:  recType,
		RecommendationLabel: recommendationLabel(recType),
		Reason:              reason,
	}
}

// offersNumber reports whether a recommendation type carries a new threshold value. Every other
// type keeps the current threshold: "investigate", "review" and "not enough data" are verdicts
// about the alert, not values to apply.
func offersNumber(recType string) bool {
	return recType == "tune_threshold" || recType == "tune_both"
}

// minMeaningfulReduction is the smallest estimated drop in firings (percent) worth changing a rule
// for. Below it the suggestion is a re-statement of the current threshold (2 → 2.02).
const minMeaningfulReduction = 10.0

// chronicReviewWarning is why a value offered for an always-in-alarm rule needs a person's
// judgement: the same data fits a broken system and a threshold set too tight.
const chronicReviewWarning = "Either the system is persistently degraded or the threshold was set inside normal behaviour — the data cannot tell those apart. Confirm the current level is acceptable before applying."

// finalizeSuggestion runs the safety checks on a computed suggestion and enforces what it may
// carry. It is the only place that decides whether a number survives.
func finalizeSuggestion(s *ThresholdSuggestion, alertDef *AlertDefinition, alertQuality *AlertQualityScore, mh *MetricHistory) {
	if s == nil || alertDef == nil {
		return
	}
	theta := alertDef.CurrentThreshold
	legacy := s.Diagnosis == ""

	// Guardrails judge a proposed change. An abstain proposes none, and a baseline diagnosis that
	// keeps the threshold (excursion, ambiguous, pinned at a bound) is already final.
	runGuardrails := s.RecommendationType != "insufficient_data" && s.RecommendationType != "not_eligible" &&
		(legacy || offersNumber(s.RecommendationType))

	var risk *SuggestionRisk
	if runGuardrails {
		risk = ValidateSuggestion(s, alertDef, alertQuality)
		// A gate may move the threshold after the reduction was estimated. Recompute so the stored
		// number describes the value actually kept.
		if mh != nil && len(mh.Values) > 0 {
			sortedVals := append([]float64(nil), mh.Values...)
			sort.Float64s(sortedVals)
			s.EstimatedReduction = estimateReduction(sortedVals, theta, s.SuggestedThreshold, isGreaterThanOperator(alertDef.Operator))
		}
		s.RiskLevel = risk.Level
		s.RiskWarnings = risk.Warnings
		s.Confidence = AdjustConfidenceForRisk(s.Confidence, risk)
		switch {
		case s.Diagnosis == "chronic":
			settleChronic(s, risk)
		case !legacy && !offersNumber(s.RecommendationType) && s.baseReason != "":
			// A gate refused the re-baseline. The diagnosis still stands; the value it named does
			// not, so it must not survive in the explanation either.
			s.Reason = s.baseReason + " No new value is offered: " + firstWarning(risk)
		}
	}

	reworded := dropNegligibleTuning(s, theta)
	enforceNumberInvariant(s, theta)

	// The legacy path has no diagnosis narrative; its reason is built from what is left to say.
	if legacy && risk != nil && !reworded {
		s.Reason = BuildUserFacingReason(s, alertDef, risk)
	}
}

// settleChronic decides the fate of the value named for an always-in-alarm rule. It is offered
// only when no safety check objected, and then as something to review rather than clean advice.
func settleChronic(s *ThresholdSuggestion, risk *SuggestionRisk) {
	if s.RecommendationType == "tune_threshold" && risk.Level == "safe" {
		s.RiskLevel = "review"
		s.RiskWarnings = []string{chronicReviewWarning}
		s.Confidence = "low"
		return
	}
	s.RecommendationType = "investigate_signal"
	s.Reason = s.baseReason + " No new value is offered: " + firstWarning(risk)
	s.RiskLevel = ""
	s.RiskWarnings = nil
	s.Confidence = "low"
}

// firstWarning is the safety check's own explanation for refusing a value.
func firstWarning(risk *SuggestionRisk) string {
	if risk != nil && len(risk.Warnings) > 0 {
		return risk.Warnings[0]
	}
	return "no value clear of the normal range passed the safety checks."
}

// dropNegligibleTuning withdraws a threshold change too small to matter. It reports whether it
// reworded the suggestion's reason.
func dropNegligibleTuning(s *ThresholdSuggestion, theta float64) bool {
	if !offersNumber(s.RecommendationType) || s.EstimatedReduction >= minMeaningfulReduction {
		return false
	}
	small := fmt.Sprintf("Moving the threshold from %.4g to %.4g would remove only about %.0f%% of firings, which is not worth a change.",
		theta, s.SuggestedThreshold, s.EstimatedReduction)
	if s.RecommendationType == "tune_both" && s.SuggestedDuration > 0 {
		// The longer wait stands on its own evidence (how long firings last).
		s.RecommendationType = "increase_duration"
		s.Reason = small + " A longer evaluation window is still suggested."
	} else {
		s.RecommendationType = "none"
		s.Reason = small
	}
	s.RiskLevel = ""
	s.RiskWarnings = nil
	return true
}

// enforceNumberInvariant is the rule every reader relies on: a suggestion carries a value other
// than the current threshold only when its type is a tuning.
func enforceNumberInvariant(s *ThresholdSuggestion, theta float64) {
	if offersNumber(s.RecommendationType) {
		return
	}
	s.SuggestedThreshold = theta
	s.EstimatedReduction = 0
}

// normalizeStoredNumber applies the same invariant to a stored row, so a row written before a
// rule changed is served by today's rule. It returns the suggested threshold and estimated
// reduction to serve. A missing or unknown type is not a tuning.
func normalizeStoredNumber(recType string, current, suggested, reduction *float64) (*float64, *float64) {
	if offersNumber(recType) {
		return suggested, reduction
	}
	return current, nil
}

// seriesIdentityLabels are the labels that name what a series is about, most telling first.
var seriesIdentityLabels = []string{"namespace", "pod", "container", "node", "queue", "device", "persistentvolumeclaim", "deployment", "statefulset", "daemonset", "job_name", "horizontalpodautoscaler"}

// seriesPlumbingLabels describe how a series was scraped, not what it is about.
var seriesPlumbingLabels = map[string]bool{"__name__": true, "job": true, "endpoint": true, "prometheus": true, "service": true, "cluster": true}

// describeSeries names the instance a metric series belongs to, from at most four of its labels:
// "namespace=shop, pod=cart-5d9f". Empty when the series carries no labels (a rule aggregated to
// one number has no instance to name).
func describeSeries(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	const maxLabels = 4
	var parts []string
	used := map[string]bool{}
	add := func(k string) {
		v, ok := labels[k]
		if !ok || v == "" || used[k] || len(parts) >= maxLabels {
			return
		}
		used[k] = true
		if len(v) > 60 {
			v = v[:60] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	for _, k := range seriesIdentityLabels {
		add(k)
	}
	rest := make([]string, 0, len(labels))
	for k := range labels {
		if !seriesPlumbingLabels[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		add(k)
	}
	if len(parts) == 0 {
		// Only plumbing labels: still better than nothing for telling two series apart.
		all := make([]string, 0, len(labels))
		for k := range labels {
			all = append(all, k)
		}
		sort.Strings(all)
		for _, k := range all {
			add(k)
		}
	}
	return strings.Join(parts, ", ")
}

// storedChronicValueRe matches the sentence an always-in-alarm row used to end with, naming a
// value ("93.8 is the nearest value outside the observed range (~98% fewer firings); …").
var storedChronicValueRe = regexp.MustCompile(` [-+]?[0-9][0-9.e+-]* is the nearest value outside the observed range.*$`)

// normalizeStoredReason is normalizeStoredNumber's counterpart for the explanation: a row that
// is served without a new value must not go on to name one in its reason.
func normalizeStoredReason(recType, reason string) string {
	if offersNumber(recType) {
		return reason
	}
	return strings.TrimSpace(storedChronicValueRe.ReplaceAllString(reason, ""))
}

// isStateAlert reports whether a rule watches a yes/no condition rather than a level: it fires
// at ">= 1" and the metric only ever reads 0 or 1 (crash-looping, a job failed, a target is
// down). There is no threshold between "no" and "yes" to move.
func isStateAlert(alertDef *AlertDefinition, values []float64) bool {
	if alertDef == nil || len(values) == 0 || alertDef.CurrentThreshold != 1 {
		return false
	}
	op := strings.ToLower(strings.TrimSpace(alertDef.Operator))
	if op != ">=" && op != "greaterthanorequaltothreshold" {
		return false
	}
	for _, v := range values {
		if v != 0 && v != 1 {
			return false
		}
	}
	return true
}

const stateAlertReason = "This alert watches a yes/no condition: the metric only reads 0 or 1 and the rule fires on 1. There is no threshold to tune. If it is noisy, change how long the condition must hold before it fires."

// Reasons a rule gets no numeric suggestion. Call sites wrap these (%w) around the technical
// detail, which goes to logs; plainReason words the stored, user-facing text.
var (
	errRuleNotFound        = errors.New("rule definition not found")
	errNotComparison       = errors.New("not a comparison against a number")
	errDynamicThreshold    = errors.New("dynamic threshold")
	errThresholdUnreadable = errors.New("threshold not readable")
	errMetricUnavailable   = errors.New("metric could not be read")
	// errMetricQueryFailed: the metric store did not answer (an error, a dropped connection, a
	// timeout). Unlike errMetricUnavailable — it answered, with no data — this says nothing about
	// the rule, so the rule's last verdict is kept and the fetch is tried again on the next run.
	errMetricQueryFailed = errors.New("metric store did not answer")
)

// ruleLookupError reports a failed lookup of a rule's definition. Only a missing row means the
// rule is not there; a timeout or a dropped connection must not be stored as "rule not found".
func ruleLookupError(subject string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no event_rule found for %s: %w", errRuleNotFound, subject, err)
	}
	return fmt.Errorf("no event_rule found for %s: %w", subject, err)
}

// plainReason words why no numeric suggestion exists, for someone who has not read the rule's
// source. Unknown causes get a general sentence rather than the raw error.
func plainReason(cause error) string {
	switch {
	case cause == nil:
		return "No suggestion could be computed for this alert."
	case errors.Is(cause, errMultiCondition):
		return "This rule fires only when several conditions hold together, so changing one number would not predict when it fires."
	case errors.Is(cause, errDynamicThreshold):
		return "This rule compares two changing values rather than a value against a fixed number, so there is no threshold to tune."
	case errors.Is(cause, errNotComparison):
		return "This rule is not a comparison against a number (for example, it checks that a metric is missing), so there is no threshold to tune."
	case errors.Is(cause, errRuleNotFound):
		return "The rule definition behind this alert could not be found, so its threshold could not be read."
	case errors.Is(cause, errThresholdUnreadable):
		return "This alert does not carry a readable threshold, so there is nothing to compare the metric against."
	case errors.Is(cause, errMetricQueryFailed):
		return "The metric store did not answer while this rule was being analysed. It will be tried again on the next run."
	case errors.Is(cause, errMetricUnavailable):
		return "The metric behind this rule could not be read for the last 7 days, so the threshold could not be checked against real values."
	default:
		return "This rule could not be analysed for a threshold suggestion."
	}
}
