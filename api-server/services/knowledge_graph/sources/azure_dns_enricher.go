package sources

import (
	"log/slog"
	"strings"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

func init() {
	RegisterCrossSourceEnricherFactory("azure_private_dns", func(logger *slog.Logger) core.CrossSourceEnricherInterface {
		return NewAzureDNSEnricher(logger)
	}, "Links Azure private DNS zones to the VNets they are linked to, across subscriptions, via a live Resource Graph query")
}

// AzureDNSEnricher connects private DNS zone nodes to the VNets they are linked to.
// The link data is billing-only (absent from collected meta) and a zone is often
// in a different subscription than the VNet it serves (hub-spoke), so resolution
// runs here in Phase 2.1 against the unified, cross-account node set rather than
// per-account in the Azure source.
type AzureDNSEnricher struct {
	logger *slog.Logger
}

func NewAzureDNSEnricher(logger *slog.Logger) *AzureDNSEnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &AzureDNSEnricher{logger: logger}
}

func (e *AzureDNSEnricher) GetName() string {
	return "azure_private_dns"
}

// EnrichCrossSources fetches private-DNS-zone→VNet links for every Azure account
// (unioned) and emits an ASSOCIATED_WITH edge for each link whose zone and VNet
// both exist in the graph — including cross-subscription links.
func (e *AzureDNSEnricher) EnrichCrossSources(
	reqCtx *security.RequestContext,
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {
	if reqCtx == nil {
		return allNodes, allEdges, nil
	}

	// Index nodes by lowercased arn and resource_id, and collect the distinct
	// Azure accounts whose service principals can run the Resource Graph query.
	index := make(map[string]*core.DbNode, len(allNodes)*2)
	azureAccounts := make(map[string]struct{})
	for _, n := range allNodes {
		if arn, _ := n.Properties["arn"].(string); arn != "" {
			index[strings.ToLower(arn)] = n
		}
		if rid, _ := n.Properties["resource_id"].(string); rid != "" {
			if _, exists := index[strings.ToLower(rid)]; !exists {
				index[strings.ToLower(rid)] = n
			}
		}
		if n.Source == "azure" && n.CloudAccountID != "" {
			azureAccounts[n.CloudAccountID] = struct{}{}
		}
	}
	if len(azureAccounts) == 0 {
		return allNodes, allEdges, nil
	}

	// Union the links across accounts (different service principals may see
	// different subscriptions).
	var links []dnsVNetLink
	for accountID := range azureAccounts {
		links = append(links, fetchPrivateDNSVNetLinks(reqCtx, accountID, e.logger)...)
	}

	newEdges := buildPrivateDNSVNetEdges(links, index, tenantID)

	e.logger.Info("azure_private_dns enricher: completed",
		"azure_accounts", len(azureAccounts), "links_fetched", len(links), "edges_created", len(newEdges))

	allEdges = append(allEdges, newEdges...)
	return allNodes, allEdges, nil
}

// buildPrivateDNSVNetEdges resolves zone→vnet link pairs against the node index and
// returns DNSZone → VNet (ASSOCIATED_WITH) edges, deduped by zone+vnet pair.
func buildPrivateDNSVNetEdges(links []dnsVNetLink, index map[string]*core.DbNode, tenantID string) []*core.DbEdge {
	seen := make(map[string]struct{})
	edges := make([]*core.DbEdge, 0, len(links))
	for _, l := range links {
		if l.ZoneID == "" || l.VnetID == "" {
			continue
		}
		zone := index[strings.ToLower(l.ZoneID)]
		vnet := index[strings.ToLower(l.VnetID)]
		if zone == nil || vnet == nil || zone.ID == vnet.ID {
			continue
		}
		key := zone.ID + "|" + vnet.ID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		edges = append(edges, core.NewEdge(
			zone.ID,
			vnet.ID,
			core.RelationshipAssociatedWith,
			map[string]interface{}{"connection_type": "private_dns_vnet_link"},
			tenantID,
			zone.CloudAccountID,
			"azure_private_dns",
		))
	}
	return edges
}
