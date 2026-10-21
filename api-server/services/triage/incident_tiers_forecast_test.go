package triage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDerivedSignal_CPUForecasts(t *testing.T) {
	for _, ft := range []string{"CPUThrottlingPredicted", "CPUSaturationPredicted", "CPUThrottlingSLOBreached", "CPUSaturationSLOBreached", "SLO", "Anomaly"} {
		assert.True(t, isDerivedSignal(AlertIdentity{FindingType: ft}), ft)
	}
	assert.False(t, isDerivedSignal(AlertIdentity{FindingType: "CPUThrottlingHigh"}), "the real alert is a failure, not a prediction")
}
