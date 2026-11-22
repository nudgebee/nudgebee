package forecast

import (
	"errors"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/observability"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakes installs the three seams and restores them when the test ends.
func fakes(t *testing.T, provider string, pods []string, fetch func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error)) {
	t.Helper()
	origProvider, origFetch, origPods, origKind := metricsProviderFor, fetchUtilisation, workloadPods, inventoryKind
	// Only the forecast fields are saved and restored: copying the whole config.Config
	// races with the package's background RabbitMQ goroutine, which reads it.
	c := &config.Config
	origCfg := [...]any{c.NBForecastProviders, c.NBForecastMinHours, c.NBForecastMinRiseThrottlingPct,
		c.NBForecastMinRiseSaturationPct, c.NBForecastHighWithinHours, c.NBForecastFetchTimeoutSeconds}
	t.Cleanup(func() {
		metricsProviderFor, fetchUtilisation, workloadPods, inventoryKind = origProvider, origFetch, origPods, origKind
		c.NBForecastProviders = origCfg[0].(string)
		c.NBForecastMinHours = origCfg[1].(int)
		c.NBForecastMinRiseThrottlingPct = origCfg[2].(float64)
		c.NBForecastMinRiseSaturationPct = origCfg[3].(float64)
		c.NBForecastHighWithinHours = origCfg[4].(float64)
		c.NBForecastFetchTimeoutSeconds = origCfg[5].(int)
	})
	config.Config.NBForecastProviders = "prometheus"
	config.Config.NBForecastMinHours = 48
	config.Config.NBForecastMinRiseThrottlingPct = 5
	config.Config.NBForecastMinRiseSaturationPct = 10
	config.Config.NBForecastHighWithinHours = 48
	config.Config.NBForecastFetchTimeoutSeconds = 5

	metricsProviderFor = func(*security.RequestContext, string, string, string, string) (string, string, error) {
		return provider, "agent", nil
	}
	workloadPods = func(string, string, string, string, time.Time) ([]string, error) { return pods, nil }
	inventoryKind = func(string, string, string) string { return "" }
	fetchUtilisation = func(_ *security.RequestContext, req observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		return fetch(req)
	}
}

// promResult renders hourly points ending at end as one summed series.
func promResult(key string, end time.Time, values func(h int) float64) observability.QueryResult {
	var ts []int64
	var vs []float64
	for h := 0; h < 72; h++ {
		ts = append(ts, end.Add(time.Duration(h-72)*time.Hour).Unix())
		vs = append(vs, values(h))
	}
	return observability.QueryResult{QueryKey: key, Payload: []observability.Result{{Timestamps: ts, Values: vs}}}
}

var testEnd = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)

func climbing(req observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
	return observability.OutputMetricQuery{Results: []observability.QueryResult{
		promResult("cpu_throttling_ratio", testEnd, func(h int) float64 { return 0.05 + 0.06*float64(h)/24 }),
		// usage climbs 6 pp a day of a 2-core workload total, from 40% to ~58%
		promResult("cpu_usage_limited", testEnd, func(h int) float64 { return 0.8 + 0.12*float64(h)/24 }),
		promResult("cpu_limit", testEnd, func(int) float64 { return 2 }),
		promResult("pod_count", testEnd, func(int) float64 { return 4 }),
	}}, nil
}

func TestEvaluate_ThrottlingFromTheRatioKey(t *testing.T) {
	var asked observability.GetUtilisationTrendRequest
	fakes(t, "prometheus", []string{"order-6d9f-a", "order-6d9f-b"}, func(req observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		asked = req
		return climbing(req)
	})

	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "order", Kind: "Deployment", Condition: ConditionThrottling, EndTime: testEnd})

	require.Equal(t, StatusProjected, res.Status, res.Reason)
	assert.Equal(t, 25.0, res.ThresholdPct, "the default threshold applies")
	assert.Equal(t, 0.5, res.PerPodLimit, "cpu_limit is a workload total: 2 cores over 4 pods")
	assert.Equal(t, "deployment", res.Kind)
	assert.Equal(t, "prometheus", res.Provider)

	assert.Equal(t, []string{"order-6d9f-a", "order-6d9f-b"}, asked.PodNames, "pods come from the inventory, not the name prefix")
	assert.Equal(t, 300, asked.Step, "fetched at 5 minutes, averaged into hourly buckets")
	assert.Equal(t, 300, asked.StepInterval)
	assert.Equal(t, testEnd.Add(-72*time.Hour).UnixMilli(), asked.StartTime)
	assert.ElementsMatch(t, []string{"cpu_throttling_ratio", "cpu_limit", "cpu_usage_limited", "pod_count"}, asked.Request["metrics"],
		"the preset's value, limit and usage, and the pod count")
	assert.Equal(t, UnitCores, res.LimitUnit)
}

func TestEvaluate_UnknownMetricIsAnError(t *testing.T) {
	fakes(t, "prometheus", []string{"p"}, func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		t.Fatal("must not fetch for an unknown metric")
		return observability.OutputMetricQuery{}, nil
	})
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "order", Condition: "latency"})
	assert.Equal(t, StatusError, res.Status)
	assert.Contains(t, res.Reason, "cpu_saturation, cpu_throttling", "names the valid metrics")
}

func TestEvaluate_OmittedKindComesFromTheInventory(t *testing.T) {
	var podsAskedFor string
	fakes(t, "prometheus", []string{"kafka-0"}, climbing)
	inventoryKind = func(string, string, string) string { return "StatefulSet" }
	workloadPods = func(_, _, _, kind string, _ time.Time) ([]string, error) {
		podsAskedFor = kind
		return []string{"kafka-0"}, nil
	}
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "kafka", Condition: ConditionThrottling, EndTime: testEnd})
	assert.Equal(t, "statefulset", res.Kind)
	assert.Equal(t, "statefulset", podsAskedFor, "a StatefulSet's pods, not a Deployment's")
}

func TestEvaluate_SaturationIsUsageOverLimit(t *testing.T) {
	fakes(t, "prometheus", []string{"p"}, climbing)
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "order", Condition: ConditionSaturation, EndTime: testEnd})

	require.Equal(t, StatusProjected, res.Status, res.Reason)
	assert.Equal(t, 90.0, res.ThresholdPct)
	assert.InDelta(t, 57, res.CurrentPct, 2)
	assert.InDelta(t, 6, res.SlopePctPerDay, 0.5)
}

func TestEvaluate_UnsupportedProviderNeverFetches(t *testing.T) {
	fakes(t, "datadog", []string{"p"}, func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		t.Fatal("must not fetch for an unsupported provider")
		return observability.OutputMetricQuery{}, nil
	})
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "order", Condition: ConditionThrottling})
	assert.Equal(t, StatusProviderUnsupported, res.Status)
	assert.Contains(t, res.Reason, "datadog")
}

func TestEvaluate_NoPodsIsInsufficientHistory(t *testing.T) {
	fakes(t, "prometheus", nil, climbing)
	res := Evaluate(nil, Request{AccountId: "acc", Namespace: "shop", Name: "gone", Condition: ConditionThrottling})
	assert.Equal(t, StatusInsufficientHistory, res.Status)
}

func TestEvaluate_FailuresAreErrorsNotQuiet(t *testing.T) {
	fakes(t, "prometheus", []string{"p"}, func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		return observability.OutputMetricQuery{}, errors.New("relay down")
	})
	assert.Equal(t, StatusError, Evaluate(nil, Request{AccountId: "acc", Condition: ConditionThrottling}).Status)

	msg := "bad query"
	fakes(t, "prometheus", []string{"p"}, func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		return observability.OutputMetricQuery{Results: []observability.QueryResult{{QueryKey: "cpu_limit", Error: &msg}}}, nil
	})
	res := Evaluate(nil, Request{AccountId: "acc", Condition: ConditionThrottling})
	assert.Equal(t, StatusError, res.Status)
	assert.Contains(t, res.Reason, "bad query")
}

func TestEvaluate_NoLimitSeries(t *testing.T) {
	fakes(t, "prometheus", []string{"p"}, func(observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
		return observability.OutputMetricQuery{Results: []observability.QueryResult{
			promResult("cpu_throttling_ratio", testEnd, func(int) float64 { return 0.1 }),
			{QueryKey: "cpu_limit"},
		}}, nil
	})
	assert.Equal(t, StatusNoLimit, Evaluate(nil, Request{AccountId: "acc", Condition: ConditionThrottling, EndTime: testEnd}).Status)
}

func TestHourly_AveragesWithinABucketAndSkipsNaN(t *testing.T) {
	h0 := testEnd.Unix()
	r := observability.QueryResult{Payload: []observability.Result{{
		Timestamps: []int64{h0, h0 + 600, h0 + 3600, h0 + 7200},
		Values:     []float64{1, 3, nan(), 5},
	}}}
	got := hourly(r)
	require.Len(t, got, 2)
	assert.Equal(t, 2.0, got[0].V)
	assert.Equal(t, 5.0, got[1].V)
}

func nan() float64 { var z float64; return z / z }
