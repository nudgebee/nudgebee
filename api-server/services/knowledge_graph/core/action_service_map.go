package core

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"runtime/debug"
	"strings"
	"time"

	"github.com/lib/pq"
)

func init() {
	playbooks.RegisterAction("knowledge_graph_service_map", &knowledgeGraphServiceMapAction{})
}

type knowledgeGraphServiceMapAction struct{}

// KnowledgeGraphServiceMapResponse holds the KG neighborhood data for a service.
type KnowledgeGraphServiceMapResponse struct {
	Nodes          []KgEvidenceNode `json:"nodes"`
	Edges          []KgEdge         `json:"edges"`
	TargetService  string           `json:"target_service"`
	Namespace      string           `json:"namespace"`
	additionalInfo map[string]any
	insights       []playbooks.PlaybookActionResponseInsight
}

func (r *KnowledgeGraphServiceMapResponse) GetFormatName() string {
	return "knowledge_graph"
}

func (r *KnowledgeGraphServiceMapResponse) GetData() any {
	return map[string]any{
		"nodes":          r.Nodes,
		"edges":          r.Edges,
		"target_service": r.TargetService,
		"namespace":      r.Namespace,
	}
}

func (r *KnowledgeGraphServiceMapResponse) GetAdditionalInfo() map[string]any {
	return r.additionalInfo
}

func (r *KnowledgeGraphServiceMapResponse) GetInsights() []playbooks.PlaybookActionResponseInsight {
	return r.insights
}

// ExtractLabels exposes upstream/downstream service data for subsequent actions.
func (r *KnowledgeGraphServiceMapResponse) ExtractLabels() map[string]any {
	if r.additionalInfo == nil {
		return map[string]any{}
	}
	return map[string]any{
		"upstream_services":   r.additionalInfo["upstream_services"],
		"downstream_services": r.additionalInfo["downstream_services"],
		"target_service":      r.TargetService,
		"kg_node_types":       r.additionalInfo["kg_node_types"],
	}
}

func (a *knowledgeGraphServiceMapAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	ev := ctx.GetEvent()
	return ev.SubjectNamespace != "" && (ev.SubjectOwner != "" || ev.SubjectName != "")
}

func (a *knowledgeGraphServiceMapAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	ev := ctx.GetEvent()
	// Prefer SubjectOwner (workload name like "accounting") over SubjectName (pod name like "accounting-5cf6fc4b7f-wlwxm")
	serviceName := ev.SubjectOwner
	if serviceName == "" {
		serviceName = ev.SubjectName
	}
	return a.Execute(ctx, map[string]any{
		"service_name": serviceName,
		"namespace":    ev.SubjectNamespace,
	})
}

func (a *knowledgeGraphServiceMapAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	serviceName, _ := rawParams["service_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if serviceName == "" {
		return nil, fmt.Errorf("service_name is required")
	}

	logger := ctx.GetLogger()
	reqCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), logger, nil, nil)

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("knowledge_graph_service_map: failed to get db manager: %w", err)
	}

	kgService := NewService(reqCtx, logger, dbManager)

	// Find service nodes matching name + namespace, scoped to account first
	nodeIDs, err := findServiceNodes(dbManager, ctx.GetTenantId(), ctx.GetAccountId(), serviceName, namespace)
	if err != nil || len(nodeIDs) == 0 {
		// Fall back to tenant-wide search
		nodeIDs, err = findServiceNodes(dbManager, ctx.GetTenantId(), "", serviceName, namespace)
	}
	if err != nil || len(nodeIDs) == 0 {
		// Last resort: drop the namespace. A cloud event carries the provider's
		// service code there (AmazonRDS, AWSELB) and cloud nodes carry no
		// namespace, so the filtered attempts above could never match one. Only
		// reached when the namespaced lookups found nothing, so a Kubernetes
		// subject that does exist is still matched with its namespace first.
		nodeIDs, err = findServiceNodes(dbManager, ctx.GetTenantId(), ctx.GetAccountId(), serviceName, "")
	}
	if err != nil || len(nodeIDs) == 0 {
		// A load-balancer alarm identifies its subject by the CloudWatch
		// dimension ("app/my-lb/1a2b3c"); the node is named "my-lb". Recover the
		// name and retry. Returns "" for anything that is not such a dimension,
		// so this is a no-op for every other subject.
		if lbName := ELBV2LoadBalancerName(serviceName); lbName != "" {
			nodeIDs, err = findServiceNodes(dbManager, ctx.GetTenantId(), ctx.GetAccountId(), lbName, "")
		}
		if err != nil || len(nodeIDs) == 0 {
			// Same mismatch as the load balancer above, for every other cloud
			// resource: the alarm names its subject by the provider id
			// ("i-0dcee3621b8456783") while the node is named from its Name tag
			// ("orders-api"). The id is not in query_attributes, so the name
			// lookups above cannot match it, and an EC2 alarm produced no
			// knowledge_graph evidence at all - which silently removed it from
			// correlation, because correlation walks that evidence and had
			// nothing to walk.
			nodeIDs, err = findServiceNodesByResourceID(dbManager, ctx.GetTenantId(), ctx.GetAccountId(), serviceName)
		}
		if err != nil || len(nodeIDs) == 0 {
			logger.Info("knowledge_graph_service_map: no matching service nodes found",
				"service", serviceName, "namespace", namespace)
			// Returning no evidence here removes the event from correlation and
			// from analysis, and nothing downstream can tell that happened - the
			// alarm, the event and the graph all look fine. Two days of this
			// looked like "correlation is broken on AWS" when the truth was
			// "one lookup could not name a resource". Record the near-miss so
			// an unresolvable subject is reviewable instead of invisible.
			//
			// Off the caller's path entirely. Recording is best-effort, this
			// branch runs on every unresolvable event, and a slow metastore
			// would otherwise add its timeout to event processing at exactly
			// the moment things are already going wrong. Request-scoped values
			// are read here, not inside the goroutine, so a pooled or reused
			// context cannot race with it.
			tenantID := ctx.GetTenantId()
			accountID := ctx.GetAccountId()
			// A panic in a goroutine takes the whole process down — it is not
			// caught by the HTTP layer's recovery, which only wraps the
			// request's own stack. This one runs on every unresolvable event,
			// so an input that panics the recorder would not be a single failed
			// request but a crash loop on the busiest path.
			//
			// The logger is resolved here rather than inside: ctx is
			// request-scoped, and reading it after the request has returned is
			// the kind of thing that would itself panic in the handler meant to
			// report a panic.
			recLogger := logger
			if recLogger == nil {
				recLogger = slog.Default()
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						recLogger.Error("panic recording uncertain classification",
							"recover", r, "stack", string(debug.Stack()))
					}
				}()
				recCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				RecordUncertainClassification(recCtx, dbManager, UncertainClassificationCandidate{
					TenantID:           tenantID,
					Source:             "knowledge_graph_service_map",
					ClassificationKind: "node_match",
					CandidateName:      serviceName,
					CandidateNamespace: namespace,
					ReasonCode:         "no_node_for_event_subject",
					ReasonDescription: "event subject matched no graph node by name, " +
						"namespace-less name, load-balancer dimension, resource_id or arn",
					Evidence: map[string]interface{}{
						"account_id": accountID,
						"subject":    serviceName,
						"namespace":  namespace,
					},
				})
			}()
			return nil, nil
		}
	}

	// Get 1-level neighborhood (direct dependencies only) to keep the graph
	// focused. Storage is included so collapsed CALLS edges pointing at
	// Storage nodes (S3, Cloud Storage buckets, etc.) surface in the
	// neighborhood after core.CollapseEnrichedExternalServices runs.
	//
	// ComputeInstance, LoadBalancer and ServerlessFunction are here because on a
	// VM or serverless deployment they ARE the application — there is no Workload
	// or Pod layer above them. Without them the neighbourhood of an AWS resource
	// came back with the resource alone and no edges, even when the graph held
	// `api instance --CALLS--> database`, because the only node that could sit on
	// the other end of that edge was filtered out. An empty neighbourhood also
	// starves event correlation, which walks this evidence: with no edges to
	// traverse, dependency_distance is always 0 and no cross-tier correlation can
	// ever be produced.
	//
	// Plumbing (VPC, Subnet, SecurityGroup) stays out on purpose: an AWS resource
	// is attached to several of them, and they would crowd out the services an
	// operator is looking for while adding no dependency information.
	graph, err := kgService.GetMultipleNodeNeighbors(reqCtx, nodeIDs, 1, serviceMapNeighbourTypes, true)
	if err != nil {
		logger.Warn("knowledge_graph_service_map: failed to get neighbors", "error", err)
		return nil, nil
	}

	if len(graph.Nodes) == 0 {
		logger.Info("knowledge_graph_service_map: no neighbor nodes found", "service", serviceName)
		return nil, nil
	}

	// Build insights and extract upstream/downstream services
	upstream, downstream, nodeTypes := extractTopology(graph, nodeIDs, serviceName)
	insights := buildKGInsights(graph, serviceName, upstream, downstream)

	additionalInfo := map[string]any{
		"action_name":         "knowledge_graph_service_map",
		"title":               fmt.Sprintf("Service Map for %s", serviceName),
		"service_name":        serviceName,
		"namespace":           namespace,
		"upstream_services":   upstream,
		"downstream_services": downstream,
		"kg_node_types":       nodeTypes,
	}

	return &KnowledgeGraphServiceMapResponse{
		Nodes:          ToEvidenceNodes(graph.Nodes),
		Edges:          graph.Edges,
		TargetService:  serviceName,
		Namespace:      namespace,
		additionalInfo: additionalInfo,
		insights:       insights,
	}, nil
}

// KgEvidenceNode is the projection of KgNode carried in the
// knowledge_graph_service_map evidence block. A full KgNode is a copy of the
// stored graph row — every property a source wrote, including the k8s
// annotations map, which alone accounted for ~31% of one measured block
// (22,539 of 72,671 bytes) via kubectl's last-applied-configuration.
//
// Two consumers read this block. The investigate UI renders id / node_type /
// properties.name / properties.namespace (unique_key as the name fallback).
// triage's dependency parser needs that set plus the edges — and, for cloud
// nodes, properties.resource_id / properties.arn, which are how it matches an
// event to the node the event is about. Projecting here keeps the evidence to
// what is read instead of filtering noisy keys one at a time; the cost of
// getting the allowlist wrong is that a consumer degrades silently, so add to
// evidenceNodeProperties rather than trimming it on size grounds alone.
type KgEvidenceNode struct {
	ID           string         `json:"id"`
	NodeType     NodeType       `json:"node_type"`
	SpecificType string         `json:"specific_type,omitempty"`
	UniqueKey    string         `json:"unique_key"`
	Properties   map[string]any `json:"properties"`
	// LogoID is the icon identifier the frontend renders for this node, resolved
	// here rather than in the UI so evidence nodes carry the same logo the
	// knowledge-graph view already gets from KgNode.LogoID.
	LogoID string `json:"logo_id,omitempty"`
}

// evidenceNodeProperties is the property allowlist for KgEvidenceNode: what
// the node is (name, namespace, cluster, kind, engine, role, region), how to
// identify it (resource_id, arn) and whether it is healthy (phase, status,
// state, ready). The rest — annotations, container images, resource
// requests/limits, timestamps — is reachable from the KG APIs when an
// investigation actually needs it.
//
// resource_id and arn are here because the card has a second consumer besides
// the UI. Correlation parses it to build its dependency graph, and it joins an
// event to a node by the event's subject. A cloud event names its subject by
// provider id (i-0dcee3621b8456783); a node is named from its Name tag
// (nudgebee-scenario-services-order). Without an identifier on the node there
// is nothing to join on, so correlation saw the right topology and still scored
// every pair at distance 0.
//
// Excluding ARNs was a deliberate choice when this card was only read by
// humans, for whom they are noise. It is two strings per node, and dropping
// them silently disables cross-resource correlation for every cloud provider —
// which is not a trade the size saving is worth.
var evidenceNodeProperties = []string{
	"name", "namespace", "cluster",
	"kind", "engine", "role", "region",
	"resource_id", "arn",
	"phase", "status", "state", "ready",
}

// ToEvidenceNodes projects each node onto KgEvidenceNode. Absent and nil
// properties are skipped rather than emitted as nulls, so a node contributes
// only the keys its source actually populated.
func ToEvidenceNodes(nodes []KgNode) []KgEvidenceNode {
	evidenceNodes := make([]KgEvidenceNode, 0, len(nodes))
	for i := range nodes {
		properties := make(map[string]any, len(evidenceNodeProperties))
		for _, key := range evidenceNodeProperties {
			if value, present := nodes[i].Properties[key]; present && value != nil {
				properties[key] = value
			}
		}
		evidenceNodes = append(evidenceNodes, KgEvidenceNode{
			ID:           nodes[i].ID,
			NodeType:     nodes[i].NodeType,
			SpecificType: nodes[i].SpecificType,
			UniqueKey:    nodes[i].UniqueKey,
			Properties:   properties,
			// Computed from the full property map, not the trimmed allowlist above:
			// ComputeLogoID reads engine/kind/service_name, which the allowlist drops.
			LogoID: ComputeLogoID(nodes[i].NodeType, nodes[i].SpecificType, nodes[i].Source, nodes[i].Properties),
		})
	}
	return evidenceNodes
}

// seedNodeTypeNames is the SQL-facing form of ImpactSeedNodeTypes, built once
// because the set is static and findServiceNodes runs per event.
var seedNodeTypeNames = func() []string {
	seedTypes := ImpactSeedNodeTypes()
	names := make([]string, 0, len(seedTypes))
	for _, nodeType := range seedTypes {
		names = append(names, string(nodeType))
	}
	return names
}()

// serviceMapNeighbourTypes are the node types worth showing around an alerting
// resource: things that carry or serve traffic, not the infrastructure a
// resource merely sits in.
var serviceMapNeighbourTypes = []NodeType{
	NodeTypeService, NodeTypeExternalService, NodeTypeDatabase,
	NodeTypeMessageQueue, NodeTypeCache, NodeTypeStorage,
	NodeTypeWorkload, NodeTypeK8sService,
	NodeTypeComputeInstance, NodeTypeLoadBalancer, NodeTypeServerlessFunction,
}

// findServiceNodes queries the KG for nodes matching a service name and namespace.
//
// The node types accepted are those the graph defines a blast-radius traversal
// for (ImpactSeedNodeTypes) rather than a fixed list, so a cloud resource — a
// Database, ComputeInstance or LoadBalancer — resolves as readily as a Workload.
// Restricting to that set still excludes plumbing (SecurityGroup, Subnet, VPC),
// which shares names with real resources and would otherwise match first.
//
// namespace is only applied when the caller supplies one, and cloud events
// should not: they carry the provider's service code there (AmazonRDS, AWSELB)
// while cloud nodes carry no namespace at all, so filtering on it can only ever
// fail. Kubernetes callers must keep passing it — a workload name is unique only
// within its namespace.
// findServiceNodesByResourceID resolves a node by the provider's own identifier
// (instance id, ARN) rather than its display name. Cloud alarms identify their
// subject by id, while nodes are named from the Name tag, so the name-based
// lookups miss every resource whose tag differs from its id - which is most of
// them. resource_id is not indexed into query_attributes, so this reads
// properties directly; it runs only after the name lookups have failed.
func findServiceNodesByResourceID(dbManager *database.DatabaseManager, tenantID, accountID, resourceID string) ([]string, error) {
	return findNodesByProviderIdentifierInTable(dbManager, "knowledge_graph_node", tenantID, accountID, resourceID)
}

// ResolveNodeByProviderIdentifier resolves the single graph node a cloud event's
// subject names, when that subject is a provider identifier (instance id, ARN)
// rather than the display name the node carries.
//
// This exists because the same mismatch has now been fixed three times in three
// layers - the evidence lookup, the evidence property projection, and the blast
// radius seed - each time by a different one-off. An event says
// i-0dcee3621b8456783; the node is named nudgebee-scenario-services-order from
// its Name tag. Any lookup that matches on name alone silently resolves nothing,
// and every caller degrades quietly rather than erroring: no evidence, no
// correlation, "no service map for this one".
//
// Callers that resolve a subject to a node should use this after their
// name-based attempts fail, so a fourth layer cannot reintroduce the same gap.
//
// Exactly one match counts. Two nodes sharing an identifier means we cannot say
// which resource the event is about, and guessing seeds a blast radius on the
// wrong one - the same rule the name-based path applies.
func (s *Service) ResolveNodeByProviderIdentifier(tenantID, accountID, identifier string) (string, bool) {
	ids, err := findServiceNodesByResourceID(s.dbManager, tenantID, accountID, identifier)
	if err != nil {
		s.logger.Warn("resolve node by provider identifier failed",
			"identifier", identifier, "error", err)
		return "", false
	}
	if len(ids) != 1 {
		return "", false
	}
	return ids[0], true
}

// findNodesByProviderIdentifierInTable is the SQL, with the table as a parameter
// so a DB test can run it against a throwaway table instead of needing the real
// graph and its foreign keys (the convention pr_lifecycle_*_test.go established).
// The table name is never caller-supplied at runtime - production passes a
// constant.
func findNodesByProviderIdentifierInTable(dbManager *database.DatabaseManager, table, tenantID, accountID, resourceID string) ([]string, error) {
	if resourceID == "" {
		return nil, nil
	}
	query := `
		SELECT id FROM ` + table + `
		WHERE tenant_id = $1
		  AND (properties->>'resource_id' = $2 OR properties->>'arn' = $2)
		  AND node_type = ANY($3)
		  AND level = 'Tenant'
		  AND is_active = true
	`
	args := []interface{}{tenantID, resourceID, pq.Array(seedNodeTypeNames)}
	if accountID != "" {
		query += " AND cloud_account_id = $4"
		args = append(args, accountID)
	}
	query += " LIMIT 5"

	rows, err := dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query service nodes by resource id: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("failed to close rows", "error", closeErr)
		}
	}()

	var nodeIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan node id: %w", err)
		}
		nodeIDs = append(nodeIDs, id)
	}
	return nodeIDs, rows.Err()
}

func findServiceNodes(dbManager *database.DatabaseManager, tenantID, accountID, name, namespace string) ([]string, error) {
	query := `
		SELECT id FROM knowledge_graph_node
		WHERE tenant_id = $1
		  AND query_attributes->>'name' = $2
		  AND node_type = ANY($3)
		  AND level = 'Tenant'
		  AND is_active = true
	`
	args := []interface{}{tenantID, name, pq.Array(seedNodeTypeNames)}
	argIdx := 4

	if namespace != "" {
		query += fmt.Sprintf(" AND query_attributes->>'namespace' = $%d", argIdx)
		args = append(args, namespace)
		argIdx++
	}

	if accountID != "" {
		query += fmt.Sprintf(" AND cloud_account_id = $%d", argIdx)
		args = append(args, accountID)
	}

	query += " LIMIT 5"

	rows, err := dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query service nodes: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("failed to close rows", "error", closeErr)
		}
	}()

	var nodeIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan node id: %w", err)
		}
		nodeIDs = append(nodeIDs, id)
	}
	return nodeIDs, rows.Err()
}

// extractTopology identifies upstream/downstream services and unique node types.
func extractTopology(graph KnowledgeGraph, targetNodeIDs []string, targetName string) (upstream, downstream, nodeTypes []string) {
	targetSet := make(map[string]bool, len(targetNodeIDs))
	for _, id := range targetNodeIDs {
		targetSet[id] = true
	}

	nodeNameByID := make(map[string]string, len(graph.Nodes))
	nodeTypeSet := make(map[string]bool)
	for _, node := range graph.Nodes {
		name, _ := node.Properties["name"].(string)
		nodeNameByID[node.ID] = name
		nodeTypeSet[string(node.NodeType)] = true
	}

	upstreamSet := make(map[string]bool)
	downstreamSet := make(map[string]bool)

	for _, edge := range graph.Edges {
		if edge.RelationshipType != RelationshipCalls {
			continue
		}
		// edge: source CALLS destination
		if targetSet[edge.DestinationNodeID] {
			// Something calls the target → upstream
			if name := nodeNameByID[edge.SourceNodeID]; name != "" && name != targetName {
				upstreamSet[name] = true
			}
		}
		if targetSet[edge.SourceNodeID] {
			// Target calls something → downstream
			if name := nodeNameByID[edge.DestinationNodeID]; name != "" && name != targetName {
				downstreamSet[name] = true
			}
		}
	}

	for name := range upstreamSet {
		upstream = append(upstream, name)
	}
	for name := range downstreamSet {
		downstream = append(downstream, name)
	}
	for nt := range nodeTypeSet {
		nodeTypes = append(nodeTypes, nt)
	}
	return
}

// buildKGInsights generates insights from the KG neighborhood.
func buildKGInsights(graph KnowledgeGraph, serviceName string, upstream, downstream []string) []playbooks.PlaybookActionResponseInsight {
	var insights []playbooks.PlaybookActionResponseInsight

	if len(upstream) > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("%d upstream services call %s: %s", len(upstream), serviceName, strings.Join(upstream, ", ")),
			Severity: "info",
		})
	}

	if len(downstream) > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("%s depends on %d downstream services: %s", serviceName, len(downstream), strings.Join(downstream, ", ")),
			Severity: "info",
		})
	}

	// Check for external services, databases, and other cloud-resource
	// dependencies. After core.CollapseEnrichedExternalServices runs, CALLS
	// edges may land directly on Cache / MessageQueue / Storage nodes that
	// were previously hidden behind an ExternalService hop — surface those
	// too so the insight count doesn't shrink for tenants on the new code.
	var externalServices, databases, caches, queues, storage []string
	for _, node := range graph.Nodes {
		name, _ := node.Properties["name"].(string)
		if name == "" {
			continue
		}
		switch node.NodeType {
		case NodeTypeExternalService:
			externalServices = append(externalServices, name)
		case NodeTypeDatabase:
			databases = append(databases, name)
		case NodeTypeCache:
			caches = append(caches, name)
		case NodeTypeMessageQueue:
			queues = append(queues, name)
		case NodeTypeStorage:
			storage = append(storage, name)
		}
	}

	if len(externalServices) > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Connected to %d external services: %s", len(externalServices), strings.Join(externalServices, ", ")),
			Severity: "info",
		})
	}

	if len(databases) > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Uses %d databases: %s", len(databases), strings.Join(databases, ", ")),
			Severity: "info",
		})
	}

	if len(caches) > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Uses %d caches: %s", len(caches), strings.Join(caches, ", ")),
			Severity: "info",
		})
	}

	if len(queues) > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Uses %d message queues: %s", len(queues), strings.Join(queues, ", ")),
			Severity: "info",
		})
	}

	if len(storage) > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Uses %d storage resources: %s", len(storage), strings.Join(storage, ", ")),
			Severity: "info",
		})
	}

	return insights
}
