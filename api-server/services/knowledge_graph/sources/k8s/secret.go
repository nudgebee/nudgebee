package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"sort"
)

// k8sSecretSchema is the concrete-property schema for KubernetesSecret.
// Names are the node.Properties keys written by createK8sSecretNode (data
// values are never stored — only key names + counts + type). The ontology
// reads namespace/cluster/secret_type.
var k8sSecretSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesSecret",
	NodeType:     core.NodeTypeK8sSecret,
	Properties: []core.PropertyDef{
		{Name: "namespace", Indexed: true},
		{Name: "cluster", Indexed: true},
		{Name: "secret_type", Indexed: true},
		{Name: "key_count", Indexed: true},
		{Name: "managed_by", Indexed: true},
		{Name: "data_keys"},
		{Name: "annotations"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "composite_id"},
		{Name: "creation_timestamp"},
		{Name: "deletion_timestamp"},
		{Name: "owner_references"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sSecretSchema) }

// fetchK8sSecretsFromRelay fetches every Secret across the cluster, drops
// helm release state, and returns metadata-only records (no .data values
// land in the KG payload).
func (s *K8sSource) fetchK8sSecretsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sSecretFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay Secret fetch: cloud_account_id is empty")
		return []K8sSecretFromRelay{}, nil
	}

	s.logger.Info("fetching K8s Secrets from relay server",
		"resource_type", "secrets",
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
				"resource_type":  "secrets",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for Secrets", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for Secrets: %w", err)
	}

	secrets, dropped, err := s.parseK8sSecretsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse Secrets response", "error", err)
		return nil, fmt.Errorf("failed to parse Secrets response: %w", err)
	}

	s.logger.Info("successfully fetched K8s Secrets from relay",
		"count", len(secrets), "dropped_helm_release_state", dropped)
	return secrets, nil
}

// parseK8sSecretsResponse unwraps the relay envelope, drops Helm release
// state, strips .Data values, and returns the surviving secrets plus the
// drop count for logging. Each surviving secret keeps Data set to a map
// with key→nil so downstream code can count keys without holding values.
func (s *K8sSource) parseK8sSecretsResponse(response map[string]interface{}) ([]K8sSecretFromRelay, int, error) {
	secrets := make([]K8sSecretFromRelay, 0)
	dropped := 0

	actualDataArray, err := s.parseRelayDataArray(response, "Secrets")
	if err != nil {
		return secrets, dropped, err
	}

	if len(actualDataArray) == 0 {
		return secrets, dropped, nil
	}

	for _, secAny := range actualDataArray {
		secMap, ok := secAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid Secret entry")
			continue
		}

		secBytes, err := json.Marshal(secMap)
		if err != nil {
			s.logger.Warn("failed to marshal Secret", "error", err)
			continue
		}

		var sec K8sSecretFromRelay
		if err := json.Unmarshal(secBytes, &sec); err != nil {
			s.logger.Warn("failed to unmarshal Secret", "error", err)
			continue
		}

		// Drop Helm per-release state secrets — they're not credentials
		// and they dominate the count.
		if sec.Type == helmReleaseSecretType {
			dropped++
			continue
		}

		// Replace .Data values with nil placeholders so downstream code
		// can iterate the keys but never touches the values. This is
		// belt-and-suspenders: the node-creation path also never emits
		// these values, but stripping them here means a stray log line
		// or panic dump can't leak the material either.
		if len(sec.Data) > 0 {
			stripped := make(map[string]interface{}, len(sec.Data))
			for k := range sec.Data {
				stripped[k] = nil
			}
			sec.Data = stripped
		}

		secrets = append(secrets, sec)
	}

	return secrets, dropped, nil
}

// createK8sSecretNode builds a K8sSecret DbNode. data values are not stored;
// only the key names + the Type + metadata land in the graph.
func (s *K8sSource) createK8sSecretNode(sec *K8sSecretFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":        sec.Metadata.Name,
		"namespace":   sec.Metadata.Namespace,
		"cluster":     clusterName,
		"secret_type": sec.Type,
	}
	properties["key_count"] = len(sec.Data)
	if len(sec.Data) > 0 {
		keys := make([]string, 0, len(sec.Data))
		for k := range sec.Data {
			keys = append(keys, k)
		}
		// Go map iteration is non-deterministic; sort so the property is
		// byte-stable across syncs and the upsert doesn't churn the row.
		sort.Strings(keys)
		properties["data_keys"] = keys
	}
	if len(sec.Metadata.Labels) > 0 {
		properties["labels"] = sec.Metadata.Labels
		if mb, ok := sec.Metadata.Labels[helmManagedByLabel].(string); ok && mb != "" {
			properties["managed_by"] = mb
		}
	}
	if len(sec.Metadata.Annotations) > 0 {
		properties["annotations"] = sec.Metadata.Annotations
	}

	properties["specific_type"] = "KubernetesSecret"

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeK8sSecret,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeK8sSecret, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// convertK8sSecretsToGraph emits K8sSecret nodes and BELONGS_TO edges to
// their owning namespace. Returns the lookup map for Workload →
// USES_SECRET wiring.
func (s *K8sSource) convertK8sSecretsToGraph(secrets []K8sSecretFromRelay, workloads []K8sWorkloadRow, namespaceNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0, len(secrets))
	edges := make([]*core.DbEdge, 0)
	byKey := make(map[string]*core.DbNode, len(secrets))

	// Pre-compute namespace → cluster lookup; see
	// convertK8sConfigMapsToGraph for the rationale.
	perNamespace, fallback := buildNamespaceClusterMap(workloads)

	for i := range secrets {
		sec := &secrets[i]
		clusterName := resolveCluster(perNamespace, fallback, sec.Metadata.Namespace)
		node := s.createK8sSecretNode(sec, clusterName, req)
		nodes = append(nodes, node)
		byKey[fmt.Sprintf("%s/%s", sec.Metadata.Namespace, sec.Metadata.Name)] = node

		namespaceKey := fmt.Sprintf("%s/%s", clusterName, sec.Metadata.Namespace)
		if nsNode, ok := namespaceNodes[namespaceKey]; ok {
			edges = append(edges, core.NewEdge(
				node.ID, nsNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{"connection_type": "namespace_membership"},
				req.TenantID, req.CloudAccountID, "k8s",
			))
		}
	}

	return nodes, edges, byKey
}
