package triage

import (
	"strings"
	"testing"
)

func TestSuggestWait(t *testing.T) {
	// 50 firings; 45 cleared within ten minutes, 44 of them within one.
	q := &AlertQualityScore{TransientRate: 0.9, TotalCount: 50, ResolvedCount: 50, ShortFirings: shortFirings(44, 45, 45, 45, 45)}

	t.Run("adds the shortest extra wait that covers the short firings", func(t *testing.T) {
		a := suggestWait(q, 30, true)
		if a == nil {
			t.Fatal("expected advice")
		}
		// 30s today + 1m, written as whole minutes.
		if a.SuggestedMinutes != 2 || a.WithinSeconds != 60 || a.Removed != 44 || a.Total != 50 {
			t.Errorf("got %+v", *a)
		}
		reason := a.reason()
		for _, want := range []string{"44 of 50", "within 1m", "Waiting 2m", "instead of 30s"} {
			if !strings.Contains(reason, want) {
				t.Errorf("reason should mention %q: %s", want, reason)
			}
		}
	})

	t.Run("a rule that fires at once gets the extra wait itself", func(t *testing.T) {
		a := suggestWait(q, 0, true)
		if a == nil || a.SuggestedMinutes != 1 {
			t.Fatalf("got %+v", a)
		}
		if !strings.Contains(a.reason(), "instead of firing at once") {
			t.Errorf("reason: %s", a.reason())
		}
	})

	t.Run("a rule that already waits is never given a shorter or equal wait", func(t *testing.T) {
		// Already waiting 15 minutes: the advice adds to it, it does not restate a flat "10 minutes".
		a := suggestWait(q, 900, true)
		if a == nil || a.SuggestedMinutes != 16 {
			t.Fatalf("got %+v", a)
		}
	})

	t.Run("no advice past the longest wait advice may ask for", func(t *testing.T) {
		slow := &AlertQualityScore{TransientRate: 0.9, TotalCount: 40, ResolvedCount: 40, ShortFirings: shortFirings(0, 0, 0, 10, 36)}
		if a := suggestWait(slow, 25*60, true); a != nil {
			t.Errorf("25m + 10m passes the limit, got %+v", *a)
		}
	})

	t.Run("no advice without a known wait, or when firings are not mostly short", func(t *testing.T) {
		if a := suggestWait(q, 0, false); a != nil {
			t.Errorf("unknown wait: got %+v", *a)
		}
		long := &AlertQualityScore{TransientRate: 0.4, TotalCount: 50, ResolvedCount: 50, ShortFirings: shortFirings(5, 10, 15, 18, 20)}
		if a := suggestWait(long, 30, true); a != nil {
			t.Errorf("mostly long firings: got %+v", *a)
		}
		if a := suggestWait(nil, 30, true); a != nil {
			t.Errorf("no firing history: got %+v", *a)
		}
	})

	t.Run("no advice for a rule whose firings mostly never clear", func(t *testing.T) {
		// 100 firings, 6 with an end time (all short), 94 still open: the six say nothing about
		// the rest, and "never resolves" must not turn into "wait a minute longer".
		neverClears := &AlertQualityScore{TransientRate: 1.0, TotalCount: 100, ResolvedCount: 6, ShortFirings: shortFirings(6, 6, 6, 6, 6)}
		if a := suggestWait(neverClears, 0, true); a != nil {
			t.Errorf("got %+v", *a)
		}
		s := &ThresholdSuggestion{RecommendationType: "review_alert", Reason: "Alert never resolves."}
		offerWait(s, &AlertDefinition{forKnown: true}, neverClears)
		if s.RecommendationType != "review_alert" || s.SuggestedDuration != 0 {
			t.Errorf("a never-resolving rule must stay under review: %+v", s)
		}
	})

	t.Run("routed alerts that clear on a five-minute grid get a five-minute step", func(t *testing.T) {
		grid := &AlertQualityScore{TransientRate: 0.8, TotalCount: 100, ResolvedCount: 82, ShortFirings: shortFirings(0, 0, 0, 62, 66)}
		a := suggestWait(grid, 120, true)
		if a == nil || a.WithinSeconds != 300 || a.SuggestedMinutes != 7 {
			t.Fatalf("got %+v", a)
		}
	})
}

func TestOfferWait(t *testing.T) {
	shortLived := &AlertQualityScore{TransientRate: 0.9, TotalCount: 50, ResolvedCount: 50, ShortFirings: shortFirings(0, 0, 0, 44, 46)}
	rule := &AlertDefinition{ForSeconds: 120, forKnown: true}

	t.Run("a verdict that changes nothing still gets a wait to set", func(t *testing.T) {
		for _, recType := range []string{"insufficient_data", "review_alert", "investigate_signal", "not_eligible", "none"} {
			s := &ThresholdSuggestion{RecommendationType: recType, Reason: "Why no threshold advice.", RiskLevel: "dangerous", RiskWarnings: []string{"about a value"}}
			offerWait(s, rule, shortLived)
			if s.RecommendationType != "increase_duration" || s.SuggestedDuration != 7 || s.CurrentDurationSeconds != 120 {
				t.Errorf("%s: got type=%q wait=%ds -> %dm", recType, s.RecommendationType, s.CurrentDurationSeconds, s.SuggestedDuration)
			}
			if !strings.HasPrefix(s.Reason, "Why no threshold advice. ") || !strings.Contains(s.Reason, "firing history alone") {
				t.Errorf("%s: the reason must keep why no threshold advice was given and say what the wait rests on: %s", recType, s.Reason)
			}
			if s.RiskLevel != "" || s.RiskWarnings != nil {
				t.Errorf("%s: a risk about a threshold value must not travel with wait advice", recType)
			}
		}
	})

	t.Run("a tuning, and a verdict that already carries a wait, are left alone", func(t *testing.T) {
		tune := &ThresholdSuggestion{RecommendationType: "tune_threshold", SuggestedThreshold: 12}
		offerWait(tune, rule, shortLived)
		if tune.RecommendationType != "tune_threshold" || tune.SuggestedDuration != 0 {
			t.Errorf("a tuning must stay a tuning: %+v", tune)
		}
		already := &ThresholdSuggestion{RecommendationType: "increase_duration", SuggestedDuration: 4, Reason: "kept"}
		offerWait(already, rule, shortLived)
		if already.SuggestedDuration != 4 || already.Reason != "kept" {
			t.Errorf("existing wait advice must not be replaced: %+v", already)
		}
	})

	t.Run("no wait is offered without the evidence for one", func(t *testing.T) {
		longLived := &AlertQualityScore{TransientRate: 0.2, TotalCount: 50, ResolvedCount: 50, ShortFirings: shortFirings(0, 0, 0, 5, 10)}
		s := &ThresholdSuggestion{RecommendationType: "investigate_signal", Reason: "kept"}
		offerWait(s, rule, longLived)
		if s.RecommendationType != "investigate_signal" || s.Reason != "kept" {
			t.Errorf("mostly long firings: %+v", s)
		}
		s = &ThresholdSuggestion{RecommendationType: "investigate_signal", Reason: "kept"}
		offerWait(s, &AlertDefinition{}, shortLived)
		if s.RecommendationType != "investigate_signal" {
			t.Errorf("a rule whose wait is not known gets no wait to set: %+v", s)
		}
		offerWait(nil, rule, shortLived) // must not panic
	})
}

func TestCurrentWait(t *testing.T) {
	if s, known := currentWait(&AlertDefinition{ForSeconds: 30, forKnown: true}); s != 30 || !known {
		t.Errorf("Prometheus rule: got %d, %v", s, known)
	}
	if s, known := currentWait(&AlertDefinition{forKnown: true}); s != 0 || !known {
		t.Errorf("a rule with no wait is known to wait 0: got %d, %v", s, known)
	}
	if s, known := currentWait(&AlertDefinition{Period: 300, EvaluationPeriods: 3}); s != 900 || !known {
		t.Errorf("cloud alarm: got %d, %v", s, known)
	}
	if s, known := currentWait(&AlertDefinition{Period: 60}); s != 60 || !known {
		t.Errorf("a window with no period count is one period: got %d, %v", s, known)
	}
	if _, known := currentWait(&AlertDefinition{}); known {
		t.Error("a definition that carries no wait is not known")
	}
}

func TestFormatWait(t *testing.T) {
	cases := map[int]string{0: "0s", 30: "30s", 60: "1m", 90: "1m30s", 120: "2m", 900: "15m", 3600: "1h", 5400: "1h30m"}
	for in, want := range cases {
		if got := formatWait(in); got != want {
			t.Errorf("formatWait(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDurationSeconds(t *testing.T) {
	cases := map[string]int{
		"30s":     30,
		"2m":      120,
		"1h30m":   5400,
		"900":     900, // the rules API reports seconds as a bare number
		"30":      30,
		"0":       0,
		"0s":      0,
		"PT5M":    300,
		"":        0,
		"garbage": 0,
	}
	for in, want := range cases {
		if got := parseDurationSeconds(in); got != want {
			t.Errorf("parseDurationSeconds(%q) = %d, want %d", in, got, want)
		}
	}
	// The minute form is what revert bookkeeping stores; a bare-seconds wait used to read as 0.
	if got := parseDurationMinutes("900"); got != 15 {
		t.Errorf(`parseDurationMinutes("900") = %d, want 15`, got)
	}
}

func TestBuildRewrittenRule_SameWaitInAnotherNotationIsNotAChange(t *testing.T) {
	sug := &ApplySuggestionRow{Source: "prometheus", RecommendationType: "increase_duration", CurrentThreshold: 5}
	rule := &SourceRule{Source: "prometheus", Expr: "up > 5", Duration: "120"}
	out, err := BuildRewrittenRule(sug, rule, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if out.ChangedDuration {
		t.Error(`"2m" over a stored "120" seconds is the same wait`)
	}
	out, err = BuildRewrittenRule(sug, rule, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !out.ChangedDuration || out.Duration != "3m" {
		t.Errorf("3m over 120s is a change: %+v", out)
	}
}

func TestPlanRevert_RestoresTheRecordedWaitExactly(t *testing.T) {
	prevThreshold := 5.0
	prevMinutes := 0 // a 30s wait is 0 whole minutes: the minute bookkeeping cannot restore it
	// The row has since been recomputed to a different recommendation; revert must not depend on it.
	sug := &ApplySuggestionRow{Source: "prometheus", RecommendationType: "investigate_signal", CurrentThreshold: 5,
		PreviousThreshold: &prevThreshold, PreviousDuration: &prevMinutes, ApplyStatus: "applied", ApplyMethod: "direct"}
	rule := &SourceRule{Source: "prometheus", Expr: "up > 5", Duration: "2m"}

	plan, err := planRevert(sug, rule, map[string]any{ruleDurationKey: "30s"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.rewritten.Duration != "30s" || !plan.rewritten.ChangedDuration {
		t.Errorf("revert must write the recorded wait: %+v", plan.rewritten)
	}
	if plan.rewritten.Restore != nil {
		t.Error("the recorded wait is not a cloud provider record and must not reach Restore")
	}

	// A rule that had no wait is restored to none.
	plan, err = planRevert(sug, rule, map[string]any{ruleDurationKey: ""})
	if err != nil {
		t.Fatal(err)
	}
	if plan.rewritten.Duration != "0s" {
		t.Errorf(`a rule with no wait is restored with "0s", got %q`, plan.rewritten.Duration)
	}

	// Nothing recorded (an apply made before waits were recorded): the minute bookkeeping stands.
	plan, err = planRevert(sug, rule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.rewritten.Duration != "" {
		t.Errorf("no recorded wait and 0 minutes: nothing to restore, got %q", plan.rewritten.Duration)
	}
}

func TestApplyBlockReason_HeldWhileTheMetricCannotBeRead(t *testing.T) {
	sug := &ApplySuggestionRow{RecommendationType: "tune_threshold", RiskLevel: "safe", Confidence: "high", FetchFailedAt: "2026-01-02T06:00:00Z"}
	if reason := applyBlockReason(sug, true); !strings.Contains(reason, "last attempt to read") {
		t.Errorf("advice from an earlier run must not be applied, even with the override: %q", reason)
	}
	sug.FetchFailedAt = ""
	if reason := applyBlockReason(sug, false); reason != "" {
		t.Errorf("a safe, confident tuning read on the latest run applies: %q", reason)
	}
}

func TestDescribeSeries(t *testing.T) {
	cases := []struct {
		labels map[string]string
		want   string
	}{
		{map[string]string{"namespace": "shop", "pod": "cart-5d9f", "job": "kube-state-metrics", "endpoint": "http"}, "namespace=shop, pod=cart-5d9f"},
		{map[string]string{"queue": "events", "namespace": "mq", "vhost": "/"}, "namespace=mq, queue=events, vhost=/"},
		{map[string]string{"instance": "192.0.2.1:9100", "job": "node"}, "instance=192.0.2.1:9100"},
		{map[string]string{"job": "node"}, "job=node"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := describeSeries(c.labels); got != c.want {
			t.Errorf("describeSeries(%v) = %q, want %q", c.labels, got, c.want)
		}
	}
}
