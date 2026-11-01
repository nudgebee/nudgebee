package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSeriesMatchLabels(t *testing.T) {
	t.Run("pod is set back to the pod the alert named; the input is untouched", func(t *testing.T) {
		labels := map[string]string{"pod": "orders", "namespace": "shop", AlertPodLabel: "orders-86c5cc649d-xbdz4"}
		got := SeriesMatchLabels(labels)
		assert.Equal(t, "orders-86c5cc649d-xbdz4", got["pod"])
		assert.Equal(t, "shop", got["namespace"])
		assert.Equal(t, "orders", labels["pod"])
	})

	t.Run("no kept pod: labels returned as they are", func(t *testing.T) {
		labels := map[string]string{"pod": "orders", "namespace": "shop"}
		assert.Equal(t, labels, SeriesMatchLabels(labels))
		emptyKept := map[string]string{"pod": "orders", AlertPodLabel: ""}
		assert.Equal(t, "orders", SeriesMatchLabels(emptyKept)["pod"])
	})

	t.Run("works on the decoded-JSON label map triage reads", func(t *testing.T) {
		got := SeriesMatchLabels(map[string]interface{}{"pod": "node-exporter", AlertPodLabel: "node-exporter-xhdz5"})
		assert.Equal(t, "node-exporter-xhdz5", got["pod"])
		assert.Nil(t, SeriesMatchLabels[interface{}](nil))
	})
}
