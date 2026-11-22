package providers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiskObservationRange(t *testing.T) {
	now := time.Date(2026, 10, 9, 16, 37, 42, 123, time.FixedZone("IST", 5*3600+1800))

	start, end := DiskObservationRange(now)

	// 16:37:42 IST is 11:07:42 UTC, which truncates to the 11:05 boundary.
	assert.Equal(t, time.Date(2026, 10, 9, 11, 5, 0, 0, time.UTC), end)
	assert.Equal(t, time.Date(2026, 9, 9, 11, 5, 0, 0, time.UTC), start)
	assert.Equal(t, int64(8640), int64(end.Sub(start)/DiskMetricPeriod))
}

func TestMergeMetricItems(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * DiskMetricPeriod) }

	t.Run("no items", func(t *testing.T) {
		assert.Nil(t, MergeMetricItems(nil))
	})

	t.Run("items with no points", func(t *testing.T) {
		assert.Nil(t, MergeMetricItems([]MetricItem{{Name: "VolumeReadOps"}, {Name: "VolumeReadOps"}}))
	})

	t.Run("pages in descending order are joined ascending", func(t *testing.T) {
		merged := MergeMetricItems([]MetricItem{
			{Name: "VolumeReadOps", Statistics: "Sum", Values: []float64{50, 40}, Timestamps: []time.Time{at(4), at(3)}},
			{Name: "VolumeReadOps", Statistics: "Sum", Values: []float64{20, 10}, Timestamps: []time.Time{at(1), at(0)}},
		})
		require.NotNil(t, merged)
		assert.Equal(t, "VolumeReadOps", merged.Name)
		assert.Equal(t, "Sum", merged.Statistics)
		assert.Equal(t, []float64{10, 20, 40, 50}, merged.Values)
		assert.Equal(t, []time.Time{at(0), at(1), at(3), at(4)}, merged.Timestamps)
	})

	t.Run("a zero value is a measurement and is kept", func(t *testing.T) {
		merged := MergeMetricItems([]MetricItem{{Values: []float64{0, 5}, Timestamps: []time.Time{at(0), at(1)}}})
		require.NotNil(t, merged)
		assert.Equal(t, []float64{0, 5}, merged.Values)
	})

	t.Run("a repeated timestamp keeps the first value", func(t *testing.T) {
		merged := MergeMetricItems([]MetricItem{
			{Values: []float64{10}, Timestamps: []time.Time{at(0)}},
			{Values: []float64{99, 20}, Timestamps: []time.Time{at(0), at(1)}},
		})
		require.NotNil(t, merged)
		assert.Equal(t, []float64{10, 20}, merged.Values)
	})

	t.Run("values without a timestamp are dropped", func(t *testing.T) {
		merged := MergeMetricItems([]MetricItem{{Values: []float64{10, 20, 30}, Timestamps: []time.Time{at(0), at(1)}}})
		require.NotNil(t, merged)
		assert.Equal(t, []float64{10, 20}, merged.Values)
		assert.Len(t, merged.Timestamps, 2)
	})

	t.Run("the inputs are not modified", func(t *testing.T) {
		items := []MetricItem{{Values: []float64{2, 1}, Timestamps: []time.Time{at(1), at(0)}}}
		MergeMetricItems(items)
		assert.Equal(t, []float64{2, 1}, items[0].Values)
		assert.Equal(t, []time.Time{at(1), at(0)}, items[0].Timestamps)
	})
}
