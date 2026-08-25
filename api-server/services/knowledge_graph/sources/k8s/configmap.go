package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"sort"
)

// k8sConfigMapSchema is the concrete-property schema for
// KubernetesConfigMap. Names are the node.Properties keys written by
// createK8sConfigMapNode (values are never stored — only key names + counts).
var k8sConfigMapSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesConfigMap",
	NodeType:     core.NodeTypeConfigMap,
	Properties: []core.PropertyDef{
		{Name: "namespace", Indexed: true},
		{Name: "cluster", Indexed: true},
		{Name: "key_count", Indexed: true},
		{Name: "managed_by", Indexed: true},
		{Name: "data_keys"},
		{Name: "binary_data_keys"},
		{Name: "annotations"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sConfigMapSchema) }

// fetchK8sConfigMapsFromRelay fetches every ConfigMap across the cluster
// via the relay's get_resource action. Same shape as the ServiceAccount /
// Service fetches.
func (s *K8sSource) fetchK8sConfigMapsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sConfigMapFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay ConfigMap fetch: cloud_account_id is empty")
		return []K8sConfigMapFromRelay{}, nil
	}

	s.logger.Info("fetching K8s ConfigMaps from relay server",
		"resource_type", "configmaps",
		"account_id", req.CloudAccountID)

	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":          "",
				"version":        "v1",
				"resource_type":  "configmaps",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for ConfigMaps", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for ConfigMaps: %w", err)
	}

	cms, err := s.parseK8sConfigMapsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse ConfigMaps response", "error", err)
		return nil, fmt.Errorf("failed to parse ConfigMaps response: %w", err)
	}

	s.logger.Info("successfully fetched K8s ConfigMaps from relay", "count", len(cms))
	return cms, nil
}

// parseK8sConfigMapsResponse unwraps the relay envelope and decodes each
// ConfigMap. Errors on individual entries are logged and skipped — we don't
// want one malformed CM to fail the whole sync.
func (s *K8sSource) parseK8sConfigMapsResponse(response map[string]interface{}) ([]K8sConfigMapFromRelay, error) {
	cms := make([]K8sConfigMapFromRelay, 0)

	actualDataArray, err := s.parseRelayDataArray(response, "ConfigMaps")
	if err != nil {
		return cms, err
	}

	if len(actualDataArray) == 0 {
		return cms, nil
	}

	for _, cmAny := range actualDataArray {
		cmMap, ok := cmAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid ConfigMap entry")
			continue
		}

		cmBytes, err := json.Marshal(cmMap)
		if err != nil {
			s.logger.Warn("failed to marshal ConfigMap", "error", err)
			continue
		}

		var cm K8sConfigMapFromRelay
		if err := json.Unmarshal(cmBytes, &cm); err != nil {
			s.logger.Warn("failed to unmarshal ConfigMap", "error", err)
			continue
		}

		cms = append(cms, cm)
	}

	return cms, nil
}

// createK8sConfigMapNode builds a ConfigMap DbNode. data/binaryData keys
// are recorded; values are not.
func (s *K8sSource) createK8sConfigMapNode(cm *K8sConfigMapFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":      cm.Metadata.Name,
		"namespace": cm.Metadata.Namespace,
		"cluster":   clusterName,
	}
	keyCount := len(cm.Data) + len(cm.BinaryData)
	properties["key_count"] = keyCount
	if len(cm.Data) > 0 {
		keys := make([]string, 0, len(cm.Data))
		for k := range cm.Data {
			keys = append(keys, k)
		}
		// Go map iteration is non-deterministic; sort so the property is
		// byte-stable across syncs and the upsert doesn't churn the row
		// every build.
		sort.Strings(keys)
		properties["data_keys"] = keys
	}
	if len(cm.BinaryData) > 0 {
		keys := make([]string, 0, len(cm.BinaryData))
		for k := range cm.BinaryData {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		properties["binary_data_keys"] = keys
	}
	if len(cm.Metadata.Labels) > 0 {
		properties["labels"] = cm.Metadata.Labels
		if mb, ok := cm.Metadata.Labels[helmManagedByLabel].(string); ok && mb != "" {
			properties["managed_by"] = mb
		}
	}
	if len(cm.Metadata.Annotations) > 0 {
		properties["annotations"] = cm.Metadata.Annotations
	}

	properties["specific_type"] = "KubernetesConfigMap"

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeConfigMap,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeConfigMap, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// convertK8sConfigMapsToGraph emits ConfigMap nodes and BELONGS_TO edges to
// their owning namespace. Returns a lookup map keyed by "namespace/name"
// for the Workload → USES_CONFIG → ConfigMap wiring downstream.
func (s *K8sSource) convertK8sConfigMapsToGraph(cms []K8sConfigMapFromRelay, workloads []K8sWorkloadRow, clusterNodes, namespaceNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0, len(cms))
	edges := make([]*core.DbEdge, 0)
	byKey := make(map[string]*core.DbNode, len(cms))

	// Pre-compute namespace → cluster lookup so the per-CM cluster
	// resolution is O(1) instead of O(workloads). Cluster name is needed
	// twice per CM (node properties + namespace edge key).
	perNamespace, fallback := buildNamespaceClusterMap(workloads)

	for i := range cms {
		cm := &cms[i]
		clusterName := resolveCluster(perNamespace, fallback, cm.Metadata.Namespace)
		node := s.createK8sConfigMapNode(cm, clusterName, req)
		nodes = append(nodes, node)
		byKey[fmt.Sprintf("%s/%s", cm.Metadata.Namespace, cm.Metadata.Name)] = node

		nsNode, nsNodes, nsEdges := s.ensureNamespaceNode(cm.Metadata.Namespace, clusterName, namespaceNodes, clusterNodes, req)
		nodes = append(nodes, nsNodes...)
		edges = append(edges, nsEdges...)
		edges = append(edges, core.NewEdge(
			node.ID, nsNode.ID,
			core.RelationshipBelongsTo,
			map[string]interface{}{"connection_type": "namespace_membership"},
			req.TenantID, req.CloudAccountID, "k8s",
		))
	}

	return nodes, edges, byKey
}
