package triage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDerivedSignal_CPUForecasts(t *testing.T) {
	for _, ft := range []string{"CPUThrottlingPredicted", "CPUSaturationPredicted", "CPUThrottlingSLOBreached", "CPUSaturationSLOBreached", "MemorySaturationPredicted", "MemorySaturationSLOBreached", "memorysaturationslobreached", "SLO", "Anomaly"} {
		assert.True(t, isDerivedSignal(AlertIdentity{FindingType: ft}), ft)
	}
	assert.False(t, isDerivedSignal(AlertIdentity{FindingType: "CPUThrottlingHigh"}), "the real alert is a failure, not a prediction")
	assert.False(t, isDerivedSignal(AlertIdentity{FindingType: "KubeContainerOOMKilled"}), "the OOM kill leads the incident, not the memory forecast")
}
