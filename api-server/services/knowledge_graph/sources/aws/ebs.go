package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// ebsVolumeSchema — concrete schema for EBS volumes. Fields written by
// extractEBSMetadata.
var ebsVolumeSchema = core.SpecificTypeSchema{
	SpecificType: "EBSVolume",
	NodeType:     core.NodeTypeStorage,
	Properties: []core.PropertyDef{
		{Name: "volume_type", Indexed: true},
		{Name: "encrypted", Indexed: true},
		{Name: "size_gb", Indexed: true},
		{Name: "iops", Indexed: true},
		{Name: "availability_zone", Indexed: true},
		{Name: "volume_state", Indexed: true},
		{Name: "volume_id"},
		{Name: "throughput_mibs"},
		{Name: "kms_key_id"},
		{Name: "snapshot_id"},
		{Name: "multi_attach_enabled"},
		{Name: "attached_instance_id"},
		{Name: "device"},
		{Name: "attachment_state"},
		{Name: "delete_on_termination"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "create_time"},
		{Name: "size"},
		{Name: "outpost_arn"},
		{Name: "fast_restored"},
	},
}

// ebsSnapshotSchema — concrete schema for EBS snapshots (extractSnapshotMetadata).
var ebsSnapshotSchema = core.SpecificTypeSchema{
	SpecificType: "EBSSnapshot",
	NodeType:     core.NodeTypeStorage,
	Properties: []core.PropertyDef{
		{Name: "storage_type", Indexed: true},
		{Name: "encrypted", Indexed: true},
		{Name: "size_gb", Indexed: true},
		{Name: "snapshot_state", Indexed: true},
		{Name: "snapshot_id"},
		{Name: "volume_id"},
		{Name: "progress"},
		{Name: "kms_key_id"},
		{Name: "owner_id"},
		{Name: "description"},
		{Name: "start_time"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "public"},
		{Name: "state_message"},
		{Name: "volume_size"},
		{Name: "outpost_arn"},
		{Name: "data_encryption_key_id"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(ebsVolumeSchema)
	core.RegisterSpecificTypeSchema(ebsSnapshotSchema)
}

// extractEBSMetadata extracts essential fields for EBS volumes
func (s *AWSSource) extractEBSMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Volume ID (critical identifier)
	if volumeID, ok := metaMap["VolumeId"].(string); ok && volumeID != "" {
		properties["volume_id"] = volumeID
	}

	// Size in GB (important for capacity planning)
	if size, ok := metaMap["Size"].(float64); ok {
		properties["size_gb"] = int(size)
	} else if size, ok := metaMap["Size"].(int); ok {
		properties["size_gb"] = size
	}

	// Volume type (gp2, gp3, io1, io2, st1, sc1, standard)
	if volumeType, ok := metaMap["VolumeType"].(string); ok && volumeType != "" {
		properties["volume_type"] = volumeType
	}

	// IOPS (important for performance)
	if iops, ok := metaMap["Iops"].(float64); ok {
		properties["iops"] = int(iops)
	} else if iops, ok := metaMap["Iops"].(int); ok {
		properties["iops"] = iops
	}

	// Throughput in MiB/s (for gp3 and io2 volumes)
	if throughput, ok := metaMap["Throughput"].(float64); ok {
		properties["throughput_mibs"] = int(throughput)
	} else if throughput, ok := metaMap["Throughput"].(int); ok {
		properties["throughput_mibs"] = throughput
	}

	// Encryption status (important for security)
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// KMS Key ID (for encrypted volumes)
	if kmsKeyID, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyID != "" {
		properties["kms_key_id"] = kmsKeyID
	}

	// Availability Zone (important for data locality)
	if az, ok := metaMap["AvailabilityZone"].(string); ok && az != "" {
		properties["availability_zone"] = az
	}

	// State (in-use, available, creating, deleting, etc.)
	if state, ok := metaMap["State"].(string); ok && state != "" {
		properties["volume_state"] = state
	}

	// Snapshot ID (if volume was created from a snapshot)
	if snapshotID, ok := metaMap["SnapshotId"].(string); ok && snapshotID != "" {
		properties["snapshot_id"] = snapshotID
	}

	// Multi-attach enabled (for io1/io2 volumes)
	if multiAttach, ok := metaMap["MultiAttachEnabled"].(bool); ok {
		properties["multi_attach_enabled"] = multiAttach
	}

	// Attachment information (needed for EBS->EC2 relationship)
	if attachments, ok := metaMap["Attachments"].([]interface{}); ok && len(attachments) > 0 {
		// Extract first attachment details (most volumes have single attachment)
		if attachment, ok := attachments[0].(map[string]interface{}); ok {
			if instanceID, ok := attachment["InstanceId"].(string); ok && instanceID != "" {
				properties["attached_instance_id"] = instanceID
			}
			if device, ok := attachment["Device"].(string); ok && device != "" {
				properties["device"] = device
			}
			if attachState, ok := attachment["State"].(string); ok && attachState != "" {
				properties["attachment_state"] = attachState
			}
			if deleteOnTerm, ok := attachment["DeleteOnTermination"].(bool); ok {
				properties["delete_on_termination"] = deleteOnTerm
			}
		}
	}
}

// extractSnapshotMetadata extracts essential fields for EBS snapshots
func (s *AWSSource) extractSnapshotMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Set storage_type to distinguish from EBS volumes
	properties["storage_type"] = "snapshot"

	// Snapshot ID (critical identifier)
	if snapshotID, ok := metaMap["SnapshotId"].(string); ok && snapshotID != "" {
		properties["snapshot_id"] = snapshotID
	}

	// Volume ID (the source volume this snapshot was created from)
	if volumeID, ok := metaMap["VolumeId"].(string); ok && volumeID != "" {
		properties["volume_id"] = volumeID
	}

	// Size in GB
	if size, ok := metaMap["VolumeSize"].(float64); ok {
		properties["size_gb"] = int(size)
	} else if size, ok := metaMap["VolumeSize"].(int); ok {
		properties["size_gb"] = size
	}

	// State (pending, completed, error, recoverable, recovering)
	if state, ok := metaMap["State"].(string); ok && state != "" {
		properties["snapshot_state"] = state
	}

	// Progress (percentage complete, e.g., "100%")
	if progress, ok := metaMap["Progress"].(string); ok && progress != "" {
		properties["progress"] = progress
	}

	// Encryption status
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// KMS Key ID (for encrypted snapshots)
	if kmsKeyID, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyID != "" {
		properties["kms_key_id"] = kmsKeyID
	}

	// Owner ID (AWS account that owns the snapshot)
	if ownerID, ok := metaMap["OwnerId"].(string); ok && ownerID != "" {
		properties["owner_id"] = ownerID
	}

	// Description
	if description, ok := metaMap["Description"].(string); ok && description != "" {
		properties["description"] = description
	}

	// Start time (when the snapshot was initiated)
	if startTime, ok := metaMap["StartTime"].(string); ok && startTime != "" {
		properties["start_time"] = startTime
	}
}

// createEBSEdges creates edges for EBS volumes attached to EC2 instances
func (s *AWSSource) createEBSEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use pre-extracted attachment properties (set by extractEBSMetadata).
		// Raw meta is not stored in node.Properties, so we read the flattened fields.
		instanceID, _ := node.Properties["attached_instance_id"].(string)
		if instanceID == "" {
			continue
		}

		if ec2Node, exists := lookup.ByResourceID[instanceID]; exists {
			device, _ := node.Properties["device"].(string)
			attachState, _ := node.Properties["attachment_state"].(string)
			deleteOnTermination, _ := node.Properties["delete_on_termination"].(bool)

			edges = append(edges, s.createEdge(node, ec2Node, core.RelationshipHostedOn,
				map[string]interface{}{
					"connection_type":       "ebs_attachment",
					"device":                device,
					"state":                 attachState,
					"delete_on_termination": deleteOnTermination,
				}, req))
		}
	}

	return edges
}
