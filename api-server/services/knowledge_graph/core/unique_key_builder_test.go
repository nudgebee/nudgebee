package core

import "testing"

func TestDeriveCloudProvider(t *testing.T) {
	tests := []struct {
		name     string
		observer string
		nodeType NodeType
		want     string
	}{
		{"aws static observer + Database", "aws", NodeTypeDatabase, CloudProviderAWS},
		{"aws static observer + Pod", "aws", NodeTypePod, CloudProviderAWS},
		{"k8s static observer + Workload", "k8s", NodeTypeWorkload, CloudProviderK8s},
		{"gcp static observer + ComputeInstance", "gcp", NodeTypeComputeInstance, CloudProviderGCP},
		{"azure static observer + LoadBalancer", "azure", NodeTypeLoadBalancer, CloudProviderAzure},

		{"ebpf observer + Pod → k8s", "ebpf", NodeTypePod, CloudProviderK8s},
		{"ebpf observer + Workload → k8s", "ebpf", NodeTypeWorkload, CloudProviderK8s},
		{"ebpf observer + K8sService → k8s", "ebpf", NodeTypeK8sService, CloudProviderK8s},
		{"ebpf observer + Cluster → k8s", "ebpf", NodeTypeCluster, CloudProviderK8s},
		{"ebpf observer + Namespace → k8s", "ebpf", NodeTypeNamespace, CloudProviderK8s},
		{"ebpf observer + Node → k8s", "ebpf", NodeTypeNode, CloudProviderK8s},
		{"ebpf observer + ExternalService → external", "ebpf", NodeTypeExternalService, CloudProviderExternal},
		{"ebpf observer + Database → external (observer can't tell)", "ebpf", NodeTypeDatabase, CloudProviderExternal},

		{"traces observer + K8sService → k8s", "traces", NodeTypeK8sService, CloudProviderK8s},
		{"traces observer + Workload → k8s", "traces", NodeTypeWorkload, CloudProviderK8s},
		{"traces observer + ExternalService → external", "traces", NodeTypeExternalService, CloudProviderExternal},
		{"traces observer + Service → external", "traces", NodeTypeService, CloudProviderExternal},

		{"datadog-apm observer + Workload → k8s", "datadog-apm", NodeTypeWorkload, CloudProviderK8s},
		{"datadog-apm observer + Service → external", "datadog-apm", NodeTypeService, CloudProviderExternal},

		{"cloud observer fallback → external", "cloud", NodeTypeDatabase, CloudProviderExternal},
		{"cloud observer K8s nodetype → k8s", "cloud", NodeTypePod, CloudProviderK8s},

		{"unknown observer + K8s nodetype → k8s", "mock", NodeTypeWorkload, CloudProviderK8s},
		{"unknown observer + non-K8s nodetype → external", "mock", NodeTypeService, CloudProviderExternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveCloudProvider(tt.observer, tt.nodeType)
			if got != tt.want {
				t.Errorf("DeriveCloudProvider(%q, %q) = %q, want %q", tt.observer, tt.nodeType, got, tt.want)
			}
		})
	}
}

func TestBuildUniqueKey_CloudProviderInPositionZero(t *testing.T) {
	key := BuildUniqueKey(CloudProviderAWS, "acc-1", "us-east-1", NodeTypeDatabase, "vpc-x", "prod-db")
	want := "aws:acc-1:us-east-1:Database:vpc-x:prod-db"
	if key != want {
		t.Errorf("BuildUniqueKey() = %q, want %q", key, want)
	}

	if got := GetCloudProvider(key); got != CloudProviderAWS {
		t.Errorf("GetCloudProvider(%q) = %q, want %q", key, got, CloudProviderAWS)
	}
}

func TestUniqueKeyComponents_ValidateRequiresCloudProvider(t *testing.T) {
	c := &UniqueKeyComponents{
		NodeType: NodeTypeService,
		Name:     "x",
	}
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() returned nil, want error for missing cloud_provider")
	}

	c.CloudProvider = CloudProviderAWS
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() returned %v, want nil after setting cloud_provider", err)
	}
}

// TestCrossProviderDeduplication asserts that two observers producing the same
// physical resource (same cloud_provider:account:location:NodeType:hierarchy:name)
// produce the same UniqueKey, so DeduplicateNodes merges them into one.
func TestCrossProviderDeduplication(t *testing.T) {
	awsKey := BuildUniqueKey(CloudProviderAWS, "acc1", "us-east-1", NodeTypeDatabase, "vpc-x", "prod-db")
	enrichmentKey := BuildUniqueKey(CloudProviderAWS, "acc1", "us-east-1", NodeTypeDatabase, "vpc-x", "prod-db")

	if awsKey != enrichmentKey {
		t.Fatalf("expected identical keys for same physical resource: aws=%q enrichment=%q", awsKey, enrichmentKey)
	}

	awsNode := NewNode(NodeTypeDatabase, awsKey, map[string]interface{}{"name": "prod-db"}, "tenant-1", "acc1", "aws")
	enrichmentNode := NewNode(NodeTypeDatabase, enrichmentKey, map[string]interface{}{"name": "prod-db", "engine": "postgres"}, "tenant-1", "acc1", "cloud")

	if awsNode.ID != enrichmentNode.ID {
		t.Errorf("expected same ID for same UniqueKey+tenant+account: aws=%q cloud=%q", awsNode.ID, enrichmentNode.ID)
	}

	deduped := DeduplicateNodes([]*DbNode{awsNode, enrichmentNode})
	if len(deduped) != 1 {
		t.Errorf("expected 1 surviving node, got %d", len(deduped))
	}
	if val := deduped[0].Properties["engine"]; val != "postgres" {
		t.Errorf("expected merged property engine=postgres, got %v", val)
	}
}

// TestCrossFlowSourceWorkloadDeduplication verifies that ebpf and traces
// observing the same K8s Workload produce the same key (cloud_provider="k8s")
// and merge during dedup.
func TestCrossFlowSourceWorkloadDeduplication(t *testing.T) {
	ebpfProvider := DeriveCloudProvider("ebpf", NodeTypeWorkload)
	tracesProvider := DeriveCloudProvider("traces", NodeTypeWorkload)
	if ebpfProvider != CloudProviderK8s || tracesProvider != CloudProviderK8s {
		t.Fatalf("expected both ebpf and traces to map K8s Workload to k8s; got ebpf=%s traces=%s", ebpfProvider, tracesProvider)
	}

	ebpfKey := BuildUniqueKey(ebpfProvider, "cluster1", "us-east-1", NodeTypeWorkload, "default", "checkout-svc")
	tracesKey := BuildUniqueKey(tracesProvider, "cluster1", "us-east-1", NodeTypeWorkload, "default", "checkout-svc")

	if ebpfKey != tracesKey {
		t.Errorf("expected identical keys for same workload: ebpf=%q traces=%q", ebpfKey, tracesKey)
	}
}

// TestExternalServiceCrossFlowSource asserts ebpf and traces observing the same
// external endpoint name converge on the same key.
func TestExternalServiceCrossFlowSource(t *testing.T) {
	ebpfProvider := DeriveCloudProvider("ebpf", NodeTypeExternalService)
	tracesProvider := DeriveCloudProvider("traces", NodeTypeExternalService)
	if ebpfProvider != CloudProviderExternal || tracesProvider != CloudProviderExternal {
		t.Fatalf("expected both observers to map ExternalService to external; got ebpf=%s traces=%s", ebpfProvider, tracesProvider)
	}

	ebpfKey := BuildUniqueKey(ebpfProvider, "tenant-acc", "", NodeTypeExternalService, "", "redis.external.com")
	tracesKey := BuildUniqueKey(tracesProvider, "tenant-acc", "", NodeTypeExternalService, "", "redis.external.com")
	if ebpfKey != tracesKey {
		t.Errorf("expected identical external-service keys: ebpf=%q traces=%q", ebpfKey, tracesKey)
	}
}
