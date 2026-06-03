package traces

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"os"
	"strings"
	"testing"
	"time"
)

func TestKnowledgeGraphExtraction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	testAccountID := os.Getenv("TEST_ACCOUNT")
	testTenantID := os.Getenv("TEST_TENANT")

	ctxt := security.NewRequestContextForTenantAdmin(testTenantID, nil, nil, nil)

	nodes, edges, err := BuildKnowledgeGraphFromTraces(ctxt, "relay-server", testAccountID, testTenantID)
	if err != nil {
		t.Fatalf("Error building knowledge graph: %v", err)
	}

	if len(nodes) == 0 {
		t.Error("Expected non-zero nodes, got 0")
	}
	if len(edges) == 0 {
		t.Error("Expected non-zero edges, got 0")
	}
}

func TestRefreshKnowledgeGraph(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)

	tenant := os.Getenv("TEST_TENANT")
	request := RefreshKnowledgeGraphRequest{
		CloudAccountID: os.Getenv("TEST_ACCOUNT"),
		TenantID:       tenant,
		ForceRefresh:   true,
	}
	_, err := RefreshKnowledgeGraph(ctxt.GetContext(), request)
	if err != nil {
		t.Fatalf("Error refreshing knowledge graph: %v", err)
	}
}

func TestFetchServieMapUsingKG(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)

	tenant := os.Getenv("TEST_TENANT")
	accountID := os.Getenv("TEST_ACCOUNT")
	service, err := NewKnowledgeGraphService()
	if err != nil {
		t.Fatalf("Error creating knowledge graph service: %v", err)
	}
	serviceMap, err := service.BuildServiceMapFromKnowledgeGraph(ctxt, accountID, tenant, nil, nil)
	if err != nil {
		t.Fatalf("Error fetching service map: %v", err)
	}
	// print as json string
	jsonStr, err := json.Marshal(serviceMap)
	if err != nil {
		t.Fatalf("Error marshaling service map to JSON: %v", err)
	}
	t.Logf("Service Map: %s", jsonStr)
}

func TestKnowledgeGraph(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)

	tenant := os.Getenv("TEST_TENANT")
	accountID := os.Getenv("TEST_ACCOUNT")

	// Step 1: Build knowledge graph from traces
	t.Log("Step 1: Building knowledge graph from traces for tracking-service...")
	nodes, edges, err := BuildKnowledgeGraphFromTraces(ctxt, "tracking-service", accountID, tenant)

	if err != nil {
		t.Fatalf("Error building knowledge graph: %v", err)
	}

	if len(nodes) == 0 {
		t.Error("Expected non-zero nodes, got 0")
	}
	if len(edges) == 0 {
		t.Error("Expected non-zero edges, got 0")
	}

	t.Logf("✅ Knowledge graph built: %d nodes, %d edges", len(nodes), len(edges))

	// Store knowledge graph for manual verification
	graphData := map[string]any{
		"nodes": nodes,
		"edges": edges,
	}
	jsonBytes, _ := json.MarshalIndent(graphData, "", "  ")
	err = os.WriteFile("knowledge_graph_test_output.json", jsonBytes, 0644)
	if err != nil {
		t.Fatalf("Error writing knowledge graph to file: %v", err)
	}

	// Step 2: Convert KG back to Service Map using the refactored helper
	t.Log("Step 2: Converting knowledge graph back to service map...")
	kgService, err := NewKnowledgeGraphService()
	if err != nil {
		t.Fatalf("Error creating KG service: %v", err)
	}

	serviceMapFromKG, err := kgService.BuildServiceMapFromNodes(nodes, edges)
	if err != nil {
		t.Fatalf("Error building service map from KG: %v", err)
	}

	t.Logf("✅ Service map built from KG: %d applications", len(serviceMapFromKG.Applications))

	// Store service map for verification
	serviceMapBytes, _ := json.MarshalIndent(serviceMapFromKG, "", "  ")
	err = os.WriteFile("kg_service_map_round_trip_output.json", serviceMapBytes, 0644)
	if err != nil {
		t.Fatalf("Error writing service map from KG: %v", err)
	}

	// Step 3: Validate service names are correct
	t.Log("Step 3: Validating service names match between unique_key and properties...")
	errors := 0
	validatedServices := 0

	for _, node := range nodes {
		if node.NodeType != NodeTypeService && node.NodeType != NodeTypeExternalService {
			continue
		}

		serviceName, ok := node.Properties["name"].(string)
		if !ok {
			t.Errorf("❌ Node %s has no valid name property", node.UniqueKey)
			errors++
			continue
		}

		// Extract expected service name from unique_key
		// Format: "NodeType:service-name:environment"
		parts := strings.Split(node.UniqueKey, ":")
		if len(parts) < 2 {
			t.Errorf("❌ Invalid unique_key format: %s", node.UniqueKey)
			errors++
			continue
		}
		expectedName := parts[1]

		if serviceName != expectedName {
			t.Errorf("❌ Service name mismatch! UniqueKey='%s' has service '%s' but properties.name='%s'",
				node.UniqueKey, expectedName, serviceName)
			errors++
		} else {
			t.Logf("✅ Service name correct: %s", serviceName)
			validatedServices++
		}
	}

	if errors > 0 {
		t.Fatalf("❌ Found %d service name validation errors", errors)
	}

	t.Logf("✅ All %d service nodes validated successfully", validatedServices)
	t.Log("✅ Test completed successfully!")
	t.Log("📁 Output files:")
	t.Log("   - knowledge_graph_test_output.json")
	t.Log("   - kg_service_map_round_trip_output.json")
}

func TestCloudLBResolve(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, "TEST_AWS_ACCOUNT", "TEST_K8S_ACCOUNT")
	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)

	// IMPORTANT: AWS and K8s use different account IDs
	awsAccountID := env["TEST_AWS_ACCOUNT"] // For cloud.ExecuteCli (LoadBalancer queries)
	k8sAccountID := env["TEST_K8S_ACCOUNT"] // For relay.ExecutePrometheus (kube_pod_info queries)

	lbDNS := "internal-staging-tracking-internal-lb-1187205550.us-east-1.elb.amazonaws.com"
	region := "us-east-1"

	t.Logf("AWS Account ID (for LoadBalancer): %s", awsAccountID)
	t.Logf("K8s Account ID (for kube_pod_info): %s", k8sAccountID)

	// Step 1: Get LoadBalancer ARN by DNS name
	t.Log("Step 1: Getting LoadBalancer ARN...")
	arnCommand := fmt.Sprintf(
		"aws elbv2 describe-load-balancers --region %s --query 'LoadBalancers[?DNSName==`%s`].LoadBalancerArn' --output text",
		region, lbDNS,
	)
	t.Logf("Executing command: %s", arnCommand)
	arnResp, err := cloud.ExecuteCli(ctxt, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   arnCommand,
	})
	if err != nil {
		// Skip test if cloud collector is not available
		if strings.Contains(err.Error(), "cloud collector server url not set") ||
			strings.Contains(err.Error(), "unable to access cloud server") ||
			strings.Contains(err.Error(), "unexpected end of JSON input") {
			t.Skipf("Skipping test: cloud collector not available - %v", err)
		}
		t.Fatalf("Error getting LoadBalancer ARN: %v", err)
	}
	t.Logf("LoadBalancer ARN Response (full): %+v", arnResp)

	// Check if response has error field
	if errMsg, ok := arnResp["error"]; ok {
		t.Fatalf("Cloud CLI returned error: %v", errMsg)
	}

	// Extract ARN from response data
	var lbArn string
	if data, ok := arnResp["data"].(string); ok {
		lbArn = strings.TrimSpace(data)
		t.Logf("LoadBalancer ARN: %s", lbArn)
	} else {
		t.Fatalf("Could not extract ARN from response: %+v", arnResp)
	}

	if lbArn == "" {
		t.Fatalf("LoadBalancer ARN is empty")
	}

	// Step 2: Get target groups for the LoadBalancer
	t.Log("\nStep 2: Getting target groups...")
	tgCommand := fmt.Sprintf(
		"aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json",
		region, lbArn,
	)
	tgResp, err := cloud.ExecuteCli(ctxt, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tgCommand,
	})
	if err != nil {
		t.Fatalf("Error getting target groups: %v", err)
	}
	t.Logf("Target Groups Response: %+v", tgResp)

	// Parse target groups
	var targetGroups []map[string]interface{}
	if data, ok := tgResp["data"].(string); ok {
		var tgData struct {
			TargetGroups []map[string]interface{} `json:"TargetGroups"`
		}
		if err := json.Unmarshal([]byte(data), &tgData); err != nil {
			t.Fatalf("Error parsing target groups: %v", err)
		}
		targetGroups = tgData.TargetGroups
		t.Logf("Found %d target groups", len(targetGroups))
	}

	// Step 3: Get target health for each target group and collect IPs
	uniqueIPs := make(map[string]bool)

	for i, tg := range targetGroups {
		tgArn := tg["TargetGroupArn"].(string)
		tgName := tg["TargetGroupName"].(string)
		t.Logf("\nStep 3.%d: Getting target health for target group: %s", i+1, tgName)

		healthCommand := fmt.Sprintf(
			"aws elbv2 describe-target-health --region %s --target-group-arn %s --output json",
			region, tgArn,
		)
		healthResp, err := cloud.ExecuteCli(ctxt, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   healthCommand,
		})
		if err != nil {
			t.Errorf("Error getting target health for %s: %v", tgName, err)
			continue
		}

		// Parse target health
		if data, ok := healthResp["data"].(string); ok {
			var healthData struct {
				TargetHealthDescriptions []map[string]interface{} `json:"TargetHealthDescriptions"`
			}
			if err := json.Unmarshal([]byte(data), &healthData); err != nil {
				t.Errorf("Error parsing target health: %v", err)
				continue
			}
			t.Logf("Found %d targets in target group %s:", len(healthData.TargetHealthDescriptions), tgName)
			for j, target := range healthData.TargetHealthDescriptions {
				t.Logf("  Target %d: %+v", j+1, target)

				// Extract IP address from target
				if targetInfo, ok := target["Target"].(map[string]interface{}); ok {
					if ip, ok := targetInfo["Id"].(string); ok {
						uniqueIPs[ip] = true
					}
				}
			}
		}
	}

	// Step 4: Map target IPs to K8s pods using kube_pod_info metric
	if len(uniqueIPs) > 0 {
		t.Log("\nStep 4: Mapping target IPs to K8s pods...")

		// Build IP filter for PromQL query
		ipList := []string{}
		for ip := range uniqueIPs {
			ipList = append(ipList, ip)
		}
		ipFilter := strings.Join(ipList, "|")
		t.Logf("Querying kube_pod_info for IPs: %s", ipFilter)

		// Query Prometheus for pod info
		queries := map[string]string{
			"pod_info": fmt.Sprintf(`kube_pod_info{pod_ip=~"%s"}`, ipFilter),
		}

		endTime := time.Now()
		startTime := endTime.Add(-5 * time.Minute)

		podInfoResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
		if err != nil {
			t.Logf("⚠️  Warning: Could not query kube_pod_info: %v", err)
		} else {
			t.Logf("Pod info response: %+v", podInfoResp)

			// Parse pod info results
			if data, ok := podInfoResp["data"].(map[string]interface{}); ok {
				if podInfoData, ok := data["pod_info"].(map[string]interface{}); ok {
					if result, ok := podInfoData["result"].([]interface{}); ok {
						t.Logf("\nFound %d pods matching the target IPs:", len(result))
						for _, item := range result {
							if pod, ok := item.(map[string]interface{}); ok {
								if metric, ok := pod["metric"].(map[string]interface{}); ok {
									podIP := metric["pod_ip"]
									podName := metric["pod"]
									namespace := metric["namespace"]
									t.Logf("  • Pod IP: %s → Pod: %s (Namespace: %s)", podIP, podName, namespace)
								}
							}
						}
					}
				}
			}
		}
	}

	t.Log("\n✅ LoadBalancer resolution completed successfully")
}

func TestCloudResolveDNS(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, "TEST_AWS_ACCOUNT")
	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)

	// IMPORTANT: AWS and K8s use different account IDs
	awsAccountID := env["TEST_AWS_ACCOUNT"] // For cloud.ExecuteCli (LoadBalancer queries)

	// Test DNS hostname to resolve
	testHostname := "staging-redis.example.internal"
	t.Logf("Testing DNS resolution for: %s", testHostname)

	// Step 1: List hosted zones to find the matching zone
	t.Log("Step 1: Listing hosted zones...")
	zonesCommand := "aws route53 list-hosted-zones-by-name --output json"
	zonesResp, err := cloud.ExecuteCli(ctxt, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   zonesCommand,
	})
	if err != nil {
		t.Fatalf("Error listing hosted zones: %v", err)
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
			t.Fatalf("Failed to parse hosted zones: %v", err)
		}
	} else {
		t.Fatal("No data in hosted zones response")
	}

	t.Logf("Found %d hosted zones", len(hostedZones.HostedZones))

	// Step 2: Find the zone matching our hostname (e.g., example.internal)
	var matchingZoneId string
	var matchingZoneName string
	for _, zone := range hostedZones.HostedZones {
		// Remove trailing dot from zone name for comparison
		zoneName := strings.TrimSuffix(zone.Name, ".")
		// Check if hostname ends with this zone name
		if strings.HasSuffix(testHostname, zoneName) {
			matchingZoneId = zone.Id
			matchingZoneName = zone.Name
			t.Logf("Found matching zone: %s (ID: %s, Private: %v)",
				zone.Name, zone.Id, zone.Config.PrivateZone)
			break
		}
	}

	if matchingZoneId == "" {
		t.Fatalf("No hosted zone found matching hostname: %s", testHostname)
	}

	// Step 3: Query DNS records for the specific hostname
	t.Logf("Step 2: Querying DNS records in zone %s...", matchingZoneName)

	// Route 53 expects DNS names with trailing dot
	dnsNameWithDot := testHostname + "."
	recordsCommand := fmt.Sprintf(
		"aws route53 list-resource-record-sets --hosted-zone-id %s --output json",
		matchingZoneId,
	)

	recordsResp, err := cloud.ExecuteCli(ctxt, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   recordsCommand,
	})
	if err != nil {
		t.Fatalf("Error querying DNS records: %v", err)
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
			t.Fatalf("Failed to parse DNS records: %v", err)
		}
	} else {
		t.Fatal("No data in DNS records response")
	}

	t.Logf("Found %d DNS records in zone", len(recordSets.ResourceRecordSets))

	// Step 4: Find records matching our hostname
	t.Logf("Step 3: Analyzing records for %s...", testHostname)
	var foundRecord bool
	var resolvedIPs []string
	var aliasTarget string
	var cnameTarget string

	for _, record := range recordSets.ResourceRecordSets {
		if record.Name == dnsNameWithDot {
			foundRecord = true
			t.Logf("Found DNS record: Type=%s, TTL=%d", record.Type, record.TTL)
			t.Logf("  ResourceRecords count: %d", len(record.ResourceRecords))

			switch record.Type {
			case "A", "AAAA":
				// A/AAAA records contain IP addresses
				for _, rr := range record.ResourceRecords {
					resolvedIPs = append(resolvedIPs, rr.Value)
					t.Logf("  → IP: %s", rr.Value)
				}

			case "CNAME":
				// CNAME points to another DNS name
				if len(record.ResourceRecords) > 0 {
					cnameTarget = strings.TrimSuffix(record.ResourceRecords[0].Value, ".")
					t.Logf("  → CNAME: %s", cnameTarget)

					// Check what type of resource this is
					if strings.Contains(cnameTarget, ".elb.amazonaws.com") {
						t.Logf("  ✓ This is an Elastic Load Balancer")
					} else if strings.Contains(cnameTarget, ".cache.amazonaws.com") {
						t.Logf("  ✓ This is an ElastiCache cluster")
					} else if strings.Contains(cnameTarget, ".rds.amazonaws.com") {
						t.Logf("  ✓ This is an RDS instance")
					} else {
						t.Logf("  → Other AWS/external service")
					}
				} else {
					t.Logf("  ⚠️  CNAME record has no ResourceRecords!")
				}

			case "ALIAS":
				// Alias record points to AWS resource
				if record.AliasTarget.DNSName != "" {
					aliasTarget = strings.TrimSuffix(record.AliasTarget.DNSName, ".")
					t.Logf("  → Alias Target: %s", aliasTarget)
					t.Logf("  → Alias Zone ID: %s", record.AliasTarget.HostedZoneId)

					// Check if it's an ELB
					if strings.Contains(aliasTarget, ".elb.amazonaws.com") {
						t.Logf("  Note: This is an Elastic Load Balancer - can map to targets")
					}
				}
			}
		}
	}

	if !foundRecord {
		t.Logf("Warning: No DNS record found for %s", testHostname)
		t.Logf("Available records in zone:")
		for i, record := range recordSets.ResourceRecordSets {
			if i < 10 { // Show first 10 records
				t.Logf("  - %s (%s)", record.Name, record.Type)
			}
		}
	}

	// Step 5: Summary of what we found
	t.Log("\n=== DNS Resolution Summary ===")
	t.Logf("Hostname: %s", testHostname)
	t.Logf("Hosted Zone: %s", matchingZoneName)
	t.Logf("Found Record: %v", foundRecord)

	if len(resolvedIPs) > 0 {
		t.Logf("✓ Resolved to IPs: %v", resolvedIPs)
		t.Log("✓ Can map these IPs to pods using kube_pod_info (similar to LoadBalancer enrichment)")
	}

	if cnameTarget != "" {
		t.Logf("✓ CNAME points to: %s", cnameTarget)
		if strings.Contains(cnameTarget, ".elb.amazonaws.com") {
			t.Log("✓ Can query LoadBalancer ARN and map to pods")
		} else if strings.Contains(cnameTarget, ".cache.amazonaws.com") {
			t.Log("✓ Can create ElastiCache node")
		}
	}

	if aliasTarget != "" {
		t.Logf("✓ Alias points to: %s", aliasTarget)
		if strings.Contains(aliasTarget, ".elb.amazonaws.com") {
			t.Log("✓ Can extract LoadBalancer ARN and map to pods")
		}
	}

	if !foundRecord {
		t.Log("✗ DNS record not found - cannot resolve upstream")
	}

	// Verify we have enough data for upstream mapping
	if len(resolvedIPs) > 0 || aliasTarget != "" || cnameTarget != "" {
		t.Log("\n✓ DNS resolution is VIABLE - we can map DNS names to upstreams")
	} else {
		t.Log("\n✗ DNS resolution NOT viable - no IP or alias data found")
	}
}

func TestGetAWSAccountsForTenant(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	if tenantID == "" {
		t.Skip("TEST_TENANT environment variable not set")
	}

	t.Logf("Testing GetAWSAccountsForTenant for tenant: %s", tenantID)

	accounts, err := GetAWSAccountsForTenant(tenantID)

	if err != nil {
		t.Fatalf("❌ Error getting AWS accounts: %v", err)
	}

	t.Logf("\n=== AWS Accounts for Tenant ===")
	t.Logf("✓ Found %d AWS accounts", len(accounts))

	for i, acc := range accounts {
		t.Logf("  [%d] %s", i+1, acc)
	}

	if len(accounts) == 0 {
		t.Log("\n⚠️  No AWS accounts found")
		t.Log("   DNS enrichment will be skipped for this tenant")
		t.Log("   To enable DNS enrichment:")
		t.Log("   1. Add an AWS cloud account in the Nudgebee UI")
		t.Log("   2. Ensure cloud_provider = 'AWS' and status = 'active'")
	} else {
		t.Log("\n✅ AWS accounts found - DNS enrichment will be enabled")
	}
}

func TestResolveStagingInternalCache(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, "TEST_AWS_ACCOUNT")
	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)
	awsAccountID := env["TEST_AWS_ACCOUNT"]

	// Test the specific hostname from the debug output
	testHostname := "staging-internal-cache.example.internal"
	t.Logf("Testing DNS resolution for: %s", testHostname)

	// List hosted zones
	zonesCommand := "aws route53 list-hosted-zones-by-name --output json"
	zonesResp, err := cloud.ExecuteCli(ctxt, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   zonesCommand,
	})
	if err != nil {
		t.Fatalf("Error listing zones: %v", err)
	}

	var hostedZones struct {
		HostedZones []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"HostedZones"`
	}

	if data, ok := zonesResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &hostedZones); err != nil {
			t.Fatalf("Failed to parse zones: %v", err)
		}
	}

	// Find zone
	var matchingZoneId string
	for _, zone := range hostedZones.HostedZones {
		zoneName := strings.TrimSuffix(zone.Name, ".")
		if strings.HasSuffix(testHostname, zoneName) {
			matchingZoneId = zone.Id
			t.Logf("Found zone: %s (ID: %s)", zone.Name, zone.Id)
			break
		}
	}

	if matchingZoneId == "" {
		t.Fatalf("No zone found for %s", testHostname)
	}

	// Query DNS records
	recordsCommand := fmt.Sprintf(
		"aws route53 list-resource-record-sets --hosted-zone-id %s --output json",
		matchingZoneId,
	)

	recordsResp, err := cloud.ExecuteCli(ctxt, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   recordsCommand,
	})
	if err != nil {
		t.Fatalf("Error querying records: %v", err)
	}

	var recordSets struct {
		ResourceRecordSets []struct {
			Name            string `json:"Name"`
			Type            string `json:"Type"`
			TTL             int    `json:"TTL"`
			ResourceRecords []struct {
				Value string `json:"Value"`
			} `json:"ResourceRecords"`
		} `json:"ResourceRecordSets"`
	}

	if data, ok := recordsResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &recordSets); err != nil {
			t.Fatalf("Failed to parse records: %v", err)
		}
	}

	// Find the specific record
	dnsNameWithDot := testHostname + "."
	for _, record := range recordSets.ResourceRecordSets {
		if record.Name == dnsNameWithDot {
			t.Logf("\n✅ Found record:")
			t.Logf("  Name: %s", record.Name)
			t.Logf("  Type: %s", record.Type)
			t.Logf("  TTL: %d", record.TTL)
			t.Logf("  ResourceRecords: %d items", len(record.ResourceRecords))

			for i, rr := range record.ResourceRecords {
				t.Logf("  [%d] Value: %s", i+1, rr.Value)

				// Analyze what this points to
				if strings.Contains(rr.Value, ".cache.amazonaws.com") {
					t.Logf("      → This is an ElastiCache endpoint!")
					t.Logf("      → Can create ElastiCache node in knowledge graph")
				} else if strings.Contains(rr.Value, ".elb.amazonaws.com") {
					t.Logf("      → This is an ELB endpoint!")
					t.Logf("      → Can query target groups and map to pods")
				} else if strings.Contains(rr.Value, ".rds.amazonaws.com") {
					t.Logf("      → This is an RDS endpoint!")
					t.Logf("      → Can create RDS node in knowledge graph")
				}
			}
			return
		}
	}

	t.Fatalf("Record not found for %s", testHostname)
}

func TestEnrichRoute53DNSWithTargets(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, "TEST_AWS_ACCOUNT", "TEST_K8S_ACCOUNT")
	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)

	// IMPORTANT: AWS and K8s use different account IDs
	awsAccountID := env["TEST_AWS_ACCOUNT"] // For cloud.ExecuteCli
	k8sAccountID := env["TEST_K8S_ACCOUNT"] // For relay.ExecutePrometheus
	tenantID := os.Getenv("TEST_TENANT")

	// Test DNS hostname to resolve
	testHostname := "api-gateway-admin-eks-staging.example.internal"
	t.Logf("Testing Route53 DNS enrichment for: %s", testHostname)

	// Create some existing service nodes to match against
	existingNodes := []*KnowledgeGraphNode{
		{
			ID:        "service-1",
			NodeType:  NodeTypeService,
			UniqueKey: "Service:api-gateway:staging",
			Properties: map[string]interface{}{
				"name":      "api-gateway",
				"namespace": "staging",
				"cluster":   "eks-staging",
			},
		},
	}

	// Call EnrichRoute53DNSWithTargets
	nodes, edges, err := EnrichRoute53DNSWithTargets(
		ctxt,
		testHostname,
		existingNodes,
		awsAccountID,
		k8sAccountID,
		tenantID,
	)

	if err != nil {
		t.Fatalf("Error enriching DNS: %v", err)
	}

	t.Logf("\n=== Enrichment Results ===")
	t.Logf("Nodes added: %d", len(nodes))
	t.Logf("Edges added: %d", len(edges))

	// Log details of added nodes
	if len(nodes) > 0 {
		t.Log("\n--- Added Nodes ---")
		for i, node := range nodes {
			t.Logf("[%d] Type: %s, ID: %s", i+1, node.NodeType, node.ID)
			if name, ok := node.Properties["name"].(string); ok {
				t.Logf("    Name: %s", name)
			}
			if namespace, ok := node.Properties["namespace"].(string); ok {
				t.Logf("    Namespace: %s", namespace)
			}
			if ownerKind, ok := node.Properties["owner_kind"].(string); ok {
				t.Logf("    Owner Kind: %s", ownerKind)
			}
			if podIP, ok := node.Properties["pod_ip"].(string); ok {
				t.Logf("    Pod IP: %s", podIP)
			}
			if endpoint, ok := node.Properties["endpoint"].(string); ok {
				t.Logf("    Endpoint: %s", endpoint)
			}
		}
	}

	// Log details of added edges
	if len(edges) > 0 {
		t.Log("\n--- Added Edges ---")
		for i, edge := range edges {
			t.Logf("[%d] %s -> %s (Type: %s)",
				i+1, edge.SourceNodeID, edge.DestinationNodeID, edge.RelationshipType)
			if discoveredFrom, ok := edge.Properties["discovered_from"].(string); ok {
				t.Logf("    Discovered from: %s", discoveredFrom)
			}
		}
	}

	// Verify results
	if len(nodes) == 0 && len(edges) == 0 {
		t.Log("\n⚠️  No enrichment data returned - DNS may not resolve to pods in the cluster")
		t.Log("   This is not necessarily an error - it may mean:")
		t.Log("   1. DNS points to a service not currently running")
		t.Log("   2. DNS points to an external resource (ElastiCache, etc.)")
		t.Log("   3. DNS points to a load balancer with no active targets")
	} else {
		t.Log("\n✅ Route53 DNS enrichment SUCCESSFUL!")
		t.Logf("   Successfully mapped %s to %d pod/resource nodes", testHostname, len(nodes))
	}
}
