package observability

import (
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"strings"
	"testing"
)

// memorySeries builds n samples one minute apart, each stepMiB larger than the
// last, in the wire shape the enricher produces: byte values as strings,
// timestamps as float seconds.
func memorySeries(container string, limitMiB float64, n int, startMiB, stepMiB float64) podMetricEntry {
	entry := podMetricEntry{
		Metric: map[string]any{
			"container": container,
			"limits":    map[string]any{"memory": limitMiB * bytesPerMiB, "cpu": 0},
		},
	}
	for i := 0; i < n; i++ {
		entry.Timestamps = append(entry.Timestamps, float64(1757505600+i*60))
		entry.Values = append(entry.Values, fmt.Sprintf("%.0f", (startMiB+float64(i)*stepMiB)*bytesPerMiB))
	}
	return entry
}

func appendSamples(entry podMetricEntry, valuesMiB ...float64) podMetricEntry {
	next := entry.Timestamps[len(entry.Timestamps)-1]
	for i, v := range valuesMiB {
		entry.Timestamps = append(entry.Timestamps, next+float64((i+1)*60))
		entry.Values = append(entry.Values, fmt.Sprintf("%.0f", v*bytesPerMiB))
	}
	return entry
}

func onlyInsight(t *testing.T, entry podMetricEntry) playbooks.PlaybookActionResponseInsight {
	t.Helper()
	insights := memoryTrendInsights([]podMetricEntry{entry}, "memory")
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight; got %d", len(insights))
	}
	return insights[0]
}

// The distinction the OOMKilled event cannot make on its own: a working set
// that climbs and never falls back is retaining something, so a bigger limit
// only moves the deadline.
func TestMemoryTrend_UnboundedGrowth(t *testing.T) {
	got := onlyInsight(t, memorySeries("api", 192, 30, 30, 5))
	if !strings.Contains(got.Message, "rose 483% and never fell more than 10% below its running peak") {
		t.Errorf("monotonic growth must be called out with what was measured; got %q", got.Message)
	}
	if !strings.Contains(got.Message, "192MiB limit") {
		t.Errorf("the limit belongs in the summary; got %q", got.Message)
	}
	if !strings.Contains(got.Message, "before changing the limit") {
		t.Errorf("summary should push back on the reflexive limit bump; got %q", got.Message)
	}
	if !strings.Contains(got.Message, "Container api") {
		t.Errorf("the container should be named; got %q", got.Message)
	}
	if got.Severity != "High" {
		t.Errorf("severity = %q; unbounded retention changes the recommended action", got.Severity)
	}
}

func TestMemoryTrend_SawtoothIsNotUnbounded(t *testing.T) {
	// Rises, gives memory back, rises again — a workload that does release.
	entry := appendSamples(memorySeries("api", 192, 10, 40, 5), 45, 50, 55, 60, 65)
	got := onlyInsight(t, entry)
	if strings.Contains(got.Message, "running peak") {
		t.Errorf("a series that falls back must not read as unbounded; got %q", got.Message)
	}
	if !strings.Contains(got.Message, "cannot tell apart") {
		t.Errorf("expected the honest fall-back reading; got %q", got.Message)
	}
	if got.Severity != "Info" {
		t.Errorf("severity = %q; a fall-back is not the alarming case", got.Severity)
	}
}

// Caught by running scenario 3 end-to-end: the OOMKilled container had grown
// 9MiB → 185MiB and was killed, but the reported series was summed across two
// pod incarnations during the restart, so the dying pod leaving the sum read as
// "the workload does release memory" — the exact inverse, on the exact case
// this insight exists for. Every provider's utilisation query wraps in sum()
// with no `by (pod)`, so the series can never be attributed to one pod.
func TestMemoryTrend_FallBackDoesNotClaimTheWorkloadReleasedMemory(t *testing.T) {
	entry := memorySeries("api", 384, 20, 9, 16) // climbs to ~313MiB
	entry = appendSamples(entry, 160, 168, 176, 184, 192)
	got := onlyInsight(t, entry)
	if strings.Contains(got.Message, "does release memory") {
		t.Errorf("a drop in a pod-summed series must not be read as a release; got %q", got.Message)
	}
	if !strings.Contains(got.Message, "a pod left the set") {
		t.Errorf("the alternative explanation must be stated; got %q", got.Message)
	}
}

// The numbers are a total across pods and the limit is summed to match; saying
// otherwise invites an operator to compare them against a pod spec that reads
// half as large.
func TestMemoryTrend_LabelsTheSeriesAsAWorkloadTotal(t *testing.T) {
	got := onlyInsight(t, memorySeries("api", 384, 30, 30, 5))
	if !strings.Contains(got.Message, "across the workload's pods") {
		t.Errorf("the summing must be stated; got %q", got.Message)
	}
	if !strings.Contains(got.Message, "combined 384MiB limit") {
		t.Errorf("the limit must be marked as combined; got %q", got.Message)
	}
}

// A container that OOMs and restarts drops to near zero. That is the kill, not
// the workload releasing memory under load — reading it as a sawtooth inverts
// the conclusion.
func TestMemoryTrend_RestartDropIsNotRelease(t *testing.T) {
	entry := appendSamples(memorySeries("api", 192, 20, 30, 8), 4, 12, 20, 28, 36)
	got := onlyInsight(t, entry)
	if strings.Contains(got.Message, "does release memory") {
		t.Errorf("an OOM restart must not read as releasing memory; got %q", got.Message)
	}
}

// A slow decline — a GC handing memory back over several scrapes, or replicas
// draining away — never drops 10% between two adjacent samples. Comparing
// against the previous sample read this as unbroken growth, which is the one
// conclusion that must not be produced by accident: it is what says "no limit
// is large enough".
func TestMemoryTrend_GradualDeclineIsAFallBack(t *testing.T) {
	entry := memorySeries("api", 512, 12, 200, 0) // flat at the peak
	entry = appendSamples(entry, 195, 190, 185, 180, 175, 170, 165, 160)
	got := onlyInsight(t, entry)
	if strings.Contains(got.Message, "running peak") {
		t.Errorf("a steady decline is a release, not unbounded growth; got %q", got.Message)
	}
	if !strings.Contains(got.Message, "fell back within the window") {
		t.Errorf("expected the fall-back reading; got %q", got.Message)
	}
}

// The mirror case, and why the drop is not measured against the peak
// alone: after an OOM restart the container climbs back from near zero, so
// every sample sits below the old peak while the workload is doing the
// opposite of releasing.
func TestMemoryTrend_ClimbingBackAfterRestartIsNotARelease(t *testing.T) {
	entry := memorySeries("api", 192, 20, 30, 8) // climbs to ~182MiB
	entry = appendSamples(entry, 4, 12, 20, 28, 36, 44, 52, 60)
	got := onlyInsight(t, entry)
	if strings.Contains(got.Message, "does release memory") {
		t.Errorf("recovery after an OOM restart must not read as a release; got %q", got.Message)
	}
}

// A restart drops below the floor that keeps it out of "dropped", but it is
// still a fall: what climbs afterwards is the container starting up, and the
// series never held near its peak. Reading that climb as retention claimed
// "without ever falling back" on a series that had just fallen to near zero.
func TestMemoryTrend_StartupAfterRestartIsNotRetention(t *testing.T) {
	entry := memorySeries("api", 192, 20, 30, 8) // climbs to ~182MiB
	entry = appendSamples(entry, 4, 12, 20, 28, 36, 44, 52, 60)
	got := onlyInsight(t, entry)
	if got.Severity == "High" || strings.Contains(got.Message, "running peak") {
		t.Errorf("growth after a restart must not read as retention; got %q", got.Message)
	}
}

// memoryValues builds a one-minute series from explicit MiB readings.
func memoryValues(limitMiB float64, valuesMiB ...float64) podMetricEntry {
	return appendSamples(memorySeries("api", limitMiB, 1, valuesMiB[0], 0), valuesMiB[1:]...)
}

// Real series from dev over a ~10-minute window, each of which got the High
// "grew without ever falling back … retained" reading because it ended above
// its first sample — by a single MiB, or after dips, or not at all. None grew
// materially; the flat ones must say so.
func TestMemoryTrend_NoMaterialGrowthIsNotRetention(t *testing.T) {
	cases := []struct {
		name     string
		entry    podMetricEntry
		wantFlat string
	}{
		{"flat", memorySeries("api", 0, 11, 207, 0), "between 207MiB and 207MiB"},
		{"one MiB up after dips", memoryValues(0, 193, 195, 191, 198, 204, 200, 196, 194, 192, 193, 194), "between 191MiB and 204MiB"},
		{"dip then back", memoryValues(0, 52.8, 53.0, 52.5, 50.1, 51.7, 52.9, 53.3, 52.6, 53.0, 53.1), "between 50MiB and 53MiB"},
		{"bump and settle", memoryValues(0, 197, 203, 212, 208, 201, 198, 199, 200, 198, 200), "between 197MiB and 212MiB"},
		{"flat under the limit", memoryValues(300, 229, 229, 230, 229, 228, 229, 229, 230, 229, 229, 229), "between 228MiB and 230MiB"},
		// Dips 9% and recovers: neither a fall-back nor flat nor growth.
		{"vmagent dip and recover", memoryValues(0, 1373, 1380, 1352, 1300, 1249, 1275, 1320, 1356, 1371, 1388), ""},
		// 40%, but 8MiB: allocator noise on a small container, not retention.
		{"small container", memoryValues(64, 20, 21, 22, 23, 24, 25, 26, 27, 28), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := onlyInsight(t, tc.entry)
			if got.Severity != "Info" || strings.Contains(got.Message, "running peak") {
				t.Errorf("no material growth must not read as retention; got %s %q", got.Severity, got.Message)
			}
			if tc.wantFlat != "" && !strings.Contains(got.Message, "held flat, "+tc.wantFlat) {
				t.Errorf("a series inside a 10%% band must say it held flat (%s); got %q", tc.wantFlat, got.Message)
			}
			if tc.wantFlat == "" && strings.Contains(got.Message, "held flat") {
				t.Errorf("a series outside the 10%% band is not flat; got %q", got.Message)
			}
		})
	}
}

// The case this insight exists for must survive the growth threshold: a
// container climbing towards its limit, with the small dips a real series has,
// still reads as held — and the message states only what the series did.
func TestMemoryTrend_ClimbToLimitWithSmallDipsIsStillRetention(t *testing.T) {
	got := onlyInsight(t, memoryValues(192, 150, 155, 159, 157, 164, 170, 168, 176, 182, 186, 190))
	if got.Severity != "High" {
		t.Errorf("severity = %q; a 27%% climb to the limit is the case this insight exists for", got.Severity)
	}
	if !strings.Contains(got.Message, "rose 27% and never fell more than 10% below its running peak") {
		t.Errorf("the message must state the measured rise; got %q", got.Message)
	}
}

// A series whose first sample is zero has no ratio to report; it must not
// render "+Inf%" or fire on the division.
func TestMemoryTrend_ZeroFirstSampleIsNotRetention(t *testing.T) {
	got := onlyInsight(t, memoryValues(192, 0, 0, 20, 40, 60, 80, 100))
	if got.Severity == "High" || strings.Contains(got.Message, "Inf") {
		t.Errorf("a zero start has no growth ratio; got %s %q", got.Severity, got.Message)
	}
}

func TestMemoryTrend_TooFewSamplesSaysNothing(t *testing.T) {
	if insights := memoryTrendInsights([]podMetricEntry{memorySeries("api", 192, 2, 30, 5)}, "memory"); len(insights) != 0 {
		t.Errorf("two points cannot characterise a trend; got %v", insights)
	}
}

// The cpu variant of this action runs the same code path. Describing CPU cores
// as a working set would be wrong, and emitting from both variants would double
// every insight.
func TestMemoryTrend_OnlyForMemorySeries(t *testing.T) {
	for _, resourceType := range []string{"CPU", "cpu", ""} {
		if insights := memoryTrendInsights([]podMetricEntry{memorySeries("api", 192, 30, 30, 5)}, resourceType); len(insights) != 0 {
			t.Errorf("resource_type %q must not produce a memory trend; got %v", resourceType, insights)
		}
	}
}

// The OOMKilled pod whose container is already gone: the enricher seeds entries
// from requests/limits alone, with no samples at all.
func TestMemoryTrend_EmptySeriesIsSkipped(t *testing.T) {
	entry := podMetricEntry{Metric: map[string]any{"container": "api"}, Timestamps: []float64{}, Values: []string{}}
	if insights := memoryTrendInsights([]podMetricEntry{entry}, "memory"); len(insights) != 0 {
		t.Errorf("expected no insight for an empty series; got %v", insights)
	}
}

// Prometheus sends values as strings precisely so NaN and Inf survive JSON,
// and ParseFloat accepts both. Every comparison against NaN is false, so a
// single NaN sample would silently defeat the peak and fall-back detection
// this file is built on — and could be rendered to the reader as "NaN MiB".
func TestMemoryTrend_NaNAndInfAreDropped(t *testing.T) {
	entry := memorySeries("api", 192, 30, 30, 5)
	entry.Values[3] = "NaN"
	entry.Values[7] = "+Inf"
	got := onlyInsight(t, entry)
	if strings.Contains(got.Message, "NaN") || strings.Contains(got.Message, "Inf") {
		t.Errorf("NaN/Inf must never reach the summary; got %q", got.Message)
	}
	// The remaining samples still describe a curve that only grows.
	if got.Severity != "High" || !strings.Contains(got.Message, "running peak") {
		t.Errorf("dropping NaN/Inf must not change the reading; got %q", got.Message)
	}
}

func TestMemoryTrend_UnparseableSamplesAreDroppedNotZeroed(t *testing.T) {
	entry := memorySeries("api", 192, 10, 100, 5)
	entry.Values[4] = "not-a-number"
	got := onlyInsight(t, entry)
	// A phantom 0 would read as the workload releasing everything.
	if strings.Contains(got.Message, "does release memory") {
		t.Errorf("a dropped sample must not read as a memory release; got %q", got.Message)
	}
}

func TestMemoryTrend_NoLimitOmitsTheComparison(t *testing.T) {
	entry := memorySeries("api", 0, 30, 30, 5)
	got := onlyInsight(t, entry)
	if strings.Contains(got.Message, "limit") && !strings.Contains(got.Message, "before changing the limit") {
		t.Errorf("no limit set — the summary must not claim one; got %q", got.Message)
	}
}

func TestMemoryTrend_OneInsightPerContainer(t *testing.T) {
	entries := []podMetricEntry{
		memorySeries("api", 192, 30, 30, 5),
		memorySeries("sidecar", 64, 30, 10, 1),
	}
	insights := memoryTrendInsights(entries, "memory")
	if len(insights) != 2 {
		t.Fatalf("expected one insight per container; got %d", len(insights))
	}
	if !strings.Contains(insights[0].Message, "api") || !strings.Contains(insights[1].Message, "sidecar") {
		t.Errorf("insights should name their containers; got %v", insights)
	}
}

// A series that is zero throughout (a container that never allocated, or a
// scrape that reports 0) still reads as flat rather than falling through to the
// bare summary.
func TestMemoryTrend_AllZeroSeriesIsFlat(t *testing.T) {
	got := onlyInsight(t, memoryValues(192, 0, 0, 0, 0, 0, 0))
	if got.Severity != "Info" || !strings.Contains(got.Message, "held flat") {
		t.Errorf("an all-zero series is flat; got %s %q", got.Severity, got.Message)
	}
}
