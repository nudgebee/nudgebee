package core

import (
	"nudgebee/services/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeepAlertPod(t *testing.T) {
	t.Run("the pod the alert named is kept before the workload name replaces it", func(t *testing.T) {
		labels := map[string]string{"pod": "orders-86c5cc649d-xbdz4", "namespace": "shop"}
		keepAlertPod(labels, "orders")
		assert.Equal(t, "orders-86c5cc649d-xbdz4", labels[common.AlertPodLabel])
	})

	t.Run("nothing to keep when the label is empty or already the workload name", func(t *testing.T) {
		labels := map[string]string{"namespace": "shop"}
		keepAlertPod(labels, "orders")
		assert.NotContains(t, labels, common.AlertPodLabel)

		labels = map[string]string{"pod": "orders"}
		keepAlertPod(labels, "orders")
		assert.NotContains(t, labels, common.AlertPodLabel)
	})

	t.Run("a second match does not overwrite the kept pod with the first match's workload name", func(t *testing.T) {
		labels := map[string]string{"pod": "orders", common.AlertPodLabel: "orders-86c5cc649d-xbdz4"}
		keepAlertPod(labels, "orders-v2")
		assert.Equal(t, "orders-86c5cc649d-xbdz4", labels[common.AlertPodLabel])
	})
}
