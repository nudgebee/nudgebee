package k8s

import "nudgebee/services/knowledge_graph/core"

// Concrete-property schemas for the two infra scaffolding nodes minted
// here. createClusterNode stamps only name/subtype (base keys) plus the cloud
// account attributes (environment/region/... — all universal base keys) added
// by enrichK8sNodesWithCloudAttributes, so the Cluster schema declares no
// resource-specific properties. createNamespaceNode adds cluster.
var k8sClusterSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesCluster",
	NodeType:     core.NodeTypeCluster,
	Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "creation_timestamp"},
		{Name: "external_id"},
		{Name: "git_version"},
		{Name: "version_major"},
		{Name: "version_minor"},
		{Name: "go_version"},
		{Name: "compiler"},
		{Name: "platform"},
		{Name: "api_server_url"},
		{Name: "kubeconfig_insecure_skip_tls_verify"},
		{Name: "kubeconfig_has_certificate_authority_data"},
		{Name: "kubeconfig_has_certificate_authority_file"},
		{Name: "kubeconfig_ca_file_path"},
		{Name: "kubeconfig_has_client_certificate"},
		{Name: "kubeconfig_has_client_key"},
		{Name: "kubeconfig_tls_configuration_status"},
	},
}

var k8sNamespaceSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesNamespace",
	NodeType:     core.NodeTypeNamespace,
	Properties: []core.PropertyDef{
		{Name: "cluster", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "creation_timestamp"},
		{Name: "deletion_timestamp"},
		{Name: "status_phase"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(k8sClusterSchema)
	core.RegisterSpecificTypeSchema(k8sNamespaceSchema)
}

// buildNamespaceClusterMap walks the workload slice once to produce a
// namespace → cluster-name lookup plus an account-wide fallback cluster.
// Callers can then resolve a namespace's cluster in O(1) instead of the
// O(M) linear scan getClusterNameForResource does. Mirrors the
// first-choice-same-namespace, then-any-workload semantics of the older
// helper without re-walking the slice per resource.
func buildNamespaceClusterMap(workloads []K8sWorkloadRow) (perNamespace map[string]string, fallback string) {
	perNamespace = make(map[string]string)
	for _, w := range workloads {
		if w.ClusterName == "" {
			continue
		}
		if fallback == "" {
			fallback = w.ClusterName
		}
		if _, present := perNamespace[w.Namespace]; !present {
			perNamespace[w.Namespace] = w.ClusterName
		}
	}
	return perNamespace, fallback
}

// resolveCluster returns the cluster name for a namespace via the
// pre-computed lookup, falling back to the account-wide cluster when the
// namespace has no workloads of its own.
func resolveCluster(perNamespace map[string]string, fallback, namespace string) string {
	if c, ok := perNamespace[namespace]; ok {
		return c
	}
	return fallback
}

// getClusterNameForResource determines the cluster name for a resource by looking at workloads in the same namespace
func (s *K8sSource) getClusterNameForResource(namespace string, workloads []K8sWorkloadRow) string {
	return clusterNameForNamespace(namespace, workloads)
}

// clusterNameForNamespace resolves a cluster name for a namespace-scoped resource.
// First-choice: any workload in the same namespace (carries the matching cluster
// label). Fallback: any workload in this account that has a cluster name set.
// Without the fallback, namespaces that hold no workloads of their own (only
// Services, PVCs, ServiceAccounts) drop their Namespace→Cluster edge and end up
// as orphan nodes (clickhouse, envoy-gateway-system, splunk, mongodb in
// production).
func clusterNameForNamespace(namespace string, workloads []K8sWorkloadRow) string {
	for _, workload := range workloads {
		if workload.Namespace == namespace && workload.ClusterName != "" {
			return workload.ClusterName
		}
	}
	for _, workload := range workloads {
		if workload.ClusterName != "" {
			return workload.ClusterName
		}
	}
	return ""
}

// createClusterNode creates a Cluster node
func (s *K8sSource) createClusterNode(clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":          clusterName,
		"subtype":       "Cluster",
		"specific_type": "KubernetesCluster",
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeCluster,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeCluster, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createNamespaceNode creates a Namespace node
func (s *K8sSource) createNamespaceNode(namespace, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":          namespace,
		"cluster":       clusterName,
		"subtype":       "Namespace",
		"specific_type": "KubernetesNamespace",
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeNamespace,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeNamespace, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}
