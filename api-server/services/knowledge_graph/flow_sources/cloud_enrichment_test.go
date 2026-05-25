package flow_sources

import (
	"io"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

func TestClassifyAWSEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		endpoint     string
		wantType     core.NodeType
		wantResource string
	}{
		{"elasticache", "my-cluster.abc123.0001.use1.cache.amazonaws.com", core.NodeTypeCache, "elasticache_inferred"},
		{"rds", "mydb.abc123.us-east-1.rds.amazonaws.com", core.NodeTypeDatabase, "rds_inferred"},
		{"cloudfront", "d111111abcdef8.cloudfront.net", core.NodeTypeCDN, "cloudfront_inferred"},
		{"apigateway", "abcd1234.execute-api.us-east-1.amazonaws.com", core.NodeTypeAPIGateway, "apigateway_inferred"},
		{"lambda_url", "abcd1234.lambda-url.us-east-1.on.aws", core.NodeTypeServerlessFunction, "lambda_inferred"},
		{"dynamodb", "dynamodb.us-east-1.amazonaws.com", core.NodeTypeDatabase, "dynamodb_inferred"},
		{"sqs", "sqs.us-east-1.amazonaws.com", core.NodeTypeMessageQueue, "sqs_inferred"},
		{"sns", "sns.us-east-1.amazonaws.com", core.NodeTypeMessageQueue, "sns_inferred"},
		{"s3_virtual_global", "my-bucket.s3.amazonaws.com", core.NodeTypeStorage, "s3_inferred"},
		{"s3_virtual_regional", "my-bucket.s3.us-east-1.amazonaws.com", core.NodeTypeStorage, "s3_inferred"},
		{"s3_path_global", "s3.amazonaws.com", core.NodeTypeStorage, "s3_inferred"},
		{"s3_path_regional", "s3-us-east-1.amazonaws.com", core.NodeTypeStorage, "s3_inferred"},
		{"s3_website", "my-bucket.s3-website-us-east-1.amazonaws.com", core.NodeTypeStorage, "s3_inferred"},
		{"unknown", "example.com", core.NodeTypeExternalService, "aws_service_inferred"},
		{"case_insensitive", "MY-DB.ABC.US-EAST-1.RDS.AMAZONAWS.COM", core.NodeTypeDatabase, "rds_inferred"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotResource := classifyAWSEndpoint(tc.endpoint)
			if gotType != tc.wantType {
				t.Errorf("classifyAWSEndpoint(%q) NodeType = %q, want %q", tc.endpoint, gotType, tc.wantType)
			}
			if gotResource != tc.wantResource {
				t.Errorf("classifyAWSEndpoint(%q) resourceType = %q, want %q", tc.endpoint, gotResource, tc.wantResource)
			}
		})
	}
}

func TestDetermineCloudResourceNodeType(t *testing.T) {
	cases := []struct {
		name         string
		resourceType string
		wantType     core.NodeType
		wantOk       bool
	}{
		{"loadbalancer", "loadbalancer", core.NodeTypeLoadBalancer, true},
		{"loadbalancer_mixed_case", "AWS::ElasticLoadBalancingV2::LoadBalancer", core.NodeTypeLoadBalancer, true},
		{"rds", "rds", core.NodeTypeDatabase, true},
		{"db_exact", "db", core.NodeTypeDatabase, true},
		{"elasticache", "elasticache", core.NodeTypeCache, true},
		{"s3_substring", "s3", core.NodeTypeStorage, true},
		{"storage_exact", "storage", core.NodeTypeStorage, true},
		{"ec2", "ec2", core.NodeTypeComputeInstance, true},
		{"compute-instance", "compute-instance", core.NodeTypeComputeInstance, true},
		{"lambda", "lambda", core.NodeTypeServerlessFunction, true},
		{"function_exact", "function", core.NodeTypeServerlessFunction, true},
		{"dynamodb", "dynamodb", core.NodeTypeDatabase, true},
		{"sqs", "sqs", core.NodeTypeMessageQueue, true},
		{"vpc_exact", "vpc", core.NodeTypeVPC, true},
		{"natgateway_exact", "natgateway", core.NodeTypeNetworkGateway, true},
		{"route53", "route53", core.NodeTypeDNSZone, true},
		{"cloudfront", "cloudfront", core.NodeTypeCDN, true},
		{"ecr", "ecr", core.NodeTypeContainerRegistry, true},
		{"eks", "eks", core.NodeTypeManagedCluster, true},
		{"secretsmanager", "secretsmanager", core.NodeTypeSecretVault, true},
		{"cloudwatch", "cloudwatch", core.NodeTypeMonitoringService, true},
		{"unknown", "kinesis", "", false},
		{"unknown_stepfunctions", "stepfunctions", "", false},
		{"empty", "", "", false},
		{"random_garbage", "asdfqwerty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotOk := determineCloudResourceNodeType(tc.resourceType)
			if gotOk != tc.wantOk {
				t.Errorf("determineCloudResourceNodeType(%q) ok = %v, want %v", tc.resourceType, gotOk, tc.wantOk)
			}
			if gotType != tc.wantType {
				t.Errorf("determineCloudResourceNodeType(%q) NodeType = %q, want %q", tc.resourceType, gotType, tc.wantType)
			}
		})
	}
}

func TestExtractResourceNameFromEndpoint(t *testing.T) {
	const bucket = "my-bucket"
	cases := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"s3_virtual_global", bucket + ".s3.amazonaws.com", bucket},
		{"s3_virtual_regional", bucket + ".s3.us-east-1.amazonaws.com", bucket},
		{"s3_website", bucket + ".s3-website-us-east-1.amazonaws.com", bucket},
		{"s3_path_global", "s3.amazonaws.com", ""},
		{"s3_path_regional", "s3-us-east-1.amazonaws.com", ""},
		{"rds", "mydb.abc123.us-east-1.rds.amazonaws.com", "mydb"},
		{"elasticache", "my-cluster.abc123.0001.use1.cache.amazonaws.com", "my-cluster"},
		{"cloudfront", "d111111abcdef8.cloudfront.net", "d111111abcdef8"},
		{"apigateway", "abcd1234.execute-api.us-east-1.amazonaws.com", "abcd1234"},
		{"lambda_url", "abcd1234.lambda-url.us-east-1.on.aws", "abcd1234"},
		{"dynamodb_no_resource_in_host", "dynamodb.us-east-1.amazonaws.com", ""},
		{"sqs_no_resource_in_host", "sqs.us-east-1.amazonaws.com", ""},
		{"unknown", "example.com", ""},
		{"case_insensitive", "MY-BUCKET.S3.US-EAST-1.AMAZONAWS.COM", bucket},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractResourceNameFromEndpoint(tc.endpoint); got != tc.want {
				t.Errorf("extractResourceNameFromEndpoint(%q) = %q, want %q", tc.endpoint, got, tc.want)
			}
		})
	}
}

// silentLogger returns a slog.Logger that discards output — used in tests so
// we can assert on behaviour without polluting test output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// makeNode is a tiny test-only constructor for DbNode with the fields
// buildCloudEndpointIndex actually reads.
func makeNode(id string, nodeType core.NodeType, props map[string]interface{}) *core.DbNode {
	return &core.DbNode{
		ID:         id,
		NodeType:   nodeType,
		UniqueKey:  string(nodeType) + ":" + id,
		Properties: props,
	}
}

func TestBuildCloudEndpointIndex(t *testing.T) {
	const (
		lbDNS        = "my-alb-1234567890.us-east-1.elb.amazonaws.com"
		rdsDNS       = "mydb.abc123.us-east-1.rds.amazonaws.com"
		rdsEndpoint  = "mydb-write.abc123.us-east-1.rds.amazonaws.com"
		cfgEndpoint  = "my-cache.cfg.use1.cache.amazonaws.com"
		priEndpoint  = "my-cache.0001.use1.cache.amazonaws.com"
		readEndpoint = "my-cache-ro.0001.use1.cache.amazonaws.com"
		ng1Endpoint  = "shard-1.abc.use1.cache.amazonaws.com"
		ng2Endpoint  = "shard-2.abc.use1.cache.amazonaws.com"
		cnEndpoint   = "memcached-001.abc.use1.cache.amazonaws.com"
	)

	lb := makeNode("lb1", core.NodeTypeLoadBalancer, map[string]interface{}{"dns_name": lbDNS})
	db := makeNode("db1", core.NodeTypeDatabase, map[string]interface{}{
		"dns_name":         rdsDNS,
		"endpoint_address": rdsEndpoint,
	})
	cache := makeNode("cache1", core.NodeTypeCache, map[string]interface{}{
		"configuration_endpoint": cfgEndpoint,
		"primary_endpoint":       priEndpoint,
		"reader_endpoint":        readEndpoint,
		"node_group_endpoints":   []string{ng1Endpoint, ng2Endpoint},
		"cache_node_endpoints":   []interface{}{cnEndpoint, ""}, // mixed shape, empty-skip
	})
	// Should be ignored — wrong node type
	pod := makeNode("pod1", core.NodeTypePod, map[string]interface{}{"dns_name": "should-not-index"})
	// Should be ignored — nil properties (defensive)
	nilProps := &core.DbNode{ID: "n1", NodeType: core.NodeTypeLoadBalancer}
	// nil node entry — defensive
	nodes := []*core.DbNode{lb, db, cache, pod, nilProps, nil}

	idx := buildCloudEndpointIndex(nil, "", nodes, silentLogger())

	cases := []struct {
		name      string
		endpoint  string
		wantNode  *core.DbNode
		wantField string
	}{
		{"lb_dns_name", lbDNS, lb, "dns_name"},
		{"lb_dns_name_uppercased", "MY-ALB-1234567890.US-EAST-1.ELB.AMAZONAWS.COM", lb, "dns_name"},
		{"lb_dns_name_with_whitespace", "   " + lbDNS + "   ", lb, "dns_name"},
		{"rds_dns_name", rdsDNS, db, "dns_name"},
		{"rds_endpoint_address", rdsEndpoint, db, "endpoint_address"},
		{"cache_config", cfgEndpoint, cache, "configuration_endpoint"},
		{"cache_primary", priEndpoint, cache, "primary_endpoint"},
		{"cache_reader", readEndpoint, cache, "reader_endpoint"},
		{"cache_node_group_1", ng1Endpoint, cache, "node_group_endpoints"},
		{"cache_node_group_2", ng2Endpoint, cache, "node_group_endpoints"},
		{"cache_memcached_node", cnEndpoint, cache, "cache_node_endpoints"},
		{"unindexed_pod_dns", "should-not-index", nil, ""},
		{"unknown_endpoint", "nowhere.example.com", nil, ""},
		{"empty_endpoint", "", nil, ""},
		{"whitespace_only", "   ", nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotNode, gotField := findCloudResourceByEndpoint(idx, tc.endpoint)
			if gotNode != tc.wantNode {
				wantID := "<nil>"
				if tc.wantNode != nil {
					wantID = tc.wantNode.ID
				}
				gotID := "<nil>"
				if gotNode != nil {
					gotID = gotNode.ID
				}
				t.Errorf("findCloudResourceByEndpoint(%q) node = %s, want %s", tc.endpoint, gotID, wantID)
			}
			if gotField != tc.wantField {
				t.Errorf("findCloudResourceByEndpoint(%q) field = %q, want %q", tc.endpoint, gotField, tc.wantField)
			}
		})
	}
}

func TestBuildCloudEndpointIndex_FirstWriteWinsOnCollision(t *testing.T) {
	const sharedDNS = "duplicate.us-east-1.elb.amazonaws.com"
	first := makeNode("first", core.NodeTypeLoadBalancer, map[string]interface{}{"dns_name": sharedDNS})
	second := makeNode("second", core.NodeTypeLoadBalancer, map[string]interface{}{"dns_name": sharedDNS})

	idx := buildCloudEndpointIndex(nil, "", []*core.DbNode{first, second}, silentLogger())

	gotNode, _ := findCloudResourceByEndpoint(idx, sharedDNS)
	if gotNode != first {
		t.Errorf("expected first-write-wins to return %s, got %v", first.ID, gotNode)
	}
}

func TestFindCloudResourceByEndpoint_EmptyIndex(t *testing.T) {
	if node, field := findCloudResourceByEndpoint(nil, "any.endpoint.com"); node != nil || field != "" {
		t.Errorf("expected (nil, \"\") for nil index, got (%v, %q)", node, field)
	}
	if node, field := findCloudResourceByEndpoint(map[string]endpointHit{}, "any.endpoint.com"); node != nil || field != "" {
		t.Errorf("expected (nil, \"\") for empty index, got (%v, %q)", node, field)
	}
}

func TestStrProp(t *testing.T) {
	cases := []struct {
		name string
		node *core.DbNode
		key  string
		want string
	}{
		{"nil node", nil, "x", ""},
		{"nil props", &core.DbNode{}, "x", ""},
		{"missing key", &core.DbNode{Properties: map[string]interface{}{"a": "1"}}, "b", ""},
		{"present string", &core.DbNode{Properties: map[string]interface{}{"a": "hello"}}, "a", "hello"},
		{"non-string value", &core.DbNode{Properties: map[string]interface{}{"a": 42}}, "a", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strProp(tc.node, tc.key); got != tc.want {
				t.Errorf("strProp = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStringSliceProp(t *testing.T) {
	cases := []struct {
		name string
		node *core.DbNode
		key  string
		want []string
	}{
		{"nil node", nil, "x", nil},
		{"nil props", &core.DbNode{}, "x", nil},
		{"missing key", &core.DbNode{Properties: map[string]interface{}{"a": "1"}}, "b", nil},
		{"native []string", &core.DbNode{Properties: map[string]interface{}{"a": []string{"x", "y"}}}, "a", []string{"x", "y"}},
		{"[]interface{} with strings", &core.DbNode{Properties: map[string]interface{}{"a": []interface{}{"x", "y"}}}, "a", []string{"x", "y"}},
		{"[]interface{} with empties", &core.DbNode{Properties: map[string]interface{}{"a": []interface{}{"x", "", "y"}}}, "a", []string{"x", "y"}},
		{"[]interface{} with non-strings", &core.DbNode{Properties: map[string]interface{}{"a": []interface{}{1, "y", true}}}, "a", []string{"y"}},
		{"wrong type", &core.DbNode{Properties: map[string]interface{}{"a": "scalar"}}, "a", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stringSliceProp(tc.node, tc.key)
			if len(got) != len(tc.want) {
				t.Errorf("stringSliceProp len = %d, want %d (got=%v want=%v)", len(got), len(tc.want), got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("stringSliceProp[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestMakeRoutesThroughEdge(t *testing.T) {
	src := makeNode("src", core.NodeTypeExternalService, map[string]interface{}{"name": "es"})
	dst := makeNode("dst", core.NodeTypeLoadBalancer, map[string]interface{}{"name": "lb"})

	t.Run("direct match", func(t *testing.T) {
		edge := makeRoutesThroughEdge(src, dst, "dns_name", "es.example.com", "", "acct1", "tenantA")
		if edge.SourceNodeID != src.ID || edge.DestinationNodeID != dst.ID {
			t.Errorf("edge endpoints wrong: %s -> %s", edge.SourceNodeID, edge.DestinationNodeID)
		}
		if edge.RelationshipType != core.RelationshipRoutesThrough {
			t.Errorf("RelationshipType = %q, want %q", edge.RelationshipType, core.RelationshipRoutesThrough)
		}
		if edge.Properties["discovered_from"] != "graph_endpoint_index" {
			t.Errorf("discovered_from = %v, want graph_endpoint_index", edge.Properties["discovered_from"])
		}
		if _, has := edge.Properties["resolved_dns_name"]; has {
			t.Error("resolved_dns_name should be omitted when empty")
		}
		if edge.CloudAccountID != "acct1" || edge.TenantID != "tenantA" {
			t.Errorf("scope wrong: %s / %s", edge.CloudAccountID, edge.TenantID)
		}
	})

	t.Run("route53 match carries resolved_dns_name", func(t *testing.T) {
		edge := makeRoutesThroughEdge(src, dst, "route53_resolution", "es -> aws-host", "es.example.com", "acct1", "tenantA")
		if edge.Properties["resolved_dns_name"] != "es.example.com" {
			t.Errorf("resolved_dns_name = %v, want es.example.com", edge.Properties["resolved_dns_name"])
		}
	})
}

// TestBuildCloudEndpointIndex_NewNodeTypes covers the NodeTypes added to the
// index in this PR: Storage / MessageQueue / Topic / ContainerRegistry /
// ManagedCluster / CDN / ServerlessFunction / APIGateway. Without these,
// DirectEndpointMatchStrategy never hits on S3/SQS/SNS/DDB/etc. external
// services. dns_aliases coverage is the second half — S3 in particular has
// dualstack/website/regionless variants that all must hit the same node.
func TestBuildCloudEndpointIndex_NewNodeTypes(t *testing.T) {
	const (
		s3DNS         = "nudgebee-emails.s3.us-east-1.amazonaws.com"
		s3Dualstack   = "nudgebee-emails.s3.dualstack.us-east-1.amazonaws.com"
		s3Website     = "nudgebee-emails.s3-website-us-east-1.amazonaws.com"
		s3Global      = "nudgebee-emails.s3.amazonaws.com"
		sqsDNS        = "sqs.us-east-1.amazonaws.com"
		snsDNS        = "sns.us-east-1.amazonaws.com"
		ddbDNS        = "dynamodb.us-east-1.amazonaws.com"
		ddbSDK        = "331803013664.ddb.us-east-1.amazonaws.com"
		ecrDNS        = "123456789012.dkr.ecr.us-east-1.amazonaws.com"
		eksAPI        = "ad02f73e98b72b083776f2e963e06c93.gr7.us-east-1.eks.amazonaws.com"
		cfDNS         = "d111111abcdef8.cloudfront.net"
		lambdaService = "lambda.us-east-1.amazonaws.com"
		apigwDNS      = "abcd1234.execute-api.us-east-1.amazonaws.com"
	)

	s3 := makeNode("s3-1", core.NodeTypeStorage, map[string]interface{}{
		"dns_name":    s3DNS,
		"dns_aliases": []string{s3Dualstack, s3Website, s3Global},
	})
	sqs := makeNode("sqs-1", core.NodeTypeMessageQueue, map[string]interface{}{"dns_name": sqsDNS})
	sns := makeNode("sns-1", core.NodeTypeTopic, map[string]interface{}{"dns_name": snsDNS})
	ddb := makeNode("ddb-1", core.NodeTypeDatabase, map[string]interface{}{
		"dns_name":    ddbDNS,
		"dns_aliases": []string{ddbSDK},
	})
	ecr := makeNode("ecr-1", core.NodeTypeContainerRegistry, map[string]interface{}{"dns_name": ecrDNS})
	eks := makeNode("eks-1", core.NodeTypeManagedCluster, map[string]interface{}{"dns_name": eksAPI})
	cdn := makeNode("cdn-1", core.NodeTypeCDN, map[string]interface{}{"dns_name": cfDNS})
	lambda := makeNode("lambda-1", core.NodeTypeServerlessFunction, map[string]interface{}{"dns_name": lambdaService})
	apigw := makeNode("apigw-1", core.NodeTypeAPIGateway, map[string]interface{}{"dns_name": apigwDNS})

	idx := buildCloudEndpointIndex(nil, "", []*core.DbNode{s3, sqs, sns, ddb, ecr, eks, cdn, lambda, apigw}, silentLogger())

	cases := []struct {
		name      string
		endpoint  string
		wantNode  *core.DbNode
		wantField string
	}{
		{"s3_canonical", s3DNS, s3, "dns_name"},
		{"s3_dualstack_alias", s3Dualstack, s3, "dns_aliases"},
		{"s3_website_alias", s3Website, s3, "dns_aliases"},
		{"s3_global_alias", s3Global, s3, "dns_aliases"},
		{"s3_uppercased", "NUDGEBEE-EMAILS.S3.US-EAST-1.AMAZONAWS.COM", s3, "dns_name"},
		{"sqs", sqsDNS, sqs, "dns_name"},
		{"sns", snsDNS, sns, "dns_name"},
		{"ddb_canonical", ddbDNS, ddb, "dns_name"},
		{"ddb_account_scoped_sdk_alias", ddbSDK, ddb, "dns_aliases"},
		{"ecr_private", ecrDNS, ecr, "dns_name"},
		{"eks_api", eksAPI, eks, "dns_name"},
		{"cloudfront", cfDNS, cdn, "dns_name"},
		{"lambda_service_endpoint", lambdaService, lambda, "dns_name"},
		{"apigw", apigwDNS, apigw, "dns_name"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotNode, gotField := findCloudResourceByEndpoint(idx, tc.endpoint)
			if gotNode != tc.wantNode {
				wantID := "<nil>"
				if tc.wantNode != nil {
					wantID = tc.wantNode.ID
				}
				gotID := "<nil>"
				if gotNode != nil {
					gotID = gotNode.ID
				}
				t.Errorf("findCloudResourceByEndpoint(%q) node = %s, want %s", tc.endpoint, gotID, wantID)
			}
			if gotField != tc.wantField {
				t.Errorf("findCloudResourceByEndpoint(%q) field = %q, want %q", tc.endpoint, gotField, tc.wantField)
			}
		})
	}
}

// TestSynthesizeFromResource exercises the cloud_resourses-derived synthesis
// path used by extractDNSName. Mirror of the AWS source's in-graph synthesis
// — both must mint the same string for a given resource so the two index
// build paths converge on a single key.
func TestSynthesizeFromResource(t *testing.T) {
	cases := []struct {
		name string
		row  *CloudResourceRow
		want string
	}{
		{
			name: "s3_canonical",
			row: &CloudResourceRow{
				Type:   "storage",
				Name:   "nudgebee-emails",
				Region: "us-east-1",
			},
			want: "nudgebee-emails.s3.us-east-1.amazonaws.com",
		},
		// Region-/account-scoped service endpoints (queue/topic/table) are not
		// synthesized — see AwsServiceDNS doc for the production rationale.
		{
			name: "sqs_not_synthesized",
			row:  &CloudResourceRow{Type: "queue", Name: "my-queue", Region: "us-west-2"},
			want: "",
		},
		{
			name: "sns_not_synthesized",
			row:  &CloudResourceRow{Type: "topic", Name: "my-topic", Region: "eu-west-1"},
			want: "",
		},
		{
			name: "dynamodb_not_synthesized",
			row:  &CloudResourceRow{Type: "table", Name: "my-table", Region: "us-east-1", AccountNumber: "331803013664"},
			want: "",
		},
		{
			name: "unknown_type_no_synthesize",
			row:  &CloudResourceRow{Type: "compute-instance", Name: "i-abc", Region: "us-east-1"},
			want: "",
		},

		// GCP Cloud Storage — `<bucket>.storage.googleapis.com` per-bucket.
		{
			name: "gcs_bucket_canonical_collector_type",
			row:  &CloudResourceRow{Type: "storage.googleapis.com/Bucket", Name: "nudgebee-gcp-templates"},
			want: "nudgebee-gcp-templates.storage.googleapis.com",
		},
		{
			name: "gcs_bucket_canonical_short_type",
			row:  &CloudResourceRow{Type: "cloud-storage", Name: "my-bucket"},
			want: "my-bucket.storage.googleapis.com",
		},
		// GCP service endpoints intentionally NOT synthesized.
		{
			name: "gcp_pubsub_not_synthesized",
			row:  &CloudResourceRow{Type: "pubsub.googleapis.com/Topic", Name: "my-topic", Region: "us-central1"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := synthesizeFromResource(tc.row); got != tc.want {
				t.Errorf("synthesizeFromResource = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRepoURIHostLocal(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{"with_scheme", "https://nudgebee-booth-eln5wjp7uq-el.a.run.app", "nudgebee-booth-eln5wjp7uq-el.a.run.app"},
		{"without_scheme", "nudgebee-booth-eln5wjp7uq-el.a.run.app/api/v1", "nudgebee-booth-eln5wjp7uq-el.a.run.app"},
		{"with_path", "https://AD02F73E.gr7.us-east-1.eks.amazonaws.com/v1", "AD02F73E.gr7.us-east-1.eks.amazonaws.com"},
		{"empty", "", ""},
		{"whitespace", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoURIHostLocal(tc.uri); got != tc.want {
				t.Errorf("repoURIHostLocal(%q) = %q, want %q", tc.uri, got, tc.want)
			}
		})
	}
}
