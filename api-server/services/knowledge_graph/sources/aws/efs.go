package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// efsFileSystemSchema — concrete schema for EFS file systems. Fields written by
// extractEFSMetadata.
var efsFileSystemSchema = core.SpecificTypeSchema{
	SpecificType: "EFSFileSystem",
	NodeType:     core.NodeTypeStorage,
	Properties: []core.PropertyDef{
		{Name: "encrypted", Indexed: true},
		{Name: "size_in_bytes", Indexed: true},
		{Name: "filesystem_id"},
		{Name: "performance_mode"},
		{Name: "throughput_mode"},
		{Name: "provisioned_throughput_mibps"},
		{Name: "kms_key_id"},
		{Name: "lifecycle_state"},
		{Name: "number_of_mount_targets"},
		{Name: "creation_time"},
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "owner_id"},
		{Name: "creation_token"},
		{Name: "life_cycle_state"},
		{Name: "size_in_bytes_value"},
		{Name: "size_in_bytes_timestamp"},
		{Name: "availability_zone_name"},
		{Name: "availability_zone_id"},
		{Name: "file_system_protection"},
	},
}

func init() { core.RegisterSpecificTypeSchema(efsFileSystemSchema) }

// extractEFSMetadata extracts essential fields for EFS file systems
func (s *AWSSource) extractEFSMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// File system ID (fs-xxxxx)
	if fsId, ok := metaMap["FileSystemId"].(string); ok && fsId != "" {
		properties["filesystem_id"] = fsId
	}

	// Performance mode (generalPurpose or maxIO)
	if perfMode, ok := metaMap["PerformanceMode"].(string); ok && perfMode != "" {
		properties["performance_mode"] = perfMode
	}

	// Throughput mode (bursting or provisioned)
	if throughputMode, ok := metaMap["ThroughputMode"].(string); ok && throughputMode != "" {
		properties["throughput_mode"] = throughputMode
	}

	// Provisioned throughput (only for provisioned mode)
	if provisionedThroughput, ok := metaMap["ProvisionedThroughputInMibps"].(float64); ok && provisionedThroughput > 0 {
		properties["provisioned_throughput_mibps"] = provisionedThroughput
	}

	// Encryption
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// KMS Key ID (if encrypted)
	if kmsKeyId, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyId != "" {
		properties["kms_key_id"] = kmsKeyId
	}

	// Lifecycle state
	if lifecycleState, ok := metaMap["LifeCycleState"].(string); ok && lifecycleState != "" {
		properties["lifecycle_state"] = lifecycleState
	}

	// Size in bytes
	if sizeInBytes, ok := metaMap["SizeInBytes"].(map[string]interface{}); ok {
		if value, ok := sizeInBytes["Value"].(float64); ok {
			properties["size_in_bytes"] = int64(value)
		}
	}

	// Number of mount targets
	if numMountTargets, ok := metaMap["NumberOfMountTargets"].(float64); ok {
		properties["number_of_mount_targets"] = int(numMountTargets)
	}

	// Creation time
	if creationTime, ok := metaMap["CreationTime"].(string); ok && creationTime != "" {
		properties["creation_time"] = creationTime
	}

	// Generate DNS name from filesystem ID and region
	// Format: fs-xxxxx.efs.{region}.amazonaws.com
	if fsId, ok := properties["filesystem_id"].(string); ok && fsId != "" {
		if region, ok := properties["region"].(string); ok && region != "" {
			properties["dns_name"] = fmt.Sprintf("%s.efs.%s.amazonaws.com", fsId, region)
		}
	}
}

// fetchEFSMountTargetsFromAWS fetches EFS mount target data from AWS using cloud collector CLI
func (s *AWSSource) fetchEFSMountTargetsFromAWS(reqCtx *security.RequestContext, req *core.SourceBuildRequest, accountID string, fileSystemID string) ([]EFSMountTargetData, error) {
	// Build AWS CLI command to describe mount targets for an EFS file system
	cmd := fmt.Sprintf("aws efs describe-mount-targets --file-system-id %s --output json", fileSystemID)

	s.logger.Info("Fetching EFS mount targets from AWS",
		"file_system_id", fileSystemID,
		"account_id", accountID,
		"command", cmd)

	// Execute AWS CLI command via cloud collector
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute AWS CLI command: %w", err)
	}

	// Parse response
	var result struct {
		MountTargets []EFSMountTargetData `json:"MountTargets"`
	}

	// Log the raw response for debugging
	s.logger.Info("Cloud CLI response received for EFS mount targets", "response_keys", getMapKeys(resp))

	// Try different response formats
	var output string
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		output = dataStr
	} else if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		output = outputStr
	} else if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		output = resultStr
	} else {
		if respBytes, err := json.Marshal(resp); err == nil {
			s.logger.Error("Invalid response format from cloud CLI", "raw_response", string(respBytes))
		}
		return nil, fmt.Errorf("invalid response format from cloud CLI: expected 'data', 'output', or 'result' field with string value")
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		s.logger.Error("Failed to parse EFS mount targets JSON", "error", err, "output_preview", sources.TruncateString(output, 200))
		return nil, fmt.Errorf("failed to parse EFS mount targets response: %w", err)
	}

	s.logger.Info("Successfully fetched EFS mount targets from AWS",
		"file_system_id", fileSystemID,
		"mount_target_count", len(result.MountTargets))

	return result.MountTargets, nil
}

// createEFSEdges creates edges for EFS (Elastic File System) resources
// EFS → VPC (BELONGS_TO), EFS → Subnet (HOSTED_ON), EFS → ENI (ASSOCIATED_WITH)
func (s *AWSSource) createEFSEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Only process AmazonEFS resources
		if service, ok := node.Properties["service"].(string); !ok || service != "AmazonEFS" {
			continue
		}

		// Get file system ID
		fileSystemID, _ := node.Properties["resource_id"].(string)
		if fileSystemID == "" {
			if name, ok := node.Properties["name"].(string); ok {
				fileSystemID = name
			}
		}

		// Fetch mount targets from AWS CLI
		var mountTargets []EFSMountTargetData
		if fileSystemID != "" && req.CloudAccountID != "" {
			targets, err := s.fetchEFSMountTargetsFromAWS(reqCtx, req, req.CloudAccountID, fileSystemID)
			if err != nil {
				s.logger.Error("Failed to fetch EFS mount targets from AWS",
					"file_system_id", fileSystemID,
					"error", err)
			} else {
				mountTargets = targets
			}
		}

		if len(mountTargets) == 0 {
			continue
		}

		// Track unique VPCs to avoid duplicate edges
		vpcEdgesCreated := make(map[string]bool)

		for _, mt := range mountTargets {
			// EFS → VPC (BELONGS_TO) - only create one edge per VPC
			if mt.VpcId != "" && !vpcEdgesCreated[mt.VpcId] {
				if vpcNode, exists := lookup.ByResourceID[mt.VpcId]; exists {
					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipBelongsTo,
						map[string]interface{}{
							"connection_type": "vpc",
							"vpc_id":          mt.VpcId,
						}, req))
					vpcEdgesCreated[mt.VpcId] = true
				} else {
					s.logger.Warn("EFS mount target VPC not found in lookup", "fs_id", fileSystemID, "vpc_id", mt.VpcId)
				}
				// Store VPC ID in node properties
				node.Properties["vpc_id"] = mt.VpcId
			}

			// EFS → Subnet (HOSTED_ON) - one edge per mount target subnet
			if mt.SubnetId != "" {
				if subnetNode, exists := lookup.ByResourceID[mt.SubnetId]; exists {
					edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type":        "mount_target",
							"mount_target_id":        mt.MountTargetId,
							"subnet_id":              mt.SubnetId,
							"availability_zone":      mt.AvailabilityZoneName,
							"availability_zone_id":   mt.AvailabilityZoneId,
							"mount_target_ip":        mt.IpAddress,
							"mount_target_lifecycle": mt.LifeCycleState,
						}, req))
				} else {
					s.logger.Warn("EFS mount target Subnet not found in lookup", "fs_id", fileSystemID, "subnet_id", mt.SubnetId)
				}
			}

			// EFS → NetworkInterface (ASSOCIATED_WITH) - mount target ENI
			if mt.NetworkInterfaceId != "" {
				if eniNode, exists := lookup.ByResourceID[mt.NetworkInterfaceId]; exists {
					edges = append(edges, s.createEdge(node, eniNode, core.RelationshipAssociatedWith,
						map[string]interface{}{
							"connection_type":      "mount_target_eni",
							"mount_target_id":      mt.MountTargetId,
							"network_interface_id": mt.NetworkInterfaceId,
							"mount_target_ip":      mt.IpAddress,
						}, req))
				}
			}
		}
	}

	return edges
}
