package services_server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// TestListMetricsSeriesLabelValues_TimeoutBounds guards the timeout added to
// stop the observed prod hang class where an /rpc/metrics call for
// metrics_list_label_values could stall the caller for ~85 minutes until the
// conversation TTL reaped it (2026-08-03 conv 9591d843 + a9ea0c9e).
//
// The test stands up a fake service endpoint that never responds (server
// blocks the goroutine forever until t.Cleanup unblocks it), swaps in a
// short test timeout, and asserts the call errors out promptly instead of
// hanging until the Go test binary's 10-min default deadline.
func TestListMetricsSeriesLabelValues_TimeoutBounds(t *testing.T) {
	// Never-respond handler: blocks until we signal `done` so the HTTP client
	// has to give up on its own timeout. Cleanup order matters: httptest.Server.Close
	// blocks until in-flight handlers return, so `done` MUST close BEFORE
	// server.Close runs. t.Cleanup is LIFO, so register server.Close first
	// (runs LAST) and done-close second (runs FIRST at teardown).
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(done) })

	origEndpoint := config.Config.ServiceEndpoint
	origTimeout := metricsLabelValuesRequestTimeout
	t.Cleanup(func() {
		config.Config.ServiceEndpoint = origEndpoint
		metricsLabelValuesRequestTimeout = origTimeout
	})

	config.Config.ServiceEndpoint = server.URL
	metricsLabelValuesRequestTimeout = 300 * time.Millisecond

	// Set tenant directly on ctx so we skip the DB fallback lookup at the
	// call site (security.GetTenantIdFromAccountId) — otherwise the test
	// would depend on the metastore being reachable.
	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	// Budget: give the call 5x the timeout to complete. A working timeout fires
	// well within this; a broken one would blow past and only die on the test's
	// 30s cap below.
	start := time.Now()
	done2 := make(chan error, 1)
	go func() {
		_, err := ListMetricsSeriesLabelValues(*ctx, "test-account", "prometheus", "instance")
		done2 <- err
	}()

	select {
	case err := <-done2:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatalf("expected error from timed-out request, got nil (elapsed %s)", elapsed)
		}
		// Client-side timeouts surface as "context deadline exceeded" wrapped
		// by net/http; be liberal in what we accept (both %v and Error()).
		msg := err.Error()
		if !strings.Contains(strings.ToLower(msg), "deadline") &&
			!strings.Contains(strings.ToLower(msg), "timeout") &&
			!strings.Contains(strings.ToLower(msg), "canceled") {
			t.Fatalf("expected timeout/deadline error, got %q (elapsed %s)", msg, elapsed)
		}
		// Timeout should fire close to the configured 300ms — allow generous
		// slack for CI jitter, but reject anything obviously outside the bound.
		if elapsed > 5*time.Second {
			t.Fatalf("call took %s — timeout is not being honored (should fire within ~300ms)", elapsed)
		}
		t.Logf("ok: call returned %q after %s (configured timeout %s)", msg, elapsed, metricsLabelValuesRequestTimeout)
	case <-time.After(30 * time.Second):
		t.Fatal("test itself timed out after 30s — production timeout is definitely not applied")
	}
}
