package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
)

// k8sIngressSchema is the concrete-property schema for KubernetesIngress.
// Names are the node.Properties keys written by createIngressNode; the ontology
// reads namespace/cluster/host (host is the first rule's host, pinned
// as a scalar query_attribute).
var k8sIngressSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesIngress",
	NodeType:     core.NodeTypeIngress,
	Properties: []core.PropertyDef{
		{Name: "namespace", Indexed: true},
		{Name: "cluster", Indexed: true},
		{Name: "ingress_class", Indexed: true},
		{Name: "host", Indexed: true},
		{Name: "path", Indexed: true},
		{Name: "hosts"},
		{Name: "paths"},
		{Name: "annotations"},
		{Name: "lb_hostname"},
		{Name: "dns_name"},
		{Name: "lb_ip"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "creation_timestamp"},
		{Name: "deletion_timestamp"},
		{Name: "ingress_class_name"},
		{Name: "rules"},
		{Name: "default_backend"},
		{Name: "host_names"},
		{Name: "load_balancer_dns_names"},
		{Name: "ingress_group_name"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sIngressSchema) }

// resolveIngressBackendServices queries Kubernetes Ingress resources and creates Service nodes for backend services.
// This resolves: LoadBalancer/Ingress Controller → Ingress Resource → Backend Services
// The query is executed once per unique account_id to optimize performance.
func (s *K8sSource) resolveIngressBackendServices(
	ctx context.Context,
	reqCtx *security.RequestContext,
	ingressControllerNodes []*core.DbNode,
	serviceNodes []*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge) {
	if len(ingressControllerNodes) == 0 {
		return nil, nil
	}

	// Group ingress controller nodes by account_id
	nodesByAccount := make(map[string][]*core.DbNode)
	for _, node := range ingressControllerNodes {
		accountID := node.CloudAccountID
		if accountID == "" {
			continue
		}
		nodesByAccount[accountID] = append(nodesByAccount[accountID], node)
	}

	var allNodes []*core.DbNode
	var allEdges []*core.DbEdge

	// Execute query once per account_id
	for accountID, controllerNodes := range nodesByAccount {
		nodes, edges, err := s.resolveIngressBackendsForAccount(ctx, accountID, req.TenantID, controllerNodes, serviceNodes, req)
		if err != nil {
			s.logger.Warn("Failed to resolve ingress backends for account",
				"account_id", accountID,
				"error", err)
			continue
		}
		allNodes = append(allNodes, nodes...)
		allEdges = append(allEdges, edges...)
	}

	return allNodes, allEdges
}

// resolveIngressBackendsForAccount fetches ingress resources for a single account and creates backend service nodes
func (s *K8sSource) resolveIngressBackendsForAccount(
	ctx context.Context,
	k8sAccountID string,
	tenantID string,
	controllerNodes []*core.DbNode,
	serviceNodes []*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	ingressData, err := s.fetchIngressResources(k8sAccountID)
	if err != nil {
		return nil, nil, err
	}
	if ingressData == nil {
		return nil, nil, nil
	}

	controllerMap := s.buildIngressControllerMap(controllerNodes)
	processor := s.newIngressBackendProcessor(k8sAccountID, tenantID, controllerMap, serviceNodes, req)
	nodes, edges := processor.processIngressList(ingressData)

	s.logger.Info("Resolved ingress backend services",
		"account_id", k8sAccountID,
		"backend_services", len(nodes),
		"edges", len(edges))

	return nodes, edges, nil
}

// fetchIngressResources executes kubectl to get ingress resources for an account
func (s *K8sSource) fetchIngressResources(k8sAccountID string) (*k8sIngressList, error) {
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  k8sAccountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": "kubectl get ingress --all-namespaces -o json",
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to execute kubectl command: %w", err)
	}

	stdout, err := s.extractIngressRelayStdout(relayResponse)
	if err != nil {
		return nil, err
	}
	if stdout == "" {
		s.logger.Debug("No Ingress resources found", "account_id", k8sAccountID)
		return nil, nil
	}

	var result k8sIngressList
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return nil, fmt.Errorf("failed to parse ingress list: %w", err)
	}
	return &result, nil
}

// extractIngressRelayStdout extracts stdout from relay response
func (s *K8sSource) extractIngressRelayStdout(relayResponse map[string]any) (string, error) {
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected data format in relay response: %T", relayResponse["data"])
	}
	var relayData struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return "", fmt.Errorf("failed to unmarshal relay data: %w", err)
	}
	return relayData.Stdout, nil
}

// buildIngressControllerMap creates a map of controllers by ingress class
func (s *K8sSource) buildIngressControllerMap(controllerNodes []*core.DbNode) *ingressControllerMap {
	cm := &ingressControllerMap{byClass: make(map[string]*core.DbNode)}
	for _, controller := range controllerNodes {
		if ingressClass, ok := controller.Properties["ingress_class"].(string); ok && ingressClass != "" {
			cm.byClass[ingressClass] = controller
		}
		if cm.defaultController == nil {
			cm.defaultController = controller
		}
	}
	return cm
}

func (cm *ingressControllerMap) getController(ingressClassName string) *core.DbNode {
	if ingressClassName != "" {
		if controller, ok := cm.byClass[ingressClassName]; ok {
			return controller
		}
	}
	return cm.defaultController
}

func (s *K8sSource) newIngressBackendProcessor(accountID, tenantID string, cm *ingressControllerMap, serviceNodes []*core.DbNode, req *core.SourceBuildRequest) *ingressBackendProcessor {
	// Build lookup map for existing K8s services by namespace:name
	existingServices := make(map[string]*core.DbNode)
	for _, node := range serviceNodes {
		if node.NodeType != core.NodeTypeK8sService {
			continue
		}
		namespace, _ := node.Properties["namespace"].(string)
		name, _ := node.Properties["name"].(string)
		if namespace != "" && name != "" {
			key := fmt.Sprintf("%s:%s", namespace, name)
			existingServices[key] = node
		}
	}

	return &ingressBackendProcessor{
		source:           s,
		k8sAccountID:     accountID,
		tenantID:         tenantID,
		controllerMap:    cm,
		uniqueBackends:   make(map[string]*core.DbNode),
		existingServices: existingServices,
		req:              req,
	}
}

func (p *ingressBackendProcessor) processIngressList(data *k8sIngressList) ([]*core.DbNode, []*core.DbEdge) {
	for i := range data.Items {
		p.processIngress(&data.Items[i])
	}
	return p.nodes, p.edges
}

func (p *ingressBackendProcessor) processIngress(ingress *k8sIngressResource) {
	controller := p.controllerMap.getController(ingress.Spec.IngressClassName)
	if controller == nil {
		return
	}
	environment, _ := core.GetNodePropertyString(controller, "environment")
	cluster, _ := core.GetNodePropertyString(controller, "cluster")

	// Emit the Ingress node itself + the controller→Ingress edge. Without
	// this the LoadBalancer→Controller→Ingress→K8sService→Workload chain
	// collapses to Controller→backendService and the Ingress resource has
	// no representation in the KG.
	ingressNode := p.createIngressNode(ingress, cluster, environment)
	p.nodes = append(p.nodes, ingressNode)
	p.edges = append(p.edges, p.createEdge(controller.ID, ingressNode.ID,
		map[string]interface{}{
			"connection_type": "ingress_resource",
			"ingress_class":   ingress.Spec.IngressClassName,
		}))

	for i := range ingress.Spec.Rules {
		rule := &ingress.Spec.Rules[i]
		for j := range rule.HTTP.Paths {
			p.processBackendPath(ingress, ingressNode, rule, &rule.HTTP.Paths[j], environment)
		}
	}
}

func (p *ingressBackendProcessor) processBackendPath(
	ingress *k8sIngressResource,
	ingressNode *core.DbNode,
	rule *k8sIngressRule,
	path *k8sIngressPath,
	environment string,
) {
	serviceName := path.Backend.Service.Name
	namespace := ingress.Metadata.Namespace
	if serviceName == "" {
		return
	}

	backendKey := fmt.Sprintf("%s:%s", namespace, serviceName)
	edgeProps := map[string]interface{}{
		"discovered_from": "ingress_resource",
		"ingress_name":    ingress.Metadata.Name,
		"ingress_host":    rule.Host,
		"ingress_path":    path.Path,
		"backend_port":    path.Backend.Service.Port.Number,
	}

	// Resolve backend node: prefer an existing K8sService, otherwise create
	// the synthesized backend Service (cross-namespace ref, or a service
	// filtered out of the relay snapshot). The cache key is processor-scoped
	// so we don't create the same synthesized node twice across multiple
	// Ingresses that share a backend.
	dst, isCached := p.uniqueBackends[backendKey]
	if !isCached {
		if existingService, exists := p.existingServices[backendKey]; exists {
			dst = existingService
		} else {
			dst = p.createBackendNode(serviceName, namespace, environment, ingress, rule, path)
			p.nodes = append(p.nodes, dst)
		}
		p.uniqueBackends[backendKey] = dst
	}

	// Emit one Ingress→K8sService edge per path. Don't skip when the
	// destination is cached — different Ingresses pointing at the same
	// backend each need their own edge (different host/path metadata).
	p.edges = append(p.edges, p.createEdgeWithRel(
		ingressNode.ID, dst.ID, core.RelationshipRoutesToService, edgeProps))

	p.source.logger.Debug("Linked Ingress to backend service",
		"ingress", ingress.Metadata.Name,
		"namespace", namespace,
		"backend_service", serviceName,
		"host", rule.Host,
		"path", path.Path,
		"backend_existed", isCached,
		"account_id", p.k8sAccountID)
}

// createIngressNode builds an Ingress KG node from a fetched K8s Ingress
// resource. One node per Ingress (not per rule/path) — the host/path
// list is aggregated; per-path detail lives on the outbound
// Ingress→K8sService edges. lb_hostname / dns_name come from
// Status.LoadBalancer.Ingress so a future cross-account enricher can
// match the Ingress to its cloud LoadBalancer by DNS.
func (p *ingressBackendProcessor) createIngressNode(
	ingress *k8sIngressResource,
	cluster, environment string,
) *core.DbNode {
	hosts := make([]string, 0, len(ingress.Spec.Rules))
	paths := make([]string, 0)
	for i := range ingress.Spec.Rules {
		r := &ingress.Spec.Rules[i]
		if r.Host != "" {
			hosts = append(hosts, r.Host)
		}
		for j := range r.HTTP.Paths {
			if r.HTTP.Paths[j].Path != "" {
				paths = append(paths, r.HTTP.Paths[j].Path)
			}
		}
	}

	properties := map[string]interface{}{
		"name":          ingress.Metadata.Name,
		"namespace":     ingress.Metadata.Namespace,
		"cluster":       cluster,
		"environment":   environment,
		"ingress_class": ingress.Spec.IngressClassName,
		"hosts":         hosts,
		"paths":         paths,
	}
	if len(ingress.Metadata.Labels) > 0 {
		properties["labels"] = ingress.Metadata.Labels
	}
	if len(ingress.Metadata.Annotations) > 0 {
		properties["annotations"] = ingress.Metadata.Annotations
	}
	// Pin the first rule's host/path as the canonical query_attribute (the
	// query_attributes spec in core/types.go locks these as scalar strings).
	if len(hosts) > 0 {
		properties["host"] = hosts[0]
	}
	if len(paths) > 0 {
		properties["path"] = paths[0]
	}
	if lbi := ingress.Status.LoadBalancer.Ingress; len(lbi) > 0 {
		if lbi[0].Hostname != "" {
			properties["lb_hostname"] = lbi[0].Hostname
			properties["dns_name"] = strings.ToLower(lbi[0].Hostname)
		}
		if lbi[0].IP != "" {
			properties["lb_ip"] = lbi[0].IP
		}
	}

	properties["specific_type"] = "KubernetesIngress"

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeIngress,
		Properties:     properties,
		CloudAccountID: p.k8sAccountID,
	}
	uniqueKey := p.source.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeIngress, uniqueKey, properties, p.tenantID, p.k8sAccountID, "k8s")
}

func (p *ingressBackendProcessor) createBackendNode(
	serviceName, namespace, environment string,
	ingress *k8sIngressResource,
	rule *k8sIngressRule,
	path *k8sIngressPath,
) *core.DbNode {
	properties := map[string]interface{}{
		"name":          serviceName,
		"namespace":     namespace,
		"environment":   environment,
		"service.name":  serviceName,
		"ingress_host":  rule.Host,
		"ingress_path":  path.Path,
		"ingress_name":  ingress.Metadata.Name,
		"backend_port":  path.Backend.Service.Port.Number,
		"exposed_via":   "ingress",
		"ingress_class": ingress.Spec.IngressClassName,
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeService,
		Properties:     properties,
		CloudAccountID: p.k8sAccountID,
	}
	uniqueKey := p.source.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeService, uniqueKey, properties, p.tenantID, p.k8sAccountID, "k8s")
}

func (p *ingressBackendProcessor) createEdge(sourceKey, destKey string, props map[string]interface{}) *core.DbEdge {
	return p.createEdgeWithRel(sourceKey, destKey, core.RelationshipRoutesTo, props)
}

// createEdgeWithRel is the same as createEdge but lets callers pick the
// relationship type. Used for Ingress→K8sService which is RoutesToService,
// not the generic RoutesTo.
func (p *ingressBackendProcessor) createEdgeWithRel(
	sourceKey, destKey string,
	rel core.RelationshipType,
	props map[string]interface{},
) *core.DbEdge {
	return core.NewEdge(sourceKey, destKey, rel, props, p.tenantID, p.k8sAccountID, "k8s")
}

// findIngressControllerNodes identifies ingress controller nodes from workload and service nodes
func (s *K8sSource) findIngressControllerNodes(workloadNodes map[string]*core.DbNode, serviceNodes []*core.DbNode) []*core.DbNode {
	var controllers []*core.DbNode

	// Check workloads for ingress controllers
	for _, node := range workloadNodes {
		if s.isIngressController(node) {
			controllers = append(controllers, node)
		}
	}

	// Check services for LoadBalancer type with ingress-related names
	for _, node := range serviceNodes {
		if s.isIngressControllerService(node) {
			controllers = append(controllers, node)
		}
	}

	return controllers
}

// isIngressController checks if a workload node is an ingress controller
func (s *K8sSource) isIngressController(node *core.DbNode) bool {
	name, _ := core.GetNodePropertyString(node, "name")
	nameLower := strings.ToLower(name)

	// Check name patterns
	if strings.Contains(nameLower, "nginx-ingress") ||
		strings.Contains(nameLower, "ingress-nginx") ||
		strings.Contains(nameLower, "ingress-controller") ||
		strings.Contains(nameLower, "traefik") ||
		strings.Contains(nameLower, "haproxy-ingress") ||
		strings.Contains(nameLower, "kong-ingress") {
		return true
	}

	// Check labels
	if labels, ok := node.Properties["labels"].(map[string]interface{}); ok {
		if appName, ok := labels["app.kubernetes.io/name"].(string); ok {
			appNameLower := strings.ToLower(appName)
			if strings.Contains(appNameLower, "ingress") ||
				strings.Contains(appNameLower, "nginx") ||
				strings.Contains(appNameLower, "traefik") {
				return true
			}
		}
		if component, ok := labels["app.kubernetes.io/component"].(string); ok {
			if strings.ToLower(component) == "controller" {
				return true
			}
		}
	}

	return false
}

// isIngressControllerService checks if a service node is an ingress controller service
func (s *K8sSource) isIngressControllerService(node *core.DbNode) bool {
	// Check if it's a LoadBalancer service
	serviceType, _ := core.GetNodePropertyString(node, "service_type")
	if serviceType != "LoadBalancer" {
		return false
	}

	name, _ := core.GetNodePropertyString(node, "name")
	nameLower := strings.ToLower(name)

	return strings.Contains(nameLower, "ingress") ||
		strings.Contains(nameLower, "nginx") ||
		strings.Contains(nameLower, "traefik")
}
