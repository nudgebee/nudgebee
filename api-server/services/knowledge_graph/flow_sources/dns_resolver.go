package flow_sources

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Route53HostedZone represents a Route 53 hosted zone
type Route53HostedZone struct {
	ID          string `json:"Id"`
	Name        string `json:"Name"`
	PrivateZone bool   `json:"PrivateZone"`
}

// Route53ZoneCache holds cached hosted zones per AWS account
// This allows fetching zones once per account and reusing for multiple DNS resolutions
type Route53ZoneCache struct {
	mu    sync.RWMutex
	zones map[string][]Route53HostedZone // key: awsAccountID -> hosted zones
}

// NewRoute53ZoneCache creates a new zone cache
func NewRoute53ZoneCache() *Route53ZoneCache {
	return &Route53ZoneCache{
		zones: make(map[string][]Route53HostedZone),
	}
}

// GetZones returns cached zones for an account, or nil if not cached
func (c *Route53ZoneCache) GetZones(awsAccountID string) []Route53HostedZone {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.zones[awsAccountID]
}

// SetZones caches zones for an account
func (c *Route53ZoneCache) SetZones(awsAccountID string, zones []Route53HostedZone) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.zones[awsAccountID] = zones
}

// Route53RecordSet represents a DNS record set from Route 53
type Route53RecordSet struct {
	Name            string `json:"Name"`
	Type            string `json:"Type"`
	ResourceRecords []struct {
		Value string `json:"Value"`
	} `json:"ResourceRecords"`
}

// Route53RecordCache holds cached resource record sets per hosted zone
// This avoids repeated API calls when resolving multiple hostnames in the same zone
type Route53RecordCache struct {
	mu      sync.RWMutex
	records map[string][]Route53RecordSet // key: awsAccountID:hostedZoneID -> record sets
}

// NewRoute53RecordCache creates a new record cache
func NewRoute53RecordCache() *Route53RecordCache {
	return &Route53RecordCache{
		records: make(map[string][]Route53RecordSet),
	}
}

// GetRecords returns cached records for a zone, or nil if not cached
func (c *Route53RecordCache) GetRecords(awsAccountID, hostedZoneID string) []Route53RecordSet {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := awsAccountID + ":" + hostedZoneID
	return c.records[key]
}

// SetRecords caches records for a zone
func (c *Route53RecordCache) SetRecords(awsAccountID, hostedZoneID string, records []Route53RecordSet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := awsAccountID + ":" + hostedZoneID
	c.records[key] = records
}

// FetchRecordSets fetches all resource record sets for a hosted zone
// This should be called once per zone and the result cached
func FetchRecordSets(
	requestContext *security.RequestContext,
	awsAccountID string,
	hostedZoneID string,
) ([]Route53RecordSet, error) {
	recordCommand := fmt.Sprintf("aws route53 list-resource-record-sets --hosted-zone-id %s --output json", hostedZoneID)
	recordResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   recordCommand,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list resource record sets: %w", err)
	}

	var recordSets struct {
		ResourceRecordSets []Route53RecordSet `json:"ResourceRecordSets"`
	}

	if data, ok := recordResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &recordSets); err != nil {
			return nil, fmt.Errorf("failed to parse resource record sets: %w", err)
		}
	} else {
		return nil, nil
	}

	slog.Info("Fetched Route 53 resource record sets",
		"aws_account", awsAccountID,
		"hosted_zone_id", hostedZoneID,
		"record_count", len(recordSets.ResourceRecordSets))

	return recordSets.ResourceRecordSets, nil
}

// FetchHostedZones fetches all hosted zones for an AWS account
// This should be called once per account and the result cached
func FetchHostedZones(
	requestContext *security.RequestContext,
	awsAccountID string,
) ([]Route53HostedZone, error) {
	zonesCommand := "aws route53 list-hosted-zones-by-name --output json"
	zonesResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   zonesCommand,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list hosted zones: %w", err)
	}

	var hostedZones struct {
		HostedZones []struct {
			Id     string `json:"Id"`
			Name   string `json:"Name"`
			Config struct {
				PrivateZone bool `json:"PrivateZone"`
			} `json:"Config"`
		} `json:"HostedZones"`
	}

	if data, ok := zonesResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &hostedZones); err != nil {
			return nil, fmt.Errorf("failed to parse hosted zones: %w", err)
		}
	} else {
		return nil, nil
	}

	// Convert to our type
	zones := make([]Route53HostedZone, 0, len(hostedZones.HostedZones))
	for _, z := range hostedZones.HostedZones {
		zones = append(zones, Route53HostedZone{
			ID:          z.Id,
			Name:        z.Name,
			PrivateZone: z.Config.PrivateZone,
		})
	}

	slog.Info("Fetched Route 53 hosted zones",
		"aws_account", awsAccountID,
		"zone_count", len(zones))

	return zones, nil
}

// findMatchingZone finds the most specific hosted zone that matches the given hostname
// It returns the longest matching zone to handle cases where multiple zones could match
// (e.g., for hostname "www.sub.example.com", zone "sub.example.com" is preferred over "example.com")
func findMatchingZone(hostname string, zones []Route53HostedZone) *Route53HostedZone {
	var bestMatch *Route53HostedZone
	var bestMatchLen int

	for i := range zones {
		zoneName := strings.TrimSuffix(zones[i].Name, ".")
		if strings.HasSuffix(hostname, zoneName) && len(zoneName) > bestMatchLen {
			bestMatch = &zones[i]
			bestMatchLen = len(zoneName)
		}
	}
	return bestMatch
}

// ResolveRoute53DNS resolves a hostname via Route 53 and returns the AWS endpoint (if it's an AWS service)
// Returns empty string if not found or not an AWS service
// Native implementation that queries Route 53 directly
// Note: For batch operations, use ResolveRoute53DNSWithZones with pre-fetched zones for better performance
func ResolveRoute53DNS(
	requestContext *security.RequestContext,
	hostname string,
	awsAccountID string,
) (string, error) {
	if hostname == "" {
		return "", nil
	}

	// Fetch zones (this is inefficient for batch operations - use ResolveRoute53DNSWithZones instead)
	zones, err := FetchHostedZones(requestContext, awsAccountID)
	if err != nil {
		return "", err
	}

	return ResolveRoute53DNSWithZones(requestContext, hostname, awsAccountID, zones)
}

// ResolveRoute53DNSWithZones resolves a hostname via Route 53 using pre-fetched hosted zones
// This is more efficient for batch operations as it avoids repeated zone lookups
// Returns empty string if not found or not an AWS service
// Note: For even better performance with many hostnames, use ResolveRoute53DNSWithCache
func ResolveRoute53DNSWithZones(
	requestContext *security.RequestContext,
	hostname string,
	awsAccountID string,
	zones []Route53HostedZone,
) (string, error) {
	// Delegate to cached version with nil cache (will fetch records each time)
	return ResolveRoute53DNSWithCache(requestContext, hostname, awsAccountID, zones, nil)
}

// ResolveRoute53DNSWithCache resolves a hostname via Route 53 using pre-fetched zones and cached records
// This is the most efficient option for batch operations as it caches both zones and record sets
// Returns empty string if not found or not an AWS service
func ResolveRoute53DNSWithCache(
	requestContext *security.RequestContext,
	hostname string,
	awsAccountID string,
	zones []Route53HostedZone,
	recordCache *Route53RecordCache,
) (string, error) {
	if hostname == "" {
		return "", nil
	}

	if len(zones) == 0 {
		return "", nil
	}

	// Find matching zone
	matchingZone := findMatchingZone(hostname, zones)
	if matchingZone == nil {
		return "", nil // No matching zone found
	}

	// Try to get records from cache first
	var records []Route53RecordSet
	if recordCache != nil {
		records = recordCache.GetRecords(awsAccountID, matchingZone.ID)
	}

	// If not cached, fetch from AWS
	if records == nil {
		var err error
		records, err = FetchRecordSets(requestContext, awsAccountID, matchingZone.ID)
		if err != nil {
			return "", err
		}

		// Cache the records for future lookups
		if recordCache != nil && records != nil {
			recordCache.SetRecords(awsAccountID, matchingZone.ID, records)
		}
	}

	if records == nil {
		return "", nil
	}

	// Find the record for this hostname
	hostnameWithDot := hostname + "."
	for _, record := range records {
		if record.Name == hostnameWithDot || record.Name == hostname {
			// Handle CNAME records
			if record.Type == "CNAME" && len(record.ResourceRecords) > 0 {
				cname := strings.TrimSuffix(record.ResourceRecords[0].Value, ".")

				// Only return if it's an AWS service endpoint
				if strings.Contains(cname, ".elb.amazonaws.com") ||
					strings.Contains(cname, ".cache.amazonaws.com") ||
					strings.Contains(cname, ".rds.amazonaws.com") {
					return cname, nil
				}
			}
			break
		}
	}

	return "", nil // Not found or not an AWS service
}

// EnrichRoute53DNSWithTargets enriches DNS hostname nodes with their backend targets
// This resolves DNS names through Route 53 and maps them to K8s pods or AWS resources
// Native implementation that works directly with core.DbNode and core.DbEdge types
func EnrichRoute53DNSWithTargets(
	requestContext *security.RequestContext,
	hostname string,
	existingNodes []*core.DbNode,
	awsAccountID string,
	k8sAccountID string,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	newNodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	if hostname == "" {
		return newNodes, edges, nil
	}

	slog.Info("Enriching DNS hostname with targets",
		"hostname", hostname,
		"aws_account", awsAccountID,
		"k8s_account", k8sAccountID)

	// Step 1: List hosted zones to find the matching zone
	zonesCommand := "aws route53 list-hosted-zones-by-name --output json"
	zonesResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   zonesCommand,
	})
	if err != nil {
		slog.Warn("Failed to list Route 53 hosted zones",
			"hostname", hostname,
			"error", err)
		return newNodes, edges, nil
	}

	// Parse hosted zones response
	var hostedZones struct {
		HostedZones []struct {
			Id     string `json:"Id"`
			Name   string `json:"Name"`
			Config struct {
				PrivateZone bool `json:"PrivateZone"`
			} `json:"Config"`
		} `json:"HostedZones"`
	}

	if data, ok := zonesResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &hostedZones); err != nil {
			slog.Warn("Failed to parse hosted zones", "error", err)
			return newNodes, edges, nil
		}
	} else {
		slog.Info("No data in hosted zones response")
		return newNodes, edges, nil
	}

	// Step 2: Find the zone matching our hostname
	var matchingZoneId string
	var matchingZoneName string

	slog.Info("Searching for matching hosted zone",
		"hostname", hostname,
		"zones_to_check", len(hostedZones.HostedZones))

	for _, zone := range hostedZones.HostedZones {
		zoneName := strings.TrimSuffix(zone.Name, ".")
		matches := strings.HasSuffix(hostname, zoneName)

		slog.Debug("Checking zone match",
			"hostname", hostname,
			"zone", zoneName,
			"matches", matches)

		if matches {
			matchingZoneId = zone.Id
			matchingZoneName = zone.Name
			slog.Info("Found matching Route 53 zone",
				"hostname", hostname,
				"zone", zone.Name,
				"zone_id", zone.Id,
				"private", zone.Config.PrivateZone)
			break
		}
	}

	if matchingZoneId == "" {
		slog.Warn("No hosted zone found for hostname",
			"hostname", hostname,
			"zones_checked", len(hostedZones.HostedZones))
		return newNodes, edges, nil
	}

	// Step 3: Query DNS records for the hostname
	recordsCommand := fmt.Sprintf(
		"aws route53 list-resource-record-sets --hosted-zone-id %s --output json",
		matchingZoneId,
	)

	recordsResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   recordsCommand,
	})
	if err != nil {
		slog.Warn("Failed to query Route 53 DNS records",
			"hostname", hostname,
			"zone_id", matchingZoneId,
			"error", err)
		return newNodes, edges, nil
	}

	// Parse DNS records
	var recordSets struct {
		ResourceRecordSets []struct {
			Name            string `json:"Name"`
			Type            string `json:"Type"`
			TTL             int    `json:"TTL"`
			ResourceRecords []struct {
				Value string `json:"Value"`
			} `json:"ResourceRecords"`
			AliasTarget struct {
				HostedZoneId         string `json:"HostedZoneId"`
				DNSName              string `json:"DNSName"`
				EvaluateTargetHealth bool   `json:"EvaluateTargetHealth"`
			} `json:"AliasTarget"`
		} `json:"ResourceRecordSets"`
	}

	if data, ok := recordsResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &recordSets); err != nil {
			slog.Warn("Failed to parse DNS records", "error", err)
			return newNodes, edges, nil
		}
	} else {
		slog.Info("No data in DNS records response")
		return newNodes, edges, nil
	}

	// Step 4: Find records matching our hostname
	dnsNameWithDot := hostname + "."
	var foundRecord bool

	for _, record := range recordSets.ResourceRecordSets {
		if record.Name != dnsNameWithDot {
			continue
		}

		foundRecord = true
		slog.Info("Found DNS record",
			"hostname", hostname,
			"type", record.Type,
			"ttl", record.TTL)

		switch record.Type {
		case "A", "AAAA":
			// A/AAAA records contain IP addresses - map to pods
			ips := make([]string, 0)
			for _, rr := range record.ResourceRecords {
				ips = append(ips, rr.Value)
			}

			if len(ips) > 0 {
				slog.Info("Resolving DNS A record to pods",
					"hostname", hostname,
					"ips", ips)

				// Map IPs to pods using the existing MapIPsToPods wrapper
				podNodes, podEdges, err := MapIPsToPods(
					ips,
					k8sAccountID,
					tenantID,
					hostname,
					existingNodes,
				)
				if err != nil {
					slog.Warn("Failed to map DNS IPs to pods",
						"hostname", hostname,
						"error", err)
				} else {
					newNodes = append(newNodes, podNodes...)
					edges = append(edges, podEdges...)
				}
			}

		case "CNAME":
			// CNAME points to another DNS name
			if len(record.ResourceRecords) == 0 {
				break
			}

			cname := strings.TrimSuffix(record.ResourceRecords[0].Value, ".")
			slog.Info("DNS CNAME record found",
				"hostname", hostname,
				"cname", cname)

			// Check if CNAME points to an ELB
			if strings.Contains(cname, ".elb.amazonaws.com") {
				slog.Info("DNS resolves to ELB, enriching with load balancer targets",
					"hostname", hostname,
					"elb_dns", cname)

				// Create a temporary LoadBalancer node
				lbNode := &core.DbNode{
					NodeType: core.NodeTypeLoadBalancer,
					Properties: map[string]interface{}{
						"name":           hostname,
						"resource_type":  "LoadBalancer",
						"dns_name":       cname,
						"aws_account_id": awsAccountID,
					},
				}

				// Try to extract region and ARN from ELB DNS
				// Format: internal-{name}-{id}.{region}.elb.amazonaws.com
				parts := strings.Split(cname, ".")
				if len(parts) >= 3 {
					region := parts[len(parts)-4] // e.g., "us-east-1"
					lbNode.Properties["region"] = region

					// Try to get LoadBalancer ARN
					elbCommand := fmt.Sprintf(
						"aws elbv2 describe-load-balancers --region %s --query 'LoadBalancers[?DNSName==`%s`].LoadBalancerArn' --output json",
						region, cname,
					)

					elbResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
						AccountID: awsAccountID,
						Command:   elbCommand,
					})
					if err == nil {
						if data, ok := elbResp["data"].(string); ok {
							var arns []string
							if err := json.Unmarshal([]byte(data), &arns); err == nil && len(arns) > 0 {
								lbNode.Properties["arn"] = arns[0]

								// Use CloudEnricher to enrich LoadBalancer with targets (using DbNodes directly)
								enricher := NewCloudEnricher(slog.Default())
								lbPodNodes, lbEdges, err := enricher.EnrichLoadBalancers(
									requestContext,
									[]*core.DbNode{lbNode},
									existingNodes,
									awsAccountID,
									k8sAccountID,
									tenantID,
								)
								if err != nil {
									slog.Warn("Failed to enrich DNS via LoadBalancer",
										"hostname", hostname,
										"error", err)
								} else {
									newNodes = append(newNodes, lbPodNodes...)
									edges = append(edges, lbEdges...)
								}
							}
						}
					}
				}
			} else if strings.Contains(cname, ".cache.amazonaws.com") {
				// ElastiCache (Redis/Memcached)
				slog.Info("DNS resolves to ElastiCache",
					"hostname", hostname,
					"cache_endpoint", cname)

				// Create ElastiCache node using core.NewNode() with proper unique key
				cacheProperties := map[string]interface{}{
					"name":       cname,
					"endpoint":   cname,
					"dns_name":   hostname,
					"cache_type": "elasticache",
				}
				keyComponents := core.NewUniqueKeyComponents(core.CloudProviderAWS, core.NodeTypeCache)
				keyComponents.Account = awsAccountID
				keyComponents.Name = cname
				cacheNode := core.NewNode(core.NodeTypeCache, keyComponents.Build(), cacheProperties, tenantID, awsAccountID, "cloud")
				newNodes = append(newNodes, cacheNode)

				// Find the external service node for this hostname
				var externalServiceNode *core.DbNode
				for _, node := range existingNodes {
					if node.NodeType == core.NodeTypeExternalService {
						if nodeName, ok := node.Properties["name"].(string); ok && nodeName == hostname {
							externalServiceNode = node
							break
						}
					}
				}

				// Create edge from external service to ElastiCache
				if externalServiceNode != nil {
					edge := &core.DbEdge{
						ID:                uuid.New().String(),
						SourceNodeID:      externalServiceNode.ID,
						DestinationNodeID: cacheNode.ID,
						RelationshipType:  core.RelationshipRoutesTo,
						Properties: map[string]interface{}{
							"discovered_from": "route53_dns",
							"dns_name":        hostname,
							"cache_endpoint":  cname,
						},
						CloudAccountID: awsAccountID,
						TenantID:       tenantID,
						Level:          "Tenant",
						Source:         "cloud",
						CreatedAt:      time.Now(),
						UpdatedAt:      time.Now(),
					}
					edges = append(edges, edge)

					slog.Info("Created edge from external service to ElastiCache",
						"external_service", hostname,
						"cache_endpoint", cname)
				} else {
					slog.Warn("Could not find external service node for hostname",
						"hostname", hostname)
				}

			} else if strings.Contains(cname, ".rds.amazonaws.com") {
				// RDS (PostgreSQL/MySQL/etc)
				slog.Info("DNS resolves to RDS",
					"hostname", hostname,
					"rds_endpoint", cname)

				// Create RDS node using core.NewNode() with proper unique key
				rdsProperties := map[string]interface{}{
					"name":     cname,
					"endpoint": cname,
					"dns_name": hostname,
				}
				rdsKeyComponents := core.NewUniqueKeyComponents(core.CloudProviderAWS, core.NodeTypeDatabase)
				rdsKeyComponents.Account = awsAccountID
				rdsKeyComponents.Name = cname
				rdsNode := core.NewNode(core.NodeTypeDatabase, rdsKeyComponents.Build(), rdsProperties, tenantID, awsAccountID, "cloud")
				newNodes = append(newNodes, rdsNode)

				// Find the external service node for this hostname
				var externalServiceNode *core.DbNode
				for _, node := range existingNodes {
					if node.NodeType == core.NodeTypeExternalService {
						if nodeName, ok := node.Properties["name"].(string); ok && nodeName == hostname {
							externalServiceNode = node
							break
						}
					}
				}

				// Create edge from external service to RDS
				if externalServiceNode != nil {
					edge := &core.DbEdge{
						ID:                uuid.New().String(),
						SourceNodeID:      externalServiceNode.ID,
						DestinationNodeID: rdsNode.ID,
						RelationshipType:  core.RelationshipRoutesTo,
						Properties: map[string]interface{}{
							"discovered_from": "route53_dns",
							"dns_name":        hostname,
							"rds_endpoint":    cname,
						},
						CloudAccountID: awsAccountID,
						TenantID:       tenantID,
						Level:          "Tenant",
						Source:         "cloud",
						CreatedAt:      time.Now(),
						UpdatedAt:      time.Now(),
					}
					edges = append(edges, edge)

					slog.Info("Created edge from external service to RDS",
						"external_service", hostname,
						"rds_endpoint", cname)
				} else {
					slog.Warn("Could not find external service node for hostname",
						"hostname", hostname)
				}

			} else {
				// Generic external service
				slog.Info("DNS resolves to external service",
					"hostname", hostname,
					"target", cname)

				// Create external service node using core.NewNode() with proper unique key
				extProperties := map[string]interface{}{
					"name":   hostname,
					"target": cname,
				}
				extKeyComponents := core.NewUniqueKeyComponents(core.CloudProviderExternal, core.NodeTypeExternalService)
				extKeyComponents.Account = tenantID
				extKeyComponents.Name = hostname
				externalNode := core.NewNode(core.NodeTypeExternalService, extKeyComponents.Build(), extProperties, tenantID, "", "cloud")
				newNodes = append(newNodes, externalNode)
			}
		}
	}

	if !foundRecord {
		slog.Info("No DNS record found for hostname",
			"hostname", hostname,
			"zone", matchingZoneName)
	}

	slog.Info("Route 53 DNS enrichment completed",
		"hostname", hostname,
		"nodes_added", len(newNodes),
		"edges_added", len(edges))

	return newNodes, edges, nil
}

// ResolveIngressBackendServices queries Kubernetes Ingress resources and creates Service nodes
// Native implementation that works directly with core.DbNode and core.DbEdge types
// This function resolves backend services exposed by Ingress controllers (e.g., nginx-ingress)
func ResolveIngressBackendServices(
	requestContext *security.RequestContext,
	k8sAccountID string,
	tenantID string,
	environment string,
	ingressControllerNode *core.DbNode,
) ([]*core.DbNode, []*core.DbEdge, error) {

	// Execute kubectl command via relay server to get all Ingress resources
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  k8sAccountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": "kubectl get ingress --all-namespaces -o json",
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute kubectl command: %w", err)
	}

	// Extract stdout from relay response
	var relayData struct {
		Stdout string `json:"stdout"`
	}
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected data format in relay response: %T", relayResponse["data"])
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal relay data: %w", err)
	}

	if relayData.Stdout == "" {
		slog.Debug("No Ingress resources found")
		return nil, nil, nil // No ingress resources found
	}

	// Parse the Ingress list response
	type IngressBackend struct {
		Service struct {
			Name string `json:"name"`
			Port struct {
				Number int `json:"number"`
			} `json:"port"`
		} `json:"service"`
	}

	type IngressPath struct {
		Path    string         `json:"path"`
		Backend IngressBackend `json:"backend"`
	}

	type IngressRule struct {
		Host string `json:"host"`
		HTTP struct {
			Paths []IngressPath `json:"paths"`
		} `json:"http"`
	}

	type IngressSpec struct {
		IngressClassName string        `json:"ingressClassName"`
		Rules            []IngressRule `json:"rules"`
	}

	type IngressResource struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec IngressSpec `json:"spec"`
	}

	type IngressList struct {
		Items []IngressResource `json:"items"`
	}

	var ingressList IngressList
	if err := json.Unmarshal([]byte(relayData.Stdout), &ingressList); err != nil {
		return nil, nil, fmt.Errorf("failed to parse ingress list: %w", err)
	}

	var backendNodes []*core.DbNode
	var backendEdges []*core.DbEdge

	// Track unique backend services to avoid duplicates
	uniqueBackends := make(map[string]bool) // key: namespace:serviceName

	for _, ingress := range ingressList.Items {
		// Only process ingresses that use nginx ingress controller
		if ingress.Spec.IngressClassName != "nginx" {
			continue
		}

		for _, rule := range ingress.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				backendServiceName := path.Backend.Service.Name
				namespace := ingress.Metadata.Namespace

				if backendServiceName == "" {
					continue
				}

				backendKey := fmt.Sprintf("%s:%s", namespace, backendServiceName)
				if uniqueBackends[backendKey] {
					continue // Already processed this backend
				}
				uniqueBackends[backendKey] = true

				// Create Service node for the backend service using core.NewNode()
				backendProperties := map[string]interface{}{
					"name":          backendServiceName,
					"namespace":     namespace,
					"environment":   environment,
					"service.name":  backendServiceName,
					"ingress_host":  rule.Host,
					"ingress_path":  path.Path,
					"ingress_name":  ingress.Metadata.Name,
					"backend_port":  path.Backend.Service.Port.Number,
					"exposed_via":   "ingress",
					"ingress_class": ingress.Spec.IngressClassName,
				}
				backendKeyComponents := core.NewUniqueKeyComponents(core.CloudProviderK8s, core.NodeTypeService)
				backendKeyComponents.Account = k8sAccountID
				backendKeyComponents.Hierarchy = namespace
				backendKeyComponents.Name = backendServiceName
				backendNode := core.NewNode(core.NodeTypeService, backendKeyComponents.Build(), backendProperties, tenantID, k8sAccountID, "cloud")

				// Create edge from ingress controller to backend service
				backendEdge := &core.DbEdge{
					ID:                uuid.New().String(),
					SourceNodeID:      ingressControllerNode.ID,
					DestinationNodeID: backendNode.ID,
					RelationshipType:  core.RelationshipRoutesTo,
					Properties: map[string]interface{}{
						"discovered_from": "ingress_resource",
						"ingress_name":    ingress.Metadata.Name,
						"ingress_host":    rule.Host,
						"ingress_path":    path.Path,
						"backend_port":    path.Backend.Service.Port.Number,
					},
					CloudAccountID: k8sAccountID,
					TenantID:       tenantID,
					Level:          "Tenant",
					Source:         "cloud",
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}

				backendNodes = append(backendNodes, backendNode)
				backendEdges = append(backendEdges, backendEdge)

				slog.Debug("Resolved Ingress backend service",
					"ingress", ingress.Metadata.Name,
					"namespace", namespace,
					"backend_service", backendServiceName,
					"host", rule.Host,
					"path", path.Path)
			}
		}
	}

	return backendNodes, backendEdges, nil
}

// EnrichLoadBalancerWithTargets enriches a LoadBalancer node by discovering its backend targets
// This function queries AWS ELB target groups and maps them to Kubernetes pods/services
// Native implementation using core.DbNode and core.DbEdge types
func EnrichLoadBalancerWithTargets(
	requestContext *security.RequestContext,
	lbNode *core.DbNode,
	existingNodes []*core.DbNode,
	awsAccountID string,
	k8sAccountID string,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	podNodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Build a map of existing Service nodes for quick lookup by name+namespace+cluster
	serviceNodeMap := make(map[string]*core.DbNode)
	// Build a map to track pod owner nodes (Deployment/StatefulSet/DaemonSet)
	ownerNodeMap := make(map[string]*core.DbNode)
	for _, node := range existingNodes {
		if node.NodeType == core.NodeTypeService {
			name, _ := node.Properties["name"].(string)
			namespace, _ := node.Properties["namespace"].(string)
			cluster, _ := node.Properties["cluster"].(string)
			if name != "" {
				// Key format: name:namespace:cluster (namespace and cluster can be empty)
				key := fmt.Sprintf("%s:%s:%s", name, namespace, cluster)
				serviceNodeMap[key] = node
			}
		}
	}

	// Extract LoadBalancer ARN and region from properties
	arn, arnOk := lbNode.Properties["arn"].(string)
	region, regionOk := lbNode.Properties["region"].(string)

	if !arnOk || !regionOk || arn == "" || region == "" {
		slog.Debug("Skipping LoadBalancer enrichment: missing ARN or region",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1: Query AWS for target groups
	tgCommand := fmt.Sprintf(
		"aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json",
		region, arn,
	)

	tgResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tgCommand,
	})
	if err != nil {
		slog.Warn("Failed to query LoadBalancer target groups",
			"lb_name", lbNode.Properties["name"],
			"error", err)
		return podNodes, edges, nil
	}

	// Parse target groups
	var targetGroups []map[string]interface{}
	if data, ok := tgResp["data"].(string); ok {
		var tgData struct {
			TargetGroups []map[string]interface{} `json:"TargetGroups"`
		}
		if err := json.Unmarshal([]byte(data), &tgData); err != nil {
			slog.Warn("Failed to parse target groups", "error", err)
			return podNodes, edges, nil
		}
		targetGroups = tgData.TargetGroups
	}

	if len(targetGroups) == 0 {
		slog.Debug("No target groups found for LoadBalancer",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1.5: Query LoadBalancer tags to check for Kubernetes service mapping
	tagsCommand := fmt.Sprintf(
		"aws elbv2 describe-tags --resource-arns %s --output json",
		arn,
	)

	tagsResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tagsCommand,
	})

	var k8sServiceName, k8sNamespace string
	if err == nil && tagsResp != nil {
		if data, ok := tagsResp["data"].(string); ok {
			var tagsData struct {
				TagDescriptions []struct {
					Tags []struct {
						Key   string `json:"Key"`
						Value string `json:"Value"`
					} `json:"Tags"`
				} `json:"TagDescriptions"`
			}
			if json.Unmarshal([]byte(data), &tagsData) == nil && len(tagsData.TagDescriptions) > 0 {
				for _, tag := range tagsData.TagDescriptions[0].Tags {
					if tag.Key == "kubernetes.io/service-name" {
						parts := strings.Split(tag.Value, "/")
						if len(parts) == 2 {
							k8sNamespace = parts[0]
							k8sServiceName = parts[1]
							slog.Info("Found Kubernetes service for LoadBalancer",
								"lb_name", lbNode.Properties["name"],
								"k8s_service", tag.Value)
						}
						break
					}
				}
			}
		}
	}

	// If this LB is for an ingress controller, create ingress node and skip pod mapping
	if k8sNamespace != "" && k8sServiceName != "" && strings.Contains(k8sServiceName, "ingress") {
		// Infer environment from LoadBalancer tags or use "inferred"
		environment := "inferred"
		if lbEnv, ok := lbNode.Properties["environment"].(string); ok && lbEnv != "" {
			environment = lbEnv
		}

		// Create ingress node using core.NewNode() with proper unique key
		ingressProperties := map[string]interface{}{
			"name":         k8sServiceName,
			"namespace":    k8sNamespace,
			"environment":  environment,
			"type":         "nginx",
			"service.name": k8sServiceName,
		}
		ingressKeyComponents := core.NewUniqueKeyComponents(core.CloudProviderK8s, core.NodeTypeService)
		ingressKeyComponents.Account = k8sAccountID
		ingressKeyComponents.Hierarchy = k8sNamespace
		ingressKeyComponents.Name = k8sServiceName
		ingressNode := core.NewNode(core.NodeTypeService, ingressKeyComponents.Build(), ingressProperties, tenantID, k8sAccountID, "cloud")

		edge := &core.DbEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.ID,
			DestinationNodeID: ingressNode.ID,
			RelationshipType:  core.RelationshipRoutesTo,
			Properties: map[string]interface{}{
				"discovered_from": "aws_lb_tags",
				"service_name":    fmt.Sprintf("%s/%s", k8sNamespace, k8sServiceName),
			},
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}

		slog.Info("Created ingress controller node for LoadBalancer",
			"lb_name", lbNode.Properties["name"],
			"ingress_service", k8sServiceName,
			"namespace", k8sNamespace)

		// Collect initial nodes and edges
		nodes := []*core.DbNode{ingressNode}
		edges := []*core.DbEdge{edge}

		// Step 1.5: Resolve Ingress resources to backend services
		// Query for Ingress resources across all namespaces to find backend services
		ingressBackendNodes, ingressBackendEdges, err := ResolveIngressBackendServices(requestContext, k8sAccountID, tenantID, environment, ingressNode)
		if err != nil {
			slog.Warn("Failed to resolve Ingress backend services",
				"error", err,
				"ingress_service", k8sServiceName)
			// Continue without backend resolution - this is not a fatal error
		} else if ingressBackendNodes != nil {
			nodes = append(nodes, ingressBackendNodes...)
			edges = append(edges, ingressBackendEdges...)
			slog.Info("Resolved Ingress backend services",
				"ingress_service", k8sServiceName,
				"backend_services_count", len(ingressBackendNodes))
		}

		return nodes, edges, nil
	}

	// Step 2: Collect all target IPs and instance IDs from all target groups
	uniqueIPs := make(map[string]bool)
	instanceIDs := make(map[string]bool)

	for _, tg := range targetGroups {
		tgArn, ok := tg["TargetGroupArn"].(string)
		if !ok {
			continue
		}

		healthCommand := fmt.Sprintf(
			"aws elbv2 describe-target-health --region %s --target-group-arn %s --output json",
			region, tgArn,
		)

		healthResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   healthCommand,
		})
		if err != nil {
			slog.Warn("Failed to query target health", "target_group", tgArn, "error", err)
			continue
		}

		// Parse target health
		if data, ok := healthResp["data"].(string); ok {
			var healthData struct {
				TargetHealthDescriptions []map[string]interface{} `json:"TargetHealthDescriptions"`
			}
			if err := json.Unmarshal([]byte(data), &healthData); err != nil {
				continue
			}

			for _, target := range healthData.TargetHealthDescriptions {
				if targetInfo, ok := target["Target"].(map[string]interface{}); ok {
					if targetID, ok := targetInfo["Id"].(string); ok {
						// Check if this is an instance ID (starts with "i-") or an IP address
						if strings.HasPrefix(targetID, "i-") {
							// This is an EC2 instance ID - collect it for resolution
							instanceIDs[targetID] = true
						} else {
							// It's an IP address - add it directly
							uniqueIPs[targetID] = true
						}
					}
				}
			}
		}
	}

	// Step 2b: Resolve EC2 instance IDs to private IPs
	if len(instanceIDs) > 0 {
		slog.Info("Resolving EC2 instance IDs to private IPs",
			"lb_name", lbNode.Properties["name"],
			"instance_count", len(instanceIDs))

		// Build space-separated list of instance IDs
		instanceIDList := make([]string, 0, len(instanceIDs))
		for instanceID := range instanceIDs {
			instanceIDList = append(instanceIDList, instanceID)
		}
		instanceIDStr := strings.Join(instanceIDList, " ")

		// Query EC2 to get private IPs for all instances in one call
		ec2Command := fmt.Sprintf(
			"aws ec2 describe-instances --region %s --instance-ids %s --query 'Reservations[].Instances[].[InstanceId,PrivateIpAddress]' --output json",
			region, instanceIDStr,
		)

		ec2Resp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   ec2Command,
		})
		if err != nil {
			slog.Warn("Failed to query EC2 instances",
				"lb_name", lbNode.Properties["name"],
				"error", err)
		} else {
			// Parse EC2 response: [[instanceID, privateIP], ...]
			if data, ok := ec2Resp["data"].(string); ok {
				var instances [][]string
				if err := json.Unmarshal([]byte(data), &instances); err == nil {
					for _, inst := range instances {
						if len(inst) == 2 {
							instanceID := inst[0]
							privateIP := inst[1]
							if privateIP != "" {
								uniqueIPs[privateIP] = true
								slog.Debug("Resolved EC2 instance to private IP",
									"instance_id", instanceID,
									"private_ip", privateIP,
									"lb_name", lbNode.Properties["name"])
							}
						}
					}
					slog.Info("EC2 instance resolution completed",
						"lb_name", lbNode.Properties["name"],
						"instances_resolved", len(instances),
						"total_ips", len(uniqueIPs))
				} else {
					slog.Warn("Failed to parse EC2 response", "error", err)
				}
			}
		}
	}

	if len(uniqueIPs) == 0 {
		slog.Info("No target IPs found for LoadBalancer",
			"lb_name", lbNode.Properties["name"],
			"instance_targets_attempted", len(instanceIDs))
		return podNodes, edges, nil
	}

	// Step 3: Query kube_pod_info to map IPs to pod names (using K8s account ID)
	ipList := make([]string, 0, len(uniqueIPs))
	for ip := range uniqueIPs {
		ipList = append(ipList, ip)
	}
	ipFilter := strings.Join(ipList, "|")

	queries := map[string]string{
		"pod_info": fmt.Sprintf(`kube_pod_info{pod_ip=~"%s"}`, ipFilter),
	}

	// Use UTC: relay.ExecutePrometheus formats the timestamp with a "UTC"
	// suffix; the value must already be UTC or Prometheus is queried at a
	// future time and returns empty. Defense in depth with the relay-side fix.
	endTime := time.Now().UTC()
	startTime := endTime.Add(-5 * time.Minute)

	podInfoResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
	if err != nil {
		slog.Warn("Failed to query kube_pod_info",
			"lb_name", lbNode.Properties["name"],
			"error", err)
		return podNodes, edges, nil
	}

	// Step 4: Create Pod nodes and edges from query results
	// The response structure from relay.ExecutePrometheus can be:
	// {"pod_info": [{"metric": {...}, "value": [...]}, ...]} - query name as key
	// {"data": [{"metric": ..., "value": ...}, ...]} - data wrapper
	// {"data": {"pod_info": {"result": [...]}}} - nested structure
	var resultArray []interface{}

	// Try to get the array from the response
	if podInfoData, ok := podInfoResp["pod_info"].([]interface{}); ok {
		// Response is: {"pod_info": [{"metric": ..., "value": ...}, ...]}
		resultArray = podInfoData
		slog.Debug("Found pod info with query name key", "count", len(podInfoData))
	} else if data, ok := podInfoResp["data"].([]interface{}); ok {
		// Response is: {"data": [{"metric": ..., "value": ...}, ...]}
		resultArray = data
		slog.Debug("Found pod info in data array", "count", len(data))
	} else if data, ok := podInfoResp["data"].(map[string]interface{}); ok {
		// Response is: {"data": {"pod_info": {"result": [...]}}}
		if podInfoData, ok := data["pod_info"].(map[string]interface{}); ok {
			if result, ok := podInfoData["result"].([]interface{}); ok {
				resultArray = result
				slog.Debug("Found pod info in nested structure", "count", len(result))
			}
		}
	}

	if len(resultArray) == 0 {
		slog.Warn("No pod info results found in Prometheus response",
			"lb_name", lbNode.Properties["name"],
			"target_ips", len(uniqueIPs))
	}

	// Collect all ReplicaSets we need to query for owners
	replicaSetsToQuery := make(map[string]bool) // key: "namespace/replicaset-name"
	podMetrics := make([]map[string]interface{}, 0)

	for _, item := range resultArray {
		if pod, ok := item.(map[string]interface{}); ok {
			if metric, ok := pod["metric"].(map[string]interface{}); ok {
				podMetrics = append(podMetrics, metric)

				// If created by ReplicaSet, we'll need to query for its owner (Deployment)
				createdByKind, _ := metric["created_by_kind"].(string)
				createdByName, _ := metric["created_by_name"].(string)
				namespace, _ := metric["namespace"].(string)

				if createdByKind == "ReplicaSet" && createdByName != "" && namespace != "" {
					replicaSetsToQuery[fmt.Sprintf("%s/%s", namespace, createdByName)] = true
				}
			}
		}
	}

	// Step 5: Query kube_replicaset_owner to get Deployment owners for ReplicaSets
	replicaSetOwners := make(map[string]map[string]string) // key: "namespace/replicaset" -> {"kind": "Deployment", "name": "xxx"}
	if len(replicaSetsToQuery) > 0 {
		rsQueries := map[string]string{
			"rs_owner": "kube_replicaset_owner",
		}

		rsResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, rsQueries, true)
		if err == nil {
			var rsResultArray []interface{}
			if rsData, ok := rsResp["rs_owner"].([]interface{}); ok {
				rsResultArray = rsData
			} else if data, ok := rsResp["data"].([]interface{}); ok {
				rsResultArray = data
			} else if data, ok := rsResp["data"].(map[string]interface{}); ok {
				if rsData, ok := data["rs_owner"].(map[string]interface{}); ok {
					if result, ok := rsData["result"].([]interface{}); ok {
						rsResultArray = result
					}
				}
			}

			for _, item := range rsResultArray {
				if rs, ok := item.(map[string]interface{}); ok {
					if metric, ok := rs["metric"].(map[string]interface{}); ok {
						rsNamespace, _ := metric["namespace"].(string)
						rsName, _ := metric["replicaset"].(string)
						ownerKind, _ := metric["owner_kind"].(string)
						ownerName, _ := metric["owner_name"].(string)

						if rsNamespace != "" && rsName != "" {
							key := fmt.Sprintf("%s/%s", rsNamespace, rsName)
							replicaSetOwners[key] = map[string]string{
								"kind": ownerKind,
								"name": ownerName,
							}
						}
					}
				}
			}
		}
	}

	// Step 6: Process all pod metrics and create owner nodes
	for _, metric := range podMetrics {
		podIP, _ := metric["pod_ip"].(string)
		podName, _ := metric["pod"].(string)
		namespace, _ := metric["namespace"].(string)
		k8sCluster, _ := metric["k8s_cluster"].(string)
		createdByKind, _ := metric["created_by_kind"].(string)
		createdByName, _ := metric["created_by_name"].(string)

		if podName == "" || namespace == "" {
			continue
		}

		// Determine the actual owner (resolve ReplicaSet -> Deployment)
		ownerKind := createdByKind
		ownerName := createdByName

		if createdByKind == "ReplicaSet" && createdByName != "" {
			rsKey := fmt.Sprintf("%s/%s", namespace, createdByName)
			if owner, found := replicaSetOwners[rsKey]; found && owner["kind"] != "" {
				ownerKind = owner["kind"]
				ownerName = owner["name"]
			} else {
				// Fallback: extract deployment name from ReplicaSet name
				// ReplicaSet pattern: {deployment-name}-{hash}
				ownerName = core.ExtractDeploymentFromReplicaSet(createdByName)
				ownerKind = "Deployment"
			}
		}

		// If no owner info, skip this pod
		if ownerKind == "" || ownerName == "" {
			slog.Debug("Skipping pod without owner info",
				"pod_name", podName,
				"namespace", namespace)
			continue
		}

		// Try to find matching Service node using owner name
		var targetNode *core.DbNode
		var targetNodeType = core.NodeTypePod // Default to Pod owner

		// Try different combinations to find matching service
		serviceKeys := []string{
			fmt.Sprintf("%s:%s:%s", ownerName, namespace, k8sCluster),
			fmt.Sprintf("%s:%s:", ownerName, namespace),
			fmt.Sprintf("%s::%s", ownerName, k8sCluster),
			fmt.Sprintf("%s::", ownerName),
		}

		for _, key := range serviceKeys {
			if svcNode, found := serviceNodeMap[key]; found {
				targetNode = svcNode
				targetNodeType = core.NodeTypeService
				slog.Info("Found matching Service for LoadBalancer target",
					"lb_name", lbNode.Properties["name"],
					"pod_name", podName,
					"owner_name", ownerName,
					"service_key", key)
				break
			}
		}

		// If no Service match found, create owner node (Deployment/StatefulSet/DaemonSet)
		if targetNode == nil {
			// Create unique key for the owner: namespace:kind:name
			ownerKey := fmt.Sprintf("%s:%s:%s", namespace, ownerKind, ownerName)

			// Check if we already created this owner node
			if existingOwner, found := ownerNodeMap[ownerKey]; found {
				// Add this pod to the existing owner's pod list
				if pods, ok := existingOwner.Properties["pods"].([]string); ok {
					existingOwner.Properties["pods"] = append(pods, podName)
				}
				targetNode = existingOwner
			} else {
				// Create new owner node with ID format: namespace:kind:name
				// Preserve all metric labels from Prometheus first
				labels := make(map[string]string)
				for k, v := range metric {
					if strVal, ok := v.(string); ok {
						labels[k] = strVal
					}
				}

				// Build properties with standard fields extracted from labels
				properties := map[string]any{
					"name":       ownerName,
					"namespace":  namespace,
					"owner_kind": ownerKind,
					"pods":       []string{podName},
				}

				// Extract commonly-used K8s fields from labels to top-level for easy access
				// This standardizes access pattern: check top-level first, fall back to labels
				if k8sCluster != "" {
					properties["k8s_cluster"] = k8sCluster
				} else if cluster, ok := labels["k8s_cluster"]; ok && cluster != "" {
					properties["k8s_cluster"] = cluster
				}

				if node, ok := labels["node"]; ok && node != "" {
					properties["node"] = node
				}

				if hostIP, ok := labels["host_ip"]; ok && hostIP != "" {
					properties["host_ip"] = hostIP
				}

				// Store all labels for full context
				if len(labels) > 0 {
					properties["labels"] = labels
				}

				// Create target node using core.NewNode() with proper unique key
				targetKeyComponents := core.NewUniqueKeyComponents(core.CloudProviderK8s, core.NodeTypePod)
				targetKeyComponents.Account = k8sAccountID
				targetKeyComponents.Hierarchy = namespace
				targetKeyComponents.Name = fmt.Sprintf("%s:%s", ownerKind, ownerName)
				targetNode = core.NewNode(core.NodeTypePod, targetKeyComponents.Build(), properties, tenantID, k8sAccountID, "cloud")
				ownerNodeMap[ownerKey] = targetNode
				podNodes = append(podNodes, targetNode)
			}
		}

		// Create edge: LoadBalancer -> Service/Owner
		edge := &core.DbEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.ID,
			DestinationNodeID: targetNode.ID,
			RelationshipType:  core.RelationshipRoutesTo,
			Properties: map[string]any{
				"discovered_from": "aws_target_health",
				"target_ip":       podIP,
				"pod_name":        podName,
				"owner_kind":      ownerKind,
				"owner_name":      ownerName,
			},
			CloudAccountID: awsAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		edges = append(edges, edge)

		slog.Info("Linked LoadBalancer to target",
			"lb_name", lbNode.Properties["name"],
			"target_type", targetNodeType,
			"target_name", targetNode.Properties["name"],
			"pod_name", podName,
			"owner_kind", ownerKind,
			"owner_name", ownerName,
			"namespace", namespace,
			"pod_ip", podIP)
	}

	slog.Info("LoadBalancer target enrichment completed",
		"lb_name", lbNode.Properties["name"],
		"target_ips", len(uniqueIPs),
		"pods_discovered", len(podNodes))

	return podNodes, edges, nil
}
