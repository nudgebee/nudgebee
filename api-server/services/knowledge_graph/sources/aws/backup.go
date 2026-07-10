package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// backupVaultSchema — concrete schema for AWS Backup vaults (NodeTypeBackupVault).
// Fields written by extractBackupVaultMetadata.
var backupVaultSchema = core.SpecificTypeSchema{
	SpecificType: "BackupVault",
	NodeType:     core.NodeTypeBackupVault,
	Properties: []core.PropertyDef{
		{Name: "vault_type"},
		{Name: "vault_state"},
		{Name: "locked"},
		{Name: "encryption_key_arn"},
		{Name: "number_of_recovery_points"},
		{Name: "creation_date"},
	},
}

// backupPlanSchema — concrete schema for AWS Backup plans (NodeTypeBackupPolicy).
// Fields written by extractBackupPolicyMetadata.
var backupPlanSchema = core.SpecificTypeSchema{
	SpecificType: "BackupPlan",
	NodeType:     core.NodeTypeBackupPolicy,
	Properties: []core.PropertyDef{
		{Name: "target_backup_vault_name"},
		{Name: "primary_rule_name"},
		{Name: "rule_count"},
		{Name: "version_id"},
		{Name: "delete_after_days"},
		{Name: "move_to_cold_storage_after_days"},
		{Name: "creation_date"},
		{Name: "last_execution_date"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(backupVaultSchema)
	core.RegisterSpecificTypeSchema(backupPlanSchema)
}

// extractBackupVaultMetadata extracts essential fields for AWS Backup Vaults
func (s *AWSSource) extractBackupVaultMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Vault locked status
	if locked, ok := metaMap["Locked"].(bool); ok {
		properties["locked"] = locked
	}

	// Vault type (BACKUP_VAULT, LOGICALLY_AIR_GAPPED_BACKUP_VAULT)
	if vaultType, ok := metaMap["VaultType"].(string); ok && vaultType != "" {
		properties["vault_type"] = vaultType
	}

	// Vault state
	if vaultState, ok := metaMap["VaultState"].(string); ok && vaultState != "" {
		properties["vault_state"] = vaultState
	}

	// Encryption key ARN (for IS_ENCRYPTED_BY edge)
	if encryptionKeyArn, ok := metaMap["EncryptionKeyArn"].(string); ok && encryptionKeyArn != "" {
		properties["encryption_key_arn"] = encryptionKeyArn
	}

	// Number of recovery points
	if numRecoveryPoints, ok := metaMap["NumberOfRecoveryPoints"].(float64); ok {
		properties["number_of_recovery_points"] = int(numRecoveryPoints)
	}

	// Creation date
	if creationDate, ok := metaMap["CreationDate"].(string); ok && creationDate != "" {
		properties["creation_date"] = creationDate
	}
}

// extractBackupPolicyMetadata extracts essential fields for AWS Backup Plans
func (s *AWSSource) extractBackupPolicyMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Version ID
	if versionId, ok := metaMap["VersionId"].(string); ok && versionId != "" {
		properties["version_id"] = versionId
	}

	// Plan details contain the backup rules
	if planDetails, ok := metaMap["PlanDetails"].(map[string]interface{}); ok {
		// Extract rules
		if rules, ok := planDetails["Rules"].([]interface{}); ok && len(rules) > 0 {
			properties["rule_count"] = len(rules)

			// Extract target vault from first rule
			if firstRule, ok := rules[0].(map[string]interface{}); ok {
				if targetVaultName, ok := firstRule["TargetBackupVaultName"].(string); ok && targetVaultName != "" {
					properties["target_backup_vault_name"] = targetVaultName
				}
				if ruleName, ok := firstRule["RuleName"].(string); ok && ruleName != "" {
					properties["primary_rule_name"] = ruleName
				}
				// Extract lifecycle info
				if lifecycle, ok := firstRule["Lifecycle"].(map[string]interface{}); ok {
					if deleteAfter, ok := lifecycle["DeleteAfterDays"].(float64); ok {
						properties["delete_after_days"] = int(deleteAfter)
					}
					if moveToCold, ok := lifecycle["MoveToColdStorageAfterDays"].(float64); ok {
						properties["move_to_cold_storage_after_days"] = int(moveToCold)
					}
				}
			}
		}
	}

	// Creation date
	if creationDate, ok := metaMap["CreationDate"].(string); ok && creationDate != "" {
		properties["creation_date"] = creationDate
	}

	// Last execution date
	if lastExecutionDate, ok := metaMap["LastExecutionDate"].(string); ok && lastExecutionDate != "" {
		properties["last_execution_date"] = lastExecutionDate
	}
}

// createBackupVaultEdges creates edges for AWS Backup Vault resources
// BackupVault → EncryptionKey (IS_ENCRYPTED_BY)
func (s *AWSSource) createBackupVaultEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// Check for encryption key ARN in metadata
		if encryptionKeyArn, ok := meta["EncryptionKeyArn"].(string); ok && encryptionKeyArn != "" {
			// Look up by ARN
			if kmsNode, exists := lookup.ByARN[encryptionKeyArn]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"encryption_key_arn": encryptionKeyArn,
					}, req))
			} else {
				// Try to find by key ID (last part of ARN)
				parts := strings.Split(encryptionKeyArn, "/")
				if len(parts) > 0 {
					keyID := parts[len(parts)-1]
					if kmsNode, exists := lookup.ByResourceID[keyID]; exists {
						edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
							map[string]interface{}{
								"encryption_key_arn": encryptionKeyArn,
								"encryption_key_id":  keyID,
							}, req))
					}
				}
			}
		}
	}

	return edges
}

// createBackupPolicyEdges creates edges for AWS Backup Plan resources
// BackupPolicy → BackupVault (STORES_IN)
func (s *AWSSource) createBackupPolicyEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use the already-extracted target_backup_vault_name from node properties
		// (extracted by extractBackupPolicyMetadata from PlanDetails.Rules)
		targetVaultName, ok := node.Properties["target_backup_vault_name"].(string)
		if !ok || targetVaultName == "" {
			continue
		}

		// Look up backup vault by name
		if vaultNodes, exists := lookup.ByNodeType[core.NodeTypeBackupVault]; exists {
			for _, vaultNode := range vaultNodes {
				if name, ok := vaultNode.Properties["name"].(string); ok && name == targetVaultName {
					edges = append(edges, s.createEdge(node, vaultNode, core.RelationshipStoresIn,
						map[string]interface{}{
							"target_vault_name": targetVaultName,
						}, req))
					break
				}
			}
		}
	}

	return edges
}
