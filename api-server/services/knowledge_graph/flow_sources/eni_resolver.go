package flow_sources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	InitENICaches()
}

// InitENICaches initializes all ENI-related caches
func InitENICaches() {
	// Cache ENI lookups for 10 minutes (ENIs don't change frequently)
	common.CacheCreateNamespace("eni_ip_mapping",
		common.CacheNamespaceWithExpiration(10*time.Minute),
		common.CacheNamespaceWithMaxEntries(5000),
	)

	// Cache RDS endpoints for 5 minutes (moderate refresh rate)
	common.CacheCreateNamespace("rds_endpoints",
		common.CacheNamespaceWithExpiration(5*time.Minute),
		common.CacheNamespaceWithMaxEntries(100),
	)

	// Cache ElastiCache endpoints for 5 minutes
	common.CacheCreateNamespace("elasticache_endpoints",
		common.CacheNamespaceWithExpiration(5*time.Minute),
		common.CacheNamespaceWithMaxEntries(100),
	)

	// Cache DNS resolutions for endpoint IPs (15 minutes)
	common.CacheCreateNamespace("endpoint_dns",
		common.CacheNamespaceWithExpiration(15*time.Minute),
		common.CacheNamespaceWithMaxEntries(1000),
	)

	slog.Info("ENI resolver caches initialized",
		"eni_ttl", "10m",
		"endpoints_ttl", "5m",
		"dns_ttl", "15m")
}

// ENIInfo represents information about an Elastic Network Interface
type ENIInfo struct {
	ENIId            string                 `json:"eni_id"`
	PrivateIPs       []string               `json:"private_ips"`
	PublicIP         string                 `json:"public_ip,omitempty"`
	SubnetID         string                 `json:"subnet_id"`
	VPCID            string                 `json:"vpc_id"`
	SecurityGroups   []string               `json:"security_groups"`
	AvailabilityZone string                 `json:"availability_zone"`
	InterfaceType    string                 `json:"interface_type"`
	Description      string                 `json:"description"`
	RequesterId      string                 `json:"requester_id"`
	Attachment       map[string]interface{} `json:"attachment,omitempty"`
	Tags             map[string]string      `json:"tags,omitempty"`
	RDSTags          map[string]string      `json:"rds_tags,omitempty"`
	Status           string                 `json:"status"`
}

// ENIResourceMapping represents the mapping from ENI to AWS resource
type ENIResourceMapping struct {
	ENI            *ENIInfo               `json:"eni"`
	MatchType      string                 `json:"match_type"`    // "tag", "ip", "attachment", "description"
	ResourceType   string                 `json:"resource_type"` // "rds", "elasticache", "ec2", "lambda", etc.
	ResourceID     string                 `json:"resource_id"`
	ResourceName   string                 `json:"resource_name,omitempty"`
	ClusterID      string                 `json:"cluster_id,omitempty"`
	Endpoint       string                 `json:"endpoint,omitempty"`
	MatchedIPs     []string               `json:"matched_ips,omitempty"`
	Confidence     float64                `json:"confidence"` // 0.0 - 1.0
	AdditionalInfo map[string]interface{} `json:"additional_info,omitempty"`
}

// RDSEndpointInfo represents an RDS endpoint with its IPs
type RDSEndpointInfo struct {
	Type      string   `json:"type"` // "instance", "cluster-writer", "cluster-reader", "proxy"
	ID        string   `json:"id"`
	ClusterID string   `json:"cluster_id,omitempty"`
	Endpoint  string   `json:"endpoint"`
	IPs       []string `json:"ips"`
}

// ENIResolver handles ENI-based resource resolution
type ENIResolver struct {
	requestContext *security.RequestContext
	awsAccountID   string
}

// NewENIResolver creates a new ENI resolver
func NewENIResolver(requestContext *security.RequestContext, awsAccountID string) *ENIResolver {
	return &ENIResolver{
		requestContext: requestContext,
		awsAccountID:   awsAccountID,
	}
}

// ResolveIPToResource resolves an IP address to an AWS resource via ENI
func (r *ENIResolver) ResolveIPToResource(ctx context.Context, ipAddress string) ([]*ENIResourceMapping, error) {
	// Validate IP address
	if net.ParseIP(ipAddress) == nil {
		return nil, fmt.Errorf("invalid IP address: %s", ipAddress)
	}

	slog.Info("Resolving IP to AWS resource via ENI",
		"ip", ipAddress,
		"aws_account", r.awsAccountID)

	// Step 1: Find ENI with this IP
	enis, err := r.findENIByIP(ctx, ipAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to find ENI: %w", err)
	}

	if len(enis) == 0 {
		slog.Info("No ENI found for IP", "ip", ipAddress)
		return nil, nil
	}

	slog.Info("Found ENIs for IP",
		"ip", ipAddress,
		"eni_count", len(enis))

	// Step 2: Map each ENI to AWS resources
	mappings := make([]*ENIResourceMapping, 0)

	for _, eni := range enis {
		// Try different mapping strategies
		resourceMappings := r.mapENIToResources(ctx, eni, ipAddress)
		mappings = append(mappings, resourceMappings...)
	}

	// Sort by confidence
	for i := 0; i < len(mappings)-1; i++ {
		for j := i + 1; j < len(mappings); j++ {
			if mappings[j].Confidence > mappings[i].Confidence {
				mappings[i], mappings[j] = mappings[j], mappings[i]
			}
		}
	}

	slog.Info("ENI resolution completed",
		"ip", ipAddress,
		"eni_count", len(enis),
		"mappings_found", len(mappings))

	return mappings, nil
}

// findENIByIP finds ENIs with the given IP address (with caching)
func (r *ENIResolver) findENIByIP(ctx context.Context, ipAddress string) ([]*ENIInfo, error) {
	// Create cache key: account:ip
	cacheKey := fmt.Sprintf("%s:%s", r.awsAccountID, ipAddress)

	// Check cache first
	if cached, found := common.CacheGet("eni_ip_mapping", cacheKey); found {
		var enis []*ENIInfo
		if unmarshalErr := json.Unmarshal(cached, &enis); unmarshalErr == nil {
			slog.Debug("ENI lookup cache hit",
				"ip", ipAddress,
				"account", r.awsAccountID,
				"eni_count", len(enis))
			return enis, nil
		} else {
			// Cache unmarshal failed, continue to fetch
			slog.Warn("ENI cache unmarshal failed", "error", unmarshalErr)
		}
	}

	slog.Debug("ENI lookup cache miss, querying AWS",
		"ip", ipAddress,
		"account", r.awsAccountID)

	// Build AWS CLI command to describe network interfaces
	cmd := fmt.Sprintf(
		`aws ec2 describe-network-interfaces --filters "Name=addresses.private-ip-address,Values=%s" --output json`,
		ipAddress,
	)

	resp, err := cloud.ExecuteCli(r.requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: r.awsAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("AWS CLI command failed: %w", err)
	}

	// Parse response
	var result struct {
		NetworkInterfaces []struct {
			NetworkInterfaceId string `json:"NetworkInterfaceId"`
			SubnetId           string `json:"SubnetId"`
			VpcId              string `json:"VpcId"`
			AvailabilityZone   string `json:"AvailabilityZone"`
			Description        string `json:"Description"`
			InterfaceType      string `json:"InterfaceType"`
			PrivateIpAddress   string `json:"PrivateIpAddress"`
			PrivateIpAddresses []struct {
				Primary          bool   `json:"Primary"`
				PrivateIpAddress string `json:"PrivateIpAddress"`
			} `json:"PrivateIpAddresses"`
			Association *struct {
				PublicIp string `json:"PublicIp"`
			} `json:"Association"`
			Groups []struct {
				GroupId   string `json:"GroupId"`
				GroupName string `json:"GroupName"`
			} `json:"Groups"`
			Attachment *struct {
				AttachmentId string `json:"AttachmentId"`
				InstanceId   string `json:"InstanceId"`
				DeviceIndex  int    `json:"DeviceIndex"`
				Status       string `json:"Status"`
			} `json:"Attachment"`
			RequesterId      string `json:"RequesterId"`
			RequesterManaged bool   `json:"RequesterManaged"`
			Status           string `json:"Status"`
			TagSet           []struct {
				Key   string `json:"Key"`
				Value string `json:"Value"`
			} `json:"TagSet"`
		} `json:"NetworkInterfaces"`
	}

	if data, ok := resp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid response format")
	}

	// Convert to ENIInfo
	enis := make([]*ENIInfo, 0, len(result.NetworkInterfaces))
	for _, ni := range result.NetworkInterfaces {
		eni := &ENIInfo{
			ENIId:            ni.NetworkInterfaceId,
			SubnetID:         ni.SubnetId,
			VPCID:            ni.VpcId,
			AvailabilityZone: ni.AvailabilityZone,
			Description:      ni.Description,
			InterfaceType:    ni.InterfaceType,
			RequesterId:      ni.RequesterId,
			Status:           ni.Status,
			PrivateIPs:       make([]string, 0),
			SecurityGroups:   make([]string, 0),
			Tags:             make(map[string]string),
			RDSTags:          make(map[string]string),
		}

		// Extract private IPs
		for _, ip := range ni.PrivateIpAddresses {
			eni.PrivateIPs = append(eni.PrivateIPs, ip.PrivateIpAddress)
		}

		// Extract public IP
		if ni.Association != nil {
			eni.PublicIP = ni.Association.PublicIp
		}

		// Extract security groups
		for _, sg := range ni.Groups {
			eni.SecurityGroups = append(eni.SecurityGroups, sg.GroupId)
		}

		// Extract attachment info
		if ni.Attachment != nil {
			eni.Attachment = map[string]interface{}{
				"attachment_id": ni.Attachment.AttachmentId,
				"instance_id":   ni.Attachment.InstanceId,
				"device_index":  ni.Attachment.DeviceIndex,
				"status":        ni.Attachment.Status,
			}
		}

		// Extract tags
		for _, tag := range ni.TagSet {
			eni.Tags[tag.Key] = tag.Value

			// Separate RDS tags
			if strings.HasPrefix(tag.Key, "aws:rds:") {
				eni.RDSTags[tag.Key] = tag.Value
			}
		}

		enis = append(enis, eni)
	}

	// Cache the results
	if cacheData, err := json.Marshal(enis); err == nil {
		if err := common.CacheSet("eni_ip_mapping", cacheKey, cacheData); err != nil {
			slog.Warn("Failed to cache ENI lookup", "ip", ipAddress, "error", err)
		} else {
			slog.Debug("ENI lookup cached",
				"ip", ipAddress,
				"account", r.awsAccountID,
				"eni_count", len(enis))
		}
	}

	return enis, nil
}

// mapENIToResources maps an ENI to AWS resources using multiple strategies
func (r *ENIResolver) mapENIToResources(ctx context.Context, eni *ENIInfo, originalIP string) []*ENIResourceMapping {
	mappings := make([]*ENIResourceMapping, 0)

	// Strategy 1: RDS Tag-based mapping (highest confidence)
	if eni.RequesterId == "amazon-rds" && len(eni.RDSTags) > 0 {
		if mapping := r.mapENIToRDSByTags(eni, originalIP); mapping != nil {
			mappings = append(mappings, mapping)
		}
	}

	// Strategy 2: RDS IP-based mapping
	if eni.RequesterId == "amazon-rds" || strings.Contains(strings.ToLower(eni.Description), "rds") {
		if rdsMappings := r.mapENIToRDSByIP(ctx, eni, originalIP); len(rdsMappings) > 0 {
			mappings = append(mappings, rdsMappings...)
		}
	}

	// Strategy 3: ElastiCache mapping
	if eni.RequesterId == "amazon-elasticache" || strings.Contains(strings.ToLower(eni.Description), "elasticache") {
		if cacheMappings := r.mapENIToElastiCache(ctx, eni, originalIP); len(cacheMappings) > 0 {
			mappings = append(mappings, cacheMappings...)
		}
	}

	// Strategy 4: EC2 attachment-based mapping
	if eni.Attachment != nil {
		if instanceID, ok := eni.Attachment["instance_id"].(string); ok && instanceID != "" {
			mapping := &ENIResourceMapping{
				ENI:          eni,
				MatchType:    "attachment",
				ResourceType: "ec2",
				ResourceID:   instanceID,
				MatchedIPs:   []string{originalIP},
				Confidence:   0.95,
			}
			mappings = append(mappings, mapping)
		}
	}

	// Strategy 5: Lambda mapping (by description)
	if strings.Contains(strings.ToLower(eni.Description), "lambda") {
		mapping := &ENIResourceMapping{
			ENI:          eni,
			MatchType:    "description",
			ResourceType: "lambda",
			ResourceID:   eni.Description,
			MatchedIPs:   []string{originalIP},
			Confidence:   0.70,
		}
		mappings = append(mappings, mapping)
	}

	// Strategy 6: EKS mapping
	if strings.Contains(strings.ToLower(eni.Description), "eks") {
		mapping := &ENIResourceMapping{
			ENI:          eni,
			MatchType:    "description",
			ResourceType: "eks",
			ResourceID:   eni.Description,
			MatchedIPs:   []string{originalIP},
			Confidence:   0.70,
		}
		mappings = append(mappings, mapping)
	}

	return mappings
}

// mapENIToRDSByTags maps ENI to RDS using AWS tags
func (r *ENIResolver) mapENIToRDSByTags(eni *ENIInfo, originalIP string) *ENIResourceMapping {
	// Extract RDS information from tags
	dbID := ""
	clusterID := ""

	if val, ok := eni.RDSTags["aws:rds:db-id"]; ok {
		dbID = val
	}
	if val, ok := eni.RDSTags["aws:rds:cluster-id"]; ok {
		clusterID = val
	}

	if dbID == "" && clusterID == "" {
		return nil
	}

	resourceID := dbID
	if resourceID == "" {
		resourceID = clusterID
	}

	mapping := &ENIResourceMapping{
		ENI:          eni,
		MatchType:    "tag",
		ResourceType: "rds",
		ResourceID:   resourceID,
		ClusterID:    clusterID,
		MatchedIPs:   []string{originalIP},
		Confidence:   0.95,
		AdditionalInfo: map[string]interface{}{
			"rds_tags": eni.RDSTags,
		},
	}

	slog.Info("Mapped ENI to RDS by tags",
		"eni_id", eni.ENIId,
		"resource_id", resourceID,
		"cluster_id", clusterID)

	return mapping
}

// mapENIToRDSByIP maps ENI to RDS by matching IPs with RDS endpoints
func (r *ENIResolver) mapENIToRDSByIP(ctx context.Context, eni *ENIInfo, originalIP string) []*ENIResourceMapping {
	// Get all RDS endpoints
	endpoints, err := r.getRDSEndpoints(ctx)
	if err != nil {
		slog.Warn("Failed to get RDS endpoints", "error", err)
		return nil
	}

	mappings := make([]*ENIResourceMapping, 0)
	eniIPSet := make(map[string]bool)
	for _, ip := range eni.PrivateIPs {
		eniIPSet[ip] = true
	}

	// Match ENI IPs with RDS endpoint IPs
	for _, endpoint := range endpoints {
		matchedIPs := make([]string, 0)
		for _, endpointIP := range endpoint.IPs {
			if eniIPSet[endpointIP] {
				matchedIPs = append(matchedIPs, endpointIP)
			}
		}

		if len(matchedIPs) > 0 {
			mapping := &ENIResourceMapping{
				ENI:          eni,
				MatchType:    "ip",
				ResourceType: "rds",
				ResourceID:   endpoint.ID,
				ClusterID:    endpoint.ClusterID,
				ResourceName: endpoint.ID,
				Endpoint:     endpoint.Endpoint,
				MatchedIPs:   matchedIPs,
				Confidence:   0.90,
				AdditionalInfo: map[string]interface{}{
					"rds_type": endpoint.Type,
				},
			}
			mappings = append(mappings, mapping)

			slog.Info("Mapped ENI to RDS by IP",
				"eni_id", eni.ENIId,
				"rds_id", endpoint.ID,
				"rds_type", endpoint.Type,
				"matched_ips", matchedIPs)
		}
	}

	return mappings
}

// mapENIToElastiCache maps ENI to ElastiCache clusters
func (r *ENIResolver) mapENIToElastiCache(ctx context.Context, eni *ENIInfo, originalIP string) []*ENIResourceMapping {
	// Get ElastiCache endpoints
	endpoints, err := r.getElastiCacheEndpoints(ctx)
	if err != nil {
		slog.Warn("Failed to get ElastiCache endpoints", "error", err)
		return nil
	}

	mappings := make([]*ENIResourceMapping, 0)
	eniIPSet := make(map[string]bool)
	for _, ip := range eni.PrivateIPs {
		eniIPSet[ip] = true
	}

	// Match ENI IPs with ElastiCache endpoint IPs
	for _, endpoint := range endpoints {
		matchedIPs := make([]string, 0)
		for _, endpointIP := range endpoint.IPs {
			if eniIPSet[endpointIP] {
				matchedIPs = append(matchedIPs, endpointIP)
			}
		}

		if len(matchedIPs) > 0 {
			mapping := &ENIResourceMapping{
				ENI:          eni,
				MatchType:    "ip",
				ResourceType: "elasticache",
				ResourceID:   endpoint.ID,
				ResourceName: endpoint.ID,
				Endpoint:     endpoint.Endpoint,
				MatchedIPs:   matchedIPs,
				Confidence:   0.90,
				AdditionalInfo: map[string]interface{}{
					"cache_type": endpoint.Type,
				},
			}
			mappings = append(mappings, mapping)

			slog.Info("Mapped ENI to ElastiCache by IP",
				"eni_id", eni.ENIId,
				"cache_id", endpoint.ID,
				"matched_ips", matchedIPs)
		}
	}

	return mappings
}

// getRDSEndpoints retrieves all RDS endpoints with their IPs (with caching)
func (r *ENIResolver) getRDSEndpoints(ctx context.Context) ([]*RDSEndpointInfo, error) {
	// Create cache key: account
	cacheKey := r.awsAccountID

	// Check cache first
	if cached, found := common.CacheGet("rds_endpoints", cacheKey); found {
		var endpoints []*RDSEndpointInfo
		if unmarshalErr := json.Unmarshal(cached, &endpoints); unmarshalErr == nil {
			slog.Debug("RDS endpoints cache hit",
				"account", r.awsAccountID,
				"endpoint_count", len(endpoints))
			return endpoints, nil
		} else {
			slog.Warn("RDS endpoints cache unmarshal failed", "error", unmarshalErr)
		}
	}

	slog.Debug("RDS endpoints cache miss, querying AWS", "account", r.awsAccountID)

	endpoints := make([]*RDSEndpointInfo, 0)

	// Get DB Instances
	instances, err := r.getRDSInstances(ctx)
	if err != nil {
		slog.Warn("Failed to get RDS instances", "error", err)
	} else {
		endpoints = append(endpoints, instances...)
	}

	// Get DB Clusters
	clusters, err := r.getRDSClusters(ctx)
	if err != nil {
		slog.Warn("Failed to get RDS clusters", "error", err)
	} else {
		endpoints = append(endpoints, clusters...)
	}

	// Get DB Proxies
	proxies, err := r.getRDSProxies(ctx)
	if err != nil {
		slog.Warn("Failed to get RDS proxies", "error", err)
	} else {
		endpoints = append(endpoints, proxies...)
	}

	// Cache the results
	if cacheData, err := json.Marshal(endpoints); err == nil {
		if err := common.CacheSet("rds_endpoints", cacheKey, cacheData); err != nil {
			slog.Warn("Failed to cache RDS endpoints", "error", err)
		} else {
			slog.Debug("RDS endpoints cached",
				"account", r.awsAccountID,
				"endpoint_count", len(endpoints))
		}
	}

	return endpoints, nil
}

// getRDSInstances gets RDS instance endpoints
func (r *ENIResolver) getRDSInstances(ctx context.Context) ([]*RDSEndpointInfo, error) {
	cmd := `aws rds describe-db-instances --output json`

	resp, err := cloud.ExecuteCli(r.requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: r.awsAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		DBInstances []struct {
			DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
			DBClusterIdentifier  string `json:"DBClusterIdentifier"`
			Endpoint             struct {
				Address string `json:"Address"`
				Port    int    `json:"Port"`
			} `json:"Endpoint"`
		} `json:"DBInstances"`
	}

	if data, ok := resp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, err
		}
	}

	endpoints := make([]*RDSEndpointInfo, 0)
	for _, db := range result.DBInstances {
		if db.Endpoint.Address == "" {
			continue
		}

		ips := r.resolveHostnameToIPs(db.Endpoint.Address)
		endpoints = append(endpoints, &RDSEndpointInfo{
			Type:      "instance",
			ID:        db.DBInstanceIdentifier,
			ClusterID: db.DBClusterIdentifier,
			Endpoint:  db.Endpoint.Address,
			IPs:       ips,
		})
	}

	return endpoints, nil
}

// getRDSClusters gets RDS cluster endpoints
func (r *ENIResolver) getRDSClusters(ctx context.Context) ([]*RDSEndpointInfo, error) {
	cmd := `aws rds describe-db-clusters --output json`

	resp, err := cloud.ExecuteCli(r.requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: r.awsAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		DBClusters []struct {
			DBClusterIdentifier string `json:"DBClusterIdentifier"`
			Endpoint            string `json:"Endpoint"`
			ReaderEndpoint      string `json:"ReaderEndpoint"`
		} `json:"DBClusters"`
	}

	if data, ok := resp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, err
		}
	}

	endpoints := make([]*RDSEndpointInfo, 0)
	for _, cluster := range result.DBClusters {
		// Writer endpoint
		if cluster.Endpoint != "" {
			ips := r.resolveHostnameToIPs(cluster.Endpoint)
			endpoints = append(endpoints, &RDSEndpointInfo{
				Type:     "cluster-writer",
				ID:       cluster.DBClusterIdentifier,
				Endpoint: cluster.Endpoint,
				IPs:      ips,
			})
		}

		// Reader endpoint
		if cluster.ReaderEndpoint != "" {
			ips := r.resolveHostnameToIPs(cluster.ReaderEndpoint)
			endpoints = append(endpoints, &RDSEndpointInfo{
				Type:     "cluster-reader",
				ID:       cluster.DBClusterIdentifier,
				Endpoint: cluster.ReaderEndpoint,
				IPs:      ips,
			})
		}
	}

	return endpoints, nil
}

// getRDSProxies gets RDS proxy endpoints
func (r *ENIResolver) getRDSProxies(ctx context.Context) ([]*RDSEndpointInfo, error) {
	cmd := `aws rds describe-db-proxies --output json`

	resp, err := cloud.ExecuteCli(r.requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: r.awsAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		DBProxies []struct {
			DBProxyName string `json:"DBProxyName"`
			Endpoint    string `json:"Endpoint"`
		} `json:"DBProxies"`
	}

	if data, ok := resp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, err
		}
	}

	endpoints := make([]*RDSEndpointInfo, 0)
	for _, proxy := range result.DBProxies {
		if proxy.Endpoint == "" {
			continue
		}

		ips := r.resolveHostnameToIPs(proxy.Endpoint)
		endpoints = append(endpoints, &RDSEndpointInfo{
			Type:     "proxy",
			ID:       proxy.DBProxyName,
			Endpoint: proxy.Endpoint,
			IPs:      ips,
		})
	}

	return endpoints, nil
}

// getElastiCacheEndpoints gets ElastiCache cluster endpoints (with caching)
func (r *ENIResolver) getElastiCacheEndpoints(ctx context.Context) ([]*RDSEndpointInfo, error) {
	// Create cache key: account
	cacheKey := r.awsAccountID

	// Check cache first
	if cached, found := common.CacheGet("elasticache_endpoints", cacheKey); found {
		var endpoints []*RDSEndpointInfo
		if unmarshalErr := json.Unmarshal(cached, &endpoints); unmarshalErr == nil {
			slog.Debug("ElastiCache endpoints cache hit",
				"account", r.awsAccountID,
				"endpoint_count", len(endpoints))
			return endpoints, nil
		} else {
			slog.Warn("ElastiCache endpoints cache unmarshal failed", "error", unmarshalErr)
		}
	}

	slog.Debug("ElastiCache endpoints cache miss, querying AWS", "account", r.awsAccountID)

	endpoints := make([]*RDSEndpointInfo, 0)

	// Get cache clusters
	cmd := `aws elasticache describe-cache-clusters --show-cache-node-info --output json`

	resp, err := cloud.ExecuteCli(r.requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: r.awsAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		CacheClusters []struct {
			CacheClusterId        string `json:"CacheClusterId"`
			ConfigurationEndpoint *struct {
				Address string `json:"Address"`
			} `json:"ConfigurationEndpoint"`
			CacheNodes []struct {
				Endpoint *struct {
					Address string `json:"Address"`
				} `json:"Endpoint"`
			} `json:"CacheNodes"`
		} `json:"CacheClusters"`
	}

	if data, ok := resp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, err
		}
	}

	for _, cluster := range result.CacheClusters {
		// Configuration endpoint (Memcached)
		if cluster.ConfigurationEndpoint != nil && cluster.ConfigurationEndpoint.Address != "" {
			ips := r.resolveHostnameToIPs(cluster.ConfigurationEndpoint.Address)
			endpoints = append(endpoints, &RDSEndpointInfo{
				Type:     "cache-cluster",
				ID:       cluster.CacheClusterId,
				Endpoint: cluster.ConfigurationEndpoint.Address,
				IPs:      ips,
			})
		}

		// Individual cache nodes (Redis)
		for _, node := range cluster.CacheNodes {
			if node.Endpoint != nil && node.Endpoint.Address != "" {
				ips := r.resolveHostnameToIPs(node.Endpoint.Address)
				endpoints = append(endpoints, &RDSEndpointInfo{
					Type:     "cache-node",
					ID:       cluster.CacheClusterId,
					Endpoint: node.Endpoint.Address,
					IPs:      ips,
				})
			}
		}
	}

	// Cache the results
	if cacheData, err := json.Marshal(endpoints); err == nil {
		if err := common.CacheSet("elasticache_endpoints", cacheKey, cacheData); err != nil {
			slog.Warn("Failed to cache ElastiCache endpoints", "error", err)
		} else {
			slog.Debug("ElastiCache endpoints cached",
				"account", r.awsAccountID,
				"endpoint_count", len(endpoints))
		}
	}

	return endpoints, nil
}

// resolveHostnameToIPs resolves a hostname to IP addresses (with caching)
func (r *ENIResolver) resolveHostnameToIPs(hostname string) []string {
	if hostname == "" {
		return nil
	}

	// Check cache first
	if cached, found := common.CacheGet("endpoint_dns", hostname); found {
		var ips []string
		if unmarshalErr := json.Unmarshal(cached, &ips); unmarshalErr == nil {
			slog.Debug("DNS resolution cache hit", "hostname", hostname, "ips", len(ips))
			return ips
		}
	}

	// Resolve DNS
	ips, err := net.LookupHost(hostname)
	if err != nil {
		slog.Debug("DNS resolution failed", "hostname", hostname, "error", err)
		return nil
	}

	// Cache the results
	if cacheData, err := json.Marshal(ips); err == nil {
		if err := common.CacheSet("endpoint_dns", hostname, cacheData); err != nil {
			slog.Debug("Failed to cache DNS resolution", "hostname", hostname, "error", err)
		}
	}

	return ips
}
