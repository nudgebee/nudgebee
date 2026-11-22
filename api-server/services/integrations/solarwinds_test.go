package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSolarWindsConfigSchema_HasNoDefaultTraceProvider guards the disabled
// DefaultTraceProvider option: SolarWinds has no span listing, so selecting it
// as the default trace provider leaves the Traces tab permanently empty.
func TestSolarWindsConfigSchema_HasNoDefaultTraceProvider(t *testing.T) {
	schema := SolarWinds{}.ConfigSchema()
	_, ok := schema.Properties[core.DefaultTraceProvider]
	assert.False(t, ok, "SolarWinds config schema must not expose DefaultTraceProvider")

	// Sibling default-provider toggles remain present.
	_, hasLog := schema.Properties[core.DefaultLogProvider]
	_, hasMetric := schema.Properties[core.DefaultMetricsProvider]
	assert.True(t, hasLog && hasMetric, "log/metric default-provider toggles must still be present")
}

// TestDoSolarWindsGETWithContext_Cancellation verifies the context is actually
// wired into the HTTP call: the background wrapper succeeds, a cancelled context
// aborts in-flight.
func TestDoSolarWindsGETWithContext_Cancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// Back-compat wrapper (context.Background) still works.
	body, status, err := DoSolarWindsGET("tok", srv.URL, "/v1/metrics", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, `{"ok":true}`, string(body))

	// A cancelled context aborts the request rather than running to completion.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = DoSolarWindsGETWithContext(ctx, "tok", srv.URL, "/v1/metrics", nil)
	require.Error(t, err, "a cancelled context must abort the SolarWinds GET")
	assert.Contains(t, err.Error(), "context canceled")
}
