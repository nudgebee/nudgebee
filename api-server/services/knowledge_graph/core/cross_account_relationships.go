package core

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

//go:embed default_relationships.json
var defaultRelationshipsJSON []byte

var (
	defaultRelationships     []CrossAccountRelationship
	defaultRelationshipsOnce sync.Once
	defaultRelationshipsErr  error
)

// loadDefaultRelationships loads the default cross-account relationships from embedded JSON
// Uses sync.Once to ensure it's only loaded once
func loadDefaultRelationships() ([]CrossAccountRelationship, error) {
	defaultRelationshipsOnce.Do(func() {
		defaultRelationshipsErr = json.Unmarshal(defaultRelationshipsJSON, &defaultRelationships)
	})
	return defaultRelationships, defaultRelationshipsErr
}

// BuildCrossAccountRelationships creates relationships between nodes from different sources/accounts
// based on configured matching rules. If no rules are provided, uses default relationships from embedded JSON.
func (s *Service) BuildCrossAccountRelationships(graph *Graph, rules []CrossAccountRelationship) ([]*DbEdge, error) {
	// If no rules provided, use defaults
	if len(rules) == 0 {
		s.logger.Info("no cross-account relationship rules provided, loading defaults")
		defaults, err := loadDefaultRelationships()
		if err != nil {
			s.logger.Error("failed to load default relationships", "error", err)
			return nil, fmt.Errorf("failed to load default relationships: %w", err)
		}
		rules = defaults
		s.logger.Info("loaded default cross-account relationships", "count", len(rules))
	}

	s.logger.Info("building cross-account relationships",
		"rules_count", len(rules),
		"nodes_count", len(graph.Nodes))

	newEdges := make([]*DbEdge, 0)

	// Process each rule
	for _, rule := range rules {
		if !rule.Enabled {
			s.logger.Info("skipping disabled rule", "rule_name", rule.Name)
			continue
		}

		s.logger.Info("processing cross-account relationship rule",
			"rule_name", rule.Name,
			"source_type", rule.SourceType,
			"target_type", rule.TargetType,
			"source_node_type", rule.SourceNodeType,
			"target_node_type", rule.TargetNodeType)

		// Get source and target nodes
		sourceNodes := s.filterNodesByTypeAndSource(graph.Nodes, rule.SourceNodeType, rule.SourceType)
		targetNodes := s.filterNodesByTypeAndSource(graph.Nodes, rule.TargetNodeType, rule.TargetType)

		s.logger.Info("filtered nodes for matching",
			"rule_name", rule.Name,
			"source_nodes", len(sourceNodes),
			"target_nodes", len(targetNodes))

		// Match nodes and create relationships
		matches := 0
		for _, sourceNode := range sourceNodes {
			for _, targetNode := range targetNodes {
				// Skip if same account and cross-account is not allowed
				if !rule.CrossAccount && sourceNode.CloudAccountID != targetNode.CloudAccountID {
					continue
				}

				// Check if nodes match based on rules
				if s.nodesMatch(sourceNode, targetNode, rule.MatchingRules) {
					// Create edge from source to target
					edge := NewEdge(
						sourceNode.ID,
						targetNode.ID,
						rule.RelationshipType,
						map[string]interface{}{
							"cross_account_rule": rule.Name,
							"source_type":        rule.SourceType,
							"target_type":        rule.TargetType,
						},
						graph.TenantID,
						targetNode.CloudAccountID, // Use target's account ID
						"cross_account",
					)
					newEdges = append(newEdges, edge)
					matches++

					// Create bidirectional edge if configured
					if rule.Bidirectional {
						reverseEdge := NewEdge(
							targetNode.ID,
							sourceNode.ID,
							rule.RelationshipType,
							map[string]interface{}{
								"cross_account_rule": rule.Name,
								"source_type":        rule.TargetType,
								"target_type":        rule.SourceType,
								"bidirectional":      true,
							},
							graph.TenantID,
							sourceNode.CloudAccountID,
							"cross_account",
						)
						newEdges = append(newEdges, reverseEdge)
					}
				}
			}
		}

		s.logger.Info("completed cross-account relationship rule",
			"rule_name", rule.Name,
			"matches_found", matches)
	}

	s.logger.Info("completed building cross-account relationships",
		"new_edges", len(newEdges))

	return newEdges, nil
}

// filterNodesByTypeAndSource filters nodes by node type and source
func (s *Service) filterNodesByTypeAndSource(nodes []*DbNode, nodeType NodeType, source string) []*DbNode {
	filtered := make([]*DbNode, 0)
	for _, node := range nodes {
		if node.NodeType == nodeType && node.Source == source {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// nodesMatch checks if two nodes match based on the provided matching rules
// All rules must match (AND logic)
func (s *Service) nodesMatch(sourceNode, targetNode *DbNode, rules []MatchingRule) bool {
	if len(rules) == 0 {
		return false
	}

	// All rules must match (AND logic)
	for _, rule := range rules {
		if !s.propertyMatch(sourceNode, targetNode, rule) {
			return false
		}
	}

	return true
}

// propertyMatch checks if source and target properties match based on a single rule
func (s *Service) propertyMatch(sourceNode, targetNode *DbNode, rule MatchingRule) bool {
	// Get property values from both nodes
	sourceValue := getNestedProperty(sourceNode.Properties, rule.SourceProperty)
	targetValue := getNestedProperty(targetNode.Properties, rule.TargetProperty)

	// Handle nil values early
	if sourceValue == nil || targetValue == nil {
		return false
	}

	// Check if target is an array - iterate through all elements
	if targetArray, ok := targetValue.([]interface{}); ok {
		for _, item := range targetArray {
			if s.matchValues(sourceValue, item, rule) {
				return true
			}
			// If item is a map (like container objects), try to extract nested property
			if itemMap, ok := item.(map[string]interface{}); ok {
				// For containers array, try to match against specific fields like "image"
				for _, value := range itemMap {
					if s.matchValues(sourceValue, value, rule) {
						return true
					}
				}
			}
		}
		return false
	}

	// Standard single-value matching
	return s.matchValues(sourceValue, targetValue, rule)
}

// matchValues performs the actual matching logic between two values
func (s *Service) matchValues(sourceValue, targetValue interface{}, rule MatchingRule) bool {
	// Convert to strings
	sourceStr := fmt.Sprintf("%v", sourceValue)
	targetStr := fmt.Sprintf("%v", targetValue)

	// Handle empty values
	if sourceStr == "" || targetStr == "" || sourceStr == "<nil>" || targetStr == "<nil>" {
		return false
	}

	// Apply case sensitivity
	if !rule.CaseSensitive {
		sourceStr = strings.ToLower(sourceStr)
		targetStr = strings.ToLower(targetStr)
	}

	// Apply matching logic based on match type
	switch rule.MatchType {
	case MatchTypeExact:
		return sourceStr == targetStr

	case MatchTypeContains:
		return strings.Contains(targetStr, sourceStr) || strings.Contains(sourceStr, targetStr)

	case MatchTypeRegex:
		// Try to compile and match regex
		regex, err := regexp.Compile(sourceStr)
		if err != nil {
			s.logger.Warn("invalid regex pattern",
				"pattern", sourceStr,
				"error", err)
			return false
		}
		return regex.MatchString(targetStr)

	default:
		s.logger.Warn("unknown match type", "match_type", rule.MatchType)
		return false
	}
}

// getNestedProperty retrieves a nested property from a properties map
// Supports dot notation like "labels.dd_service_name" or "metadata.annotations.key"
func getNestedProperty(properties map[string]interface{}, path string) interface{} {
	if properties == nil {
		return nil
	}

	// Split path by dots
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}

	// Navigate through nested properties
	current := interface{}(properties)
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, exists := v[part]
			if !exists {
				return nil
			}
			current = val
		case map[interface{}]interface{}:
			// Handle cases where keys might be interface{}
			val, exists := v[part]
			if !exists {
				return nil
			}
			current = val
		default:
			// Cannot navigate further
			return nil
		}
	}

	return current
}

// GetNodePropertyString is already defined in helpers.go
// This helper safely extracts string properties from a node and supports nested property paths
