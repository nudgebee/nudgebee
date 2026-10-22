package event

import (
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsCPUThrottlingResolvable(t *testing.T) {
	for _, key := range []string{"CPUThrottlingHigh", "CPUThrottlingPredicted", "CPUSaturationPredicted", "CPUThrottlingSLOBreached", "CPUSaturationSLOBreached"} {
		assert.True(t, isCPUThrottlingResolvable(key), key)
	}
	assert.False(t, isCPUThrottlingResolvable("KubePodCrashLooping"))
}

func TestIsMemoryLimitResolvable(t *testing.T) {
	for _, key := range []string{"PodMemoryReachingLimit", "MemorySaturationPredicted", "MemorySaturationSLOBreached"} {
		assert.True(t, isMemoryLimitResolvable(key), key)
		assert.False(t, isCPUThrottlingResolvable(key), "a memory alert never gets the CPU fix: "+key)
	}
	assert.False(t, isMemoryLimitResolvable("CPUThrottlingPredicted"))
	assert.False(t, isMemoryLimitResolvable("KubeContainerOOMKilled"))
}

func TestLabelValue(t *testing.T) {
	var labels models.Json
	require.NoError(t, labels.UnmarshalJSON([]byte(`{"container":"app","workload_kind":"deployment"}`)))
	assert.Equal(t, "app", labelValue(&labels, "container"))
	assert.Equal(t, "", labelValue(&labels, "missing"))
	assert.Equal(t, "", labelValue(nil, "container"))

	var arr models.Json
	require.NoError(t, arr.UnmarshalJSON([]byte(`[1,2]`)))
	assert.Equal(t, "", labelValue(&arr, "container"))
}
