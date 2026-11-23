package observability

import (
	"log/slog"
	"math"
	"testing"

	"nudgebee/services/eventrule/playbooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hpaCtx(aggKey string, labels map[string]string, subjectNamespace string) playbooks.PlaybookActionContext {
	return playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{
		AggregationKey:   aggKey,
		Labels:           labels,
		SubjectNamespace: subjectNamespace,
	})
}

// TestHpaRegisteredUnderExistingName — the name already exists as a catalog row
// and an agent_playbook binding with nothing behind it. Registering under
// exactly that name is what turns the row live, so a rename silently
// un-implements it again.
func TestHpaRegisteredUnderExistingName(t *testing.T) {
	action, found := playbooks.GetAction("hpa_mismatch_enricher")
	require.True(t, found, "hpa_mismatch_enricher is not registered")
	_, ok := action.(*hpaStatusAction)
	assert.True(t, ok, "hpa_mismatch_enricher is not the HPA action")
}

func TestHpaCanAutoExecute(t *testing.T) {
	a := &hpaStatusAction{}
	labels := map[string]string{"horizontalpodautoscaler": "image-resizer", "namespace": "media"}

	assert.True(t, a.CanAutoExecute(hpaCtx("KubeHpaMaxedOut", labels, "")))
	assert.True(t, a.CanAutoExecute(hpaCtx("KubeHpaReplicasMismatch", labels, "")))

	// Unrelated alerts get nothing.
	assert.False(t, a.CanAutoExecute(hpaCtx("KubePodCrashLooping", labels, "")))

	// The right alert with no autoscaler in its labels must not run: the
	// subject on these alerts is the scaled workload, and querying that name as
	// an HPA returns nothing while looking authoritative.
	assert.False(t, a.CanAutoExecute(hpaCtx("KubeHpaMaxedOut", map[string]string{"deployment": "image-resizer"}, "media")))
	assert.False(t, a.CanAutoExecute(hpaCtx("KubeHpaMaxedOut", nil, "media")))
}

// TestHpaNameNamespace — kube-state-metrics writes `horizontalpodautoscaler`;
// some relabelings shorten it to `hpa`. Namespace falls back to the subject's,
// but the NAME never does.
func TestHpaNameNamespace(t *testing.T) {
	name, ns := hpaNameNamespace(playbooks.PlaybookEvent{
		Labels: map[string]string{"horizontalpodautoscaler": "image-resizer", "namespace": "media"},
	})
	assert.Equal(t, "image-resizer", name)
	assert.Equal(t, "media", ns)

	name, ns = hpaNameNamespace(playbooks.PlaybookEvent{
		Labels:           map[string]string{"hpa": "llm-gateway"},
		SubjectNamespace: "nudgebee",
	})
	assert.Equal(t, "llm-gateway", name)
	assert.Equal(t, "nudgebee", ns, "namespace may fall back to the subject's")

	name, _ = hpaNameNamespace(playbooks.PlaybookEvent{
		Labels:      map[string]string{"deployment": "image-resizer"},
		SubjectName: "image-resizer",
	})
	assert.Equal(t, "", name, "the scaled workload must never be treated as the HPA")
}

// TestHpaQueriesScopedToAutoscaler — every query must name this autoscaler and
// its namespace. An unscoped query returns every HPA in the cluster and the
// card would describe the wrong one, confidently. This is the same class of bug
// that the over-commitment queries shipped with.
func TestHpaQueriesScopedToAutoscaler(t *testing.T) {
	qs := hpaQueries("image-resizer", "media")
	require.Len(t, qs, 7)
	for key, q := range qs {
		assert.Contains(t, q, `horizontalpodautoscaler="image-resizer"`, "query %q is not scoped to the HPA", key)
		assert.Contains(t, q, `namespace="media"`, "query %q is not scoped to the namespace", key)
	}

	// With no namespace the queries still scope by name rather than dropping
	// the selector entirely.
	for key, q := range hpaQueries("llm-gateway", "") {
		assert.Contains(t, q, `horizontalpodautoscaler="llm-gateway"`, "query %q lost its scope", key)
		assert.NotContains(t, q, "namespace=", "query %q invented a namespace", key)
	}
}

func TestHpaMetricRows(t *testing.T) {
	results := map[string][]latestSample{
		"target_metric": {
			{Metric: map[string]string{"metric_name": "cpu", "metric_target_type": "utilization"}, Value: 70},
		},
		"current_metric": {
			{Metric: map[string]string{"metric_name": "cpu", "metric_target_type": "utilization"}, Value: 93},
		},
	}
	rows, pinned := hpaMetricRows(results)
	require.Len(t, rows, 1)
	assert.Equal(t, "cpu utilization", rows[0][0])
	assert.Equal(t, "93% (target 70%)", rows[0][1])
	assert.Equal(t, "cpu utilization", pinned, "a metric above its target is what pinned the autoscaler")

	// Under target: nothing is pinned.
	results["current_metric"] = []latestSample{{Metric: map[string]string{"metric_name": "cpu", "metric_target_type": "utilization"}, Value: 40}}
	_, pinned = hpaMetricRows(results)
	assert.Equal(t, "", pinned)
}

// TestHpaMetricRowsPairsByTargetType — kube-state-metrics reports the current
// reading once per metric_target_type. Each target must pair with the reading of
// its own type, or a utilization target is shown against the raw `value` series
// (0.2 cores as "0%", 91328512 bytes as "91328512%").
func TestHpaMetricRowsPairsByTargetType(t *testing.T) {
	sample := func(name, targetType string, v float64) latestSample {
		return latestSample{Metric: map[string]string{"metric_name": name, "metric_target_type": targetType}, Value: v}
	}
	results := map[string][]latestSample{
		"target_metric": {
			sample("cpu", "utilization", 70),
			sample("memory", "utilization", 80),
		},
		"current_metric": {
			sample("cpu", "average", 0.1),
			sample("cpu", "utilization", 1000),
			sample("cpu", "value", 0.2),
			sample("memory", "average", 45664256),
			sample("memory", "utilization", 136),
			sample("memory", "value", 91328512),
		},
	}
	rows, pinned := hpaMetricRows(results)
	assert.Equal(t, [][]any{
		{"cpu utilization", "1000% (target 70%)"},
		{"memory utilization", "136% (target 80%)"},
	}, rows, "readings of a type no target uses must not get their own rows")
	assert.Equal(t, "cpu utilization", pinned)

	// No target series at all: every reading is shown, each under its own type.
	delete(results, "target_metric")
	rows, pinned = hpaMetricRows(results)
	require.Len(t, rows, 6)
	assert.Equal(t, []any{"cpu utilization", "1000%"}, rows[1])
	assert.Equal(t, []any{"memory value", "91328512"}, rows[5])
	assert.Equal(t, "", pinned, "nothing is above a target when there is no target")
}

// TestHpaConditionRows — a healthy autoscaler must add no rows. Printing
// "AbleToScale true" on every card is noise; the false case is the answer.
func TestHpaConditionRows(t *testing.T) {
	healthy := []latestSample{
		{Metric: map[string]string{"condition": "AbleToScale", "status": "true"}},
		{Metric: map[string]string{"condition": "ScalingActive", "status": "true"}},
		{Metric: map[string]string{"condition": "ScalingLimited", "status": "false"}},
	}
	assert.Empty(t, hpaConditionRows(healthy))

	problem := []latestSample{
		{Metric: map[string]string{"condition": "ScalingLimited", "status": "true"}},
		{Metric: map[string]string{"condition": "AbleToScale", "status": "false"}},
		{Metric: map[string]string{"condition": "ScalingActive", "status": "true"}},
	}
	rows := hpaConditionRows(problem)
	require.Len(t, rows, 2)
	assert.Equal(t, "AbleToScale", rows[0][0])
	assert.Contains(t, rows[0][1], "cannot scale")
	assert.Equal(t, "ScalingLimited", rows[1][0])
	assert.Contains(t, rows[1][1], "replica bound")
}

func TestHpaInsight(t *testing.T) {
	// At the ceiling with a metric over target — the actionable case.
	in := hpaInsight("image-resizer", "media", 8, true, 8, true, "cpu utilization")
	require.Len(t, in, 1)
	assert.Contains(t, in[0].Message, "media/image-resizer is at its maximum of 8 replicas")
	assert.Contains(t, in[0].Message, "cpu utilization is above its target")

	// Below the ceiling: no warning.
	assert.Empty(t, hpaInsight("image-resizer", "media", 4, true, 8, true, "cpu utilization"))
	// Missing data: no warning invented.
	assert.Empty(t, hpaInsight("image-resizer", "media", 0, false, 8, true, ""))
}

func TestHpaFormatting(t *testing.T) {
	assert.Equal(t, "70%", hpaMetricValue(70, "utilization"))
	assert.Equal(t, "70%", hpaMetricValue(70, "Utilization"))
	assert.Equal(t, "250", hpaMetricValue(250, "AverageValue"))
	assert.Equal(t, "1.50", hpaMetricValue(1.5, "AverageValue"))
	// Prometheus renders NaN/Inf as real samples. int64(NaN) is undefined in Go,
	// and "NaN%" on a card is worse than saying the reading is missing.
	assert.Equal(t, "unknown", hpaMetricValue(math.NaN(), "utilization"))
	assert.Equal(t, "unknown", hpaMetricValue(math.Inf(1), "AverageValue"))
	assert.Equal(t, "unknown", hpaMetricValue(math.Inf(-1), "utilization"))

	assert.Equal(t, "media/image-resizer", hpaLabel("media", "image-resizer"))
	assert.Equal(t, "image-resizer", hpaLabel("", "image-resizer"))

	assert.Equal(t, "min 2, max 8", hpaRange(2, true, 8, true))
	assert.Equal(t, "max 8", hpaRange(0, false, 8, true))
}

// TestFirstValue — zero replicas and no data are different answers, and the
// card treats them differently.
func TestFirstValue(t *testing.T) {
	v, ok := firstValue([]latestSample{{Value: 0}})
	assert.True(t, ok)
	assert.Equal(t, 0.0, v)

	_, ok = firstValue(nil)
	assert.False(t, ok)

	// A NaN sample is no data, not a number. Every comparison against NaN is
	// false in Go, so an unguarded NaN slips past `current < maxReplicas` and
	// makes the insight announce a ceiling we never actually measured.
	_, ok = firstValue([]latestSample{{Value: math.NaN()}})
	assert.False(t, ok, "NaN must read as absent")
	_, ok = firstValue([]latestSample{{Value: math.Inf(1)}})
	assert.False(t, ok, "Inf must read as absent")

	// A usable sample after an unusable one still wins.
	v, ok = firstValue([]latestSample{{Value: math.NaN()}, {Value: 3}})
	assert.True(t, ok)
	assert.Equal(t, 3.0, v)
}

// TestHpaInsightIgnoresUnreadableReplicas — the bug this guards. Without the
// NaN filter, firstValue returns (NaN, true), `current < maxReplicas` is false
// because every NaN comparison is false, and the card asserts the autoscaler is
// at its ceiling on data we never read.
func TestHpaInsightIgnoresUnreadableReplicas(t *testing.T) {
	current, haveCurrent := firstValue([]latestSample{{Value: math.NaN()}})
	assert.False(t, haveCurrent)
	assert.Empty(t, hpaInsight("image-resizer", "media", current, haveCurrent, 8, true, "cpu utilization"),
		"an unreadable replica count must not produce a ceiling warning")
}

// TestHpaMetricRowsSkipUnreadable — a NaN reading must not pair with a target
// and must not be treated as "above target", which would name it as the metric
// that pinned the autoscaler.
func TestHpaMetricRowsSkipUnreadable(t *testing.T) {
	rows, pinned := hpaMetricRows(map[string][]latestSample{
		"target_metric":  {{Metric: map[string]string{"metric_name": "cpu", "metric_target_type": "utilization"}, Value: 70}},
		"current_metric": {{Metric: map[string]string{"metric_name": "cpu", "metric_target_type": "utilization"}, Value: math.NaN()}},
	})
	assert.Equal(t, "", pinned, "an unreadable metric cannot be what pinned the autoscaler")
	require.Len(t, rows, 1)
	assert.Equal(t, "target 70%", rows[0][1], "shows the target alone rather than inventing a reading")
}
