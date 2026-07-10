package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/account"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"strings"
)

// Concrete-property schemas for the Karpenter CRDs. Names are the
// node.Properties keys written by createKarpenterNodePoolNode /
// createKarpenterNodeClaimNode. NodePool rolls up to ComputeInstancePool
// (state ← the base "status" key); NodeClaim to CustomResource (type ← crd_kind).
var karpenterNodePoolSchema = core.SpecificTypeSchema{
	SpecificType: "KarpenterNodePool",
	NodeType:     core.NodeTypeComputeInstancePool,
	Properties: []core.PropertyDef{
		{Name: "cluster", Indexed: true},
		{Name: "provisioned_by", Indexed: true},
		{Name: "crd_kind", Indexed: true},
		{Name: "crd_group"},
		{Name: "cpu_limit"},
		{Name: "memory_limit"},
		{Name: "consolidation_policy"},
		{Name: "annotations"},
	},
}

var karpenterNodeClaimSchema = core.SpecificTypeSchema{
	SpecificType: "KarpenterNodeClaim",
	NodeType:     core.NodeTypeCRD,
	Properties: []core.PropertyDef{
		{Name: "cluster", Indexed: true},
		{Name: "crd_kind", Indexed: true},
		{Name: "provisioned_by", Indexed: true},
		{Name: "provider_id", Indexed: true},
		{Name: "capacity_type", Indexed: true},
		{Name: "crd_group"},
		{Name: "node_name"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(karpenterNodePoolSchema)
	core.RegisterSpecificTypeSchema(karpenterNodeClaimSchema)
}

// clusterHasKarpenter reports whether the Karpenter CRD fetch should run for
// this cluster, using the autoscaler type the agent heartbeat reports
// (telemetry.DetectAutoScaler → AgentDetails.Features.AutoscalerType). Probing
// karpenter.sh on a non-Karpenter cluster yields guaranteed 404s the in-cluster
// agent logs as ERROR, so we want to skip it — but only when we're sure.
//
// Skip ONLY on positive evidence of a different autoscaler (e.g. "gke",
// "cluster-autoscaler"). Probe (fail-open) in every uncertain case:
//   - agent details unreadable (no agent row, telemetry gap, DB error)
//   - autoScalerType absent/empty — older agents omit it, and DetectAutoScaler
//     reports "" when it can't find a Karpenter/CAS deployment (incl. RBAC gaps)
//
// Rationale: silently dropping a real cluster's NodePools/NodeClaims is worse
// than leaving some 404 noise. The common noise source — GKE/AKS clusters —
// reports a concrete non-Karpenter type, so it's still skipped.
func (s *K8sSource) clusterHasKarpenter(req *core.SourceBuildRequest) bool {
	// Fail-fast tenant guard: never look up agent details without a tenant
	// scope, consistent with the rest of the build flow's tenant_id-scoped
	// queries.
	if req.TenantID == "" || req.CloudAccountID == "" {
		return false
	}
	details, err := account.GetAgentConnectionDetails(req.CloudAccountID)
	if err != nil {
		s.logger.Debug("Karpenter gate: agent details unavailable, probing anyway",
			"cloud_account_id", req.CloudAccountID, "error", err)
		return true // fail-open
	}
	fetch, reason := shouldFetchKarpenterCRDs(details.Features.AutoscalerType, details.Features.AutoscalerEnabled)
	if !fetch {
		s.logger.Info("skipping Karpenter CRD fetch",
			"cloud_account_id", req.CloudAccountID, "reason", reason)
	}
	return fetch
}

// shouldFetchKarpenterCRDs is the pure decision behind clusterHasKarpenter,
// split out so the policy can be unit-tested without a DB/agent lookup. It
// skips ONLY on positive evidence of a non-Karpenter autoscaler; an unknown
// (nil/empty) type probes. reason is for logging only.
func shouldFetchKarpenterCRDs(autoscalerType *string, autoscalerEnabled *bool) (fetch bool, reason string) {
	if autoscalerType == nil || strings.TrimSpace(*autoscalerType) == "" {
		return true, "autoscaler type unknown"
	}
	if !strings.EqualFold(*autoscalerType, "karpenter") {
		return false, "autoscaler is not Karpenter (" + *autoscalerType + ")"
	}
	// AutoscalerEnabled is reported alongside the type; treat a nil flag as
	// enabled (older agents may omit it) and only skip on an explicit false.
	if autoscalerEnabled != nil && !*autoscalerEnabled {
		return false, "Karpenter autoscaler reported disabled"
	}
	return true, "karpenter"
}

// fetchKarpenterNodePoolsFromRelay fetches Karpenter NodePool CRs via the
// relay's generic get_resource action. NodePool is cluster-scoped (not
// namespace-scoped), so we pass all_namespaces=false. Soft-fails on relay
// error — Karpenter isn't required infrastructure for the KG to build.
func (s *K8sSource) fetchKarpenterNodePoolsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sCRDFromRelay, error) {
	return s.fetchKarpenterCRDFromRelay(ctx, req, "nodepools")
}

// fetchKarpenterNodeClaimsFromRelay fetches Karpenter NodeClaim CRs.
// NodeClaim is also cluster-scoped.
func (s *K8sSource) fetchKarpenterNodeClaimsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sCRDFromRelay, error) {
	return s.fetchKarpenterCRDFromRelay(ctx, req, "nodeclaims")
}

// fetchKarpenterCRDFromRelay is the shared fetch helper for Karpenter
// CRDs. Tries the v1 API first, falls back to v1beta1 if v1 returns an
// error or empty result — supports both Karpenter 1.x clusters (v1) and
// older clusters running Karpenter 0.32-0.37 (v1beta1).
func (s *K8sSource) fetchKarpenterCRDFromRelay(ctx context.Context, req *core.SourceBuildRequest, resourceType string) ([]K8sCRDFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay Karpenter fetch: cloud_account_id is empty",
			"resource_type", resourceType)
		return []K8sCRDFromRelay{}, nil
	}

	// Primary attempt: v1 (Karpenter 1.x).
	crds, v1Err := s.fetchKarpenterCRDForVersion(req, resourceType, karpenterAPIVersionV1)
	if v1Err == nil && len(crds) > 0 {
		return crds, nil
	}

	// Fallback: v1beta1 (Karpenter 0.32 – 0.37). Same fetch shape, different
	// API version. We treat both error and empty as triggers because the
	// relay's response when the requested version isn't served can be either
	// an inner ACTION_UNEXPECTED_ERROR or an empty findings list depending
	// on the agent's K8s client version.
	if v1Err != nil {
		s.logger.Info("Karpenter v1 fetch failed, falling back to v1beta1",
			"resource_type", resourceType, "v1_error", v1Err)
	} else {
		s.logger.Info("Karpenter v1 fetch returned empty, falling back to v1beta1",
			"resource_type", resourceType)
	}
	crdsBeta, betaErr := s.fetchKarpenterCRDForVersion(req, resourceType, karpenterAPIVersionV1Beta1)
	if betaErr != nil {
		// Both versions failed — return the v1 error if we had one (more
		// likely to surface the real issue, eg auth), else the v1beta1 error.
		if v1Err != nil {
			return nil, fmt.Errorf("failed to fetch %s on both v1 and v1beta1 (v1: %w; v1beta1: %v)", resourceType, v1Err, betaErr)
		}
		return nil, fmt.Errorf("failed to fetch %s on v1beta1 (v1 returned empty): %w", resourceType, betaErr)
	}
	return crdsBeta, nil
}

// fetchKarpenterCRDForVersion executes a single get_resource call for a
// specific (group, version, resource_type). Same shape as
// fetchK8sServiceAccountsFromRelay.
func (s *K8sSource) fetchKarpenterCRDForVersion(req *core.SourceBuildRequest, resourceType, version string) ([]K8sCRDFromRelay, error) {
	s.logger.Info("fetching Karpenter CRDs from relay server",
		"resource_type", resourceType,
		"group", karpenterGroup,
		"version", version,
		"account_id", req.CloudAccountID)

	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":         karpenterGroup,
				"version":       version,
				"resource_type": resourceType,
				// NodePool / NodeClaim are cluster-scoped, but the relay agent's
				// get_resource action requires all_namespaces=true to enumerate
				// CRDs regardless of scope — it dispatches to a generic kubectl-get
				// path that needs the flag set. With all_namespaces=false the
				// agent returns ACTION_UNEXPECTED_ERROR (status_code 500 inside
				// an HTTP 200 envelope). Verified via direct relay probe 2026-05-26.
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Warn("failed to execute relay request for Karpenter CRD",
			"resource_type", resourceType, "version", version, "error", err)
		return nil, fmt.Errorf("failed to execute relay request for %s (%s): %w", resourceType, version, err)
	}

	crds, err := s.parseKarpenterCRDResponse(relayResponse, resourceType)
	if err != nil {
		s.logger.Warn("failed to parse Karpenter CRD response",
			"resource_type", resourceType, "version", version, "error", err)
		return nil, fmt.Errorf("failed to parse %s response (%s): %w", resourceType, version, err)
	}

	s.logger.Info("successfully fetched Karpenter CRDs from relay",
		"resource_type", resourceType, "version", version, "count", len(crds))
	return crds, nil
}

// parseKarpenterCRDResponse parses a Karpenter CRD list response into
// K8sCRDFromRelay records. Mirrors parseK8sServiceAccountsResponse.
func (s *K8sSource) parseKarpenterCRDResponse(response map[string]interface{}, resourceType string) ([]K8sCRDFromRelay, error) {
	crds := make([]K8sCRDFromRelay, 0)

	actualDataArray, err := s.parseRelayDataArray(response, resourceType)
	if err != nil {
		return crds, err
	}

	if len(actualDataArray) == 0 {
		return crds, nil
	}

	for _, crdAny := range actualDataArray {
		crdMap, ok := crdAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid CRD entry", "resource_type", resourceType)
			continue
		}

		crdBytes, err := json.Marshal(crdMap)
		if err != nil {
			s.logger.Warn("failed to marshal CRD", "resource_type", resourceType, "error", err)
			continue
		}

		var crd K8sCRDFromRelay
		if err := json.Unmarshal(crdBytes, &crd); err != nil {
			s.logger.Warn("failed to unmarshal CRD", "resource_type", resourceType, "error", err)
			continue
		}

		crds = append(crds, crd)
	}

	return crds, nil
}

// createKarpenterNodePoolNode builds a ComputeInstancePool DbNode from a
// fetched Karpenter NodePool CR. NodePool is cluster-scoped so namespace
// is left empty; the unique-key hierarchy resolves to "" via the existing
// GenerateUniqueKey rule for cluster-scoped resources.
func (s *K8sSource) createKarpenterNodePoolNode(np *K8sCRDFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":           np.Metadata.Name,
		"cluster":        clusterName,
		"provisioned_by": karpenterProvisionedBy,
		"subtype":        "KarpenterNodePool",
		"specific_type":  "KarpenterNodePool",
		"crd_kind":       "NodePool",
		"crd_group":      karpenterGroup,
	}
	if len(np.Metadata.Labels) > 0 {
		properties["labels"] = np.Metadata.Labels
	}
	if len(np.Metadata.Annotations) > 0 {
		properties["annotations"] = np.Metadata.Annotations
	}
	// Capacity limits — surface them so capacity-planning queries work.
	if spec := np.Spec; spec != nil {
		if limits, ok := spec["limits"].(map[string]interface{}); ok {
			if cpu, ok := limits["cpu"]; ok {
				properties["cpu_limit"] = cpu
			}
			if mem, ok := limits["memory"]; ok {
				properties["memory_limit"] = mem
			}
		}
		// disruption.consolidationPolicy / consolidateAfter — useful operationally.
		if disruption, ok := spec["disruption"].(map[string]interface{}); ok {
			if p, ok := disruption["consolidationPolicy"].(string); ok {
				properties["consolidation_policy"] = p
			}
		}
	}
	// Status condition — Ready=True is the most common gate.
	if status := np.Status; status != nil {
		if conditions, ok := status["conditions"].([]interface{}); ok {
			for _, c := range conditions {
				if cm, ok := c.(map[string]interface{}); ok {
					if t, _ := cm["type"].(string); t == "Ready" {
						if v, ok := cm["status"].(string); ok {
							properties["status"] = v
						}
					}
				}
			}
		}
	}

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeComputeInstancePool,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeComputeInstancePool, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createKarpenterNodeClaimNode builds a CustomResource DbNode for a
// Karpenter NodeClaim. NodeClaim is cluster-scoped (no namespace).
// `provider_id` is hoisted from .status.providerID so the phase-3
// cross-account rule can match it to a ComputeInstance.
func (s *K8sSource) createKarpenterNodeClaimNode(nc *K8sCRDFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":           nc.Metadata.Name,
		"cluster":        clusterName,
		"provisioned_by": karpenterProvisionedBy,
		"specific_type":  "KarpenterNodeClaim",
		"crd_kind":       "NodeClaim",
		"crd_group":      karpenterGroup,
	}
	if len(nc.Metadata.Labels) > 0 {
		properties["labels"] = nc.Metadata.Labels
	}
	if status := nc.Status; status != nil {
		if pid, ok := status["providerID"].(string); ok && pid != "" {
			properties["provider_id"] = pid
		}
		if nn, ok := status["nodeName"].(string); ok && nn != "" {
			properties["node_name"] = nn
		}
		// Ready / Launched / Registered conditions.
		if conditions, ok := status["conditions"].([]interface{}); ok {
			for _, c := range conditions {
				if cm, ok := c.(map[string]interface{}); ok {
					if t, _ := cm["type"].(string); t == "Ready" {
						if v, ok := cm["status"].(string); ok {
							properties["status"] = v
						}
					}
				}
			}
		}
	}
	if spec := nc.Spec; spec != nil {
		// .spec.requirements is a list of label-selector terms; we hoist
		// the well-known capacity-type one (spot vs on-demand) because
		// it's the field operators ask about most.
		if reqs, ok := spec["requirements"].([]interface{}); ok {
			for _, r := range reqs {
				if rm, ok := r.(map[string]interface{}); ok {
					if k, _ := rm["key"].(string); k == "karpenter.sh/capacity-type" {
						if vals, ok := rm["values"].([]interface{}); ok && len(vals) > 0 {
							if v, ok := vals[0].(string); ok {
								properties["capacity_type"] = v
							}
						}
					}
				}
			}
		}
	}

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeCRD,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeCRD, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// convertKarpenterNodePoolsToGraph emits ComputeInstancePool nodes for
// the Karpenter NodePools, plus an intra-source NodePool → BELONGS_TO →
// Cluster edge. The Cluster node is in turn linked to the AWS-side
// ManagedCluster by the existing phase-3 rule k8s_cluster_to_eks_cluster_by_name,
// so the full chain ComputeInstancePool → Cluster → ManagedCluster is
// reachable in 2 hops without us guessing the EKS cluster name from
// Karpenter metadata (which doesn't carry it). Returns the NodePool
// lookup map keyed by name — used by convertKarpenterNodeClaimsToGraph
// to wire the MANAGES edge.
func (s *K8sSource) convertKarpenterNodePoolsToGraph(nps []K8sCRDFromRelay, clusterName string, clusterNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0, len(nps))
	edges := make([]*core.DbEdge, 0, len(nps))
	byName := make(map[string]*core.DbNode, len(nps))
	for i := range nps {
		np := &nps[i]
		npNode := s.createKarpenterNodePoolNode(np, clusterName, req)
		nodes = append(nodes, npNode)
		byName[np.Metadata.Name] = npNode

		// NodePool → BELONGS_TO → Cluster (intra-source). The Cluster node
		// is keyed by Nudgebee's account_name (eg "k8s-prod"), which is the
		// same value we stamp on the NodePool's properties.cluster.
		if clusterName != "" {
			if clusterNode, ok := clusterNodes[clusterName]; ok {
				edges = append(edges, core.NewEdge(
					npNode.ID, clusterNode.ID,
					core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "karpenter_nodepool_in_cluster"},
					req.TenantID, req.CloudAccountID, "k8s",
				))
			}
		}
	}
	return nodes, edges, byName
}

// createNodeToKarpenterNodePoolEdges wires Node → BELONGS_TO → NodePool by
// reading the `karpenter.sh/nodepool` label that Karpenter stamps on every
// node it provisions. This is intra-source (both endpoints from k8s_source)
// rather than a phase-3 rule because the cross_account_relationships matcher
// resolves dotted property paths by splitting on `.` and walking sub-maps —
// so a path like `labels.karpenter.sh/nodepool` is read as
// `labels → karpenter → sh/nodepool` and misses the actual `karpenter.sh/nodepool`
// label key (which is a single string with dots in it). Wiring this in Go
// sidesteps that limitation without changing the matcher.
func (s *K8sSource) createNodeToKarpenterNodePoolEdges(k8sNodes []*core.DbNode, nodePoolsByName map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	if len(nodePoolsByName) == 0 || len(k8sNodes) == 0 {
		return nil
	}
	edges := make([]*core.DbEdge, 0)
	for _, n := range k8sNodes {
		if n == nil {
			continue
		}
		labels, ok := n.Properties["labels"].(map[string]interface{})
		if !ok {
			continue
		}
		poolName, ok := labels[karpenterNodePoolLabel].(string)
		if !ok || poolName == "" {
			continue
		}
		npNode, exists := nodePoolsByName[poolName]
		if !exists {
			continue
		}
		edges = append(edges, core.NewEdge(
			n.ID, npNode.ID,
			core.RelationshipBelongsTo,
			map[string]interface{}{
				"connection_type": "karpenter_node_provisioned_by",
				"nodepool":        poolName,
			},
			req.TenantID, req.CloudAccountID, "k8s",
		))
	}
	return edges
}

// convertKarpenterNodeClaimsToGraph emits CustomResource nodes for the
// NodeClaims plus the two intra-source edges:
//   - NodePool → MANAGES → NodeClaim (via NodeClaim's ownerReferences)
//   - NodeClaim → RUNS_ON → Node (via .status.nodeName matched to existing
//     K8s Node graph nodes by name)
//
// The cross-account NodeClaim → ComputeInstance HOSTED_ON edge is wired
// by the phase-3 rule karpenter_nodeclaim_to_aws_ec2_instance via
// .status.providerID.
func (s *K8sSource) convertKarpenterNodeClaimsToGraph(ncs []K8sCRDFromRelay, nodePoolsByName map[string]*core.DbNode, k8sNodesByName map[string]*core.DbNode, clusterName string, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	nodes := make([]*core.DbNode, 0, len(ncs))
	edges := make([]*core.DbEdge, 0)

	for i := range ncs {
		nc := &ncs[i]
		ncNode := s.createKarpenterNodeClaimNode(nc, clusterName, req)
		nodes = append(nodes, ncNode)

		// NodePool → MANAGES → NodeClaim. NodeClaim's ownerReferences[].name
		// holds the NodePool name. We accept any ownerRef whose kind is
		// "NodePool" — Karpenter is the only operator currently emitting
		// that kind.
		for _, owner := range nc.Metadata.OwnerReferences {
			ownerKind, _ := owner["kind"].(string)
			ownerName, _ := owner["name"].(string)
			if ownerKind != "NodePool" || ownerName == "" {
				continue
			}
			if npNode, ok := nodePoolsByName[ownerName]; ok {
				edges = append(edges, core.NewEdge(
					npNode.ID, ncNode.ID,
					core.RelationshipManages,
					map[string]interface{}{"connection_type": "karpenter_nodepool_manages_nodeclaim"},
					req.TenantID, req.CloudAccountID, "k8s",
				))
			}
		}

		// NodeClaim → RUNS_ON → Node. .status.nodeName is the K8s Node the
		// claim materialized into. May be empty for pending/failed claims.
		if nodeName, _ := core.GetNodePropertyString(ncNode, "node_name"); nodeName != "" {
			if nodeGraphNode, ok := k8sNodesByName[nodeName]; ok {
				edges = append(edges, core.NewEdge(
					ncNode.ID, nodeGraphNode.ID,
					core.RelationshipRunsOn,
					map[string]interface{}{"connection_type": "karpenter_nodeclaim_materialized_node"},
					req.TenantID, req.CloudAccountID, "k8s",
				))
			}
		}
	}

	return nodes, edges
}
