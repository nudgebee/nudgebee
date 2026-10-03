//go:build liveMimir

// Save-time validation against a REAL Mimir: the query probe and the ruler probe
// both pass on a genuine engine, and a ruler declared on an endpoint that has no
// ruler API is caught at save time. Run with a Mimir at MIMIR_LIVE_URL, e.g.
//
//	MIMIR_LIVE_URL=http://localhost:9009/prometheus \
//	  go test -tags liveMimir ./integrations/ -run TestLiveMimir -v
package integrations

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveMimir_ValidateConfigProbesQueryAndRuler(t *testing.T) {
	url := os.Getenv("MIMIR_LIVE_URL")
	if url == "" {
		t.Skip("MIMIR_LIVE_URL not set")
	}

	t.Run("query endpoint alone", func(t *testing.T) {
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{PrometheusURLKey: url}), "acc")
		assert.Empty(t, errs, errorsToString(errs))
	})

	t.Run("query endpoint with its ruler", func(t *testing.T) {
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
			PrometheusURLKey:       url,
			PrometheusRulerTypeKey: PrometheusRulerMimirCortex,
		}), "acc")
		assert.Empty(t, errs, errorsToString(errs))
	})

	t.Run("ruler declared on a path that has none is caught on save", func(t *testing.T) {
		// Mimir serves the Prometheus API under /prometheus; the ruler API is not
		// reachable under a made-up prefix, which is what a mis-set ruler URL is.
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
			PrometheusURLKey:       url,
			PrometheusRulerTypeKey: PrometheusRulerMimirCortex,
			PrometheusRulerURLKey:  url + "/not-a-ruler",
		}), "acc")
		require.NotEmpty(t, errs)
		assert.Contains(t, errorsToString(errs), "no ruler API")
	})
}
