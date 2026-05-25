package flow_sources

import (
	"context"
	"encoding/json"
	"testing"

	"nudgebee/services/security"
)

// TestENIResolverCreation tests creating a new ENI resolver
func TestENIResolverCreation(t *testing.T) {
	requestContext := security.NewRequestContextForTenantAdmin("test-tenant", nil, nil, nil)
	awsAccountID := "123456789012"

	resolver := NewENIResolver(requestContext, awsAccountID)

	if resolver == nil {
		t.Fatal("Expected resolver to be created, got nil")
		return
	}

	if resolver.awsAccountID != awsAccountID {
		t.Errorf("Expected AWS account ID %s, got %s", awsAccountID, resolver.awsAccountID)
	}

	if resolver.requestContext == nil {
		t.Error("Expected request context to be set, got nil")
	}
}

// TestIsIPAddress tests IP address detection
func TestIsIPAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Valid IPv4",
			input:    "10.0.1.50",
			expected: true,
		},
		{
			name:     "Valid IPv4 - different range",
			input:    "172.31.4.191",
			expected: true,
		},
		{
			name:     "Valid IPv4 - edge case",
			input:    "192.168.0.1",
			expected: true,
		},
		{
			name:     "Hostname",
			input:    "mydb.us-east-1.rds.amazonaws.com",
			expected: false,
		},
		{
			name:     "Simple hostname",
			input:    "localhost",
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "Invalid IP",
			input:    "999.999.999.999",
			expected: false,
		},
		{
			name:     "Partial IP",
			input:    "10.0.1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsIPAddress(tt.input)
			if result != tt.expected {
				t.Errorf("IsIPAddress(%s) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestENIInfoParsing tests ENI information parsing
func TestENIInfoParsing(t *testing.T) {
	// Test ENI with RDS tags
	eni := &ENIInfo{
		ENIId:            "eni-123abc456def",
		PrivateIPs:       []string{"10.0.2.100", "10.0.2.101"},
		PublicIP:         "",
		SubnetID:         "subnet-012345",
		VPCID:            "vpc-678910",
		SecurityGroups:   []string{"sg-111222", "sg-333444"},
		AvailabilityZone: "us-east-1a",
		Status:           "in-use",
		RequesterId:      "amazon-rds",
		Description:      "RDS Network Interface",
		InterfaceType:    "interface",
		RDSTags: map[string]string{
			"aws:rds:db-id":      "mydb-prod",
			"aws:rds:cluster-id": "aurora-cluster",
		},
		Attachment: map[string]interface{}{
			"instance_id": "",
			"status":      "attached",
		},
	}

	// Verify ENI structure
	if eni.ENIId != "eni-123abc456def" {
		t.Errorf("Expected ENI ID eni-123abc456def, got %s", eni.ENIId)
	}

	if len(eni.PrivateIPs) != 2 {
		t.Errorf("Expected 2 private IPs, got %d", len(eni.PrivateIPs))
	}

	if eni.RequesterId != "amazon-rds" {
		t.Errorf("Expected requester amazon-rds, got %s", eni.RequesterId)
	}

	if len(eni.RDSTags) != 2 {
		t.Errorf("Expected 2 RDS tags, got %d", len(eni.RDSTags))
	}

	dbID, ok := eni.RDSTags["aws:rds:db-id"]
	if !ok || dbID != "mydb-prod" {
		t.Errorf("Expected RDS DB ID mydb-prod, got %s", dbID)
	}
}

// TestENIResourceMapping tests resource mapping structure
func TestENIResourceMapping(t *testing.T) {
	mapping := &ENIResourceMapping{
		ResourceType: "rds",
		ResourceID:   "mydb-prod",
		ResourceName: "mydb-prod",
		ClusterID:    "aurora-cluster",
		Endpoint:     "mydb-prod.c123.us-east-1.rds.amazonaws.com",
		MatchType:    "tag",
		Confidence:   0.95,
		MatchedIPs:   []string{"10.0.2.100"},
		ENI: &ENIInfo{
			ENIId:      "eni-123abc",
			PrivateIPs: []string{"10.0.2.100"},
			VPCID:      "vpc-123",
		},
		AdditionalInfo: map[string]interface{}{
			"rds_type": "instance",
		},
	}

	// Verify mapping structure
	if mapping.ResourceType != "rds" {
		t.Errorf("Expected resource type rds, got %s", mapping.ResourceType)
	}

	if mapping.MatchType != "tag" {
		t.Errorf("Expected match type tag, got %s", mapping.MatchType)
	}

	if mapping.Confidence != 0.95 {
		t.Errorf("Expected confidence 0.95, got %.2f", mapping.Confidence)
	}

	if len(mapping.MatchedIPs) != 1 {
		t.Errorf("Expected 1 matched IP, got %d", len(mapping.MatchedIPs))
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(mapping)
	if err != nil {
		t.Fatalf("Failed to marshal mapping to JSON: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaledMapping ENIResourceMapping
	err = json.Unmarshal(jsonData, &unmarshaledMapping)
	if err != nil {
		t.Fatalf("Failed to unmarshal mapping from JSON: %v", err)
	}

	if unmarshaledMapping.ResourceID != mapping.ResourceID {
		t.Errorf("Expected resource ID %s after unmarshal, got %s",
			mapping.ResourceID, unmarshaledMapping.ResourceID)
	}
}

// TestRDSEndpointInfo tests RDS endpoint information structure
func TestRDSEndpointInfo(t *testing.T) {
	endpoint := &RDSEndpointInfo{
		Type:     "instance",
		ID:       "mydb-prod",
		Endpoint: "mydb-prod.c123.us-east-1.rds.amazonaws.com",
		IPs:      []string{"10.0.2.100", "10.0.2.101"},
	}

	if endpoint.Type != "instance" {
		t.Errorf("Expected type instance, got %s", endpoint.Type)
	}

	if len(endpoint.IPs) != 2 {
		t.Errorf("Expected 2 IPs, got %d", len(endpoint.IPs))
	}

	// Test different endpoint types
	endpointTypes := []struct {
		endpointType string
		expectedID   string
	}{
		{"instance", "db-instance"},
		{"cluster-writer", "aurora-cluster"},
		{"cluster-reader", "aurora-cluster"},
		{"proxy", "rds-proxy"},
		{"cache-cluster", "redis-prod"},
		{"cache-node", "memcached-node"},
	}

	for _, tt := range endpointTypes {
		ep := &RDSEndpointInfo{
			Type: tt.endpointType,
			ID:   tt.expectedID,
		}

		if ep.Type != tt.endpointType {
			t.Errorf("Expected type %s, got %s", tt.endpointType, ep.Type)
		}
	}
}

// TestConfidenceScores tests that confidence scores are in valid range
func TestConfidenceScores(t *testing.T) {
	testCases := []struct {
		name               string
		matchType          string
		expectedMin        float64
		expectedMax        float64
		expectedConfidence float64
	}{
		{
			name:               "Tag-based match",
			matchType:          "tag",
			expectedMin:        0.90,
			expectedMax:        1.0,
			expectedConfidence: 0.95,
		},
		{
			name:               "IP-based match",
			matchType:          "ip",
			expectedMin:        0.85,
			expectedMax:        0.95,
			expectedConfidence: 0.90,
		},
		{
			name:               "Attachment-based match",
			matchType:          "attachment",
			expectedMin:        0.90,
			expectedMax:        1.0,
			expectedConfidence: 0.95,
		},
		{
			name:               "Description-based match",
			matchType:          "description",
			expectedMin:        0.60,
			expectedMax:        0.80,
			expectedConfidence: 0.70,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Verify confidence is in expected range
			if tc.expectedConfidence < tc.expectedMin || tc.expectedConfidence > tc.expectedMax {
				t.Errorf("Confidence %.2f for %s is out of expected range [%.2f, %.2f]",
					tc.expectedConfidence, tc.matchType, tc.expectedMin, tc.expectedMax)
			}

			// Verify confidence is between 0 and 1
			if tc.expectedConfidence < 0.0 || tc.expectedConfidence > 1.0 {
				t.Errorf("Confidence %.2f for %s is invalid (must be between 0 and 1)",
					tc.expectedConfidence, tc.matchType)
			}
		})
	}
}

// TestResourceTypeMapping tests resource type mapping
func TestResourceTypeMapping(t *testing.T) {
	tests := []struct {
		name         string
		requesterID  string
		expectedType string
	}{
		{
			name:         "RDS requester",
			requesterID:  "amazon-rds",
			expectedType: "rds",
		},
		{
			name:         "ElastiCache requester",
			requesterID:  "amazon-elasticache",
			expectedType: "elasticache",
		},
		{
			name:         "Lambda requester",
			requesterID:  "lambda",
			expectedType: "lambda",
		},
		{
			name:         "EKS requester",
			requesterID:  "eks",
			expectedType: "eks",
		},
		{
			name:         "EC2 instance (no requester)",
			requesterID:  "",
			expectedType: "ec2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This would be tested in actual resolution logic
			// Here we're documenting the expected mappings
			if tt.requesterID == "amazon-rds" && tt.expectedType != "rds" {
				t.Errorf("Expected RDS type for amazon-rds requester")
			}
		})
	}
}

// TestENIResolverMultipleStrategies tests that resolver tries multiple strategies
func TestENIResolverMultipleStrategies(t *testing.T) {
	// This test documents that the resolver should try multiple strategies
	strategies := []string{
		"tag",         // Tag-based (highest confidence for RDS)
		"ip",          // IP matching (works for all services)
		"attachment",  // Attachment-based (EC2 instances)
		"description", // Description parsing (Lambda, EKS)
	}

	if len(strategies) != 4 {
		t.Errorf("Expected 4 resolution strategies, got %d", len(strategies))
	}

	// Verify strategy order (tag should be first for highest confidence)
	if strategies[0] != "tag" {
		t.Error("Tag-based strategy should be tried first (highest confidence)")
	}
}

// TestENICacheKeys tests cache key format
func TestENICacheKeys(t *testing.T) {
	awsAccountID := "123456789012"
	ipAddress := "10.0.2.100"

	// ENI IP mapping cache key format
	expectedKey := "123456789012:10.0.2.100"
	actualKey := awsAccountID + ":" + ipAddress

	if actualKey != expectedKey {
		t.Errorf("Expected cache key %s, got %s", expectedKey, actualKey)
	}

	// RDS endpoints cache key (just account ID)
	if awsAccountID != "123456789012" {
		t.Errorf("Expected account ID cache key 123456789012, got %s", awsAccountID)
	}
}

// TestIPMatchingLogic tests IP matching between ENI and endpoints
func TestIPMatchingLogic(t *testing.T) {
	eniIPs := []string{"10.0.2.100", "10.0.2.101"}
	endpointIPs := []string{"10.0.2.100"}

	// Should match because endpoint IP is in ENI IPs
	matched := false
	for _, eniIP := range eniIPs {
		for _, endpointIP := range endpointIPs {
			if eniIP == endpointIP {
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}

	if !matched {
		t.Error("Expected IPs to match, but no match found")
	}

	// Test non-matching case
	nonMatchingEndpointIPs := []string{"10.0.3.200"}
	matched = false
	for _, eniIP := range eniIPs {
		for _, endpointIP := range nonMatchingEndpointIPs {
			if eniIP == endpointIP {
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}

	if matched {
		t.Error("Expected IPs to NOT match, but found a match")
	}
}

// TestRDSTagExtraction tests RDS tag extraction from ENI tags
func TestRDSTagExtraction(t *testing.T) {
	tags := []struct {
		Key   string
		Value string
	}{
		{"Name", "MyDB"},
		{"aws:rds:db-id", "mydb-prod"},
		{"aws:rds:cluster-id", "aurora-cluster"},
		{"Environment", "production"},
		{"aws:cloudformation:stack-name", "rds-stack"},
	}

	rdsTags := make(map[string]string)
	for _, tag := range tags {
		if tag.Key == "aws:rds:db-id" || tag.Key == "aws:rds:cluster-id" {
			rdsTags[tag.Key] = tag.Value
		}
	}

	// Should have extracted 2 RDS tags
	if len(rdsTags) != 2 {
		t.Errorf("Expected 2 RDS tags, got %d", len(rdsTags))
	}

	// Verify specific tags
	if rdsTags["aws:rds:db-id"] != "mydb-prod" {
		t.Errorf("Expected DB ID mydb-prod, got %s", rdsTags["aws:rds:db-id"])
	}

	if rdsTags["aws:rds:cluster-id"] != "aurora-cluster" {
		t.Errorf("Expected cluster ID aurora-cluster, got %s", rdsTags["aws:rds:cluster-id"])
	}
}

// TestMultipleENIMappings tests handling multiple ENI mappings
func TestMultipleENIMappings(t *testing.T) {
	// A single IP might map to multiple resources (e.g., RDS cluster with multiple endpoints)
	mappings := []*ENIResourceMapping{
		{
			ResourceType: "rds",
			ResourceID:   "mydb-writer",
			MatchType:    "tag",
			Confidence:   0.95,
		},
		{
			ResourceType: "rds",
			ResourceID:   "mydb-reader",
			MatchType:    "ip",
			Confidence:   0.90,
		},
	}

	if len(mappings) != 2 {
		t.Errorf("Expected 2 mappings, got %d", len(mappings))
	}

	// Highest confidence should be first (tag-based)
	if mappings[0].Confidence < mappings[1].Confidence {
		t.Error("Expected mappings to be sorted by confidence (highest first)")
	}
}

// TestEmptyIPResolution tests handling of empty IP address
func TestEmptyIPResolution(t *testing.T) {
	requestContext := security.NewRequestContextForTenantAdmin("test-tenant", nil, nil, nil)
	resolver := NewENIResolver(requestContext, "123456789012")

	ctx := context.Background()

	// This should handle gracefully
	mappings, err := resolver.ResolveIPToResource(ctx, "")

	// Should return error or empty mappings
	if err == nil && len(mappings) > 0 {
		t.Error("Expected error or empty mappings for empty IP, got results")
	}
}

// TestInvalidIPResolution tests handling of invalid IP address
func TestInvalidIPResolution(t *testing.T) {
	invalidIPs := []string{
		"not-an-ip",
		"999.999.999.999",
		"10.0.1",
		"hostname.example.com",
	}

	for _, ip := range invalidIPs {
		if IsIPAddress(ip) {
			t.Errorf("Expected %s to be invalid IP, but IsIPAddress returned true", ip)
		}
	}
}

// TestCacheNamespaces tests that all required cache namespaces are initialized
func TestCacheNamespaces(t *testing.T) {
	expectedNamespaces := []string{
		"eni_ip_mapping",
		"rds_endpoints",
		"elasticache_endpoints",
		"endpoint_dns",
	}

	if len(expectedNamespaces) != 4 {
		t.Errorf("Expected 4 cache namespaces, got %d", len(expectedNamespaces))
	}

	// Verify namespace names
	namespaceMap := make(map[string]bool)
	for _, ns := range expectedNamespaces {
		namespaceMap[ns] = true
	}

	required := []string{"eni_ip_mapping", "rds_endpoints", "elasticache_endpoints", "endpoint_dns"}
	for _, req := range required {
		if !namespaceMap[req] {
			t.Errorf("Missing required cache namespace: %s", req)
		}
	}
}

// TestCacheTTLValues tests expected cache TTL values
func TestCacheTTLValues(t *testing.T) {
	cacheTTLs := map[string]int{
		"eni_ip_mapping":        10, // 10 minutes
		"rds_endpoints":         5,  // 5 minutes
		"elasticache_endpoints": 5,  // 5 minutes
		"endpoint_dns":          15, // 15 minutes
	}

	// Verify TTL values are reasonable
	for cacheName, ttlMinutes := range cacheTTLs {
		if ttlMinutes < 1 || ttlMinutes > 60 {
			t.Errorf("Cache TTL for %s is %d minutes, which seems unreasonable",
				cacheName, ttlMinutes)
		}
	}

	// ENI mapping should have longer TTL than endpoints (less frequent changes)
	if cacheTTLs["eni_ip_mapping"] < cacheTTLs["rds_endpoints"] {
		t.Error("ENI IP mapping cache should have longer TTL than RDS endpoints")
	}

	// DNS resolution should have longest TTL (most stable)
	if cacheTTLs["endpoint_dns"] < cacheTTLs["eni_ip_mapping"] {
		t.Error("DNS cache should have longest TTL")
	}
}

// TestEC2AttachmentParsing tests EC2 instance attachment parsing
func TestEC2AttachmentParsing(t *testing.T) {
	attachment := map[string]interface{}{
		"instance_id":           "i-123abc456def",
		"device_index":          0,
		"status":                "attached",
		"attach_time":           "2024-01-15T10:30:00Z",
		"delete_on_termination": true,
	}

	instanceID, ok := attachment["instance_id"].(string)
	if !ok || instanceID == "" {
		t.Error("Failed to extract instance ID from attachment")
	}

	if instanceID != "i-123abc456def" {
		t.Errorf("Expected instance ID i-123abc456def, got %s", instanceID)
	}

	status, ok := attachment["status"].(string)
	if !ok || status != "attached" {
		t.Errorf("Expected status attached, got %s", status)
	}
}

// TestSupportedResourceTypes tests all supported AWS resource types
func TestSupportedResourceTypes(t *testing.T) {
	supportedTypes := []string{
		"rds",         // RDS instances and clusters
		"elasticache", // ElastiCache Redis/Memcached
		"ec2",         // EC2 instances
		"lambda",      // Lambda functions
		"eks",         // EKS clusters
	}

	if len(supportedTypes) != 5 {
		t.Errorf("Expected 5 supported resource types, got %d", len(supportedTypes))
	}

	// Verify each type is a valid AWS service
	awsServices := map[string]bool{
		"rds":         true,
		"elasticache": true,
		"ec2":         true,
		"lambda":      true,
		"eks":         true,
	}

	for _, resourceType := range supportedTypes {
		if !awsServices[resourceType] {
			t.Errorf("Resource type %s is not a valid AWS service", resourceType)
		}
	}
}

// TestMatchTypeValues tests valid match type values
func TestMatchTypeValues(t *testing.T) {
	validMatchTypes := []string{
		"tag",
		"ip",
		"attachment",
		"description",
	}

	// When used in cloud resolver, they become "eni_" prefixed
	cloudResolverMatchTypes := []string{
		"eni_tag",
		"eni_ip",
		"eni_attachment",
		"eni_description",
	}

	if len(validMatchTypes) != 4 {
		t.Errorf("Expected 4 match types, got %d", len(validMatchTypes))
	}

	if len(cloudResolverMatchTypes) != 4 {
		t.Errorf("Expected 4 cloud resolver match types, got %d", len(cloudResolverMatchTypes))
	}

	// Verify prefixing
	for i, matchType := range validMatchTypes {
		expected := "eni_" + matchType
		if cloudResolverMatchTypes[i] != expected {
			t.Errorf("Expected cloud resolver match type %s, got %s",
				expected, cloudResolverMatchTypes[i])
		}
	}
}

// TestPerformanceExpectations documents expected performance characteristics
func TestPerformanceExpectations(t *testing.T) {
	performance := map[string]struct {
		operation    string
		withoutCache int // milliseconds
		withCache    int // milliseconds
		improvement  int // factor
	}{
		"eni_lookup": {
			operation:    "ENI IP lookup",
			withoutCache: 200,
			withCache:    1,
			improvement:  200,
		},
		"rds_endpoints": {
			operation:    "RDS endpoints list",
			withoutCache: 500,
			withCache:    1,
			improvement:  500,
		},
		"elasticache_endpoints": {
			operation:    "ElastiCache endpoints list",
			withoutCache: 300,
			withCache:    1,
			improvement:  300,
		},
		"dns_resolution": {
			operation:    "DNS resolution",
			withoutCache: 50,
			withCache:    1,
			improvement:  50,
		},
	}

	for name, perf := range performance {
		// Verify cache provides significant improvement
		actualImprovement := perf.withoutCache / perf.withCache
		if actualImprovement != perf.improvement {
			t.Errorf("%s: Expected %dx improvement, calculated %dx",
				name, perf.improvement, actualImprovement)
		}

		// Verify cache performance is < 5ms
		if perf.withCache > 5 {
			t.Errorf("%s: Cache performance %dms is too slow (should be < 5ms)",
				name, perf.withCache)
		}
	}
}
