package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiskPerformanceSourceFor(t *testing.T) {
	tests := []struct {
		name       string
		known      bool
		adjustable bool
		reported   bool
		want       DiskPerformanceSource
	}{
		{"adjustable and reported is explicit", true, true, true, DiskPerformanceExplicit},
		{"adjustable but not reported is unavailable", true, true, false, DiskPerformanceUnavailable},
		{"not adjustable is derived when reported", true, false, true, DiskPerformanceDerived},
		{"not adjustable is derived when not reported", true, false, false, DiskPerformanceDerived},
		{"unknown type is unavailable even when reported", false, false, true, DiskPerformanceUnavailable},
		{"unknown type is unavailable", false, false, false, DiskPerformanceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DiskPerformanceSourceFor(tt.known, tt.adjustable, tt.reported))
		})
	}
}

func TestDiskConfigHasAdjustablePerformance(t *testing.T) {
	tests := []struct {
		name       string
		iops       DiskPerformanceSource
		throughput DiskPerformanceSource
		want       bool
	}{
		{"both explicit", DiskPerformanceExplicit, DiskPerformanceExplicit, true},
		{"only iops explicit", DiskPerformanceExplicit, DiskPerformanceDerived, true},
		{"only throughput explicit", DiskPerformanceDerived, DiskPerformanceExplicit, true},
		{"both derived", DiskPerformanceDerived, DiskPerformanceDerived, false},
		{"both unavailable", DiskPerformanceUnavailable, DiskPerformanceUnavailable, false},
		{"zero value", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DiskConfig{IOPSSource: tt.iops, ThroughputSource: tt.throughput}
			assert.Equal(t, tt.want, config.HasAdjustablePerformance())
		})
	}
}

func TestMBpsToMiBps(t *testing.T) {
	assert.InDelta(t, 0.0, MBpsToMiBps(0), 1e-9)
	assert.InDelta(t, 0.95367431640625, MBpsToMiBps(1), 1e-9)
	assert.InDelta(t, 119.20928955078125, MBpsToMiBps(125), 1e-9)
	// 1,048,576 MB/s is exactly 1,000,000 MiB/s.
	assert.InDelta(t, 1_000_000.0, MBpsToMiBps(1_048_576), 1e-6)
}

func TestMiBpsToMBps(t *testing.T) {
	assert.InDelta(t, 1.048576, MiBpsToMBps(1), 1e-9)
	// The two conversions undo each other.
	assert.InDelta(t, 125.0, MiBpsToMBps(MBpsToMiBps(125)), 1e-9)
	assert.InDelta(t, 140.0, MBpsToMiBps(MiBpsToMBps(140)), 1e-9)
}

func TestPositiveMetaFloat(t *testing.T) {
	resource := Resource{Meta: map[string]any{
		"Size":       float64(100),
		"IntSize":    int64(50),
		"Zero":       float64(0),
		"Negative":   float64(-1),
		"Text":       "gp3",
		"Null":       nil,
		"properties": map[string]any{"diskSizeGB": float64(256)},
	}}

	tests := []struct {
		name string
		path string
		want *float64
	}{
		{"float value", "Meta.Size", floatPtr(100)},
		{"integer value", "Meta.IntSize", floatPtr(50)},
		{"nested value", "Meta.properties.diskSizeGB", floatPtr(256)},
		{"zero is not set", "Meta.Zero", nil},
		{"negative is not set", "Meta.Negative", nil},
		{"non-numeric text", "Meta.Text", nil},
		{"null", "Meta.Null", nil},
		{"missing field", "Meta.Missing", nil},
		{"missing nested field", "Meta.properties.missing", nil},
		{"path through a non-map", "Meta.Size.inner", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PositiveMetaFloat(resource, tt.path))
		})
	}
}

func TestMetaStringAndMetaStrings(t *testing.T) {
	resource := Resource{Meta: map[string]any{
		"name":   "disk-1",
		"users":  []any{"vm-a", "", 7, "vm-b"},
		"zones":  []string{"1", ""},
		"number": float64(3),
	}}

	assert.Equal(t, "disk-1", MetaString(resource, "Meta.name"))
	assert.Equal(t, "", MetaString(resource, "Meta.missing"))

	assert.Equal(t, []string{"vm-a", "vm-b"}, MetaStrings(resource, "Meta.users"))
	assert.Equal(t, []string{"1"}, MetaStrings(resource, "Meta.zones"))
	assert.Nil(t, MetaStrings(resource, "Meta.number"))
	assert.Nil(t, MetaStrings(resource, "Meta.missing"))
}

func floatPtr(v float64) *float64 { return &v }
