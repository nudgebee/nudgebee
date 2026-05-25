package triage

import (
	"encoding/json"
	"fmt"
	"sort"

	"nudgebee/services/internal/database/models"
)

// ServiceNode represents a service in the dependency graph
type ServiceNode struct {
	ID struct {
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
		Name      string `json:"name"`
	} `json:"Id"`
	Upstreams   []ServiceLink `json:"Upstreams"`
	Downstreams []ServiceLink `json:"Downstreams"`
}

// ServiceLink represents a dependency link between services
type ServiceLink struct {
	ID     interface{} `json:"Id"` // Can be string or object
	Status int         `json:"Status"`
}

// nodeAliasPriority ranks node types for alias-registration order so that when
// the same namespace+name appears as multiple node types (e.g. a Workload and a
// K8sService both named "flagd"), the node with CALLS edges wins the alias.
// Lower = higher priority. Unknown types fall through to the default below.
// Shared between buildDependencyGraph and parseKnowledgeGraphEvidence — the
// ordering rule is the same regardless of which evidence shape the graph came
// from.
var nodeAliasPriority = map[string]int{
	"Workload":        0,
	"Service":         1,
	"K8sService":      2,
	"ExternalService": 3,
}

// nodeAliasPriorityDefault is returned for node types absent from
// nodeAliasPriority. Set above any known priority so known types win.
const nodeAliasPriorityDefault = 100

// aliasPriorityFor returns the registration priority for a node type.
func aliasPriorityFor(nodeType string) int {
	if p, ok := nodeAliasPriority[nodeType]; ok {
		return p
	}
	return nodeAliasPriorityDefault
}

// pendingAlias holds the (canonical, namespace, kind, name) tuple needed to
// register aliases for a node once all nodes have been added to the graph.
// Kept as a package-level type so both graph-building paths share the slice
// shape and sort order.
type pendingAlias struct {
	key, namespace, kind, name string
	priority                   int
}

// sortAliasesByPriority sorts pending alias registrations in priority order
// (stable) so that higher-priority node types win alias conflicts.
func sortAliasesByPriority(pending []pendingAlias) {
	sort.SliceStable(pending, func(i, j int) bool {
		return pending[i].priority < pending[j].priority
	})
}

// DependencyGraph represents the service dependency graph
type DependencyGraph struct {
	Nodes        map[string]*ServiceNode // key: "namespace:kind:name"
	Edges        map[string][]string     // key: node -> []downstream nodes
	ReverseEdges map[string][]string     // key: node -> []upstream nodes
	// nodeAliases maps alternative key representations to their canonical graph
	// key. Events and graph use divergent conventions: graph keys are built from
	// the OTel node_type ("Workload", "Service") with ":" separators, while event
	// collectors often write "Deployment" with "/" separators (e.g. k8s_api_server
	// writes "demo/Deployment/flagd" for the same resource the graph stores as
	// "demo:Workload:flagd"). Without this indirection every graph lookup against
	// an event-sourced key silently returns no path, so the correlation engine
	// scores ServiceMap-adjacent events at time-only (0.30) and drops them below
	// the 0.50 threshold.
	nodeAliases map[string]string // alias key → canonical key
}

// resolveKey returns the canonical graph key for a caller-supplied service key.
// If the key is already canonical (present in Nodes) it is returned unchanged.
// If it matches a registered alias, the canonical form is returned. Otherwise
// the input is returned as-is so downstream lookups still fail predictably.
func (g *DependencyGraph) resolveKey(key string) string {
	if key == "" {
		return ""
	}
	if _, ok := g.Nodes[key]; ok {
		return key
	}
	if canonical, ok := g.nodeAliases[key]; ok {
		return canonical
	}
	return key
}

// registerNodeAliases records non-canonical key forms that should resolve to
// `canonical`. For a Workload node "demo:Workload:flagd" it registers the event
// writers' forms "demo:Deployment:flagd", "demo/Workload/flagd",
// "demo/Deployment/flagd", and so on.
//
// Registration is first-wins: when the same alias could point at two different
// canonical nodes (e.g. a namespace contains both a Workload and a K8sService
// named "flagd"), the first registration keeps the alias. Callers should
// therefore register nodes in order of correlation preference (Workload before
// K8sService / Service) so the alias lands on the node that carries CALLS edges.
func (g *DependencyGraph) registerNodeAliases(canonical, namespace, nodeType, name string) {
	if g.nodeAliases == nil {
		g.nodeAliases = make(map[string]string)
	}
	// Event-side kind synonyms. nodeType is the canonical form (from the graph
	// source); the rest are what different event collectors typically write.
	kinds := []string{nodeType, "Deployment", "StatefulSet", "DaemonSet", "Pod", "Service", "Workload"}
	seen := make(map[string]struct{}, len(kinds)*2)
	for _, kind := range kinds {
		if kind == "" {
			continue
		}
		for _, sep := range []string{":", "/"} {
			key := fmt.Sprintf("%s%s%s%s%s", namespace, sep, kind, sep, name)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			if key == canonical {
				continue
			}
			if _, isCanonical := g.Nodes[key]; isCanonical {
				// Don't shadow a real canonical node with an alias.
				continue
			}
			if _, already := g.nodeAliases[key]; already {
				continue
			}
			g.nodeAliases[key] = canonical
		}
	}
}

// parseServiceMapFromEvent extracts and parses service map from event evidences
func parseServiceMapFromEvent(event *models.Event) (*DependencyGraph, error) {
	if event.Evidences == nil {
		return nil, fmt.Errorf("no evidences in event")
	}

	if !event.Evidences.IsArray() {
		return nil, fmt.Errorf("evidences is not an array")
	}

	evidences := event.Evidences.Array()

	// Find service_map evidence (supports multiple formats)
	for _, ev := range evidences {
		evidence, ok := ev.(map[string]interface{})
		if !ok {
			continue
		}
		evidenceType, _ := evidence["type"].(string)
		additionalInfo, _ := evidence["additional_info"].(map[string]interface{})

		// Knowledge graph format (from knowledge_graph_service_map action)
		if evidenceType == "knowledge_graph" {
			if graph := parseKnowledgeGraphEvidence(evidence); graph != nil {
				return graph, nil
			}
			continue
		}

		// Service map format (from traces_dependency_map / cloud_service_map)
		isServiceMap := evidenceType == "service_map"

		// Legacy format: type=json with action_name=service_map_enricher (from k8s agent)
		if !isServiceMap && evidenceType == "json" && additionalInfo != nil {
			actionName, _ := additionalInfo["action_name"].(string)
			isServiceMap = actionName == "service_map_enricher"
		}

		if isServiceMap {
			dataStr, ok := evidence["data"].(string)
			if !ok {
				continue
			}

			var serviceMapData struct {
				Data []ServiceNode `json:"data"`
			}

			if err := json.Unmarshal([]byte(dataStr), &serviceMapData); err != nil {
				return nil, fmt.Errorf("failed to unmarshal service map: %w", err)
			}

			return buildDependencyGraph(serviceMapData.Data), nil
		}
	}

	return nil, fmt.Errorf("service_map evidence not found")
}

// buildDependencyGraph constructs a dependency graph from service nodes
func buildDependencyGraph(nodes []ServiceNode) *DependencyGraph {
	graph := &DependencyGraph{
		Nodes:        make(map[string]*ServiceNode),
		Edges:        make(map[string][]string),
		ReverseEdges: make(map[string][]string),
	}

	// First pass: Add all nodes and collect alias registrations in priority order
	// so higher-priority node types (e.g. Workload) win alias conflicts with
	// lower-priority ones (e.g. K8sService) when the same namespace+name appears
	// under multiple types.
	pending := make([]pendingAlias, 0, len(nodes))
	for i := range nodes {
		node := &nodes[i]
		nodeKey := formatNodeKey(node.ID.Namespace, node.ID.Kind, node.ID.Name)
		graph.Nodes[nodeKey] = node
		pending = append(pending, pendingAlias{
			key:       nodeKey,
			namespace: node.ID.Namespace,
			kind:      node.ID.Kind,
			name:      node.ID.Name,
			priority:  aliasPriorityFor(node.ID.Kind),
		})
	}
	sortAliasesByPriority(pending)
	for _, p := range pending {
		graph.registerNodeAliases(p.key, p.namespace, p.kind, p.name)
	}

	// Second pass: Build edges
	for i := range nodes {
		node := &nodes[i]
		nodeKey := formatNodeKey(node.ID.Namespace, node.ID.Kind, node.ID.Name)

		// Process downstreams
		for _, downstream := range node.Downstreams {
			downstreamKey := extractNodeKeyFromLink(downstream.ID)
			if downstreamKey != "" {
				graph.Edges[nodeKey] = append(graph.Edges[nodeKey], downstreamKey)
				graph.ReverseEdges[downstreamKey] = append(graph.ReverseEdges[downstreamKey], nodeKey)
			}
		}

		// Process upstreams (for reverse edges)
		for _, upstream := range node.Upstreams {
			upstreamKey := extractNodeKeyFromLink(upstream.ID)
			if upstreamKey != "" {
				graph.ReverseEdges[nodeKey] = append(graph.ReverseEdges[nodeKey], upstreamKey)
				graph.Edges[upstreamKey] = append(graph.Edges[upstreamKey], nodeKey)
			}
		}
	}

	return graph
}

// getDependencyDistance calculates the shortest path distance between two services
// Returns -1 if no path exists, 0 if same service, positive number for hops
func (g *DependencyGraph) getDependencyDistance(service1Key, service2Key string) int {
	service1Key = g.resolveKey(service1Key)
	service2Key = g.resolveKey(service2Key)
	if service1Key == service2Key {
		return 0
	}

	// BFS to find shortest path
	queue := []struct {
		node string
		dist int
	}{{service1Key, 0}}

	visited := make(map[string]bool)
	visited[service1Key] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Check downstream neighbors
		for _, neighbor := range g.Edges[current.node] {
			if neighbor == service2Key {
				return current.dist + 1
			}

			if !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, struct {
					node string
					dist int
				}{neighbor, current.dist + 1})
			}
		}

		// Also check upstream neighbors (bidirectional search)
		for _, neighbor := range g.ReverseEdges[current.node] {
			if neighbor == service2Key {
				return current.dist + 1
			}

			if !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, struct {
					node string
					dist int
				}{neighbor, current.dist + 1})
			}
		}
	}

	return -1 // No path found
}

// isUpstream checks if service1 is an upstream dependency of service2
func (g *DependencyGraph) isUpstream(service1Key, service2Key string) bool {
	service1Key = g.resolveKey(service1Key)
	service2Key = g.resolveKey(service2Key)
	if service1Key == service2Key {
		return false
	}

	// Check if service1 is in service2's upstream list
	for _, upstream := range g.ReverseEdges[service2Key] {
		if upstream == service1Key {
			return true
		}
	}

	return false
}

// isDownstream checks if service1 is a downstream dependent of service2
func (g *DependencyGraph) isDownstream(service1Key, service2Key string) bool {
	service1Key = g.resolveKey(service1Key)
	service2Key = g.resolveKey(service2Key)
	if service1Key == service2Key {
		return false
	}

	// Check if service1 is in service2's downstream list
	for _, downstream := range g.Edges[service2Key] {
		if downstream == service1Key {
			return true
		}
	}

	return false
}

// formatNodeKey creates a consistent key format for a service node
func formatNodeKey(namespace, kind, name string) string {
	return fmt.Sprintf("%s:%s:%s", namespace, kind, name)
}

// extractNodeKeyFromLink extracts node key from a service link
func extractNodeKeyFromLink(linkID interface{}) string {
	switch v := linkID.(type) {
	case string:
		// Link is already a string key like "namespace:kind:name"
		return v
	case map[string]interface{}:
		// Link is an object with namespace, kind, name fields
		namespace, _ := v["namespace"].(string)
		kind, _ := v["kind"].(string)
		name, _ := v["name"].(string)
		return formatNodeKey(namespace, kind, name)
	default:
		return ""
	}
}

// parseKnowledgeGraphEvidence builds a DependencyGraph from knowledge_graph evidence.
// KG evidence has nodes (with properties.name, properties.namespace, node_type) and
// edges (with source_node_id, dest_node_id, relationship_type).
func parseKnowledgeGraphEvidence(evidence map[string]interface{}) *DependencyGraph {
	dataRaw := evidence["data"]
	if dataRaw == nil {
		return nil
	}

	dataMap, ok := dataRaw.(map[string]interface{})
	if !ok {
		// Try as JSON string
		dataStr, ok := dataRaw.(string)
		if !ok {
			return nil
		}
		if err := json.Unmarshal([]byte(dataStr), &dataMap); err != nil {
			return nil
		}
	}

	nodesRaw, _ := dataMap["nodes"].([]interface{})
	edgesRaw, _ := dataMap["edges"].([]interface{})

	if len(nodesRaw) == 0 {
		return nil
	}

	graph := &DependencyGraph{
		Nodes:        make(map[string]*ServiceNode),
		Edges:        make(map[string][]string),
		ReverseEdges: make(map[string][]string),
	}

	// Map node IDs to node keys for edge resolution
	nodeIDToKey := make(map[string]string)

	// Collect alias registrations so we can sort by node-type priority before
	// applying them. Workload nodes carry the caller/callee CALLS edges from
	// ebpf and traces, so aliases (e.g. the "demo:Deployment:flagd" form used by
	// event collectors) should resolve to the Workload node rather than a
	// same-named K8sService that has no outbound CALLS edges.
	var pending []pendingAlias

	for _, nodeRaw := range nodesRaw {
		node, ok := nodeRaw.(map[string]interface{})
		if !ok {
			continue
		}

		nodeID, _ := node["id"].(string)
		nodeType, _ := node["node_type"].(string)
		properties, _ := node["properties"].(map[string]interface{})
		if properties == nil {
			continue
		}

		name, _ := properties["name"].(string)
		namespace, _ := properties["namespace"].(string)
		if name == "" {
			continue
		}

		kind := nodeType
		if kind == "" {
			kind = "Service"
		}

		nodeKey := formatNodeKey(namespace, kind, name)
		nodeIDToKey[nodeID] = nodeKey

		graph.Nodes[nodeKey] = &ServiceNode{
			ID: struct {
				Namespace string `json:"namespace"`
				Kind      string `json:"kind"`
				Name      string `json:"name"`
			}{
				Namespace: namespace,
				Kind:      kind,
				Name:      name,
			},
		}

		pending = append(pending, pendingAlias{
			key:       nodeKey,
			namespace: namespace,
			kind:      kind,
			name:      name,
			priority:  aliasPriorityFor(kind),
		})
	}

	// Register aliases in priority order so Workload nodes win when the same
	// namespace/name appears as both a Workload and a Service / K8sService.
	sortAliasesByPriority(pending)
	for _, p := range pending {
		graph.registerNodeAliases(p.key, p.namespace, p.kind, p.name)
	}

	// Build edges from KG edges (only CALLS relationships)
	for _, edgeRaw := range edgesRaw {
		edge, ok := edgeRaw.(map[string]interface{})
		if !ok {
			continue
		}

		relType, _ := edge["relationship_type"].(string)
		if relType != "CALLS" {
			continue
		}

		sourceID, _ := edge["source_node_id"].(string)
		destID, _ := edge["dest_node_id"].(string)

		sourceKey := nodeIDToKey[sourceID]
		destKey := nodeIDToKey[destID]

		if sourceKey == "" || destKey == "" {
			continue
		}

		// source CALLS dest → source is upstream of dest
		graph.Edges[sourceKey] = append(graph.Edges[sourceKey], destKey)
		graph.ReverseEdges[destKey] = append(graph.ReverseEdges[destKey], sourceKey)
	}

	return graph
}

// getServiceKeyFromEvent returns the service key for correlation matching.
// It prefers the pre-computed service_key from the DB (populated by collectors for
// K8s workloads, AWS ARNs, GCP resource paths, Azure resource IDs, etc.).
// Falls back to constructing a key from K8s subject fields when service_key is empty.
//
// All non-nil checks on optional subject fields are paired with an empty-string check:
// sqlx scans an empty TEXT column into a non-nil *string pointing at "", so a bare
// `!= nil` check would accept the empty string and either overwrite a default
// ("Deployment" → "") or short-circuit the SubjectOwner→SubjectName fallback to
// return "". In production this happened on every pagerduty webhook (subject_owner
// and subject_owner_kind both empty strings), blocking ServiceMap correlation for
// pagerduty-sourced events.
func getServiceKeyFromEvent(event *models.Event) string {
	// Use the DB service_key if available — this is the general-purpose resource identifier
	// set by collectors across all sources (K8s, AWS, GCP, Azure, CloudFoundry, etc.)
	if event.ServiceKey != nil && *event.ServiceKey != "" {
		return *event.ServiceKey
	}

	// Fallback: construct from K8s subject fields (for sources that don't set service_key)
	namespace := ""
	if event.SubjectNamespace != nil && *event.SubjectNamespace != "" {
		namespace = *event.SubjectNamespace
	}

	kind := "Deployment" // Default
	if event.SubjectOwnerKind != nil && *event.SubjectOwnerKind != "" {
		kind = *event.SubjectOwnerKind
	}

	name := ""
	if event.SubjectOwner != nil && *event.SubjectOwner != "" {
		name = *event.SubjectOwner
	} else if event.SubjectName != nil && *event.SubjectName != "" {
		name = *event.SubjectName
	}

	if name == "" {
		return ""
	}

	return formatNodeKey(namespace, kind, name)
}
