package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// createCDNEdges links each Cloud CDN node to the BackendPool it fronts.
// Cloud CDN isn't a standalone resource — it's enabled on a backend service —
// so the CDN node carries that backend service name (origin_backend_service_name,
// falling back to its own name). The matching BackendPool node is created from the
// same backend service, so a name match wires the public CDN entry point to its
// origin pool. Without this rule CDN nodes are always orphaned.
func (s *GCPSource) createCDNEdges(lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	cdnNodes, ok := lookup.ByNodeType[core.NodeTypeCDN]
	if !ok {
		return edges
	}

	backendPoolByName := make(map[string]*core.DbNode)
	if bpNodes, ok := lookup.ByNodeType[core.NodeTypeBackendPool]; ok {
		for _, n := range bpNodes {
			if shortName := extractGCPShortName(getNodeName(n)); shortName != "" {
				backendPoolByName[shortName] = n
			}
		}
	}

	for _, cdnNode := range cdnNodes {
		backendName, _ := cdnNode.Properties["origin_backend_service_name"].(string)
		if backendName == "" {
			backendName = getNodeName(cdnNode)
		}
		// Normalise to a short name: origin_backend_service_name is usually already
		// short, but guard against a full self-link/URL so the lookup doesn't miss.
		backendName = extractGCPShortName(backendName)
		if backendName == "" {
			continue
		}
		if bpNode, exists := backendPoolByName[backendName]; exists {
			edges = append(edges, s.createEdge(cdnNode, bpNode, core.RelationshipRoutesTo,
				map[string]interface{}{"connection_type": "cdn_backend"}, req))
		}
	}

	s.logger.Info("created GCP CDN → BackendPool edges", "edge_count", len(edges))
	return edges
}

func (s *GCPSource) fetchCDNBackendsFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPCDNBackendService, error) {
	allBackends := []GCPCDNBackendService{}

	// Global backend services (Cloud CDN is only available on global external HTTP(S) LBs).
	cmd := "gcloud compute backend-services list --global --format=json"
	s.logger.Info("fetching GCP global backend services for CDN detection via CLI", "account_id", accountID)

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

	var backends []GCPCDNBackendService
	if err := json.Unmarshal([]byte(output), &backends); err != nil {
		return nil, fmt.Errorf("failed to parse backend services response: %w", err)
	}

	for i := range backends {
		if backends[i].EnableCDN {
			allBackends = append(allBackends, backends[i])
		}
	}

	return allBackends, nil
}

// ensureGCPCDNNodes creates CDN nodes from Cloud CDN-enabled backend services (CLI data).
// Cloud CDN is a flag on a backend service, not a separate resource — we create a synthetic CDN
// node per CDN-enabled backend so the cross-account rule "gcp_cdn_to_loadbalancer" can match
// CDN.origin_backend_service_name ↔ LoadBalancer.backend_service.
func (s *GCPSource) ensureGCPCDNNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, cdn := range cliData.cdnBackends {
		found := false
		if cdnNodes, ok := lookup.ByNodeType[core.NodeTypeCDN]; ok {
			for _, existing := range cdnNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					found = true
					break
				}
			}
		}
		if found {
			continue
		}

		properties := map[string]interface{}{
			"name":                        name,
			"type":                        "cloud-cdn",
			"subtype":                     "GCPCDN",
			"service_name":                "Cloud CDN",
			"cloud_provider":              "GCP",
			"inferred":                    false,
			"enabled":                     true,
			"is_public_entry":             true,
			"origin_backend_service_name": name,
			"self_link":                   cdn.SelfLink,
		}
		if cdn.CdnPolicy != nil {
			if cdn.CdnPolicy.CacheMode != "" {
				properties["cache_mode"] = cdn.CdnPolicy.CacheMode
			}
			if cdn.CdnPolicy.DefaultTtl > 0 {
				properties["default_ttl"] = cdn.CdnPolicy.DefaultTtl
			}
		}

		uniqueKey := fmt.Sprintf("gcp:%s:global:%s:%s",
			req.CloudAccountID, core.NodeTypeCDN, name)
		node := core.NewNode(core.NodeTypeCDN, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
		nodes = append(nodes, node)
		s.logger.Debug("created GCP CDN node from CLI", "name", name)
	}
	return nodes
}
