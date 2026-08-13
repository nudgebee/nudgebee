package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// cloudSQLInstanceSchema is the concrete per-specific_type property schema for
// the CloudSQLInstance specific_type. Names are the node.Properties keys written by
// extractGCPCloudSQLMetadata + enrichCloudSQLFromCLI; universal base keys are
// implicit. Indexed mirrors core.QueryablePropertiesMap[NodeTypeDatabase] for
// the fields GCP populates (engine/storage_type/dns_name/private_ip/vpc_id),
// plus native tier/availability_type. instance_type is declared but NOT Indexed
// (governed by the existing map only).
var cloudSQLInstanceSchema = core.SpecificTypeSchema{
	SpecificType: "CloudSQLInstance",
	NodeType:     core.NodeTypeDatabase,
	Properties: []core.PropertyDef{
		{Name: "engine", Indexed: true},
		{Name: "dns_name", Indexed: true},
		{Name: "private_ip", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "storage_type", Indexed: true},
		{Name: "tier", Indexed: true},
		{Name: "availability_type", Indexed: true},
		{Name: "instance_type"},
		{Name: "master_instance_name"},
		{Name: "instance_state"},
		{Name: "public_ip"},
		{Name: "vpc_network_url"},
		{Name: "storage_size_gb"},
		{Name: "ssl_mode"},
		{Name: "backup_enabled"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "database_version"},
		{Name: "database_engine"},
		{Name: "gce_zone"},
		{Name: "backend_type"},
		{Name: "network_id"},
		{Name: "service_account_email"},
		{Name: "connection_name"},
		{Name: "disk_size_gb"},
		{Name: "disk_type"},
		{Name: "require_ssl"},
		{Name: "ip_addresses"},
		{Name: "authorized_networks"},
		{Name: "backup_configuration"},
		{Name: "database_flags"},
	},
}

func init() { core.RegisterSpecificTypeSchema(cloudSQLInstanceSchema) }

// extractCloudSQLSettings lifts sizing / HA fields from a Cloud SQL Admin API
// `settings` object into top-level properties. Each read is guarded so an absent
// field is skipped (the Cloud SQL settings fields tier/disk_size_gb/disk_type/
// availability_type/backup_enabled). Property names mirror the ones
// enrichCloudSQLFromCLI sets, so the meta-only and CLI paths agree.
func extractCloudSQLSettings(properties, settings map[string]interface{}) {
	if tier, ok := settings["tier"].(string); ok && tier != "" {
		properties["tier"] = tier
	}
	if diskSize, ok := settings["dataDiskSizeGb"]; ok && diskSize != nil {
		properties["storage_size_gb"] = diskSize
	}
	if diskType, ok := settings["dataDiskType"].(string); ok && diskType != "" {
		properties["storage_type"] = diskType
	}
	if availType, ok := settings["availabilityType"].(string); ok && availType != "" {
		properties["availability_type"] = availType
	}
	if backupCfg, ok := settings["backupConfiguration"].(map[string]interface{}); ok {
		if enabled, ok := backupCfg["enabled"].(bool); ok {
			properties["backup_enabled"] = enabled
		}
	}
}

// extractGCPCloudSQLMetadata extracts network info from sqladmin.googleapis.com/Instance meta
func (s *GCPSource) extractGCPCloudSQLMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Database version
	if dbVersion, ok := metaMap["databaseVersion"].(string); ok && dbVersion != "" {
		properties["engine"] = dbVersion
	}

	// Connection name (used as dns_name)
	if connectionName, ok := metaMap["connectionName"].(string); ok && connectionName != "" {
		properties["dns_name"] = connectionName
	}

	// IP addresses
	if ipAddresses, ok := metaMap["ipAddresses"].([]interface{}); ok && len(ipAddresses) > 0 {
		if ip, ok := ipAddresses[0].(map[string]interface{}); ok {
			if ipAddr, ok := ip["ipAddress"].(string); ok && ipAddr != "" {
				properties["private_ip"] = ipAddr
			}
		}
	}

	// VPC + sizing/HA fields from the Cloud SQL Admin API `settings` object.
	if settings, ok := metaMap["settings"].(map[string]interface{}); ok {
		if ipConfig, ok := settings["ipConfiguration"].(map[string]interface{}); ok {
			if privateNetwork, ok := ipConfig["privateNetwork"].(string); ok && privateNetwork != "" {
				vpcName := extractGCPResourceNameFromURL(privateNetwork)
				properties["vpc_id"] = vpcName
				properties["vpc_network_url"] = privateNetwork
			}
		}
		extractCloudSQLSettings(properties, settings)
	}

	// Instance type and replica relationship
	if instanceType, ok := metaMap["instanceType"].(string); ok && instanceType != "" {
		properties["instance_type"] = instanceType
	}
	if masterInstanceName, ok := metaMap["masterInstanceName"].(string); ok && masterInstanceName != "" {
		// Format: "project:instance-name" → extract short name after ":"
		colonIdx := strings.LastIndex(masterInstanceName, ":")
		if colonIdx >= 0 && colonIdx < len(masterInstanceName)-1 {
			properties["master_instance_name"] = masterInstanceName[colonIdx+1:]
		} else {
			properties["master_instance_name"] = masterInstanceName
		}
	}
}

// fetchCloudSQLInstancesFromGCP fetches all Cloud SQL instances via gcloud CLI
func (s *GCPSource) fetchCloudSQLInstancesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPCloudSQLInstance, error) {
	cmd := "gcloud sql instances list --format=json"

	s.logger.Info("fetching GCP Cloud SQL instances via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var instances []GCPCloudSQLInstance
	if err := json.Unmarshal([]byte(output), &instances); err != nil {
		return nil, fmt.Errorf("failed to parse Cloud SQL instances response: %w", err)
	}

	return instances, nil
}

// enrichCloudSQLFromCLI enriches a Cloud SQL node with CLI data
func (s *GCPSource) enrichCloudSQLFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	sqlInst, ok := cliData.sqlInstances[shortName]
	if !ok {
		return
	}

	// Network information (only if not already set)
	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if sqlInst.Settings.IpConfiguration.PrivateNetwork != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(sqlInst.Settings.IpConfiguration.PrivateNetwork)
			node.Properties["vpc_network_url"] = sqlInst.Settings.IpConfiguration.PrivateNetwork
		}
	}

	// Basic properties
	if sqlInst.ConnectionName != "" {
		node.Properties["dns_name"] = sqlInst.ConnectionName
	}
	if sqlInst.DatabaseVersion != "" {
		node.Properties["engine"] = sqlInst.DatabaseVersion
	}
	if sqlInst.State != "" {
		node.Properties["instance_state"] = sqlInst.State
	}
	if sqlInst.Region != "" {
		node.Properties["region"] = sqlInst.Region
	}

	// IP addresses
	for _, ip := range sqlInst.IpAddresses {
		if ip.Type == "PRIVATE" && ip.IpAddress != "" {
			node.Properties["private_ip"] = ip.IpAddress
		} else if ip.Type == "PRIMARY" && ip.IpAddress != "" {
			node.Properties["public_ip"] = ip.IpAddress
		}
	}

	// Additional Cloud SQL settings
	if sqlInst.Settings.Tier != "" {
		node.Properties["tier"] = sqlInst.Settings.Tier
	}
	if sqlInst.Settings.AvailabilityType != "" {
		node.Properties["availability_type"] = sqlInst.Settings.AvailabilityType
	}
	if sqlInst.Settings.DataDiskSizeGb != "" {
		node.Properties["storage_size_gb"] = sqlInst.Settings.DataDiskSizeGb
	}
	if sqlInst.Settings.DataDiskType != "" {
		node.Properties["storage_type"] = sqlInst.Settings.DataDiskType
	}
	if sqlInst.Settings.IpConfiguration.SslMode != "" {
		node.Properties["ssl_mode"] = sqlInst.Settings.IpConfiguration.SslMode
	}
	node.Properties["backup_enabled"] = sqlInst.Settings.BackupConfiguration.Enabled

	// Replica relationship (for cloud-sql billing type nodes that lack meta)
	if sqlInst.InstanceType != "" {
		if _, already := node.Properties["instance_type"]; !already {
			node.Properties["instance_type"] = sqlInst.InstanceType
		}
	}
	if sqlInst.MasterInstanceName != "" {
		if _, already := node.Properties["master_instance_name"]; !already {
			colonIdx := strings.LastIndex(sqlInst.MasterInstanceName, ":")
			if colonIdx >= 0 && colonIdx < len(sqlInst.MasterInstanceName)-1 {
				node.Properties["master_instance_name"] = sqlInst.MasterInstanceName[colonIdx+1:]
			} else {
				node.Properties["master_instance_name"] = sqlInst.MasterInstanceName
			}
		}
	}
}

// createCloudSQLEdges creates edges from Cloud SQL instances to VPCs
func (s *GCPSource) createCloudSQLEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	dbNodes, ok := lookup.ByNodeType[core.NodeTypeDatabase]
	if !ok {
		return edges
	}

	for _, node := range dbNodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "Cloud SQL" {
			continue
		}

		// Cloud SQL → VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}
	}

	s.logger.Info("created GCP Cloud SQL edges", "edge_count", len(edges))
	return edges
}

// createCloudSQLReplicaEdges creates BELONGS_TO edges from Cloud SQL replicas to their primary instances
// Uses meta.instanceType == "READ_REPLICA_INSTANCE" and meta.masterInstanceName == "project:instance-name"
func (s *GCPSource) createCloudSQLReplicaEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	dbNodes, ok := lookup.ByNodeType[core.NodeTypeDatabase]
	if !ok {
		return edges
	}

	for _, node := range dbNodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "Cloud SQL" {
			continue
		}

		instanceType, _ := node.Properties["instance_type"].(string)
		if instanceType != "READ_REPLICA_INSTANCE" {
			continue
		}

		masterInstanceName, _ := node.Properties["master_instance_name"].(string)
		if masterInstanceName == "" {
			continue
		}

		// Find the primary instance node by short name
		primaryNode := findNodeByNameAndType(lookup, core.NodeTypeDatabase, masterInstanceName)
		if primaryNode == nil {
			continue
		}

		edges = append(edges, s.createEdge(node, primaryNode, core.RelationshipBelongsTo,
			map[string]interface{}{"connection_type": "read_replica"}, req))
		s.logger.Debug("created Cloud SQL replica → primary edge",
			"replica", getNodeName(node),
			"primary", masterInstanceName)
	}

	s.logger.Info("created GCP Cloud SQL replica edges", "edge_count", len(edges))
	return edges
}
