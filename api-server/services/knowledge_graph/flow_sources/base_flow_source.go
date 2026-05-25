package flow_sources

import (
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"time"

	"github.com/google/uuid"
)

// BaseFlowSource provides common functionality for flow sources
// Flow sources can embed this to inherit default behavior
type BaseFlowSource struct {
	name        string
	category    core.FlowSourceCategory
	enabled     bool
	logger      *slog.Logger
	config      FlowSourceConfig //nolint:unused // Reserved for future configuration needs
	metrics     FlowSourceMetrics
	nodeMatcher *NodeMatcher
	priority    FlowSourcePriority
}

// NewBaseFlowSource creates a new BaseFlowSource
func NewBaseFlowSource(name string, category core.FlowSourceCategory, enabled bool, logger *slog.Logger) *BaseFlowSource {
	if logger == nil {
		logger = slog.Default()
	}

	return &BaseFlowSource{
		name:     name,
		category: category,
		enabled:  enabled,
		logger:   logger,
		metrics:  FlowSourceMetrics{},
		priority: PriorityMedium,
	}
}

// GetName returns the name of the flow source
func (b *BaseFlowSource) GetName() string {
	return b.name
}

// GetSourceCategory returns the category of the flow source
func (b *BaseFlowSource) GetSourceCategory() core.FlowSourceCategory {
	return b.category
}

// IsEnabled returns whether the flow source is enabled
func (b *BaseFlowSource) IsEnabled() bool {
	return b.enabled
}

// SetEnabled sets the enabled state
func (b *BaseFlowSource) SetEnabled(enabled bool) {
	b.enabled = enabled
}

// GetPriority returns the execution priority
func (b *BaseFlowSource) GetPriority() FlowSourcePriority {
	return b.priority
}

// SetPriority sets the execution priority
func (b *BaseFlowSource) SetPriority(priority FlowSourcePriority) {
	b.priority = priority
}

// Validate provides basic validation
func (b *BaseFlowSource) Validate() error {
	if b.name == "" {
		return fmt.Errorf("flow source name cannot be empty")
	}
	return nil
}

// InitializeNodeMatcher initializes the node matcher with existing nodes
func (b *BaseFlowSource) InitializeNodeMatcher(nodes []*core.DbNode) {
	b.nodeMatcher = NewNodeMatcher(nodes)
	b.logger.Info("initialized node matcher for flow source",
		"source", b.name,
		"nodes_count", len(nodes))
}

// GetNodeMatcher returns the node matcher
func (b *BaseFlowSource) GetNodeMatcher() *NodeMatcher {
	return b.nodeMatcher
}

// CreateEdge is a helper method to create an edge between two nodes.
// Deduplication happens later via priority-based merging in DeduplicateEdgesWithPriority.
func (b *BaseFlowSource) CreateEdge(
	sourceNode *core.DbNode,
	destNode *core.DbNode,
	relationshipType core.RelationshipType,
	properties map[string]interface{},
	tenantID string,
	cloudAccountID string,
) *core.DbEdge {
	if properties == nil {
		properties = make(map[string]interface{})
	}

	// Add metadata about this edge
	properties["created_by_flow_source"] = b.name
	properties["flow_source_category"] = string(b.category)
	properties["source_priority"] = int(GetEdgeSourcePriority(b.name, relationshipType))

	edge := &core.DbEdge{
		ID:                uuid.New().String(),
		SourceNodeID:      sourceNode.ID,
		DestinationNodeID: destNode.ID,
		RelationshipType:  relationshipType,
		Properties:        properties,
		CloudAccountID:    cloudAccountID,
		TenantID:          tenantID,
		Source:            b.name,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	b.metrics.RelationshipsCreated++
	return edge
}

// CreateEdgeByUniqueKey creates an edge using unique keys instead of node IDs
// This is useful when you want to reference nodes by their unique_key
func (b *BaseFlowSource) CreateEdgeByUniqueKey(
	sourceUniqueKey string,
	destUniqueKey string,
	relationshipType core.RelationshipType,
	properties map[string]interface{},
	tenantID string,
	cloudAccountID string,
) (*core.DbEdge, error) {
	if b.nodeMatcher == nil {
		return nil, fmt.Errorf("node matcher not initialized")
	}

	sourceNode, err := b.nodeMatcher.FindNodeByUniqueKey(sourceUniqueKey)
	if err != nil {
		return nil, fmt.Errorf("source node not found: %w", err)
	}

	destNode, err := b.nodeMatcher.FindNodeByUniqueKey(destUniqueKey)
	if err != nil {
		return nil, fmt.Errorf("destination node not found: %w", err)
	}

	return b.CreateEdge(sourceNode, destNode, relationshipType, properties, tenantID, cloudAccountID), nil
}

// MatchSourceAndDestination is a helper to match both source and destination nodes
// Returns: (sourceNode, destNode, error)
func (b *BaseFlowSource) MatchSourceAndDestination(
	sourceCriteria MatchCriteria,
	destCriteria MatchCriteria,
) (*core.DbNode, *core.DbNode, error) {
	if b.nodeMatcher == nil {
		return nil, nil, fmt.Errorf("node matcher not initialized")
	}

	// Find source node
	sourceResult, err := b.nodeMatcher.FindNode(sourceCriteria)
	if err != nil {
		b.metrics.NodesNotMatched++
		return nil, nil, fmt.Errorf("source node not found: %w", err)
	}

	// Find destination node
	destResult, err := b.nodeMatcher.FindNode(destCriteria)
	if err != nil {
		b.metrics.NodesNotMatched++
		return nil, nil, fmt.Errorf("destination node not found: %w", err)
	}

	b.metrics.NodesMatched += 2
	b.logger.Debug("matched source and destination nodes",
		"source", b.name,
		"source_node", sourceResult.Node.UniqueKey,
		"dest_node", destResult.Node.UniqueKey,
		"source_confidence", sourceResult.Confidence,
		"dest_confidence", destResult.Confidence)

	return sourceResult.Node, destResult.Node, nil
}

// GetMetrics returns the metrics for this flow source
func (b *BaseFlowSource) GetMetrics() FlowSourceMetrics {
	return b.metrics
}

// ResetMetrics resets the metrics
func (b *BaseFlowSource) ResetMetrics() {
	b.metrics = FlowSourceMetrics{}
}

// LogMetrics logs the current metrics
func (b *BaseFlowSource) LogMetrics() {
	b.logger.Info("flow source metrics",
		"source", b.name,
		"relationships_created", b.metrics.RelationshipsCreated,
		"nodes_matched", b.metrics.NodesMatched,
		"nodes_not_matched", b.metrics.NodesNotMatched,
		"errors", b.metrics.ErrorCount,
		"duration_seconds", b.metrics.BuildDuration.Seconds())
}

// TrackBuildTime tracks the build duration
func (b *BaseFlowSource) TrackBuildTime(start time.Time) {
	b.metrics.BuildDuration = time.Since(start)
}

// IncrementErrorCount increments the error counter
func (b *BaseFlowSource) IncrementErrorCount() {
	b.metrics.ErrorCount++
}
