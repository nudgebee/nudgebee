package sources

import (
	"context"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
)

// Source represents a source that can provide knowledge graph data
type Source interface {
	// GetName returns the name of the source (e.g., "trace", "aws", "k8s")
	GetName() string

	// BuildGraph builds a knowledge graph from the source
	BuildGraph(ctx context.Context, req *BuildRequest) (*core.Graph, error)

	// IsEnabled checks if the source is enabled and configured
	IsEnabled() bool

	// Validate validates the source configuration
	Validate() error
}

// BuildRequest contains parameters for building a knowledge graph from a source
type BuildRequest struct {
	TenantID       string
	CloudAccountID string
	TimeRange      *core.TimeRange
	Filters        map[string]string
}

// SourceConfig holds common configuration for all sources
type SourceConfig struct {
	TenantID       string
	CloudAccountID string
	Enabled        bool
}

// SourceMetrics tracks metrics for a source
type SourceMetrics struct {
	NodesExtracted int
	EdgesExtracted int
	ErrorCount     int
	BuildDuration  float64 // seconds
}

// BaseSource provides a default implementation for common source functionality
// Sources can embed this struct to inherit default behavior and override methods as needed
type BaseSource struct {
	sourceName string
}

// NewBaseSource creates a new BaseSource with the given source name
func NewBaseSource(sourceName string) BaseSource {
	return BaseSource{
		sourceName: sourceName,
	}
}

// GenerateUniqueKey generates a unique key for a node
// Default implementation uses the 6-part format:
// {source}:{account}:{location}:{NodeType}:{hierarchy}:{name}
// Sources can override this method to implement custom unique key generation logic
func (b *BaseSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	// Create key components with defaults
	keyComponents := core.NewUniqueKeyComponents(b.sourceName, node.NodeType)

	// Extract name from properties
	if nameVal, ok := node.Properties["name"]; ok {
		if nameStr, ok := nameVal.(string); ok {
			keyComponents.Name = nameStr
		}
	}

	// If no name found, try other common identifiers
	if keyComponents.Name == "" {
		if idVal, ok := node.Properties["id"]; ok {
			if idStr, ok := idVal.(string); ok {
				keyComponents.Name = idStr
			}
		}
	}

	// Fallback to node ID if no name or id found
	if keyComponents.Name == "" {
		keyComponents.Name = node.ID
	}

	// Extract account (use cloud_account_id if available)
	if node.CloudAccountID != "" {
		keyComponents.Account = node.CloudAccountID
	}

	// Validate and build the key
	if err := keyComponents.Validate(); err != nil {
		// Fallback to simple format if validation fails
		return fmt.Sprintf("%s:%s:%s:%s:%s:%s", b.sourceName, "", "", node.NodeType, "", keyComponents.Name)
	}

	return keyComponents.Build()
}
