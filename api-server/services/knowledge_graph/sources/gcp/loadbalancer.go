package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// gcpLoadBalancerSchema is the concrete per-specific_type property schema for the
// GCPLoadBalancer specific_type. In GCP the forwarding rule is the LB frontend;
// names are the
// node.Properties keys written by ensureGCPLoadBalancerNodes + enrichLoadBalancerFromCLI.
// Indexed mirrors core.QueryablePropertiesMap[NodeTypeLoadBalancer] (dns_name/vpc_id)
// plus native scheme/type/ip/public-entry filters.
var gcpLoadBalancerSchema = core.SpecificTypeSchema{
	SpecificType: "GCPLoadBalancer",
	NodeType:     core.NodeTypeLoadBalancer,
	Properties: []core.PropertyDef{
		{Name: "dns_name", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "load_balancing_scheme", Indexed: true},
		{Name: "load_balancer_type", Indexed: true},
		{Name: "ip_address", Indexed: true},
		{Name: "is_public_entry", Indexed: true},
		{Name: "ip_protocol"},
		{Name: "port_range"},
		{Name: "ports"},
		{Name: "network_tier"},
		{Name: "self_link"},
		{Name: "vpc_network_url"},
		{Name: "subnet_id"},
		{Name: "subnet_url"},
		{Name: "backend_service"},
		{Name: "backend_service_url"},
		{Name: "target"},
		{Name: "target_name"},
		{Name: "target_url"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "partial_uri"},
		{Name: "lb_type"},
		{Name: "network_partial_uri"},
		{Name: "project_id"},
		{Name: "subnetwork_partial_uri"},
	},
}

// gcpBackendServiceSchema is the concrete per-specific_type property schema for
// the GCPBackendService specific_type. Names are the node.Properties keys written by
// ensureGCPBackendServiceNodes. Indexed mirrors
// core.QueryablePropertiesMap[NodeTypeBackendPool] (protocol/port); the remaining
// LB-config fields are declared but NOT Indexed (governed by the existing map only).
var gcpBackendServiceSchema = core.SpecificTypeSchema{
	SpecificType: "GCPBackendService",
	NodeType:     core.NodeTypeBackendPool,
	Properties: []core.PropertyDef{
		{Name: "protocol", Indexed: true},
		{Name: "port", Indexed: true},
		{Name: "port_name"},
		{Name: "timeout_sec"},
		{Name: "load_balancing_scheme"},
		{Name: "session_affinity"},
		{Name: "self_link"},
		{Name: "health_checks"},
		{Name: "backends"},
		{Name: "draining_timeout_sec"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "partial_uri"},
		{Name: "project_id"},
		{Name: "description"},
		{Name: "security_policy"},
		{Name: "creation_timestamp"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(gcpLoadBalancerSchema)
	core.RegisterSpecificTypeSchema(gcpBackendServiceSchema)
}

// createServerlessBackendEdges links each LB BackendPool to the Cloud Run / App Engine
// ServerlessFunction it fronts, resolved through the backend's serverless NEG
// (backend → NEG → cloudRun.service / appEngine.service). It also returns a
// backend-service-name → target-node map so Pub/Sub push endpoints that hit a custom
// domain served by that backend can be resolved to the same consumer.
func (s *GCPSource) createServerlessBackendEdges(cliData *gcpCLIData, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbEdge, map[string]*core.DbNode) {
	edges := make([]*core.DbEdge, 0)
	backendTargets := make(map[string]*core.DbNode)
	if cliData == nil || len(cliData.backendServices) == 0 {
		return edges, backendTargets
	}

	cloudRunByName, appEngineNode := indexServerlessNodes(lookup)

	for name, bs := range cliData.backendServices {
		target := resolveBackendTargetNode(bs, cliData, cloudRunByName, appEngineNode)
		if target == nil {
			continue
		}
		backendTargets[name] = target
		if bp := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, name); bp != nil {
			edges = append(edges, s.createEdge(bp, target, core.RelationshipRoutesTo,
				map[string]interface{}{"connection_type": "backend_service"}, req))
		}
	}

	s.logger.Info("created GCP backend → service edges", "edge_count", len(edges))
	return edges, backendTargets
}

// indexServerlessNodes returns Cloud Run ServerlessFunction nodes keyed by short
// name, and the account's single App Engine node (type=app-engine), if present.
func indexServerlessNodes(lookup *sources.NodeLookup) (map[string]*core.DbNode, *core.DbNode) {
	cloudRunByName := make(map[string]*core.DbNode)
	var appEngineNode *core.DbNode
	srv, ok := lookup.ByNodeType[core.NodeTypeServerlessFunction]
	if !ok {
		return cloudRunByName, nil
	}
	for _, n := range srv {
		if t, _ := n.Properties["type"].(string); t == "app-engine" {
			appEngineNode = n
			continue
		}
		if name := extractGCPShortName(getNodeName(n)); name != "" {
			cloudRunByName[name] = n
		}
	}
	return cloudRunByName, appEngineNode
}

// resolveBackendTargetNode walks a backend service's serverless NEG(s) to the
// Cloud Run service node (by name) or the account's App Engine node.
func resolveBackendTargetNode(bs *GCPBackendService, cliData *gcpCLIData, cloudRunByName map[string]*core.DbNode, appEngineNode *core.DbNode) *core.DbNode {
	for _, b := range bs.Backends {
		neg := cliData.serverlessNEGs[extractGCPResourceNameFromURL(b.Group)]
		if neg == nil {
			continue
		}
		if neg.CloudRun != nil && neg.CloudRun.Service != "" {
			if n := cloudRunByName[neg.CloudRun.Service]; n != nil {
				return n
			}
		}
		// An App Engine NEG with an empty service targets the App Engine *default*
		// service (gcloud emits `appEngine: {}`), so match on the object's presence,
		// not a non-empty service name — all App Engine services map to the one node.
		if neg.AppEngine != nil && appEngineNode != nil {
			return appEngineNode
		}
	}
	return nil
}

// addURLMapHostTargets maps each host routed by a url-map to the backend's target
// service node (host → path-matcher default backend → backendTargets), so a push
// endpoint on a custom domain resolves to the service the LB delivers it to.
func addURLMapHostTargets(um *GCPURLMap, backendTargets map[string]*core.DbNode, out map[string]*core.DbNode) {
	pmBackend := make(map[string]string, len(um.PathMatchers))
	for _, pm := range um.PathMatchers {
		pmBackend[pm.Name] = extractGCPResourceNameFromURL(pm.DefaultService)
	}
	for _, hr := range um.HostRules {
		backendName := pmBackend[hr.PathMatcher]
		if backendName == "" {
			backendName = extractGCPResourceNameFromURL(um.DefaultService)
		}
		target := backendTargets[backendName]
		if target == nil {
			continue
		}
		for _, h := range hr.Hosts {
			out[strings.ToLower(h)] = target
		}
	}
}

// extractGCPForwardingRuleMetadata extracts routing info from forwarding rule meta
func (s *GCPSource) extractGCPForwardingRuleMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract target (target pool or target proxy URL) → short name for edge lookup
	if target, ok := metaMap["target"].(string); ok && target != "" {
		properties["target_name"] = extractGCPResourceNameFromURL(target)
		properties["target_url"] = target
	}

	// Extract network/subnet if present (not all forwarding rules have these in DB meta)
	if network, ok := metaMap["network"].(string); ok && network != "" {
		properties["vpc_id"] = extractGCPResourceNameFromURL(network)
		properties["vpc_network_url"] = network
	}
	if subnetwork, ok := metaMap["subnetwork"].(string); ok && subnetwork != "" {
		properties["subnet_id"] = extractGCPResourceNameFromURL(subnetwork)
		properties["subnet_url"] = subnetwork
	}
}

// extractGCPTargetPoolMetadata extracts instance references from target pool meta
func (s *GCPSource) extractGCPTargetPoolMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	instances, ok := metaMap["instances"].([]interface{})
	if !ok || len(instances) == 0 {
		return
	}

	instanceNames := make([]string, 0, len(instances))
	for _, inst := range instances {
		if url, ok := inst.(string); ok && url != "" {
			name := extractGCPResourceNameFromURL(url)
			if name != "" {
				instanceNames = append(instanceNames, name)
			}
		}
	}
	if len(instanceNames) > 0 {
		properties["instance_names"] = instanceNames
	}
}

// fetchForwardingRulesFromGCP fetches all forwarding rules (load balancer frontends) via gcloud CLI
func (s *GCPSource) fetchForwardingRulesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPForwardingRule, error) {
	// Fetch both global and regional forwarding rules
	allRules := []GCPForwardingRule{}

	// Global forwarding rules
	globalCmd := "gcloud compute forwarding-rules list --global --format=json"
	s.logger.Info("fetching GCP global forwarding rules via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   globalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch global forwarding rules", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var rules []GCPForwardingRule
			if err := json.Unmarshal([]byte(output), &rules); err == nil {
				for i := range rules {
					rules[i].Region = "global"
				}
				allRules = append(allRules, rules...)
			}
		}
	}

	// Regional forwarding rules (all regions)
	regionalCmd := "gcloud compute forwarding-rules list --format=json"
	s.logger.Info("fetching GCP regional forwarding rules via CLI", "account_id", accountID)

	resp, err = cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   regionalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch regional forwarding rules", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var rules []GCPForwardingRule
			if err := json.Unmarshal([]byte(output), &rules); err == nil {
				allRules = append(allRules, rules...)
			}
		}
	}

	return allRules, nil
}

// fetchBackendServicesFromGCP fetches all backend services via gcloud CLI
func (s *GCPSource) fetchBackendServicesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPBackendService, error) {
	allServices := []GCPBackendService{}

	// Global backend services
	globalCmd := "gcloud compute backend-services list --global --format=json"
	s.logger.Info("fetching GCP global backend services via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   globalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch global backend services", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var services []GCPBackendService
			if err := json.Unmarshal([]byte(output), &services); err == nil {
				for i := range services {
					services[i].Region = "global"
				}
				allServices = append(allServices, services...)
			}
		}
	}

	// Regional backend services
	regionalCmd := "gcloud compute backend-services list --format=json"
	s.logger.Info("fetching GCP regional backend services via CLI", "account_id", accountID)

	resp, err = cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   regionalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch regional backend services", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var services []GCPBackendService
			if err := json.Unmarshal([]byte(output), &services); err == nil {
				allServices = append(allServices, services...)
			}
		}
	}

	return allServices, nil
}

// fetchHealthChecksFromGCP fetches all health checks via gcloud CLI
func (s *GCPSource) fetchHealthChecksFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPHealthCheck, error) {
	cmd := "gcloud compute health-checks list --format=json"

	s.logger.Info("fetching GCP health checks via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var healthChecks []GCPHealthCheck
	if err := json.Unmarshal([]byte(output), &healthChecks); err != nil {
		return nil, fmt.Errorf("failed to parse health checks response: %w", err)
	}

	return healthChecks, nil
}

// fetchURLMapsFromGCP fetches all URL maps via gcloud CLI
func (s *GCPSource) fetchURLMapsFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPURLMap, error) {
	cmd := "gcloud compute url-maps list --format=json"

	s.logger.Info("fetching GCP URL maps via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var urlMaps []GCPURLMap
	if err := json.Unmarshal([]byte(output), &urlMaps); err != nil {
		return nil, fmt.Errorf("failed to parse URL maps response: %w", err)
	}

	return urlMaps, nil
}

// fetchTargetProxiesFromGCP fetches all target HTTP and HTTPS proxies via gcloud CLI
func (s *GCPSource) fetchTargetProxiesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPTargetProxy, error) {
	allProxies := []GCPTargetProxy{}

	// HTTP proxies
	httpCmd := "gcloud compute target-http-proxies list --format=json"
	s.logger.Info("fetching GCP target HTTP proxies via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   httpCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch target HTTP proxies", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var proxies []GCPTargetProxy
			if err := json.Unmarshal([]byte(output), &proxies); err == nil {
				for i := range proxies {
					proxies[i].ProxyType = "HTTP"
				}
				allProxies = append(allProxies, proxies...)
			}
		}
	}

	// HTTPS proxies
	httpsCmd := "gcloud compute target-https-proxies list --format=json"
	s.logger.Info("fetching GCP target HTTPS proxies via CLI", "account_id", accountID)

	resp, err = cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   httpsCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch target HTTPS proxies", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var proxies []GCPTargetProxy
			if err := json.Unmarshal([]byte(output), &proxies); err == nil {
				for i := range proxies {
					proxies[i].ProxyType = "HTTPS"
				}
				allProxies = append(allProxies, proxies...)
			}
		}
	}

	return allProxies, nil
}

// enrichLoadBalancerFromCLI enriches a forwarding rule node with VPC/subnet/IP info from CLI data.
// When the CLI lookup misses (forwarding rule not in cliData — typically because the CLI fetch
// failed for that region or the rule was created/deleted between fetches), we fall back to
// reading the IP from the node's existing meta (which the DB-side ingestion stores under
// keys like "IPAddress" / "ip_address"). This prevents LoadBalancer nodes from sitting with
// ip_address = NULL, which would block all IP-based cross-account matching rules.
func (s *GCPSource) enrichLoadBalancerFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	fr, ok := cliData.forwardingRules[shortName]
	if !ok {
		// Fallback: try to populate ip_address from existing meta when CLI didn't return this rule.
		s.fillLoadBalancerIPFromMeta(node)
		return
	}

	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if fr.Network != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
			node.Properties["vpc_network_url"] = fr.Network
		}
	}
	if _, hasSubnet := node.Properties["subnet_id"]; !hasSubnet {
		if fr.Subnetwork != "" {
			node.Properties["subnet_id"] = extractGCPResourceNameFromURL(fr.Subnetwork)
			node.Properties["subnet_url"] = fr.Subnetwork
		}
	}

	// Fill in backend_service if not already set from meta
	if _, hasBS := node.Properties["backend_service"]; !hasBS {
		if fr.BackendService != "" {
			node.Properties["backend_service"] = extractGCPResourceNameFromURL(fr.BackendService)
			node.Properties["backend_service_url"] = fr.BackendService
		}
	}

	// Fill in ip_address if not already set — required for the cross-account rule
	// k8s_service_loadbalancer_to_gcp_loadbalancer to match.
	if _, hasIP := node.Properties["ip_address"]; !hasIP {
		if fr.IPAddress != "" {
			node.Properties["ip_address"] = fr.IPAddress
		} else {
			s.fillLoadBalancerIPFromMeta(node)
		}
	}

	// Mark public-facing LBs so the UI can render an "Internet" cap on them.
	if _, hasMarker := node.Properties["is_public_entry"]; !hasMarker && fr.LoadBalancingScheme != "" {
		node.Properties["is_public_entry"] = isGCPLoadBalancerPublic(fr.LoadBalancingScheme)
	}
}

// fillLoadBalancerIPFromMeta reads ip_address from the node's properties.meta map under any of
// several common GCP API field names, as a fallback when the CLI fetch didn't return this rule.
func (s *GCPSource) fillLoadBalancerIPFromMeta(node *core.DbNode) {
	if _, hasIP := node.Properties["ip_address"]; hasIP {
		return
	}
	meta, ok := node.Properties["meta"].(map[string]interface{})
	if !ok {
		return
	}
	for _, key := range []string{"IPAddress", "ip_address", "ipAddress", "frontend_ip", "frontendIpAddress"} {
		if v, ok := meta[key].(string); ok && v != "" {
			node.Properties["ip_address"] = v
			return
		}
	}
}

// isGCPLoadBalancerPublic reports whether a GCP forwarding-rule LoadBalancingScheme value
// indicates the LB is externally reachable from the public internet.
func isGCPLoadBalancerPublic(scheme string) bool {
	return scheme == "EXTERNAL" || scheme == "EXTERNAL_MANAGED"
}

// ensureGCPLoadBalancerNodes creates LoadBalancer nodes from forwarding rules (CLI data)
// In GCP, forwarding rules are the frontend/entry point of load balancers
func (s *GCPSource) ensureGCPLoadBalancerNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, fr := range cliData.forwardingRules {
		// Check if a LoadBalancer node already exists with this name
		var existingNode *core.DbNode
		if lbNodes, ok := lookup.ByNodeType[core.NodeTypeLoadBalancer]; ok {
			for _, existing := range lbNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					existingNode = existing
					break
				}
			}
		}

		// Enrich existing DB node with CLI data (network/subnet not stored in DB meta)
		if existingNode != nil {
			if _, hasVPC := existingNode.Properties["vpc_id"]; !hasVPC {
				if fr.Network != "" {
					existingNode.Properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
					existingNode.Properties["vpc_network_url"] = fr.Network
				}
			}
			if _, hasSubnet := existingNode.Properties["subnet_id"]; !hasSubnet {
				if fr.Subnetwork != "" {
					existingNode.Properties["subnet_id"] = extractGCPResourceNameFromURL(fr.Subnetwork)
					existingNode.Properties["subnet_url"] = fr.Subnetwork
				}
			}
			if _, hasBS := existingNode.Properties["backend_service"]; !hasBS {
				if fr.BackendService != "" {
					existingNode.Properties["backend_service"] = extractGCPResourceNameFromURL(fr.BackendService)
					existingNode.Properties["backend_service_url"] = fr.BackendService
				}
			}
			continue
		}

		if existingNode == nil {
			// Determine load balancer type based on LoadBalancingScheme
			var lbType string
			switch fr.LoadBalancingScheme {
			case "EXTERNAL", "EXTERNAL_MANAGED":
				lbType = "External"
			case "INTERNAL", "INTERNAL_MANAGED":
				lbType = "Internal"
			default:
				lbType = "Network"
			}

			properties := map[string]interface{}{
				"name":                  name,
				"type":                  "forwarding-rule",
				"subtype":               "GCPLoadBalancer",
				"service_name":          "Cloud Load Balancing",
				"cloud_provider":        "GCP",
				"region":                fr.Region,
				"inferred":              false,
				"ip_address":            fr.IPAddress,
				"ip_protocol":           fr.IPProtocol,
				"port_range":            fr.PortRange,
				"load_balancing_scheme": fr.LoadBalancingScheme,
				"load_balancer_type":    lbType,
				"network_tier":          fr.NetworkTier,
				"self_link":             fr.SelfLink,
				"is_public_entry":       isGCPLoadBalancerPublic(fr.LoadBalancingScheme),
			}

			// Add ports if available
			if len(fr.Ports) > 0 {
				properties["ports"] = fr.Ports
			}

			// Add network/subnet info for internal load balancers
			if fr.Network != "" {
				properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
				properties["vpc_network_url"] = fr.Network
			}
			if fr.Subnetwork != "" {
				properties["subnet_id"] = extractGCPResourceNameFromURL(fr.Subnetwork)
				properties["subnet_url"] = fr.Subnetwork
			}

			// Link to backend service if available
			if fr.BackendService != "" {
				properties["backend_service"] = extractGCPResourceNameFromURL(fr.BackendService)
				properties["backend_service_url"] = fr.BackendService
			}

			// Link to target (for target-based LBs)
			if fr.Target != "" {
				targetShortName := extractGCPResourceNameFromURL(fr.Target)
				properties["target"] = targetShortName
				properties["target_name"] = targetShortName // used by createLoadBalancerEdges for edge lookup
				properties["target_url"] = fr.Target
			}

			// Add labels
			if len(fr.Labels) > 0 {
				properties["labels"] = fr.Labels
			}

			// Generate unique key
			uniqueKey := fmt.Sprintf("gcp:%s:%s:%s:%s",
				req.CloudAccountID,
				fr.Region,
				core.NodeTypeLoadBalancer,
				name)

			node := core.NewNode(core.NodeTypeLoadBalancer, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP LoadBalancer node from CLI",
				"name", name,
				"type", lbType,
				"ip", fr.IPAddress)
		}
	}

	return nodes
}

// ensureGCPBackendServiceNodes creates BackendPool nodes from backend services (CLI data)
func (s *GCPSource) ensureGCPBackendServiceNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, bs := range cliData.backendServices {
		// Check if a BackendPool node already exists with this name
		found := false
		if bpNodes, ok := lookup.ByNodeType[core.NodeTypeBackendPool]; ok {
			for _, existing := range bpNodes {
				existingName := extractGCPShortName(getNodeName(existing))
				if existingName == name {
					found = true
					break
				}
			}
		}

		if !found {
			properties := map[string]interface{}{
				"name":                  name,
				"type":                  "backend-service",
				"subtype":               "GCPBackendService",
				"service_name":          "Cloud Load Balancing",
				"cloud_provider":        "GCP",
				"region":                bs.Region,
				"inferred":              false,
				"protocol":              bs.Protocol,
				"port":                  bs.Port,
				"port_name":             bs.PortName,
				"timeout_sec":           bs.TimeoutSec,
				"load_balancing_scheme": bs.LoadBalancingScheme,
				"session_affinity":      bs.SessionAffinity,
				"self_link":             bs.SelfLink,
			}

			// Add health checks
			if len(bs.HealthChecks) > 0 {
				healthCheckNames := make([]string, len(bs.HealthChecks))
				for i, hc := range bs.HealthChecks {
					healthCheckNames[i] = extractGCPResourceNameFromURL(hc)
				}
				properties["health_checks"] = healthCheckNames
			}

			// Add backend groups (instance groups or NEGs)
			if len(bs.Backends) > 0 {
				backendGroups := make([]map[string]interface{}, len(bs.Backends))
				for i, backend := range bs.Backends {
					backendGroups[i] = map[string]interface{}{
						"group":           extractGCPResourceNameFromURL(backend.Group),
						"group_url":       backend.Group,
						"balancing_mode":  backend.BalancingMode,
						"max_utilization": backend.MaxUtilization,
						"capacity_scaler": backend.CapacityScaler,
					}
				}
				properties["backends"] = backendGroups
			}

			// Connection draining
			if bs.ConnectionDraining.DrainingTimeoutSec > 0 {
				properties["draining_timeout_sec"] = bs.ConnectionDraining.DrainingTimeoutSec
			}

			// Generate unique key
			uniqueKey := fmt.Sprintf("gcp:%s:%s:%s:%s",
				req.CloudAccountID,
				bs.Region,
				core.NodeTypeBackendPool,
				name)

			node := core.NewNode(core.NodeTypeBackendPool, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP BackendService node from CLI",
				"name", name,
				"protocol", bs.Protocol,
				"backend_count", len(bs.Backends))
		}
	}

	return nodes
}

// createLoadBalancerEdges creates edges for Load Balancer components
// - LoadBalancer → VPC (HOSTED_ON)
// - LoadBalancer → Subnet (HOSTED_ON)
// - LoadBalancer → BackendPool/TargetPool (ROUTES_TO)
// - TargetPool → ComputeInstance (ROUTES_TO) - for classic TCP/UDP load balancers
// - BackendPool → GKE Cluster (ROUTES_TO) - if backend is a GKE NEG (backend-service based)
func (s *GCPSource) createLoadBalancerEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Get all LoadBalancer nodes
	lbNodes, hasLBs := lookup.ByNodeType[core.NodeTypeLoadBalancer]
	if !hasLBs {
		return edges
	}

	for _, lbNode := range lbNodes {
		// 1. LoadBalancer → VPC edge
		if vpcID, ok := lbNode.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(lbNode, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. LoadBalancer → Subnet edge
		if subnetID, ok := lbNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode := findNodeByNameAndType(lookup, core.NodeTypeSubnet, subnetID); subnetNode != nil {
				edges = append(edges, s.createEdge(lbNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// 3. LoadBalancer → BackendPool (backend-service based LBs)
		if backendService, ok := lbNode.Properties["backend_service"].(string); ok && backendService != "" {
			if bpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, backendService); bpNode != nil {
				edges = append(edges, s.createEdge(lbNode, bpNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "backend_service"}, req))
				s.logger.Debug("created LoadBalancer → BackendPool edge",
					"lb", getNodeName(lbNode),
					"backend_service", backendService)
			}
		}

		// 4. LoadBalancer → TargetPool (classic TCP/UDP LBs use target instead of backend_service)
		if targetName, ok := lbNode.Properties["target_name"].(string); ok && targetName != "" {
			if tpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, targetName); tpNode != nil {
				edges = append(edges, s.createEdge(lbNode, tpNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "target_pool"}, req))
				s.logger.Debug("created LoadBalancer → TargetPool edge",
					"lb", getNodeName(lbNode),
					"target_pool", targetName)
			}
		}
	}

	// BackendPool edges: target-pool → compute instances, backend-service → GKE cluster (NEG)
	bpNodes, hasBPs := lookup.ByNodeType[core.NodeTypeBackendPool]
	if hasBPs {
		computeNodes, hasCompute := lookup.ByNodeType[core.NodeTypeComputeInstance]
		computeByName := make(map[string]*core.DbNode)
		if hasCompute {
			for _, cn := range computeNodes {
				if n, ok := cn.Properties["name"].(string); ok && n != "" {
					computeByName[extractGCPShortName(n)] = cn
				}
			}
		}

		clusterNodes, hasClusters := lookup.ByNodeType[core.NodeTypeManagedCluster]
		clusterByName := make(map[string]*core.DbNode)
		if hasClusters {
			for _, clusterNode := range clusterNodes {
				if name, ok := clusterNode.Properties["name"].(string); ok {
					shortName := extractGCPShortName(name)
					if shortName != "" {
						clusterByName[shortName] = clusterNode
					}
				}
			}
		}

		for _, bpNode := range bpNodes {
			bpType, _ := bpNode.Properties["type"].(string)

			if bpType == "target-pool" {
				// TargetPool → ComputeInstance edges (classic TCP/UDP LBs)
				if instanceNames, ok := bpNode.Properties["instance_names"].([]string); ok {
					for _, instName := range instanceNames {
						if instNode, exists := computeByName[instName]; exists {
							edges = append(edges, s.createEdge(bpNode, instNode, core.RelationshipRoutesTo,
								map[string]interface{}{"connection_type": "target_pool_instance"}, req))
						}
					}
					s.logger.Debug("created TargetPool → ComputeInstance edges",
						"target_pool", getNodeName(bpNode),
						"instance_count", len(instanceNames))
				}
			} else if bpType == "backend-service" {
				// BackendPool → GKE Cluster edges (NEG-based, for backend-service type only)
				if backends, ok := bpNode.Properties["backends"].([]map[string]interface{}); ok {
					for _, backend := range backends {
						if groupURL, ok := backend["group_url"].(string); ok {
							for clusterName, clusterNode := range clusterByName {
								if strings.Contains(groupURL, clusterName) {
									edges = append(edges, s.createEdge(bpNode, clusterNode, core.RelationshipRoutesTo,
										map[string]interface{}{
											"connection_type": "gke_neg",
											"neg_url":         groupURL,
										}, req))
									s.logger.Debug("created BackendPool → GKE Cluster edge",
										"backend_service", getNodeName(bpNode),
										"cluster", clusterName)
									break
								}
							}
						}
					}
				}
			}
		}
	}

	s.logger.Info("created GCP load balancer edges", "edge_count", len(edges))
	return edges
}

// ensureGCPHealthCheckNodes creates HealthCheck nodes from CLI data
func (s *GCPSource) ensureGCPHealthCheckNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, hc := range cliData.healthChecks {
		// Check if already exists
		found := false
		if hcNodes, ok := lookup.ByNodeType[core.NodeTypeCloudResource]; ok {
			for _, existing := range hcNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					existingType, _ := existing.Properties["type"].(string)
					if existingType == "health-check" {
						found = true
						break
					}
				}
			}
		}

		if !found {
			properties := map[string]interface{}{
				"name":                name,
				"type":                "health-check",
				"subtype":             "GCPHealthCheck",
				"service_name":        "Cloud Load Balancing",
				"cloud_provider":      "GCP",
				"inferred":            false,
				"check_interval_sec":  hc.CheckIntervalSec,
				"timeout_sec":         hc.TimeoutSec,
				"healthy_threshold":   hc.HealthyThreshold,
				"unhealthy_threshold": hc.UnhealthyThreshold,
				"health_check_type":   hc.Type,
				"self_link":           hc.SelfLink,
			}

			uniqueKey := fmt.Sprintf("gcp:%s:global:%s:health-check:%s",
				req.CloudAccountID, core.NodeTypeCloudResource, name)

			node := core.NewNode(core.NodeTypeCloudResource, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP HealthCheck node from CLI", "name", name)
		}
	}
	return nodes
}

// ensureGCPTargetProxyNodes creates TargetProxy nodes from CLI data
func (s *GCPSource) ensureGCPTargetProxyNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, proxy := range cliData.targetProxies {
		found := false
		if crNodes, ok := lookup.ByNodeType[core.NodeTypeCloudResource]; ok {
			for _, existing := range crNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					existingType, _ := existing.Properties["type"].(string)
					if existingType == "target-http-proxy" || existingType == "target-https-proxy" {
						found = true
						break
					}
				}
			}
		}

		if !found {
			proxyType := "target-http-proxy"
			if proxy.ProxyType == "HTTPS" {
				proxyType = "target-https-proxy"
			}

			properties := map[string]interface{}{
				"name":           name,
				"type":           proxyType,
				"subtype":        "GCPTargetProxy",
				"service_name":   "Cloud Load Balancing",
				"cloud_provider": "GCP",
				"inferred":       false,
				"proxy_type":     proxy.ProxyType,
				"url_map":        extractGCPResourceNameFromURL(proxy.UrlMap),
				"url_map_url":    proxy.UrlMap,
				"self_link":      proxy.SelfLink,
			}

			uniqueKey := fmt.Sprintf("gcp:%s:global:%s:%s:%s",
				req.CloudAccountID, core.NodeTypeCloudResource, proxyType, name)

			node := core.NewNode(core.NodeTypeCloudResource, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP TargetProxy node from CLI", "name", name, "type", proxyType)
		}
	}
	return nodes
}

// ensureGCPURLMapNodes creates URLMap nodes from CLI data (NodeTypeRouteTable — same concept as routing rules)
func (s *GCPSource) ensureGCPURLMapNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, urlMap := range cliData.urlMaps {
		found := enrichExistingURLMapNode(lookup, name, urlMap)

		if !found {
			defaultService := extractGCPResourceNameFromURL(urlMap.DefaultService)
			properties := map[string]interface{}{
				"name":                name,
				"type":                "url-map",
				"subtype":             "GCPURLMap",
				"service_name":        "Cloud Load Balancing",
				"cloud_provider":      "GCP",
				"inferred":            false,
				"default_service":     defaultService,
				"default_service_url": urlMap.DefaultService,
				"self_link":           urlMap.SelfLink,
			}

			uniqueKey := fmt.Sprintf("gcp:%s:global:%s:url-map:%s",
				req.CloudAccountID, core.NodeTypeRouteTable, name)

			node := core.NewNode(core.NodeTypeRouteTable, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP URLMap node from CLI", "name", name, "default_service", defaultService)
		}
	}
	return nodes
}

// enrichExistingURLMapNode finds a url-map RouteTable node already present in the
// graph (created from the resource table) and stamps the CLI-only routing target
// onto it so the URLMap → BackendService chain edge can form. Returns true when a
// matching node was found, signalling the caller not to create a duplicate.
func enrichExistingURLMapNode(lookup *sources.NodeLookup, name string, urlMap *GCPURLMap) bool {
	rtNodes, ok := lookup.ByNodeType[core.NodeTypeRouteTable]
	if !ok {
		return false
	}
	for _, existing := range rtNodes {
		if extractGCPShortName(getNodeName(existing)) != name {
			continue
		}
		if existing.Properties == nil {
			existing.Properties = make(map[string]interface{})
		}
		if _, has := existing.Properties["default_service"]; !has {
			existing.Properties["default_service"] = extractGCPResourceNameFromURL(urlMap.DefaultService)
			existing.Properties["default_service_url"] = urlMap.DefaultService
			existing.Properties["self_link"] = urlMap.SelfLink
		}
		return true
	}
	return false
}

// createLoadBalancerChainEdges creates the full LB chain:
// ForwardingRule → TargetProxy → URLMap → BackendService, BackendService → HealthCheck
func (s *GCPSource) createLoadBalancerChainEdges(lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build lookup maps for TargetProxy and URLMap nodes (by short name)
	targetProxyByName := make(map[string]*core.DbNode)
	if crNodes, ok := lookup.ByNodeType[core.NodeTypeCloudResource]; ok {
		for _, n := range crNodes {
			t, _ := n.Properties["type"].(string)
			if t == "target-http-proxy" || t == "target-https-proxy" {
				shortName := extractGCPShortName(getNodeName(n))
				if shortName != "" {
					targetProxyByName[shortName] = n
				}
			}
		}
	}

	urlMapByName := make(map[string]*core.DbNode)
	if rtNodes, ok := lookup.ByNodeType[core.NodeTypeRouteTable]; ok {
		for _, n := range rtNodes {
			shortName := extractGCPShortName(getNodeName(n))
			if shortName != "" {
				urlMapByName[shortName] = n
			}
		}
	}

	healthCheckByName := make(map[string]*core.DbNode)
	if crNodes, ok := lookup.ByNodeType[core.NodeTypeCloudResource]; ok {
		for _, n := range crNodes {
			t, _ := n.Properties["type"].(string)
			if t == "health-check" {
				shortName := extractGCPShortName(getNodeName(n))
				if shortName != "" {
					healthCheckByName[shortName] = n
				}
			}
		}
	}

	// 1. ForwardingRule → TargetProxy
	if lbNodes, ok := lookup.ByNodeType[core.NodeTypeLoadBalancer]; ok {
		for _, lbNode := range lbNodes {
			targetName, _ := lbNode.Properties["target_name"].(string)
			if targetName == "" {
				continue
			}
			if proxyNode, exists := targetProxyByName[targetName]; exists {
				edges = append(edges, s.createEdge(lbNode, proxyNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "target_proxy"}, req))
				s.logger.Debug("created ForwardingRule → TargetProxy edge",
					"lb", getNodeName(lbNode), "proxy", targetName)
			}
		}
	}

	// 2. TargetProxy → URLMap
	for name, proxyNode := range targetProxyByName {
		urlMapName, _ := proxyNode.Properties["url_map"].(string)
		if urlMapName == "" {
			continue
		}
		if urlMapNode, exists := urlMapByName[urlMapName]; exists {
			edges = append(edges, s.createEdge(proxyNode, urlMapNode, core.RelationshipRoutesTo,
				map[string]interface{}{"connection_type": "url_map"}, req))
			s.logger.Debug("created TargetProxy → URLMap edge",
				"proxy", name, "url_map", urlMapName)
		}
	}

	// 3. URLMap → BackendService (default service + path-matcher services)
	for name, urlMapNode := range urlMapByName {
		// Default service
		defaultService, _ := urlMapNode.Properties["default_service"].(string)
		if defaultService != "" {
			if bpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, defaultService); bpNode != nil {
				edges = append(edges, s.createEdge(urlMapNode, bpNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "default_backend"}, req))
				s.logger.Debug("created URLMap → BackendService edge",
					"url_map", name, "backend", defaultService)
			}
		}

		// Path-matcher services (path-based routing rules)
		urlMapCLI, hasCLI := cliData.urlMaps[name]
		if hasCLI {
			seenBackends := make(map[string]bool)
			if defaultService != "" {
				seenBackends[defaultService] = true
			}
			for _, pm := range urlMapCLI.PathMatchers {
				pmService := extractGCPResourceNameFromURL(pm.DefaultService)
				if pmService == "" || seenBackends[pmService] {
					continue
				}
				if bpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, pmService); bpNode != nil {
					edges = append(edges, s.createEdge(urlMapNode, bpNode, core.RelationshipRoutesTo,
						map[string]interface{}{
							"connection_type": "path_matcher_backend",
							"path_matcher":    pm.Name,
						}, req))
					seenBackends[pmService] = true
					s.logger.Debug("created URLMap → PathMatcher BackendService edge",
						"url_map", name, "backend", pmService, "matcher", pm.Name)
				}
			}
		}
	}

	// 4. BackendService → HealthCheck
	if bpNodes, ok := lookup.ByNodeType[core.NodeTypeBackendPool]; ok {
		for _, bpNode := range bpNodes {
			bpName := extractGCPShortName(getNodeName(bpNode))
			bs, exists := cliData.backendServices[bpName]
			if !exists {
				continue
			}
			for _, hcURL := range bs.HealthChecks {
				hcName := extractGCPResourceNameFromURL(hcURL)
				if hcNode, exists := healthCheckByName[hcName]; exists {
					edges = append(edges, s.createEdge(bpNode, hcNode, core.RelationshipAssociatedWith,
						map[string]interface{}{"connection_type": "health_check"}, req))
					s.logger.Debug("created BackendService → HealthCheck edge",
						"backend", bpName, "health_check", hcName)
				}
			}
		}
	}

	s.logger.Info("created GCP LB chain edges", "edge_count", len(edges))
	return edges
}
