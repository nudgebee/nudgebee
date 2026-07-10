package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
)

// k8sPVSchema is the concrete-property schema for
// KubernetesPersistentVolume. Names are the node.Properties keys written by
// createNodeFromK8sPV; the ontology reads cluster/storage_class/
// capacity/phase. Cloud-source (CSI / in-tree) fields are declared so the
// cross-account volume matchers have a documented contract.
var k8sPVSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesPersistentVolume",
	NodeType:     core.NodeTypePV,
	Properties: []core.PropertyDef{
		{Name: "cluster", Indexed: true},
		{Name: "storage_class", Indexed: true},
		{Name: "phase", Indexed: true},
		{Name: "capacity", Indexed: true},
		{Name: "access_modes", Indexed: true},
		{Name: "reclaim_policy", Indexed: true},
		{Name: "volume_mode"},
		{Name: "volume_source"},
		{Name: "volume_name"},
		{Name: "csi_driver"},
		{Name: "csi_volume_handle"},
		{Name: "aws_ebs"},
		{Name: "azure_disk"},
		{Name: "gce_pd"},
		{Name: "uid"},
		{Name: "annotations"},
		{Name: "creation_timestamp"},
		{Name: "status_message"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sPVSchema) }

// fetchK8sPVsFromRelay fetches K8s PersistentVolumes from the relay server
func (s *K8sSource) fetchK8sPVsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sPVFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay PV fetch: cloud_account_id is empty")
		return []K8sPVFromRelay{}, nil
	}

	s.logger.Info("fetching K8s PVs from relay server",
		"resource_type", "persistentvolumes",
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
				"resource_type":  "persistentvolumes",
				"all_namespaces": false, // PVs are cluster-scoped
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for PVs", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for PVs: %w", err)
	}

	// Parse response
	pvs, err := s.parseK8sPVsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse PVs response", "error", err)
		return nil, fmt.Errorf("failed to parse PVs response: %w", err)
	}

	s.logger.Info("successfully fetched K8s PVs from relay", "count", len(pvs))
	return pvs, nil
}

// parseK8sPVsResponse parses the relay response for K8s PersistentVolumes
func (s *K8sSource) parseK8sPVsResponse(response map[string]interface{}) ([]K8sPVFromRelay, error) {
	pvs := make([]K8sPVFromRelay, 0)

	// Extract data array using shared helper
	actualDataArray, err := s.parseRelayDataArray(response, "PVs")
	if err != nil {
		return pvs, err
	}

	// Return early if no data
	if len(actualDataArray) == 0 {
		return pvs, nil
	}

	// Parse each PV
	for _, pvAny := range actualDataArray {
		pvMap, ok := pvAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid PV entry")
			continue
		}

		pvBytes, err := json.Marshal(pvMap)
		if err != nil {
			s.logger.Warn("failed to marshal PV", "error", err)
			continue
		}

		var pv K8sPVFromRelay
		if err := json.Unmarshal(pvBytes, &pv); err != nil {
			s.logger.Warn("failed to unmarshal PV", "error", err)
			continue
		}

		pvs = append(pvs, pv)
	}

	return pvs, nil
}

// convertK8sPVsToGraph converts K8s PersistentVolumes to knowledge graph nodes and edges
func (s *K8sSource) convertK8sPVsToGraph(pvs []K8sPVFromRelay, workloads []K8sWorkloadRow, clusterNodes, namespaceNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	for _, pv := range pvs {
		// Determine cluster name from workloads (or use first available cluster)
		clusterName := ""
		if len(workloads) > 0 {
			clusterName = workloads[0].ClusterName
		}

		// Create PV node
		pvNode := s.createNodeFromK8sPV(&pv, clusterName, req)
		nodes = append(nodes, pvNode)

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

			// Link PV to cluster
			edge := core.NewEdge(
				pvNode.ID,
				clusterNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{
					"connection_type": "cluster",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, edge)
		}
	}

	return nodes, edges, clusterNodes, namespaceNodes
}

// createNodeFromK8sPV creates a knowledge graph node from a K8s PersistentVolume
func (s *K8sSource) createNodeFromK8sPV(pv *K8sPVFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = pv.Metadata.Name
	properties["cluster"] = clusterName
	properties["uid"] = pv.Metadata.UID
	properties["phase"] = pv.Status.Phase
	properties["storage_class"] = pv.Spec.StorageClassName
	properties["volume_mode"] = pv.Spec.VolumeMode
	properties["reclaim_policy"] = pv.Spec.PersistentVolumeReclaimPolicy

	// Add access modes
	if len(pv.Spec.AccessModes) > 0 {
		properties["access_modes"] = pv.Spec.AccessModes
	}

	// Add capacity
	if len(pv.Spec.Capacity) > 0 {
		properties["capacity"] = pv.Spec.Capacity
	}

	// Add cloud-specific volume source information.
	// Order matters: CSI is the modern path (every GKE/EKS PV created in
	// the last ~3 years lives here); legacy in-tree fields are kept for
	// older clusters. See issue #31101 gap #7 for the CSI extraction.
	if pv.Spec.CSI != nil && pv.Spec.CSI.VolumeHandle != "" {
		properties["volume_source"] = "csi"
		properties["csi_driver"] = pv.Spec.CSI.Driver
		properties["csi_volume_handle"] = pv.Spec.CSI.VolumeHandle
		// Extract just the underlying disk name from the volume_handle so
		// cross-account rules can match on a stable short name without
		// each rule needing to know the cloud-specific URL format.
		// Examples:
		//   GKE pd.csi:    projects/<proj>/zones/<zone>/disks/<name>
		//   EKS ebs.csi:   vol-<id>
		//   AKS disk.csi:  /subscriptions/.../disks/<name>
		properties["volume_name"] = extractCSIVolumeName(pv.Spec.CSI.VolumeHandle)
	} else if len(pv.Spec.AWSElasticBlockStore) > 0 {
		properties["volume_source"] = "aws_ebs"
		properties["aws_ebs"] = pv.Spec.AWSElasticBlockStore
	} else if len(pv.Spec.AzureDisk) > 0 {
		properties["volume_source"] = "azure_disk"
		properties["azure_disk"] = pv.Spec.AzureDisk
	} else if len(pv.Spec.GCEPersistentDisk) > 0 {
		properties["volume_source"] = "gce_pd"
		properties["gce_pd"] = pv.Spec.GCEPersistentDisk
	}

	// Add labels if present
	if len(pv.Metadata.Labels) > 0 {
		properties["labels"] = pv.Metadata.Labels
	}

	// Add annotations if present
	if len(pv.Metadata.Annotations) > 0 {
		properties["annotations"] = pv.Metadata.Annotations
	}

	// Add creation timestamp
	if pv.Metadata.CreationTimestamp != "" {
		properties["creation_timestamp"] = pv.Metadata.CreationTimestamp
	}

	// Add status message if present
	if pv.Status.Message != "" {
		properties["status_message"] = pv.Status.Message
	}

	// Add subtype property for PV
	properties["subtype"] = "PersistentVolume"
	properties["specific_type"] = "KubernetesPersistentVolume"

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypePV,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypePV, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}
