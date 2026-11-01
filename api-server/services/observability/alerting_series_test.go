package observability

import (
	"nudgebee/services/common"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func alertingTestSeries(labels ...map[string]any) []any {
	out := make([]any, 0, len(labels))
	for _, l := range labels {
		out = append(out, map[string]any{"metric": l, "timestamps": []any{1.0}, "values": []any{"1"}})
	}
	return out
}

func seriesLabel(s any, key string) any {
	return s.(map[string]any)["metric"].(map[string]any)[key]
}

func isAlerting(s any) bool {
	v, _ := s.(map[string]any)["alerting"].(bool)
	return v
}

func TestMarkAlertingSeries(t *testing.T) {
	t.Run("postgres volume leads, elasticsearch volume stays after it unmarked", func(t *testing.T) {
		series := alertingTestSeries(
			map[string]any{"namespace": "nudgebee", "persistentvolumeclaim": "data-elasticsearch-0", "job": "kubelet"},
			map[string]any{"namespace": "nudgebee", "persistentvolumeclaim": "data-postgresql-0", "job": "kubelet"},
		)
		got := markAlertingSeries(series, map[string]string{
			"alertname": "KubernetesVolumeOutOfDiskSpace", "namespace": "nudgebee", "persistentvolumeclaim": "data-postgresql-0", "job": "kubelet",
		})
		assert.Len(t, got, 2)
		assert.Equal(t, "data-postgresql-0", seriesLabel(got[0], "persistentvolumeclaim"))
		assert.True(t, isAlerting(got[0]))
		assert.Equal(t, "data-elasticsearch-0", seriesLabel(got[1], "persistentvolumeclaim"))
		assert.False(t, isAlerting(got[1]))
	})

	t.Run("orders among seven deployments; a drifted shared label does not unmark it", func(t *testing.T) {
		var labels []map[string]any
		for _, d := range []string{"benchmark-scenarios/analytics-db", "benchmark-scenarios/checkout-api", "shop/cart", "shop/orders", "benchmark-scenarios/shipping-api", "shop/web", "shop/search"} {
			ns, name, _ := strings.Cut(d, "/")
			labels = append(labels, map[string]any{"namespace": ns, "deployment": name, "pod": "kube-state-metrics-abc"})
		}
		got := markAlertingSeries(alertingTestSeries(labels...), map[string]string{
			"namespace": "shop", "deployment": "orders", "pod": "kube-state-metrics-old",
		})
		assert.Len(t, got, 7)
		assert.Equal(t, "orders", seriesLabel(got[0], "deployment"))
		assert.True(t, isAlerting(got[0]))
		for _, s := range got[1:] {
			assert.False(t, isAlerting(s))
		}
		// The rest keep their original order.
		assert.Equal(t, "analytics-db", seriesLabel(got[1], "deployment"))
		assert.Equal(t, "checkout-api", seriesLabel(got[2], "deployment"))
	})

	t.Run("nothing tells the series apart from the event's view: order and marks untouched", func(t *testing.T) {
		series := alertingTestSeries(
			map[string]any{"job": "node", "device": "sda"},
			map[string]any{"job": "node", "device": "sdb"},
		)
		got := markAlertingSeries(series, map[string]string{"job": "node"})
		assert.Equal(t, "sda", seriesLabel(got[0], "device"))
		assert.False(t, isAlerting(got[0]))
		assert.False(t, isAlerting(got[1]))
	})

	t.Run("no series matches the event (pod label mismatch): nothing marked", func(t *testing.T) {
		series := alertingTestSeries(
			map[string]any{"pod": "a-1"},
			map[string]any{"pod": "b-1"},
		)
		got := markAlertingSeries(series, map[string]string{"pod": "c-1"})
		assert.Equal(t, "a-1", seriesLabel(got[0], "pod"))
		assert.False(t, isAlerting(got[0]))
		assert.False(t, isAlerting(got[1]))
	})

	t.Run("single series is left alone", func(t *testing.T) {
		series := alertingTestSeries(map[string]any{"datname": "temporal"})
		got := markAlertingSeries(series, map[string]string{"datname": "temporal"})
		assert.False(t, isAlerting(got[0]))
	})

	t.Run("pod alert: the event's pod label is the workload, the kept pod marks the series", func(t *testing.T) {
		series := alertingTestSeries(
			map[string]any{"namespace": "shop", "pod": "cart-5d9f7c6b8-abcde"},
			map[string]any{"namespace": "shop", "pod": "orders-86c5cc649d-xbdz4"},
			map[string]any{"namespace": "billing", "pod": "orders-7f6d5c4b3-zzzzz"},
		)
		event := map[string]string{"namespace": "shop", "pod": "orders", "kind": "Deployment", common.AlertPodLabel: "orders-86c5cc649d-xbdz4"}

		got := markAlertingSeries(series, event)
		assert.False(t, isAlerting(got[0]), "without the kept pod the workload name matches no series")

		got = markAlertingSeries(series, common.SeriesMatchLabels(event))
		assert.Equal(t, "orders-86c5cc649d-xbdz4", seriesLabel(got[0], "pod"))
		assert.True(t, isAlerting(got[0]))
		assert.False(t, isAlerting(got[1]))
		assert.False(t, isAlerting(got[2]))
	})

	t.Run("node alert: instance and the kept exporter pod both agree", func(t *testing.T) {
		series := alertingTestSeries(
			map[string]any{"instance": "192.0.2.119:9100", "pod": "node-exporter-2zs48", "job": "node-exporter"},
			map[string]any{"instance": "192.0.2.27:9100", "pod": "node-exporter-mmxd8", "job": "node-exporter"},
		)
		event := map[string]string{"instance": "192.0.2.27:9100", "pod": "node-exporter", "kind": "DaemonSet", "job": "node-exporter", common.AlertPodLabel: "node-exporter-mmxd8"}
		got := markAlertingSeries(series, common.SeriesMatchLabels(event))
		assert.Equal(t, "192.0.2.27:9100", seriesLabel(got[0], "instance"))
		assert.True(t, isAlerting(got[0]))
		assert.False(t, isAlerting(got[1]))
	})
}
