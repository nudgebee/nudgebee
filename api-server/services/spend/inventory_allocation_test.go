package spend

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectorSumColumns and collectorAvgColumns mirror the lists in the k8s-collector's
// spend_handler.py. They are duplicated here on purpose: get_metric_data indexes each
// one directly (`k8s_data[key]`), so a column missing from the payload is a KeyError
// that fails the whole ingest rather than a skipped metric. This test is the only
// thing standing between a renamed struct tag and a broken spend sync.
var collectorSumColumns = []string{
	"cpuCores", "cpuCoreHours", "cpuCost", "cpuCostAdjustment",
	"gpuCount", "gpuHours", "gpuCost", "gpuCostAdjustment",
	"networkTransferBytes", "networkReceiveBytes", "networkCost",
	"networkCrossZoneCost", "networkCrossRegionCost", "networkInternetCost",
	"networkCostAdjustment", "loadBalancerCost", "loadBalancerCostAdjustment",
	"pvBytes", "pvByteHours", "pvCost", "pvCostAdjustment",
	"ramBytes", "ramByteHours", "ramCost", "ramCostAdjustment",
	"externalCost", "sharedCost", "totalCost",
}

var collectorAvgColumns = []string{
	"cpuCoreRequestAverage", "cpuCoreUsageAverage", "cpuEfficiency",
	"ramByteRequestAverage", "ramByteUsageAverage", "ramEfficiency", "totalEfficiency",
}

func TestAllocationEntryCarriesEveryColumnTheCollectorIndexes(t *testing.T) {
	raw, err := json.Marshal(allocationEntry{})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	for _, col := range append(append([]string{}, collectorSumColumns...), collectorAvgColumns...) {
		assert.Containsf(t, got, col, "process_spend does k8s_data[%q] and would raise KeyError", col)
	}
}

// The node's whole hourly price must end up distributed across CPU and RAM: the
// components are what land in cloud_resource_metrics, and their sum is the node's
// totalCost, which is the spends amount.
func TestSplitNodePricePreservesTheNodePrice(t *testing.T) {
	const cores, ramGiB, hourly = 2.0, 7.5, 0.103
	p := splitNodePrice(cores, ramGiB, hourly)

	assert.InDelta(t, hourly, p.CPUPerCoreHr*cores+p.RAMPerGiBHr*ramGiB, 1e-9)
	// CPU is the more expensive resource per unit, per OpenCost's default ratio.
	assert.Greater(t, p.CPUPerCoreHr, p.RAMPerGiBHr)
}

// A node with no resolved price (or no capacity) must produce zero, not NaN: a NaN
// serialises as invalid JSON and fails the collector's ingest for the whole account.
func TestSplitNodePriceNeverProducesNaN(t *testing.T) {
	for _, tc := range []struct{ cores, ramGiB, hourly float64 }{
		{0, 0, 0.103}, // no capacity recorded
		{2, 7.5, 0},   // no price for this instance type
		{0, 0, 0},
	} {
		p := splitNodePrice(tc.cores, tc.ramGiB, tc.hourly)
		assert.Zero(t, p.CPUPerCoreHr)
		assert.Zero(t, p.RAMPerGiBHr)
	}
}

func TestParseAllocationStep(t *testing.T) {
	assert.Equal(t, 24*time.Hour, parseAllocationStep("1d"))
	assert.Equal(t, 48*time.Hour, parseAllocationStep("2d"))
	assert.Equal(t, time.Hour, parseAllocationStep("1h"))
	// Anything unusable falls back to a day, which is the granularity the spends
	// primary key (…, date) can actually represent.
	assert.Equal(t, defaultAllocationStep, parseAllocationStep(""))
	assert.Equal(t, defaultAllocationStep, parseAllocationStep("garbage"))
	assert.Equal(t, defaultAllocationStep, parseAllocationStep("0d"))
}

func TestSplitAllocationIntervalsClampsTheFinalInterval(t *testing.T) {
	start := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	end := start.Add(50 * time.Hour) // two days and two hours

	got := splitAllocationIntervals(start, end, 24*time.Hour)
	require.Len(t, got, 3)
	// The tail must not extend past the window, or the last day bills 24 hours for
	// two hours of coverage.
	assert.Equal(t, end, got[2].End)
	assert.Equal(t, 2.0, got[2].End.Sub(got[2].Start).Hours())
}

func TestSplitAllocationIntervalsRejectsAnEmptyWindow(t *testing.T) {
	now := time.Now()
	assert.Nil(t, splitAllocationIntervals(now, now, time.Hour))
	assert.Nil(t, splitAllocationIntervals(now, now.Add(-time.Hour), time.Hour))
	assert.Nil(t, splitAllocationIntervals(now, now.Add(time.Hour), 0))
}

func TestOverlapHours(t *testing.T) {
	day := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	next := day.Add(24 * time.Hour)

	// Alive for the whole day.
	assert.Equal(t, 24.0, overlapHours(day, next, day.Add(-time.Hour), next.Add(time.Hour)))
	// Created at midday.
	assert.Equal(t, 12.0, overlapHours(day, next, day.Add(12*time.Hour), next))
	// Gone before the interval.
	assert.Equal(t, 0.0, overlapHours(day, next, day.Add(-48*time.Hour), day.Add(-24*time.Hour)))
	// Touching but not overlapping.
	assert.Equal(t, 0.0, overlapHours(day, next, next, next.Add(time.Hour)))
}

// A still-running resource must be billed to the end of the window, not to its last
// heartbeat: last_seen/updated_at lag by a discovery cycle, so charging only up to it
// undercounts every window.
func TestActiveRangeExtendsLiveResourcesToTheWindowEnd(t *testing.T) {
	created := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	lastSeen := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)

	_, end := activeRange(created, lastSeen, true, windowEnd)
	assert.Equal(t, windowEnd, end)

	// A deleted resource stops at its last sighting.
	_, end = activeRange(created, lastSeen, false, windowEnd)
	assert.Equal(t, lastSeen, end)

	// No last sighting recorded at all: fall back to the window end rather than to
	// the zero time, which would silently produce zero hours for every resource.
	_, end = activeRange(created, time.Time{}, false, windowEnd)
	assert.Equal(t, windowEnd, end)
}

// Kubernetes quantities are not plain numbers. Memory in particular is stored in
// forms like "1395864371200m" — milli-bytes — which a suffix-stripping parser reads
// as 1.4e12 bytes instead of 1.4e9, a 1000x overcharge.
func TestPodRequestsParsesKubernetesQuantities(t *testing.T) {
	meta := []byte(`{"config":{"containers":[
	  {"resources":{"requests":{"cpu":"334m","memory":"1395864371200m"}}},
	  {"resources":{"requests":{"cpu":"1","memory":"557Mi"}}}
	]}}`)

	cpu, ram := podRequests(meta)
	assert.InDelta(t, 1.334, cpu, 1e-9)
	assert.InDelta(t, 1395864371.2+557*1024*1024, ram, 1.0)
}

func TestPodRequestsToleratesMissingAndMalformedSpecs(t *testing.T) {
	for _, meta := range []string{
		`{}`,
		`{"config":{}}`,
		`{"config":{"containers":[{"resources":{"requests":{}}}]}}`,
		`not json`,
		`{"config":{"containers":[{"resources":{"requests":{"cpu":"not-a-quantity"}}}]}}`,
	} {
		cpu, ram := podRequests([]byte(meta))
		assert.Zero(t, cpu, meta)
		assert.Zero(t, ram, meta)
	}
}

// BestEffort pods are real and common. They must contribute zero cost rather than
// being dropped or defaulting to a share of the node.
func TestPodRequestsIsZeroForBestEffortPods(t *testing.T) {
	meta := []byte(`{"config":{"containers":[
	  {"resources":{"limits":{},"requests":{}}},
	  {"resources":{"limits":{},"requests":{}}}
	]}}`)
	cpu, ram := podRequests(meta)
	assert.Zero(t, cpu)
	assert.Zero(t, ram)
}

// The window the collector reads must be parseable by Python's
// datetime.fromisoformat. A "Z" suffix only parses from Python 3.11.
func TestAllocationWindowUsesAnOffsetPythonCanParse(t *testing.T) {
	start := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	got := start.Format("2006-01-02T15:04:05-07:00")
	assert.Equal(t, "2026-08-25T00:00:00+00:00", got)
	assert.NotContains(t, got, "Z")
}

// A non-finite price must never reach the payload: json.Marshal rejects NaN and ±Inf
// with UnsupportedValueError, which fails the collector's ingest for the WHOLE account
// rather than skipping the offending node. The two arrive by different routes — every
// comparison against NaN is false so it slips past `hourlyCost <= 0`, while +Inf
// passes that check outright — and Postgres float8 stores both, so each is reachable
// from k8s_nodes.cost. The marshal assertion is the real contract; zero is just how
// we satisfy it.
func TestSplitNodePriceRejectsNonFinitePrices(t *testing.T) {
	cases := map[string]nodePricing{
		"NaN price":         splitNodePrice(2, 7.5, math.NaN()),
		"+Inf price":        splitNodePrice(2, 7.5, math.Inf(1)),
		"-Inf price":        splitNodePrice(2, 7.5, math.Inf(-1)),
		"NaN cpu capacity":  splitNodePrice(math.NaN(), 7.5, 0.4),
		"+Inf cpu capacity": splitNodePrice(math.Inf(1), 7.5, 0.4),
		"NaN ram capacity":  splitNodePrice(2, math.NaN(), 0.4),
		"+Inf ram capacity": splitNodePrice(2, math.Inf(1), 0.4),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := json.Marshal(p)
			require.NoError(t, err, "non-finite pricing would fail the account's ingest")
			assert.Zero(t, p.CPUPerCoreHr)
			assert.Zero(t, p.RAMPerGiBHr)
		})
	}
}

// A StatefulSet pod keeps its name across a restart, so one window can hold the old
// instance and the new one. Overwriting charged only the last and silently dropped
// the hours the earlier instance ran.
func TestRepeatedPodNameAccumulatesRatherThanOverwrites(t *testing.T) {
	first := allocationEntry{
		CPUCores: 0.5, RAMBytes: 100, CPUCoreRequestAverage: 0.5, RAMByteRequestAverage: 100,
		CPUCoreHours: 6, RAMByteHours: 600, CPUCost: 1.0, RAMCost: 0.5, TotalCost: 1.5,
	}
	second := allocationEntry{
		CPUCores: 0.5, RAMBytes: 100, CPUCoreRequestAverage: 0.5, RAMByteRequestAverage: 100,
		CPUCoreHours: 6, RAMByteHours: 600, CPUCost: 1.0, RAMCost: 0.5, TotalCost: 1.5,
	}

	got := mergeAllocationEntries(first, second)

	// Consumption adds: the pod really did run for twelve core-hours across the two.
	assert.Equal(t, 12.0, got.CPUCoreHours)
	assert.Equal(t, 3.0, got.TotalCost)
	// TotalCost must stay the sum of its components after a merge.
	assert.Equal(t, got.CPUCost+got.RAMCost, got.TotalCost)
	// Levels do not: two sequential instances did not reserve one core between them.
	assert.Equal(t, 0.5, got.CPUCores)
	assert.Equal(t, 100.0, got.RAMBytes)
	assert.Equal(t, 0.5, got.CPUCoreRequestAverage)
}

// A larger later instance (a pod resized on restart) reports the larger reservation.
func TestMergeTakesTheLargerReservation(t *testing.T) {
	got := mergeAllocationEntries(
		allocationEntry{CPUCores: 0.25, RAMBytes: 50},
		allocationEntry{CPUCores: 1.0, RAMBytes: 200},
	)
	assert.Equal(t, 1.0, got.CPUCores)
	assert.Equal(t, 200.0, got.RAMBytes)
}
