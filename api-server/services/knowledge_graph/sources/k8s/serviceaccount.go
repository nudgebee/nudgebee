package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
)

// k8sServiceAccountSchema is the concrete-property schema for
// KubernetesServiceAccount. Names are the node.Properties keys written by
// createK8sServiceAccountNode; the cloud-identity-binding fields (role_arn,
// gcp_service_account_email) are hoisted for the IRSA / Workload-Identity
// cross-account matchers.
var k8sServiceAccountSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesServiceAccount",
	NodeType:     core.NodeTypeK8sServiceAccount,
	Properties: []core.PropertyDef{
		{Name: "namespace", Indexed: true},
		{Name: "cluster", Indexed: true},
		{Name: "role_arn", Indexed: true},
		{Name: "gcp_service_account_email"},
		{Name: "annotations"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "automount_service_account_token"},
		{Name: "aws_role_arn"},
		{Name: "gcp_service_account"},
		{Name: "uid"},
		{Name: "creation_timestamp"},
		{Name: "resource_version"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sServiceAccountSchema) }

// fetchK8sServiceAccountsFromRelay fetches K8s ServiceAccounts via the relay's
// generic get_resource action. Same shape as fetchK8sPVCsFromRelay — only the
// resource_type differs. Powers the IRSA chain (SA carries the
// eks.amazonaws.com/role-arn annotation that ties a workload to an IAM role).
func (s *K8sSource) fetchK8sServiceAccountsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sServiceAccountFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay ServiceAccount fetch: cloud_account_id is empty")
		return []K8sServiceAccountFromRelay{}, nil
	}

	s.logger.Info("fetching K8s ServiceAccounts from relay server",
		"resource_type", "serviceaccounts",
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
				"resource_type":  "serviceaccounts",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for ServiceAccounts", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for ServiceAccounts: %w", err)
	}

	sas, err := s.parseK8sServiceAccountsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse ServiceAccounts response", "error", err)
		return nil, fmt.Errorf("failed to parse ServiceAccounts response: %w", err)
	}

	s.logger.Info("successfully fetched K8s ServiceAccounts from relay", "count", len(sas))
	return sas, nil
}

// parseK8sServiceAccountsResponse parses the relay response for K8s ServiceAccounts.
func (s *K8sSource) parseK8sServiceAccountsResponse(response map[string]interface{}) ([]K8sServiceAccountFromRelay, error) {
	sas := make([]K8sServiceAccountFromRelay, 0)

	actualDataArray, err := s.parseRelayDataArray(response, "ServiceAccounts")
	if err != nil {
		return sas, err
	}

	if len(actualDataArray) == 0 {
		return sas, nil
	}

	for _, saAny := range actualDataArray {
		saMap, ok := saAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid ServiceAccount entry")
			continue
		}

		saBytes, err := json.Marshal(saMap)
		if err != nil {
			s.logger.Warn("failed to marshal ServiceAccount", "error", err)
			continue
		}

		var sa K8sServiceAccountFromRelay
		if err := json.Unmarshal(saBytes, &sa); err != nil {
			s.logger.Warn("failed to unmarshal ServiceAccount", "error", err)
			continue
		}

		sas = append(sas, sa)
	}

	return sas, nil
}

// createK8sServiceAccountNode builds a ServiceAccount DbNode from a relay-fetched
// SA. Cloud-identity-binding annotations are hoisted to top-level properties
// so phase-3 cross-account matchers don't have to walk the annotation map.
func (s *K8sSource) createK8sServiceAccountNode(sa *K8sServiceAccountFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":      sa.Metadata.Name,
		"namespace": sa.Metadata.Namespace,
		"cluster":   clusterName,
	}
	if len(sa.Metadata.Labels) > 0 {
		properties["labels"] = sa.Metadata.Labels
	}
	if len(sa.Metadata.Annotations) > 0 {
		properties["annotations"] = sa.Metadata.Annotations
		if roleArnAny, ok := sa.Metadata.Annotations[IRSAAnnotation]; ok {
			if roleArn, ok := roleArnAny.(string); ok && roleArn != "" {
				properties["role_arn"] = roleArn
			}
		}
		if gcpSAAny, ok := sa.Metadata.Annotations[GKEWorkloadIdentityAnnotation]; ok {
			if gcpSA, ok := gcpSAAny.(string); ok && gcpSA != "" {
				properties["gcp_service_account_email"] = gcpSA
			}
		}
	}

	properties["specific_type"] = "KubernetesServiceAccount"

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeK8sServiceAccount,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeK8sServiceAccount, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// convertK8sServiceAccountsToGraph emits ServiceAccount nodes + BELONGS_TO edges
// to their owning namespace. Returns the SA lookup map keyed by
// "namespace/name" — used by createWorkloadServiceAccountEdges to wire
// Workload → USES_SERVICE_ACCOUNT → ServiceAccount.
//
// The cross-account SA → ASSUMES → ServiceIdentity edge is NOT emitted here —
// that hop is wired by the phase-3 cross-account rules engine via
// default_relationships.json (rule: k8s_serviceaccount_to_aws_iam_role_irsa),
// because per-source lookup.ByARN cannot see the AWS source's ServiceIdentity
// nodes at this stage.
func (s *K8sSource) convertK8sServiceAccountsToGraph(sas []K8sServiceAccountFromRelay, workloads []K8sWorkloadRow, namespaceNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0, len(sas))
	edges := make([]*core.DbEdge, 0)
	saByKey := make(map[string]*core.DbNode, len(sas)) // "namespace/name" -> SA node

	for i := range sas {
		sa := &sas[i]
		clusterName := s.getClusterNameForResource(sa.Metadata.Namespace, workloads)
		saNode := s.createK8sServiceAccountNode(sa, clusterName, req)
		nodes = append(nodes, saNode)
		saByKey[fmt.Sprintf("%s/%s", sa.Metadata.Namespace, sa.Metadata.Name)] = saNode

		// Link SA to its namespace if we have one.
		namespaceKey := fmt.Sprintf("%s/%s", clusterName, sa.Metadata.Namespace)
		if nsNode, ok := namespaceNodes[namespaceKey]; ok {
			edges = append(edges, core.NewEdge(
				saNode.ID, nsNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{"connection_type": "namespace_membership"},
				req.TenantID, req.CloudAccountID, "k8s",
			))
		}
	}

	return nodes, edges, saByKey
}

// createWorkloadServiceAccountEdges emits Workload → USES_SERVICE_ACCOUNT →
// ServiceAccount edges for every workload whose properties.service_account_name
// resolves to a known SA in the per-build lookup. SAs absent from the lookup
// (eg implicit `default` SAs that kubectl didn't return) are skipped silently.
func (s *K8sSource) createWorkloadServiceAccountEdges(workloadNodes map[string]*core.DbNode, saByKey map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	if len(saByKey) == 0 || len(workloadNodes) == 0 {
		return nil
	}
	edges := make([]*core.DbEdge, 0)
	for _, w := range workloadNodes {
		if w == nil {
			continue
		}
		saName, _ := core.GetNodePropertyString(w, "service_account_name")
		if saName == "" {
			continue
		}
		namespace, _ := core.GetNodePropertyString(w, "namespace")
		if namespace == "" {
			continue
		}
		saNode, ok := saByKey[fmt.Sprintf("%s/%s", namespace, saName)]
		if !ok {
			continue
		}
		edges = append(edges, core.NewEdge(
			w.ID, saNode.ID,
			core.RelationshipUsesServiceAccount,
			map[string]interface{}{
				"connection_type":      "service_account",
				"service_account_name": saName,
			},
			req.TenantID, req.CloudAccountID, "k8s",
		))
	}
	return edges
}

// a different collector version uses the raw shape.
//
// Returns "" when no SA can be resolved — in that case
// createWorkloadServiceAccountEdges skips the Workload→SA edge.
func extractServiceAccountName(metaMap map[string]interface{}, kind string) string {
	if metaMap == nil {
		return ""
	}
	// Preferred path: collector-flattened `config.service_account`.
	if cfg, ok := metaMap["config"].(map[string]interface{}); ok {
		if san, ok := cfg["service_account"].(string); ok && san != "" {
			return san
		}
	}
	// Fallback paths: raw K8s API shape, in case collector format changes.
	spec, _ := metaMap["spec"].(map[string]interface{})
	if spec == nil {
		return ""
	}
	var podSpec map[string]interface{}
	switch kind {
	case "Pod":
		podSpec = spec
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		if tmpl, ok := spec["template"].(map[string]interface{}); ok {
			podSpec, _ = tmpl["spec"].(map[string]interface{})
		}
	case "CronJob":
		if jt, ok := spec["jobTemplate"].(map[string]interface{}); ok {
			if jtSpec, ok := jt["spec"].(map[string]interface{}); ok {
				if tmpl, ok := jtSpec["template"].(map[string]interface{}); ok {
					podSpec, _ = tmpl["spec"].(map[string]interface{})
				}
			}
		}
	}
	if podSpec == nil {
		return ""
	}
	if san, ok := podSpec["serviceAccountName"].(string); ok && san != "" {
		return san
	}
	if san, ok := podSpec["serviceAccount"].(string); ok && san != "" {
		return san
	}
	return ""
}
