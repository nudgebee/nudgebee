package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
)

// k8sServiceSchema is the concrete-property schema for KubernetesService.
// Names are the node.Properties keys written by createNodeFromK8sService; the
// ontology reads namespace/cluster/service_type/cluster_ip.
var k8sServiceSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesService",
	NodeType:     core.NodeTypeK8sService,
	Properties: []core.PropertyDef{
		{Name: "namespace", Indexed: true},
		{Name: "cluster", Indexed: true},
		{Name: "service_type", Indexed: true},
		{Name: "cluster_ip", Indexed: true},
		{Name: "uid"},
		{Name: "session_affinity"},
		{Name: "selector"},
		{Name: "annotations"},
		{Name: "ports"},
		{Name: "node_ports"},
		{Name: "external_ips"},
		{Name: "cluster_ips"},
		{Name: "creation_timestamp"},
		{Name: "load_balancer_hostname"},
		{Name: "load_balancer_ip"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "qualified_name"},
		{Name: "deletion_timestamp"},
		{Name: "load_balancer_ingress"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sServiceSchema) }

// fetchK8sServicesFromRelay fetches K8s services from the relay server
func (s *K8sSource) fetchK8sServicesFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sServiceFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay service fetch: cloud_account_id is empty")
		return []K8sServiceFromRelay{}, nil
	}

	s.logger.Info("fetching K8s services from relay server",
		"account_id", req.CloudAccountID)

	// Execute relay request
	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":          "",
				"version":        "v1",
				"resource_type":  "services",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Warn("failed to execute relay request for K8s services", "error", err)
		return nil, fmt.Errorf("failed to execute relay request: %w", err)
	}

	// Parse the nested response structure
	services, err := s.parseK8sServicesResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse K8s services response", "error", err)
		return nil, fmt.Errorf("failed to parse services response: %w", err)
	}

	s.logger.Info("successfully fetched K8s services from relay",
		"count", len(services),
		"account_id", req.CloudAccountID)

	return services, nil
}

// parseK8sServicesResponse parses the nested relay response to extract K8s services
func (s *K8sSource) parseK8sServicesResponse(response map[string]interface{}) ([]K8sServiceFromRelay, error) {
	// Navigate: response -> data
	dataAny, ok := response["data"]
	if !ok {
		return nil, fmt.Errorf("response missing 'data' field")
	}

	data, ok := dataAny.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("'data' is not a map")
	}

	// Navigate: data -> findings
	findingsAny, ok := data["findings"]
	if !ok {
		return nil, fmt.Errorf("data missing 'findings' field")
	}

	findings, ok := findingsAny.([]interface{})
	if !ok || len(findings) == 0 {
		s.logger.Warn("no findings in relay response")
		return []K8sServiceFromRelay{}, nil
	}

	// Get first finding
	finding, ok := findings[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("first finding is not a map")
	}

	// Navigate: finding -> evidence
	evidenceAny, ok := finding["evidence"]
	if !ok {
		return nil, fmt.Errorf("finding missing 'evidence' field")
	}

	evidence, ok := evidenceAny.([]interface{})
	if !ok || len(evidence) == 0 {
		s.logger.Warn("no evidence in relay response")
		return []K8sServiceFromRelay{}, nil
	}

	// Get first evidence
	evidenceItem, ok := evidence[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("first evidence is not a map")
	}

	// Navigate: evidence -> data (JSON string)
	evidenceDataAny, ok := evidenceItem["data"]
	if !ok {
		return nil, fmt.Errorf("evidence missing 'data' field")
	}

	evidenceDataStr, ok := evidenceDataAny.(string)
	if !ok {
		return nil, fmt.Errorf("evidence data is not a string")
	}

	// Parse the JSON string to get the array
	var dataArray []map[string]interface{}
	if err := json.Unmarshal([]byte(evidenceDataStr), &dataArray); err != nil {
		return nil, fmt.Errorf("failed to unmarshal evidence data: %w", err)
	}

	if len(dataArray) == 0 {
		s.logger.Warn("empty data array in evidence")
		return []K8sServiceFromRelay{}, nil
	}

	// Get the first element which contains the actual services data
	firstData := dataArray[0]

	// Navigate: firstData -> data (array of services)
	servicesDataAny, ok := firstData["data"]
	if !ok {
		return nil, fmt.Errorf("first data element missing 'data' field")
	}

	// Parse as array of services
	var servicesData []interface{}
	switch v := servicesDataAny.(type) {
	case string:
		// If it's a string, unmarshal it
		if err := json.Unmarshal([]byte(v), &servicesData); err != nil {
			return nil, fmt.Errorf("failed to unmarshal services data string: %w", err)
		}
	case []interface{}:
		// If it's already an array, use it directly
		servicesData = v
	default:
		return nil, fmt.Errorf("services data is neither string nor array")
	}

	// Convert to K8sServiceFromRelay structs
	services := make([]K8sServiceFromRelay, 0, len(servicesData))
	for _, svcAny := range servicesData {
		svcMap, ok := svcAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid service entry")
			continue
		}

		// Marshal and unmarshal to convert to struct
		svcBytes, err := json.Marshal(svcMap)
		if err != nil {
			s.logger.Warn("failed to marshal service", "error", err)
			continue
		}

		var service K8sServiceFromRelay
		if err := json.Unmarshal(svcBytes, &service); err != nil {
			s.logger.Warn("failed to unmarshal service", "error", err)
			continue
		}

		services = append(services, service)
	}

	return services, nil
}

// convertK8sServicesToGraph converts K8s services from relay to knowledge graph nodes and edges
func (s *K8sSource) convertK8sServicesToGraph(services []K8sServiceFromRelay, workloads []K8sWorkloadRow, clusterNodes, namespaceNodes map[string]*core.DbNode, workloadNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Build a map of workloads for matching services to pods
	workloadMap := make(map[string]*K8sWorkloadRow)
	for i := range workloads {
		key := fmt.Sprintf("%s/%s", workloads[i].Namespace, workloads[i].Name)
		workloadMap[key] = &workloads[i]
	}

	for _, service := range services {
		// Determine cluster name - we'll get this from the first matching workload
		// or use a default value
		clusterName := s.getClusterNameForService(&service, workloads)

		// Create service node
		serviceNode := s.createNodeFromK8sService(&service, clusterName, req)
		nodes = append(nodes, serviceNode)

		// Create or get namespace node
		namespaceKey := fmt.Sprintf("%s/%s", clusterName, service.Metadata.Namespace)
		var namespaceNode *core.DbNode
		if existingNs, exists := namespaceNodes[namespaceKey]; exists {
			namespaceNode = existingNs
		} else {
			namespaceNode = s.createNamespaceNode(service.Metadata.Namespace, clusterName, req)
			namespaceNodes[namespaceKey] = namespaceNode
			nodes = append(nodes, namespaceNode)
		}

		// Link service to namespace
		edge := core.NewEdge(
			serviceNode.ID,
			namespaceNode.ID,
			core.RelationshipRunsOn,
			map[string]interface{}{
				"connection_type": "namespace",
			},
			req.TenantID,
			req.CloudAccountID,
			"k8s",
		)
		edges = append(edges, edge)

		// Create or get cluster node
		if clusterName != "" {
			var clusterNode *core.DbNode
			if existingCluster, exists := clusterNodes[clusterName]; exists {
				clusterNode = existingCluster
			} else {
				clusterNode = s.createClusterNode(clusterName, req)
				clusterNodes[clusterName] = clusterNode
				nodes = append(nodes, clusterNode)
			}

			// Link namespace to cluster (if not already linked)
			nsToClusterEdge := core.NewEdge(
				namespaceNode.ID,
				clusterNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{
					"connection_type": "cluster",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, nsToClusterEdge)
		}

		// Match service to workloads based on selector
		if len(service.Spec.Selector) > 0 {
			matchedWorkloads := s.matchServiceToWorkloads(&service, workloads)
			for _, workload := range matchedWorkloads {
				// Find the actual workload node - use the same key format as convertWorkloadsToGraph
				workloadKey := fmt.Sprintf("%s/%s/%s/%s",
					workload.ClusterName,
					workload.Kind,
					workload.Namespace,
					workload.Name)

				workloadNode, workloadExists := workloadNodes[workloadKey]
				if !workloadExists {
					continue
				}

				// Create edge from service to workload (service exposes workload)
				edge := core.NewEdge(
					workloadNode.ID,
					serviceNode.ID,
					core.RelationshipExposes,
					map[string]interface{}{
						"connection_type": "service_selector",
						"selector":        service.Spec.Selector,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)
			}
		}
	}

	return nodes, edges, clusterNodes, namespaceNodes
}

// getClusterNameForService determines the cluster name for a service by looking at workloads
func (s *K8sSource) getClusterNameForService(service *K8sServiceFromRelay, workloads []K8sWorkloadRow) string {
	return clusterNameForNamespace(service.Metadata.Namespace, workloads)
}

// createNodeFromK8sService creates a knowledge graph node from a K8s service
func (s *K8sSource) createNodeFromK8sService(service *K8sServiceFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = service.Metadata.Name
	properties["namespace"] = service.Metadata.Namespace
	properties["cluster"] = clusterName
	properties["uid"] = service.Metadata.UID
	properties["cluster_ip"] = service.Spec.ClusterIP
	properties["service_type"] = service.Spec.Type
	properties["session_affinity"] = service.Spec.SessionAffinity

	// Add labels if present
	if len(service.Metadata.Labels) > 0 {
		properties["labels"] = service.Metadata.Labels
	}

	// Add annotations if present
	if len(service.Metadata.Annotations) > 0 {
		properties["annotations"] = service.Metadata.Annotations
	}

	// Add selector if present
	if len(service.Spec.Selector) > 0 {
		properties["selector"] = service.Spec.Selector
	}

	// Add ports and extract node_ports for cross-source matching
	if len(service.Spec.Ports) > 0 {
		ports := make([]map[string]interface{}, 0, len(service.Spec.Ports))
		nodePorts := make([]int, 0)
		for _, port := range service.Spec.Ports {
			portMap := map[string]interface{}{
				"name":     port.Name,
				"port":     port.Port,
				"protocol": port.Protocol,
			}
			if port.TargetPort != nil {
				portMap["target_port"] = port.TargetPort
			}
			if port.NodePort != nil {
				portMap["node_port"] = *port.NodePort
				nodePorts = append(nodePorts, *port.NodePort)
			}
			ports = append(ports, portMap)
		}
		properties["ports"] = ports
		// Add node_ports as top-level property for ALB -> NodePort service matching
		if len(nodePorts) > 0 {
			properties["node_ports"] = nodePorts
		}
	}

	// Add external IPs if present
	if len(service.Spec.ExternalIPs) > 0 {
		properties["external_ips"] = service.Spec.ExternalIPs
	}

	// Add cluster IPs if present
	if len(service.Spec.ClusterIPs) > 0 {
		properties["cluster_ips"] = service.Spec.ClusterIPs
	}

	// Add creation timestamp
	if service.Metadata.CreationTimestamp != "" {
		properties["creation_timestamp"] = service.Metadata.CreationTimestamp
	}

	// Extract load balancer hostname from status (for services of type LoadBalancer)
	if service.Spec.Type == "LoadBalancer" {
		if len(service.Status.LoadBalancer.Ingress) > 0 {
			firstIngress := service.Status.LoadBalancer.Ingress[0]
			// AWS ELBs use "hostname" field
			if firstIngress.Hostname != "" {
				properties["load_balancer_hostname"] = firstIngress.Hostname
			}
			// Some cloud providers use "ip" field instead
			if firstIngress.IP != "" {
				properties["load_balancer_ip"] = firstIngress.IP
			}
		}
	}

	// Add subtype property for K8s service
	properties["subtype"] = service.Spec.Type
	properties["specific_type"] = "KubernetesService"

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeK8sService,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeK8sService, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// matchServiceToWorkloads finds workloads that match the service selector
func (s *K8sSource) matchServiceToWorkloads(service *K8sServiceFromRelay, workloads []K8sWorkloadRow) []K8sWorkloadRow {
	matched := make([]K8sWorkloadRow, 0)

	// If no selector, no matches
	if len(service.Spec.Selector) == 0 {
		return matched
	}

	for _, workload := range workloads {
		// Only match workloads in the same namespace
		if workload.Namespace != service.Metadata.Namespace {
			continue
		}

		// Extract labels from workload using multiple sources
		workloadLabels := s.extractWorkloadLabels(&workload)

		// Check if all service selector labels match workload labels
		if s.labelsMatch(service.Spec.Selector, workloadLabels) {
			matched = append(matched, workload)
		}
	}

	return matched
}
