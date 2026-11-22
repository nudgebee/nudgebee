package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cluster tiles chart bytes per bucket, not bytes per second.
//
// The existing network_receive_packet / network_transmit_packets keys are rates, and
// the transmit one is sign-negated so the pair renders as a mirrored chart. Neither
// suits "how much crossed the wire this day", so these keys use increase() over the
// caller's own bucket and return an unsigned number.
func TestClusterNetworkIncreaseKeys(t *testing.T) {
	meta := RequestMetadata{RangeWindow: "30d", Step: "1d", RateWindow: "5m"}
	q := buildPrometheusWorkloadQueries(meta, []string{
		"network_receive_bytes_increase", "network_transmit_bytes_increase",
	})

	rx := q["network_receive_bytes_increase"]
	tx := q["network_transmit_bytes_increase"]
	require.NotEmpty(t, rx)
	require.NotEmpty(t, tx)

	for name, query := range map[string]string{"receive": rx, "transmit": tx} {
		assert.Contains(t, query, "increase(", "%s counts bytes in the bucket, not a rate", name)
		// The bucket is the caller's step, not the window-derived default: each point
		// must cover exactly the span it is charted against.
		assert.Contains(t, query, "[1d]", "%s uses the requested bucket", name)
		// Node-scoped source, not the per-pod container_network_* counters: these keys
		// are always cluster-wide, and the container form's series count blows past
		// vmselect's index limit over a month of pod churn (422 -> empty tile). The
		// device filter keeps the sum to real NICs.
		assert.Contains(t, query, "node_network_", "%s must read node_network_*, not container_network_*", name)
		assert.NotContains(t, query, "container_network_", "%s must not use the high-cardinality per-pod counter", name)
		assert.Contains(t, query, `device!~"lo|veth`, "%s must exclude virtual/loopback interfaces", name)
		_, err := promQLParser.ParseExpr(stripClusterPlaceholder(query))
		require.NoError(t, err, "%s: %s", name, query)
	}

	// Unsigned, unlike network_transmit_packets, which negates for a mirrored chart.
	assert.NotContains(t, tx, "-(")
	assert.NotContains(t, tx, "* -1")
}

// Datadog says why it cannot answer rather than dropping the key, which would be
// indistinguishable from a cluster that moved no traffic.
func TestClusterNetworkIncrease_DatadogSaysWhy(t *testing.T) {
	results := datadogNoteUnanswerableKeys(nil, []string{
		"network_receive_bytes_increase", "network_transmit_bytes_increase",
	})
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Empty(t, r.Payload)
		assert.Contains(t, r.Note, "semantics are unconfirmed",
			"%s must name the reason", r.QueryKey)
	}
}

// Elasticsearch has no equivalent either, and its existing unsupported path already
// reports that; this pins that the keys are not silently answered from a rate source.
func TestClusterNetworkIncrease_ElasticsearchUnsupported(t *testing.T) {
	for _, key := range []string{"network_receive_bytes_increase", "network_transmit_bytes_increase"} {
		_, ok := esUtilisationMetric(key, esScopeCluster, false)
		assert.False(t, ok, "%s must report unsupported rather than answer from a rate", key)
	}
}
