package gcp

import (
	"encoding/json"

	"nudgebee/services/knowledge_graph/sources"
)

// GCP load-balancer topology, reconstructed from the `cloud_resourses` rows the
// source has already loaded, instead of from `gcloud compute ... list`.
//
// BuildGraph fetches every row for the account before it does anything else, and
// daily discovery has already written the same load-balancer chain the graph
// walks. Running a gcloud command for it costs a `gcloud auth
// activate-service-account` plus the command itself — two Python process starts,
// roughly 1.5 CPU-seconds — per list, per build, per account.
//
// The catch is that the collector does not persist gcloud's own JSON. It writes
// its own snake_case projection of the protobuf, so unlike the AWS types (whose
// meta is the CLI payload verbatim) each type needs an explicit mapper. The
// intermediate structs below are that projection, named for the collector
// function that writes them, so the two can be diffed when either side changes:
//
//	backendServiceMeta  <- gcloud_load_balancing.go backendServiceToResource
//	healthCheckMeta     <- gcloud_load_balancing.go healthCheckToResource
//	forwardingRuleMeta  <- gcloud_load_balancing.go forwardingRuleToResource
//	targetProxyMeta     <- gcloud_load_balancing.go targetHttpProxyToResource
//	                       and targetHttpsProxyToResource
//
// url-map is deliberately absent. The collector stores `host_rules_count` and
// `path_matchers_count` — counts, not rules — while the graph needs the actual
// host rules and path matchers to build path-based routing edges. Reading that
// row would silently drop those edges, so URL maps stay on the CLI until the
// collector persists the real payload.

// Row types written by the GCP collector for the load-balancer chain.
const (
	rowTypeBackendService       = "backend-service"
	rowTypeHealthCheck          = "health-check"
	rowTypeForwardingRule       = "forwarding-rule"
	rowTypeTargetHTTPProxy      = "target-http-proxy"
	rowTypeTargetHTTPSProxy     = "target-https-proxy"
	rowTypeNetworkEndpointGroup = "network-endpoint-group"
)

// gcpRowIndex groups the account's already-loaded rows by type.
type gcpRowIndex map[string][]sources.CloudResourceRow

func newGCPRowIndex(resources []sources.CloudResourceRow) gcpRowIndex {
	index := make(gcpRowIndex)
	for _, row := range resources {
		index[row.Type] = append(index[row.Type], row)
	}
	return index
}

func (i gcpRowIndex) rows(resourceType string) []sources.CloudResourceRow {
	return i[resourceType]
}

// --- backend services --------------------------------------------------------

type backendServiceMeta struct {
	Protocol                     string   `json:"protocol"`
	Port                         int      `json:"port"`
	PortName                     string   `json:"port_name"`
	TimeoutSec                   int      `json:"timeout_sec"`
	LoadBalancingScheme          string   `json:"load_balancing_scheme"`
	HealthChecks                 []string `json:"health_checks"`
	SessionAffinity              string   `json:"session_affinity"`
	ConnectionDrainingTimeoutSec int      `json:"connection_draining_timeout_sec"`
	Backends                     []struct {
		Group          string  `json:"group"`
		BalancingMode  string  `json:"balancing_mode"`
		MaxUtilization float64 `json:"max_utilization"`
		CapacityScaler float64 `json:"capacity_scaler"`
	} `json:"backends"`
}

func backendServicesFromRows(rows []sources.CloudResourceRow) []GCPBackendService {
	services := make([]GCPBackendService, 0, len(rows))
	for _, row := range rows {
		var meta backendServiceMeta
		if err := json.Unmarshal(row.Meta, &meta); err != nil {
			continue
		}

		service := GCPBackendService{
			Name:                row.Name,
			Region:              row.Region,
			SelfLink:            row.ARN, // the collector stores selfLink in Arn
			Protocol:            meta.Protocol,
			Port:                meta.Port,
			PortName:            meta.PortName,
			TimeoutSec:          meta.TimeoutSec,
			LoadBalancingScheme: meta.LoadBalancingScheme,
			HealthChecks:        meta.HealthChecks,
			SessionAffinity:     meta.SessionAffinity,
		}
		service.ConnectionDraining.DrainingTimeoutSec = meta.ConnectionDrainingTimeoutSec

		for _, backend := range meta.Backends {
			service.Backends = append(service.Backends, struct {
				Group          string  `json:"group"`
				BalancingMode  string  `json:"balancingMode"`
				MaxUtilization float64 `json:"maxUtilization"`
				CapacityScaler float64 `json:"capacityScaler"`
			}{
				Group:          backend.Group,
				BalancingMode:  backend.BalancingMode,
				MaxUtilization: backend.MaxUtilization,
				CapacityScaler: backend.CapacityScaler,
			})
		}

		services = append(services, service)
	}
	return services
}

// --- health checks -----------------------------------------------------------

type healthCheckPortMeta struct {
	Port        int    `json:"port"`
	RequestPath string `json:"request_path"`
}

type healthCheckMeta struct {
	Type               string               `json:"type"`
	CheckIntervalSec   int                  `json:"check_interval_sec"`
	TimeoutSec         int                  `json:"timeout_sec"`
	HealthyThreshold   int                  `json:"healthy_threshold"`
	UnhealthyThreshold int                  `json:"unhealthy_threshold"`
	HTTPHealthCheck    *healthCheckPortMeta `json:"http_health_check"`
	HTTPSHealthCheck   *healthCheckPortMeta `json:"https_health_check"`
	TCPHealthCheck     *healthCheckPortMeta `json:"tcp_health_check"`
}

func healthChecksFromRows(rows []sources.CloudResourceRow) []GCPHealthCheck {
	checks := make([]GCPHealthCheck, 0, len(rows))
	for _, row := range rows {
		var meta healthCheckMeta
		if err := json.Unmarshal(row.Meta, &meta); err != nil {
			continue
		}

		check := GCPHealthCheck{
			Name:               row.Name,
			SelfLink:           row.ARN,
			Type:               meta.Type,
			CheckIntervalSec:   meta.CheckIntervalSec,
			TimeoutSec:         meta.TimeoutSec,
			HealthyThreshold:   meta.HealthyThreshold,
			UnhealthyThreshold: meta.UnhealthyThreshold,
		}
		if meta.HTTPHealthCheck != nil {
			check.HttpHealthCheck = &struct {
				Port        int    `json:"port"`
				RequestPath string `json:"requestPath"`
			}{Port: meta.HTTPHealthCheck.Port, RequestPath: meta.HTTPHealthCheck.RequestPath}
		}
		if meta.HTTPSHealthCheck != nil {
			check.HttpsHealthCheck = &struct {
				Port        int    `json:"port"`
				RequestPath string `json:"requestPath"`
			}{Port: meta.HTTPSHealthCheck.Port, RequestPath: meta.HTTPSHealthCheck.RequestPath}
		}
		if meta.TCPHealthCheck != nil {
			check.TcpHealthCheck = &struct {
				Port int `json:"port"`
			}{Port: meta.TCPHealthCheck.Port}
		}

		checks = append(checks, check)
	}
	return checks
}

// --- forwarding rules --------------------------------------------------------

type forwardingRuleMeta struct {
	IPAddress           string   `json:"ip_address"`
	IPProtocol          string   `json:"ip_protocol"`
	PortRange           string   `json:"port_range"`
	Ports               []string `json:"ports"`
	Target              string   `json:"target"`
	BackendService      string   `json:"backend_service"`
	LoadBalancingScheme string   `json:"load_balancing_scheme"`
	Network             string   `json:"network"`
	Subnetwork          string   `json:"subnetwork"`
	NetworkTier         string   `json:"network_tier"`
}

func forwardingRulesFromRows(rows []sources.CloudResourceRow) []GCPForwardingRule {
	rules := make([]GCPForwardingRule, 0, len(rows))
	for _, row := range rows {
		var meta forwardingRuleMeta
		if err := json.Unmarshal(row.Meta, &meta); err != nil {
			continue
		}

		rules = append(rules, GCPForwardingRule{
			Name:                row.Name,
			Region:              row.Region,
			SelfLink:            row.ARN,
			IPAddress:           meta.IPAddress,
			IPProtocol:          meta.IPProtocol,
			PortRange:           meta.PortRange,
			Ports:               meta.Ports,
			Target:              meta.Target,
			BackendService:      meta.BackendService,
			LoadBalancingScheme: meta.LoadBalancingScheme,
			Network:             meta.Network,
			Subnetwork:          meta.Subnetwork,
			NetworkTier:         meta.NetworkTier,
			Labels:              labelsFromTags(row.Tags),
		})
	}
	return rules
}

// labelsFromTags flattens the collector's key -> []value tags column into the
// single-valued label map the CLI shape carries.
func labelsFromTags(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var tags map[string][]string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil
	}
	labels := make(map[string]string, len(tags))
	for key, values := range tags {
		if len(values) > 0 {
			labels[key] = values[0]
		} else {
			labels[key] = ""
		}
	}
	return labels
}

// --- target proxies ----------------------------------------------------------

type targetProxyMeta struct {
	URLMap          string   `json:"url_map"`
	SSLCertificates []string `json:"ssl_certificates"`
}

// targetProxiesFromRows maps both proxy types. proxyType is stamped from the row
// type rather than the payload, matching what the CLI fetchers do.
func targetProxiesFromRows(rows []sources.CloudResourceRow, proxyType string) []GCPTargetProxy {
	proxies := make([]GCPTargetProxy, 0, len(rows))
	for _, row := range rows {
		var meta targetProxyMeta
		if err := json.Unmarshal(row.Meta, &meta); err != nil {
			continue
		}
		proxies = append(proxies, GCPTargetProxy{
			Name:            row.Name,
			SelfLink:        row.ARN,
			UrlMap:          meta.URLMap,
			SslCertificates: meta.SSLCertificates,
			ProxyType:       proxyType,
		})
	}
	return proxies
}

// --- network endpoint groups -------------------------------------------------

type negMeta struct {
	NetworkEndpointType string `json:"network_endpoint_type"`
	CloudRun            *struct {
		Service string `json:"service"`
	} `json:"cloud_run"`
	AppEngine *struct {
		Service string `json:"service"`
	} `json:"app_engine"`
}

// serverlessNEGsFromRows keeps only the NEGs that name a Cloud Run or App Engine
// service, mirroring the filter the CLI fetcher applies. Zonal GCE_VM_IP_PORT
// NEGs (the ones GKE creates per zone) are collected but dropped here, because
// the consumer resolves a backend service to a *serverless* workload.
//
// An account whose NEG rows contain no serverless entries returns an empty slice,
// and that is a real answer rather than a miss: the caller only falls back to the
// CLI when there are no NEG rows at all.
func serverlessNEGsFromRows(rows []sources.CloudResourceRow) []GCPServerlessNEG {
	negs := make([]GCPServerlessNEG, 0, len(rows))
	for _, row := range rows {
		var meta negMeta
		if err := json.Unmarshal(row.Meta, &meta); err != nil {
			continue
		}
		if meta.CloudRun == nil && meta.AppEngine == nil {
			continue
		}

		neg := GCPServerlessNEG{
			Name:                row.Name,
			Region:              row.Region,
			NetworkEndpointType: meta.NetworkEndpointType,
		}
		if meta.CloudRun != nil {
			neg.CloudRun = &struct {
				Service string `json:"service"`
			}{Service: meta.CloudRun.Service}
		}
		if meta.AppEngine != nil {
			neg.AppEngine = &struct {
				Service string `json:"service"`
			}{Service: meta.AppEngine.Service}
		}
		negs = append(negs, neg)
	}
	return negs
}
