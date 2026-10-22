package forecast

import (
	"strings"
	"testing"

	"nudgebee/services/observability"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memorySeries is a container whose RSS climbs from 50% to ~68% of a 512 MiB limit
// over three days, with oom restarts in the last 6h.
func memorySeries(limitMiB float64, oom float64) func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
	return func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		ratio := func(h int) float64 { return 0.50 + 0.06*float64(h)/24 }
		return observability.OutputMetricQuery{Results: []observability.QueryResult{
			promResult(string(observability.KeyContainerMemoryRSSLimitRatio), testEnd, ratio),
			promResult(string(observability.KeyContainerMemoryLimitPerPodMiB), testEnd, func(int) float64 { return limitMiB }),
			promResult(string(observability.KeyContainerMemoryRSSMiB), testEnd, func(h int) float64 { return ratio(h) * limitMiB }),
			promResult(string(observability.KeyContainerRestartsLastOOM6h), testEnd, func(int) float64 { return oom }),
		}}, nil
	}
}

func TestEvaluate_MemoryIsPerPodAndScopedToItsContainer(t *testing.T) {
	var asked observability.GetUtilisationTrendRequest
	fetch := memorySeries(512, 0)
	fakes(t, "prometheus", []string{"orders-a", "orders-b", "orders-c"}, func(req observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		asked = req
		return fetch(req)
	})
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "orders", Kind: "Deployment",
		Container: "api", Condition: ConditionMemorySaturation, ThresholdPct: 80, EndTime: testEnd})

	require.Equal(t, StatusProjected, res.Status, res.Reason)
	assert.Equal(t, "api", res.Container)
	assert.Equal(t, "api", asked.Request["container_name"], "every key narrows to the container")
	assert.NotContains(t, asked.Request["metrics"], string(observability.KeyPodCount),
		"per-pod keys need no pod count, so replicas and HPA scaling cannot move them")
	assert.Equal(t, 512.0, res.PerPodLimit, "three replicas, but the limit is the per-pod 512 MiB, never 3x")
	assert.Equal(t, UnitMiB, res.LimitUnit)
	assert.Greater(t, res.SuggestedLimit, 512.0)
	assert.Zero(t, int(res.SuggestedLimit)%64, "rounded up to 64 MiB")
	assert.InDelta(t, 67.6, res.CurrentPct, 1, "the ratio, in percent, not divided by replicas")
}

func TestEvaluate_MemoryNeedsAContainer(t *testing.T) {
	fakes(t, "prometheus", []string{"p"}, func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		t.Fatal("must not fetch an unscoped memory forecast")
		return observability.OutputMetricQuery{}, nil
	})
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "orders", Condition: ConditionMemorySaturation, EndTime: testEnd})
	assert.Equal(t, StatusError, res.Status)
	assert.Contains(t, res.Reason, "needs a container")
}

// OOM restarts are evidence: present or not, the status is the same.
func TestEvaluate_MemoryOOMRestartsAreEvidenceOnly(t *testing.T) {
	run := func(oom float64) Result {
		fakes(t, "prometheus", []string{"p"}, memorySeries(512, oom))
		return Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "orders", Container: "api",
			Condition: ConditionMemorySaturation, ThresholdPct: 80, EndTime: testEnd})
	}
	quiet, oom := run(0), run(4)
	assert.Equal(t, 0, quiet.OOMRestarts6h)
	assert.Equal(t, 4, oom.OOMRestarts6h)
	assert.Equal(t, quiet.Status, oom.Status, "OOM restarts never change the status")
	assert.Equal(t, quiet.ETAHours, oom.ETAHours)
}

// A flat container above the threshold (a heap sized as a share of its limit) is
// breached, with no resize: Fit suggests a limit only for a projected breach.
func TestEvaluate_MemoryFlatAboveThresholdIsBreachedWithoutASuggestion(t *testing.T) {
	fakes(t, "prometheus", []string{"p"}, func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		return observability.OutputMetricQuery{Results: []observability.QueryResult{
			promResult(string(observability.KeyContainerMemoryRSSLimitRatio), testEnd, func(int) float64 { return 0.93 }),
			promResult(string(observability.KeyContainerMemoryLimitPerPodMiB), testEnd, func(int) float64 { return 1024 }),
			promResult(string(observability.KeyContainerMemoryRSSMiB), testEnd, func(int) float64 { return 952 }),
		}}, nil
	})
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "orders", Container: "api",
		Condition: ConditionMemorySaturation, EndTime: testEnd})
	assert.Equal(t, StatusBreached, res.Status)
	assert.Equal(t, 90.0, res.ThresholdPct, "the memory default")
	assert.Zero(t, res.SuggestedLimit)
}

func TestFit_ReasonsNameTheResource(t *testing.T) {
	short := []Point{{T: testEnd, V: 50}}
	limit := []Point{{T: testEnd, V: 512}}
	mem := Fit(FitInput{Condition: ConditionMemorySaturation, ThresholdPct: 90, Value: short, PerPodLimit: limit, Resource: "memory", Now: testEnd, Params: Params{MinHours: 48}})
	assert.Equal(t, StatusInsufficientHistory, mem.Status)
	assert.Contains(t, mem.Reason, "memory limit")
	cpu := Fit(FitInput{Condition: ConditionThrottling, ThresholdPct: 75, Value: short, PerPodLimit: limit, Now: testEnd, Params: Params{MinHours: 48}})
	assert.Contains(t, cpu.Reason, "CPU limit", "an unset resource is CPU, so CPU reasons are unchanged")
}

func memoryResult(container string) Result {
	r := projected(LevelMedium, 70)
	r.Condition, r.Container, r.Workload, r.LimitUnit, r.PerPodLimit, r.SuggestedLimit = ConditionMemorySaturation, container, "orders", UnitMiB, 512, 704
	return r
}

func TestFingerprint_MemoryCarriesTheContainerAndCPUIsUnchanged(t *testing.T) {
	assert.Equal(t, "cpuforecast|acc|shop|deployment|order-service|cpu_throttling|MEDIUM",
		fingerprint(target, projected(LevelMedium, 70), LevelMedium), "CPU fingerprints must not change: open alerts are matched on them")
	assert.Equal(t, "cpuforecast|acc|shop|deployment|orders|memory_saturation|api|MEDIUM",
		fingerprint(target, memoryResult("api"), LevelMedium))
	assert.NotEqual(t, fingerprint(target, memoryResult("api"), LevelMedium), fingerprint(target, memoryResult("sidecar"), LevelMedium))
}

func TestSync_MemoryAlertNamesItsContainer(t *testing.T) {
	s := fakeStore(t)
	require.NoError(t, Sync(target, memoryResult("api"), nil))
	require.Len(t, s.upserted, 1)
	e := s.upserted[0]
	assert.Equal(t, "MemorySaturationPredicted", e.AggregationKey)
	assert.Equal(t, "api", e.Labels["container"], "the SLO's container, never the CPU single-limit lookup")
	assert.True(t, strings.HasPrefix(e.Title, "orders/api: Memory usage (RSS) projected to reach 25% of its limit"), e.Title)
}

func TestSync_MemoryClosesAlertsOfAContainerNoLongerWatched(t *testing.T) {
	s := fakeStore(t)
	s.stale = []string{"cpuforecast|acc|shop|deployment|orders|memory_saturation|old|MEDIUM"}
	require.NoError(t, Sync(target, memoryResult("api"), nil))
	require.NotEmpty(t, s.closes)
	assert.Equal(t, closeCall{s.stale[0], CloseUnwatched, false}, s.closes[0], "repaired first, as unwatched, at once")
}

func TestSync_CPUNeverLooksForStaleContainers(t *testing.T) {
	s := fakeStore(t)
	s.stale = []string{"should-not-be-closed"}
	require.NoError(t, Sync(target, projected(LevelMedium, 70), nil))
	for _, c := range s.closes {
		assert.NotEqual(t, "should-not-be-closed", c.fp)
	}
}

func TestCloseAll_MemoryClosesThatContainersAlerts(t *testing.T) {
	s := fakeStore(t)
	require.NoError(t, CloseAll(target, "shop", "Deployment", "orders", "api", ConditionMemorySaturation))
	require.Len(t, s.closes, 3)
	for _, c := range s.closes {
		assert.Contains(t, c.fp, "|memory_saturation|api|")
	}
}

func TestTrendInsights_OOMRestartsAreEvidenceWording(t *testing.T) {
	r := memoryResult("api")
	r.OOMRestarts6h = 3
	var found string
	for _, in := range trendInsights(r) {
		if strings.Contains(in.Message, "OOMKilled") {
			found = in.Message
		}
	}
	require.NotEmpty(t, found)
	assert.Contains(t, found, "3 restarts in the last 6h, last exit OOMKilled")
	assert.Contains(t, found, "if one was raised", "agentless accounts have no OOMKilled alert to point at")
}

func TestTableHeaders_MemoryNamesTheBusiestPod(t *testing.T) {
	h := tableHeaders(ConditionMemorySaturation)
	assert.Equal(t, "peak memory of busiest pod", h[2], "the memory series is each hour's peak")
	assert.Equal(t, "largest memory limit per pod", h[3])
	assert.Equal(t, "cpu usage per pod", tableHeaders(ConditionThrottling)[2], "CPU headers are unchanged")
}

func TestQuantity_WritesMiB(t *testing.T) {
	assert.Equal(t, "704 MiB", quantity(704, UnitMiB))
}

func memoryBreach() Result {
	return Result{
		Status: StatusBreached, Condition: ConditionMemorySaturation, ThresholdPct: 90, CurrentPct: 93,
		SlopePctPerDay: 0.2, Namespace: "shop", Workload: "orders", Kind: "deployment", Container: "api",
		LimitUnit: UnitMiB, PerPodLimit: 1024,
		Daily: []DailyRow{{Date: "2026-10-20", ValuePct: 92}, {Date: "2026-10-21", ValuePct: 93}},
	}
}

// A memory breach notifies even when it has stopped climbing: at the threshold, the
// next spike is an OOM kill.
func TestSync_FlatMemoryBreachIsHigh(t *testing.T) {
	s := fakeStore(t)
	require.NoError(t, Sync(target, memoryBreach(), nil))
	require.Len(t, s.upserted, 1)
	assert.Equal(t, "MemorySaturationSLOBreached", s.upserted[0].AggregationKey)
	assert.Equal(t, "HIGH", string(s.upserted[0].Priority))
}

func TestTrendInsights_MemoryDaysAreMeansOfHourlyPeaks(t *testing.T) {
	assert.Contains(t, trendInsights(memoryBreach())[0].Message, "Memory usage (RSS) by day (daily mean of hourly peaks): 2026-10-20 92%")
	assert.Contains(t, trendInsights(projected(LevelMedium, 70))[0].Message, "by day (daily mean):", "CPU wording is unchanged")
}
