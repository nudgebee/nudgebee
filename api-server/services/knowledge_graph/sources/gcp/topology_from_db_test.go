package gcp

import (
	"encoding/json"
	"testing"

	"nudgebee/services/knowledge_graph/sources"
)

// The meta payloads below are copied from what the collector writes in
// collector-server/cloud-collector/providers/gcloud/gcloud_load_balancing.go.
// If a mapper here drifts from that writer the graph silently loses edges — the
// row is present, so nothing falls back to the CLI — which is what these tests
// exist to catch. Project and resource names are synthetic.

func gcpRow(resourceType, name, region, selfLink, meta string) sources.CloudResourceRow {
	return sources.CloudResourceRow{
		Name:     name,
		Type:     resourceType,
		Region:   region,
		ARN:      selfLink,
		Meta:     json.RawMessage(meta),
		IsActive: true,
	}
}

// backendServiceToResource writes these keys.
const backendServiceMetaJSON = `{
  "description": "web backend",
  "protocol": "HTTP",
  "port": 80,
  "port_name": "http",
  "timeout_sec": 30,
  "load_balancing_scheme": "EXTERNAL_MANAGED",
  "health_checks": ["https://www.googleapis.com/compute/v1/projects/p/global/healthChecks/web-hc"],
  "session_affinity": "NONE",
  "connection_draining_timeout_sec": 300,
  "backends": [
    {"group": "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instanceGroups/web-ig",
     "balancing_mode": "UTILIZATION", "max_utilization": 0.8, "capacity_scaler": 1}
  ]
}`

func TestBackendServicesFromRows(t *testing.T) {
	rows := []sources.CloudResourceRow{
		gcpRow(rowTypeBackendService, "web-backend", "global",
			"https://www.googleapis.com/compute/v1/projects/p/global/backendServices/web-backend",
			backendServiceMetaJSON),
	}

	services := backendServicesFromRows(rows)
	if len(services) != 1 {
		t.Fatalf("got %d services, want 1", len(services))
	}
	s := services[0]

	if s.Name != "web-backend" {
		t.Errorf("Name = %q", s.Name)
	}
	if s.Region != "global" {
		t.Errorf("Region = %q", s.Region)
	}
	// The collector stores selfLink in the arn column; edge builders resolve
	// backends by selfLink, so losing it breaks the LB chain.
	if s.SelfLink != "https://www.googleapis.com/compute/v1/projects/p/global/backendServices/web-backend" {
		t.Errorf("SelfLink = %q, want the arn column", s.SelfLink)
	}
	if s.Protocol != "HTTP" || s.Port != 80 || s.PortName != "http" {
		t.Errorf("protocol/port/portName = %q/%d/%q", s.Protocol, s.Port, s.PortName)
	}
	if s.TimeoutSec != 30 {
		t.Errorf("TimeoutSec = %d", s.TimeoutSec)
	}
	if s.LoadBalancingScheme != "EXTERNAL_MANAGED" {
		t.Errorf("LoadBalancingScheme = %q", s.LoadBalancingScheme)
	}
	if s.SessionAffinity != "NONE" {
		t.Errorf("SessionAffinity = %q", s.SessionAffinity)
	}
	if s.ConnectionDraining.DrainingTimeoutSec != 300 {
		t.Errorf("DrainingTimeoutSec = %d", s.ConnectionDraining.DrainingTimeoutSec)
	}

	// health_checks drives backend-service -> health-check edges.
	if len(s.HealthChecks) != 1 ||
		s.HealthChecks[0] != "https://www.googleapis.com/compute/v1/projects/p/global/healthChecks/web-hc" {
		t.Errorf("HealthChecks = %+v", s.HealthChecks)
	}

	// backends[].group drives backend-service -> instance-group / NEG edges.
	if len(s.Backends) != 1 {
		t.Fatalf("got %d backends, want 1", len(s.Backends))
	}
	b := s.Backends[0]
	if b.Group != "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instanceGroups/web-ig" {
		t.Errorf("backend Group = %q", b.Group)
	}
	if b.BalancingMode != "UTILIZATION" {
		t.Errorf("BalancingMode = %q (collector writes balancing_mode)", b.BalancingMode)
	}
	if b.MaxUtilization != 0.8 {
		t.Errorf("MaxUtilization = %v (collector writes max_utilization)", b.MaxUtilization)
	}
	if b.CapacityScaler != 1 {
		t.Errorf("CapacityScaler = %v (collector writes capacity_scaler)", b.CapacityScaler)
	}
}

// healthCheckToResource writes these keys.
const healthCheckMetaJSON = `{
  "type": "HTTP",
  "check_interval_sec": 5,
  "timeout_sec": 5,
  "healthy_threshold": 2,
  "unhealthy_threshold": 3,
  "http_health_check": {"port": 8080, "request_path": "/healthz"}
}`

func TestHealthChecksFromRows(t *testing.T) {
	rows := []sources.CloudResourceRow{
		gcpRow(rowTypeHealthCheck, "web-hc", "global",
			"https://www.googleapis.com/compute/v1/projects/p/global/healthChecks/web-hc",
			healthCheckMetaJSON),
	}

	checks := healthChecksFromRows(rows)
	if len(checks) != 1 {
		t.Fatalf("got %d checks, want 1", len(checks))
	}
	c := checks[0]

	if c.Name != "web-hc" || c.Type != "HTTP" {
		t.Errorf("Name/Type = %q/%q", c.Name, c.Type)
	}
	if c.SelfLink != "https://www.googleapis.com/compute/v1/projects/p/global/healthChecks/web-hc" {
		t.Errorf("SelfLink = %q", c.SelfLink)
	}
	if c.CheckIntervalSec != 5 || c.TimeoutSec != 5 {
		t.Errorf("intervals = %d/%d", c.CheckIntervalSec, c.TimeoutSec)
	}
	if c.HealthyThreshold != 2 || c.UnhealthyThreshold != 3 {
		t.Errorf("thresholds = %d/%d", c.HealthyThreshold, c.UnhealthyThreshold)
	}
	if c.HttpHealthCheck == nil {
		t.Fatal("HttpHealthCheck is nil (collector writes http_health_check)")
	}
	if c.HttpHealthCheck.Port != 8080 || c.HttpHealthCheck.RequestPath != "/healthz" {
		t.Errorf("http check = %+v (collector writes request_path)", *c.HttpHealthCheck)
	}
	if c.HttpsHealthCheck != nil || c.TcpHealthCheck != nil {
		t.Error("absent check types must stay nil")
	}
}

func TestHealthChecksFromRowsOtherProtocols(t *testing.T) {
	rows := []sources.CloudResourceRow{
		gcpRow(rowTypeHealthCheck, "tls-hc", "global", "link",
			`{"type": "HTTPS", "https_health_check": {"port": 443, "request_path": "/"}}`),
		gcpRow(rowTypeHealthCheck, "tcp-hc", "global", "link",
			`{"type": "TCP", "tcp_health_check": {"port": 6379}}`),
	}

	checks := healthChecksFromRows(rows)
	if len(checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(checks))
	}
	if checks[0].HttpsHealthCheck == nil || checks[0].HttpsHealthCheck.Port != 443 {
		t.Errorf("https check = %+v", checks[0].HttpsHealthCheck)
	}
	if checks[1].TcpHealthCheck == nil || checks[1].TcpHealthCheck.Port != 6379 {
		t.Errorf("tcp check = %+v", checks[1].TcpHealthCheck)
	}
}

// forwardingRuleToResource writes these keys.
const forwardingRuleMetaJSON = `{
  "ip_address": "34.120.0.1",
  "ip_protocol": "TCP",
  "port_range": "443-443",
  "ports": ["443"],
  "target": "https://www.googleapis.com/compute/v1/projects/p/global/targetHttpsProxies/web-proxy",
  "backend_service": "https://www.googleapis.com/compute/v1/projects/p/global/backendServices/web-backend",
  "load_balancing_scheme": "EXTERNAL_MANAGED",
  "network": "projects/p/global/networks/default",
  "subnetwork": "projects/p/regions/us-central1/subnetworks/default",
  "network_tier": "PREMIUM"
}`

func TestForwardingRulesFromRows(t *testing.T) {
	row := gcpRow(rowTypeForwardingRule, "web-fr", "global",
		"https://www.googleapis.com/compute/v1/projects/p/global/forwardingRules/web-fr",
		forwardingRuleMetaJSON)
	row.Tags = json.RawMessage(`{"env": ["prod"], "team": []}`)

	rules := forwardingRulesFromRows([]sources.CloudResourceRow{row})
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	r := rules[0]

	if r.Name != "web-fr" || r.Region != "global" {
		t.Errorf("Name/Region = %q/%q", r.Name, r.Region)
	}
	if r.IPAddress != "34.120.0.1" || r.IPProtocol != "TCP" {
		t.Errorf("ip = %q/%q (collector writes ip_address, ip_protocol)", r.IPAddress, r.IPProtocol)
	}
	if r.PortRange != "443-443" || len(r.Ports) != 1 || r.Ports[0] != "443" {
		t.Errorf("ports = %q/%+v", r.PortRange, r.Ports)
	}
	// target and backend_service are what chain a forwarding rule to the rest of
	// the load balancer.
	if r.Target != "https://www.googleapis.com/compute/v1/projects/p/global/targetHttpsProxies/web-proxy" {
		t.Errorf("Target = %q", r.Target)
	}
	if r.BackendService != "https://www.googleapis.com/compute/v1/projects/p/global/backendServices/web-backend" {
		t.Errorf("BackendService = %q (collector writes backend_service)", r.BackendService)
	}
	if r.LoadBalancingScheme != "EXTERNAL_MANAGED" || r.NetworkTier != "PREMIUM" {
		t.Errorf("scheme/tier = %q/%q", r.LoadBalancingScheme, r.NetworkTier)
	}
	if r.Network != "projects/p/global/networks/default" || r.Subnetwork != "projects/p/regions/us-central1/subnetworks/default" {
		t.Errorf("network/subnetwork = %q/%q", r.Network, r.Subnetwork)
	}
	if r.Labels["env"] != "prod" {
		t.Errorf("Labels = %+v", r.Labels)
	}
	if v, ok := r.Labels["team"]; !ok || v != "" {
		t.Errorf("a label key with no values must map to empty, got %q ok=%v", v, ok)
	}
}

func TestTargetProxiesFromRows(t *testing.T) {
	httpRows := []sources.CloudResourceRow{
		gcpRow(rowTypeTargetHTTPProxy, "web-http-proxy", "global",
			"https://www.googleapis.com/compute/v1/projects/p/global/targetHttpProxies/web-http-proxy",
			`{"url_map": "https://www.googleapis.com/compute/v1/projects/p/global/urlMaps/web-map"}`),
	}
	httpsRows := []sources.CloudResourceRow{
		gcpRow(rowTypeTargetHTTPSProxy, "web-https-proxy", "global",
			"https://www.googleapis.com/compute/v1/projects/p/global/targetHttpsProxies/web-https-proxy",
			`{"url_map": "https://www.googleapis.com/compute/v1/projects/p/global/urlMaps/web-map",
			  "ssl_certificates": ["projects/p/global/sslCertificates/web-cert"]}`),
	}

	proxies := targetProxiesFromRows(httpRows, "HTTP")
	proxies = append(proxies, targetProxiesFromRows(httpsRows, "HTTPS")...)
	if len(proxies) != 2 {
		t.Fatalf("got %d proxies, want 2", len(proxies))
	}

	// url_map is the link from proxy to URL map — the next hop in the chain.
	if proxies[0].UrlMap != "https://www.googleapis.com/compute/v1/projects/p/global/urlMaps/web-map" {
		t.Errorf("HTTP proxy UrlMap = %q (collector writes url_map)", proxies[0].UrlMap)
	}
	if proxies[0].ProxyType != "HTTP" {
		t.Errorf("ProxyType = %q", proxies[0].ProxyType)
	}
	if proxies[1].ProxyType != "HTTPS" {
		t.Errorf("ProxyType = %q", proxies[1].ProxyType)
	}
	if len(proxies[1].SslCertificates) != 1 {
		t.Errorf("SslCertificates = %+v (collector writes ssl_certificates)", proxies[1].SslCertificates)
	}
}

// Malformed meta must be skipped, not panic or produce a zero-valued entry that
// looks like a real resource.
func TestMappersSkipUnparseableMeta(t *testing.T) {
	bad := []sources.CloudResourceRow{gcpRow("x", "broken", "global", "link", `not json`)}

	if got := backendServicesFromRows(bad); len(got) != 0 {
		t.Errorf("backendServicesFromRows = %+v, want none", got)
	}
	if got := healthChecksFromRows(bad); len(got) != 0 {
		t.Errorf("healthChecksFromRows = %+v, want none", got)
	}
	if got := forwardingRulesFromRows(bad); len(got) != 0 {
		t.Errorf("forwardingRulesFromRows = %+v, want none", got)
	}
	if got := targetProxiesFromRows(bad, "HTTP"); len(got) != 0 {
		t.Errorf("targetProxiesFromRows = %+v, want none", got)
	}
}

func TestGCPRowIndex(t *testing.T) {
	index := newGCPRowIndex([]sources.CloudResourceRow{
		gcpRow(rowTypeBackendService, "bs-1", "global", "l1", `{}`),
		gcpRow(rowTypeBackendService, "bs-2", "us-central1", "l2", `{}`),
		gcpRow(rowTypeHealthCheck, "hc-1", "global", "l3", `{}`),
	})

	if got := len(index.rows(rowTypeBackendService)); got != 2 {
		t.Errorf("backend-service rows = %d, want 2", got)
	}
	if got := len(index.rows(rowTypeHealthCheck)); got != 1 {
		t.Errorf("health-check rows = %d, want 1", got)
	}
	// An uncollected type must return nothing, which is what sends the caller to
	// the CLI.
	if got := len(index.rows("url-map")); got != 0 {
		t.Errorf("url-map rows = %d, want 0", got)
	}
}

// Consumer side of the contract pinned by
// collector-server/cloud-collector/providers/gcloud/gcloud_neg_test.go. The meta
// below is what that writer emits; if the two drift, one of these two tests fails.
func TestServerlessNEGsFromRows(t *testing.T) {
	rows := []sources.CloudResourceRow{
		gcpRow(rowTypeNetworkEndpointGroup, "checkout-neg", "us-central1", "link-a",
			`{"network_endpoint_type":"SERVERLESS","cloud_run":{"service":"checkout"}}`),
		gcpRow(rowTypeNetworkEndpointGroup, "legacy-neg", "us-central1", "link-b",
			`{"network_endpoint_type":"SERVERLESS","app_engine":{"service":"legacy"}}`),
		// Zonal GKE NEG: collected, but not a serverless backing service.
		gcpRow(rowTypeNetworkEndpointGroup, "k8s1-abc", "us-central1-a", "link-c",
			`{"network_endpoint_type":"GCE_VM_IP_PORT","size":1}`),
	}

	negs := serverlessNEGsFromRows(rows)
	if len(negs) != 2 {
		t.Fatalf("got %d serverless NEGs, want 2 (the zonal GKE NEG must be filtered out)", len(negs))
	}

	if negs[0].CloudRun == nil || negs[0].CloudRun.Service != "checkout" {
		t.Errorf("cloud_run.service not mapped: %+v", negs[0].CloudRun)
	}
	if negs[0].NetworkEndpointType != "SERVERLESS" {
		t.Errorf("NetworkEndpointType = %q", negs[0].NetworkEndpointType)
	}
	if negs[1].AppEngine == nil || negs[1].AppEngine.Service != "legacy" {
		t.Errorf("app_engine.service not mapped: %+v", negs[1].AppEngine)
	}
}

// An account whose NEGs are all zonal yields an empty slice, not a miss — the
// caller only falls back to the CLI when there are no NEG rows at all.
func TestServerlessNEGsFromRowsAllZonal(t *testing.T) {
	rows := []sources.CloudResourceRow{
		gcpRow(rowTypeNetworkEndpointGroup, "k8s1-abc", "us-central1-a", "l",
			`{"network_endpoint_type":"GCE_VM_IP_PORT"}`),
	}
	if got := serverlessNEGsFromRows(rows); len(got) != 0 {
		t.Errorf("got %+v, want no serverless NEGs", got)
	}
}
