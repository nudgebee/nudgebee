package triage

import (
	"os"
	"testing"
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
