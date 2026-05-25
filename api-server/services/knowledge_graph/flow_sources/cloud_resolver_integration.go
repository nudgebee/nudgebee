package flow_sources

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/traces"
)

// ResolveHostnamesToCloudResources resolves hostnames to cloud resources for knowledge graph integration
// This function integrates with the existing knowledge graph service
func ResolveHostnamesToCloudResources(
	requestContext *security.RequestContext,
	tenantID string,
	cloudAccountIDs []string,
	hostnames []string,
) (map[string][]CloudResourceMatch, error) {

	// Get AWS account IDs for Route 53 resolution
	awsAccountIDs, err := getAWSAccountIDs(requestContext, tenantID)
	if err != nil {
		slog.Warn("Failed to get AWS account IDs", "error", err)
		awsAccountIDs = []string{}
	}

	// Filter to specific accounts if provided
	if len(cloudAccountIDs) > 0 {
		// Filter awsAccountIDs to only include those in cloudAccountIDs
		filtered := make([]string, 0, len(awsAccountIDs))
		for _, awsID := range awsAccountIDs {
			for _, cloudID := range cloudAccountIDs {
				if awsID == cloudID {
					filtered = append(filtered, awsID)
					break
				}
			}
		}
		awsAccountIDs = filtered
	}

	// Create cloud resolver
	resolver, err := NewCloudResolver(requestContext, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud resolver: %w", err)
	}

	// Resolve all hostnames
	ctx := context.Background()
	results, err := resolver.ResolveBulkHostnames(ctx, hostnames, awsAccountIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve hostnames: %w", err)
	}

	// Convert results to matches map
	hostnameToMatches := make(map[string][]CloudResourceMatch)
	for hostname, result := range results {
		if result.HasMatch() {
			hostnameToMatches[hostname] = result.Matches
		}
	}

	slog.Info("Hostname resolution completed",
		"total_hostnames", len(hostnames),
		"resolved_count", len(hostnameToMatches),
		"aws_accounts", len(awsAccountIDs))

	return hostnameToMatches, nil
}

// EnrichExternalServicesWithCloudResources enriches external services with cloud resource information
// This can be used in the knowledge graph building process
func EnrichExternalServicesWithCloudResources(
	requestContext *security.RequestContext,
	tenantID string,
	cloudAccountIDs []string,
	externalServices map[string]*traces.ExternalServiceInfo,
) (map[string][]CloudResourceMatch, error) {

	// Extract hostnames from external services
	hostnames := make([]string, 0, len(externalServices))
	for hostname := range externalServices {
		hostnames = append(hostnames, hostname)
	}

	if len(hostnames) == 0 {
		slog.Info("No hostnames to resolve")
		return make(map[string][]CloudResourceMatch), nil
	}

	// Resolve hostnames to cloud resources
	return ResolveHostnamesToCloudResources(requestContext, tenantID, cloudAccountIDs, hostnames)
}

// CreateCloudResourceEdges creates edges between services and resolved cloud resources
// This integrates with the knowledge graph edge creation
func CreateCloudResourceEdges(
	hostnameMatches map[string][]CloudResourceMatch,
	externalServiceNodes map[string]*traces.KnowledgeGraphNode,
	tenantID string,
	cloudAccountID string,
) []*traces.KnowledgeGraphEdge {

	edges := make([]*traces.KnowledgeGraphEdge, 0)

	for hostname, matches := range hostnameMatches {
		// Get the external service node for this hostname
		serviceNode, exists := externalServiceNodes[hostname]
		if !exists {
			continue
		}

		// For each match, create an edge
		for _, match := range matches {
			// Create a cloud resource node ID (using the unique key pattern)
			cloudResourceNodeID := fmt.Sprintf("%s:%s:%s", match.ResourceType, match.ResourceName, match.Region)

			edge := &traces.KnowledgeGraphEdge{
				SourceNodeID:      serviceNode.ID,
				DestinationNodeID: cloudResourceNodeID,
				RelationshipType:  "RESOLVES_TO",
				CloudAccountID:    cloudAccountID,
				TenantID:          tenantID,
				Properties: map[string]interface{}{
					"match_type":         match.MatchType,
					"match_confidence":   match.MatchConfidence,
					"resolved_dns":       match.DNSName,
					"resource_arn":       match.ARN,
					"resource_type":      match.ResourceType,
					"service_name":       match.ServiceName,
					"intermediate_value": match.IntermediateValue,
				},
			}

			edges = append(edges, edge)

			slog.Debug("Created cloud resource edge",
				"hostname", hostname,
				"resource_name", match.ResourceName,
				"resource_type", match.ResourceType,
				"match_type", match.MatchType,
				"confidence", match.MatchConfidence)
		}
	}

	slog.Info("Created cloud resource edges",
		"total_edges", len(edges),
		"hostnames_resolved", len(hostnameMatches))

	return edges
}

// getAWSAccountIDs retrieves AWS account IDs for the tenant
func getAWSAccountIDs(requestContext *security.RequestContext, tenantID string) ([]string, error) {
	db, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		SELECT id
		FROM cloud_accounts
		WHERE tenant = $1
			AND cloud_provider = 'AWS'
			AND status = 'active'
		ORDER BY created_at DESC
	`

	var accountIDs []string
	err = db.Db.Select(&accountIDs, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud accounts: %w", err)
	}

	return accountIDs, nil
}

// ResolverStats provides statistics about resolution results
type ResolverStats struct {
	TotalHostnames      int            `json:"total_hostnames"`
	ResolvedHostnames   int            `json:"resolved_hostnames"`
	UnresolvedHostnames int            `json:"unresolved_hostnames"`
	TotalMatches        int            `json:"total_matches"`
	MatchesByType       map[string]int `json:"matches_by_type"`
	MatchesByResource   map[string]int `json:"matches_by_resource"`
	AverageConfidence   float64        `json:"average_confidence"`
}

// CalculateResolverStats calculates statistics from resolver results
func CalculateResolverStats(results map[string]*CloudResolverResult) ResolverStats {
	stats := ResolverStats{
		TotalHostnames:    len(results),
		MatchesByType:     make(map[string]int),
		MatchesByResource: make(map[string]int),
	}

	totalConfidence := 0.0
	matchCount := 0

	for _, result := range results {
		if result.HasMatch() {
			stats.ResolvedHostnames++

			for _, match := range result.Matches {
				stats.TotalMatches++
				stats.MatchesByType[match.MatchType]++
				stats.MatchesByResource[match.ResourceType]++
				totalConfidence += match.MatchConfidence
				matchCount++
			}
		} else {
			stats.UnresolvedHostnames++
		}
	}

	if matchCount > 0 {
		stats.AverageConfidence = totalConfidence / float64(matchCount)
	}

	return stats
}

// Example usage in knowledge graph service:
//
// // In buildExternalServiceNodes function:
// func (e *TraceToKnowledgeGraphExtractor) buildExternalServiceNodes(...) {
//     ...
//     // After building external service nodes, resolve them to cloud resources
//     hostnameMatches, err := EnrichExternalServicesWithCloudResources(
//         requestContext,
//         tenantID,
//         cloudAccountID,
//         externalServices,
//     )
//     if err != nil {
//         slog.Warn("Failed to enrich external services with cloud resources", "error", err)
//     } else {
//         // Create edges linking external services to cloud resources
//         cloudEdges := CreateCloudResourceEdges(hostnameMatches, externalServiceNodes, tenantID, cloudAccountID)
//         edges = append(edges, cloudEdges...)
//     }
//     ...
// }
