package triage

import "testing"

// mkHistory builds a MetricHistory with evenly-spaced timestamps at the given step.
func mkHistory(step int, values []float64) *MetricHistory {
	ts := make([]int64, len(values))
	base := int64(1_700_000_000)
	for i := range values {
		ts[i] = base + int64(i*step)
	}
	return &MetricHistory{Timestamps: ts, Values: values, Step: step}
}

// band returns n values centered on `center` spread within ±jitter (deterministic).
func band(n int, center, jitter float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = center + jitter*float64(i%5-2)/2.0 // i%5-2 ∈ {-2,-1,0,1,2}
	}
	return out
}

func idxInterval(h *MetricHistory, i0, i1 int) firingInterval {
	return firingInterval{start: h.Timestamps[i0], end: h.Timestamps[i1]}
}

func concat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestPadAndMergeIntervals_MergesAdjacent(t *testing.T) {
	step := 60
	ad := &AlertDefinition{} // no for-duration ⇒ pad = 2 steps
	// two intervals 60s apart (< 2-step gap after padding) should merge into one
	in := []firingInterval{{start: 1000, end: 1100}, {start: 1160, end: 1300}}
	out := padAndMergeIntervals(in, ad, step)
	if len(out) != 1 {
		t.Fatalf("expected 1 merged interval, got %d: %+v", len(out), out)
	}
	if out[0].end != 1300 {
		t.Errorf("merged end should be 1300, got %d", out[0].end)
	}
	if out[0].start != 1000-int64(2*step) {
		t.Errorf("start should be padded back by 2 steps, got %d", out[0].start)
	}
}

func TestDiagnose_MistunedGT(t *testing.T) {
	// Normal band (p95≈97) reaches threshold 100 ⇒ mis-tuned ⇒ retune off baseline.
	h := mkHistory(60, concat(band(50, 90, 8), band(10, 120, 5)))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.Diagnosis != "mistuned" || s.RecommendationType != "tune_threshold" {
		t.Fatalf("got diagnosis=%q type=%q reason=%q", s.Diagnosis, s.RecommendationType, s.Reason)
	}
	if s.SuggestedThreshold <= 100 {
		t.Errorf("mis-tuned GT should raise threshold above 100, got %.2f", s.SuggestedThreshold)
	}
	if s.Baseline == nil || s.Firing == nil {
		t.Error("baseline/firing distributions should be attached")
	}
}

func TestDiagnose_ChronicPinned(t *testing.T) {
	// Metric pinned high (~150 > θ) most of the window ⇒ chronic ⇒ investigate, never retune.
	h := mkHistory(60, concat(band(25, 150, 4), band(75, 150, 4)))
	iv := idxInterval(h, 25, 99)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.Diagnosis != "chronic" || s.RecommendationType != "investigate_signal" {
		t.Fatalf("got diagnosis=%q type=%q coverage=%.2f", s.Diagnosis, s.RecommendationType, s.BaselineCoverage)
	}
	if s.SuggestedThreshold != 100 {
		t.Errorf("chronic must not change the threshold, got %.2f", s.SuggestedThreshold)
	}
}

func TestDiagnose_ExcursionInvestigate(t *testing.T) {
	// Normal band ~20 well below θ=100; firings are real excursions; not transient ⇒ investigate.
	h := mkHistory(60, concat(band(50, 20, 6), band(10, 150, 5)))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.Diagnosis != "excursion" || s.RecommendationType != "investigate_signal" {
		t.Fatalf("got diagnosis=%q type=%q", s.Diagnosis, s.RecommendationType)
	}
	if s.SuggestedThreshold != 100 {
		t.Errorf("excursion must not raise the threshold, got %.2f", s.SuggestedThreshold)
	}
}

func TestDiagnose_ExcursionTransientIncreaseDuration(t *testing.T) {
	// Same excursion shape but transient (self-resolving) ⇒ increase the evaluation window.
	h := mkHistory(60, concat(band(50, 20, 6), band(10, 150, 5)))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	aq := &AlertQualityScore{TransientRate: 0.85, DurationP90: 120}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, aq)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.Diagnosis != "excursion" || s.RecommendationType != "increase_duration" {
		t.Fatalf("got diagnosis=%q type=%q", s.Diagnosis, s.RecommendationType)
	}
}

func TestDiagnose_SelfCheckFailure(t *testing.T) {
	// Events fired (interval present) but no sample in the firing window crosses θ=100 ⇒ the fetched
	// history isn't what the rule evaluated ⇒ abstain.
	h := mkHistory(60, band(60, 25, 5)) // everything ~25, never near 100
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.RecommendationType != "insufficient_data" {
		t.Fatalf("expected insufficient_data on self-check failure, got %q (reason=%q)", s.RecommendationType, s.Reason)
	}
}

func TestDiagnose_MistunedLT_CacheHitShape(t *testing.T) {
	// PostgreSQLCacheHitRatio shape: normal ~0.99 with a lower tail that reaches the alert
	// threshold < 0.98 ⇒ mis-tuned ⇒ lower the threshold.
	h := mkHistory(60, concat(band(50, 0.99, 0.015), band(10, 0.96, 0.004)))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: "<", CurrentThreshold: 0.98}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.Diagnosis != "mistuned" || s.RecommendationType != "tune_threshold" {
		t.Fatalf("got diagnosis=%q type=%q reason=%q", s.Diagnosis, s.RecommendationType, s.Reason)
	}
	if s.SuggestedThreshold >= 0.98 {
		t.Errorf("mis-tuned LT should lower the threshold below 0.98, got %.4f", s.SuggestedThreshold)
	}
}

func TestSelectFiringSeries_PicksMostViolating(t *testing.T) {
	quiet := &MetricHistory{Values: []float64{10, 20, 30}}   // 0 violations of θ=100
	one := &MetricHistory{Values: []float64{110, 90, 95}}    // 1 violation
	noisy := &MetricHistory{Values: []float64{50, 150, 160}} // 2 violations
	if got := selectFiringSeries([]*MetricHistory{quiet, one, noisy}, 100, true); got != noisy {
		t.Error("expected the most-violating series to be selected (GT)")
	}
}

func TestSelectFiringSeries_LTPicksLowest(t *testing.T) {
	high := &MetricHistory{Values: []float64{0.99, 0.995}} // 0 violations of <0.98
	low := &MetricHistory{Values: []float64{0.97, 0.96}}   // 2 violations
	if got := selectFiringSeries([]*MetricHistory{high, low}, 0.98, false); got != low {
		t.Error("expected the most-violating series to be selected (LT)")
	}
}

func TestSelectSeriesForEvent_MatchesLabels(t *testing.T) {
	pg := &MetricHistory{Labels: map[string]string{"datname": "postgres"}, Values: []float64{0.99, 0.99}}
	tv := &MetricHistory{Labels: map[string]string{"datname": "temporal_visibility"}, Values: []float64{0.97, 0.96}}
	event := map[string]interface{}{"datname": "temporal_visibility", "alertname": "PostgreSQLCacheHitRatio", "severity": "warning"}
	// The event fired for temporal_visibility — analyze that series, not the healthier postgres one.
	if got := selectSeriesForEvent([]*MetricHistory{pg, tv}, event, 0.98, false); got != tv {
		t.Error("expected the series whose labels match the event (temporal_visibility)")
	}
	// No labels on the series → fall back to top-violating (LT ⇒ lowest value).
	noLabels := []*MetricHistory{{Values: []float64{0.99}}, {Values: []float64{0.95}}}
	if got := selectSeriesForEvent(noLabels, event, 0.98, false); got != noLabels[1] {
		t.Error("expected fallback to the top-violating series when no labels are present")
	}
}

func TestIsHistogramSaturated(t *testing.T) {
	adH := &AlertDefinition{MetricName: "histogram_quantile(0.95, sum by (le) (rate(x_bucket[5m])))"}
	adG := &AlertDefinition{MetricName: "rate(errors_total[5m])"}
	sat := DistStats{Count: 100, P95: 600, P99: 600}
	unsat := DistStats{Count: 100, P95: 400, P99: 600}
	if !isHistogramSaturated(adH, sat) {
		t.Error("histogram_quantile with p95==p99 should be saturated")
	}
	if isHistogramSaturated(adH, unsat) {
		t.Error("p95 != p99 is not saturated")
	}
	if isHistogramSaturated(adG, sat) {
		t.Error("non-histogram metric is never saturated")
	}
	if isHistogramSaturated(adH, DistStats{Count: 0}) {
		t.Error("empty firing distribution is not saturated")
	}
}

func TestDiagnose_HistogramSaturatedAbstains(t *testing.T) {
	// histogram_quantile alert whose firing window is pinned at the top bucket (600) ⇒ abstain.
	h := mkHistory(60, concat(band(50, 300, 40), band(10, 600, 0)))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 300,
		MetricName: "histogram_quantile(0.95, sum by (le,namespace) (rate(lat_bucket[5m])))"}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.RecommendationType != "insufficient_data" {
		t.Fatalf("expected insufficient_data on histogram saturation, got %q (diag=%q)", s.RecommendationType, s.Diagnosis)
	}
}

func TestDiagnose_PinnedRatioIsChronic(t *testing.T) {
	// Error ratio pinned at 1.0 with θ=0.05: nearly all off-alert samples cross θ and the metric
	// sits at its ceiling ⇒ chronic (investigate), NOT a raise to 0.95.
	h := mkHistory(60, concat(band(50, 1.0, 0.02), band(10, 1.0, 0.0)))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 0.05, MetricName: "sum(rate(errors[5m]))/sum(rate(total[5m]))"}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.Diagnosis != "chronic" || s.RecommendationType != "investigate_signal" {
		t.Fatalf("expected chronic/investigate for a pinned ratio, got diag=%q type=%q", s.Diagnosis, s.RecommendationType)
	}
	if s.SuggestedThreshold != 0.05 {
		t.Errorf("pinned ratio must not retune, got %.3g", s.SuggestedThreshold)
	}
}

func TestDiagnose_MistunedWrongDirectionFlagged(t *testing.T) {
	// Skewed baseline: median ~0.95 (well below θ=2) with only the tail reaching θ. med+3·MAD lands
	// below θ, so a "re-baseline" would LOWER the > threshold and INCREASE firing — must be flagged
	// for review, not emitted as a firing-increasing tune (the real NodeSystemSaturation-style bug).
	var base []float64
	for i := 0; i < 46; i++ {
		base = append(base, 0.85+0.1*float64(i%3)) // ~0.85–1.05
	}
	for i := 0; i < 4; i++ {
		base = append(base, 1.9) // tail near θ=2
	}
	fire := []float64{2.5, 2.6, 2.4, 2.5, 2.6, 2.4, 2.5, 2.6, 2.4, 2.5} // above θ
	h := mkHistory(60, concat(base, fire))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 2}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.RecommendationType == "tune_threshold" || s.SuggestedThreshold < 2 {
		t.Fatalf("a wrong-direction re-baseline must not tune a > threshold down; got type=%q sug=%.3g", s.RecommendationType, s.SuggestedThreshold)
	}
	if s.EstimatedReduction < 0 {
		t.Errorf("must not emit a negative (firing-increasing) reduction, got %.0f", s.EstimatedReduction)
	}
}

func TestDiagnose_ContaminatedBaselineAbstains(t *testing.T) {
	// >40% of off-alert samples cross θ (unbounded, not pinned) ⇒ the segmentation is dirty ⇒ abstain
	// instead of tuning off a contaminated baseline.
	h := mkHistory(60, concat(band(50, 120, 60), band(10, 300, 20)))
	iv := idxInterval(h, 50, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.RecommendationType != "insufficient_data" {
		t.Fatalf("expected contamination abstain, got %q (diag=%q)", s.RecommendationType, s.Diagnosis)
	}
}

func TestDiagnose_BooleanMetricFlagged(t *testing.T) {
	// A 0/1 metric has ≤2 distinct values — not tunable with a numeric threshold ⇒ review_alert.
	vals := make([]float64, 60)
	for i := range vals {
		if i%3 == 0 {
			vals[i] = 1
		}
	}
	h := mkHistory(60, vals)
	iv := idxInterval(h, 55, 59)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 0.5}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.RecommendationType != "review_alert" {
		t.Fatalf("expected review_alert for a binary metric, got %q (diag=%q)", s.RecommendationType, s.Diagnosis)
	}
}

func TestDiagnose_FallsBackOnUnknownOperator(t *testing.T) {
	h := mkHistory(60, band(60, 50, 10))
	ad := &AlertDefinition{Operator: "", CurrentThreshold: 100}
	if _, ok := diagnoseWithBaseline(ad, h, nil, nil); ok {
		t.Error("expected fallback (ok=false) for unknown operator")
	}
}

func TestDiagnose_InsufficientBaseline(t *testing.T) {
	// Fewer than minThresholdSamples off-alert points ⇒ abstain.
	h := mkHistory(60, concat(band(10, 90, 5), band(5, 120, 3)))
	iv := idxInterval(h, 10, 14)
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	s, ok := diagnoseWithBaseline(ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatal("expected baseline path to handle")
	}
	if s.RecommendationType != "insufficient_data" {
		t.Fatalf("expected insufficient_data with tiny baseline, got %q", s.RecommendationType)
	}
}
