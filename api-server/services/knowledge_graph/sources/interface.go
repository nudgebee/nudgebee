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
//
// UserAccount is the one exception to keying on properties["name"]: a person's
// display name is editable (in GitLab/GitHub/PagerDuty's own profile UI) and is
// not unique, so keying on it meant two people sharing a display name collapsed
// into one node with blended edges, and a rename re-keyed a node into a second
// one (#41505, #37011). Those nodes key on properties["username"] — the login /
// external user id, which is unique per integration and which Identity Sync
// already treats as the person's stable handle.
func (b *BaseSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	// Create key components with defaults
	keyComponents := core.NewUniqueKeyComponents(b.sourceName, node.NodeType)

	// Extract the key's name component: the stable login for identities (see the
	// doc comment), the display name for everything else.
	nameProperty := "name"
	if node.NodeType == core.NodeTypeUserAccount {
		nameProperty = "username"
	}
	if nameVal, ok := node.Properties[nameProperty]; ok {
		if nameStr, ok := nameVal.(string); ok {
			keyComponents.Name = nameStr
		}
	}

	// A UserAccount with no username still needs a key: fall back to the display
	// name rather than dropping through to the node id, which is derived from the
	// key itself and would change on every build.
	if keyComponents.Name == "" && nameProperty != "name" {
		if nameVal, ok := node.Properties["name"]; ok {
			if nameStr, ok := nameVal.(string); ok {
				keyComponents.Name = nameStr
			}
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
