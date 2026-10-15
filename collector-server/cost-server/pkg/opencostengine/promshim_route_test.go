package opencostengine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Each cluster goes down the path that can answer it: an agent-served cluster to
// the relay passthrough (metrics_query loses OpenCost's pod-lifetime subqueries
// there), a cluster served by a direct integration to metrics_query (there is no
// Prometheus behind its agent). The passthrough must still receive OpenCost's
// form body, so the decision has to be made without parsing it.
func TestShimRoutesEachClusterToThePathThatServesIt(t *testing.T) {
	var metricsCalls int
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metricsCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"query_key":"q","payload":[]}]}`))
	}))
	defer metrics.Close()

	var cap relayCapture
	relay := fakeRelay(t, &cap)
	defer relay.Close()
	fb, err := newRelayFallback(relay.URL, "sekrit")
	if err != nil {
		t.Fatal(err)
	}

	shim := newPrometheusShim(metrics.URL, "tok", fb)
	shim.router = newMetricsRouter(func(_ context.Context, accountID string) (bool, error) {
		return accountID == "direct-integration", nil
	})

	query := func(account string) {
		t.Helper()
		form := url.Values{"query": {"up"}, "time": {"1719446400"}}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Scope-OrgID", account)
		shim.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", account, rec.Code, rec.Body.String())
		}
	}

	query("agent-served")
	if metricsCalls != 0 {
		t.Errorf("agent-served cluster reached metrics_query %d times", metricsCalls)
	}
	if cap.path != "/prometheus-v2/api/v1/query" {
		t.Errorf("agent-served cluster forwarded to %q, want the passthrough", cap.path)
	}
	if !strings.Contains(cap.body, "query=up") {
		t.Errorf("passthrough body = %q; the form must survive the routing decision", cap.body)
	}

	cap = relayCapture{}
	query("direct-integration")
	if metricsCalls != 1 {
		t.Errorf("direct-integration cluster reached metrics_query %d times, want 1", metricsCalls)
	}
	if cap.path != "" {
		t.Errorf("direct-integration cluster also hit the relay at %q", cap.path)
	}
}

// OpenCost issues dozens of queries per allocation request; the lookup must run
// once per cluster per TTL, not per query, and must run again once it expires.
func TestMetricsRouterCachesPerClusterUntilExpiry(t *testing.T) {
	lookups := 0
	r := newMetricsRouter(func(context.Context, string) (bool, error) {
		lookups++
		return true, nil
	})
	clock := time.Unix(1_000_000, 0)
	r.now = func() time.Time { return clock }

	for range 3 {
		if !r.useObservability(context.Background(), "acct-1") {
			t.Fatal("decision = passthrough, want metrics_query")
		}
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d within the TTL, want 1", lookups)
	}

	clock = clock.Add(routeCacheTTL + time.Second)
	r.useObservability(context.Background(), "acct-1")
	if lookups != 2 {
		t.Fatalf("lookups = %d after expiry, want 2", lookups)
	}
}

// A failed lookup must not move a cluster off the passthrough every cluster used
// before routing existed, and must not be cached — the next query retries.
func TestMetricsRouterFailedLookupKeepsPassthroughAndRetries(t *testing.T) {
	lookups := 0
	r := newMetricsRouter(func(context.Context, string) (bool, error) {
		lookups++
		return true, errors.New("metastore unreachable")
	})

	if r.useObservability(context.Background(), "acct-1") {
		t.Fatal("failed lookup chose metrics_query, want the passthrough")
	}
	r.useObservability(context.Background(), "acct-1")
	if lookups != 2 {
		t.Fatalf("lookups = %d, want 2: a failure must not be cached", lookups)
	}
}
