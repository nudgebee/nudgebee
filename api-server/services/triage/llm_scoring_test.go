package triage

import (
	"testing"

	"nudgebee/services/internal/database/models"
)

func sp(s string) *string { return &s }

// Provider lifecycle/state-change notifications must be recognized; real metric alarms must not.
func TestIsProviderLifecycleNotification(t *testing.T) {
	cases := []struct {
		agg  *string
		want bool
	}{
		{sp("EC2 Instance State-change Notification"), true},
		{sp("ec2 instance state change notification"), true},
		{sp("EC2 Spot Instance Interruption Warning"), true},
		{sp("CloudWatch Alarm: sqs-rackspace-eventbridge-queue-depth"), false}, // real alarm
		{sp("SQS Messages Age - nudgebee-eventbridge-queue"), false},
		{sp("KubePodCrashLooping"), false},
		{sp("configurationchange/kubernetesresource/change"), false}, // already expected_change via verdict
		{nil, false},
	}
	for _, c := range cases {
		got := isProviderLifecycleNotification(&models.Event{AggregationKey: c.agg})
		label := "<nil>"
		if c.agg != nil {
			label = *c.agg
		}
		if got != c.want {
			t.Errorf("isProviderLifecycleNotification(%q) = %v, want %v", label, got, c.want)
		}
	}
}

// A lifecycle notification the LLM mis-rated as a control-plane P1 incident must be floored to
// info/expected_change/P3 — its score must land P3 regardless of the cached verdict's band.
func TestApplyDeterministicGuardrails_LifecycleFloor(t *testing.T) {
	// The EC2 state-change miscall: ControlPlane / high / control_plane, band P1..P0.
	v := &SignalVerdict{Category: "ControlPlane", Intrinsic: "high", Blast: "control_plane",
		RecurrenceSemantics: "neutral", EnvSensitivity: "none", BandFloor: "P1", BandCeiling: "P0"}
	ev := &models.Event{AggregationKey: sp("EC2 Instance State-change Notification")}

	out, applied := applyDeterministicGuardrails(v, ev)
	if len(applied) != 1 || applied[0] != "lifecycle_floor" {
		t.Fatalf("expected lifecycle_floor guardrail, got %v", applied)
	}
	if out == v {
		t.Fatal("guardrail must not mutate the input verdict (returned same pointer)")
	}
	if v.Intrinsic != "high" || v.BandFloor != "P1" {
		t.Fatalf("input verdict was mutated: %+v", v)
	}
	if out.Intrinsic != "info" || out.Blast != "expected_change" || out.BandFloor != "P3" || out.BandCeiling != "P3" {
		t.Fatalf("floored verdict wrong: %+v", out)
	}
	score, prio, _ := computeVerdictScore(out, "prod", 1)
	if prio != "P3" {
		t.Fatalf("lifecycle notification scored %d/%s, want P3", score, prio)
	}

	// A non-lifecycle event leaves the verdict untouched.
	out2, applied2 := applyDeterministicGuardrails(v, &models.Event{AggregationKey: sp("KubePodCrashLooping")})
	if len(applied2) != 0 || out2 != v {
		t.Fatalf("non-lifecycle event should be a no-op, got applied=%v", applied2)
	}
}

func TestComputeVerdictScore_ValidatedCases(t *testing.T) {
	tests := []struct {
		name      string
		verdict   SignalVerdict
		env       string
		recur     int
		wantScore int
		wantPrio  string
	}{
		{
			// OOMKilled on the monitoring backbone, recurring in non-prod -> P2 (was 0/P3).
			name: "oom_monitoring_backbone_recurring_nonprod",
			verdict: SignalVerdict{Intrinsic: "high", Blast: "monitoring_backbone",
				RecurrenceSemantics: "escalating", EnvSensitivity: "partial",
				BandFloor: "P3", BandCeiling: "P2"},
			env: "non_prod", recur: 22, wantScore: 59, wantPrio: "P2",
		},
		{
			// Control-plane component down: critical, barely discounted by non-prod -> P0.
			name: "control_plane_down",
			verdict: SignalVerdict{Intrinsic: "critical", Blast: "control_plane",
				RecurrenceSemantics: "neutral", EnvSensitivity: "partial",
				BandFloor: "P2", BandCeiling: "P0"},
			env: "non_prod", recur: 3, wantScore: 82, wantPrio: "P0",
		},
		{
			// Planned maintenance (GKE upgrade): info + expected_change + full discount -> P3.
			name: "planned_maintenance_noise",
			verdict: SignalVerdict{Intrinsic: "info", Blast: "expected_change",
				RecurrenceSemantics: "neutral", EnvSensitivity: "full",
				BandFloor: "P3", BandCeiling: "P3"},
			env: "non_prod", recur: 2, wantScore: 0, wantPrio: "P3",
		},
		{
			// Chronic-but-known queue backlog: recurrence as NOISE dampens it -> stays P3.
			name: "chronic_noise_dampened",
			verdict: SignalVerdict{Intrinsic: "medium", Blast: "single_workload",
				RecurrenceSemantics: "noise", EnvSensitivity: "full",
				BandFloor: "P3", BandCeiling: "P3"},
			env: "non_prod", recur: 954, wantScore: 12, wantPrio: "P3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, prio, _ := computeVerdictScore(&tt.verdict, tt.env, tt.recur)
			if got != tt.wantScore || prio != tt.wantPrio {
				t.Fatalf("got score=%d prio=%s, want score=%d prio=%s", got, prio, tt.wantScore, tt.wantPrio)
			}
		})
	}
}

// Recurrence ESCALATES crash/chronic classes (opposite of the legacy duplicate penalty).
func TestRecurrenceAdjustment_Direction(t *testing.T) {
	if recurrenceAdjustment(22, "escalating") <= 0 {
		t.Fatal("escalating recurrence should raise score")
	}
	if recurrenceAdjustment(22, "noise") >= 0 {
		t.Fatal("noise recurrence should lower score")
	}
	if recurrenceAdjustment(1, "escalating") != 0 || recurrenceAdjustment(100, "neutral") != 0 {
		t.Fatal("first-occurrence or neutral recurrence should be 0")
	}
	// escalating with the same count must beat the legacy flat -30 penalty regime
	if escalating, _, _ := computeVerdictScore(&SignalVerdict{Intrinsic: "high", Blast: "monitoring_backbone",
		RecurrenceSemantics: "escalating", EnvSensitivity: "partial", BandFloor: "P3", BandCeiling: "P2"}, "non_prod", 22); escalating == 0 {
		t.Fatal("recurring OOM must not score 0 (the legacy bug)")
	}
}

// Env is ADDITIVE, not a multiplicative cap: a HIGH alert in non-prod must be able to exceed P3.
func TestEnvAdditive_NonProdHighNotFloored(t *testing.T) {
	v := SignalVerdict{Intrinsic: "high", Blast: "customer_facing",
		RecurrenceSemantics: "escalating", EnvSensitivity: "partial", BandFloor: "P3", BandCeiling: "P1"}
	score, prio, _ := computeVerdictScore(&v, "non_prod", 21)
	if prio == "P3" {
		t.Fatalf("non-prod HIGH customer-facing should escape P3 floor, got %d/%s", score, prio)
	}
}

func TestFallbackVerdict_FromPriority(t *testing.T) {
	v := fallbackVerdict(&models.Event{Priority: sp("HIGH")})
	if v.Intrinsic != "high" || v.SourceModel != "fallback" {
		t.Fatalf("unexpected fallback verdict: %+v", v)
	}
	if v := fallbackVerdict(&models.Event{}); v.Intrinsic != "low" {
		t.Fatalf("nil priority should default intrinsic=low, got %s", v.Intrinsic)
	}
}

// Cascade dampening: root stays prominent (+), downstream/co-occurring symptoms dampen (-),
// and same_resource is no longer ignored (the legacy 0 bug).
func TestCorrelationTypeAdjustment(t *testing.T) {
	if correlationTypeAdjustment("likely_root_cause") <= 0 {
		t.Fatal("root cause should be boosted")
	}
	if correlationTypeAdjustment("downstream_impact") >= 0 {
		t.Fatal("downstream symptom should be dampened")
	}
	if correlationTypeAdjustment("same_resource") >= 0 {
		t.Fatal("same_resource must dampen, not be ignored (legacy 0 bug)")
	}
	if correlationTypeAdjustment("unknown_type") != 0 {
		t.Fatal("unknown correlation type should be neutral")
	}
}

func TestParseVerdict(t *testing.T) {
	// fenced JSON + string confidence + min/max -> band mapping
	in := "```json\n{\"category\":\"OOMKill\",\"intrinsic\":\"High\",\"blast\":\"monitoring_backbone\"," +
		"\"recurrence_semantics\":\"escalating\",\"env_sensitivity\":\"partial\"," +
		"\"min_priority\":\"P2\",\"max_priority\":\"P1\",\"confidence\":\"high\",\"reasoning\":\"x\"}\n```"
	v, err := parseVerdict(in)
	if err != nil {
		t.Fatalf("parseVerdict err: %v", err)
	}
	if v.Intrinsic != "high" || v.BandFloor != "P2" || v.BandCeiling != "P1" || v.Confidence != 0.9 {
		t.Fatalf("unexpected verdict: %+v", v)
	}
}

func TestClassKey(t *testing.T) {
	// owner present -> used as scope; aggregation_key lowercased.
	e := &models.Event{AggregationKey: sp("Pod_OOM_Killer_Enricher"), FindingType: sp("issue"),
		SubjectOwner: sp("vmagent"), SubjectNamespace: sp("victoria")}
	if got := ClassKey(e); got != "pod_oom_killer_enricher|issue|vmagent" {
		t.Fatalf("ClassKey = %q", got)
	}
	// owner empty -> falls back to namespace.
	e2 := &models.Event{AggregationKey: sp("GCP Alerts for db"), FindingType: sp("issue"),
		SubjectOwner: sp(""), SubjectNamespace: sp("nudgebee")}
	if got := ClassKey(e2); got != "gcp alerts for db|issue|nudgebee" {
		t.Fatalf("ClassKey(empty owner) = %q", got)
	}
}

func TestClampToBand(t *testing.T) {
	cases := []struct {
		name  string
		score int
		band  interface{}
		want  int
	}{
		{"above ceiling P2..P1 -> 79", 84, "P3..P1", 79},
		{"storage P2..P1 80 -> 79", 80, "P2..P1", 79},
		{"within band unchanged", 57, "P2..P1", 57},
		{"below floor lifted", 10, "P2..P1", 40},
		{"no band (legacy) unchanged", 84, nil, 84},
		{"empty band unchanged", 84, "", 84},
		{"malformed band unchanged", 84, "P2", 84},
		{"P1..P1 pins to that level", 90, "P1..P1", 79},
		{"invalid floor key unchanged", 84, "foo..P1", 84},
		{"invalid ceil key unchanged", 30, "P2..bar", 30},
		{"inverted band P1..P2 unchanged", 70, "P1..P2", 70},
	}
	for _, c := range cases {
		f := map[string]interface{}{}
		if c.band != nil {
			f["band"] = c.band
		}
		if got := clampToBand(c.score, f); got != c.want {
			t.Errorf("%s: clampToBand(%d, band=%v) = %d, want %d", c.name, c.score, c.band, got, c.want)
		}
	}
}
