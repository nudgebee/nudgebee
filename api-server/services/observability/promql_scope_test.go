package observability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopePromQLSelectors_AddsMatcherToEverySelector(t *testing.T) {
	got, err := scopePromQLSelectors(
		`sum(rate(container_network_receive_bytes_total{pod=~"api.*"}[5m])) + sum(rate(container_network_transmit_bytes_total{pod=~"api.*"}[5m]))`,
		`cluster="prod"`)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(got, `cluster="prod"`), got)
}

func TestScopePromQLSelectors_OrJoinedAndBareMetric(t *testing.T) {
	got, err := scopePromQLSelectors(
		`kube_replicaset_owner{namespace="a",replicaset=~"x|y"} or kube_replicaset_owner{namespace="b",replicaset="z"}`,
		`cluster="prod",env="eu"`)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(got, `cluster="prod"`), got)
	assert.Equal(t, 2, strings.Count(got, `env="eu"`), got)

	bare, err := scopePromQLSelectors(`kube_pod_info`, `cluster="prod"`)
	require.NoError(t, err)
	assert.Equal(t, `kube_pod_info{cluster="prod"}`, bare)
}

func TestScopePromQLSelectors_DoesNotDuplicateOrRunWithoutLabels(t *testing.T) {
	got, err := scopePromQLSelectors(`up{cluster="prod"}`, `cluster="prod"`)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(got, `cluster="prod"`), got)

	unchanged, err := scopePromQLSelectors(`up{ job="x" }`, "  ")
	require.NoError(t, err)
	assert.Equal(t, `up{ job="x" }`, unchanged)
}

func TestScopePromQLSelectors_RejectsUnparseable(t *testing.T) {
	_, err := scopePromQLSelectors(`sum(rate(up[5m])`, `cluster="prod"`)
	assert.Error(t, err)
	_, err = scopePromQLSelectors(`up`, `not a matcher`)
	assert.Error(t, err)
}
