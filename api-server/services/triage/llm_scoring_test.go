package triage

import (
	"strings"
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
	score, prio, _ := computeVerdictScore(out, "prod", 1, workloadTier{})
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
			got, prio, _ := computeVerdictScore(&tt.verdict, tt.env, tt.recur, workloadTier{})
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
		RecurrenceSemantics: "escalating", EnvSensitivity: "partial", BandFloor: "P3", BandCeiling: "P2"}, "non_prod", 22, workloadTier{}); escalating == 0 {
		t.Fatal("recurring OOM must not score 0 (the legacy bug)")
	}
}

// Env is ADDITIVE, not a multiplicative cap: a HIGH alert in non-prod must be able to exceed P3.
func TestEnvAdditive_NonProdHighNotFloored(t *testing.T) {
	v := SignalVerdict{Intrinsic: "high", Blast: "customer_facing",
		RecurrenceSemantics: "escalating", EnvSensitivity: "partial", BandFloor: "P3", BandCeiling: "P1"}
	score, prio, _ := computeVerdictScore(&v, "non_prod", 21, workloadTier{})
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

// An alert on a Service or a workload itself has no owner above it. Scoping those to the
// namespace gave every Service in a namespace one shared verdict, minted from whichever fired
// first; they are scoped to the resource instead.
func TestClassKeyOwnerlessSubject(t *testing.T) {
	ev := func(source, subjectType, name, namespace, owner string) *models.Event {
		return &models.Event{AggregationKey: sp("service_no_endpoints"), FindingType: sp("issue"),
			Source: sp(source), SubjectType: sp(subjectType), SubjectName: sp(name),
			SubjectNamespace: sp(namespace), SubjectOwner: sp(owner)}
	}
	cases := []struct {
		name string
		ev   *models.Event
		want string
	}{
		{"service is scoped to itself", ev("kubernetes_api_server", "service", "admission", "ingress-nginx", ""),
			"service_no_endpoints|issue|ingress-nginx/admission"},
		{"another service in the same namespace is a different class", ev("kubernetes_api_server", "service", "controller", "ingress-nginx", ""),
			"service_no_endpoints|issue|ingress-nginx/controller"},
		{"the same service name in another namespace is a different class", ev("kubernetes_api_server", "service", "admission", "staging", ""),
			"service_no_endpoints|issue|staging/admission"},
		{"workload kinds, any case", ev("prometheus", "StatefulSet", "postgres", "db", ""),
			"service_no_endpoints|issue|db/postgres"},
		{"deployment", ev("anomaly", "deployment", "api", "shop", ""), "service_no_endpoints|issue|shop/api"},
		{"daemonset", ev("kubernetes_api_server", "daemonset", "node-exporter", "monitoring", ""),
			"service_no_endpoints|issue|monitoring/node-exporter"},
		{"an owner still wins", ev("kubernetes_api_server", "service", "admission", "ingress-nginx", "ingress-nginx-controller"),
			"service_no_endpoints|issue|ingress-nginx-controller"},
		{"pods stay scoped to the namespace", ev("kubernetes_api_server", "pod", "api-7d9f-x2k4", "shop", ""),
			"service_no_endpoints|issue|shop"},
		{"nodes stay scoped to the namespace", ev("kubernetes_api_server", "node", "node-1", "", ""),
			"service_no_endpoints|issue|"},
		{"jobs stay scoped to the namespace", ev("kubernetes_api_server", "job", "scan-00947d98", "tools", ""),
			"service_no_endpoints|issue|tools"},
		// Cloud alerts also call their subject a service; the namespace holds the provider's service name.
		{"an AWS ECS service is not a Kubernetes Service", ev("AWS_EventBridge", "service", "cluster-a/checkout", "AmazonECS", ""),
			"service_no_endpoints|issue|AmazonECS"},
		{"a Cloud Run service is not a Kubernetes Service", ev("GCP_PubSub", "service", "checkout", "cloudrun", ""),
			"service_no_endpoints|issue|cloudrun"},
		{"a cloud feed this code has never seen stays on the cloud side", ev("Azure_EventGrid", "service", "checkout", "containerapps", ""),
			"service_no_endpoints|issue|containerapps"},
		{"a whitespace-only owner is no owner", ev("kubernetes_api_server", " Service ", " admission ", " ingress-nginx ", "  "),
			"service_no_endpoints|issue|ingress-nginx/admission"},
		{"a nameless subject falls back to the namespace", ev("kubernetes_api_server", "service", "", "shop", ""),
			"service_no_endpoints|issue|shop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassKey(tc.ev); got != tc.want {
				t.Fatalf("ClassKey = %q, want %q", got, tc.want)
			}
			// The correction log derives the key from its own event projection; the two must agree.
			basic := &eventBasicInfo{AggregationKey: tc.ev.AggregationKey, FindingType: tc.ev.FindingType,
				Source: tc.ev.Source, SubjectType: tc.ev.SubjectType, SubjectName: tc.ev.SubjectName,
				SubjectOwner: tc.ev.SubjectOwner, SubjectNamespace: tc.ev.SubjectNamespace}
			if got := basic.classKey(); got != tc.want {
				t.Fatalf("eventBasicInfo.classKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// A CloudWatch metric alarm is keyed on the check it runs, not on the alarm name, which commonly
// names the resource. Built only from the collector's ingest-time labels.
func TestClassKeyCloudWatchCheck(t *testing.T) {
	alarm := func(name, subject string, labels map[string]any) *models.Event {
		j := models.NewJsonObject(labels)
		return &models.Event{AggregationKey: sp(name), FindingType: sp("issue"), Source: sp("AWS_CloudWatch_Alarm"),
			SubjectType: sp("compute-instance"), SubjectName: sp(subject), SubjectNamespace: sp("AmazonEC2"), Labels: &j}
	}
	metric := func(ns, name, stat, threshold, dimKind string) map[string]any {
		return map[string]any{"aws_event_metric_namespace": ns, "aws_event_metric_name": name,
			"aws_event_metric_statistic": stat, "aws_event_threshold": threshold, "aws_event_instance_type": dimKind,
			"aws_event_metric_expression": "false"}
	}
	cpu := func(threshold string) map[string]any {
		return metric("AWS/EC2", "CPUUtilization", "Average", threshold, "InstanceId")
	}
	cases := []struct {
		name string
		ev   *models.Event
		want string
	}{
		{"one host", alarm("i-0aaa-HOSTA-CPU-Alarm-P1", "i-0aaa", cpu("90.000000")),
			"cw:aws/ec2/cpuutilization/average/90/instanceid|issue|AmazonEC2"},
		{"another host, same check: same class (push path writes the threshold as 90)", alarm("i-0bbb-HOSTB-CPU-Alarm-P1", "i-0bbb", cpu("90")),
			"cw:aws/ec2/cpuutilization/average/90/instanceid|issue|AmazonEC2"},
		{"another tier has its own threshold: different class", alarm("i-0aaa-HOSTA-CPU-Alarm-P3", "i-0aaa", cpu("75.000000")),
			"cw:aws/ec2/cpuutilization/average/75/instanceid|issue|AmazonEC2"},
		{"same metric on an Auto Scaling group: different class", alarm("asg-cpu", "web-asg",
			metric("AWS/EC2", "CPUUtilization", "Average", "90", "AutoScalingGroupName")),
			"cw:aws/ec2/cpuutilization/average/90/autoscalinggroupname|issue|AmazonEC2"},
		{"extended statistic", alarm("alb-p99", "app/web/1", metric("AWS/ApplicationELB", "TargetResponseTime", "p99", "1.5", "LoadBalancer")),
			"cw:aws/applicationelb/targetresponsetime/p99/1.5/loadbalancer|issue|AmazonEC2"},
		{"zero threshold is a threshold", alarm("fn-a-errors", "fn-a", metric("AWS/Lambda", "Errors", "Sum", "0", "FunctionName")),
			"cw:aws/lambda/errors/sum/0/functionname|issue|AmazonEC2"},
		{"another function's errors alarm: same class", alarm("fn-b-errors", "fn-b", metric("AWS/Lambda", "Errors", "Sum", "0.000000", "FunctionName")),
			"cw:aws/lambda/errors/sum/0/functionname|issue|AmazonEC2"},
		{"anomaly band has no threshold: alarm-name key", alarm("Anomaly-A", "i-0aaa", metric("AWS/EC2", "CPUUtilization", "Average", "", "InstanceId")),
			"anomaly-a|issue|AmazonEC2"},
		{"metric math has no metric: alarm-name key", alarm("Math-A", "i-0aaa", metric("", "", "", "10", "")),
			"math-a|issue|AmazonEC2"},
		{"metric math names only its first input: alarm-name key", alarm("Web-5xx-Rate", "app/web/1",
			func() map[string]any {
				l := metric("AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", "Sum", "5", "LoadBalancer")
				l["aws_event_metric_expression"] = "true"
				return l
			}()),
			"web-5xx-rate|issue|AmazonEC2"},
		{"collector too old to report the expression flag: alarm-name key", alarm("i-0aaa-HOSTA-CPU-Alarm-P1", "i-0aaa",
			func() map[string]any {
				l := cpu("90")
				delete(l, "aws_event_metric_expression")
				return l
			}()),
			"i-0aaa-hosta-cpu-alarm-p1|issue|AmazonEC2"},
		{"no statistic: alarm-name key", alarm("Stat-A", "i-0aaa", metric("AWS/EC2", "CPUUtilization", "", "90", "InstanceId")),
			"stat-a|issue|AmazonEC2"},
		{"no labels: alarm-name key", alarm("Plain-A", "i-0aaa", nil), "plain-a|issue|AmazonEC2"},
		{"push path: threshold read from the state reason, same class as the poll path", alarm("i-0ccc-HOSTC-CPU-Alarm-P1", "i-0ccc",
			func() map[string]any {
				l := cpu("")
				l["aws_event_state_reason"] = "Threshold Crossed: 1 datapoint [97.3 (03/11/26 05:00:00)] was greater than or equal to the threshold (90.0)."
				return l
			}()),
			"cw:aws/ec2/cpuutilization/average/90/instanceid|issue|AmazonEC2"},
		{"push path anomaly band: alarm-name key", alarm("Anomaly-B", "i-0aaa",
			func() map[string]any {
				l := cpu("")
				l["aws_event_state_reason"] = "Thresholds Crossed: 1 datapoint was greater than the upper thresholds [55.2]"
				return l
			}()),
			"anomaly-b|issue|AmazonEC2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassKey(tc.ev); got != tc.want {
				t.Fatalf("ClassKey = %q, want %q", got, tc.want)
			}
			basic := &eventBasicInfo{AggregationKey: tc.ev.AggregationKey, FindingType: tc.ev.FindingType,
				Source: tc.ev.Source, SubjectType: tc.ev.SubjectType, SubjectName: tc.ev.SubjectName,
				SubjectNamespace: tc.ev.SubjectNamespace, Labels: tc.ev.Labels}
			if got := basic.classKey(); got != tc.want {
				t.Fatalf("eventBasicInfo.classKey = %q, want %q", got, tc.want)
			}
		})
	}

	// Labels other sources carry under the same names do not exist; their keys are unchanged.
	k8s := &models.Event{AggregationKey: sp("KubePodCrashLooping"), FindingType: sp("issue"), SubjectOwner: sp("api"),
		Labels: func() *models.Json {
			j := models.NewJsonObject(map[string]any{"alertname": "KubePodCrashLooping"})
			return &j
		}()}
	if got := ClassKey(k8s); got != "kubepodcrashlooping|issue|api" {
		t.Fatalf("ClassKey(k8s) = %q", got)
	}
}

// The classifier is told the check, not the host: the verdict is served to every resource the
// check runs on, so the instance id, host name and one host's datapoints must not reach it.
func TestBuildEventContextCloudWatchCheck(t *testing.T) {
	j := models.NewJsonObject(map[string]any{"aws_event_metric_namespace": "AWS/EC2", "aws_event_metric_name": "CPUUtilization",
		"aws_event_metric_statistic": "Average", "aws_event_threshold": "90.000000", "aws_event_instance_type": "InstanceId",
		"aws_event_metric_expression": "false",
		"aws_event_state_reason":      "Threshold Crossed: 2 datapoints [97.3, 96.1] were greater than or equal to the threshold (90.0)."})
	ev := &models.Event{Title: "AmazonEC2::i-0aaa111::Threshold Crossed", AggregationKey: sp("i-0aaa111-HOSTA-CPU-Alarm-P1"),
		Source: sp("AWS_CloudWatch_Alarm"), FindingType: sp("issue"), Priority: sp("HIGH"),
		SubjectType: sp("compute-instance"), SubjectName: sp("i-0aaa111"), SubjectNamespace: sp("AmazonEC2"),
		Description: sp("CloudWatch Alarm 'i-0aaa111-HOSTA-CPU-Alarm-P1' changed from OK to ALARM."),
		Labels:      &j}
	got := buildEventContext(ev, workloadFacts{})
	for _, leak := range []string{"i-0aaa111", "HOSTA", "97.3"} {
		if strings.Contains(got, leak) {
			t.Fatalf("context names one resource (%q):\n%s", leak, got)
		}
	}
	want := "- alarm: CloudWatch aws/ec2 cpuutilization (average) greater than or equal to threshold 90, per instanceid\n"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("context = %q, want prefix %q", got, want)
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
