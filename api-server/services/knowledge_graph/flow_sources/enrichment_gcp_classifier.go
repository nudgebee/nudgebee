package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// GCP hostname suffixes
const (
	GCPGoogleAPIsSuffix     = ".googleapis.com"
	GCPAppSpotSuffix        = ".appspot.com"
	GCPCloudFunctionsSuffix = ".cloudfunctions.net"
	GCPCloudRunSuffix       = ".run.app"
	GCPGoogleCloudSuffix    = ".cloud.google.com"
)

// GCPClassifier classifies GCP hostnames to their corresponding node types
type GCPClassifier struct{}

// NewGCPClassifier creates a new GCP hostname classifier
func NewGCPClassifier() *GCPClassifier {
	return &GCPClassifier{}
}

// ClassifyHostname determines the node type and service name from a GCP hostname
// Returns (NodeType, serviceName) - NodeType will be empty string if not a GCP hostname
func (c *GCPClassifier) ClassifyHostname(hostname string) (core.NodeType, string) {
	hostnameLower := strings.ToLower(hostname)

	if !c.IsGCPHostname(hostnameLower) {
		return "", ""
	}

	return c.classifyByPattern(hostnameLower)
}

// IsGCPHostname checks if the hostname is a GCP hostname
func (c *GCPClassifier) IsGCPHostname(hostname string) bool {
	hostnameLower := strings.ToLower(hostname)
	return strings.Contains(hostnameLower, GCPGoogleAPIsSuffix) ||
		strings.Contains(hostnameLower, GCPAppSpotSuffix) ||
		strings.Contains(hostnameLower, GCPCloudFunctionsSuffix) ||
		strings.Contains(hostnameLower, GCPCloudRunSuffix) ||
		strings.Contains(hostnameLower, GCPGoogleCloudSuffix)
}

// classifyByPattern classifies the hostname by pattern matching
func (c *GCPClassifier) classifyByPattern(hostname string) (core.NodeType, string) {
	switch {
	// Cloud SQL patterns
	case strings.Contains(hostname, "sqladmin.googleapis.com") ||
		strings.Contains(hostname, "sql.googleapis.com"):
		return core.NodeTypeDatabase, "gcp-cloudsql"

	// BigQuery patterns
	case strings.Contains(hostname, "bigquery.googleapis.com") ||
		strings.Contains(hostname, "bigquerystorage.googleapis.com"):
		return core.NodeTypeDatabase, "gcp-bigquery"

	// Cloud Storage patterns
	case strings.Contains(hostname, "storage.googleapis.com"):
		return core.NodeTypeStorage, "gcp-cloudstorage"

	// Compute Engine patterns
	case strings.Contains(hostname, "compute.googleapis.com"):
		return core.NodeTypeComputeInstance, "gcp-compute"

	// Kubernetes Engine / Container patterns
	case strings.Contains(hostname, "container.googleapis.com"):
		return core.NodeTypeManagedCluster, "gcp-gke"

	// Cloud Monitoring patterns
	case strings.Contains(hostname, "monitoring.googleapis.com"):
		return core.NodeTypeMonitoringService, "gcp-monitoring"

	// Cloud Logging patterns
	case strings.Contains(hostname, "logging.googleapis.com"):
		return core.NodeTypeLogAggregator, "gcp-logging"

	// Vertex AI / AI Platform patterns
	case strings.Contains(hostname, "aiplatform.googleapis.com") ||
		strings.Contains(hostname, "ml.googleapis.com"):
		return core.NodeTypeAIService, "gcp-vertexai"

	// Cloud Functions
	case strings.Contains(hostname, "cloudfunctions.googleapis.com") ||
		strings.HasSuffix(hostname, GCPCloudFunctionsSuffix):
		return core.NodeTypeServerlessFunction, "gcp-cloudfunctions"

	// Cloud Run
	case strings.Contains(hostname, "run.googleapis.com") ||
		strings.HasSuffix(hostname, GCPCloudRunSuffix):
		return core.NodeTypeServerlessFunction, "gcp-cloudrun"

	// App Engine
	case strings.HasSuffix(hostname, GCPAppSpotSuffix):
		return core.NodeTypeServerlessFunction, "gcp-appengine"

	// Pub/Sub patterns
	case strings.Contains(hostname, "pubsub.googleapis.com"):
		return core.NodeTypeMessageQueue, "gcp-pubsub"

	// Cloud DNS patterns
	case strings.Contains(hostname, "dns.googleapis.com"):
		return core.NodeTypeDNSZone, "gcp-clouddns"

	// Cloud KMS patterns
	case strings.Contains(hostname, "cloudkms.googleapis.com"):
		return core.NodeTypeEncryptionKey, "gcp-cloudkms"

	// Secret Manager patterns
	case strings.Contains(hostname, "secretmanager.googleapis.com"):
		return core.NodeTypeSecretVault, "gcp-secretmanager"

	// Artifact Registry / Container Registry
	case strings.Contains(hostname, "artifactregistry.googleapis.com") ||
		strings.Contains(hostname, "gcr.io"):
		return core.NodeTypeContainerRegistry, "gcp-artifactregistry"

	// Cloud Memorystore (Redis)
	case strings.Contains(hostname, "redis.googleapis.com"):
		return core.NodeTypeCache, "gcp-memorystore"

	// Firestore / Datastore
	case strings.Contains(hostname, "firestore.googleapis.com") ||
		strings.Contains(hostname, "datastore.googleapis.com"):
		return core.NodeTypeDatabase, "gcp-firestore"

	// Spanner
	case strings.Contains(hostname, "spanner.googleapis.com"):
		return core.NodeTypeDatabase, "gcp-spanner"

	// Cloud Filestore
	case strings.Contains(hostname, "file.googleapis.com"):
		return core.NodeTypeStorage, "gcp-filestore"

	// Security Command Center
	case strings.Contains(hostname, "securitycenter.googleapis.com"):
		return core.NodeTypeSecurityService, "gcp-securitycenter"

	// Cloud CDN (part of compute, but identifiable)
	case strings.Contains(hostname, "cdn.googleapis.com"):
		return core.NodeTypeCDN, "gcp-cdn"

	// Generic googleapis.com - return CloudResource as fallback
	case strings.HasSuffix(hostname, GCPGoogleAPIsSuffix):
		return core.NodeTypeCloudResource, "gcp-generic"

	default:
		return core.NodeTypeCloudResource, "gcp-unknown"
	}
}

// ExtractResourceName extracts a resource name from a GCP hostname
// For example: "my-instance.us-central1.c.my-project.internal" -> "my-instance"
func (c *GCPClassifier) ExtractResourceName(hostname string) string {
	if hostname == "" {
		return ""
	}

	parts := strings.Split(hostname, ".")
	if len(parts) > 0 {
		return parts[0]
	}

	return ""
}
