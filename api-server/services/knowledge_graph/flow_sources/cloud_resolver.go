package flow_sources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"nudgebee/services/traces"
	"strings"
	"time"

	"github.com/lib/pq"
)

// CloudResourceMatch represents a matched AWS resource for a given hostname
type CloudResourceMatch struct {
	// Resource identification
	ResourceID   string `json:"resource_id"`
	ResourceName string `json:"resource_name"`
	ResourceType string `json:"resource_type"`
	ServiceName  string `json:"service_name"`
	ARN          string `json:"arn"`

	// Cloud context
	CloudProvider string `json:"cloud_provider"`
	Region        string `json:"region"`
	AccountID     string `json:"account_id"`
	AccountNumber string `json:"account_number"`

	// Endpoint information
	DNSName   string `json:"dns_name,omitempty"`
	PrivateIP string `json:"private_ip,omitempty"`
	PublicIP  string `json:"public_ip,omitempty"`

	// Match metadata
	MatchType         string  `json:"match_type"`                   // direct_dns, route53, ip_match, cname_match
	MatchConfidence   float64 `json:"match_confidence"`             // 0.0 - 1.0
	ResolvedHostname  string  `json:"resolved_hostname"`            // Original hostname that was resolved
	IntermediateValue string  `json:"intermediate_value,omitempty"` // e.g., CNAME or resolved endpoint

	// Resource metadata
	Status   string                 `json:"status"`
	IsActive bool                   `json:"is_active"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Tags     map[string]interface{} `json:"tags,omitempty"`
}

// CloudResolverResult contains the complete resolution results
type CloudResolverResult struct {
	Hostname         string                    `json:"hostname"`
	ResolvedAt       time.Time                 `json:"resolved_at"`
	DNSInfo          *traces.DNSResolutionInfo `json:"dns_info,omitempty"`
	Matches          []CloudResourceMatch      `json:"matches"`
	Route53Endpoint  string                    `json:"route53_endpoint,omitempty"`
	ResolutionTimeMs int64                     `json:"resolution_time_ms"`
}

// CloudResolver handles hostname to cloud resource resolution
type CloudResolver struct {
	requestContext *security.RequestContext
	tenantID       string
	dbManager      *database.DatabaseManager
	dnsBuilder     *traces.TraceServiceMapBuilder
}

// NewCloudResolver creates a new cloud resolver instance
func NewCloudResolver(requestContext *security.RequestContext, tenantID string) (*CloudResolver, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	return &CloudResolver{
		requestContext: requestContext,
		tenantID:       tenantID,
		dbManager:      dbManager,
		dnsBuilder:     traces.NewTraceServiceMapBuilder(),
	}, nil
}

// ResolveHostname resolves a hostname to AWS resources using multiple strategies
func (r *CloudResolver) ResolveHostname(ctx context.Context, hostname string, awsAccountIDs []string) (*CloudResolverResult, error) {
	startTime := time.Now()

	result := &CloudResolverResult{
		Hostname:   hostname,
		ResolvedAt: startTime,
		Matches:    make([]CloudResourceMatch, 0),
	}

	slog.Info("Starting cloud resolution",
		"hostname", hostname,
		"tenant_id", r.tenantID,
		"aws_accounts", len(awsAccountIDs))

	// Check if hostname is an IP address
	if IsIPAddress(hostname) {
		slog.Info("Input is an IP address, using IP-based resolution",
			"ip", hostname)

		// Use direct IP resolution
		ipMatches, err := r.resolveIPAddressDirectly(ctx, hostname, awsAccountIDs)
		if err != nil {
			slog.Warn("Direct IP resolution failed", "ip", hostname, "error", err)
		} else {
			result.Matches = append(result.Matches, ipMatches...)
		}

		// Skip DNS resolution for raw IPs
		result.DNSInfo = &traces.DNSResolutionInfo{
			Hostname: hostname,
			IPs:      []string{hostname},
		}
	} else {
		// Step 1: Perform DNS resolution to get CNAME and IPs
		result.DNSInfo = r.dnsBuilder.ResolveDNS(hostname)
		if result.DNSInfo != nil {
			slog.Info("DNS resolution completed",
				"hostname", hostname,
				"cname", result.DNSInfo.CNAME,
				"ips", len(result.DNSInfo.IPs),
				"cloud_vendor", result.DNSInfo.CloudVendor,
				"service_type", result.DNSInfo.ServiceType)
		}
	}

	// Step 2: Try direct DNS name lookup in cloud_resourses
	directMatches, err := r.resolveByDirectLookup(ctx, hostname)
	if err != nil {
		slog.Warn("Direct lookup failed", "hostname", hostname, "error", err)
	} else {
		result.Matches = append(result.Matches, directMatches...)
	}

	// Step 3: If we have a CNAME, try to resolve using CNAME
	if result.DNSInfo != nil && result.DNSInfo.CNAME != "" && result.DNSInfo.CNAME != hostname {
		cnameMatches, err := r.resolveByCNAME(ctx, hostname, result.DNSInfo.CNAME)
		if err != nil {
			slog.Warn("CNAME lookup failed", "hostname", hostname, "cname", result.DNSInfo.CNAME, "error", err)
		} else {
			result.Matches = append(result.Matches, cnameMatches...)
		}
	}

	// Step 4: Try Route 53 DNS resolution (if AWS accounts provided)
	if len(awsAccountIDs) > 0 {
		route53Matches, endpoint, err := r.resolveViaRoute53(ctx, hostname, awsAccountIDs)
		if err != nil {
			slog.Warn("Route 53 resolution failed", "hostname", hostname, "error", err)
		} else {
			if endpoint != "" {
				result.Route53Endpoint = endpoint
			}
			result.Matches = append(result.Matches, route53Matches...)
		}
	}

	// Step 5: Try IP-based resolution (if we have IPs from DNS)
	if result.DNSInfo != nil && len(result.DNSInfo.IPs) > 0 && !IsIPAddress(hostname) {
		ipMatches, err := r.resolveByIPAddress(ctx, hostname, result.DNSInfo.IPs)
		if err != nil {
			slog.Warn("IP-based resolution failed", "hostname", hostname, "error", err)
		} else {
			result.Matches = append(result.Matches, ipMatches...)
		}
	}

	// Step 6: Deduplicate matches and sort by confidence
	result.Matches = r.deduplicateAndRankMatches(result.Matches)

	result.ResolutionTimeMs = time.Since(startTime).Milliseconds()

	slog.Info("Cloud resolution completed",
		"hostname", hostname,
		"total_matches", len(result.Matches),
		"duration_ms", result.ResolutionTimeMs)

	return result, nil
}

// resolveByDirectLookup looks up resources by direct DNS name match
func (r *CloudResolver) resolveByDirectLookup(ctx context.Context, hostname string) ([]CloudResourceMatch, error) {
	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, cr.arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.is_active = true
			AND cr.cloud_provider = 'AWS'
			AND (
				-- Match LoadBalancers by DNSName
				(cr.type IN ('application_loadbalancer', 'network_loadbalancer', 'classic_loadbalancer', 'loadbalancer')
					AND cr.meta->>'DNSName' = $2)
				-- Match RDS instances by Endpoint Address
				OR (cr.type IN ('rds_instance', 'db', 'cluster')
					AND cr.service_name = 'AmazonRDS'
					AND cr.meta->'Endpoint'->>'Address' = $2)
				-- Match ElastiCache clusters by various endpoint fields
				OR (cr.type IN ('elasticache_cluster', 'elasticache_replication_group', 'cluster')
					AND cr.service_name = 'AmazonElastiCache'
					AND (
						cr.meta->'ConfigurationEndpoint'->>'Address' = $2
						OR cr.meta->'ReaderEndpoint'->>'Address' = $2
						OR cr.meta->'PrimaryEndpoint'->>'Address' = $2
						OR cr.meta->>'ReaderEndpoint' = $2
						OR cr.meta->>'PrimaryEndpoint' = $2
						OR EXISTS (
							SELECT 1 FROM jsonb_array_elements(cr.meta->'NodeGroups') AS ng
							WHERE ng->'PrimaryEndpoint'->>'Address' = $2
								OR ng->'ReaderEndpoint'->>'Address' = $2
						)
						OR EXISTS (
							SELECT 1 FROM jsonb_array_elements(cr.meta->'CacheNodes') AS cn
							WHERE cn->'Endpoint'->>'Address' = $2
						)
					))
				-- Match by name field (less common but possible)
				OR cr.name = $2
			)
	`

	var resources []traces.CloudResourceRow
	err := r.dbManager.Db.Select(&resources, query, r.tenantID, hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud_resourses: %w", err)
	}

	matches := make([]CloudResourceMatch, 0, len(resources))
	for _, res := range resources {
		match := r.resourceToMatch(res, "direct_dns", 0.95, hostname, "")
		matches = append(matches, match)
	}

	slog.Info("Direct DNS lookup completed",
		"hostname", hostname,
		"matches_found", len(matches))

	return matches, nil
}

// resolveByCNAME resolves using CNAME (e.g., AWS service endpoints)
func (r *CloudResolver) resolveByCNAME(ctx context.Context, originalHostname, cname string) ([]CloudResourceMatch, error) {
	// Use the same query as direct lookup but with CNAME
	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, cr.arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.is_active = true
			AND cr.cloud_provider = 'AWS'
			AND (
				(cr.meta->>'DNSName' = $2)
				OR (cr.meta->'Endpoint'->>'Address' = $2)
				OR (cr.meta->'ConfigurationEndpoint'->>'Address' = $2)
				OR (cr.meta->'ReaderEndpoint'->>'Address' = $2)
				OR (cr.meta->'PrimaryEndpoint'->>'Address' = $2)
				OR (cr.meta->>'ReaderEndpoint' = $2)
				OR (cr.meta->>'PrimaryEndpoint' = $2)
			)
	`

	var resources []traces.CloudResourceRow
	err := r.dbManager.Db.Select(&resources, query, r.tenantID, cname)
	if err != nil {
		return nil, fmt.Errorf("failed to query by CNAME: %w", err)
	}

	matches := make([]CloudResourceMatch, 0, len(resources))
	for _, res := range resources {
		match := r.resourceToMatch(res, "cname_match", 0.90, originalHostname, cname)
		matches = append(matches, match)
	}

	slog.Info("CNAME lookup completed",
		"original_hostname", originalHostname,
		"cname", cname,
		"matches_found", len(matches))

	return matches, nil
}

// resolveViaRoute53 uses AWS Route 53 to resolve hostname
func (r *CloudResolver) resolveViaRoute53(ctx context.Context, hostname string, awsAccountIDs []string) ([]CloudResourceMatch, string, error) {
	var resolvedEndpoint string
	matches := make([]CloudResourceMatch, 0)

	// Try each AWS account until we find a match
	for _, accountID := range awsAccountIDs {
		endpoint, err := ResolveRoute53DNS(r.requestContext, hostname, accountID)
		if err != nil {
			slog.Debug("Route 53 resolution failed for account",
				"hostname", hostname,
				"account_id", accountID,
				"error", err)
			continue
		}

		if endpoint != "" {
			resolvedEndpoint = endpoint
			slog.Info("Route 53 resolution successful",
				"hostname", hostname,
				"endpoint", endpoint,
				"account_id", accountID)

			// Now lookup resources by this endpoint
			endpointMatches, err := r.resolveByDirectLookup(ctx, endpoint)
			if err != nil {
				slog.Warn("Failed to lookup resources after Route 53 resolution",
					"endpoint", endpoint,
					"error", err)
				continue
			}

			// Update match metadata to reflect Route 53 resolution
			for i := range endpointMatches {
				endpointMatches[i].MatchType = "route53"
				endpointMatches[i].MatchConfidence = 0.92
				endpointMatches[i].ResolvedHostname = hostname
				endpointMatches[i].IntermediateValue = endpoint
			}

			matches = append(matches, endpointMatches...)
			break // Found matches, no need to check other accounts
		}
	}

	return matches, resolvedEndpoint, nil
}

// resolveIPAddressDirectly resolves when the input is already an IP address
func (r *CloudResolver) resolveIPAddressDirectly(ctx context.Context, ipAddress string, awsAccountIDs []string) ([]CloudResourceMatch, error) {
	allMatches := make([]CloudResourceMatch, 0)

	// Strategy 1: Direct IP lookup in cloud_resourses
	directMatches, err := r.resolveByIPAddressDirect(ctx, ipAddress, []string{ipAddress})
	if err != nil {
		slog.Warn("Direct IP lookup failed", "ip", ipAddress, "error", err)
	} else {
		allMatches = append(allMatches, directMatches...)
	}

	// Strategy 2: ENI-based resolution (most comprehensive for IPs)
	if len(awsAccountIDs) > 0 {
		for _, accountID := range awsAccountIDs {
			eniResolver := NewENIResolver(r.requestContext, accountID)
			eniMappings, err := eniResolver.ResolveIPToResource(ctx, ipAddress)
			if err != nil {
				slog.Debug("ENI resolution failed",
					"ip", ipAddress,
					"account_id", accountID,
					"error", err)
				continue
			}

			// Convert ENI mappings to CloudResourceMatch
			for _, eniMapping := range eniMappings {
				match := r.eniMappingToCloudResourceMatch(eniMapping, ipAddress, accountID)
				allMatches = append(allMatches, match)
			}

			// If we found matches, we can stop trying other accounts
			if len(eniMappings) > 0 {
				break
			}
		}
	}

	slog.Info("Direct IP resolution completed",
		"ip", ipAddress,
		"total_matches", len(allMatches))

	return allMatches, nil
}

// resolveByIPAddress resolves using IP addresses (for EC2, ENI mapping)
func (r *CloudResolver) resolveByIPAddress(ctx context.Context, hostname string, ips []string) ([]CloudResourceMatch, error) {
	if len(ips) == 0 {
		return nil, nil
	}

	allMatches := make([]CloudResourceMatch, 0)

	// Strategy 1: Direct IP-based lookup in cloud_resourses
	directMatches, err := r.resolveByIPAddressDirect(ctx, hostname, ips)
	if err != nil {
		slog.Warn("Direct IP lookup failed", "hostname", hostname, "error", err)
	} else {
		allMatches = append(allMatches, directMatches...)
	}

	// Strategy 2: ENI-based resolution (more comprehensive)
	eniMatches, err := r.resolveByIPAddressViaENI(ctx, hostname, ips)
	if err != nil {
		slog.Warn("ENI-based IP lookup failed", "hostname", hostname, "error", err)
	} else {
		allMatches = append(allMatches, eniMatches...)
	}

	slog.Info("IP-based lookup completed",
		"hostname", hostname,
		"ips", ips,
		"direct_matches", len(directMatches),
		"eni_matches", len(eniMatches),
		"total_matches", len(allMatches))

	return allMatches, nil
}

// resolveByIPAddressDirect performs direct IP lookup in cloud_resourses table
func (r *CloudResolver) resolveByIPAddressDirect(ctx context.Context, hostname string, ips []string) ([]CloudResourceMatch, error) {
	// Query resources that have matching IPs in their metadata
	// This handles EC2 instances, RDS instances with known IPs, etc.
	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, cr.arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.is_active = true
			AND cr.cloud_provider = 'AWS'
			AND (
				-- Match EC2 instances by private IP
				(cr.type IN ('compute-instance', 'instance')
					AND cr.service_name = 'AmazonEC2'
					AND (
						cr.meta->>'PrivateIpAddress' = ANY($2)
						OR cr.meta->>'PublicIpAddress' = ANY($2)
					))
				-- Match RDS by endpoint IP (if stored)
				OR (cr.type IN ('rds_instance', 'db')
					AND cr.service_name = 'AmazonRDS'
					AND cr.meta->'Endpoint'->>'Address' = ANY($2))
				-- Match network interfaces
				OR (cr.type = 'network-interface'
					AND cr.service_name = 'AmazonVPC'
					AND (
						cr.meta->>'PrivateIpAddress' = ANY($2)
						OR EXISTS (
							SELECT 1 FROM jsonb_array_elements(cr.meta->'PrivateIpAddresses') AS pip
							WHERE pip->>'PrivateIpAddress' = ANY($2)
						)
					))
			)
	`

	var resources []traces.CloudResourceRow
	err := r.dbManager.Db.Select(&resources, query, r.tenantID, pq.Array(ips))
	if err != nil {
		return nil, fmt.Errorf("failed to query by IP address: %w", err)
	}

	matches := make([]CloudResourceMatch, 0, len(resources))
	for _, res := range resources {
		// Find which IP matched
		matchedIP := r.findMatchingIP(res, ips)
		match := r.resourceToMatch(res, "ip_match", 0.85, hostname, matchedIP)
		matches = append(matches, match)
	}

	return matches, nil
}

// resolveByIPAddressViaENI performs IP resolution via ENI lookup
func (r *CloudResolver) resolveByIPAddressViaENI(ctx context.Context, hostname string, ips []string) ([]CloudResourceMatch, error) {
	// Get AWS account IDs from tenant
	awsAccountIDs, err := core.GetAWSAccountsForTenant(r.tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS accounts: %w", err)
	}

	if len(awsAccountIDs) == 0 {
		return nil, nil
	}

	allMatches := make([]CloudResourceMatch, 0)

	// Try ENI resolution for each IP with each AWS account
	for _, ip := range ips {
		for _, accountID := range awsAccountIDs {
			eniResolver := NewENIResolver(r.requestContext, accountID)

			eniMappings, err := eniResolver.ResolveIPToResource(ctx, ip)
			if err != nil {
				slog.Debug("ENI resolution failed",
					"ip", ip,
					"account_id", accountID,
					"error", err)
				continue
			}

			// Convert ENI mappings to CloudResourceMatch
			for _, eniMapping := range eniMappings {
				match := r.eniMappingToCloudResourceMatch(eniMapping, hostname, accountID)
				allMatches = append(allMatches, match)
			}
		}
	}

	return allMatches, nil
}

// eniMappingToCloudResourceMatch converts ENI mapping to CloudResourceMatch
func (r *CloudResolver) eniMappingToCloudResourceMatch(eniMapping *ENIResourceMapping, hostname string, accountID string) CloudResourceMatch {
	// Determine resource type for knowledge graph
	nodeType := determineNodeTypeFromResourceType(eniMapping.ResourceType)

	match := CloudResourceMatch{
		ResourceID:        eniMapping.ResourceID,
		ResourceName:      eniMapping.ResourceName,
		ResourceType:      nodeType,
		ServiceName:       mapResourceTypeToServiceName(eniMapping.ResourceType),
		CloudProvider:     "AWS",
		AccountID:         accountID,
		MatchType:         "eni_" + eniMapping.MatchType, // e.g., "eni_tag", "eni_ip"
		MatchConfidence:   eniMapping.Confidence,
		ResolvedHostname:  hostname,
		IntermediateValue: fmt.Sprintf("ENI:%s", eniMapping.ENI.ENIId),
		Status:            "active",
		IsActive:          true,
		Metadata: map[string]interface{}{
			"eni_id":          eniMapping.ENI.ENIId,
			"subnet_id":       eniMapping.ENI.SubnetID,
			"vpc_id":          eniMapping.ENI.VPCID,
			"matched_ips":     eniMapping.MatchedIPs,
			"resource_type":   eniMapping.ResourceType,
			"additional_info": eniMapping.AdditionalInfo,
		},
	}

	// Add endpoint if available
	if eniMapping.Endpoint != "" {
		match.DNSName = eniMapping.Endpoint
	}

	// Add cluster ID if available
	if eniMapping.ClusterID != "" {
		match.Metadata["cluster_id"] = eniMapping.ClusterID
	}

	return match
}

// determineNodeTypeFromResourceType maps ENI resource types to node types
func determineNodeTypeFromResourceType(resourceType string) string {
	switch resourceType {
	case "rds":
		return "rds_instance"
	case "elasticache":
		return "elasticache_cluster"
	case "ec2":
		return "compute-instance"
	case "lambda":
		return "function"
	case "eks":
		return "cluster"
	default:
		return resourceType
	}
}

// mapResourceTypeToServiceName maps resource types to AWS service names
func mapResourceTypeToServiceName(resourceType string) string {
	switch resourceType {
	case "rds":
		return "AmazonRDS"
	case "elasticache":
		return "AmazonElastiCache"
	case "ec2":
		return "AmazonEC2"
	case "lambda":
		return "AWSLambda"
	case "eks":
		return "AmazonEKS"
	default:
		return ""
	}
}

// findMatchingIP finds which IP from the list matched the resource
func (r *CloudResolver) findMatchingIP(resource traces.CloudResourceRow, ips []string) string {
	// Parse metadata to find matching IP
	if len(resource.Meta) > 0 {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil {
			// Check PrivateIpAddress
			if privateIP, ok := metaMap["PrivateIpAddress"].(string); ok {
				for _, ip := range ips {
					if ip == privateIP {
						return ip
					}
				}
			}

			// Check PublicIpAddress
			if publicIP, ok := metaMap["PublicIpAddress"].(string); ok {
				for _, ip := range ips {
					if ip == publicIP {
						return ip
					}
				}
			}
		}
	}

	return strings.Join(ips, ",")
}

// resourceToMatch converts CloudResourceRow to CloudResourceMatch
func (r *CloudResolver) resourceToMatch(resource traces.CloudResourceRow, matchType string, confidence float64, resolvedHostname, intermediateValue string) CloudResourceMatch {
	match := CloudResourceMatch{
		ResourceID:        resource.ResourceID,
		ResourceName:      resource.Name,
		ResourceType:      resource.Type,
		ServiceName:       resource.ServiceName,
		ARN:               resource.ARN,
		CloudProvider:     resource.CloudProvider,
		Region:            resource.Region,
		AccountID:         resource.Account,
		AccountNumber:     resource.AccountNumber,
		Status:            resource.Status,
		IsActive:          resource.IsActive,
		MatchType:         matchType,
		MatchConfidence:   confidence,
		ResolvedHostname:  resolvedHostname,
		IntermediateValue: intermediateValue,
	}

	// Parse metadata
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil {
			match.Metadata = metaMap

			// Extract common fields
			if dnsName, ok := metaMap["DNSName"].(string); ok {
				match.DNSName = dnsName
			}
			if privateIP, ok := metaMap["PrivateIpAddress"].(string); ok {
				match.PrivateIP = privateIP
			}
			if publicIP, ok := metaMap["PublicIpAddress"].(string); ok {
				match.PublicIP = publicIP
			}
			if endpoint, ok := metaMap["Endpoint"].(map[string]interface{}); ok {
				if address, ok := endpoint["Address"].(string); ok && match.DNSName == "" {
					match.DNSName = address
				}
			}
		}
	}

	// Parse tags
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			match.Tags = tagsMap
		}
	}

	return match
}

// deduplicateAndRankMatches removes duplicates and ranks by confidence
func (r *CloudResolver) deduplicateAndRankMatches(matches []CloudResourceMatch) []CloudResourceMatch {
	// Deduplicate by resource ID
	seen := make(map[string]*CloudResourceMatch)

	for i := range matches {
		match := &matches[i]
		key := match.ResourceID

		if existing, found := seen[key]; found {
			// Keep the match with higher confidence
			if match.MatchConfidence > existing.MatchConfidence {
				seen[key] = match
			}
		} else {
			seen[key] = match
		}
	}

	// Convert back to slice and sort by confidence (highest first)
	result := make([]CloudResourceMatch, 0, len(seen))
	for _, match := range seen {
		result = append(result, *match)
	}

	// Sort by confidence descending
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].MatchConfidence > result[i].MatchConfidence {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// ResolveBulkHostnames resolves multiple hostnames in parallel
func (r *CloudResolver) ResolveBulkHostnames(ctx context.Context, hostnames []string, awsAccountIDs []string) (map[string]*CloudResolverResult, error) {
	results := make(map[string]*CloudResolverResult)

	for _, hostname := range hostnames {
		// Skip empty or invalid hostnames
		if hostname == "" || hostname == "localhost" {
			continue
		}
		result, err := r.ResolveHostname(ctx, hostname, awsAccountIDs)
		if err != nil {
			slog.Warn("Failed to resolve hostname",
				"hostname", hostname,
				"error", err)
			continue
		}

		results[hostname] = result
	}

	return results, nil
}

// GetBestMatch returns the highest confidence match, or nil if no matches
func (result *CloudResolverResult) GetBestMatch() *CloudResourceMatch {
	if len(result.Matches) == 0 {
		return nil
	}
	return &result.Matches[0]
}

// HasMatch returns true if any matches were found
func (result *CloudResolverResult) HasMatch() bool {
	return len(result.Matches) > 0
}

// GetMatchesByType returns matches filtered by match type
func (result *CloudResolverResult) GetMatchesByType(matchType string) []CloudResourceMatch {
	filtered := make([]CloudResourceMatch, 0)
	for _, match := range result.Matches {
		if match.MatchType == matchType {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

// IsIPAddress checks if a string is an IP address
func IsIPAddress(hostname string) bool {
	return net.ParseIP(hostname) != nil
}
