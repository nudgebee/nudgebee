package core

import (
	"fmt"
	"strings"
)

// UniqueKeyComponents represents the 6-part structure of a unique key
// Format: {cloud_provider}:{account}:{location}:{NodeType}:{hierarchy}:{name}
//
// cloud_provider describes the platform a resource lives on (aws, k8s, gcp,
// azure, external). It is NOT the observer that produced the node — the
// observer is recorded separately on KgNode.Source.
type UniqueKeyComponents struct {
	CloudProvider string   // aws, k8s, gcp, azure, external
	Account       string   // account ID, subscription ID, cluster name, project ID
	Location      string   // region, zone, availability zone, or blank if not applicable
	NodeType      NodeType // the type of node
	Hierarchy     string   // VPC, resource group, namespace, or blank if not applicable
	Name          string   // resource name or identifier
}

const (
	// UniqueKeyPartCount is the expected number of parts in a unique key
	UniqueKeyPartCount = 6

	// CloudProvider constants — the only valid values for the position-0
	// segment of a unique key.
	CloudProviderAWS      = "aws"
	CloudProviderK8s      = "k8s"
	CloudProviderGCP      = "gcp"
	CloudProviderAzure    = "azure"
	CloudProviderExternal = "external"
)

// NewUniqueKeyComponents creates a new UniqueKeyComponents with defaults
func NewUniqueKeyComponents(cloudProvider string, nodeType NodeType) *UniqueKeyComponents {
	return &UniqueKeyComponents{
		CloudProvider: cloudProvider,
		Account:       "",
		Location:      "",
		NodeType:      nodeType,
		Hierarchy:     "",
		Name:          "",
	}
}

// Build constructs the unique key string from components
// Returns a 6-part colon-separated string
func (c *UniqueKeyComponents) Build() string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		c.CloudProvider,
		c.Account,
		c.Location,
		c.NodeType,
		c.Hierarchy,
		c.Name,
	)
}

// Validate checks if all required components are present
func (c *UniqueKeyComponents) Validate() error {
	if c.CloudProvider == "" {
		return fmt.Errorf("cloud_provider is required")
	}
	if c.NodeType == "" {
		return fmt.Errorf("node type is required")
	}
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	// Account, Location, and Hierarchy can be empty (blank) if not applicable
	// They will remain as empty strings in the unique key
	return nil
}

// ParseUniqueKey parses a unique key string into components
// Expected format: {cloud_provider}:{account}:{location}:{NodeType}:{hierarchy}:{name}
func ParseUniqueKey(uniqueKey string) (*UniqueKeyComponents, error) {
	parts := strings.Split(uniqueKey, ":")
	if len(parts) != UniqueKeyPartCount {
		return nil, fmt.Errorf("invalid unique key format: expected %d parts, got %d", UniqueKeyPartCount, len(parts))
	}

	return &UniqueKeyComponents{
		CloudProvider: parts[0],
		Account:       parts[1],
		Location:      parts[2],
		NodeType:      NodeType(parts[3]),
		Hierarchy:     parts[4],
		Name:          parts[5],
	}, nil
}

// GetCloudProvider extracts the cloud_provider from a unique key (first component).
func GetCloudProvider(uniqueKey string) string {
	parts := strings.Split(uniqueKey, ":")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// GetAccount extracts the account from a unique key (second component)
func GetAccount(uniqueKey string) string {
	parts := strings.Split(uniqueKey, ":")
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}

// GetLocation extracts the location from a unique key (third component)
func GetLocation(uniqueKey string) string {
	parts := strings.Split(uniqueKey, ":")
	if len(parts) > 2 {
		return parts[2]
	}
	return ""
}

// GetNodeTypeFromKey extracts the node type from a unique key (fourth component)
func GetNodeTypeFromKey(uniqueKey string) NodeType {
	parts := strings.Split(uniqueKey, ":")
	if len(parts) > 3 {
		return NodeType(parts[3])
	}
	return ""
}

// GetHierarchy extracts the hierarchy from a unique key (fifth component)
func GetHierarchy(uniqueKey string) string {
	parts := strings.Split(uniqueKey, ":")
	if len(parts) > 4 {
		return parts[4]
	}
	return ""
}

// GetName extracts the name from a unique key (sixth component)
func GetName(uniqueKey string) string {
	parts := strings.Split(uniqueKey, ":")
	if len(parts) > 5 {
		return parts[5]
	}
	return ""
}

// IsValidUniqueKey checks if a unique key has the correct format
func IsValidUniqueKey(uniqueKey string) bool {
	parts := strings.Split(uniqueKey, ":")
	return len(parts) == UniqueKeyPartCount
}

// BuildUniqueKey is a convenience function to build a unique key from individual components.
// The first argument is the cloud_provider (aws/k8s/gcp/azure/external) — NOT the observer.
func BuildUniqueKey(cloudProvider string, account string, location string, nodeType NodeType, hierarchy string, name string) string {
	// Empty values are left as blank strings in the unique key
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", cloudProvider, account, location, nodeType, hierarchy, name)
}

// k8sNodeTypes is the set of NodeType values that always map to cloud_provider="k8s"
// regardless of which observer produced them.
var k8sNodeTypes = map[NodeType]bool{
	NodeTypeCluster:       true,
	NodeTypeNamespace:     true,
	NodeTypePod:           true,
	NodeTypeNode:          true,
	NodeTypeJob:           true,
	NodeTypeCronJob:       true,
	NodeTypeCRD:           true,
	NodeTypeWorkload:      true,
	NodeTypeK8sService:    true,
	NodeTypeIngress:       true,
	NodeTypeNetworkPolicy: true,
	NodeTypeConfigMap:     true,
	NodeTypeK8sSecret:     true,
	NodeTypePVC:           true,
	NodeTypePV:            true,
}

// DeriveCloudProvider returns the platform a resource lives on, given the
// observer that produced it and the node type.
//
// Rules:
//   - Static observers (aws/k8s/gcp/azure) → matching cloud provider.
//   - Flow observers (ebpf/traces/datadog-apm) → "k8s" for K8s nodetypes,
//     "external" for everything else.
//   - "cloud" (cloud-enrichment fallback) → "external"; cloud_enrichment.go
//     should pass an explicit cloud_provider per classifier match instead of
//     relying on this fallback.
//   - Anything else → "external".
//
// For cloud_enrichment.go classifier outcomes, do NOT use this helper — the
// resolver knows which classifier matched and should pass cloud_provider
// (aws/azure/gcp/k8s) explicitly.
func DeriveCloudProvider(observerSource string, nodeType NodeType) string {
	switch observerSource {
	case CloudProviderAWS:
		return CloudProviderAWS
	case CloudProviderK8s:
		return CloudProviderK8s
	case CloudProviderGCP:
		return CloudProviderGCP
	case CloudProviderAzure:
		return CloudProviderAzure
	}
	if k8sNodeTypes[nodeType] {
		return CloudProviderK8s
	}
	return CloudProviderExternal
}
