package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// Azure hostname suffixes
const (
	AzureHostnameSuffix          = ".windows.net"
	AzureRedisHostnameSuffix     = ".redis.cache.windows.net"
	AzureSQLHostnameSuffix       = ".database.windows.net"
	AzureCosmosDBHostnameSuffix  = ".documents.azure.com"
	AzureBlobHostnameSuffix      = ".blob.core.windows.net"
	AzureQueueHostnameSuffix     = ".queue.core.windows.net"
	AzureTableHostnameSuffix     = ".table.core.windows.net"
	AzureFileHostnameSuffix      = ".file.core.windows.net"
	AzureServiceBusHostSuffix    = ".servicebus.windows.net"
	AzureEventHubsHostSuffix     = ".servicebus.windows.net"
	AzureKeyVaultHostSuffix      = ".vault.azure.net"
	AzureAzureCDNSuffix          = ".azureedge.net"
	AzureFrontDoorSuffix         = ".azurefd.net"
	AzureTrafficManagerSuffix    = ".trafficmanager.net"
	AzureAppServiceSuffix        = ".azurewebsites.net"
	AzureFunctionsSuffix         = ".azurewebsites.net"
	AzureContainerRegistrySuffix = ".azurecr.io"
	AzurePostgreSQLSuffix        = ".postgres.database.azure.com"
	AzureMySQLSuffix             = ".mysql.database.azure.com"
	AzureMariaDBSuffix           = ".mariadb.database.azure.com"
)

// AzureClassifier classifies Azure hostnames to their corresponding node types
type AzureClassifier struct{}

// NewAzureClassifier creates a new Azure hostname classifier
func NewAzureClassifier() *AzureClassifier {
	return &AzureClassifier{}
}

// ClassifyHostname determines the node type and service name from an Azure hostname
// Returns (NodeType, serviceName) - NodeType will be empty string if not an Azure hostname
func (c *AzureClassifier) ClassifyHostname(hostname string) (core.NodeType, string) {
	hostnameLower := strings.ToLower(hostname)

	// Check if it's an Azure hostname
	if !c.IsAzureHostname(hostnameLower) {
		return "", ""
	}

	return c.classifyByPattern(hostnameLower)
}

// IsAzureHostname checks if the hostname is an Azure hostname
func (c *AzureClassifier) IsAzureHostname(hostname string) bool {
	hostnameLower := strings.ToLower(hostname)
	return strings.Contains(hostnameLower, AzureHostnameSuffix) ||
		strings.Contains(hostnameLower, ".azure.com") ||
		strings.Contains(hostnameLower, ".azure.net") ||
		strings.Contains(hostnameLower, ".azureedge.net") ||
		strings.Contains(hostnameLower, ".azurefd.net") ||
		strings.Contains(hostnameLower, ".azurecr.io") ||
		strings.Contains(hostnameLower, ".trafficmanager.net")
}

// classifyByPattern classifies the hostname by pattern matching
func (c *AzureClassifier) classifyByPattern(hostname string) (core.NodeType, string) {
	switch {
	// Azure Redis Cache patterns
	case strings.Contains(hostname, ".redis.cache.windows.net") ||
		strings.Contains(hostname, ".vnet.redis.cache.windows.net"):
		return core.NodeTypeCache, "azure-redis"

	// Azure SQL Database patterns
	case strings.Contains(hostname, ".database.windows.net"):
		return core.NodeTypeDatabase, "azure-sql"

	// Azure PostgreSQL patterns
	case strings.Contains(hostname, ".postgres.database.azure.com"):
		return core.NodeTypeDatabase, "azure-postgresql"

	// Azure MySQL patterns
	case strings.Contains(hostname, ".mysql.database.azure.com"):
		return core.NodeTypeDatabase, "azure-mysql"

	// Azure MariaDB patterns
	case strings.Contains(hostname, ".mariadb.database.azure.com"):
		return core.NodeTypeDatabase, "azure-mariadb"

	// Azure Cosmos DB patterns
	case strings.Contains(hostname, ".documents.azure.com") ||
		strings.Contains(hostname, ".cosmos.azure.com"):
		return core.NodeTypeDatabase, "azure-cosmosdb"

	// Azure Blob Storage patterns
	case strings.Contains(hostname, ".blob.core.windows.net"):
		return core.NodeTypeStorage, "azure-blob"

	// Azure Queue Storage patterns
	case strings.Contains(hostname, ".queue.core.windows.net"):
		return core.NodeTypeMessageQueue, "azure-queue"

	// Azure Table Storage patterns
	case strings.Contains(hostname, ".table.core.windows.net"):
		return core.NodeTypeStorage, "azure-table"

	// Azure File Storage patterns
	case strings.Contains(hostname, ".file.core.windows.net"):
		return core.NodeTypeStorage, "azure-file"

	// Azure Service Bus patterns (messaging)
	case strings.Contains(hostname, ".servicebus.windows.net"):
		return core.NodeTypeMessageQueue, "azure-servicebus"

	// Azure Key Vault patterns
	case strings.Contains(hostname, ".vault.azure.net"):
		return core.NodeTypeSecretVault, "azure-keyvault"

	// Azure CDN patterns
	case strings.Contains(hostname, ".azureedge.net"):
		return core.NodeTypeCDN, "azure-cdn"

	// Azure Front Door patterns
	case strings.Contains(hostname, ".azurefd.net"):
		return core.NodeTypeLoadBalancer, "azure-frontdoor"

	// Azure Traffic Manager patterns
	case strings.Contains(hostname, ".trafficmanager.net"):
		return core.NodeTypeLoadBalancer, "azure-trafficmanager"

	// Azure App Service / Functions patterns
	case strings.Contains(hostname, ".azurewebsites.net"):
		// Could be App Service or Functions
		return core.NodeTypeServerlessFunction, "azure-appservice"

	// Azure Container Registry patterns
	case strings.Contains(hostname, ".azurecr.io"):
		return core.NodeTypeContainerRegistry, "azure-acr"

	// Azure Event Hubs patterns (subset of servicebus domain)
	// Note: This is already handled by servicebus check above

	// Default: generic Azure cloud resource
	default:
		return core.NodeTypeCloudResource, "azure"
	}
}

// ExtractRedisIdentifier extracts the Redis cache name from an Azure Redis hostname
// Format: {name}.redis.cache.windows.net or {name}.{region}.vnet.redis.cache.windows.net
func (c *AzureClassifier) ExtractRedisIdentifier(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) >= 4 && strings.Contains(hostname, ".redis.cache.windows.net") {
		return parts[0]
	}
	return ""
}

// ExtractResourceName extracts the resource name from Azure hostname
// Most Azure hostnames follow: {resource-name}.{service}.{region}.{domain}
func (c *AzureClassifier) ExtractResourceName(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
