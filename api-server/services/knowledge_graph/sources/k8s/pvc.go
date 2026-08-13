package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"strings"
)

// k8sPVCSchema is the concrete-property schema for
// KubernetesPersistentVolumeClaim. Names are the node.Properties keys written by
// createNodeFromK8sPVC; the ontology reads namespace/cluster/
// storage_class/capacity/phase.
var k8sPVCSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesPersistentVolumeClaim",
	NodeType:     core.NodeTypePVC,
	Properties: []core.PropertyDef{
		{Name: "namespace", Indexed: true},
		{Name: "cluster", Indexed: true},
		{Name: "storage_class", Indexed: true},
		{Name: "phase", Indexed: true},
		{Name: "capacity", Indexed: true},
		{Name: "access_modes", Indexed: true},
		{Name: "volume_name"},
		{Name: "volume_mode"},
		{Name: "resources"},
		{Name: "uid"},
		{Name: "annotations"},
		{Name: "creation_timestamp"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sPVCSchema) }

// fetchK8sPVCsFromRelay fetches K8s PersistentVolumeClaims from the relay server
func (s *K8sSource) fetchK8sPVCsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sPVCFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay PVC fetch: cloud_account_id is empty")
		return []K8sPVCFromRelay{}, nil
	}

	s.logger.Info("fetching K8s PVCs from relay server",
		"resource_type", "persistentvolumeclaims",
		"account_id", req.CloudAccountID)

	// Execute relay request
	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":          "",
				"version":        "v1",
				"resource_type":  "persistentvolumeclaims",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for PVCs", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for PVCs: %w", err)
	}

	// Parse response
	pvcs, err := s.parseK8sPVCsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse PVCs response", "error", err)
		return nil, fmt.Errorf("failed to parse PVCs response: %w", err)
	}

	s.logger.Info("successfully fetched K8s PVCs from relay", "count", len(pvcs))
	return pvcs, nil
}

// parseK8sPVCsResponse parses the relay response for K8s PersistentVolumeClaims
func (s *K8sSource) parseK8sPVCsResponse(response map[string]interface{}) ([]K8sPVCFromRelay, error) {
	pvcs := make([]K8sPVCFromRelay, 0)

	// Extract data array using shared helper
	actualDataArray, err := s.parseRelayDataArray(response, "PVCs")
	if err != nil {
		return pvcs, err
	}

	// Return early if no data
	if len(actualDataArray) == 0 {
		return pvcs, nil
	}

	// Parse each PVC
	for _, pvcAny := range actualDataArray {
		pvcMap, ok := pvcAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid PVC entry")
			continue
		}

		pvcBytes, err := json.Marshal(pvcMap)
		if err != nil {
			s.logger.Warn("failed to marshal PVC", "error", err)
			continue
		}

		var pvc K8sPVCFromRelay
		if err := json.Unmarshal(pvcBytes, &pvc); err != nil {
			s.logger.Warn("failed to unmarshal PVC", "error", err)
			continue
		}

		pvcs = append(pvcs, pvc)
	}

	return pvcs, nil
}

// convertK8sPVCsToGraph converts K8s PersistentVolumeClaims to knowledge graph nodes and edges
func (s *K8sSource) convertK8sPVCsToGraph(pvcs []K8sPVCFromRelay, workloads []K8sWorkloadRow, pvs []K8sPVFromRelay, clusterNodes, namespaceNodes map[string]*core.DbNode, workloadNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Build a map of PVCs for quick lookup
	pvcNodes := make(map[string]*core.DbNode) // key: "cluster/namespace/name" -> node
	// Build a map of PVs for binding relationships
	pvMap := make(map[string]*K8sPVFromRelay) // key: "pv_name" -> PV

	for _, pv := range pvs {
		pvMap[pv.Metadata.Name] = &pv
	}

	for _, pvc := range pvcs {
		// Determine cluster name from workloads
		clusterName := s.getClusterNameForResource(pvc.Metadata.Namespace, workloads)

		// Create PVC node
		pvcNode := s.createNodeFromK8sPVC(&pvc, clusterName, req)
		nodes = append(nodes, pvcNode)

		// Store in map for relationship creation
		pvcKey := fmt.Sprintf("%s/%s/%s", clusterName, pvc.Metadata.Namespace, pvc.Metadata.Name)
		pvcNodes[pvcKey] = pvcNode

		// Create or get namespace node
		namespaceKey := fmt.Sprintf("%s/%s", clusterName, pvc.Metadata.Namespace)
		var namespaceNode *core.DbNode
		if existingNs, exists := namespaceNodes[namespaceKey]; exists {
			namespaceNode = existingNs
		} else {
			namespaceNode = s.createNamespaceNode(pvc.Metadata.Namespace, clusterName, req)
			namespaceNodes[namespaceKey] = namespaceNode
			nodes = append(nodes, namespaceNode)
		}

		// Link PVC to namespace
		edge := core.NewEdge(
			pvcNode.ID,
			namespaceNode.ID,
			core.RelationshipBelongsTo,
			map[string]interface{}{
				"connection_type": "namespace",
			},
			req.TenantID,
			req.CloudAccountID,
			"k8s",
		)
		edges = append(edges, edge)

		// Create or get cluster node
		if clusterName != "" {
			var clusterNode *core.DbNode
			if existingCluster, exists := clusterNodes[clusterName]; exists {
				clusterNode = existingCluster
			} else {
				clusterNode = s.createClusterNode(clusterName, req)
				clusterNodes[clusterName] = clusterNode
				nodes = append(nodes, clusterNode)
			}

			// Link namespace to cluster (if not already linked)
			nsToClusterEdge := core.NewEdge(
				namespaceNode.ID,
				clusterNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{
					"connection_type": "cluster",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, nsToClusterEdge)
		}

		// Create PVC -> PV relationship if PVC is bound
		if pvc.Spec.VolumeName != "" && pvc.Status.Phase == "Bound" {
			// Find the PV from the map and create edge
			if pv, exists := pvMap[pvc.Spec.VolumeName]; exists {
				// Create PV node to get the proper unique key
				pvNode := s.createNodeFromK8sPV(pv, clusterName, req)
				edge := core.NewEdge(
					pvcNode.ID,
					pvNode.ID,
					core.RelationshipIsBoundTo,
					map[string]interface{}{
						"connection_type": "volume_binding",
						"volume_name":     pvc.Spec.VolumeName,
						"phase":           pvc.Status.Phase,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)
			}
		}
	}

	// Create relationships between workloads and PVCs
	for _, workload := range workloads {
		// Find the actual workload node - include cluster to match the key format
		workloadKey := fmt.Sprintf("%s/%s/%s/%s", workload.ClusterName, workload.Kind, workload.Namespace, workload.Name)
		workloadNode, workloadExists := workloadNodes[workloadKey]
		if !workloadExists {
			continue
		}

		// Extract PVC references from workload metadata (for direct PVC mounts)
		pvcRefs := s.extractPVCReferences(&workload)
		for _, pvcName := range pvcRefs {
			// Find the PVC node
			pvcKey := fmt.Sprintf("%s/%s/%s", workload.ClusterName, workload.Namespace, pvcName)
			if pvcNode, exists := pvcNodes[pvcKey]; exists {
				// Create edge from workload to PVC (workload mounts PVC)
				edge := core.NewEdge(
					workloadNode.ID,
					pvcNode.ID,
					core.RelationshipMounts,
					map[string]interface{}{
						"connection_type": "pvc_mount",
						"pvc_name":        pvcName,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)
			}
		}
	}

	// Create relationships between StatefulSets and their dynamically created PVCs
	// StatefulSet PVCs follow the naming pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
	statefulSetEdges := s.matchStatefulSetPVCs(pvcs, workloads, workloadNodes, pvcNodes, req)
	edges = append(edges, statefulSetEdges...)

	return nodes, edges, clusterNodes, namespaceNodes
}

// matchStatefulSetPVCs matches PVCs to StatefulSets based on the naming convention
// StatefulSet PVCs follow the pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
func (s *K8sSource) matchStatefulSetPVCs(pvcs []K8sPVCFromRelay, workloads []K8sWorkloadRow, workloadNodes map[string]*core.DbNode, pvcNodes map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build a map of StatefulSets by namespace for quick lookup
	statefulSets := make(map[string][]K8sWorkloadRow) // namespace -> []StatefulSet
	for _, workload := range workloads {
		if workload.Kind == "StatefulSet" {
			statefulSets[workload.Namespace] = append(statefulSets[workload.Namespace], workload)
		}
	}

	// Track which PVCs have already been matched to avoid duplicates
	matchedPVCs := make(map[string]bool)

	for _, pvc := range pvcs {
		pvcName := pvc.Metadata.Name
		pvcNamespace := pvc.Metadata.Namespace

		// Skip if no StatefulSets in this namespace
		stsInNamespace, exists := statefulSets[pvcNamespace]
		if !exists {
			continue
		}

		// Try to match this PVC to a StatefulSet
		for _, sts := range stsInNamespace {
			// Check if PVC name matches the StatefulSet naming pattern
			// Pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
			if s.isPVCForStatefulSet(pvcName, sts.Name) {
				// Get the cluster name for this PVC
				clusterName := s.getClusterNameForResource(pvcNamespace, workloads)

				// Find the workload node
				workloadKey := fmt.Sprintf("%s/%s/%s/%s", sts.ClusterName, sts.Kind, sts.Namespace, sts.Name)
				workloadNode, workloadExists := workloadNodes[workloadKey]
				if !workloadExists {
					continue
				}

				// Find the PVC node
				pvcKey := fmt.Sprintf("%s/%s/%s", clusterName, pvcNamespace, pvcName)
				pvcNode, pvcExists := pvcNodes[pvcKey]
				if !pvcExists {
					continue
				}

				// Avoid duplicate edges
				edgeKey := fmt.Sprintf("%s->%s", workloadNode.ID, pvcNode.ID)
				if matchedPVCs[edgeKey] {
					continue
				}
				matchedPVCs[edgeKey] = true

				// Create edge from StatefulSet to PVC
				edge := core.NewEdge(
					workloadNode.ID,
					pvcNode.ID,
					core.RelationshipMounts,
					map[string]interface{}{
						"connection_type": "statefulset_pvc",
						"pvc_name":        pvcName,
						"statefulset":     sts.Name,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)

				s.logger.Debug("matched StatefulSet PVC",
					"statefulset", sts.Name,
					"pvc", pvcName,
					"namespace", pvcNamespace)

				break // PVC can only belong to one StatefulSet
			}
		}
	}

	return edges
}

// isPVCForStatefulSet checks if a PVC name matches the StatefulSet naming pattern
// Pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
// Example: data-my-statefulset-0, data-my-statefulset-1
func (s *K8sSource) isPVCForStatefulSet(pvcName, statefulSetName string) bool {
	// The PVC name should contain the StatefulSet name followed by a dash and ordinal
	// Pattern: <prefix>-<statefulsetName>-<ordinal>

	// Check if the PVC name contains the StatefulSet name
	if !strings.Contains(pvcName, statefulSetName) {
		return false
	}

	// Find the position of the StatefulSet name in the PVC name
	stsIdx := strings.Index(pvcName, statefulSetName)
	if stsIdx <= 0 {
		// StatefulSet name should not be at the beginning (there should be a volume template prefix)
		return false
	}

	// Check that there's a dash before the StatefulSet name (separating volume template name)
	if pvcName[stsIdx-1] != '-' {
		return false
	}

	// Check what comes after the StatefulSet name
	afterSts := pvcName[stsIdx+len(statefulSetName):]

	// Should be "-<ordinal>" pattern (e.g., "-0", "-1", "-2")
	if len(afterSts) < 2 {
		return false
	}

	if afterSts[0] != '-' {
		return false
	}

	// Check if the rest is a valid ordinal (digits only)
	ordinal := afterSts[1:]
	for _, c := range ordinal {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

// extractPVCReferences extracts PVC names referenced by a workload from its metadata
func (s *K8sSource) extractPVCReferences(workload *K8sWorkloadRow) []string {
	pvcNames := make([]string, 0)
	seenNames := make(map[string]bool)

	// Parse workload metadata
	var metaMap map[string]interface{}
	if len(workload.Meta) > 0 {
		if err := json.Unmarshal(workload.Meta, &metaMap); err != nil {
			return pvcNames
		}

		// If volumes is under config.volumes
		if config, ok := metaMap["config"].(map[string]interface{}); ok {
			if volumes, ok := config["volumes"].([]interface{}); ok {
				for _, volume := range volumes {
					if volumeMap, ok := volume.(map[string]interface{}); ok {
						if pvc, ok := volumeMap["persistent_volume_claim"].(map[string]interface{}); ok {
							if claimName, ok := pvc["claim_name"].(string); ok && claimName != "" {
								if !seenNames[claimName] {
									pvcNames = append(pvcNames, claimName)
									seenNames[claimName] = true
								}
							}
						}
					}
				}
			}
		}
	}

	return pvcNames
}

// createNodeFromK8sPVC creates a knowledge graph node from a K8s PersistentVolumeClaim
func (s *K8sSource) createNodeFromK8sPVC(pvc *K8sPVCFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = pvc.Metadata.Name
	properties["namespace"] = pvc.Metadata.Namespace
	properties["cluster"] = clusterName
	properties["uid"] = pvc.Metadata.UID
	properties["phase"] = pvc.Status.Phase
	properties["storage_class"] = pvc.Spec.StorageClassName
	properties["volume_name"] = pvc.Spec.VolumeName
	properties["volume_mode"] = pvc.Spec.VolumeMode

	// Add access modes
	if len(pvc.Spec.AccessModes) > 0 {
		properties["access_modes"] = pvc.Spec.AccessModes
	}

	// Add capacity if available
	if len(pvc.Status.Capacity) > 0 {
		properties["capacity"] = pvc.Status.Capacity
	}

	// Add requested resources
	if len(pvc.Spec.Resources) > 0 {
		properties["resources"] = pvc.Spec.Resources
	}

	// Add labels if present
	if len(pvc.Metadata.Labels) > 0 {
		properties["labels"] = pvc.Metadata.Labels
	}

	// Add annotations if present
	if len(pvc.Metadata.Annotations) > 0 {
		properties["annotations"] = pvc.Metadata.Annotations
	}

	// Add creation timestamp
	if pvc.Metadata.CreationTimestamp != "" {
		properties["creation_timestamp"] = pvc.Metadata.CreationTimestamp
	}

	// Add subtype property for PVC
	properties["subtype"] = "PersistentVolumeClaim"
	properties["specific_type"] = "KubernetesPersistentVolumeClaim"

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypePVC,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypePVC, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// extractServiceAccountName resolves the serviceAccountName for a workload from
// the k8s collector's flattened meta shape. The collector hoists this field to
// `meta.config.service_account` for every workload Kind (Pod / Deployment /
// StatefulSet / DaemonSet / ReplicaSet / Job / CronJob), so we read it from one
// place regardless of Kind. Falls back to the native K8s API paths
// (`spec.template.spec.serviceAccountName`, `spec.serviceAccountName`) in case
// extractCSIVolumeName returns the underlying disk/volume name from a CSI
// `spec.csi.volume_handle`. The whole handle is also kept on the node as
// `csi_volume_handle`; this helper produces a stable short identifier that
// matches the cloud-side resource's `name` for cross-account rules.
//
// Examples:
//
//	"projects/p/zones/z/disks/pvc-abc"  -> "pvc-abc"   (GKE pd.csi.storage.gke.io)
//	"vol-0123456789abcdef0"             -> "vol-0123…" (EKS ebs.csi.aws.com)
//	"/subscriptions/…/disks/disk-name"  -> "disk-name" (AKS disk.csi.azure.com)
//	"pvc-abc"                           -> "pvc-abc"   (no path separator)
func extractCSIVolumeName(volumeHandle string) string {
	if volumeHandle == "" {
		return ""
	}
	if idx := strings.LastIndex(volumeHandle, "/"); idx >= 0 && idx < len(volumeHandle)-1 {
		return volumeHandle[idx+1:]
	}
	return volumeHandle
}
