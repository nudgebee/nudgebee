package flow_sources

import (
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

// namespaceGuardEnvEnabled is the kill switch for the namespace guard in
// matchNode. The guard is on unless this is "false".
const namespaceGuardEnvEnabled = "KG_MATCH_NAMESPACE_GUARD_ENABLED"

// NodeMatcher provides flexible node matching capabilities for flow sources
// Flow sources use this to find nodes in the existing graph that match their flow data
type NodeMatcher struct {
	nodes          []*core.DbNode
	uniqueKeyIndex map[string]*core.DbNode
	nodeTypeIndex  map[core.NodeType][]*core.DbNode

	namespaceGuard bool
	// knownNamespaces is account -> lower-cased namespaces seen on
	// k8s-sourced nodes; built on first use by knownNamespacesOnce.
	knownNamespaces     map[string]map[string]struct{}
	knownNamespacesOnce sync.Once

	// guardRejections counts candidates the namespace guard refused, so a
	// build can report it at Info level (the per-rejection log is debug and
	// never seen in production). Atomic: matchers may be used from goroutines.
	guardRejections atomic.Int64
	sampleMu        sync.Mutex
	rejectionSample []string
}

// maxRejectionSamples caps the observed->candidate pairs kept for the summary.
const maxRejectionSamples = 5

// NewNodeMatcher creates a new NodeMatcher with the given nodes
func NewNodeMatcher(nodes []*core.DbNode) *NodeMatcher {
	ukIndex := make(map[string]*core.DbNode, len(nodes))
	ntIndex := make(map[core.NodeType][]*core.DbNode)
	for _, n := range nodes {
		if n.UniqueKey != "" {
			if _, exists := ukIndex[n.UniqueKey]; !exists {
				ukIndex[n.UniqueKey] = n
			}
		}
		ntIndex[n.NodeType] = append(ntIndex[n.NodeType], n)
	}
	return &NodeMatcher{
		nodes:          nodes,
		uniqueKeyIndex: ukIndex,
		nodeTypeIndex:  ntIndex,
		namespaceGuard: !strings.EqualFold(strings.TrimSpace(os.Getenv(namespaceGuardEnvEnabled)), "false"),
	}
}

// MatchCriteria defines criteria for matching nodes
type MatchCriteria struct {
	// NodeType filters by node type (optional)
	NodeType core.NodeType

	// Source filters by source (optional) - e.g., "k8s", "aws"
	Source string

	// AccountID filters by cloud account ID (optional)
	AccountID string

	// PropertyMatches defines property-based matching rules (AND logic between rules)
	PropertyMatches []PropertyMatch

	// UniqueKeyPattern matches against unique_key (optional)
	UniqueKeyPattern string

	// UniqueKeyMatchType defines how to match unique_key
	UniqueKeyMatchType core.MatchType

	// ObservedNamespace is the namespace the flow data reported for the
	// entity being matched. When set, a candidate whose own namespace is a
	// different, known one is rejected — see namespaceGuardRejects.
	ObservedNamespace string

	// ObservingAccountID is the account the observation came from, for
	// strategies that search every account (AccountID empty). It lets the
	// namespace guard know ObservedNamespace from the observing cluster even
	// when the candidate lives in another one. Ignored when AccountID is set.
	ObservingAccountID string
}

// PropertyMatch defines a single property matching rule
type PropertyMatch struct {
	// PropertyPath is the path to the property (e.g., "name", "service_name", "labels.app")
	PropertyPath string

	// Value is the value to match against
	Value string

	// MatchType defines how to match (exact, contains, regex)
	MatchType core.MatchType

	// CaseSensitive determines if matching is case-sensitive
	CaseSensitive bool

	// Optional indicates this match is optional (won't fail if property doesn't exist)
	Optional bool
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

	// When a NodeType filter is set, scan only nodes of that type via the index
	candidates := m.nodes
	if criteria.NodeType != "" {
		candidates = m.nodeTypeIndex[criteria.NodeType]
	}

	for _, node := range candidates {
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
func (m *NodeMatcher) FindNodeByUniqueKey(uniqueKey string) (*core.DbNode, error) {
	if node, ok := m.uniqueKeyIndex[uniqueKey]; ok {
		return node, nil
	}
	return nil, fmt.Errorf("node with unique_key '%s' not found", uniqueKey)
}

// FindNodesByType finds all nodes of a specific type
func (m *NodeMatcher) FindNodesByType(nodeType core.NodeType) []*core.DbNode {
	return m.nodeTypeIndex[nodeType]
}

// FindNodeByProperty finds nodes by a single property match
func (m *NodeMatcher) FindNodeByProperty(propertyPath, value string, matchType core.MatchType, caseSensitive bool) []*core.DbNode {
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
	matches := make([]*core.DbNode, 0)
	for _, result := range results {
		if result.Matched {
			matches = append(matches, result.Node)
		}
	}
	return matches
}

// matchNode checks if a node matches all criteria
// Returns: (matched, confidence, matchedBy, strategy)
func (m *NodeMatcher) matchNode(node *core.DbNode, criteria MatchCriteria) (bool, float64, string, string) {
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

	// If no property matches were specified but we got here, it's a match
	if len(criteria.PropertyMatches) == 0 && criteria.UniqueKeyPattern == "" {
		matchedBy = "type-match"
		strategy = "type-match"
	}

	// Checked last, so only candidates that matched everything else are
	// logged as rejected.
	if m.namespaceGuardRejects(node, criteria) {
		m.recordGuardRejection(criteria.ObservedNamespace, node)
		slog.Debug("node match rejected: candidate is in another known namespace",
			"observed_namespace", criteria.ObservedNamespace,
			"candidate_unique_key", node.UniqueKey,
			"candidate_namespace", node.Properties["namespace"],
			"node_type", node.NodeType,
			"account_scope", criteria.AccountID,
			"observing_account_id", criteria.ObservingAccountID,
			"matched_by", matchedBy,
			"strategy", strategy)
		return false, 0.0, "", ""
	}

	return true, confidence, matchedBy, strategy
}

// recordGuardRejection counts a rejection and keeps the first few
// observed->candidate namespace pairs for GuardRejectionSummary.
func (m *NodeMatcher) recordGuardRejection(observedNamespace string, node *core.DbNode) {
	m.guardRejections.Add(1)
	candidateNamespace, _ := node.Properties["namespace"].(string)
	pair := observedNamespace + "->" + candidateNamespace
	m.sampleMu.Lock()
	defer m.sampleMu.Unlock()
	if len(m.rejectionSample) >= maxRejectionSamples {
		return
	}
	for _, existing := range m.rejectionSample {
		if existing == pair {
			return
		}
	}
	m.rejectionSample = append(m.rejectionSample, pair)
}

// GuardRejectionSummary returns how many candidates the namespace guard
// rejected and up to maxRejectionSamples distinct observed->candidate pairs.
func (m *NodeMatcher) GuardRejectionSummary() (int64, []string) {
	m.sampleMu.Lock()
	defer m.sampleMu.Unlock()
	return m.guardRejections.Load(), append([]string(nil), m.rejectionSample...)
}

// namespaceGuardRejects reports whether a candidate that otherwise matched
// must be refused because it lives in a different namespace from the one the
// flow data observed. This is the same rule lookupApp applies to trace
// applications, applied to graph nodes, and it lives here because every
// FindNode/FindNodes lookup funnels through matchNode — the name-only
// strategies in each flow source have been patched one at a time six times
// (#34692, #34698, #35168, #37205, #37373, #39266) while their copies kept
// binding services to a same-named workload in another namespace.
//
// A candidate is rejected only when all of these hold:
//   - the caller passed an ObservedNamespace;
//   - the candidate has a namespace of its own and it differs (case-insensitive).
//     Namespace-less candidates (Node, Cluster, cloud resources) stay eligible,
//     which keeps the traces Kind=Node pseudo-namespace "node" matching;
//   - ObservedNamespace is known: a k8s-sourced node in the candidate's account
//     or the observing account is in it. An OTel service.namespace is often a
//     logical name no cluster has, and a namespace known nowhere never rejects,
//     so those providers keep today's behaviour. Counting the observing account
//     is what stops a cross-account strategy from taking the same wrong match
//     in another cluster after the same-account strategy refused it.
func (m *NodeMatcher) namespaceGuardRejects(node *core.DbNode, criteria MatchCriteria) bool {
	if !m.namespaceGuard || criteria.ObservedNamespace == "" {
		return false
	}
	candidateNamespace, _ := node.Properties["namespace"].(string)
	if candidateNamespace == "" || strings.EqualFold(candidateNamespace, criteria.ObservedNamespace) {
		return false
	}
	observingAccountID := criteria.AccountID
	if observingAccountID == "" {
		observingAccountID = criteria.ObservingAccountID
	}
	return m.isKnownNamespace(node.CloudAccountID, criteria.ObservedNamespace) ||
		(observingAccountID != "" && m.isKnownNamespace(observingAccountID, criteria.ObservedNamespace))
}

// isKnownNamespace reports whether a node the Kubernetes source built puts
// namespace in accountID. Only node.Source counts: nodes a flow source minted
// carry the observed namespace, which may be a logical one, and traces-minted
// Workloads get cloud_provider=k8s in their unique key, so neither the key nor
// cloud_provider can tell them apart from real cluster objects.
func (m *NodeMatcher) isKnownNamespace(accountID, namespace string) bool {
	m.knownNamespacesOnce.Do(func() {
		m.knownNamespaces = make(map[string]map[string]struct{})
		for _, n := range m.nodes {
			if n.Source != "k8s" {
				continue
			}
			ns, _ := n.Properties["namespace"].(string)
			if n.NodeType == core.NodeTypeNamespace {
				ns, _ = n.Properties["name"].(string)
			}
			if ns == "" {
				continue
			}
			if m.knownNamespaces[n.CloudAccountID] == nil {
				m.knownNamespaces[n.CloudAccountID] = make(map[string]struct{})
			}
			m.knownNamespaces[n.CloudAccountID][strings.ToLower(ns)] = struct{}{}
		}
	})
	_, ok := m.knownNamespaces[accountID][strings.ToLower(namespace)]
	return ok
}

// matchProperty checks if a node's property matches the criteria
// Returns: (matched, confidence, propertyPath)
func (m *NodeMatcher) matchProperty(node *core.DbNode, propMatch PropertyMatch) (bool, float64, string) {
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
func (m *NodeMatcher) getPropertyValue(node *core.DbNode, propertyPath string) string {
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
func (m *NodeMatcher) matchString(value, pattern string, matchType core.MatchType, caseSensitive bool) (bool, float64) {
	if !caseSensitive {
		value = strings.ToLower(value)
		pattern = strings.ToLower(pattern)
	}

	switch matchType {
	case core.MatchTypeExact:
		if value == pattern {
			return true, 1.0 // Highest confidence for exact match
		}
		return false, 0.0

	case core.MatchTypeContains:
		if strings.Contains(value, pattern) {
			// Confidence based on how much of the value is the pattern
			confidence := float64(len(pattern)) / float64(len(value))
			if confidence > 1.0 {
				confidence = 1.0
			}
			return true, 0.8 * confidence // Contains is less confident than exact
		}
		return false, 0.0

	case core.MatchTypeRegex:
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
func (m *NodeMatcher) FindServiceByName(serviceName string) (*core.DbNode, error) {
	// Try multiple strategies to find the service
	strategies := []MatchCriteria{
		// Strategy 1: Exact match on name property
		{
			NodeType: core.NodeTypeService,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         serviceName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		},
		// Strategy 2: Match on service_name property
		{
			NodeType: core.NodeTypeService,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "properties.service_name",
					Value:         serviceName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		},
		// Strategy 3: Contains match on name
		{
			NodeType: core.NodeTypeService,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         serviceName,
					MatchType:     core.MatchTypeContains,
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
func (m *NodeMatcher) FindWorkloadByName(workloadName, namespace string) (*core.DbNode, error) {
	criteria := MatchCriteria{
		NodeType: core.NodeTypeWorkload,
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  "name",
				Value:         workloadName,
				MatchType:     core.MatchTypeContains,
				CaseSensitive: false,
			},
		},
	}

	// Add namespace filter if provided
	if namespace != "" {
		criteria.PropertyMatches = append(criteria.PropertyMatches, PropertyMatch{
			PropertyPath:  "properties.namespace",
			Value:         namespace,
			MatchType:     core.MatchTypeExact,
			CaseSensitive: false,
		})
	}

	result, err := m.FindNode(criteria)
	if err != nil {
		return nil, fmt.Errorf("workload '%s' not found", workloadName)
	}

	return result.Node, nil
}
