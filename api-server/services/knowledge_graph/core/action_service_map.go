package core

import (
	"fmt"
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
)

func init() {
	playbooks.RegisterAction("knowledge_graph_service_map", &knowledgeGraphServiceMapAction{})
}

type knowledgeGraphServiceMapAction struct{}

// KnowledgeGraphServiceMapResponse holds the KG neighborhood data for a service.
type KnowledgeGraphServiceMapResponse struct {
	Nodes          []KgNode `json:"nodes"`
	Edges          []KgEdge `json:"edges"`
	TargetService  string   `json:"target_service"`
	Namespace      string   `json:"namespace"`
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
		if err != nil || len(nodeIDs) == 0 {
			logger.Info("knowledge_graph_service_map: no matching service nodes found",
				"service", serviceName, "namespace", namespace)
			return nil, nil
		}
	}

	// Get 1-level neighborhood (direct dependencies only) to keep the graph focused
	serviceNodeTypes := []NodeType{
		NodeTypeService, NodeTypeExternalService, NodeTypeDatabase,
		NodeTypeMessageQueue, NodeTypeCache, NodeTypeWorkload, NodeTypeK8sService,
	}
	graph, err := kgService.GetMultipleNodeNeighbors(reqCtx, nodeIDs, 1, serviceNodeTypes, true)
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
		Nodes:          graph.Nodes,
		Edges:          graph.Edges,
		TargetService:  serviceName,
		Namespace:      namespace,
		additionalInfo: additionalInfo,
		insights:       insights,
	}, nil
}

// findServiceNodes queries the KG for nodes matching a service name and namespace.
func findServiceNodes(dbManager *database.DatabaseManager, tenantID, accountID, name, namespace string) ([]string, error) {
	query := `
		SELECT id FROM knowledge_graph_node
		WHERE tenant_id = $1
		  AND query_attributes->>'name' = $2
		  AND node_type IN ('Service', 'Workload', 'K8sService')
		  AND level = 'Tenant'
		  AND is_active = true
	`
	args := []interface{}{tenantID, name}
	argIdx := 3

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

	// Check for external services and databases
	var externalServices, databases []string
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

	return insights
}
