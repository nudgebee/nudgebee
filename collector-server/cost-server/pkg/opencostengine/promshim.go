package opencostengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PrometheusShim is an in-process HTTP server that speaks the subset of the
// Prometheus HTTP API upstream OpenCost's client calls (/api/v1/query and
// /api/v1/query_range), but answers each query by calling services-server's
// observability `metrics_query` RPC action instead of querying Prometheus
// through relay-server's /prometheus facade.
//
// Why a localhost HTTP server and not an http.RoundTripper: upstream OpenCost
// hardcodes its transport in prom.NewPrometheusClient and exposes no seam to
// inject a client/RoundTripper — the only configurable seam is the endpoint URL
// (OpenCostPrometheusConfig.ServerEndpoint). So we point that at this shim.
//
// Metadata endpoints (/api/v1/series, /labels, /label/*/values) are reverse-
// proxied unchanged to relay-server's /prometheus facade, so any OpenCost path
// that needs them keeps working. The bulk of cost computation — including the
// two nudgebee allocation queries — uses instant /api/v1/query, which this shim
// translates to metrics_query.
type PrometheusShim struct {
	serviceURL   string // services-server base URL (no trailing slash)
	serviceToken string // X-ACTION-TOKEN value
	httpClient   *http.Client
	fallback     http.Handler // reverse proxy to relay /prometheus (nil disables)
	// router picks the path per cluster. Nil sends every query to metrics_query,
	// which is what the unit tests built through newPrometheusShim exercise.
	router *metricsRouter
}

// shimQueryKey is the single key the shim uses when it forwards a one-off query
// to metrics_query; the response is matched back by position, not by key.
const shimQueryKey = "q"

// shim* mirror the JSON shape of observability.OutputMetricQuery / QueryResult /
// Result so the shim can decode metrics_query responses without importing the
// services-server module.
type shimResult struct {
	Metric     map[string]string `json:"metric"`
	Timestamps []int64           `json:"timestamps"`
	Values     []float64         `json:"values"`
}

type shimQueryResult struct {
	QueryKey string       `json:"query_key"`
	Payload  []shimResult `json:"payload"`
	Query    string       `json:"query"`
	Error    *string      `json:"error"`
}

type shimMetricsOutput struct {
	Results []shimQueryResult `json:"results"`
}

// NewPrometheusShim builds a shim from process configuration. It fails fast if
// the services-server URL or action token is missing so the misconfiguration
// surfaces at boot rather than as empty allocations.
func NewPrometheusShim() (*PrometheusShim, error) {
	// services-server is only needed to answer queries from the observability
	// backend. In the default passthrough mode the shim just proxies to the
	// relay, so requiring those two would refuse to start for no reason.
	var serviceURL, token string
	if UseObservabilityMetrics() {
		serviceURL = GetServiceApiServerUrl()
		if serviceURL == "" {
			return nil, fmt.Errorf("SERVICE_API_SERVER_URL is not set")
		}
		token = GetServiceApiServerToken()
		if token == "" {
			return nil, fmt.Errorf("ACTION_API_SERVER_TOKEN is not set")
		}
	}
	fallback, err := newRelayFallback(GetNudgebeeRelayEndpoint(), GetNudgebeeRelayAuthToken())
	if err != nil {
		return nil, err
	}
	if fallback == nil && serviceURL == "" {
		return nil, fmt.Errorf("neither RELAY_SERVER_ENDPOINT nor an observability backend is configured")
	}
	shim := newPrometheusShim(serviceURL, token, fallback)
	// With a relay to fall back on, metrics_query is used only for clusters the
	// agent cannot serve (see metricsRouter). Without one there is no choice.
	if serviceURL != "" && fallback != nil {
		shim.router = newMetricsRouter(lookupMetricsRoute)
	}
	return shim, nil
}

// newPrometheusShim is the test seam: it takes already-resolved dependencies.
func newPrometheusShim(serviceURL, token string, fallback http.Handler) *PrometheusShim {
	return &PrometheusShim{
		serviceURL:   strings.TrimSuffix(serviceURL, "/"),
		serviceToken: token,
		// Generous timeout: OpenCost issues long-window instant queries.
		httpClient: &http.Client{Timeout: 120 * time.Second},
		fallback:   fallback,
	}
}

// newRelayFallback builds a reverse proxy that forwards Prometheus requests to
// relay-server's /prometheus-v2 facade, which hands them to the agent as a raw
// HTTP proxy and returns the cluster Prometheus' own response untouched.
//
// The alternative facade, /prometheus/api/v1/*, runs the query through the
// agent's prometheus_queries_enricher instead: the agent reshapes Prometheus'
// samples into parallel timestamps/values arrays inside a findings envelope and
// the relay converts them back. OpenCost is a Prometheus client, so that round
// trip only costs time and bytes. The enricher is still the right thing for
// alert evidence and for normalising the other metric backends — it is just
// wrong when the caller already speaks Prometheus.
//
// Auth differs between the two: the enricher facade takes `Authorization:
// Bearer` + `X-Scope-OrgID`, the passthrough takes `X-SECRET-KEY` +
// `X-NB-ACCOUNT-ID`. OpenCost's client can only send the former pair — its
// header name is a hardcoded constant upstream — so translate here. That is
// what this shim is for.
//
// Returns nil (fallback disabled) when no relay endpoint is set.
func newRelayFallback(relayEndpoint, relayToken string) (http.Handler, error) {
	if relayEndpoint == "" {
		return nil, nil
	}
	target, err := url.Parse(relayEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid RELAY_SERVER_ENDPOINT %q: %w", relayEndpoint, err)
	}
	basePath := strings.TrimSuffix(target.Path, "/")
	return &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			r.Host = target.Host
			r.URL.Path = basePath + "/prometheus-v2" + r.URL.Path
			// Clear RawPath: if the incoming path had escaped characters, Go's
			// transport would otherwise use the stale RawPath and ignore the
			// prefix we just added.
			r.URL.RawPath = ""
			// The passthrough selects the account from X-NB-ACCOUNT-ID; OpenCost
			// only knows how to set X-Scope-OrgID. Carry the original across
			// rather than dropping it, so the relay's own logs stay readable.
			if org := r.Header.Get("X-Scope-OrgID"); org != "" {
				r.Header.Set("X-NB-ACCOUNT-ID", org)
			}
			if relayToken != "" {
				r.Header.Set("X-SECRET-KEY", relayToken)
			}
		},
	}, nil
}

// Handler returns the HTTP handler implementing the Prometheus API surface.
//
// Without an observability backend configured every path is proxied straight
// through to the cluster's Prometheus, which is what OpenCost wants. When one
// is configured, instant and range queries are answered from it instead — for
// the clusters the router sends there — and everything else (series, labels,
// label values) still proxies.
func (s *PrometheusShim) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.serviceURL != "" {
		mux.HandleFunc("/api/v1/query", s.handleQuery(true))
		mux.HandleFunc("/api/v1/query_range", s.handleQuery(false))
	}
	mux.HandleFunc("/", s.handleFallback)
	return mux
}

func (s *PrometheusShim) handleQuery(instant bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Decide before ParseForm: the passthrough forwards the request body,
		// which parsing would consume.
		if s.router != nil {
			if accountID := r.Header.Get("X-Scope-OrgID"); accountID != "" && !s.router.useObservability(r.Context(), accountID) {
				s.handleFallback(w, r)
				return
			}
		}
		// ParseForm merges URL query and POST form; the prometheus client uses
		// POST-with-form (GET fallback), so this covers both.
		if err := r.ParseForm(); err != nil {
			writePromError(w, http.StatusBadRequest, "bad_data", "parse form: "+err.Error())
			return
		}
		accountID := r.Header.Get("X-Scope-OrgID")
		if accountID == "" {
			writePromError(w, http.StatusBadRequest, "bad_data", "missing X-Scope-OrgID header")
			return
		}
		promql := r.Form.Get("query")
		if promql == "" {
			writePromError(w, http.StatusBadRequest, "bad_data", "missing query parameter")
			return
		}

		now := time.Now()
		var startMs, endMs int64
		if instant {
			t := parseShimTime(r.Form.Get("time"), now)
			startMs, endMs = t.UnixMilli(), t.UnixMilli()
		} else {
			startMs = parseShimTime(r.Form.Get("start"), now.Add(-time.Hour)).UnixMilli()
			endMs = parseShimTime(r.Form.Get("end"), now).UnixMilli()
		}

		res, err := s.fetchMetric(r.Context(), accountID, promql, startMs, endMs, instant)
		if err != nil {
			writePromError(w, http.StatusBadGateway, "internal", err.Error())
			return
		}
		writePromResult(w, res, instant)
	}
}

func (s *PrometheusShim) handleFallback(w http.ResponseWriter, r *http.Request) {
	if s.fallback == nil {
		writePromError(w, http.StatusNotFound, "not_found", "unsupported endpoint: "+r.URL.Path)
		return
	}
	s.fallback.ServeHTTP(w, r)
}

// fetchMetric forwards a single PromQL query to services-server's metrics_query
// action and returns the (single) query result, or nil when nothing matched.
func (s *PrometheusShim) fetchMetric(ctx context.Context, accountID, promql string, startMs, endMs int64, instant bool) (*shimQueryResult, error) {
	reqBody := map[string]any{
		"action": map[string]any{"name": "metrics_query"},
		"input": map[string]any{
			"request": map[string]any{
				"account_id": accountID,
				// The provider stays pinned: these are OpenCost's own PromQL strings,
				// and handing them to Datadog or Elasticsearch would not parse.
				//
				// The SOURCE is deliberately not pinned. Naming both makes
				// getLogsMetricsTracesProviderWithIntegration skip resolution
				// entirely, which forced every query down the agent relay — so an
				// account whose Prometheus is connected over HTTP, with no agent in
				// the cluster, got "agent not connected" for every allocation query
				// and produced no cost at all. Leaving it empty lets the account's
				// own Prometheus integration decide, and still falls back to the
				// agent when that is what the account has.
				"metric_provider": "prometheus",
				"queries":         map[string]string{shimQueryKey: promql},
				"start_time":      startMs,
				"end_time":        endMs,
				"instant":         instant,
			},
		},
		// Super-admin context (empty tenant/user + role=admin) so metrics_query's
		// per-account RBAC check passes for any cluster cost-server serves.
		"session_variables": map[string]any{"role": "admin"},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.serviceURL+"/rpc/metrics", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-ACTION-TOKEN", s.serviceToken)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics_query returned %d: %s", resp.StatusCode, truncate(body, 256))
	}

	var out shimMetricsOutput
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode metrics_query response: %w", err)
	}
	if len(out.Results) == 0 {
		return nil, nil
	}
	// The shim sends exactly one query, so take the first result.
	res := out.Results[0]
	// Fail closed on a per-query error rather than emitting empty series, which
	// would silently zero a cluster's costs.
	if res.Error != nil && *res.Error != "" {
		return nil, fmt.Errorf("metrics_query error: %s", *res.Error)
	}
	return &res, nil
}

// writePromResult renders a metrics_query result as a native Prometheus
// {status,data:{resultType,result}} envelope. The observability layer returns
// timestamps in seconds and values as float64; Prometheus expects float-second
// timestamps paired with string-encoded sample values.
func writePromResult(w http.ResponseWriter, res *shimQueryResult, instant bool) {
	// An instant query whose expression ends in a subquery (OpenCost's
	// `...[1445m:5m]` pod-lifetime queries) evaluates to a range vector, so
	// Prometheus answers it as a matrix carrying every sample. OpenCost reads
	// each pod's start and end from those samples; collapsing them to one
	// vector sample made every pod start where it ended and produced zero or
	// negative minutes and cost.
	if instant && res != nil {
		for _, series := range res.Payload {
			if len(series.Timestamps) > 1 && len(series.Values) > 1 {
				instant = false
				break
			}
		}
	}
	resultType := "matrix"
	if instant {
		resultType = "vector"
	}

	result := make([]any, 0)
	if res != nil {
		for _, series := range res.Payload {
			metric := series.Metric
			if metric == nil {
				metric = map[string]string{}
			}
			if instant {
				// Instant vector: a single sample per series. Use the last
				// paired index so a length mismatch can't pair a timestamp with
				// an unrelated value (mirrors the range branch below).
				n := len(series.Timestamps)
				if len(series.Values) < n {
					n = len(series.Values)
				}
				if n == 0 {
					continue
				}
				ts := series.Timestamps[n-1]
				val := series.Values[n-1]
				result = append(result, map[string]any{
					"metric": metric,
					"value":  []any{float64(ts), formatSample(val)},
				})
			} else {
				n := len(series.Timestamps)
				if len(series.Values) < n {
					n = len(series.Values)
				}
				values := make([]any, 0, n)
				for i := 0; i < n; i++ {
					values = append(values, []any{float64(series.Timestamps[i]), formatSample(series.Values[i])})
				}
				result = append(result, map[string]any{
					"metric": metric,
					"values": values,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": resultType,
			"result":     result,
		},
	})
}

func writePromError(w http.ResponseWriter, status int, errType, msg string) {
	writeJSON(w, status, map[string]any{
		"status":    "error",
		"errorType": errType,
		"error":     msg,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// formatSample encodes a sample value the way Prometheus does, including the
// special NaN/Inf tokens the client parser understands.
func formatSample(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	default:
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
}

// parseShimTime parses a Prometheus time parameter (unix-seconds float, as the
// client sends, or RFC3339), falling back to def when empty/unparseable.
func parseShimTime(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return def
}
