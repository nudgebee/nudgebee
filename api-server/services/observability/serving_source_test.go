package observability

import (
	"errors"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubForagerRegistered(t *testing.T, fn func(core.IntegrationDto) (bool, error)) {
	t.Helper()
	orig := foragerRegisteredDatasource
	t.Cleanup(func() { foragerRegisteredDatasource = orig })
	foragerRegisteredDatasource = fn
}

func TestServingSource(t *testing.T) {
	registered := func(core.IntegrationDto) (bool, error) { return true, nil }
	notRegistered := func(core.IntegrationDto) (bool, error) { return false, nil }
	mustNotAsk := func(core.IntegrationDto) (bool, error) {
		t.Fatal("must not look up the forager registration")
		return false, nil
	}

	t.Run("a forager-registered prometheus or loki row is served like a user row", func(t *testing.T) {
		stubForagerRegistered(t, registered)
		for _, typ := range []string{"prometheus", "loki"} {
			got, err := servingSource(&core.IntegrationDto{Source: "agent", Name: "vm-" + typ, Type: typ})
			require.NoError(t, err)
			assert.Equal(t, "user", got, typ)
		}
	})

	t.Run("an agent row that is not forager-registered stays on the agent", func(t *testing.T) {
		stubForagerRegistered(t, notRegistered)
		got, err := servingSource(&core.IntegrationDto{Source: "agent", Name: "prometheus-k8s", Type: "prometheus"})
		require.NoError(t, err)
		assert.Equal(t, "agent", got, "a Kubernetes account must keep its agent path")
	})

	t.Run("user rows are untouched and not looked up", func(t *testing.T) {
		stubForagerRegistered(t, mustNotAsk)
		got, err := servingSource(&core.IntegrationDto{Source: "user", Name: "mine", Type: "prometheus"})
		require.NoError(t, err)
		assert.Equal(t, "user", got)
	})

	t.Run("no other provider type is retargeted", func(t *testing.T) {
		stubForagerRegistered(t, mustNotAsk)
		for _, typ := range []string{"pinot", "otel_clickhouse", "jaeger", "signoz", "ES", "chronosphere"} {
			got, err := servingSource(&core.IntegrationDto{Source: "agent", Name: "x-" + typ, Type: typ})
			require.NoError(t, err)
			assert.Equal(t, "agent", got, typ)
		}
	})

	t.Run("a failed lookup is returned, not read as a Kubernetes agent row", func(t *testing.T) {
		stubForagerRegistered(t, func(core.IntegrationDto) (bool, error) { return false, errors.New("connection reset") })
		_, err := servingSource(&core.IntegrationDto{Source: "agent", Name: "vm-fleet-prom", Type: "prometheus"})
		require.Error(t, err)
	})

	t.Run("a registered row passes the VM status checks' agent-only gate", func(t *testing.T) {
		stubForagerRegistered(t, registered)
		got, err := servingSource(&core.IntegrationDto{Source: "agent", Name: "vm-fleet-prom", Type: "prometheus"})
		require.NoError(t, err)
		_, supported := getMetricsSource("prometheus", got)
		assert.NoError(t, supported, "a metrics source must exist for the resolved source")
		assert.NotEqual(t, "agent", got)
	})
}
