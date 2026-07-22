package sources

import (
	"encoding/json"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"os"
	"testing"
)

func TestGCPSourceGenerateUniqueKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewGCPSource(GCPSourceConfig{}, logger)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	tests := []struct {
		name    string
		node    *core.DbNode
		wantKey string
	}{
		{
			name: "ComputeInstance node",
			node: &core.DbNode{
				NodeType: core.NodeTypeComputeInstance,
				Properties: map[string]interface{}{
					"name":   "web-server-1",
					"region": "us-central1",
				},
				CloudAccountID: "acc-1",
			},
			wantKey: "gcp:acc-1:us-central1:ComputeInstance:web-server-1:web-server-1",
		},
		{
			name: "Database node with project_id",
			node: &core.DbNode{
				NodeType: core.NodeTypeDatabase,
				Properties: map[string]interface{}{
					"name":           "my-sql-instance",
					"region":         "us-central1",
					"gcp_project_id": "my-project",
				},
				CloudAccountID: "acc-1",
			},
			wantKey: "gcp:acc-1:us-central1:Database:my-sql-instance:my-sql-instance",
		},
		{
			name: "VPC node",
			node: &core.DbNode{
				NodeType: core.NodeTypeVPC,
				Properties: map[string]interface{}{
					"name":   "default",
					"region": "us-central1",
				},
				CloudAccountID: "acc-1",
			},
			wantKey: "gcp:acc-1:us-central1:VPC:default:default",
		},
		{
			name:    "Nil node returns empty",
			node:    nil,
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := source.GenerateUniqueKey(tt.node)
			if key != tt.wantKey {
				t.Errorf("GenerateUniqueKey() = %q, want %q", key, tt.wantKey)
			}

			// Deterministic
			if tt.node != nil {
				key2 := source.GenerateUniqueKey(tt.node)
				if key != key2 {
					t.Errorf("Not deterministic: %q != %q", key, key2)
				}
			}
		})
	}
}

func TestGCPSourceDetermineNodeType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewGCPSource(GCPSourceConfig{}, logger)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	tests := []struct {
		name        string
		resType     string
		serviceName string
		want        core.NodeType
	}{
		{"compute-engine", "compute-engine", "Compute Engine", core.NodeTypeComputeInstance},
		{"compute asset", "compute.googleapis.com/Instance", "Compute Engine", core.NodeTypeComputeInstance},
		{"cloud-sql", "cloud-sql", "Cloud SQL", core.NodeTypeDatabase},
		{"sqladmin asset", "sqladmin.googleapis.com/Instance/POSTGRES_17", "Cloud SQL", core.NodeTypeDatabase},
		{"kubernetes-engine", "kubernetes-engine", "Kubernetes Engine", core.NodeTypeManagedCluster},
		{"gke asset", "container.googleapis.com/Cluster", "Kubernetes Engine", core.NodeTypeManagedCluster},
		{"bigquery", "bigquery", "BigQuery", core.NodeTypeDatabase},
		{"bq dataset", "bigquery.googleapis.com/Dataset", "BigQuery", core.NodeTypeDatabase},
		{"bq table", "bigquery.googleapis.com/Table", "BigQuery", core.NodeTypeDatabase},
		{"storage bucket", "storage.googleapis.com/Bucket", "Cloud Storage", core.NodeTypeStorage},
		{"networking", "networking", "Networking", core.NodeTypeVPC},
		{"subnet", "subnet", "Networking", core.NodeTypeSubnet},
		{"cloud-logging", "cloud-logging", "Cloud Logging", core.NodeTypeLogAggregator},
		{"cloud-monitoring", "cloud-monitoring", "Cloud Monitoring", core.NodeTypeMonitoringService},
		{"vertex-ai", "vertex-ai", "Vertex AI", core.NodeTypeAIService},
		{"gemini-api", "gemini-api", "Gemini API", core.NodeTypeAIService},
		{"url-map is a route table", "url-map", "Cloud Load Balancing", core.NodeTypeRouteTable},
		{"cloud function is serverless", "cloudfunctions.googleapis.com/Function", "Cloud Functions", core.NodeTypeServerlessFunction},
		{"app engine is serverless", "app-engine", "App Engine", core.NodeTypeServerlessFunction},
		{"secret manager is a vault", "secret-manager", "Secret Manager", core.NodeTypeSecretVault},
		{"cloud tasks is a queue", "cloud-tasks", "Cloud Tasks", core.NodeTypeQueue},
		{"memorystore is a cache", "memorystore", "Memorystore", core.NodeTypeCache},
		{"firestore is a database", "firestore", "Firestore", core.NodeTypeDatabase},
		{"cloud scheduler is a cronjob", "cloud-scheduler", "Cloud Scheduler", core.NodeTypeCronJob},
		{"unknown type fallback to service", "unknown-type", "Compute Engine", core.NodeTypeComputeInstance},
		{"completely unknown", "unknown-type", "Unknown Service", core.NodeTypeCloudResource},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := source.determineNodeType(tt.resType, tt.serviceName)
			if got != tt.want {
				t.Errorf("determineNodeType(%q, %q) = %q, want %q", tt.resType, tt.serviceName, got, tt.want)
			}
		})
	}
}

func TestExtractGCPShortName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"full path", "my-project/zones/us-central1-a/instances/web-server-1", "web-server-1"},
		{"simple name", "web-server-1", "web-server-1"},
		{"two parts", "project/instance-name", "instance-name"},
		{"empty", "", ""},
		{"trailing slash", "project/", "project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGCPShortName(tt.input)
			if got != tt.want {
				t.Errorf("extractGCPShortName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractGCPProjectID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"full path", "my-project/zones/us-central1-a/instances/foo", "my-project"},
		{"simple name", "simple-name", "simple-name"},
		{"two parts", "project/instance", "project"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGCPProjectID(tt.input)
			if got != tt.want {
				t.Errorf("extractGCPProjectID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractGCPResourceNameFromURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"full URL",
			"https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default",
			"default",
		},
		{
			"path only",
			"projects/my-project/regions/us-central1/subnetworks/default-subnet",
			"default-subnet",
		},
		{"simple name", "default", "default"},
		{"empty", "", ""},
		{
			"trailing slash",
			"https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default/",
			"default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGCPResourceNameFromURL(tt.input)
			if got != tt.want {
				t.Errorf("extractGCPResourceNameFromURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGCPSourceGetName(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	if got := source.GetName(); got != "gcp" {
		t.Errorf("GetName() = %q, want 'gcp'", got)
	}
}

func TestGCPSourceIsEnabled(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	if !source.IsEnabled() {
		t.Error("IsEnabled() = false, want true")
	}
}

func TestGCPSourceValidate(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	if err := source.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestGCPSourceShouldIncludeResource(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{
		ServiceTypeFilter: GCPDefaultServiceTypeFilter,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	tests := []struct {
		name     string
		resource CloudResourceRow
		want     bool
	}{
		{
			"allowed compute-engine type",
			CloudResourceRow{Type: "compute-engine", ServiceName: "Compute Engine"},
			true,
		},
		{
			"allowed asset inventory compute type",
			CloudResourceRow{Type: "compute.googleapis.com/Instance", ServiceName: "Compute Engine"},
			true,
		},
		{
			"disallowed type for filtered service",
			CloudResourceRow{Type: "some-unknown-type", ServiceName: "Compute Engine"},
			false,
		},
		{
			"unknown service passes through",
			CloudResourceRow{Type: "custom-thing", ServiceName: "Custom Service"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := source.shouldIncludeResource(&tt.resource)
			if got != tt.want {
				t.Errorf("shouldIncludeResource(%s/%s) = %v, want %v",
					tt.resource.Type, tt.resource.ServiceName, got, tt.want)
			}
		})
	}
}

func TestParseGCloudCLIResponse(t *testing.T) {
	tests := []struct {
		name    string
		resp    map[string]any
		wantErr bool
		wantVal string
	}{
		{
			"data field string",
			map[string]any{"data": `[{"name":"test"}]`},
			false,
			`[{"name":"test"}]`,
		},
		{
			"output field",
			map[string]any{"output": `[{"name":"test2"}]`},
			false,
			`[{"name":"test2"}]`,
		},
		{
			"result field",
			map[string]any{"result": `[{"name":"test3"}]`},
			false,
			`[{"name":"test3"}]`,
		},
		{
			"empty response",
			map[string]any{},
			true,
			"",
		},
		{
			"nil response",
			nil,
			true,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGCloudCLIResponse(tt.resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGCloudCLIResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.wantVal {
				t.Errorf("parseGCloudCLIResponse() = %q, want %q", got, tt.wantVal)
			}
		})
	}
}

func TestCreateSubnetToVPCEdges(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}

	newSubnet := func(name, vpcID string) *core.DbNode {
		props := map[string]interface{}{"name": name}
		if vpcID != "" {
			props["vpc_id"] = vpcID
		}
		return core.NewNode(core.NodeTypeSubnet, "gcp:acct-1:r:Subnet:"+name+":"+name, props, req.TenantID, req.CloudAccountID, "gcp")
	}
	newVPC := func(name string) *core.DbNode {
		return core.NewNode(core.NodeTypeVPC, "gcp:acct-1:global:VPC:"+name+":"+name,
			map[string]interface{}{"name": name}, req.TenantID, req.CloudAccountID, "gcp")
	}

	t.Run("single VPC links explicit and infers orphans", func(t *testing.T) {
		vpc := newVPC("default")
		matched := newSubnet("with-vpc", "default")
		orphan := newSubnet("no-vpc", "")
		// Explicit vpc_id pointing at a VPC absent from this account's lookup
		// (e.g. Shared VPC). Must NOT be inferred to the sole VPC.
		absent := newSubnet("absent-vpc", "host-shared-vpc")
		lookup := newNodeLookup([]*core.DbNode{vpc, matched, orphan, absent})

		edges := source.createSubnetToVPCEdges(nil, lookup, req)
		if len(edges) != 2 {
			t.Fatalf("expected 2 edges, got %d", len(edges))
		}
		byConn := map[string]int{}
		for _, e := range edges {
			if e.DestinationNodeID != vpc.ID {
				t.Errorf("edge does not point at the VPC node")
			}
			if e.SourceNodeID == absent.ID {
				t.Errorf("subnet with an explicit (absent) vpc_id must not be inferred to soleVPC")
			}
			byConn[e.Properties["connection_type"].(string)]++
		}
		if byConn["vpc"] != 1 || byConn["vpc_inferred"] != 1 {
			t.Errorf("expected one 'vpc' and one 'vpc_inferred' edge, got %v", byConn)
		}
	})

	t.Run("multiple VPCs: no inference for subnets without vpc_id", func(t *testing.T) {
		vpcA := newVPC("default")
		vpcB := newVPC("prod")
		orphan := newSubnet("no-vpc", "")
		matched := newSubnet("with-vpc", "prod")
		lookup := newNodeLookup([]*core.DbNode{vpcA, vpcB, orphan, matched})

		edges := source.createSubnetToVPCEdges(nil, lookup, req)
		if len(edges) != 1 {
			t.Fatalf("expected 1 edge (only the explicit match), got %d", len(edges))
		}
		if edges[0].Properties["connection_type"].(string) != "vpc" {
			t.Errorf("expected explicit 'vpc' edge, got %v", edges[0].Properties["connection_type"])
		}
		if edges[0].DestinationNodeID != vpcB.ID {
			t.Errorf("explicit match should point at the named VPC")
		}
	})
}

func TestExtractGCPSubnetMetadata(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	props := map[string]interface{}{}
	meta := map[string]interface{}{
		"network":         "https://www.googleapis.com/compute/v1/projects/p1/global/networks/prod-vpc",
		"ip_cidr_range":   "10.0.0.0/20",
		"gateway_address": "10.0.0.1",
	}
	source.extractGCPSubnetMetadata(props, meta)

	if props["vpc_id"] != "prod-vpc" {
		t.Errorf("vpc_id = %v, want prod-vpc", props["vpc_id"])
	}
	if props["ip_cidr_range"] != "10.0.0.0/20" {
		t.Errorf("ip_cidr_range = %v, want 10.0.0.0/20", props["ip_cidr_range"])
	}

	// Missing network must not set vpc_id.
	empty := map[string]interface{}{}
	source.extractGCPSubnetMetadata(empty, map[string]interface{}{})
	if _, ok := empty["vpc_id"]; ok {
		t.Errorf("vpc_id should be unset when network is absent")
	}
}

func TestCreateCDNEdges(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}

	backendPool := core.NewNode(core.NodeTypeBackendPool, "gcp:acct-1:global:BackendPool:web-bes:web-bes",
		map[string]interface{}{"name": "web-bes"}, req.TenantID, req.CloudAccountID, "gcp")
	cdnMatched := core.NewNode(core.NodeTypeCDN, "gcp:acct-1:global:CDN:web-bes",
		map[string]interface{}{"name": "web-bes", "origin_backend_service_name": "web-bes"}, req.TenantID, req.CloudAccountID, "gcp")
	cdnUnmatched := core.NewNode(core.NodeTypeCDN, "gcp:acct-1:global:CDN:orphan-bes",
		map[string]interface{}{"name": "orphan-bes", "origin_backend_service_name": "orphan-bes"}, req.TenantID, req.CloudAccountID, "gcp")

	lookup := newNodeLookup([]*core.DbNode{backendPool, cdnMatched, cdnUnmatched})
	edges := source.createCDNEdges(lookup, req)

	if len(edges) != 1 {
		t.Fatalf("expected 1 CDN edge, got %d", len(edges))
	}
	if edges[0].SourceNodeID != cdnMatched.ID || edges[0].DestinationNodeID != backendPool.ID {
		t.Errorf("CDN edge endpoints wrong: src=%s dst=%s", edges[0].SourceNodeID, edges[0].DestinationNodeID)
	}
	if edges[0].RelationshipType != core.RelationshipRoutesTo {
		t.Errorf("CDN edge relationship = %v, want ROUTES_TO", edges[0].RelationshipType)
	}
	if edges[0].Properties["connection_type"] != "cdn_backend" {
		t.Errorf("connection_type = %v, want cdn_backend", edges[0].Properties["connection_type"])
	}
}

func TestCreateNodeFromResourceProjectID(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-uuid"}

	// Bare Name + populated account_number → project must come from account_number,
	// not from the resource's own name.
	bare := &CloudResourceRow{
		ID: "r1", Name: "default", Type: "subnet", ServiceName: "Networking",
		Region: "us-west3", ARN: "cbp-infra/regions/us-west3/subnetworks/default",
		AccountNumber: "cbp-infra", IsActive: true,
	}
	node := source.createNodeFromResource(bare, req)
	if node == nil {
		t.Fatal("expected a node, got nil")
	}
	if node.Properties["gcp_project_id"] != "cbp-infra" {
		t.Errorf("gcp_project_id = %v, want cbp-infra", node.Properties["gcp_project_id"])
	}

	// Empty account_number → fall back to parsing a path-style Name.
	fallback := &CloudResourceRow{
		ID: "r2", Name: "myproj/regions/us-west3/subnetworks/s", Type: "subnet",
		ServiceName: "Networking", Region: "us-west3", IsActive: true,
	}
	node2 := source.createNodeFromResource(fallback, req)
	if node2 == nil {
		t.Fatal("expected a node, got nil")
	}
	if node2.Properties["gcp_project_id"] != "myproj" {
		t.Errorf("fallback gcp_project_id = %v, want myproj", node2.Properties["gcp_project_id"])
	}
}

func TestCreatePubSubSubscriptionEdges(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}

	cloudRun := core.NewNode(core.NodeTypeServerlessFunction, "gcp:acct-1:us-central1:ServerlessFunction:cdc-events:cdc-events",
		map[string]interface{}{"name": "cdc-events", "dns_name": "cdc-events-abc-uc.a.run.app"},
		req.TenantID, req.CloudAccountID, "gcp")
	apiRun := core.NewNode(core.NodeTypeServerlessFunction, "gcp:acct-1:us-central1:ServerlessFunction:api:api",
		map[string]interface{}{"name": "api", "dns_name": "api-xyz-uc.a.run.app"},
		req.TenantID, req.CloudAccountID, "gcp")
	appEngine := core.NewNode(core.NodeTypeServerlessFunction, "gcp:acct-1:us-central1:ServerlessFunction:myproj:myproj",
		map[string]interface{}{"name": "myproj", "type": "app-engine"}, req.TenantID, req.CloudAccountID, "gcp")
	topic := core.NewNode(core.NodeTypeTopic, "gcp:acct-1:global:Topic:evt-topic:evt-topic",
		map[string]interface{}{"name": "evt-topic"}, req.TenantID, req.CloudAccountID, "gcp")
	lookup := newNodeLookup([]*core.DbNode{cloudRun, apiRun, appEngine, topic})

	// Custom domain api.example.com is served by an LB url-map whose path-matcher
	// defaults to the "api-cloudrun-service" backend, which resolves to apiRun.
	backendTargets := map[string]*core.DbNode{"api-cloudrun-service": apiRun}
	cli := &gcpCLIData{urlMaps: map[string]*GCPURLMap{
		"lb": {Name: "lb", DefaultService: ".../backendServices/default",
			HostRules: []struct {
				Hosts       []string `json:"hosts"`
				PathMatcher string   `json:"pathMatcher"`
			}{{Hosts: []string{"api.example.com"}, PathMatcher: "pm-api"}},
			PathMatchers: []struct {
				Name           string `json:"name"`
				DefaultService string `json:"defaultService"`
			}{{Name: "pm-api", DefaultService: ".../backendServices/api-cloudrun-service"}}},
	}}

	subMeta := func(topic, push string) json.RawMessage {
		b, _ := json.Marshal(map[string]interface{}{"topic": topic, "push_endpoint": push})
		return b
	}
	resources := []CloudResourceRow{
		// push → Cloud Run run.app: link to cloudRun
		{Name: "evt-sub", Type: "pubsub.googleapis.com/Subscription",
			Meta: subMeta("evt-topic", "https://cdc-events-abc-uc.a.run.app?__GCP_CloudEventsMode=CE_PUBSUB_BINDING")},
		// push → custom domain behind the LB: link to apiRun
		{Name: "api-sub", Type: "pubsub.googleapis.com/Subscription",
			Meta: subMeta("evt-topic", "https://api.example.com/pubsub/handler")},
		// push → appspot: link to App Engine node
		{Name: "gae-sub", Type: "pubsub.googleapis.com/Subscription",
			Meta: subMeta("evt-topic", "https://myproj.uc.r.appspot.com/_ah/push-handlers/pubsub")},
		// push → genuinely external host: skip
		{Name: "ext-sub", Type: "pubsub.googleapis.com/Subscription",
			Meta: subMeta("evt-topic", "https://secops.external.io/webhook")},
		// pull subscription (no push): skip
		{Name: "pull-sub", Type: "pubsub.googleapis.com/Subscription",
			Meta: subMeta("evt-topic", "")},
		// not a subscription: ignore
		{Name: "evt-topic", Type: "pubsub.googleapis.com/Topic", Meta: json.RawMessage(`{}`)},
	}

	edges := source.createPubSubSubscriptionEdges(resources, cli, lookup, backendTargets, req)
	if len(edges) != 3 {
		t.Fatalf("expected 3 subscription edges (run.app + custom-domain + appspot), got %d", len(edges))
	}
	bySrc := map[string]bool{}
	for _, e := range edges {
		if e.DestinationNodeID != topic.ID {
			t.Errorf("edge does not point at the topic")
		}
		if e.RelationshipType != core.RelationshipSubscribesTo || e.Properties["connection_type"] != "pubsub_push_subscription" {
			t.Errorf("unexpected edge: rel=%v conn=%v", e.RelationshipType, e.Properties["connection_type"])
		}
		bySrc[e.SourceNodeID] = true
	}
	for _, want := range []*core.DbNode{cloudRun, apiRun, appEngine} {
		if !bySrc[want.ID] {
			t.Errorf("expected an edge from consumer %s", want.Properties["name"])
		}
	}
}

func TestEnrichExistingURLMapNode(t *testing.T) {
	tenant, acct := "t1", "acct-1"
	// Resource-table url-map node (now typed RouteTable) with no routing target yet.
	existing := core.NewNode(core.NodeTypeRouteTable, "gcp:acct-1:global:RouteTable:lb-map:lb-map",
		map[string]interface{}{"name": "lb-map", "type": "url-map"}, tenant, acct, "gcp")
	lookup := newNodeLookup([]*core.DbNode{existing})

	urlMap := &GCPURLMap{
		Name:           "lb-map",
		DefaultService: "https://www.googleapis.com/compute/v1/projects/p/global/backendServices/web-bes",
		SelfLink:       "https://www.googleapis.com/compute/v1/projects/p/global/urlMaps/lb-map",
	}

	if !enrichExistingURLMapNode(lookup, "lb-map", urlMap) {
		t.Fatal("expected enrichExistingURLMapNode to find and enrich the node")
	}
	if existing.Properties["default_service"] != "web-bes" {
		t.Errorf("default_service = %v, want web-bes", existing.Properties["default_service"])
	}
	if existing.Properties["default_service_url"] != urlMap.DefaultService {
		t.Errorf("default_service_url not stamped")
	}

	// No matching node → returns false (caller creates a fresh node).
	if enrichExistingURLMapNode(lookup, "other", urlMap) {
		t.Error("expected false when no url-map node matches")
	}
}

func TestShouldSuppressGCPResource_ComputeEngineBilling(t *testing.T) {
	tests := []struct {
		name string
		row  *CloudResourceRow
		want bool
	}{
		{
			"compute-engine billing stub is suppressed",
			&CloudResourceRow{Type: "compute-engine", ServiceName: "Compute Engine", Name: "my-project", Meta: json.RawMessage(`{"nb_source":"billing"}`)},
			true,
		},
		{
			"real compute-engine instance (api source) is kept",
			&CloudResourceRow{Type: "compute-engine", ServiceName: "Compute Engine", Name: "web-1", Meta: json.RawMessage(`{"nb_source":"api","zone":"us-central1-a"}`)},
			false,
		},
		{
			"compute-engine with no meta is kept",
			&CloudResourceRow{Type: "compute-engine", ServiceName: "Compute Engine", Name: "web-1"},
			false,
		},
		{
			"vm-manager stub still suppressed",
			&CloudResourceRow{Type: "vm-manager", Meta: json.RawMessage(`{"nb_source":"billing"}`)},
			true,
		},
		{
			"Cloud Tasks billing rollup is suppressed",
			&CloudResourceRow{Type: "cloud-tasks", ServiceName: "Cloud Tasks", Name: "my-project", Meta: json.RawMessage(`{"nb_source":"billing"}`)},
			true,
		},
		{
			"Secret Manager billing rollup is suppressed",
			&CloudResourceRow{Type: "secret-manager", ServiceName: "Secret Manager", Name: "my-project", Meta: json.RawMessage(`{"nb_source":"billing"}`)},
			true,
		},
		{
			"real api resource (non-billing) is kept",
			&CloudResourceRow{Type: "storage.googleapis.com/Bucket", ServiceName: "Cloud Storage", Name: "my-bucket", Meta: json.RawMessage(`{"nb_source":"api"}`)},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSuppressGCPResource(tt.row); got != tt.want {
				t.Errorf("shouldSuppressGCPResource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveBackendTargetNode(t *testing.T) {
	tenant, acct := "t1", "acct-1"
	apiRun := core.NewNode(core.NodeTypeServerlessFunction, "k:cr", map[string]interface{}{"name": "api"}, tenant, acct, "gcp")
	appEngine := core.NewNode(core.NodeTypeServerlessFunction, "k:gae", map[string]interface{}{"name": "myproj", "type": "app-engine"}, tenant, acct, "gcp")
	cloudRunByName := map[string]*core.DbNode{"api": apiRun}

	cli := &gcpCLIData{serverlessNEGs: map[string]*GCPServerlessNEG{
		"api-cloudrun-service": {Name: "api-cloudrun-service", CloudRun: &struct {
			Service string `json:"service"`
		}{Service: "api"}},
		// App Engine default service: appEngine present but service empty (gcloud emits {})
		"gaedefault": {Name: "gaedefault", AppEngine: &struct {
			Service string `json:"service"`
		}{Service: ""}},
	}}
	be := func(neg string) *GCPBackendService {
		bs := &GCPBackendService{Name: neg}
		bs.Backends = append(bs.Backends, struct {
			Group          string  `json:"group"`
			BalancingMode  string  `json:"balancingMode"`
			MaxUtilization float64 `json:"maxUtilization"`
			CapacityScaler float64 `json:"capacityScaler"`
		}{Group: ".../networkEndpointGroups/" + neg})
		return bs
	}

	if got := resolveBackendTargetNode(be("api-cloudrun-service"), cli, cloudRunByName, appEngine); got != apiRun {
		t.Errorf("cloudRun NEG should resolve to the Cloud Run node")
	}
	if got := resolveBackendTargetNode(be("gaedefault"), cli, cloudRunByName, appEngine); got != appEngine {
		t.Errorf("App Engine default-service NEG (empty service) should resolve to the App Engine node, got %v", got)
	}
	if got := resolveBackendTargetNode(be("unknown"), cli, cloudRunByName, appEngine); got != nil {
		t.Errorf("unknown NEG should resolve to nil")
	}
}

func TestCreateCronJobEdges(t *testing.T) {
	source, _ := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}

	topic := core.NewNode(core.NodeTypeTopic, "k:topic", map[string]interface{}{"name": "backup-topic"}, req.TenantID, req.CloudAccountID, "gcp")
	cloudRun := core.NewNode(core.NodeTypeServerlessFunction, "k:cr", map[string]interface{}{"name": "rtm", "dns_name": "rtm-abc-uc.a.run.app"}, req.TenantID, req.CloudAccountID, "gcp")
	cronPubsub := core.NewNode(core.NodeTypeCronJob, "k:c1", map[string]interface{}{"name": "backup", "meta": map[string]interface{}{"target_type": "pubsub", "target": "projects/p/topics/backup-topic"}}, req.TenantID, req.CloudAccountID, "gcp")
	cronHTTP := core.NewNode(core.NodeTypeCronJob, "k:c2", map[string]interface{}{"name": "metrics", "meta": map[string]interface{}{"target_type": "http", "target": "https://rtm-abc-uc.a.run.app/cron/metrics"}}, req.TenantID, req.CloudAccountID, "gcp")
	cronExt := core.NewNode(core.NodeTypeCronJob, "k:c3", map[string]interface{}{"name": "ext", "meta": map[string]interface{}{"target_type": "http", "target": "https://external.example.com/hook"}}, req.TenantID, req.CloudAccountID, "gcp")
	lookup := newNodeLookup([]*core.DbNode{topic, cloudRun, cronPubsub, cronHTTP, cronExt})

	edges := source.createCronJobEdges(nil, lookup, map[string]*core.DbNode{}, req)
	if len(edges) != 2 {
		t.Fatalf("expected 2 cron edges (pubsub + run.app http), got %d", len(edges))
	}
	got := map[string]core.RelationshipType{}
	for _, e := range edges {
		got[e.DestinationNodeID] = e.RelationshipType
	}
	if got[topic.ID] != core.RelationshipPublishesTo {
		t.Errorf("cron→topic should be PUBLISHES_TO, got %v", got[topic.ID])
	}
	if got[cloudRun.ID] != core.RelationshipCalls {
		t.Errorf("cron→cloudrun should be CALLS, got %v", got[cloudRun.ID])
	}
}

func TestCreateRunsAsEdges(t *testing.T) {
	source, _ := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}

	sa := core.NewNode(core.NodeTypeServiceIdentity, "k:sa", map[string]interface{}{"name": "run-sa@proj.iam.gserviceaccount.com"}, req.TenantID, req.CloudAccountID, "gcp")
	withSA := core.NewNode(core.NodeTypeServerlessFunction, "k:cr1", map[string]interface{}{"name": "api", "meta": map[string]interface{}{"service_account": "run-sa@proj.iam.gserviceaccount.com"}}, req.TenantID, req.CloudAccountID, "gcp")
	noSA := core.NewNode(core.NodeTypeServerlessFunction, "k:cr2", map[string]interface{}{"name": "web", "meta": map[string]interface{}{"service_account": ""}}, req.TenantID, req.CloudAccountID, "gcp")
	lookup := newNodeLookup([]*core.DbNode{sa, withSA, noSA})

	edges := source.createRunsAsEdges(lookup, req)
	if len(edges) != 1 {
		t.Fatalf("expected 1 RUNS_AS edge, got %d", len(edges))
	}
	if edges[0].SourceNodeID != withSA.ID || edges[0].DestinationNodeID != sa.ID || edges[0].RelationshipType != core.RelationshipRunsAs {
		t.Errorf("unexpected RUNS_AS edge: %+v", edges[0])
	}
}

func TestCreateUsesSecretEdges(t *testing.T) {
	source, _ := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}

	secretA := core.NewNode(core.NodeTypeSecretVault, "k:sa", map[string]interface{}{"name": "booking-secret"}, req.TenantID, req.CloudAccountID, "gcp")
	secretB := core.NewNode(core.NodeTypeSecretVault, "k:sb", map[string]interface{}{"name": "api-key"}, req.TenantID, req.CloudAccountID, "gcp")
	svc := core.NewNode(core.NodeTypeServerlessFunction, "k:cr", map[string]interface{}{"name": "api", "meta": map[string]interface{}{
		"secrets": []interface{}{"booking-secret", "projects/p/secrets/api-key", "missing-secret"},
	}}, req.TenantID, req.CloudAccountID, "gcp")
	lookup := newNodeLookup([]*core.DbNode{secretA, secretB, svc})

	edges := source.createUsesSecretEdges(lookup, req)
	if len(edges) != 2 {
		t.Fatalf("expected 2 USES_SECRET edges (booking-secret + api-key; missing skipped), got %d", len(edges))
	}
	for _, e := range edges {
		if e.SourceNodeID != svc.ID || e.RelationshipType != core.RelationshipUsesSecret {
			t.Errorf("unexpected edge: %+v", e)
		}
	}
}

func iamBindingRow(role, member string) CloudResourceRow {
	meta, _ := json.Marshal(map[string]interface{}{"role": role, "member": member, "member_type": "serviceAccount", "project_id": "proj"})
	return CloudResourceRow{
		Name:        member,
		Type:        gcpIAMBindingType,
		ServiceName: gcpIAMPolicyServiceName,
		Meta:        meta,
	}
}

func TestCreateHasAccessEdges(t *testing.T) {
	source, _ := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}
	const sa = "app@proj.iam.gserviceaccount.com"

	identity := core.NewNode(core.NodeTypeServiceIdentity, "k:sa", map[string]interface{}{"name": sa, "email": sa}, req.TenantID, req.CloudAccountID, "gcp")
	firestore := core.NewNode(core.NodeTypeDatabase, "k:fs", map[string]interface{}{"name": "(default)", "type": "firestore"}, req.TenantID, req.CloudAccountID, "gcp")
	dataset := core.NewNode(core.NodeTypeDatabase, "k:ds", map[string]interface{}{"name": "events", "type": "bigquery.googleapis.com/Dataset"}, req.TenantID, req.CloudAccountID, "gcp")
	table := core.NewNode(core.NodeTypeDatabase, "k:tbl", map[string]interface{}{"name": "events.t1", "type": "bigquery.googleapis.com/Table"}, req.TenantID, req.CloudAccountID, "gcp")
	bucket := core.NewNode(core.NodeTypeStorage, "k:bk", map[string]interface{}{"name": "assets", "type": "storage.googleapis.com/Bucket"}, req.TenantID, req.CloudAccountID, "gcp")
	topic := core.NewNode(core.NodeTypeTopic, "k:tp", map[string]interface{}{"name": "evt", "type": "pubsub.googleapis.com/Topic"}, req.TenantID, req.CloudAccountID, "gcp")
	lookup := newNodeLookup([]*core.DbNode{identity, firestore, dataset, table, bucket, topic})

	resources := []CloudResourceRow{
		iamBindingRow("roles/datastore.user", sa),
		iamBindingRow("roles/datastore.importExportAdmin", sa), // overlapping datastore role → deduped to 1 edge
		iamBindingRow("roles/bigquery.dataEditor", sa),
		iamBindingRow("roles/storage.admin", sa),
		iamBindingRow("roles/pubsub.publisher", sa),
		iamBindingRow("roles/pubsub.subscriber", sa),
		iamBindingRow("roles/editor", sa),                                        // primitive role → ignored
		iamBindingRow("roles/bigquery.jobUser", sa),                              // non-data bq role → ignored
		iamBindingRow("roles/datastore.user", "other@x.iam.gserviceaccount.com"), // no identity node → skipped
	}

	edges := source.createHasAccessEdges(resources, lookup, req)

	byRelDest := map[string]core.RelationshipType{}
	for _, e := range edges {
		byRelDest[e.DestinationNodeID] = e.RelationshipType
	}
	// Expect: firestore(HAS_ACCESS), dataset(HAS_ACCESS), bucket(HAS_ACCESS), topic(PUBLISHES + SUBSCRIBES) = 5 edges
	if len(edges) != 5 {
		t.Fatalf("expected 5 IAM access edges, got %d: %+v", len(edges), byRelDest)
	}
	if byRelDest[firestore.ID] != core.RelationshipHasAccessTo {
		t.Errorf("firestore should be HAS_ACCESS_TO via datastore.user")
	}
	if byRelDest[dataset.ID] != core.RelationshipHasAccessTo {
		t.Errorf("bigquery dataset should be HAS_ACCESS_TO via dataEditor")
	}
	if _, ok := byRelDest[table.ID]; ok {
		t.Errorf("bigquery TABLE must never be connected (dataset-only)")
	}
	if byRelDest[bucket.ID] != core.RelationshipHasAccessTo {
		t.Errorf("bucket should be HAS_ACCESS_TO via storage.admin")
	}
	// topic gets both PUBLISHES_TO and SUBSCRIBES_TO; the map keeps the last, just assert edge count per rel below.
	var pub, sub int
	for _, e := range edges {
		if e.DestinationNodeID == topic.ID && e.RelationshipType == core.RelationshipPublishesTo {
			pub++
		}
		if e.DestinationNodeID == topic.ID && e.RelationshipType == core.RelationshipSubscribesTo {
			sub++
		}
		if e.SourceNodeID != identity.ID {
			t.Errorf("all IAM access edges must originate from the ServiceIdentity")
		}
		if e.Properties["evidence"] != "iam_policy" {
			t.Errorf("edge missing iam_policy provenance: %+v", e.Properties)
		}
	}
	if pub != 1 || sub != 1 {
		t.Errorf("topic should have exactly one PUBLISHES_TO and one SUBSCRIBES_TO, got pub=%d sub=%d", pub, sub)
	}
}

func TestCreateHasAccessEdges_FanoutCap(t *testing.T) {
	source, _ := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}
	const sa = "app@proj.iam.gserviceaccount.com"

	nodes := []*core.DbNode{core.NewNode(core.NodeTypeServiceIdentity, "k:sa", map[string]interface{}{"name": sa}, req.TenantID, req.CloudAccountID, "gcp")}
	for i := 0; i < maxIAMAccessFanout+1; i++ {
		id := "bk" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		nodes = append(nodes, core.NewNode(core.NodeTypeStorage, "k:"+id, map[string]interface{}{"name": id, "type": "storage.googleapis.com/Bucket"}, req.TenantID, req.CloudAccountID, "gcp"))
	}
	lookup := newNodeLookup(nodes)

	edges := source.createHasAccessEdges([]CloudResourceRow{iamBindingRow("roles/storage.admin", sa)}, lookup, req)
	if len(edges) != 0 {
		t.Fatalf("grant over %d candidates should be skipped by the fan-out cap, got %d edges", maxIAMAccessFanout, len(edges))
	}
}

func TestCreateCacheEndpointEdges(t *testing.T) {
	source, _ := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}

	cacheA := core.NewNode(core.NodeTypeCache, "k:ca", map[string]interface{}{"name": "response-cache", "meta": map[string]interface{}{"host": "10.193.205.204", "port": int64(6379)}}, req.TenantID, req.CloudAccountID, "gcp")
	cacheB := core.NewNode(core.NodeTypeCache, "k:cb", map[string]interface{}{"name": "storage-cache", "meta": map[string]interface{}{"host": "10.40.243.4"}}, req.TenantID, req.CloudAccountID, "gcp")
	// svc references two distinct cache hosts (+ one unknown host that must not match)
	svc := core.NewNode(core.NodeTypeServerlessFunction, "k:svc", map[string]interface{}{"name": "setmore", "meta": map[string]interface{}{
		"redis_endpoints": []interface{}{"10.193.205.204", "10.40.243.4", "10.99.99.99"},
	}}, req.TenantID, req.CloudAccountID, "gcp")
	// svc2 references cacheA twice (dedup) via list repetition
	svc2 := core.NewNode(core.NodeTypeServerlessFunction, "k:svc2", map[string]interface{}{"name": "serviceforge", "meta": map[string]interface{}{
		"redis_endpoints": []interface{}{"10.193.205.204", "10.193.205.204"},
	}}, req.TenantID, req.CloudAccountID, "gcp")
	lookup := newNodeLookup([]*core.DbNode{cacheA, cacheB, svc, svc2})

	edges := source.createCacheEndpointEdges(lookup, req)

	// svc → cacheA, svc → cacheB, svc2 → cacheA = 3 (unknown host + duplicate skipped)
	if len(edges) != 3 {
		t.Fatalf("expected 3 cache endpoint edges, got %d", len(edges))
	}
	for _, e := range edges {
		if e.RelationshipType != core.RelationshipCalls {
			t.Errorf("cache endpoint edge should be CALLS, got %s", e.RelationshipType)
		}
		if e.DestinationNodeID != cacheA.ID && e.DestinationNodeID != cacheB.ID {
			t.Errorf("edge must target a Cache node, got dest %s", e.DestinationNodeID)
		}
		if e.Properties["evidence"] != "redis_env_endpoint" {
			t.Errorf("edge missing redis_env_endpoint provenance")
		}
	}
}

func TestCreateCacheEndpointEdges_NoCaches(t *testing.T) {
	source, _ := NewGCPSource(GCPSourceConfig{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "acct-1"}
	svc := core.NewNode(core.NodeTypeServerlessFunction, "k:svc", map[string]interface{}{"name": "setmore", "meta": map[string]interface{}{
		"redis_endpoints": []interface{}{"10.1.2.3"},
	}}, req.TenantID, req.CloudAccountID, "gcp")
	edges := source.createCacheEndpointEdges(newNodeLookup([]*core.DbNode{svc}), req)
	if len(edges) != 0 {
		t.Fatalf("no cache nodes → no edges, got %d", len(edges))
	}
}
