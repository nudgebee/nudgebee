package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// fetchAllGCPCLIData fetches all GCP metadata via gcloud CLI in bulk
func (s *GCPSource) fetchAllGCPCLIData(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (data *gcpCLIData) {
	data = &gcpCLIData{
		computeInstances: make(map[string]*GCPComputeInstance),
		sqlInstances:     make(map[string]*GCPCloudSQLInstance),
		gkeClusters:      make(map[string]*GCPGKECluster),
		vpcNetworks:      make(map[string]*GCPVPCNetwork),
		subnets:          make(map[string]*GCPSubnetData),
		firewallRules:    make(map[string]*GCPFirewallRule),
		// Load Balancer components
		forwardingRules: make(map[string]*GCPForwardingRule),
		backendServices: make(map[string]*GCPBackendService),
		serverlessNEGs:  make(map[string]*GCPServerlessNEG),
		healthChecks:    make(map[string]*GCPHealthCheck),
		urlMaps:         make(map[string]*GCPURLMap),
		targetProxies:   make(map[string]*GCPTargetProxy),
		dnsZones:        make(map[string]*GCPDNSManagedZone),
		cdnBackends:     make(map[string]*GCPCDNBackendService),
	}

	// Guard against panics from CLI calls (e.g., missing cloud-collector config in test environments)
	defer func() {
		if r := recover(); r != nil {
			s.logger.Warn("recovered from panic during GCP CLI data fetch", "error", fmt.Sprintf("%v", r))
		}
	}()

	if req.CloudAccountID == "" {
		s.logger.Warn("no cloud account ID, skipping GCP CLI enrichment")
		return data
	}

	accountID := req.CloudAccountID

	// Fetch VPC networks
	networks, err := s.fetchVPCNetworksFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP VPC networks via CLI", "error", err)
	} else {
		for i := range networks {
			data.vpcNetworks[networks[i].Name] = &networks[i]
		}
		s.logger.Info("fetched GCP VPC networks via CLI", "count", len(networks))
	}

	// Fetch subnets
	subnets, err := s.fetchSubnetsFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP subnets via CLI", "error", err)
	} else {
		for i := range subnets {
			data.subnets[subnets[i].SelfLink] = &subnets[i]
			// Also index by name for easier lookup
			data.subnets[subnets[i].Name] = &subnets[i]
		}
		s.logger.Info("fetched GCP subnets via CLI", "count", len(subnets))
	}

	// Fetch firewall rules
	firewallRules, err := s.fetchFirewallRulesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP firewall rules via CLI", "error", err)
	} else {
		for i := range firewallRules {
			data.firewallRules[firewallRules[i].Name] = &firewallRules[i]
		}
		s.logger.Info("fetched GCP firewall rules via CLI", "count", len(firewallRules))
	}

	// Fetch compute instances
	instances, err := s.fetchComputeInstancesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP compute instances via CLI", "error", err)
	} else {
		for i := range instances {
			data.computeInstances[instances[i].Name] = &instances[i]
		}
		s.logger.Info("fetched GCP compute instances via CLI", "count", len(instances))
	}

	// Fetch Cloud SQL instances
	sqlInstances, err := s.fetchCloudSQLInstancesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP Cloud SQL instances via CLI", "error", err)
	} else {
		for i := range sqlInstances {
			data.sqlInstances[sqlInstances[i].Name] = &sqlInstances[i]
		}
		s.logger.Info("fetched GCP Cloud SQL instances via CLI", "count", len(sqlInstances))
	}

	// Fetch GKE clusters
	clusters, err := s.fetchGKEClustersFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP GKE clusters via CLI", "error", err)
	} else {
		for i := range clusters {
			data.gkeClusters[clusters[i].Name] = &clusters[i]
		}
		s.logger.Info("fetched GCP GKE clusters via CLI", "count", len(clusters))
	}

	// Fetch Load Balancer components
	// Forwarding rules (load balancer frontends)
	forwardingRules, err := s.fetchForwardingRulesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP forwarding rules via CLI", "error", err)
	} else {
		for i := range forwardingRules {
			data.forwardingRules[forwardingRules[i].Name] = &forwardingRules[i]
		}
		s.logger.Info("fetched GCP forwarding rules via CLI", "count", len(forwardingRules))
	}

	// Backend services
	backendServices, err := s.fetchBackendServicesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP backend services via CLI", "error", err)
	} else {
		for i := range backendServices {
			data.backendServices[backendServices[i].Name] = &backendServices[i]
		}
		s.logger.Info("fetched GCP backend services via CLI", "count", len(backendServices))
	}

	// Serverless NEGs (resolve LB backend → Cloud Run / App Engine service)
	serverlessNEGs, err := s.fetchServerlessNEGsFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP serverless NEGs via CLI", "error", err)
	} else {
		for i := range serverlessNEGs {
			data.serverlessNEGs[serverlessNEGs[i].Name] = &serverlessNEGs[i]
		}
		s.logger.Info("fetched GCP serverless NEGs via CLI", "count", len(serverlessNEGs))
	}

	// Health checks
	healthChecks, err := s.fetchHealthChecksFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP health checks via CLI", "error", err)
	} else {
		for i := range healthChecks {
			data.healthChecks[healthChecks[i].Name] = &healthChecks[i]
		}
		s.logger.Info("fetched GCP health checks via CLI", "count", len(healthChecks))
	}

	// URL maps (for HTTP(S) load balancers)
	urlMaps, err := s.fetchURLMapsFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP URL maps via CLI", "error", err)
	} else {
		for i := range urlMaps {
			data.urlMaps[urlMaps[i].Name] = &urlMaps[i]
		}
		s.logger.Info("fetched GCP URL maps via CLI", "count", len(urlMaps))
	}

	// Target proxies (HTTP and HTTPS)
	targetProxies, err := s.fetchTargetProxiesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP target proxies via CLI", "error", err)
	} else {
		for i := range targetProxies {
			data.targetProxies[targetProxies[i].Name] = &targetProxies[i]
		}
		s.logger.Info("fetched GCP target proxies via CLI", "count", len(targetProxies))
	}

	// Cloud DNS managed zones (and their record sets)
	dnsZones, err := s.fetchDNSZonesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP DNS zones via CLI", "error", err)
	} else {
		for i := range dnsZones {
			data.dnsZones[dnsZones[i].Name] = &dnsZones[i]
		}
		s.logger.Info("fetched GCP DNS zones via CLI", "count", len(dnsZones))
	}

	// Cloud CDN-enabled backend services
	cdnBackends, err := s.fetchCDNBackendsFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP Cloud CDN backends via CLI", "error", err)
	} else {
		for i := range cdnBackends {
			data.cdnBackends[cdnBackends[i].Name] = &cdnBackends[i]
		}
		s.logger.Info("fetched GCP Cloud CDN backends via CLI", "count", len(cdnBackends))
	}

	return data
}

// parseGCloudCLIResponse extracts the JSON string from a cloud CLI response
func parseGCloudCLIResponse(resp map[string]any) (string, error) {
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		return dataStr, nil
	}
	if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		return outputStr, nil
	}
	if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		return resultStr, nil
	}

	respBytes, _ := json.Marshal(resp)
	return "", fmt.Errorf("invalid response format from gcloud CLI: expected 'data', 'output', or 'result' field, got: %s", sources.TruncateString(string(respBytes), 200))
}

// enrichNodesFromCLIData enriches nodes with VPC/subnet info and additional properties from CLI data
func (s *GCPSource) enrichNodesFromCLIData(nodes []*core.DbNode, cliData *gcpCLIData) {
	for _, node := range nodes {
		shortName := extractGCPShortName(getNodeName(node))

		switch node.NodeType {
		case core.NodeTypeComputeInstance:
			s.enrichComputeInstanceFromCLI(node, shortName, cliData)

		case core.NodeTypeDatabase:
			serviceName, _ := node.Properties["service_name"].(string)
			if serviceName == "Cloud SQL" {
				s.enrichCloudSQLFromCLI(node, shortName, cliData)
			}

		case core.NodeTypeManagedCluster:
			s.enrichGKEClusterFromCLI(node, shortName, cliData)

		case core.NodeTypeLoadBalancer:
			s.enrichLoadBalancerFromCLI(node, shortName, cliData)

		case core.NodeTypeSecurityGroup:
			s.enrichFirewallRuleFromCLI(node, shortName, cliData)
		}
	}
}
