package observability

import (
	"testing"

	"nudgebee/services/cloud"

	"github.com/stretchr/testify/assert"
)

func sampleCloudMetrics() []cloud.MetricListItem {
	return []cloud.MetricListItem{
		{
			Name:      "CPUUtilization",
			Namespace: "AWS/EC2",
			Dimensions: []map[string]string{
				{"InstanceId": "i-1"},
				{"InstanceId": "i-2"},
			},
		},
		{
			Name:      "NetworkIn",
			Namespace: "AWS/EC2",
			Dimensions: []map[string]string{
				{"InstanceId": "i-1", "AutoScalingGroupName": "asg-a"},
			},
		},
		{
			Name:       "NoDims",
			Namespace:  "AWS/EC2",
			Dimensions: nil,
		},
	}
}

func TestDimensionLabelsFromMetrics_ScopedToMetric(t *testing.T) {
	labels := dimensionLabelsFromMetrics(sampleCloudMetrics(), "NetworkIn")
	assert.Equal(t, []string{"AutoScalingGroupName", "InstanceId"}, labels)
}

func TestDimensionLabelsFromMetrics_AllMetrics(t *testing.T) {
	labels := dimensionLabelsFromMetrics(sampleCloudMetrics(), "")
	// union of keys across all metrics, sorted, deduped
	assert.Equal(t, []string{"AutoScalingGroupName", "InstanceId"}, labels)
}

func TestDimensionLabelsFromMetrics_UnknownMetric(t *testing.T) {
	assert.Empty(t, dimensionLabelsFromMetrics(sampleCloudMetrics(), "DoesNotExist"))
}

func TestDimensionValuesFromMetrics(t *testing.T) {
	// distinct InstanceId values across all metrics
	vals := dimensionValuesFromMetrics(sampleCloudMetrics(), "InstanceId", "")
	assert.Equal(t, []string{"i-1", "i-2"}, vals)

	// scoped to a single metric
	scoped := dimensionValuesFromMetrics(sampleCloudMetrics(), "InstanceId", "CPUUtilization")
	assert.Equal(t, []string{"i-1", "i-2"}, scoped)

	// dimension that only exists on NetworkIn
	asg := dimensionValuesFromMetrics(sampleCloudMetrics(), "AutoScalingGroupName", "")
	assert.Equal(t, []string{"asg-a"}, asg)

	// unknown dimension -> empty
	assert.Empty(t, dimensionValuesFromMetrics(sampleCloudMetrics(), "Nope", ""))
}

func TestRequestString(t *testing.T) {
	v, ok := requestString(map[string]any{"service_name": "AmazonEC2"}, "service_name")
	assert.True(t, ok)
	assert.Equal(t, "AmazonEC2", v)

	_, ok = requestString(nil, "service_name")
	assert.False(t, ok)

	_, ok = requestString(map[string]any{"service_name": 123}, "service_name")
	assert.False(t, ok)
}
