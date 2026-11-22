package triage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ramp returns n values evenly spaced from a to b — a deterministic stand-in for a spread-out
// distribution.
func ramp(n int, a, b float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		if n == 1 {
			out[i] = a
			continue
		}
		out[i] = a + (b-a)*float64(i)/float64(n-1)
	}
	return out
}

func repeat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// verdictCase is one real rule shape and what Alert Tuning may say about it. offAlert samples come
// first in the history; the firing window covers everything after them.
type verdictCase struct {
	name       string
	ad         AlertDefinition
	offAlert   []float64
	firing     []float64
	wantType   string
	wantNumber bool
	wantRisk   string // checked only when set
	wantInWhy  string // substring the reason or a risk warning must carry, when set
}

// The rule shapes below are the ones this change was reviewed against (#38804): two worth tuning,
// six where a number would silence a real failure or change nothing, plus the shapes found in the
// review that followed. Each history reproduces a distribution's shape: where the metric sits, how
// wide it is, and where its extreme is.
func verdictCases() []verdictCase {
	return []verdictCase{
		{
			// Load-like metric sitting well above its threshold all week, with a long tail. The value
			// clear of the normal range is one the metric still reached: worth offering, for review.
			name:       "NodeDiskIOSaturation: level metric above its threshold, long tail",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 10, MetricName: `rate(node_disk_io_time_weighted_seconds_total{job="node-exporter"}[5m])`},
			offAlert:   ramp(40, 12, 60),
			firing:     concat(ramp(120, 10.5, 50), ramp(30, 50, 90), ramp(10, 90, 140)),
			wantType:   "tune_threshold",
			wantNumber: true,
			wantRisk:   "review",
		},
		{
			// Cache hit ratio hovering at its "< 0.98" threshold with occasional dips: the ticket's own
			// example of a threshold set inside normal behaviour.
			name:       "PostgreSQLCacheHitRatio: ratio hovering at its threshold",
			ad:         AlertDefinition{Operator: "<", CurrentThreshold: 0.98, MetricName: `avg by (datname) (rate(pg_stat_database_blks_hit[5m]) / (rate(pg_stat_database_blks_hit[5m]) + rate(pg_stat_database_blks_read[5m])))`},
			offAlert:   band(25, 0.975, 0.01),
			firing:     concat(band(600, 0.97, 0.01), band(50, 0.92, 0.02)),
			wantType:   "tune_threshold",
			wantNumber: true,
			wantRisk:   "review",
		},
		{
			// A third of targets down is a failure, not noise. The value clear of the "normal" range
			// lands near the top of a percentage: alerting only when almost everything is down.
			name:       "TargetDown: percentage of targets down, value lands near the ceiling",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 10, MetricName: `100 * (count by (cluster, job, namespace, service) (up == 0) / count by (cluster, job, namespace, service) (up))`},
			offAlert:   ramp(40, 8, 30),
			firing:     concat(ramp(150, 12, 62), repeat(10, 100)),
			wantType:   "investigate_signal",
			wantNumber: false,
			wantInWhy:  "suppress",
		},
		{
			// Notifications failing 85% of the time. The value clear of the range is 1.0, the ceiling
			// of a ratio: a rule that can never fire.
			name:       "AlertmanagerFailedToSendAlerts: failure ratio, value is the ceiling",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 0.01, MetricName: `rate(alertmanager_notifications_failed_total[5m]) / rate(alertmanager_notifications_total[5m])`},
			offAlert:   ramp(40, 0.7, 1.0),
			firing:     concat(ramp(140, 0.62, 0.98), repeat(20, 1.0)),
			wantType:   "investigate_signal",
			wantNumber: false,
		},
		{
			// A job's age only grows. The value clear of the range is the oldest age seen, which the
			// job passes again within a day.
			name:       "KubeJobNotCompleted: age that only grows, value is the observed maximum",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 43200, MetricName: `time() - max by (namespace, job_name) (kube_job_status_start_time{job="kube-state-metrics"})`},
			offAlert:   ramp(40, 1.60e6, 1.72e6),
			firing:     ramp(160, 1.72e6, 2.24e6),
			wantType:   "investigate_signal",
			wantNumber: false,
		},
		{
			// Seconds until a certificate expires, already negative: the certificates have expired.
			// The value clear of the range is further below zero.
			name:       "NGINXCertificateExpiry: time to expiry already negative",
			ad:         AlertDefinition{Operator: "<", CurrentThreshold: 604800, MetricName: `avg by (host) (nginx_ingress_controller_ssl_expire_time_seconds) - time()`},
			offAlert:   ramp(40, -100000, -230000),
			firing:     ramp(160, -230000, -760000),
			wantType:   "investigate_signal",
			wantNumber: false,
		},
		{
			// The autoscaler sits at its maximum of 8 replicas: a constant.
			name:       "KubeHpaMaxedOut: constant metric",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 1, MetricName: `kube_horizontalpodautoscaler_status_current_replicas`},
			offAlert:   repeat(40, 8),
			firing:     repeat(160, 8),
			wantType:   "investigate_signal",
			wantNumber: false,
		},
		{
			// Memory sitting at a constant 89% of its limit against an 80% threshold.
			name:       "PodMemoryReachingLimit: constant ratio above its threshold",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 0.8, MetricName: `container_memory_working_set_bytes / container_spec_memory_limit_bytes`},
			offAlert:   repeat(40, 0.89),
			firing:     repeat(160, 0.89),
			wantType:   "investigate_signal",
			wantNumber: false,
		},
		{
			// Normal band just reaches the threshold; the re-baselined value (about 2.1 against 2)
			// removes a few percent of firings. Not worth changing a rule for.
			name:       "NodeSystemSaturation: re-baseline that barely moves firings",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 2, MetricName: `node_load1 / count without (cpu, mode) (node_cpu_seconds_total{mode="idle"})`},
			offAlert:   concat(repeat(8, 2.01), ramp(92, 0.23, 1.67)),
			firing:     concat(ramp(20, 0.8, 1.9), ramp(90, 2.3, 4)),
			wantType:   "none",
			wantNumber: false,
			wantInWhy:  "only about",
		},
		{
			// A queue that is empty except in bursts: more than half the samples are zero, so the
			// suggested value is read from the bursts alone. Offered, but never as clean advice.
			name:       "RabbitmqTooManyReadyMessages: empty queue with bursts",
			ad:         AlertDefinition{Operator: ">", CurrentThreshold: 100, MetricName: `sum by (queue, pod, namespace) (rabbitmq_queue_messages_ready{namespace="rabbit"})`},
			offAlert:   concat(ramp(17, 120, 1900), repeat(175, 0)),
			firing:     ramp(20, 2100, 2600),
			wantType:   "tune_threshold",
			wantNumber: true,
			wantRisk:   "review",
			wantInWhy:  "single value",
		},
		{
			// Crash-looping: the metric reads 1 while the pod is in back-off and the rule fires on 1.
			name:       "KubePodCrashLooping: yes/no condition",
			ad:         AlertDefinition{Operator: ">=", CurrentThreshold: 1, MetricName: `max_over_time(kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff"}[5m])`},
			offAlert:   repeat(30, 0),
			firing:     repeat(60, 1),
			wantType:   "not_eligible",
			wantNumber: false,
			wantInWhy:  "yes/no",
		},
	}
}

func runVerdictCase(t *testing.T, c verdictCase) *ThresholdSuggestion {
	t.Helper()
	h := mkHistory(60, concat(c.offAlert, c.firing))
	iv := idxInterval(h, len(c.offAlert), len(h.Values)-1)
	ad := c.ad
	s, ok := diagnoseWithBaseline(&ad, h, []firingInterval{iv}, nil)
	if !ok {
		t.Fatalf("%s: expected the baseline path to handle this shape", c.name)
	}
	finalizeSuggestion(s, &ad, nil, h)
	return s
}

func TestVerdict_ReviewedRules(t *testing.T) {
	for _, c := range verdictCases() {
		t.Run(c.name, func(t *testing.T) {
			s := runVerdictCase(t, c)
			theta := c.ad.CurrentThreshold
			if s.RecommendationType != c.wantType {
				t.Fatalf("type = %q, want %q (suggested=%.6g reduction=%.0f%% risk=%q reason=%s warnings=%v)",
					s.RecommendationType, c.wantType, s.SuggestedThreshold, s.EstimatedReduction, s.RiskLevel, s.Reason, s.RiskWarnings)
			}
			if hasNumber := s.SuggestedThreshold != theta; hasNumber != c.wantNumber {
				t.Fatalf("offers a new value = %v (%.6g against %.6g), want %v — reason=%s",
					hasNumber, s.SuggestedThreshold, theta, c.wantNumber, s.Reason)
			}
			if c.wantRisk != "" && s.RiskLevel != c.wantRisk {
				t.Errorf("risk = %q, want %q (warnings=%v)", s.RiskLevel, c.wantRisk, s.RiskWarnings)
			}
			if c.wantInWhy != "" {
				why := s.Reason + " " + strings.Join(s.RiskWarnings, " ")
				if !strings.Contains(why, c.wantInWhy) {
					t.Errorf("explanation should mention %q, got: %s", c.wantInWhy, why)
				}
			}
			if c.wantNumber {
				// A value that is offered must loosen the rule, stay on the same side of zero, and
				// be one the metric still reached.
				gt := isGreaterThanOperator(c.ad.Operator)
				if (gt && s.SuggestedThreshold <= theta) || (!gt && s.SuggestedThreshold >= theta) {
					t.Errorf("offered value %.6g does not loosen %s %.6g", s.SuggestedThreshold, c.ad.Operator, theta)
				}
				if (theta > 0) != (s.SuggestedThreshold > 0) {
					t.Errorf("offered value %.6g flips the sign of %.6g", s.SuggestedThreshold, theta)
				}
				if s.EstimatedReduction < minMeaningfulReduction || s.EstimatedReduction >= 100 {
					t.Errorf("offered value must remove some firings but not all, got %.0f%%", s.EstimatedReduction)
				}
			}
		})
	}
}

// The rule every reader relies on: a suggestion differs from the current threshold only when its
// type is a tuning, and a tuning always says how risky it is.
func TestVerdict_NumberOnlyOnATuning(t *testing.T) {
	for _, c := range verdictCases() {
		s := runVerdictCase(t, c)
		if s.SuggestedThreshold == c.ad.CurrentThreshold {
			if s.EstimatedReduction != 0 {
				t.Errorf("%s: no new value but a %.0f%% reduction is claimed", c.name, s.EstimatedReduction)
			}
			continue
		}
		if !offersNumber(s.RecommendationType) {
			t.Errorf("%s: type %q carries a new value (%.6g)", c.name, s.RecommendationType, s.SuggestedThreshold)
		}
		if s.RiskLevel == "" {
			t.Errorf("%s: a tuning must carry a risk level", c.name)
		}
	}
}

func TestVerdict_LegacyReviewRowLosesItsNumber(t *testing.T) {
	// The statistical path (no baseline diagnosis) proposes 500 against 100, which would remove 98%
	// of firings. The gate sends it to review; the value must not travel with it.
	h := mkHistory(60, concat(ramp(98, 110, 480), repeat(2, 900)))
	ad := &AlertDefinition{Operator: ">", CurrentThreshold: 100}
	s := &ThresholdSuggestion{SuggestedThreshold: 500, EstimatedReduction: 98, RecommendationType: "tune_threshold", Confidence: "medium", Method: "MAD"}
	finalizeSuggestion(s, ad, nil, h)
	if s.RecommendationType != "review_alert" {
		t.Fatalf("type = %q, want review_alert", s.RecommendationType)
	}
	if s.SuggestedThreshold != 100 || s.EstimatedReduction != 0 {
		t.Errorf("a review row must keep the current threshold, got %.6g (%.0f%%)", s.SuggestedThreshold, s.EstimatedReduction)
	}
}

func TestVerdict_RefusedRebaselineIsNotNamedInTheReason(t *testing.T) {
	// A churn ratio that is zero except in bursts up to 1.0, against "> 0.5". The re-baseline lands
	// on 1.1, above a ratio's ceiling, and is refused. The explanation must not go on to name it.
	c := verdictCase{
		ad:       AlertDefinition{Operator: ">", CurrentThreshold: 0.5, MetricName: `sum(rate(invalidations[5m])) / sum(rate(lookups[5m]))`},
		offAlert: concat(repeat(17, 1.0), repeat(175, 0)),
		firing:   ramp(20, 0.8, 1.0),
	}
	s := runVerdictCase(t, c)
	if s.RecommendationType != "review_alert" || s.SuggestedThreshold != 0.5 {
		t.Fatalf("expected a refused re-baseline, got type=%q suggested=%.4g reason=%s", s.RecommendationType, s.SuggestedThreshold, s.Reason)
	}
	if strings.Contains(s.Reason, "Re-baseline to") || !strings.Contains(s.Reason, "No new value is offered") {
		t.Errorf("a refused value must not be named in the reason: %s", s.Reason)
	}
}

func TestValidateSuggestion_SignFlipIsDangerous(t *testing.T) {
	ad := &AlertDefinition{Operator: "<", CurrentThreshold: 604800}
	s := &ThresholdSuggestion{SuggestedThreshold: -158883, EstimatedReduction: 40, RecommendationType: "tune_threshold", MetricP50: 700000, Method: "MAD"}
	risk := ValidateSuggestion(s, ad, nil)
	if risk.Level != "dangerous" || s.RecommendationType != "review_alert" {
		t.Fatalf("a value on the other side of zero must be refused: risk=%q type=%q", risk.Level, s.RecommendationType)
	}
}

func TestCheckThresholdEligibility_EqualityQuery(t *testing.T) {
	hpa := &AlertDefinition{
		Operator: ">", CurrentThreshold: 1,
		MetricName: `kube_horizontalpodautoscaler_status_current_replicas{job="kube-state-metrics"} == kube_horizontalpodautoscaler_spec_max_replicas{job="kube-state-metrics"}`,
	}
	if reason := checkThresholdEligibility(hpa); !strings.Contains(reason, "no threshold to tune") {
		t.Errorf(`"current == max > 1" watches a state and must not be tuned, got %q`, reason)
	}
	// "==" inside a percentage is not a state check.
	targetDown := &AlertDefinition{
		Operator: ">", CurrentThreshold: 10,
		MetricName: `100 * (count by (job) (up == 0) / count by (job) (up))`,
	}
	if reason := checkThresholdEligibility(targetDown); reason != "" {
		t.Errorf("a percentage that contains == is still a level, got %q", reason)
	}
	// A cloud metric name is not PromQL and is never an equality query.
	if isEqualityQuery("Available Memory Bytes") || isEqualityQuery("CPUUtilization") {
		t.Error("a cloud metric name must not read as an equality query")
	}
}

func TestIsStateAlert(t *testing.T) {
	onOff := []float64{0, 1, 1, 0, 1}
	if !isStateAlert(&AlertDefinition{Operator: ">=", CurrentThreshold: 1}, onOff) {
		t.Error(">= 1 on a 0/1 metric is a yes/no condition")
	}
	// Restart counts go above 1: ">= 1" is a real level there.
	if isStateAlert(&AlertDefinition{Operator: ">=", CurrentThreshold: 1}, []float64{0, 1, 3, 5}) {
		t.Error(">= 1 on a count is not a yes/no condition")
	}
	// An error ratio pinned at 1.0 against "> 0.05" stays with the pinned-at-failure verdict.
	if isStateAlert(&AlertDefinition{Operator: ">", CurrentThreshold: 0.05}, []float64{1, 1, 1}) {
		t.Error("a ratio threshold below 1 is not a yes/no condition")
	}
}

func TestNormalizeStoredNumber(t *testing.T) {
	cur, sug, red := 10.0, 93.81, 98.0
	// A row stored before the rule existed: "investigate" with a value.
	gotSug, gotRed := normalizeStoredNumber("investigate_signal", &cur, &sug, &red)
	if gotSug == nil || *gotSug != cur || gotRed != nil {
		t.Errorf("an investigate row must be served with the current threshold and no reduction")
	}
	// A row with no type is not a tuning either.
	if gotSug, _ := normalizeStoredNumber("", &cur, &sug, &red); gotSug == nil || *gotSug != cur {
		t.Errorf("an untyped row must be served with the current threshold")
	}
	if gotSug, gotRed := normalizeStoredNumber("tune_threshold", &cur, &sug, &red); *gotSug != sug || *gotRed != red {
		t.Errorf("a tuning keeps its value and reduction")
	}
}

func TestNormalizeStoredReason(t *testing.T) {
	stored := "Threshold 10 sits inside the metric's normal operating range: 59% of off-alert samples already cross it (baseline p50=12.5, observed p50=35.3). Either the system is persistently degraded, or the threshold was set inside normal behaviour — the evidence cannot tell those apart. 93.8 is the nearest value outside the observed range (~98% fewer firings); confirm the current level is acceptable before applying it."
	got := normalizeStoredReason("investigate_signal", stored)
	if strings.Contains(got, "93.8") || strings.Contains(got, "fewer firings") {
		t.Errorf("an investigate row must not name a value in its reason: %s", got)
	}
	if !strings.HasSuffix(got, "cannot tell those apart.") {
		t.Errorf("the diagnosis itself must be kept: %s", got)
	}
	for _, value := range []string{"2.23e+06", "-1.59e+05", "1"} {
		r := "The evidence cannot tell those apart. " + value + " is the nearest value outside the observed range (~99% fewer firings); confirm."
		if got := normalizeStoredReason("investigate_signal", r); got != "The evidence cannot tell those apart." {
			t.Errorf("value %q not removed: %s", value, got)
		}
	}
	// A tuning keeps its explanation whole.
	if got := normalizeStoredReason("tune_threshold", stored); got != stored {
		t.Error("a tuning's reason must not be touched")
	}
}

func TestUnavailableSuggestion(t *testing.T) {
	if s := UnavailableSuggestion("not_eligible", "x"); s.RecommendationType != "not_eligible" || s.RecommendationLabel != "Nothing to Tune" {
		t.Errorf("a not_eligible row stays 'nothing to tune', got %q / %q", s.RecommendationType, s.RecommendationLabel)
	}
	for _, status := range []string{"skipped", "error"} {
		if s := UnavailableSuggestion(status, "x"); s.RecommendationType != "insufficient_data" || s.RecommendationLabel != "Not Enough Data" {
			t.Errorf("a %s row could not be analysed; it must not read as 'nothing to tune', got %q / %q", status, s.RecommendationType, s.RecommendationLabel)
		}
	}
}

func TestTransientShare_NeedsEnoughFirings(t *testing.T) {
	// 2 short firings out of 106 that otherwise carry no end time are not evidence.
	if got := transientShare(2, 2); got != 0 {
		t.Errorf("two firings with a duration must not count as 100%% transient, got %.2f", got)
	}
	if got := transientShare(4, 5); got != 0.8 {
		t.Errorf("five firings with a duration are enough, got %.2f", got)
	}
}

func TestPlainReason_NoRuleSourceInUserText(t *testing.T) {
	exprs := map[string]string{
		"several conditions":  `(sum(rate(a[5m])) / sum(rate(b[5m]))) > 0.2 and sum(rate(b[5m])) > 0.1`,
		"two changing values": `sum(increase(vm_new_timeseries_created_total[24h])) by (instance) > (sum(vm_cache_entries{type="storage/hour_metric_ids"}) by (instance) * 3)`,
		"not a comparison":    `vector(1)`,
		"metric is missing":   `absent(up{job="kube-scheduler"})`,
	}
	for want, expr := range exprs {
		_, err := extractPromQLThreshold(expr)
		if err == nil {
			t.Fatalf("%q should not yield a tunable threshold", expr)
		}
		got := plainReason(err)
		if !strings.Contains(got, want) {
			t.Errorf("reason for %q should mention %q, got %q", expr, want, got)
		}
		if strings.ContainsAny(got, `{}"[]`) || strings.Contains(got, "sql:") {
			t.Errorf("reason must not carry rule source or error text, got %q", got)
		}
	}
	for _, cause := range []error{errRuleNotFound, errThresholdUnreadable, errMetricUnavailable, nil} {
		if got := plainReason(cause); got == "" || strings.ContainsAny(got, `{}"[]`) {
			t.Errorf("reason for %v must be a plain sentence, got %q", cause, got)
		}
	}
}

func TestRuleLookupError_OnlyAMissingRowIsNotFound(t *testing.T) {
	if got := plainReason(ruleLookupError(`alert "X"`, sql.ErrNoRows)); !strings.Contains(got, "could not be found") {
		t.Errorf("a missing row is 'rule not found', got %q", got)
	}
	if got := plainReason(ruleLookupError(`alert "X"`, context.DeadlineExceeded)); strings.Contains(got, "could not be found") {
		t.Errorf("a timeout must not be stored as 'rule not found', got %q", got)
	}
}

func TestIsSpreadlessMethod(t *testing.T) {
	for _, m := range []string{"IQR", "P95", "P5", "IQR-baseline", "P95-baseline", "P95-chronic"} {
		if !isSpreadlessMethod(m) {
			t.Errorf("%q is a zero-spread fallback", m)
		}
	}
	// "spike" is derived from the median and spread of the values above the threshold.
	for _, m := range []string{"MAD", "MAD-baseline", "MAD-chronic", "spike", ""} {
		if isSpreadlessMethod(m) {
			t.Errorf("%q is not a zero-spread fallback", m)
		}
	}
}

// Every recommendation type the engine can emit has its own wording. A type added without one would
// be served as "Review" — safe, but silent — so this fails instead.
func TestRecommendationLabel_CoversEveryEmittedType(t *testing.T) {
	assigned := regexp.MustCompile(`RecommendationType\s*(?:=|:)\s*"([a-z_]+)"`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range assigned.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = true
		}
	}
	if len(seen) < 5 {
		t.Fatalf("found only %d emitted types — the scan no longer matches how types are assigned", len(seen))
	}
	for recType := range seen {
		if _, ok := recommendationLabels[recType]; !ok {
			t.Errorf("recommendation type %q is emitted but has no label", recType)
		}
	}
	if recommendationLabel("something_new") != "Review" || recommendationLabel("") != "Review" {
		t.Error("an unknown type must be worded as a review, never as a tuning")
	}
}
