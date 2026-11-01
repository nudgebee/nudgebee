package triage

import (
	"os"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
)

// TestUnlocatedGroupingDefaultsOn — all three grouping switches are kill
// switches, not opt-ins. A default-off flag nobody flips leaves the path dead in
// config, which is the fate the threshold-suggestion audit measured.
func TestUnlocatedGroupingDefaultsOn(t *testing.T) {
	t.Setenv(unlocatedGroupingEnvFlag, "")
	if !unlocatedGroupingEnabled() {
		t.Error("empty flag disabled the path; it is a kill switch, not an opt-in")
	}
	if err := os.Unsetenv(unlocatedGroupingEnvFlag); err != nil {
		t.Fatalf("unset flag: %v", err)
	}
	if !unlocatedGroupingEnabled() {
		t.Error("absent flag disabled the path; it is a kill switch, not an opt-in")
	}
}

// TestUnlocatedGroupingKillSwitch — the spellings that must turn it off, and the
// near-misses that must not. "no" and "off" read as disabling to a human but are
// not honoured, so an operator reaching for the kill switch has to use the same
// two words the other switches take.
func TestUnlocatedGroupingKillSwitch(t *testing.T) {
	for _, v := range []string{"false", "FALSE", "False", "0"} {
		t.Run("off/"+v, func(t *testing.T) {
			t.Setenv(unlocatedGroupingEnvFlag, v)
			if unlocatedGroupingEnabled() {
				t.Errorf("%q did not disable", v)
			}
		})
	}
	for _, v := range []string{"true", "1", "no", "off", "disabled"} {
		t.Run("on/"+v, func(t *testing.T) {
			t.Setenv(unlocatedGroupingEnvFlag, v)
			if !unlocatedGroupingEnabled() {
				t.Errorf("%q disabled the path; only false/0 may", v)
			}
		})
	}
}

// TestUnlocatedGroupingIsIndependentOfTheOtherSwitches — the three evidence
// families are separately killable, so pulling this one must leave the other two
// alone and vice versa.
func TestUnlocatedGroupingIsIndependentOfTheOtherSwitches(t *testing.T) {
	t.Setenv(unlocatedGroupingEnvFlag, "false")
	t.Setenv(topologyGroupingEnvFlag, "")
	t.Setenv(incidentGroupingEnvFlag, "")

	if unlocatedGroupingEnabled() {
		t.Error("co-timing path should be off when explicitly killed")
	}
	if !topologyGroupingEnabled() || !incidentGroupingEnabled() {
		t.Error("killing the co-timing path must not disturb the other two")
	}
}

// TestUnlocatedScopeFor — Kubernetes alerts are bounded by their namespace; AWS
// alarms by AWS as a whole, so an ELB alarm can still meet the EC2 instances
// behind it on timing when the graph cannot connect them.
func TestUnlocatedScopeFor(t *testing.T) {
	src := func(s string) *string { return &s }

	k8s := unlocatedScopeFor(&models.Event{Source: src("kubernetes_api_server"), SubjectNamespace: src(" Benchmark-Scenarios ")})
	assert.Contains(t, k8s.leaderPredicate, "subject_namespace")
	assert.Equal(t, "benchmark-scenarios", k8s.arg, "namespace normalised the way the SQL side is")
	assert.Equal(t, IncidentAttachWindow, k8s.window, "a namespace is a narrow boundary: the ordinary window")

	for _, s := range awsAlarmSources {
		aws := unlocatedScopeFor(&models.Event{Source: src(s), SubjectNamespace: src("AWSELB")})
		assert.Contains(t, aws.leaderPredicate, "l.source", "%s is scoped by source, not by service code", s)
		assert.NotContains(t, aws.leaderPredicate, "subject_namespace", "%s may cross AWS services", s)
		assert.Equal(t, "AWS", aws.label)
		assert.Equal(t, timingOnlyCloudWindow, aws.window, "%s: a whole account is the widest boundary, so the shortest window", s)
	}
	assert.Less(t, timingOnlyCloudWindow, IncidentAttachWindow)

	// A real Kubernetes namespace that merely looks like AWS stays a namespace.
	lookalike := unlocatedScopeFor(&models.Event{Source: src("prometheus"), SubjectNamespace: src("amazon-cloudwatch")})
	assert.Contains(t, lookalike.leaderPredicate, "subject_namespace")

	// Other clouds keep the namespace rule (one provider service) until measured.
	azure := unlocatedScopeFor(&models.Event{Source: src("Azure_Monitor_Alert"), SubjectNamespace: src("microsoft.compute/virtualmachines")})
	assert.Contains(t, azure.leaderPredicate, "subject_namespace")

	// No source at all falls back to the namespace rule.
	none := unlocatedScopeFor(&models.Event{})
	assert.Equal(t, "", none.arg)
}

// TestUnlocatedScopeForNodeAlerts — a node alert carries no namespace, so the
// namespace rule would drop every node alert in the cluster into one shared ""
// bucket and let them co-time-attach to each other. The node is the boundary
// such an alert actually has.
func TestUnlocatedScopeForNodeAlerts(t *testing.T) {
	src := func(s string) *string { return &s }

	node := unlocatedScopeFor(&models.Event{
		Source:      src("prometheus"),
		SubjectType: src("node"),
		SubjectNode: src("GKE-Pool-ABC"),
	})
	assert.Contains(t, node.leaderPredicate, "subject_node")
	assert.NotContains(t, node.leaderPredicate, "subject_namespace")
	assert.Equal(t, "gke-pool-abc", node.arg, "node normalised the way the SQL side is")
	assert.Equal(t, IncidentAttachWindow, node.window)

	// A node-typed event with no node name has nothing better than the namespace
	// rule — it must not produce a predicate matching on an empty node.
	noName := unlocatedScopeFor(&models.Event{Source: src("prometheus"), SubjectType: src("node")})
	assert.Contains(t, noName.leaderPredicate, "subject_namespace")

	// subject_node is also set on pod events (the node the pod runs on). Those
	// keep the namespace rule — the pod's namespace is the tighter boundary.
	pod := unlocatedScopeFor(&models.Event{
		Source:           src("prometheus"),
		SubjectType:      src("pod"),
		SubjectNode:      src("gke-pool-abc"),
		SubjectNamespace: src("checkout"),
	})
	assert.Contains(t, pod.leaderPredicate, "subject_namespace")
	assert.Equal(t, "checkout", pod.arg)
}

// TestTimingFallbackAllowed — co-timing is only for alerts the graph cannot
// place. Configuration changes never use it; AWS alarms always may.
func TestTimingFallbackAllowed(t *testing.T) {
	str := func(s string) *string { return &s }
	k8s := &models.Event{Source: str("prometheus"), FindingType: str("issue")}
	aws := &models.Event{Source: str("AWS_CloudWatch_Alarm"), FindingType: str("issue")}
	change := &models.Event{Source: str("kubernetes_api_server"), FindingType: str("configuration_change")}

	assert.True(t, timingFallbackAllowed(k8s, false), "unplaced Kubernetes alert falls back to timing")
	assert.False(t, timingFallbackAllowed(k8s, true), "placed Kubernetes alert with quiet neighbours does not")

	assert.True(t, timingFallbackAllowed(aws, false))
	assert.True(t, timingFallbackAllowed(aws, true), "AWS alarms keep timing even when placed — the graph often cannot connect them")

	assert.False(t, timingFallbackAllowed(change, false), "a deploy event never groups on timing")
	assert.False(t, timingFallbackAllowed(change, true))
	assert.False(t, timingFallbackAllowed(&models.Event{FindingType: str(" Configuration_Change ")}, false), "matched case- and space-insensitively")

	assert.True(t, timingFallbackAllowed(&models.Event{}, false), "no source, no finding type, unplaced: fallback allowed")
}
