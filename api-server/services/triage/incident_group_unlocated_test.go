package triage

import (
	"context"
	"os"
	"testing"
	"time"

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

	for _, s := range awsAlarmSources {
		aws := unlocatedScopeFor(&models.Event{Source: src(s), SubjectNamespace: src("AWSELB")})
		assert.Contains(t, aws.leaderPredicate, "l.source", "%s is scoped by source, not by service code", s)
		assert.NotContains(t, aws.leaderPredicate, "subject_namespace", "%s may cross AWS services", s)
		assert.Equal(t, "AWS", aws.label)
	}

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

// TestCollectConnectedMembers_LocatedWithoutNeighbours — the seed has a stored
// map, so it is placed, even when topology grouping is switched off and even
// though nothing connected is alerting. No database is touched on these paths.
func TestCollectConnectedMembers_LocatedWithoutNeighbours(t *testing.T) {
	str := func(s string) *string { return &s }
	evidence := `[{"type": "knowledge_graph", "nodes": [
	  {"id": "n1", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "search", "namespace": "shop"}}
	], "edges": []}]`
	var j models.Json
	if err := j.Scan([]uint8(evidence)); err != nil {
		t.Fatal(err)
	}
	ev := &models.Event{
		Id: "e1", SubjectNamespace: str("shop"), SubjectName: str("search-abc12"),
		SubjectOwner: str("search"), SubjectOwnerKind: str("Deployment"), SubjectType: str("pod"),
		Evidences: &j,
	}

	t.Setenv(topologyGroupingEnvFlag, "false")
	pool, err := collectConnectedMembers(context.Background(), nil, ev, groupCandidate{ID: "e1"}, "shop|search", time.Now())
	assert.NoError(t, err)
	assert.True(t, pool.located, "placed regardless of the topology kill switch")
	assert.Empty(t, pool.members)

	ev.Evidences = nil
	pool, err = collectConnectedMembers(context.Background(), nil, ev, groupCandidate{ID: "e1"}, "shop|search", time.Now())
	assert.NoError(t, err)
	assert.False(t, pool.located, "no stored map — not placed")
}
