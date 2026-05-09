package core

import (
	"fmt"
	"regexp"
	"strings"
)

// MatchCriteria defines criteria for matching nodes
type MatchCriteria struct {
	// NodeType filters by node type (optional)
	NodeType NodeType

	// Source filters by source (optional) - e.g., "k8s", "aws"
	Source string

	// AccountID filters by cloud account ID (optional)
	AccountID string

	// PropertyMatches defines property-based matching rules (AND logic between rules)
	PropertyMatches []PropertyMatch

	// UniqueKeyPattern matches against unique_key (optional)
	UniqueKeyPattern string

	// UniqueKeyMatchType defines how to match unique_key
	UniqueKeyMatchType MatchType

	// Labels filters by labels (exact match on all specified labels)
	Labels map[string]string
}

// PropertyMatch defines a single property matching rule
type PropertyMatch struct {
	// PropertyPath is the path to the property (e.g., "name", "service_name", "labels.app")
	PropertyPath string

	// Value is the value to match against
	Value string

	// MatchType defines how to match (exact, contains, regex)
	MatchType MatchType

	// CaseSensitive determines if matching is case-sensitive
	CaseSensitive bool

	// Optional indicates this match is optional (won't fail if property doesn't exist)
	Optional bool
}

// MatchResult contains the result of a node matching operation
type MatchResult struct {
	Node          *DbNode
	Matched       bool
	MatchedBy     string  // Which property/criteria matched
	Confidence    float64 // Confidence score (0.0 - 1.0)
	MatchStrategy string  // Which matching strategy was used
}

// NodeMatcher provides flexible node matching capabilities
// Used by flow sources and resolvers to find nodes in the existing graph
type NodeMatcher struct {
	nodes []*DbNode
}

// NewNodeMatcher creates a new NodeMatcher with the given nodes
func NewNodeMatcher(nodes []*DbNode) *NodeMatcher {
	return &NodeMatcher{
		nodes: nodes,
	}
}

// FindNode finds a single node matching the criteria
// Returns the first node that matches all criteria
func (m *NodeMatcher) FindNode(criteria MatchCriteria) (*MatchResult, error) {
	results := m.FindNodes(criteria)
	if len(results) == 0 {
		return &MatchResult{
			Node:       nil,
			Matched:    false,
			Confidence: 0.0,
		}, fmt.Errorf("no node found matching criteria")
	}

	// Return the highest confidence match
	bestMatch := results[0]
	for _, result := range results {
		if result.Confidence > bestMatch.Confidence {
			bestMatch = result
		}
	}

	return bestMatch, nil
}

// FindNodes finds all nodes matching the criteria
func (m *NodeMatcher) FindNodes(criteria MatchCriteria) []*MatchResult {
	results := make([]*MatchResult, 0)

	for _, node := range m.nodes {
		if match, confidence, matchedBy, strategy := m.matchNode(node, criteria); match {
			results = append(results, &MatchResult{
				Node:          node,
				Matched:       true,
				MatchedBy:     matchedBy,
				Confidence:    confidence,
				MatchStrategy: strategy,
			})
		}
	}

	return results
}

// FindNodeByUniqueKey finds a node by its exact unique key
func (m *NodeMatcher) FindNodeByUniqueKey(uniqueKey string) (*DbNode, error) {
	for _, node := range m.nodes {
		if node.UniqueKey == uniqueKey {
			return node, nil
		}
	}
	return nil, fmt.Errorf("node with unique_key '%s' not found", uniqueKey)
}

// FindNodesByType finds all nodes of a specific type
func (m *NodeMatcher) FindNodesByType(nodeType NodeType) []*DbNode {
	matches := make([]*DbNode, 0)
	for _, node := range m.nodes {
		if node.NodeType == nodeType {
			matches = append(matches, node)
		}
	}
	return matches
}

// FindNodeByProperty finds nodes by a single property match
func (m *NodeMatcher) FindNodeByProperty(propertyPath, value string, matchType MatchType, caseSensitive bool) []*DbNode {
	criteria := MatchCriteria{
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  propertyPath,
				Value:         value,
				MatchType:     matchType,
				CaseSensitive: caseSensitive,
			},
		},
	}

	results := m.FindNodes(criteria)
	matches := make([]*DbNode, 0)
	for _, result := range results {
		if result.Matched {
			matches = append(matches, result.Node)
		}
	}
	return matches
}

// matchNode checks if a node matches all criteria
// Returns: (matched, confidence, matchedBy, strategy)
func (m *NodeMatcher) matchNode(node *DbNode, criteria MatchCriteria) (bool, float64, string, string) {
	confidence := 1.0
	matchedBy := ""
	strategy := "property-match"

	// Filter by NodeType
	if criteria.NodeType != "" && node.NodeType != criteria.NodeType {
		return false, 0.0, "", ""
	}

	// Filter by Source
	if criteria.Source != "" && node.Source != criteria.Source {
		return false, 0.0, "", ""
	}

	// Filter by AccountID
	if criteria.AccountID != "" && node.CloudAccountID != criteria.AccountID {
		return false, 0.0, "", ""
	}

	// Match by UniqueKey pattern
	if criteria.UniqueKeyPattern != "" {
		matched, conf := m.matchString(node.UniqueKey, criteria.UniqueKeyPattern, criteria.UniqueKeyMatchType, true)
		if !matched {
			return false, 0.0, "", ""
		}
		confidence = conf
		matchedBy = "unique_key"
		strategy = "unique-key-match"
	}

	// Match all property criteria (AND logic)
	for _, propMatch := range criteria.PropertyMatches {
		matched, conf, path := m.matchProperty(node, propMatch)
		if !matched && !propMatch.Optional {
			return false, 0.0, "", ""
		}
		if matched {
			// Reduce confidence slightly for each additional match (prefer exact matches)
			confidence *= conf
			if matchedBy == "" {
				matchedBy = path
			} else {
				matchedBy += "," + path
			}
		}
	}

	// Match labels (exact match on all specified labels)
	if len(criteria.Labels) > 0 {
		if node.Labels == nil {
			return false, 0.0, "", ""
		}
		for k, v := range criteria.Labels {
			if node.Labels[k] != v {
				return false, 0.0, "", ""
			}
		}
		if matchedBy == "" {
			matchedBy = "labels"
		} else {
			matchedBy += ",labels"
		}
		strategy = "label-match"
	}

	// If no property matches were specified but we got here, it's a match
	if len(criteria.PropertyMatches) == 0 && criteria.UniqueKeyPattern == "" && len(criteria.Labels) == 0 {
		matchedBy = "type-match"
		strategy = "type-match"
	}

	return true, confidence, matchedBy, strategy
}

// matchProperty checks if a node's property matches the criteria
// Returns: (matched, confidence, propertyPath)
func (m *NodeMatcher) matchProperty(node *DbNode, propMatch PropertyMatch) (bool, float64, string) {
	// Get property value from node
	value := m.getPropertyValue(node, propMatch.PropertyPath)
	if value == "" {
		return false, 0.0, ""
	}

	// Match the value
	matched, confidence := m.matchString(value, propMatch.Value, propMatch.MatchType, propMatch.CaseSensitive)
	return matched, confidence, propMatch.PropertyPath
}

// getPropertyValue retrieves a property value from a node using dot notation
// Supports: "name", "unique_key", "properties.service_name", "properties.labels.app"
func (m *NodeMatcher) getPropertyValue(node *DbNode, propertyPath string) string {
	parts := strings.Split(propertyPath, ".")

	// Handle top-level properties
	if len(parts) == 1 {
		switch parts[0] {
		case "name":
			if name, ok := node.Properties["name"].(string); ok {
				return name
			}
		case "unique_key":
			return node.UniqueKey
		case "source":
			return node.Source
		case "node_type":
			return string(node.NodeType)
		case "cloud_account_id":
			return node.CloudAccountID
		case "tenant_id":
			return node.TenantID
		default:
			// Try to get from properties
			if val, ok := node.Properties[parts[0]]; ok {
				return fmt.Sprintf("%v", val)
			}
		}
		return ""
	}

	// Handle nested properties (e.g., "properties.service_name")
	if parts[0] == "properties" {
		return m.getNestedProperty(node.Properties, parts[1:])
	}

	return ""
}

// getNestedProperty retrieves nested property values
func (m *NodeMatcher) getNestedProperty(props map[string]interface{}, path []string) string {
	if len(path) == 0 {
		return ""
	}

	val, ok := props[path[0]]
	if !ok {
		return ""
	}

	// If this is the last part of the path, return the value
	if len(path) == 1 {
		return fmt.Sprintf("%v", val)
	}

	// If there are more parts, continue traversing
	if nestedMap, ok := val.(map[string]interface{}); ok {
		return m.getNestedProperty(nestedMap, path[1:])
	}

	return ""
}

// matchString matches a string value against a pattern
// Returns: (matched, confidence)
func (m *NodeMatcher) matchString(value, pattern string, matchType MatchType, caseSensitive bool) (bool, float64) {
	if !caseSensitive {
		value = strings.ToLower(value)
		pattern = strings.ToLower(pattern)
	}

	switch matchType {
	case MatchTypeExact:
		if value == pattern {
			return true, 1.0 // Highest confidence for exact match
		}
		return false, 0.0

	case MatchTypeContains:
		if strings.Contains(value, pattern) {
			// Confidence based on how much of the value is the pattern
			confidence := float64(len(pattern)) / float64(len(value))
			if confidence > 1.0 {
				confidence = 1.0
			}
			return true, 0.8 * confidence // Contains is less confident than exact
		}
		return false, 0.0

	case MatchTypeRegex:
		matched, err := regexp.MatchString(pattern, value)
		if err != nil {
			return false, 0.0
		}
		if matched {
			return true, 0.9 // Regex match has high confidence
		}
		return false, 0.0

	default:
		return false, 0.0
	}
}

// FindServiceByName is a convenience method to find a service by name
// Searches across multiple possible property paths
func (m *NodeMatcher) FindServiceByName(serviceName string) (*DbNode, error) {
	// Try multiple strategies to find the service
	strategies := []MatchCriteria{
		// Strategy 1: Exact match on name property
		{
			NodeType: NodeTypeService,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         serviceName,
					MatchType:     MatchTypeExact,
					CaseSensitive: false,
				},
			},
		},
		// Strategy 2: Match on service_name property
		{
			NodeType: NodeTypeService,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "properties.service_name",
					Value:         serviceName,
					MatchType:     MatchTypeExact,
					CaseSensitive: false,
				},
			},
		},
		// Strategy 3: Contains match on name
		{
			NodeType: NodeTypeService,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         serviceName,
					MatchType:     MatchTypeContains,
					CaseSensitive: false,
				},
			},
		},
	}

	for _, strategy := range strategies {
		result, err := m.FindNode(strategy)
		if err == nil && result.Matched {
			return result.Node, nil
		}
	}

	return nil, fmt.Errorf("service '%s' not found", serviceName)
}

// FindWorkloadByName is a convenience method to find a workload (Pod) by name
func (m *NodeMatcher) FindWorkloadByName(workloadName, namespace string) (*DbNode, error) {
	criteria := MatchCriteria{
		NodeType: NodeTypeWorkload,
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  "name",
				Value:         workloadName,
				MatchType:     MatchTypeContains,
				CaseSensitive: false,
			},
		},
	}

	// Add namespace filter if provided
	if namespace != "" {
		criteria.PropertyMatches = append(criteria.PropertyMatches, PropertyMatch{
			PropertyPath:  "properties.namespace",
			Value:         namespace,
			MatchType:     MatchTypeExact,
			CaseSensitive: false,
		})
	}

	result, err := m.FindNode(criteria)
	if err != nil {
		return nil, fmt.Errorf("workload '%s' not found", workloadName)
	}

	return result.Node, nil
}

// GetNodes returns the underlying nodes slice
func (m *NodeMatcher) GetNodes() []*DbNode {
	return m.nodes
}
