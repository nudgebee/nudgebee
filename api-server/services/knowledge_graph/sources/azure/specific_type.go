package azure

import (
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// determineSpecificType resolves the concrete Azure native type (specific_type,
// e.g. AzureVirtualMachine) from the ontology NodeType + service_name. Emits
// vendor-prefixed names; defaults to the NodeType name so it's never blank.
func (s *AzureSource) determineSpecificType(nodeType core.NodeType, serviceName, resourceType string) string {
	switch nodeType {
	case core.NodeTypeComputeInstance:
		return "AzureVirtualMachine"
	case core.NodeTypeDatabase:
		sn := strings.ToLower(serviceName)
		if strings.Contains(sn, "cosmos") || strings.Contains(sn, "documentdb") {
			return "AzureCosmosDB"
		}
		return "AzureSQLDatabase"
	case core.NodeTypeManagedCluster:
		return "AKSCluster"
	case core.NodeTypeStorage:
		return "AzureStorageAccount"
	case core.NodeTypeCache:
		return "AzureCacheForRedis"
	case core.NodeTypeServerlessFunction:
		return "AzureFunctionApp"
	case core.NodeTypeLoadBalancer:
		return "AzureLoadBalancer"
	case core.NodeTypeBackendPool:
		return "AzureBackendPool"
	case core.NodeTypeVPC:
		return "AzureVirtualNetwork"
	case core.NodeTypeSubnet:
		return "AzureSubnet"
	case core.NodeTypeSecurityGroup:
		return "AzureNetworkSecurityGroup"
	case core.NodeTypeNetworkInterface:
		return "AzureNetworkInterface"
	case core.NodeTypeRouteTable:
		return "AzureRouteTable"
	case core.NodeTypeNetworkGateway:
		return "AzureNATGateway"
	case core.NodeTypePrivateEndpoint:
		return "AzurePrivateEndpoint"
	case core.NodeTypePublicIP:
		return "AzurePublicIP"
	case core.NodeTypeCDN:
		return "AzureCDNProfile"
	case core.NodeTypeDNSZone:
		return "AzureDNSZone"
	case core.NodeTypeContainerRegistry:
		return "AzureContainerRegistry"
	case core.NodeTypeSecretVault:
		return "AzureKeyVault"
	case core.NodeTypeEncryptionKey:
		return "AzureKeyVaultKey"
	case core.NodeTypeAPIGateway:
		return "AzureAPIManagement"
	case core.NodeTypeMonitoringService:
		return "AzureMonitor"
	case core.NodeTypeLogAggregator:
		return "AzureLogAnalytics"
	case core.NodeTypeServiceIdentity:
		return "AzureManagedIdentity"
	default:
		return string(nodeType)
	}
}
