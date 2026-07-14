package triage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// Baseline-fidelity: the statistics were sound but computed on the wrong signal — the whole 7-day
// history, which includes the very firing periods the tuner is trying to tune away. This file
// separates the metric history into an OFF-ALERT baseline distribution and a FIRING distribution
// (using the events table's firing intervals), then diagnoses deterministically why the alert fires
// and picks the right action — replacing the old `spike` path.

// Discriminator constants. Chosen to reproduce the hand labels on the eval set; tunable, and must be
// validated against that set rather than asserted.
const (
	chronicCoverageMax       = 0.30 // baseline coverage below this ⇒ metric lives in an alarm state (chronic)
	mistunedFraction         = 0.90 // |band edge − θ| ≤ (1−this)·θ ⇒ normal band reaches the threshold (mis-tuned)
	excursionFraction        = 0.70 // |band edge − θ| ≥ (1−this)·θ ⇒ firings are genuine excursions
	intervalMergeSteps       = 2    // merge firing intervals separated by fewer than N fetch steps
	baselineContaminationMax = 0.40 // fraction of OFF-ALERT baseline that may cross θ before the baseline is deemed contaminated/chronic
)

// firingInterval is a [start, end] window (unix seconds) during which the alert rule was firing.
type firingInterval struct {
	start int64
	end   int64
}

// baselineDecomposition holds the two distributions and the fidelity self-check result.
type baselineDecomposition struct {
	baselineSorted []float64
	firingSorted   []float64
	Baseline       DistStats
	Firing         DistStats
	Coverage       float64 // baseline sample count / total sample count
	IntervalCount  int
	// SelfCheckFailed: the events table says the alert fired, but the fetched history contains no
	// firing-window sample that violates the threshold — the history provably does not represent
	// what the rule evaluated (wrong series / resolution / step). §1.5 of the design doc.
	SelfCheckFailed bool
	// SeasonalitySuspected: weekday vs weekend baseline medians diverge materially — a 7-day window
	// may be too short to tune reliably.
	SeasonalitySuspected bool
}

// statsOfSorted computes robust statistics for a pre-sorted slice.
func statsOfSorted(sorted []float64) DistStats {
	return DistStats{
		Count:  len(sorted),
		P50:    roundTo(percentile(sorted, 50), 2),
		P90:    roundTo(percentile(sorted, 90), 2),
		P95:    roundTo(percentile(sorted, 95), 2),
		P99:    roundTo(percentile(sorted, 99), 2),
		Median: roundTo(computeMedian(sorted), 2),
		MAD:    roundTo(computeMAD(sorted), 2),
	}
}

// fetchFiringIntervals returns the firing windows over the last 7 days for the SERIES the
// decomposition analyzes. Prometheus/PagerDuty fingerprints are stable per series, so we key on the
// representative event's fingerprint — an alertname spans hundreds of pods and the union of their
// firing windows would spuriously cover the whole window (measured: avg per-series coverage 3.8% vs
// 100% union). Cloud alarm fingerprints are per-event (unique), so those fall back to the alarm
// identity, which is already effectively single-series. Open-ended events (ends_at IS NULL) are
// firing-until-now. Time bounds use the DB clock (NOW()) for consistency across replicas.
func fetchFiringIntervals(ctx context.Context, db *sqlx.DB, source, alertRuleKey, fingerprint, accountID, tenantID string) []firingInterval {
	if accountID == "" {
		return nil
	}
	var whereCondition, sourceFilter, arg string
	if (source == "prometheus" || source == "pagerduty_webhook") && fingerprint != "" {
		whereCondition = "fingerprint = $1"
		arg = fingerprint
		sourceFilter = fmt.Sprintf("AND source = '%s'", source)
	} else {
		whereCondition, sourceFilter = alertRuleWhereClause(source, alertRuleKey)
		arg = alertRuleKey
	}
	if whereCondition == "" || arg == "" {
		return nil
	}

	query := fmt.Sprintf(`
		SELECT starts_at, COALESCE(ends_at, NOW()) AS ends_at
		FROM events
		WHERE %s AND cloud_account_id = $2 AND tenant = $3
		  %s
		  AND starts_at > NOW() - INTERVAL '7 days'
		ORDER BY starts_at`, whereCondition, sourceFilter)

	rows, err := db.QueryContext(ctx, query, arg, accountID, tenantID)
	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to fetch firing intervals", "error", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var intervals []firingInterval
	for rows.Next() {
		var startsAt, endsAt time.Time
		if err := rows.Scan(&startsAt, &endsAt); err != nil {
			continue
		}
		if endsAt.Before(startsAt) {
			endsAt = startsAt
		}
		intervals = append(intervals, firingInterval{start: startsAt.Unix(), end: endsAt.Unix()})
	}
	if err := rows.Err(); err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: error iterating firing intervals", "error", err)
	}
	return intervals
}

// padAndMergeIntervals pads each interval start backward (the metric was already violating for the
// rule's `for` duration before starts_at) and merges intervals separated by fewer than a couple of
// steps (flapping alerts produce dozens of abutting windows).
func padAndMergeIntervals(intervals []firingInterval, alertDef *AlertDefinition, stepSeconds int) []firingInterval {
	if len(intervals) == 0 {
		return nil
	}
	if stepSeconds <= 0 {
		stepSeconds = 60
	}

	// Pad-start = rule "for" duration + one step. AWS carries Period×EvaluationPeriods; Azure carries
	// a window Period; Prometheus carries neither here, so fall back to one step.
	forSec := 0
	if alertDef != nil {
		switch {
		case alertDef.EvaluationPeriods > 0:
			forSec = alertDef.Period * alertDef.EvaluationPeriods
		case alertDef.Period > 0:
			forSec = alertDef.Period
		}
	}
	if forSec <= 0 {
		forSec = stepSeconds
	}
	padSec := int64(forSec + stepSeconds)

	padded := make([]firingInterval, len(intervals))
	for i, iv := range intervals {
		padded[i] = firingInterval{start: iv.start - padSec, end: iv.end}
	}
	sort.Slice(padded, func(i, j int) bool { return padded[i].start < padded[j].start })

	gap := int64(intervalMergeSteps * stepSeconds)
	merged := []firingInterval{padded[0]}
	for _, iv := range padded[1:] {
		last := &merged[len(merged)-1]
		if iv.start <= last.end+gap {
			if iv.end > last.end {
				last.end = iv.end
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}

// decomposeHistory masks the metric history against firing intervals, producing the baseline and
// firing distributions plus the §1.5 self-check.
func decomposeHistory(mh *MetricHistory, intervals []firingInterval, threshold float64, isGT bool) baselineDecomposition {
	var baseline, firing, wdVals, weVals []float64
	inFiring := intervalMasker(intervals)

	for i := 0; i < len(mh.Timestamps) && i < len(mh.Values); i++ {
		ts, v := mh.Timestamps[i], mh.Values[i]
		if inFiring(ts) {
			firing = append(firing, v)
			continue
		}
		baseline = append(baseline, v)
		// Phase E — bucket off-alert samples by weekday vs weekend for a cheap seasonality check.
		if wd := time.Unix(ts, 0).UTC().Weekday(); wd == time.Saturday || wd == time.Sunday {
			weVals = append(weVals, v)
		} else {
			wdVals = append(wdVals, v)
		}
	}
	sort.Float64s(baseline)
	sort.Float64s(firing)

	total := len(baseline) + len(firing)
	coverage := 1.0
	if total > 0 {
		coverage = float64(len(baseline)) / float64(total)
	}

	// Self-check: events fired but no firing-window sample violates the threshold.
	selfCheckFailed := false
	if len(intervals) > 0 {
		violating := 0
		for _, v := range firing {
			if (isGT && v > threshold) || (!isGT && v < threshold) {
				violating++
			}
		}
		selfCheckFailed = violating == 0
	}

	// Phase E — flag weekly seasonality when weekday and weekend baseline medians diverge materially.
	// A 7-day window can't distinguish a Monday batch job from a degradation; this caps confidence
	// and recommends validating over a longer window rather than modeling seasonality in-house.
	seasonalitySuspected := false
	if len(wdVals) >= 12 && len(weVals) >= 12 {
		sort.Float64s(wdVals)
		sort.Float64s(weVals)
		wdMed, weMed := computeMedian(wdVals), computeMedian(weVals)
		if denom := math.Max(math.Abs(wdMed), math.Abs(weMed)); denom > 1e-9 && math.Abs(wdMed-weMed)/denom > 0.30 {
			seasonalitySuspected = true
		}
	}

	return baselineDecomposition{
		baselineSorted:       baseline,
		firingSorted:         firing,
		Baseline:             statsOfSorted(baseline),
		Firing:               statsOfSorted(firing),
		Coverage:             coverage,
		IntervalCount:        len(intervals),
		SelfCheckFailed:      selfCheckFailed,
		SeasonalitySuspected: seasonalitySuspected,
	}
}

// intervalMasker returns a closure that reports whether a unix timestamp falls in any interval.
// Intervals are assumed sorted by start (padAndMergeIntervals guarantees this).
func intervalMasker(intervals []firingInterval) func(int64) bool {
	if len(intervals) == 0 {
		return func(int64) bool { return false }
	}
	return func(ts int64) bool {
		// Binary search for the last interval whose start <= ts.
		i := sort.Search(len(intervals), func(i int) bool { return intervals[i].start > ts })
		if i == 0 {
			return false
		}
		return ts <= intervals[i-1].end
	}
}

// diagnoseWithBaseline is the deterministic discriminator (pure — no DB). It returns a suggestion and
// true when it handled the alert, or (nil, false) to fall back to the legacy statistical path.
func diagnoseWithBaseline(alertDef *AlertDefinition, mh *MetricHistory, intervals []firingInterval, alertQuality *AlertQualityScore) (*ThresholdSuggestion, bool) {
	if mh == nil || len(mh.Values) == 0 || alertDef == nil {
		return nil, false
	}
	isGT := isGreaterThanOperator(alertDef.Operator)
	isLT := isLessThanOperator(alertDef.Operator)
	if !isGT && !isLT {
		return nil, false // unknown operator → legacy path
	}
	theta := alertDef.CurrentThreshold
	if theta <= 0 {
		return nil, false // threshold-0 handled by the eligibility gate; ratios can't scale by θ
	}

	full := append([]float64(nil), mh.Values...)
	sort.Float64s(full)

	step := mh.Step
	if step <= 0 {
		step = 60
	}
	merged := padAndMergeIntervals(intervals, alertDef, step)
	decomp := decomposeHistory(mh, merged, theta, isGT)

	// Seed whole-history percentiles for downstream guardrails/UI (Gate 4 reads MetricP50, etc.).
	s := &ThresholdSuggestion{
		MetricP50:          roundTo(percentile(full, 50), 2),
		MetricP90:          roundTo(percentile(full, 90), 2),
		MetricP95:          roundTo(percentile(full, 95), 2),
		MetricP99:          roundTo(percentile(full, 99), 2),
		MetricMedian:       roundTo(computeMedian(full), 2),
		MetricMAD:          roundTo(computeMAD(full), 2),
		Baseline:           &decomp.Baseline,
		Firing:             &decomp.Firing,
		BaselineCoverage:   roundTo(decomp.Coverage, 4),
		SuggestedThreshold: theta,
		Confidence:         "low",
	}

	// §1.5 self-check: history doesn't represent what the rule evaluated → abstain.
	if decomp.SelfCheckFailed {
		s.RecommendationType = "insufficient_data"
		s.Reason = "Alert fired but the fetched metric history contains no firing-window sample that crosses the threshold — the history does not represent the series/resolution the rule evaluated. Abstaining until fidelity is fixed."
		return s, true
	}

	// Not enough clean off-alert data to define a baseline.
	if decomp.Baseline.Count < minThresholdSamples {
		s.RecommendationType = "insufficient_data"
		s.Reason = fmt.Sprintf("Only %d off-alert samples (need at least %d) — insufficient clean baseline to tune reliably.", decomp.Baseline.Count, minThresholdSamples)
		return s, true
	}

	// Phase D — measurability gate: a histogram_quantile() alert whose firing-window distribution is
	// flat at its top (p95 == p99) is saturated at the largest finite bucket boundary. Any threshold
	// derived from it is invented; abstain.
	if isHistogramSaturated(alertDef, decomp.Firing) {
		s.RecommendationType = "insufficient_data"
		s.Reason = fmt.Sprintf("Latency histogram is saturated at ~%.2f (p95 == p99 in the firing window) — the true distribution is unmeasurable past the top bucket. Add higher histogram buckets, or convert this alert to a burn-rate/SLO form; the threshold cannot be tuned from this data.", decomp.Firing.P99)
		return s, true
	}

	// 1) CHRONIC (coverage) — the metric is in an alarm state most of the window.
	if decomp.Coverage < chronicCoverageMax {
		s.Diagnosis = "chronic"
		s.RecommendationType = "investigate_signal"
		s.Reason = fmt.Sprintf("Metric is in an alarm state ~%.0f%% of the window (off-alert coverage only %.0f%%). This is a persistent signal, not noise — investigate the root cause; do not retune.",
			(1-decomp.Coverage)*100, decomp.Coverage*100)
		return s, true
	}

	// Baseline-quality gate — how much of the OFF-ALERT baseline itself crosses θ. Off-alert windows
	// should mostly NOT violate; a high rate means either the metric is chronically pinned at a
	// failure bound, or the firing intervals don't cleanly separate normal from alarm (under-recorded
	// events / series mismatch). Both make the band comparison below unreliable.
	if blViol := violationRate(decomp.baselineSorted, theta, isGT); blViol > baselineContaminationMax {
		bounded, upper, lower := boundsFor(alertDef, decomp.Baseline)
		pinned := bounded && ((isGT && decomp.Baseline.P50 >= upper-(upper-lower)*0.05) ||
			(isLT && decomp.Baseline.P50 <= lower+(upper-lower)*0.05))
		if pinned {
			s.Diagnosis = "chronic"
			s.RecommendationType = "investigate_signal"
			s.Reason = fmt.Sprintf("Metric is pinned at its failure bound (baseline p50=%.3g; %.0f%% of off-alert samples cross the threshold) — a persistent failure state, not noise. Investigate; do not retune.",
				decomp.Baseline.P50, blViol*100)
		} else {
			s.RecommendationType = "insufficient_data"
			s.Reason = fmt.Sprintf("Baseline is contaminated: %.0f%% of off-alert samples still cross the threshold, so the firing intervals don't cleanly separate normal from alarm (under-recorded events or a series mismatch). Abstaining rather than tuning off a dirty baseline.",
				blViol*100)
		}
		return s, true
	}

	// Boolean/state metric — ≤2 distinct values (e.g. 0/1) isn't a magnitude a numeric threshold can
	// tune; the fix is the alert condition, not the number. (After the contamination/pinned gate so a
	// metric constant at its failure bound reads as chronic, not "binary".)
	if distinctCount(full) <= 2 {
		s.Diagnosis = "boolean"
		s.RecommendationType = "review_alert"
		s.Reason = "Metric takes at most 2 distinct values (effectively binary/state) — a numeric threshold is not meaningful here; adjust the alert condition rather than the threshold value."
		return s, true
	}

	// Band edge = the baseline tail on the firing side (upper for >, lower for <).
	var bandEdge, gapFrac float64
	if isGT {
		bandEdge = percentile(decomp.baselineSorted, 95)
		gapFrac = (theta - bandEdge) / theta
	} else {
		bandEdge = percentile(decomp.baselineSorted, 5)
		gapFrac = (bandEdge - theta) / theta
	}

	switch {
	// 2) MIS-TUNED — normal band reaches/crosses the threshold. Re-baseline off BASELINE stats.
	case gapFrac <= (1 - mistunedFraction):
		s.Diagnosis = "mistuned"
		// Use UNrounded baseline median/MAD — the persisted DistStats are rounded to 2 decimals,
		// which collapses MAD to 0 for small-magnitude (ratio) metrics.
		bMed := computeMedian(decomp.baselineSorted)
		bMad := computeMAD(decomp.baselineSorted)
		var suggested float64
		var method string
		if isGT {
			suggested, method = computeUpperThreshold(decomp.baselineSorted, bMed, bMad, percentile(decomp.baselineSorted, 95))
			suggested = applySanityCap(suggested, theta, percentile(full, 99))
		} else {
			suggested, method = computeLowerThreshold(decomp.baselineSorted, bMed, bMad)
			p1 := percentile(full, 1)
			if floor := minOf(theta*0.1, p1); suggested < floor {
				suggested = floor
			}
		}
		s.SuggestedThreshold = roundTo(suggested, 2)
		s.Method = method + "-baseline"
		s.EstimatedReduction = estimateReduction(full, theta, s.SuggestedThreshold, isGT)
		// A re-baseline must move the threshold in the noise-reducing direction (up for >, down for <).
		// When the baseline is skewed (median far from θ, only the tail reaching it),
		// computeUpper/LowerThreshold can land on the WRONG side of θ, which would INCREASE firing —
		// flag for review instead of emitting a firing-increasing "tune". (A same-direction tune with
		// 0 reduction is a benign headroom change and the batch skips it.)
		if (isGT && s.SuggestedThreshold <= theta) || (isLT && s.SuggestedThreshold >= theta) {
			s.Diagnosis = "ambiguous"
			s.RecommendationType = "review_alert"
			s.SuggestedThreshold = theta
			s.EstimatedReduction = 0
			s.Confidence = "low"
			s.Reason = fmt.Sprintf("Baseline tail reaches the threshold (p%s=%.3g vs %.3g) but a re-baseline off the skewed baseline (median=%.3g) would point the wrong way and increase firing — review manually.",
				tailLabel(isGT), bandEdge, theta, bMed)
			return s, true
		}
		s.Reason = fmt.Sprintf("Normal (off-alert) band reaches the threshold: baseline p%s=%.2f vs threshold %.2f. Re-baseline to %.2f (baseline median=%.2f, MAD=%.2f).",
			tailLabel(isGT), bandEdge, theta, s.SuggestedThreshold, bMed, bMad)
		s.Confidence = computeConfidence(decomp.baselineSorted, theta, s.SuggestedThreshold, alertQuality)
		if decomp.SeasonalitySuspected {
			if s.Confidence == "high" {
				s.Confidence = "medium"
			}
			s.Reason += " [Weekly seasonality suspected: weekday vs weekend baseline medians differ >30% — validate over a 21-28 day window before applying.]"
		}
		// The threshold is the fix. computeDurationRecommendation may add a duration when the alert is
		// also transient; force the type from the diagnosis so a 0-reduction case isn't downgraded to "none".
		computeDurationRecommendation(s, alertDef, alertQuality)
		if s.SuggestedDuration > 0 {
			s.RecommendationType = "tune_both"
		} else {
			s.RecommendationType = "tune_threshold"
		}
		return s, true

	// 3) REAL EXCURSIONS — normal band is well clear of the threshold; firings are genuine departures.
	case gapFrac >= (1 - excursionFraction):
		s.Diagnosis = "excursion"
		if alertQuality != nil && alertQuality.TransientRate > 0.7 {
			s.RecommendationType = "increase_duration"
			s.Reason = fmt.Sprintf("Firings are short, self-resolving excursions above a much lower normal band (baseline p%s=%.2f, threshold %.2f). Increase the evaluation window rather than raising the threshold.",
				tailLabel(isGT), bandEdge, theta)
			computeDurationRecommendation(s, alertDef, alertQuality)
		} else {
			s.RecommendationType = "investigate_signal"
			s.Reason = fmt.Sprintf("Firings are genuine excursions well clear of the normal band (baseline p%s=%.2f, threshold %.2f) and do not resolve quickly. Raising the threshold would hide real events — investigate instead.",
				tailLabel(isGT), bandEdge, theta)
		}
		return s, true

	// 4) AMBIGUOUS ZONE — default conservative.
	default:
		s.Diagnosis = "ambiguous"
		s.RecommendationType = "review_alert"
		s.Reason = fmt.Sprintf("Baseline p%s=%.2f sits between the mis-tuned and excursion bands relative to threshold %.2f — neither clearly over-firing on normal behavior nor a clean excursion. Flagged for review with both distributions attached.",
			tailLabel(isGT), bandEdge, theta)
		return s, true
	}
}

func tailLabel(isGT bool) string {
	if isGT {
		return "95"
	}
	return "5"
}

func minOf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// selectFiringSeries picks the series that fires the most against the threshold (Phase B). Ties break
// to the most extreme peak/trough. With one series it returns it; when none violate it returns the
// most-extreme series so decomposeHistory's self-check can flag the fidelity gap.
func selectFiringSeries(seriesList []*MetricHistory, threshold float64, isGT bool) *MetricHistory {
	if len(seriesList) == 0 {
		return nil
	}
	if len(seriesList) == 1 {
		return seriesList[0]
	}
	var best *MetricHistory
	bestViol := -1
	var bestExtreme float64
	for _, s := range seriesList {
		viol, extreme := violationScore(s.Values, threshold, isGT)
		better := best == nil || viol > bestViol || (viol == bestViol && moreExtreme(extreme, bestExtreme, isGT))
		if better {
			best, bestViol, bestExtreme = s, viol, extreme
		}
	}
	return best
}

// selectSeriesForEvent picks the metric series that matches the firing event's labels, so the series
// analyzed aligns with the fingerprint's firing intervals (an alertname spans many series; the wrong
// one produces a contaminated baseline). Falls back to the top-violating series when the relay
// returned no labels or none match.
func selectSeriesForEvent(seriesList []*MetricHistory, eventLabels map[string]interface{}, threshold float64, isGT bool) *MetricHistory {
	if len(seriesList) == 0 {
		return nil
	}
	var matches []*MetricHistory
	for _, s := range seriesList {
		if len(s.Labels) > 0 && seriesMatchesEvent(s.Labels, eventLabels) {
			matches = append(matches, s)
		}
	}
	if len(matches) > 0 {
		return selectFiringSeries(matches, threshold, isGT)
	}
	return selectFiringSeries(seriesList, threshold, isGT)
}

// seriesMatchesEvent reports whether every series label is present in the event's labels with an
// equal value (series labels ⊆ event labels) — the event carries the fired series' label set plus
// alerting meta-labels, so a true subset match identifies the series that fired.
func seriesMatchesEvent(seriesLabels map[string]string, eventLabels map[string]interface{}) bool {
	if len(eventLabels) == 0 {
		return false
	}
	for k, v := range seriesLabels {
		ev, ok := eventLabels[k]
		if !ok {
			return false
		}
		if es, ok := ev.(string); !ok || es != v {
			return false
		}
	}
	return true
}

// violationScore counts how many values violate the threshold and returns the most extreme value.
func violationScore(values []float64, threshold float64, isGT bool) (int, float64) {
	viol := 0
	var extreme float64
	for i, v := range values {
		if (isGT && v > threshold) || (!isGT && v < threshold) {
			viol++
		}
		if i == 0 || moreExtreme(v, extreme, isGT) {
			extreme = v
		}
	}
	return viol, extreme
}

func moreExtreme(a, b float64, isGT bool) bool {
	if isGT {
		return a > b
	}
	return a < b
}

// violationRate returns the fraction of samples that violate the threshold.
func violationRate(sorted []float64, threshold float64, isGT bool) float64 {
	if len(sorted) == 0 {
		return 0
	}
	viol, _ := violationScore(sorted, threshold, isGT)
	return float64(viol) / float64(len(sorted))
}

// distinctCount counts distinct values in a sorted slice.
func distinctCount(sorted []float64) int {
	if len(sorted) == 0 {
		return 0
	}
	n := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1] {
			n++
		}
	}
	return n
}

// boundsFor reports whether the metric is bounded and its [lower, upper] range, combining the regex
// hints with a 0-1 ratio heuristic (threshold in (0,1] and the baseline stays within ~1.0).
func boundsFor(alertDef *AlertDefinition, baseline DistStats) (bounded bool, upper, lower float64) {
	hint := detectMetricHints(alertDef)
	if hint.isBounded {
		return true, hint.upperBound, hint.lowerBound
	}
	if alertDef.CurrentThreshold > 0 && alertDef.CurrentThreshold <= 1.0 && baseline.P95 <= 1.05 {
		return true, 1.0, 0
	}
	return false, 0, 0
}

// isHistogramSaturated reports whether a histogram_quantile() alert's firing distribution is pinned
// at the top finite bucket (p95 == p99 > 0), meaning the true value is unmeasurable past that boundary.
func isHistogramSaturated(alertDef *AlertDefinition, firing DistStats) bool {
	if alertDef == nil || firing.Count == 0 {
		return false
	}
	if !strings.Contains(strings.ToLower(alertDef.MetricName), "histogram_quantile(") {
		return false
	}
	return firing.P95 == firing.P99 && firing.P99 > 0
}
