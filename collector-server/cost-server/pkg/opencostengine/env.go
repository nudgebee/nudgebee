package opencostengine

import (
	"os"
	"strings"
)

// GetNudgebeeDbConnectionString returns the Postgres connection string.
func GetNudgebeeDbConnectionString() string {
	return os.Getenv("NUDGEBEE_DB")
}

func GetNudgebeeRelayEndpoint() string {
	endpoint := os.Getenv("RELAY_SERVER_ENDPOINT")
	endpoint = strings.TrimSuffix(endpoint, "/")
	return endpoint
}

func GetNudgebeeRelayAuthToken() string {
	return os.Getenv("RELAY_SERVER_SECRET_KEY")
}

// GetClusterInfoProvider returns the cluster info provider backend to use.
func GetClusterInfoProvider() string {
	return os.Getenv("CLOUD_COST_PROVIDER")
}

// GetServiceApiServerUrl returns the base URL of services-server (no trailing
// slash). Used by the Prometheus shim to call the observability metrics_query
// RPC action. Supplied by the shared `nudgebee` secret (SERVICE_API_SERVER_URL).
func GetServiceApiServerUrl() string {
	return strings.TrimSuffix(os.Getenv("SERVICE_API_SERVER_URL"), "/")
}

// GetServiceApiServerToken returns the service-to-service action token sent as
// X-ACTION-TOKEN when calling services-server RPC actions. Supplied by the
// shared `nudgebee` secret (ACTION_API_SERVER_TOKEN).
func GetServiceApiServerToken() string {
	return os.Getenv("ACTION_API_SERVER_TOKEN")
}

// GetPrometheusShimAddr returns the loopback address the in-process Prometheus
// shim listens on. OpenCost is pointed at http://<addr> as its Prometheus
// server endpoint when observability-backed metrics are enabled.
func GetPrometheusShimAddr() string {
	if addr := os.Getenv("COST_PROM_SHIM_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:9009"
}

// UseObservabilityMetrics reports whether OpenCost's Prometheus queries should
// be routed through services-server's observability metrics_query API (via the
// in-process shim) instead of relay-server's /prometheus facade. Off by default
// so a missing token/URL can never break the current path.
func UseObservabilityMetrics() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("USE_OBSERVABILITY_METRICS"))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
