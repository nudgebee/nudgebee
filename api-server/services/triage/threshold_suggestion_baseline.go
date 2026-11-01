package triage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/common"
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
	chronicCoverageMax       = 0.30 // baseline coverage below this ⇒ baseline covers too little of the window to trust at high confidence
	mistunedFraction         = 0.90 // |band edge − θ| ≤ (1−this)·θ ⇒ normal band reaches the threshold (mis-tuned)
	excursionFraction        = 0.70 // |band edge − θ| ≥ (1−this)·θ ⇒ firings are genuine excursions
	intervalMergeSteps       = 2    // merge firing intervals separated by fewer than N fetch steps
	baselineContaminationMax = 0.40 // fraction of OFF-ALERT baseline that may cross θ before the baseline is deemed contaminated/chronic
	corroboratedIntervalMin  = 0.50 // fraction of sampled firing intervals that must contain a θ-crossing sample
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
	// SelfCheckFailed: the events table says the alert fired, but the fetched history does not
	// corroborate those firings — the history provably does not represent what the rule evaluated
	// (wrong series / resolution / step). §1.5 of the design doc. Measured per interval, not over
	// the pooled firing samples: pooling let a single violating sample vouch for hundreds of
	// intervals, which is how histories that never cross θ reached a confident verdict.
	SelfCheckFailed bool
	// SampledIntervals / CorroboratedIntervals: firing intervals that contain at least one sample,
	// and of those, the ones containing at least one sample that crosses θ. Intervals the history
	// has no sample for at all are excluded from both — a sparse series shouldn't read as a
	// fidelity failure.
	SampledIntervals      int
	CorroboratedIntervals int
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
	if (source == "prometheus" || source == "prometheus_alertmanager_webhook" || source == "pagerduty_webhook") && fingerprint != "" {
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
	firingIdx := intervalIndexer(intervals)
	samplesPer := make([]int, len(intervals))
	violatingPer := make([]int, len(intervals))

	for i := 0; i < len(mh.Timestamps) && i < len(mh.Values); i++ {
		ts, v := mh.Timestamps[i], mh.Values[i]
		if idx := firingIdx(ts); idx >= 0 {
			firing = append(firing, v)
			samplesPer[idx]++
			if (isGT && v > threshold) || (!isGT && v < threshold) {
				violatingPer[idx]++
			}
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

	// Self-check: the events table says the alert fired, but the history does not corroborate it.
	// Per interval — an interval is corroborated when at least one of its samples crosses θ, which
	// is the minimum the rule needed to fire. Intervals with no samples at all are not counted
	// either way. Zero intervals is its own failure: without them every sample is labelled
	// "off-alert" and the band comparison below silently runs on the whole history.
	sampledIntervals, corroborated := 0, 0
	for i := range intervals {
		if samplesPer[i] == 0 {
			continue
		}
		sampledIntervals++
		if violatingPer[i] > 0 {
			corroborated++
		}
	}
	// No intervals, or intervals that the history has no sample for anywhere, are the same failure:
	// nothing was carved out, so "baseline" is the whole history.
	selfCheckFailed := sampledIntervals == 0 ||
		float64(corroborated)/float64(sampledIntervals) < corroboratedIntervalMin

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
		baselineSorted:        baseline,
		firingSorted:          firing,
		Baseline:              statsOfSorted(baseline),
		Firing:                statsOfSorted(firing),
		Coverage:              coverage,
		IntervalCount:         len(intervals),
		SelfCheckFailed:       selfCheckFailed,
		SampledIntervals:      sampledIntervals,
		CorroboratedIntervals: corroborated,
		SeasonalitySuspected:  seasonalitySuspected,
	}
}

// intervalIndexer returns a closure giving the index of the interval a unix timestamp falls in,
// or -1 when it falls in none. Intervals are assumed sorted by start (padAndMergeIntervals
// guarantees this). The index is what lets the self-check attribute samples per interval.
func intervalIndexer(intervals []firingInterval) func(int64) int {
	if len(intervals) == 0 {
		return func(int64) int { return -1 }
	}
	return func(ts int64) int {
		// Binary search for the last interval whose start <= ts.
		i := sort.Search(len(intervals), func(i int) bool { return intervals[i].start > ts })
		if i == 0 || ts > intervals[i-1].end {
			return -1
		}
		return i - 1
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

	// §1.5 self-check: history doesn't corroborate the rule's firings → abstain. Every diagnosis
	// below is a statement about how firing behaviour differs from baseline, so it needs firing
	// evidence held to the same standard as the baseline gates that follow.
	if decomp.SelfCheckFailed {
		s.RecommendationType = "insufficient_data"
		if decomp.SampledIntervals == 0 {
			s.Reason = "No firing window for this alert falls inside the 7-day metric history, so the history cannot be split into off-alert and firing behaviour — every sample would be labelled off-alert and any threshold derived from it would describe a period the alert never fired in. Abstaining."
		} else {
			s.Reason = fmt.Sprintf("Only %d of %d firing windows contain a metric sample that crosses the threshold — the history does not represent the series/resolution the rule evaluated. Abstaining until fidelity is fixed.",
				decomp.CorroboratedIntervals, decomp.SampledIntervals)
		}
		return s, true
	}

	// Not enough clean off-alert data to define a baseline.
	if decomp.Baseline.Count < minThresholdSamples {
		s.RecommendationType = "insufficient_data"
		s.Reason = fmt.Sprintf("Only %d off-alert samples (need at least %d) — insufficient clean baseline to tune reliably.", decomp.Baseline.Count, minThresholdSamples)
		return s, true
	}

	// Phase D — measurability gate: a histogram_quantile() alert whose values pile up on the largest
	// finite bucket boundary is saturated there. Any threshold derived from it is invented; abstain.
	if saturated, ceiling := isHistogramSaturated(alertDef, full); saturated {
		s.RecommendationType = "insufficient_data"
		s.Reason = histogramSaturatedReason(ceiling)
		return s, true
	}

	// 1) CHRONIC — the metric sits past θ even when the alert is NOT firing.
	//
	// This used to be decided on coverage (the share of the window the alert spent firing), but
	// coverage describes the alert's firing pattern, not the threshold: a node genuinely pegged at
	// 100% CPU for six days has low coverage AND a clean baseline far below θ (measured on prod:
	// NodeCPUHighUsage, baseline p50=19.26 against θ=90), and a coverage verdict sent it to
	// "investigate, do not retune" while alerts whose threshold simply sits inside the metric's
	// normal band got the same answer and no number (#38804). What separates the two is whether
	// the OFF-ALERT baseline itself crosses θ — so that is the test, and coverage only caps
	// confidence further down. Low-coverage alerts with a clean baseline now fall through to the
	// band comparison, which reads them as excursions on their own evidence.
	if blViol := violationRate(decomp.baselineSorted, theta, isGT); blViol > baselineContaminationMax {
		s.Diagnosis = "chronic"
		s.RecommendationType = "investigate_signal"

		// Pinned at the metric's own failure bound (100% of requests failing, a ratio at 1.0):
		// no threshold outside the normal band exists, because the normal band ends at the bound.
		bounded, upper, lower := boundsFor(alertDef, decomp.Baseline)
		if bounded && ((isGT && decomp.Baseline.P50 >= upper-(upper-lower)*0.05) ||
			(isLT && decomp.Baseline.P50 <= lower+(upper-lower)*0.05)) {
			s.Reason = fmt.Sprintf("Metric is pinned at its failure bound (baseline p50=%.3g; %.0f%% of off-alert samples cross the threshold) — a persistent failure state, not noise. No threshold outside the metric's own range exists. Investigate; do not retune.",
				decomp.Baseline.P50, blViol*100)
			return s, true
		}

		// Otherwise there IS headroom, and statistics alone cannot say whether the system is
		// persistently degraded or the threshold was set inside normal behaviour — the same shape
		// covers "100% of requests are 4xx" and "normal load average is 4.9 against a threshold of
		// 2". So state both readings and name the value that would sit outside the observed normal
		// band, instead of returning no number at all. The type stays investigate_signal: it keeps
		// the alert on the tuning list, and applying the value takes an explicit accept (the apply
		// path promotes a user-supplied threshold to tune_threshold).
		// Derived from the WHOLE observed history, not the off-alert baseline. Reaching this branch
		// means the baseline itself crosses θ most of the time, so there is no clean off-alert
		// normal to re-baseline against — the metric's actual operating range is what the full
		// history shows. (The one shape where that would be wrong, a clean baseline far from θ with
		// a long firing window, no longer arrives here: its baseline violation rate is low, so it
		// reads as an excursion.)
		fMed := computeMedian(full)
		fMad := computeMAD(full)
		var outside float64
		var method string
		if isGT {
			outside, method = computeUpperThreshold(full, fMed, fMad, percentile(full, 95))
			outside = applySanityCap(outside, theta, percentile(full, 99))
		} else {
			outside, method = computeLowerThreshold(full, fMed, fMad)
			if floor := minOf(theta*0.1, percentile(full, 1)); outside < floor {
				outside = floor
			}
		}
		if (isGT && outside > theta) || (isLT && outside < theta) {
			s.SuggestedThreshold = roundTo(outside, 2)
			s.Method = method + "-chronic"
			s.EstimatedReduction = estimateReduction(full, theta, s.SuggestedThreshold, isGT)
			s.Reason = fmt.Sprintf("Threshold %.3g sits inside the metric's normal operating range: %.0f%% of off-alert samples already cross it (baseline p50=%.3g, observed p50=%.3g). Either the system is persistently degraded, or the threshold was set inside normal behaviour — the evidence cannot tell those apart. %.3g is the nearest value outside the observed range (~%.0f%% fewer firings); confirm the current level is acceptable before applying it.",
				theta, blViol*100, decomp.Baseline.P50, s.MetricP50, s.SuggestedThreshold, s.EstimatedReduction)
			return s, true
		}
		s.Reason = fmt.Sprintf("Threshold %.3g sits inside the metric's normal operating range: %.0f%% of off-alert samples already cross it (baseline p50=%.3g), and the observed distribution is too spread out to place a threshold clear of it. Review the alert definition rather than the number.",
			theta, blViol*100, decomp.Baseline.P50)
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
		// Coverage is no longer a verdict, but it still bounds how much the baseline can be trusted:
		// a baseline drawn from a sliver of the window describes less of normal behaviour than the
		// sample count alone suggests.
		if decomp.Coverage < chronicCoverageMax && s.Confidence == "high" {
			s.Confidence = "medium"
		}
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
// returned no labels or none match. The event's "pod" label is the workload's name once a workload
// matched, so the labels are read through common.SeriesMatchLabels — the same view the alert chart
// uses to pick its series.
func selectSeriesForEvent(seriesList []*MetricHistory, eventLabels map[string]interface{}, threshold float64, isGT bool) *MetricHistory {
	if len(seriesList) == 0 {
		return nil
	}
	eventLabels = common.SeriesMatchLabels(eventLabels)
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

// histogramSaturationShare is the fraction of samples that must sit exactly at the largest observed
// value before a histogram_quantile() series is treated as pinned at its top finite bucket. Set
// deliberately low: abstaining when we needn't costs one un-tuned suggestion, while the opposite
// error publishes an invented number as confident advice — the failure this gate exists to stop.
const histogramSaturationShare = 0.01

// isHistogramSaturated reports whether a histogram_quantile() alert's observed values are pinned at
// the top finite bucket, and returns that ceiling. histogram_quantile() cannot interpolate into
// +Inf, so every observation past the largest finite `le` collapses onto that boundary: the true
// distribution is unmeasurable there, and any threshold derived from it is invented.
//
// The signature is a repeated maximum. A continuous quantile does not keep returning the identical
// value; a clamped one returns nothing else. (A wholly flat series scores share 1.0 and is likewise
// untunable, so it correctly abstains too.)
//
// This replaces an earlier `firing.P95 == firing.P99` test that only caught TOTAL saturation. The
// dev HighP95Latency rule is partially saturated — firing p95=9.3 vs p99=10, baseline p95=7.75 vs
// p99=10, ceiling 10 in every band — so it passed that test and was published as "safe / medium
// confidence, raise 5 → 7.1", a number read straight off bucket interpolation. Testing the whole
// history rather than the firing band alone is what closes the gap: the ceiling is a property of
// the metric's bucket layout, not of any one band.
func isHistogramSaturated(alertDef *AlertDefinition, samples []float64) (bool, float64) {
	if alertDef == nil || len(samples) == 0 {
		return false, 0
	}
	if !strings.Contains(strings.ToLower(alertDef.MetricName), "histogram_quantile(") {
		return false, 0
	}
	ceiling := 0.0
	for _, v := range samples {
		if v > ceiling {
			ceiling = v
		}
	}
	if ceiling <= 0 {
		return false, 0
	}
	atCeiling := 0
	for _, v := range samples {
		if v == ceiling {
			atCeiling++
		}
	}
	return float64(atCeiling)/float64(len(samples)) >= histogramSaturationShare, ceiling
}

// histogramSaturatedReason is shared by both tuner paths so the wording can't drift apart.
func histogramSaturatedReason(ceiling float64) string {
	return fmt.Sprintf("Latency histogram is saturated at ~%.2f — its largest finite bucket. histogram_quantile() cannot measure past that boundary, so the values this alert fires on are bucket interpolation rather than measurements and no threshold can be derived from them. Add higher histogram buckets, or convert this alert to a burn-rate/SLO form.", ceiling)
}
