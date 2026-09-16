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
