package gcp

import (
	"nudgebee/services/knowledge_graph/core"
)

// determineSpecificType resolves the concrete GCP specific_type (e.g.
// GCEInstance) from the ontology NodeType + service_name. Mirrors the
// subtype switch in createNodeFromResource but emits vendor-prefixed names.
// Defaults to the NodeType name so it's never blank.
func (s *GCPSource) determineSpecificType(nodeType core.NodeType, serviceName, resourceType string) string {
	switch nodeType {
	case core.NodeTypeComputeInstance:
		return "GCEInstance"
	case core.NodeTypeDatabase:
		switch serviceName {
		case "Cloud SQL":
			return "CloudSQLInstance"
		case "BigQuery", "bigquery.googleapis.com":
			return "BigQueryDataset"
		default:
			return "GCPDatabase"
		}
	case core.NodeTypeManagedCluster:
		return "GKECluster"
	case core.NodeTypeStorage:
		switch serviceName {
		case "Cloud Storage":
			return "GCSBucket"
		case "Cloud Filestore":
			return "FilestoreInstance"
		case "compute.googleapis.com/Disk":
			return "PersistentDisk"
		default:
			return "GCPStorage"
		}
	case core.NodeTypeCache:
		return "MemorystoreInstance"
	case core.NodeTypeServerlessFunction:
		return "CloudRunService"
	case core.NodeTypeTopic:
		return "PubSubTopic"
	case core.NodeTypeContainerRegistry:
		return "ArtifactRegistry"
	case core.NodeTypeLoadBalancer:
		return "GCPLoadBalancer"
	case core.NodeTypeBackendPool:
		return "GCPBackendService"
	case core.NodeTypeVPC:
		return "GCPVpc"
	case core.NodeTypeSubnet:
		return "GCPSubnet"
	case core.NodeTypeSecurityGroup:
		return "GCPFirewall"
	case core.NodeTypeDNSZone:
		return "CloudDNSZone"
	case core.NodeTypeNetworkGateway:
		return "CloudNATGateway"
	case core.NodeTypePublicIP:
		return "GCPExternalIP"
	case core.NodeTypeLogAggregator:
		return "CloudLogging"
	case core.NodeTypeMonitoringService:
		return "CloudMonitoring"
	case core.NodeTypeAIService:
		return "VertexAI"
	case core.NodeTypeSecretVault:
		return "SecretManagerSecret"
	case core.NodeTypeEncryptionKey:
		return "CloudKMSKey"
	case core.NodeTypeServiceIdentity:
		return "GCPServiceAccount"
	default:
		return string(nodeType)
	}
}
