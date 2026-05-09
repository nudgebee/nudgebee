package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"time"
)

// FlowSourceBuildRequest contains parameters for building flow relationships from a source
// Flow sources read the existing graph from DB and create relationships based on their data
type FlowSourceBuildRequest struct {
	TenantID       string
	CloudAccountID string
	TimeRange      *core.TimeRange
	Filters        map[string]string

	// ExistingNodes are the nodes currently in the knowledge graph (from DB or in-memory)
	// Flow sources use these to find nodes to connect
	ExistingNodes []*core.DbNode
}

// FlowSourceMetrics tracks metrics for a flow source
type FlowSourceMetrics struct {
	RelationshipsCreated int
	NodesMatched         int
	NodesNotMatched      int
	ErrorCount           int
	BuildDuration        time.Duration
}

// FlowSourceConfig holds common configuration for all flow sources
type FlowSourceConfig struct {
	TenantID       string
	CloudAccountID string
	Enabled        bool
}

// FlowData represents raw flow information from external sources
// This is a generic structure that flow sources can use to represent their data
type FlowData struct {
	SourceIdentifier      string                 // Identifier for the source node (e.g., service name)
	DestinationIdentifier string                 // Identifier for the destination node
	RelationshipType      core.RelationshipType  // Type of relationship (CALLS, PUBLISHES_TO, etc.)
	Properties            map[string]interface{} // Additional properties (latency, request count, etc.)
	Timestamp             time.Time              // When this flow was observed
}

// MatchResult contains the result of a node matching operation
type MatchResult struct {
	Node          *core.DbNode
	Matched       bool
	MatchedBy     string  // Which property/criteria matched
	Confidence    float64 // Confidence score (0.0 - 1.0)
	MatchStrategy string  // Which matching strategy was used
}
