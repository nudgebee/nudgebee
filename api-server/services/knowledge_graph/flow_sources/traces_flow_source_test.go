package flow_sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

func TestNewTracesFlowSource(t *testing.T) {
	logger := slog.Default()
	source := NewTracesFlowSource(logger)

	if source == nil {
		t.Fatal("NewTracesFlowSource() returned nil")
		return
	}

	if source.GetName() != "traces" {
		t.Errorf("GetName() = %v, want %v", source.GetName(), "traces")
	}

	if source.GetSourceCategory() != core.FlowSourceCategoryTracing {
		t.Errorf("GetSourceCategory() = %v, want %v", source.GetSourceCategory(), core.FlowSourceCategoryTracing)
	}

	if !source.IsEnabled() {
		t.Error("IsEnabled() = false, want true")
	}

	// NOTE: cloudEnricher is now handled centrally in core/service.go
	// External service enrichment happens after all flow sources complete
}

func TestTracesFlowSource_Validate(t *testing.T) {
	logger := slog.Default()
	source := NewTracesFlowSource(logger)

	err := source.Validate()
	if err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestTracesFlowSource_MatchAWSHostnameToNode(t *testing.T) {
	logger := slog.Default()
	source := NewTracesFlowSource(logger)

	// Create test nodes that simulate AWS resources
	existingNodes := []*core.DbNode{
		{
			UniqueKey: "aws:account1::LoadBalancer::my-alb",
			NodeType:  core.NodeTypeLoadBalancer,
			Properties: map[string]interface{}{
				"name":     "my-alb",
				"dns_name": "af0cda30e9e064065bc19f0b2abcc1e9-b0011dc7589f04d1.elb.us-east-1.amazonaws.com",
			},
			CloudAccountID: "account1",
		},
		{
			UniqueKey: "aws:account1::Database::my-rds",
			NodeType:  core.NodeTypeDatabase,
			Properties: map[string]interface{}{
				"name":     "my-rds",
				"dns_name": "mydb.abc123.us-east-1.rds.amazonaws.com",
			},
			CloudAccountID: "account1",
		},
		{
			UniqueKey: "aws:account1::Cache::my-redis",
			NodeType:  core.NodeTypeCache,
			Properties: map[string]interface{}{
				"name":     "my-redis",
				"dns_name": "mycluster.abc123.cache.amazonaws.com",
			},
			CloudAccountID: "account1",
		},
	}

	// Initialize node matcher with existing nodes
	source.InitializeNodeMatcher(existingNodes)

	tests := []struct {
		name         string
		hostname     string
		k8sAccountID string
		wantMatch    bool
		wantNodeKey  string
	}{
		{
			name:         "match ELB by dns_name",
			hostname:     "af0cda30e9e064065bc19f0b2abcc1e9-b0011dc7589f04d1.elb.us-east-1.amazonaws.com",
			k8sAccountID: "account1",
			wantMatch:    true,
			wantNodeKey:  "aws:account1::LoadBalancer::my-alb",
		},
		{
			name:         "match RDS by dns_name",
			hostname:     "mydb.abc123.us-east-1.rds.amazonaws.com",
			k8sAccountID: "account1",
			wantMatch:    true,
			wantNodeKey:  "aws:account1::Database::my-rds",
		},
		{
			name:         "match ElastiCache by dns_name",
			hostname:     "mycluster.abc123.cache.amazonaws.com",
			k8sAccountID: "account1",
			wantMatch:    true,
			wantNodeKey:  "aws:account1::Cache::my-redis",
		},
		{
			name:         "no match for non-AWS hostname",
			hostname:     "my-service.example.com",
			k8sAccountID: "account1",
			wantMatch:    false,
			wantNodeKey:  "",
		},
		{
			name:         "no match for unknown AWS resource",
			hostname:     "unknown.xyz123.us-east-1.amazonaws.com",
			k8sAccountID: "account1",
			wantMatch:    false,
			wantNodeKey:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := source.matchAWSHostnameToNode(tt.hostname, tt.k8sAccountID)

			if tt.wantMatch {
				if result == nil {
					t.Errorf("matchAWSHostnameToNode(%q) = nil, want node with key %q", tt.hostname, tt.wantNodeKey)
					return
				}
				if result.UniqueKey != tt.wantNodeKey {
					t.Errorf("matchAWSHostnameToNode(%q) = %q, want %q", tt.hostname, result.UniqueKey, tt.wantNodeKey)
				}
			} else {
				if result != nil {
					t.Errorf("matchAWSHostnameToNode(%q) = %q, want nil", tt.hostname, result.UniqueKey)
				}
			}
		})
	}
}

func TestParseK8sServiceDNS(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		wantServiceName   string
		wantNamespace     string
		wantClusterDomain string
		wantIsPodDNS      bool
		wantPodName       string
		wantNil           bool
	}{
		{
			name:              "standard svc.cluster.local format",
			input:             "ocean-service.ocean-service-new.svc.cluster.local",
			wantServiceName:   "ocean-service",
			wantNamespace:     "ocean-service-new",
			wantClusterDomain: "svc.cluster.local",
			wantIsPodDNS:      false,
			wantNil:           false,
		},
		{
			name:              "short service.namespace.svc format",
			input:             "redis.default.svc",
			wantServiceName:   "redis",
			wantNamespace:     "default",
			wantClusterDomain: "svc",
			wantIsPodDNS:      false,
			wantNil:           false,
		},
		{
			name:              "headless service pod DNS format",
			input:             "pod-0.redis-headless.default.svc.cluster.local",
			wantServiceName:   "redis-headless",
			wantNamespace:     "default",
			wantClusterDomain: "svc.cluster.local",
			wantIsPodDNS:      true,
			wantPodName:       "pod-0",
			wantNil:           false,
		},
		{
			name:              "known namespace short form",
			input:             "my-service.kube-system",
			wantServiceName:   "my-service",
			wantNamespace:     "kube-system",
			wantClusterDomain: "",
			wantIsPodDNS:      false,
			wantNil:           false,
		},
		{
			name:              "pod.cluster.local format",
			input:             "10-244-0-5.default.pod.cluster.local",
			wantServiceName:   "",
			wantNamespace:     "default",
			wantClusterDomain: "pod.cluster.local",
			wantIsPodDNS:      true,
			wantPodName:       "10-244-0-5",
			wantNil:           false,
		},
		{
			name:    "external domain - should return nil",
			input:   "api.github.com",
			wantNil: true,
		},
		{
			name:    "AWS endpoint - should return nil",
			input:   "my-db.abc123.us-east-1.rds.amazonaws.com",
			wantNil: true,
		},
		{
			name:    "empty string - should return nil",
			input:   "",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseK8sServiceDNS(tt.input)

			if tt.wantNil {
				if result != nil {
					t.Errorf("parseK8sServiceDNS(%q) = %+v, want nil", tt.input, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("parseK8sServiceDNS(%q) = nil, want non-nil", tt.input)
				return
			}

			if result.ServiceName != tt.wantServiceName {
				t.Errorf("ServiceName = %q, want %q", result.ServiceName, tt.wantServiceName)
			}
			if result.Namespace != tt.wantNamespace {
				t.Errorf("Namespace = %q, want %q", result.Namespace, tt.wantNamespace)
			}
			if result.ClusterDomain != tt.wantClusterDomain {
				t.Errorf("ClusterDomain = %q, want %q", result.ClusterDomain, tt.wantClusterDomain)
			}
			if result.IsPodDNS != tt.wantIsPodDNS {
				t.Errorf("IsPodDNS = %v, want %v", result.IsPodDNS, tt.wantIsPodDNS)
			}
			if result.PodName != tt.wantPodName {
				t.Errorf("PodName = %q, want %q", result.PodName, tt.wantPodName)
			}
		})
	}
}

func TestIsK8sInternalDNS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "svc.cluster.local suffix",
			input: "my-service.my-namespace.svc.cluster.local",
			want:  true,
		},
		{
			name:  "pod.cluster.local suffix",
			input: "10-244-0-5.default.pod.cluster.local",
			want:  true,
		},
		{
			name:  "short .svc suffix",
			input: "redis.default.svc",
			want:  true,
		},
		{
			name:  "known namespace - kube-system",
			input: "coredns.kube-system",
			want:  true,
		},
		{
			name:  "known namespace - monitoring",
			input: "prometheus.monitoring",
			want:  true,
		},
		{
			name:  "known namespace - istio-system",
			input: "istiod.istio-system",
			want:  true,
		},
		{
			name:  "external domain",
			input: "api.github.com",
			want:  false,
		},
		{
			name:  "AWS RDS endpoint",
			input: "mydb.abc123.us-east-1.rds.amazonaws.com",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isK8sInternalDNS(tt.input)
			if result != tt.want {
				t.Errorf("isK8sInternalDNS(%q) = %v, want %v", tt.input, result, tt.want)
			}
		})
	}
}

func TestTracesFlowSource_MatchK8sInternalDNSToNode(t *testing.T) {
	logger := slog.Default()
	source := NewTracesFlowSource(logger)

	// Create test nodes that simulate K8s services and workloads
	existingNodes := []*core.DbNode{
		{
			UniqueKey: "k8s:cluster1:ocean-service-new:K8sService::ocean-service",
			NodeType:  core.NodeTypeK8sService,
			Properties: map[string]interface{}{
				"name":      "ocean-service",
				"namespace": "ocean-service-new",
			},
			CloudAccountID: "cluster1",
		},
		{
			UniqueKey: "k8s:cluster1:default:Workload::redis",
			NodeType:  core.NodeTypeWorkload,
			Properties: map[string]interface{}{
				"name":      "redis",
				"namespace": "default",
			},
			CloudAccountID: "cluster1",
		},
		{
			UniqueKey: "k8s:cluster1:monitoring:K8sService::prometheus",
			NodeType:  core.NodeTypeK8sService,
			Properties: map[string]interface{}{
				"name":      "prometheus",
				"namespace": "monitoring",
			},
			CloudAccountID: "cluster1",
		},
	}

	// Initialize node matcher with existing nodes
	source.InitializeNodeMatcher(existingNodes)

	tests := []struct {
		name         string
		dnsName      string
		k8sAccountID string
		wantMatch    bool
		wantNodeKey  string
	}{
		{
			name:         "match K8s service by svc.cluster.local DNS",
			dnsName:      "ocean-service.ocean-service-new.svc.cluster.local",
			k8sAccountID: "cluster1",
			wantMatch:    true,
			wantNodeKey:  "k8s:cluster1:ocean-service-new:K8sService::ocean-service",
		},
		{
			name:         "match Workload by short svc format",
			dnsName:      "redis.default.svc",
			k8sAccountID: "cluster1",
			wantMatch:    true,
			wantNodeKey:  "k8s:cluster1:default:Workload::redis",
		},
		{
			name:         "match K8s service by short form with known namespace",
			dnsName:      "prometheus.monitoring",
			k8sAccountID: "cluster1",
			wantMatch:    true,
			wantNodeKey:  "k8s:cluster1:monitoring:K8sService::prometheus",
		},
		{
			name:         "no match for external domain",
			dnsName:      "api.github.com",
			k8sAccountID: "cluster1",
			wantMatch:    false,
			wantNodeKey:  "",
		},
		{
			name:         "no match for non-existent service",
			dnsName:      "unknown-service.unknown-namespace.svc.cluster.local",
			k8sAccountID: "cluster1",
			wantMatch:    false,
			wantNodeKey:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := source.matchK8sInternalDNSToNode(tt.dnsName, tt.k8sAccountID)

			if tt.wantMatch {
				if result == nil {
					t.Errorf("matchK8sInternalDNSToNode(%q) = nil, want node with key %q", tt.dnsName, tt.wantNodeKey)
					return
				}
				if result.UniqueKey != tt.wantNodeKey {
					t.Errorf("matchK8sInternalDNSToNode(%q) = %q, want %q", tt.dnsName, result.UniqueKey, tt.wantNodeKey)
				}
			} else {
				if result != nil {
					t.Errorf("matchK8sInternalDNSToNode(%q) = %q, want nil", tt.dnsName, result.UniqueKey)
				}
			}
		})
	}
}

// NOTE: The main business logic for traces flow source is now in:
// 1. traces.FetchTracesAndBuildServiceMap() - building the service map from traces
// 2. ConvertServiceMapToGraph() - converting service map to graph nodes/edges
//
// External service enrichment is now centralized in core/service.go:
// - CentralizedExternalServiceEnricher.EnrichExternalServices() - runs after all flow sources complete
// - This allows cross-source node matching (e.g., eBPF and traces can share nodes)
//
// Those components have their own comprehensive tests:
// - traces package: traces/*_test.go
// - cloud enrichment: cloud_enrichment_test.go (to be added)
// - service map conversion: service_map_converter_test.go (to be added)
//
// Integration tests for the full flow should be added to knowledge_graph/integration_test.go
