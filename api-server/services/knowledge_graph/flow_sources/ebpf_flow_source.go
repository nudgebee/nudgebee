package flow_sources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

func init() {
	// Register the eBPF flow source factory in the global registry
	RegisterFlowSourceFactory(
		"ebpf",
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewEbpfFlowSource(logger), nil
		},
		"eBPF-based service map flow source that fetches network flow data from relay server",
		string(core.FlowSourceCategoryTracing),
	)
}

// EbpfFlowSource creates flow relationships from eBPF service map data
// It fetches service-to-service network flow data from the relay server and creates
// both nodes (for external services and missing entities) and edges in the knowledge graph
// NOTE: External service enrichment is done centrally in core/service.go after all flow sources complete
type EbpfFlowSource struct {
	*BaseFlowSource
	IgnoreIPAddresses bool // If true, skip nodes whose name is a raw IP address (default: true)
}

// NewEbpfFlowSource creates a new eBPF flow source
func NewEbpfFlowSource(logger *slog.Logger) *EbpfFlowSource {
	base := NewBaseFlowSource(
		"ebpf",
		core.FlowSourceCategoryTracing,
		true,
		logger,
	)

	return &EbpfFlowSource{
		BaseFlowSource:    base,
		IgnoreIPAddresses: true,
	}
}

// isIPAddress returns true if the given name is a raw IP address (or IP:port).
// When IgnoreIPAddresses is false this always returns false.
func (s *EbpfFlowSource) isIPAddress(name string) bool {
	if !s.IgnoreIPAddresses || name == "" {
		return false
	}
	host := name
	if h, _, err := net.SplitHostPort(name); err == nil {
		host = h
	}
	return net.ParseIP(host) != nil
}

// isIgnoredKind returns true for K8s resource kinds that should be skipped entirely.
// These are ephemeral or intermediate controller resources that are already represented
// by higher-level workload nodes (Deployment, StatefulSet, DaemonSet).
func (s *EbpfFlowSource) isIgnoredKind(kind string) bool {
	ignoredKinds := map[string]bool{
		"pod":        true, // ephemeral instances, covered by Deployment/StatefulSet/DaemonSet
		"replicaset": true, // managed by Deployment controller, duplicate of Deployment node
		"staticpods": true, // kubelet-managed low-level pods, noise
	}
	return ignoredKinds[strings.ToLower(kind)]
}

// Validate validates the eBPF flow source configuration
func (s *EbpfFlowSource) Validate() error {
	if err := s.BaseFlowSource.Validate(); err != nil {
		return err
	}

	// No additional validation needed for eBPF
	return nil
}

// BuildFlowRelationships builds flow relationships from eBPF service map data
func (s *EbpfFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	ctx := reqCtx.GetContext()
	startTime := TimeNow()
	defer s.TrackBuildTime(startTime)

	s.logger.Info("building flow relationships from eBPF",
		"source", s.GetName(),
		"tenant_id", req.TenantID,
		"existing_nodes", len(req.ExistingNodes))

	// Initialize node matcher
	s.InitializeNodeMatcher(req.ExistingNodes)

	edges := make([]*core.DbEdge, 0)
	nodes := make([]*core.DbNode, 0)
	externalServiceNodesByAccount := make(map[string]map[string]*core.DbNode) // Track external services by account_id

	// Build the ClusterIP resolver once per build (not per account). req.ExistingNodes
	// is constant across the per-account loop below, so rebuilding inside processK8sAccount
	// would be wasted work.
	ipResolver := NewK8sServiceIPResolver(req.ExistingNodes)

	// Build the Node-IP resolver once per build for the same reason. Unlike
	// PodIPResolver (which queries kube_pod_info per-account), Node nodes are
	// already in req.ExistingNodes via K8sSource — no relay call needed.
	nodeIPResolver := NewK8sNodeIPResolver(req.ExistingNodes)

	k8sAccounts, err := core.GetK8sAccountsForTenant(req.TenantID, req.CloudAccountIDs)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to get K8s accounts: %w", err)
	}

	if len(k8sAccounts) == 0 {
		s.logger.Info("no K8s accounts with connected agents found", "tenant_id", req.TenantID)
		return edges, nodes, nil
	}

	s.logger.Info("found K8s accounts with connected agents",
		"count", len(k8sAccounts),
		"tenant_id", req.TenantID)

	// Process each K8s account
	for _, account := range k8sAccounts {
		s.logger.Info("processing K8s account for eBPF flow source",
			"cloud_account_id", account.CloudAccountID,
			"tenant", account.Tenant)

		// Initialize external service map for this account if it doesn't exist
		if _, exists := externalServiceNodesByAccount[account.CloudAccountID]; !exists {
			externalServiceNodesByAccount[account.CloudAccountID] = make(map[string]*core.DbNode)
		}

		accountEdges, accountNewNodes, accountExternalNodes, err := s.processK8sAccount(ctx, req, account, externalServiceNodesByAccount[account.CloudAccountID], ipResolver, nodeIPResolver)
		if err != nil {
			s.logger.Error("failed to process K8s account",
				"cloud_account_id", account.CloudAccountID,
				"error", err)
			s.IncrementErrorCount()
			continue
		}

		edges = append(edges, accountEdges...)
		// Add new nodes created for unmatched K8s services/workloads
		nodes = append(nodes, accountNewNodes...)
		// Merge external service nodes for this account
		for key, node := range accountExternalNodes {
			nodes = append(nodes, node)
			externalServiceNodesByAccount[account.CloudAccountID][key] = node
		}
	}

	// Count external services for logging
	// after ALL flow sources have been executed, allowing cross-source node matching
	totalExternalServices := 0
	for _, accountExternalServices := range externalServiceNodesByAccount {
		totalExternalServices += len(accountExternalServices)
	}

	if totalExternalServices > 0 {
		s.logger.Info("external services will be enriched centrally after all flow sources complete",
			"total_external_services_count", totalExternalServices,
			"accounts_with_external_services", len(externalServiceNodesByAccount))
	}

	s.LogMetrics()

	s.logger.Info("completed building flow relationships from eBPF",
		"total_edges_created", len(edges),
		"k8s_accounts_processed", len(k8sAccounts),
		"external_services_enriched", totalExternalServices,
		"accounts_with_external_services", len(externalServiceNodesByAccount))

	return edges, nodes, nil
}

// enrichNodeWithLanguage adds language information to a node from application Type data
// This enriches matched nodes that may not have language information
func (s *EbpfFlowSource) enrichNodeWithLanguage(node *core.DbNode, app *core.ServiceApplication) {
	if node == nil || app == nil || len(app.Type) == 0 {
		return
	}

	// Check if node already has language, if not add it
	if _, hasLanguage := node.Properties["language"]; !hasLanguage {
		if language := core.GetPrimaryLanguage(app.Type); language != "" {
			node.Properties["language"] = language
		}
	}
}

// processK8sAccount processes a single K8s account and returns edges, new nodes, and external service nodes.
// ipResolver and nodeIPResolver are both built once per BuildFlowRelationships
// invocation (they read req.ExistingNodes which is account-invariant) and
// shared across accounts. The pod-IP resolver, by contrast, is built per
// account because it queries kube_pod_info via the per-account Prometheus relay.
func (s *EbpfFlowSource) processK8sAccount(
	ctx context.Context,
	req *core.FlowSourceBuildRequest,
	account core.K8sAccount,
	globalExternalServiceNodes map[string]*core.DbNode,
	ipResolver *K8sServiceIPResolver,
	nodeIPResolver *K8sNodeIPResolver,
) ([]*core.DbEdge, []*core.DbNode, map[string]*core.DbNode, error) {
	edges := make([]*core.DbEdge, 0)
	newNodes := make([]*core.DbNode, 0)
	externalServiceNodes := make(map[string]*core.DbNode)

	// Extract account ID for use throughout the function
	K8sAccountID := account.CloudAccountID

	s.logger.Info("fetching service map from relay server",
		"cloud_account_id", K8sAccountID,
		"tenant_id", req.TenantID)

	// Execute relay request to get service map
	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    K8sAccountID,
			ActionName:   relay.ServiceMapActionName,
			ActionParams: map[string]any{
				// Add any additional params here if needed
			},
		},
	}

	// Execute the relay request
	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, nil, fmt.Errorf("failed to execute relay request for service map: %w", err)
	}

	// Parse the service map from relay response
	serviceMap, err := s.parseServiceMapFromRelay(relayResponse)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, nil, fmt.Errorf("failed to parse service map from relay response: %w", err)
	}

	if serviceMap == nil {
		s.logger.Warn("service map is nil, no data found")
		return edges, newNodes, externalServiceNodes, nil
	}

	s.logger.Info("fetched service map from eBPF",
		"cloud_account_id", K8sAccountID,
		"applications_count", len(serviceMap.Applications),
		"generated_at", serviceMap.GeneratedAt.Format(time.RFC3339))

	// ipResolver is built once per BuildFlowRelationships invocation and
	// passed in — used by the `:ExternalService:<ip>` bypass branch below
	// to short-circuit raw-IP destinations into K8sService CALLS edges.
	//
	// podIPResolver is the pod-IP counterpart: built per-account because
	// kube_pod_info comes from the per-account Prometheus relay. Same bypass
	// branch consults it after ResolveIPToK8sService misses, so traffic to a
	// raw pod IP (e.g. a headless-service backend like rabbitmq-0) becomes a
	// CALLS edge to the owning Workload instead of an orphan ExternalService.
	podIPResolver := NewPodIPResolver(K8sAccountID, req.ExistingNodes, s.logger)

	// Track unmatched services for analysis
	unmatchedSources := make(map[string]int)
	unmatchedDestinations := make(map[string]int)

	// Process each application and its connections
	for i := range serviceMap.Applications {
		app := &serviceMap.Applications[i]

		// Skip ephemeral/intermediate K8s resource kinds (pods, ReplicaSets, StaticPods)
		if s.isIgnoredKind(app.Id.Kind) {
			continue
		}
		// Skip apps whose name is a raw IP address
		if s.isIPAddress(app.Id.Name) {
			continue
		}
		// Skip the eBPF source's "catch-all external" bucket: an application
		// with empty Name and many Instances whose actual endpoints live in
		// labels rather than separate ServiceApplications. As a single node
		// it has no topology signal (every workload with any external traffic
		// gets one bogus edge to it pointing at a bag of 1k+ IPs), so dropping
		// it removes graph noise without losing usable information. Per-IP
		// resolution still happens via the :ExternalService:<ip> bypass branch
		// downstream.
		if app.Id.Name == "" {
			continue
		}

		// Try to match the source application to an existing node
		sourceNode, sourceErr := s.matchApplicationToNode(app, K8sAccountID)
		if sourceErr != nil {
			// Create new node for unmatched source
			sourceName := s.getApplicationName(app)
			s.logger.Debug("source service not found in knowledge graph, creating new node",
				"service", sourceName,
				"kind", app.Id.Kind,
				"namespace", app.Id.Namespace)

			sourceNode = s.createNodeForApplication(app, req.TenantID, account)
			newNodes = append(newNodes, sourceNode)
			unmatchedSources[sourceName]++
			s.logger.Debug("created NEW node for unmatched application",
				"node_id", sourceNode.ID,
				"node_unique_key", sourceNode.UniqueKey,
				"app_name", sourceName)
		} else {
			// Enrich matched node with language information from eBPF data
			s.enrichNodeWithLanguage(sourceNode, app)
			s.logger.Debug("MATCHED existing node for application",
				"node_id", sourceNode.ID,
				"node_unique_key", sourceNode.UniqueKey,
				"app_name", s.getApplicationName(app))
		}

		// Process downstream connections (calls from this service to others)
		for j := range app.Downstreams {
			downstream := &app.Downstreams[j]

			// Find the destination application
			destApp := s.findApplicationByID(serviceMap.Applications, downstream.Id)
			var downstreamNode *core.DbNode
			var destErr error

			if destApp != nil {
				// Skip ephemeral/intermediate K8s resource kinds, raw IP names,
				// and the empty-name catch-all aggregate (see outer-loop skip).
				if s.isIgnoredKind(destApp.Id.Kind) || s.isIPAddress(destApp.Id.Name) || destApp.Id.Name == "" {
					continue
				}
				// Try to match destination application to existing node
				downstreamNode, destErr = s.matchApplicationToNode(destApp, K8sAccountID)
				if destErr != nil {
					// Create new node for unmatched destination
					destName := s.getApplicationName(destApp)
					s.logger.Debug("destination service not found in knowledge graph, creating new node",
						"service", destName,
						"kind", destApp.Id.Kind,
						"namespace", destApp.Id.Namespace)

					downstreamNode = s.createNodeForApplication(destApp, req.TenantID, account)
					newNodes = append(newNodes, downstreamNode)
					unmatchedDestinations[destName]++
				} else {
					// Enrich matched node with language information from eBPF data
					s.enrichNodeWithLanguage(downstreamNode, destApp)
				}
			} else {
				// Destination not found in service map — try to match/create by ID.
				// Skip ephemeral kinds, raw IP addresses, and the empty-name
				// catch-all aggregate (see outer-loop skip).
				if s.isIgnoredKind(downstream.Id.Kind) || s.isIPAddress(downstream.Id.Name) || downstream.Id.Name == "" {
					continue
				}
				var matchErr error
				downstreamNode, matchErr = s.matchByApplicationID(downstream.Id, K8sAccountID)
				if matchErr != nil {
					downstreamNode = s.createNodeForApplicationID(downstream.Id, req.TenantID, account)
					newNodes = append(newNodes, downstreamNode)
				}
				if downstream.Id.Name != "" {
					unmatchedDestinations[downstream.Id.Name]++
					// Only track ExternalService nodes for external enrichment
					if downstreamNode.NodeType == core.NodeTypeExternalService {
						externalServiceNodes[downstream.Id.Name] = downstreamNode
					}
				}
			}

			// Create edge with eBPF metadata
			properties := s.buildEdgeProperties(downstream, app, destApp)

			edge := s.CreateEdge(
				downstreamNode,
				sourceNode,
				core.RelationshipCalls,
				properties,
				req.TenantID,
				K8sAccountID,
			)

			if edge != nil {
				edges = append(edges, edge)
			}
		}

		// Process upstream connections (services calling this service)
		for k := range app.Upstreams {
			skipNodeSearch := false
			upstream := &app.Upstreams[k]

			// Parse upstream ID (string format like "namespace:kind:name")
			upstreamID := s.parseUpstreamID(upstream.Id)
			var upstreamNode *core.DbNode
			// Provenance for the edge below when we resolve an IP-named
			// ExternalService directly to a K8s node (Service ClusterIP or
			// owning Workload via pod IP).
			var resolvedFromIP, resolvedReason, resolutionSource string
			var resolvedOK bool
			if strings.Contains(upstream.Id, ":ExternalService:") {
				upstreamID = &core.ServiceApplicationId{
					Name:      strings.TrimPrefix(upstream.Id, ":ExternalService:"),
					Kind:      "ExternalService",
					Namespace: "",
				}
				// Source-level short-circuit: if the bypass branch's name is a
				// raw IP, try to resolve it to an existing K8s node before
				// falling back to an orphan ExternalService.
				//
				// Two ordered attempts:
				//   1. K8sServiceIPResolver — matches K8s Service ClusterIPs
				//   2. PodIPResolver — matches pod IPs to their owning Workload
				//      (covers headless services, direct pod-IP traffic, etc.)
				//
				// Caller cluster is unknown here (the bypass branch has no
				// source-node cluster context) — pass "" and rely on the
				// resolvers' global-unique fallback.
				upstreamNode, resolvedFromIP, resolvedReason, resolutionSource, resolvedOK =
					resolveIPNamedExternalService(upstreamID.Name, "", ipResolver, podIPResolver, nodeIPResolver)
				switch {
				case resolvedOK:
					skipNodeSearch = true
				case IsSpecialIPName(upstreamID.Name):
					// Loopback / link-local / metadata IPs carry no topology signal.
					// Drop the edge entirely instead of creating an orphan
					// ExternalService that bloats "what does this workload call?".
					continue
				default:
					upstreamNode = s.createExternalServiceNode(*upstreamID, req.TenantID, account)
					newNodes = append(newNodes, upstreamNode)
					if upstreamID.Name != "" {
						externalServiceNodes[upstreamID.Name] = upstreamNode // Track for enrichment
						unmatchedSources[upstreamID.Name]++
						skipNodeSearch = true
					}
				}
			}
			if upstreamID == nil {
				s.logger.Debug("failed to parse upstream ID",
					"upstream_id", upstream.Id)
				continue
			}

			// Find the upstream application
			upstreamApp := s.findApplicationByID(serviceMap.Applications, *upstreamID)

			if upstreamApp != nil && !skipNodeSearch {
				// Skip ephemeral kinds, raw IP names, and the empty-name
				// catch-all aggregate (see outer-loop skip).
				if s.isIgnoredKind(upstreamApp.Id.Kind) || s.isIPAddress(upstreamApp.Id.Name) || upstreamApp.Id.Name == "" {
					continue
				}
				// Try to match upstream application to existing node
				var upstreamErr error
				upstreamNode, upstreamErr = s.matchApplicationToNode(upstreamApp, K8sAccountID)
				if upstreamErr != nil {
					// Create new node for unmatched upstream
					upstreamName := s.getApplicationName(upstreamApp)
					s.logger.Debug("upstream service not found in knowledge graph, creating new node",
						"service", upstreamName,
						"kind", upstreamApp.Id.Kind,
						"namespace", upstreamApp.Id.Namespace)

					upstreamNode = s.createNodeForApplication(upstreamApp, req.TenantID, account)
					newNodes = append(newNodes, upstreamNode)
					unmatchedSources[upstreamName]++
				} else {
					// Enrich matched node with language information from eBPF data
					s.enrichNodeWithLanguage(upstreamNode, upstreamApp)
				}
			} else if !skipNodeSearch {
				// Upstream not found in service map — try to match/create by ID.
				// Skip ephemeral kinds, raw IP names, and the empty-name
				// catch-all aggregate (see outer-loop skip).
				if s.isIgnoredKind(upstreamID.Kind) || s.isIPAddress(upstreamID.Name) || upstreamID.Name == "" {
					continue
				}
				var matchErr error
				upstreamNode, matchErr = s.matchByApplicationID(*upstreamID, K8sAccountID)
				if matchErr != nil {
					upstreamNode = s.createNodeForApplicationID(*upstreamID, req.TenantID, account)
					newNodes = append(newNodes, upstreamNode)
				}
				if upstreamID.Name != "" {
					unmatchedSources[upstreamID.Name]++
					// Only track ExternalService nodes for external enrichment
					if upstreamNode.NodeType == core.NodeTypeExternalService {
						externalServiceNodes[upstreamID.Name] = upstreamNode
					}
				}
			}

			// Create edge from upstream to current app
			properties := s.buildUpstreamEdgeProperties(upstream, upstreamApp, app)
			if resolvedOK {
				properties["resolved_from_ip"] = resolvedFromIP
				properties["resolution_source"] = resolutionSource
				properties["resolution_reason"] = resolvedReason
			}

			edge := s.CreateEdge(
				sourceNode,
				upstreamNode,
				core.RelationshipCalls,
				properties,
				req.TenantID,
				K8sAccountID,
			)

			if edge != nil {
				edges = append(edges, edge)
			}
		}
	}

	// Log unmatched services for analysis
	if len(unmatchedSources) > 0 || len(unmatchedDestinations) > 0 {
		s.logger.Info("created new nodes for unmatched services in eBPF",
			"cloud_account_id", K8sAccountID,
			"new_nodes_count", len(newNodes),
			"unmatched_sources_count", len(unmatchedSources),
			"unmatched_destinations_count", len(unmatchedDestinations))
	}

	return edges, newNodes, externalServiceNodes, nil
}

// validateServiceApplicationsJSON validates the JSON structure before unmarshaling,
// converting any string-valued numeric fields (e.g. Latency sent as "123.4") to actual
// numbers. It returns the corrected JSON bytes so callers use the fixed data for
// the final unmarshal instead of the original bytes.
func (s *EbpfFlowSource) validateServiceApplicationsJSON(dataBytes []byte) ([]byte, error) {
	var rawData []map[string]interface{}
	if err := json.Unmarshal(dataBytes, &rawData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON for validation: %w", err)
	}

	numericFields := []string{"Weight", "Latency", "RequestCount", "FailureCount", "BytesSent", "BytesReceived"}

	for appIdx, app := range rawData {
		if err := s.validateLinkArray(app, "Upstreams", appIdx, numericFields); err != nil {
			return nil, err
		}
		if err := s.validateLinkArray(app, "Downstreams", appIdx, numericFields); err != nil {
			return nil, err
		}
	}

	// Re-marshal the corrected data so the caller gets type-fixed bytes.
	corrected, err := json.Marshal(rawData)
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal corrected service map: %w", err)
	}
	return corrected, nil
}

// validateLinkArray validates numeric fields in Upstreams or Downstreams arrays
func (s *EbpfFlowSource) validateLinkArray(app map[string]interface{}, arrayName string, appIdx int, numericFields []string) error {
	links, ok := app[arrayName].([]interface{})
	if !ok {
		return nil
	}

	for linkIdx, link := range links {
		linkMap, ok := link.(map[string]interface{})
		if !ok {
			continue
		}

		if err := s.validateNumericFields(linkMap, numericFields, appIdx, linkIdx, arrayName); err != nil {
			return err
		}
	}

	return nil
}

// validateNumericFields checks that numeric fields are not strings
func (s *EbpfFlowSource) validateNumericFields(data map[string]interface{}, fields []string, appIdx, linkIdx int, arrayName string) error {
	for _, field := range fields {
		value, exists := data[field]
		if !exists || value == nil {
			continue
		}

		if strVal, isString := value.(string); isString {
			// Try to convert the string to a float64
			// usage: import "strconv"
			if numVal, err := strconv.ParseFloat(strVal, 64); err == nil {
				// SUCCESS: Update the map with the numeric value
				data[field] = numVal
			} else {
				// FAILURE: The string was not a valid number, return the error
				return fmt.Errorf("application[%d].%s[%d].%s has string value '%s' which could not be converted to numeric",
					appIdx, arrayName, linkIdx, field, strVal)
			}
		}
	}
	return nil
}

// parseServiceMapFromRelay parses the service map from the relay response
func (s *EbpfFlowSource) parseServiceMapFromRelay(relayResponse map[string]any) (*core.EBPFServiceMap, error) {
	// Extract data field
	dataAny, ok := relayResponse["data"]
	if !ok {
		return nil, fmt.Errorf("relay response missing 'data' field")
	}

	dataMap, ok := dataAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("relay response 'data' is not a map")
	}

	// Check success field
	if success, ok := dataMap["success"].(bool); ok && !success {
		return nil, fmt.Errorf("relay request was not successful")
	}

	// Extract data array
	dataArrayAny, ok := dataMap["data"]
	if !ok {
		return nil, fmt.Errorf("relay response missing 'data.data' field")
	}

	// Marshal and unmarshal to convert to ServiceApplication slice
	dataBytes, err := json.Marshal(dataArrayAny)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal service map data: %w", err)
	}

	// Pre-validate JSON structure and fix string-typed numeric fields.
	correctedBytes, err := s.validateServiceApplicationsJSON(dataBytes)
	if err != nil {
		s.logger.Error("JSON validation failed before unmarshal",
			"error", err,
			"data_preview", string(dataBytes[:min(len(dataBytes), 500)]))
		return nil, fmt.Errorf("invalid service applications JSON structure: %w", err)
	}

	var applications []core.ServiceApplication
	if err := json.Unmarshal(correctedBytes, &applications); err != nil {
		// Enhanced error reporting for unmarshal failures
		if typeErr, ok := err.(*json.UnmarshalTypeError); ok {
			s.logger.Error("type mismatch during unmarshal",
				"field", typeErr.Field,
				"expected_type", typeErr.Type.String(),
				"actual_value", typeErr.Value,
				"offset", typeErr.Offset)
			return nil, fmt.Errorf("type mismatch in field '%s': expected %s but got %s at offset %d",
				typeErr.Field, typeErr.Type.String(), typeErr.Value, typeErr.Offset)
		}
		return nil, fmt.Errorf("failed to unmarshal service applications: %w", err)
	}

	serviceMap := &core.EBPFServiceMap{
		Applications: applications,
		GeneratedAt:  time.Now(),
	}

	return serviceMap, nil
}

// getApplicationName returns a readable name for the application
// If the application is a Pod or the name looks like a pod name, it extracts the workload name
func (s *EbpfFlowSource) getApplicationName(app *core.ServiceApplication) string {
	if app.Id.Name != "" {
		// If the kind is Pod or the name looks like a pod name, extract the workload name
		if app.Id.Kind == "Pod" || s.isPodName(app.Id.Name) {
			_, workloadName := core.ExtractPodOwner(app.Id.Name)
			return workloadName
		}
		return app.Id.Name
	}
	return fmt.Sprintf("%s/%s", app.Id.Namespace, app.Id.Kind)
}

// isPodName checks if a name looks like a Kubernetes pod name
// This is used ONLY when Kind is not "Pod" to detect pod names by pattern.
//
// Detects these patterns:
//   - Deployment: {name}-{8-10 char hash}-{5 char pod-id} (e.g., nginx-5d8b7c9f5-abc12)
//   - Job/CronJob: {name}-{5 char job-hash}-{5 char pod-id} (e.g., k8s-action-runner-nudgebee-brdzm-29kp9)
//
// Note: We intentionally DON'T detect:
//   - StatefulSet pattern ({name}-{ordinal}) - too many false positives like "service-8080", "redis-3"
//   - DaemonSet pattern ({name}-{pod-id}) - too many false positives like "konnectivity-agent"
//
// For StatefulSet and DaemonSet pods, the Kind should be "Pod" which triggers extraction directly.
func (s *EbpfFlowSource) isPodName(name string) bool {
	parts := strings.Split(name, "-")

	// Must have at least 3 parts to avoid false positives
	if len(parts) < 3 {
		return false
	}

	podID := parts[len(parts)-1]
	hash := parts[len(parts)-2]

	// Pod ID is typically 5 lowercase alphanumeric characters
	if len(podID) != 5 || !core.IsAlphanumeric(podID) {
		return false
	}

	// Check for Deployment pattern: hash is 8-10 chars
	if len(hash) >= 8 && len(hash) <= 10 && core.IsAlphanumeric(hash) {
		return true
	}

	// Check for Job/CronJob pattern: both job-hash and pod-id are 5 chars
	// e.g., k8s-action-runner-nudgebee-brdzm-29kp9
	if len(hash) == 5 && core.IsAlphanumeric(hash) {
		return true
	}

	return false
}

// getWorkloadName extracts the workload name from an application
// For pods or names that look like pod names, it extracts the owner workload name
// This is used for node creation and matching to ensure all replicas map to the same workload
func (s *EbpfFlowSource) getWorkloadName(app *core.ServiceApplication) string {
	if app.Id.Name == "" {
		return ""
	}
	if app.Id.Kind == "Pod" || s.isPodName(app.Id.Name) {
		_, workloadName := core.ExtractPodOwner(app.Id.Name)
		return workloadName
	}
	return app.Id.Name
}

// findApplicationByID finds an application in the list by its ID
func (s *EbpfFlowSource) findApplicationByID(
	applications []core.ServiceApplication,
	id core.ServiceApplicationId,
) *core.ServiceApplication {
	for i := range applications {
		app := &applications[i]
		if app.Id.Name == id.Name &&
			app.Id.Kind == id.Kind &&
			app.Id.Namespace == id.Namespace {
			return app
		}
	}
	return nil
}

// matchApplicationToNode matches a service application to a knowledge graph node
func (s *EbpfFlowSource) matchApplicationToNode(
	app *core.ServiceApplication,
	K8sAccountId string,
) (*core.DbNode, error) {
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil, fmt.Errorf("node matcher not initialized")
	}

	appName := s.getApplicationName(app)

	// Get workload name for matching (extracts from pod name if applicable)
	// This ensures pod replicas match to the same workload node
	workloadName := s.getWorkloadName(app)

	// from focus to loose search

	// Strategy 1: Match by namespace and name (for K8s services) (same account)
	if app.Id.Namespace != "" && workloadName != "" {
		result, err := matcher.FindNode(MatchCriteria{
			AccountID: K8sAccountId,
			NodeType:  s.inferNodeType(app.Id.Kind),
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "namespace",
					Value:         app.Id.Namespace,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
				{
					PropertyPath:  "name",
					Value:         workloadName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by namespace and name (strategy 1)",
				"namespace", app.Id.Namespace,
				"name", workloadName,
				"node", result.Node.UniqueKey,
				"confidence", result.Confidence)
			return result.Node, nil
		}
	}

	// Strategy 2: Exact match by name and kind (same account)
	if workloadName != "" {
		result, err := matcher.FindNode(MatchCriteria{
			AccountID: K8sAccountId,
			NodeType:  s.inferNodeType(app.Id.Kind),
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         workloadName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by name (strategy 2)",
				"app_name", workloadName,
				"node", result.Node.UniqueKey,
				"confidence", result.Confidence)
			return result.Node, nil
		}
	}

	// Strategy 3: Match by namespace and name (for K8s services) (any account)
	if app.Id.Namespace != "" && workloadName != "" {
		result, err := matcher.FindNode(MatchCriteria{
			NodeType: s.inferNodeType(app.Id.Kind),
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "namespace",
					Value:         app.Id.Namespace,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
				{
					PropertyPath:  "name",
					Value:         workloadName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by namespace and name (strategy 3)",
				"namespace", app.Id.Namespace,
				"name", workloadName,
				"node", result.Node.UniqueKey,
				"confidence", result.Confidence)
			return result.Node, nil
		}
	}

	// Strategy 4: Exact match by name and kind (any account)
	if workloadName != "" {
		result, err := matcher.FindNode(MatchCriteria{
			NodeType: s.inferNodeType(app.Id.Kind),
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         workloadName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by name (strategy 4)",
				"app_name", workloadName,
				"node", result.Node.UniqueKey,
				"confidence", result.Confidence)
			return result.Node, nil
		}
	}

	// Strategy 5: Match by service name in labels
	if serviceName, ok := app.Labels["service"]; ok && serviceName != "" {
		result, err := matcher.FindNode(MatchCriteria{
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "service_name",
					Value:         serviceName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by service label (strategy 5)",
				"service", serviceName,
				"node", result.Node.UniqueKey,
				"confidence", result.Confidence)
			return result.Node, nil
		}
	}

	// // Strategy 6: Fuzzy match by name (contains)
	// if app.Id.Name != "" {
	// 	result, err := matcher.FindNode(MatchCriteria{
	// 		PropertyMatches: []PropertyMatch{
	// 			{
	// 				PropertyPath:  "name",
	// 				Value:         app.Id.Name,
	// 				MatchType:     core.MatchTypeContains,
	// 				CaseSensitive: false,
	// 			},
	// 		},
	// 	})
	// 	if err == nil && result.Matched {
	// 		s.logger.Debug("matched application by name (contains) (strategy 6)",
	// 			"app_name", app.Id.Name,
	// 			"node", result.Node.UniqueKey,
	// 			"confidence", result.Confidence)
	// 		return result.Node, nil
	// 	}
	// }

	return nil, fmt.Errorf("no matching node found for application: %s (kind: %s)", appName, app.Id.Kind)
}

// createNodeForApplication creates a new node for an application
func (s *EbpfFlowSource) createNodeForApplication(
	app *core.ServiceApplication,
	tenantID string,
	cloudAccount core.K8sAccount,
) *core.DbNode {
	nodeType := s.inferNodeType(app.Id.Kind)

	// Get workload name (extracts from pod name if applicable)
	// This ensures all pod replicas map to the same workload node
	workloadName := s.getWorkloadName(app)

	// Build unique key using the 6-part format keyed on cloud_provider (not observer):
	// {cloud_provider}:{cloud_account_id}:{region}:{NodeType}:{namespace}:{name}
	// For eBPF-observed K8s workloads cloud_provider is "k8s" so the same node merges
	// with the k8s_source's view of the resource. The observer ("ebpf") is recorded
	// separately on node.Source.
	uniqueKey := core.BuildUniqueKey(
		core.DeriveCloudProvider("ebpf", nodeType),
		cloudAccount.CloudAccountID, // Use cloud account ID (UUID) for stability
		"",                          // eBPF doesn't have region info
		nodeType,
		app.Id.Namespace,
		workloadName, // Use workload name, not pod name
	)

	properties := make(map[string]interface{})
	properties["name"] = workloadName // Use workload name for consistency
	properties["kind"] = app.Id.Kind

	if app.Id.Namespace != "" {
		properties["namespace"] = app.Id.Namespace
	}

	if app.Category.Category != "" {
		properties["category"] = app.Category.Category
	}

	// Add labels
	for k, v := range app.Labels {
		properties[fmt.Sprintf("label_%s", k)] = v
	}

	// Add instance information
	if len(app.Instances) > 0 {
		properties["instance_count"] = len(app.Instances)
	}

	if app.DesiredInstances > 0 {
		properties["desired_instances"] = app.DesiredInstances
	}

	if app.FailedInstances > 0 {
		properties["failed_instances"] = app.FailedInstances
	}

	// Add health information
	properties["is_healthy"] = app.IsHealthy
	if app.HealthReason != "" {
		properties["health_reason"] = app.HealthReason
	}

	if len(app.Type) > 0 {
		// Extract and normalize primary language
		if language := core.GetPrimaryLanguage(app.Type); language != "" {
			properties["language"] = language
		}
	}

	// Add subtype property for eBPF application
	properties["subtype"] = app.Id.Kind

	node := core.NewNode(
		nodeType,
		uniqueKey,
		properties,
		tenantID,
		cloudAccount.CloudAccountID,
		"ebpf",
	)

	s.logger.Debug("created new node for application",
		"node_type", nodeType,
		"unique_key", uniqueKey,
		"name", app.Id.Name)

	return node
}

// createExternalServiceNode creates a node for an external service
func (s *EbpfFlowSource) createExternalServiceNode(
	id core.ServiceApplicationId,
	tenantID string,
	cloudAccount core.K8sAccount,
) *core.DbNode {
	// External services key on cloud_provider="external" so eBPF and traces
	// observations of the same external endpoint merge into a single node.
	uniqueKey := core.BuildUniqueKey(
		core.DeriveCloudProvider("ebpf", core.NodeTypeExternalService),
		cloudAccount.CloudAccountID, // Use cloud account ID (UUID) for stability
		"",                          // External services have no region
		core.NodeTypeExternalService,
		id.Namespace,
		id.Name,
	)

	properties := make(map[string]interface{})
	properties["name"] = id.Name
	properties["kind"] = "ExternalService"
	properties["is_external"] = true
	properties["subtype"] = "ExternalService"

	if id.Namespace != "" {
		properties["namespace"] = id.Namespace
	}

	node := core.NewNode(
		core.NodeTypeExternalService,
		uniqueKey,
		properties,
		tenantID,
		cloudAccount.CloudAccountID,
		"ebpf",
	)

	s.logger.Debug("created external service node",
		"unique_key", uniqueKey,
		"name", id.Name)

	return node
}

// matchByApplicationID attempts to match a ServiceApplicationId against existing graph nodes
// using the same 4-strategy approach as matchApplicationToNode, but without requiring a full
// ServiceApplication. Used when an upstream/downstream is not found in the eBPF service map.
func (s *EbpfFlowSource) matchByApplicationID(
	id core.ServiceApplicationId,
	K8sAccountID string,
) (*core.DbNode, error) {
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil, fmt.Errorf("node matcher not initialized")
	}

	nodeType := s.inferNodeType(id.Kind)

	// For non-node kinds, apply workload name extraction (strips pod hash suffixes)
	name := id.Name
	if strings.ToLower(id.Kind) != "node" {
		tmpApp := &core.ServiceApplication{Id: id}
		name = s.getWorkloadName(tmpApp)
	}
	if name == "" {
		return nil, fmt.Errorf("empty name for application ID: %v", id)
	}

	// Strategy 1: namespace + name + NodeType, same account
	if id.Namespace != "" {
		result, err := matcher.FindNode(MatchCriteria{
			AccountID: K8sAccountID,
			NodeType:  nodeType,
			PropertyMatches: []PropertyMatch{
				{PropertyPath: "namespace", Value: id.Namespace, MatchType: core.MatchTypeExact, CaseSensitive: false},
				{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
			},
		})
		if err == nil && result.Matched {
			return result.Node, nil
		}
	}

	// Strategy 2: name + NodeType, same account
	{
		result, err := matcher.FindNode(MatchCriteria{
			AccountID: K8sAccountID,
			NodeType:  nodeType,
			PropertyMatches: []PropertyMatch{
				{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
			},
		})
		if err == nil && result.Matched {
			return result.Node, nil
		}
	}

	// Strategy 3: namespace + name + NodeType, any account
	if id.Namespace != "" {
		result, err := matcher.FindNode(MatchCriteria{
			NodeType: nodeType,
			PropertyMatches: []PropertyMatch{
				{PropertyPath: "namespace", Value: id.Namespace, MatchType: core.MatchTypeExact, CaseSensitive: false},
				{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
			},
		})
		if err == nil && result.Matched {
			return result.Node, nil
		}
	}

	// Strategy 4: name + NodeType, any account
	{
		result, err := matcher.FindNode(MatchCriteria{
			NodeType: nodeType,
			PropertyMatches: []PropertyMatch{
				{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
			},
		})
		if err == nil && result.Matched {
			return result.Node, nil
		}
	}

	return nil, fmt.Errorf("no matching node found for application ID: %s (kind: %s)", name, id.Kind)
}

// createNodeForApplicationID creates a properly-typed node from a ServiceApplicationId
// when no full ServiceApplication is available (e.g., upstream/downstream not in service map).
func (s *EbpfFlowSource) createNodeForApplicationID(
	id core.ServiceApplicationId,
	tenantID string,
	cloudAccount core.K8sAccount,
) *core.DbNode {
	nodeType := s.inferNodeType(id.Kind)

	// For non-node kinds apply workload name extraction
	name := id.Name
	if strings.ToLower(id.Kind) != "node" {
		tmpApp := &core.ServiceApplication{Id: id}
		name = s.getWorkloadName(tmpApp)
	}

	uniqueKey := core.BuildUniqueKey(
		core.DeriveCloudProvider("ebpf", nodeType),
		cloudAccount.CloudAccountID,
		"",
		nodeType,
		id.Namespace,
		name,
	)

	properties := make(map[string]interface{})
	properties["name"] = name
	properties["kind"] = id.Kind
	properties["subtype"] = id.Kind
	if id.Namespace != "" {
		properties["namespace"] = id.Namespace
	}
	// Store the original CRD kind for filtering
	if nodeType == core.NodeTypeCRD {
		properties["crd_kind"] = id.Kind
	}

	node := core.NewNode(
		nodeType,
		uniqueKey,
		properties,
		tenantID,
		cloudAccount.CloudAccountID,
		"ebpf",
	)

	s.logger.Debug("created new node for application ID",
		"node_type", nodeType,
		"unique_key", uniqueKey,
		"kind", id.Kind,
		"name", name)

	return node
}

// inferNodeType infers the knowledge graph node type from the application kind
func (s *EbpfFlowSource) inferNodeType(kind string) core.NodeType {
	kindMap := map[string]core.NodeType{
		"service":                core.NodeTypeService,
		"deployment":             core.NodeTypeWorkload,
		"statefulset":            core.NodeTypeWorkload,
		"daemonset":              core.NodeTypeWorkload,
		"runner":                 core.NodeTypeWorkload,
		"external":               core.NodeTypeWorkload, // pod-like names; getWorkloadName strips hash suffix
		"job":                    core.NodeTypeJob,
		"cronjob":                core.NodeTypeCronJob,
		"database":               core.NodeTypeDatabase,
		"externalservice":        core.NodeTypeExternalService,
		"node":                   core.NodeTypeNode, // K8s worker nodes
		"dynakube":               core.NodeTypeCRD,  // Dynatrace operator CRD
		"vmalert":                core.NodeTypeCRD,  // VictoriaMetrics CRD
		"opentelemetrycollector": core.NodeTypeCRD,  // OTel operator CRD
	}

	if nodeType, ok := kindMap[strings.ToLower(kind)]; ok {
		return nodeType
	}

	// Default to Service for unknown kinds
	return core.NodeTypeService
}

// buildEdgeProperties builds edge properties from downstream link
func (s *EbpfFlowSource) buildEdgeProperties(
	downstream *core.DownstreamLink,
	sourceApp *core.ServiceApplication,
	destApp *core.ServiceApplication,
) map[string]interface{} {
	properties := make(map[string]interface{})

	// Add performance metrics
	if downstream.Latency > 0 {
		properties["latency_ms"] = downstream.Latency
	}

	// Add protocol information
	if downstream.Protocol != "" && downstream.Protocol != "Unknown" {
		properties["protocol"] = downstream.Protocol
	}

	// Add source metadata
	properties["source_service"] = sourceApp.Id.Name
	properties["source_kind"] = sourceApp.Id.Kind
	if sourceApp.Id.Namespace != "" {
		properties["source_namespace"] = sourceApp.Id.Namespace
	}

	// Add destination metadata (if available)
	if destApp != nil {
		properties["dest_service"] = destApp.Id.Name
		properties["dest_kind"] = destApp.Id.Kind
		if destApp.Id.Namespace != "" {
			properties["dest_namespace"] = destApp.Id.Namespace
		}

		// Add service category if available
		if destApp.Category.Category != "" {
			properties["dest_category"] = destApp.Category.Category
		}
	} else {
		properties["dest_service"] = downstream.Id.Name
		properties["dest_kind"] = downstream.Id.Kind
		if downstream.Id.Namespace != "" {
			properties["dest_namespace"] = downstream.Id.Namespace
		}
	}

	// Add source category if available
	if sourceApp.Category.Category != "" {
		properties["source_category"] = sourceApp.Category.Category
	}

	// Add status if available
	if downstream.Status > 0 {
		properties["status"] = downstream.Status
	}

	return properties
}

// buildUpstreamEdgeProperties builds edge properties from upstream link
func (s *EbpfFlowSource) buildUpstreamEdgeProperties(
	upstream *core.UpstreamLink,
	upstreamApp *core.ServiceApplication,
	destApp *core.ServiceApplication,
) map[string]interface{} {
	properties := make(map[string]interface{})

	// Add performance metrics
	if upstream.Latency > 0 {
		properties["latency_ms"] = upstream.Latency
	}

	if upstream.Weight > 0 {
		properties["weight"] = upstream.Weight
	}

	// Add protocol information
	if upstream.Protocol != "" && upstream.Protocol != "Unknown" {
		properties["protocol"] = upstream.Protocol
	}

	// Add destination metadata
	properties["dest_service"] = destApp.Id.Name
	properties["dest_kind"] = destApp.Id.Kind
	if destApp.Id.Namespace != "" {
		properties["dest_namespace"] = destApp.Id.Namespace
	}

	// Add source metadata (from upstream ID string)
	upstreamID := s.parseUpstreamID(upstream.Id)
	if upstreamID != nil {
		properties["source_service"] = upstreamID.Name
		properties["source_kind"] = upstreamID.Kind
		if upstreamID.Namespace != "" {
			properties["source_namespace"] = upstreamID.Namespace
		}
	}

	// Add upstream app metadata if available
	if upstreamApp != nil {
		if upstreamApp.Category.Category != "" {
			properties["source_category"] = upstreamApp.Category.Category
		}
	}

	// Add destination category if available
	if destApp.Category.Category != "" {
		properties["dest_category"] = destApp.Category.Category
	}

	// Add status if available
	if upstream.Status > 0 {
		properties["status"] = upstream.Status
	}

	return properties
}

// parseUpstreamID parses the upstream ID string (format: "namespace:kind:name" or ":kind:name")
func (s *EbpfFlowSource) parseUpstreamID(id string) *core.ServiceApplicationId {
	parts := strings.Split(id, ":")
	if len(parts) < 2 {
		return nil
	}

	if len(parts) == 2 {
		// Format: "kind:name"
		return &core.ServiceApplicationId{
			Kind: parts[0],
			Name: parts[1],
		}
	}

	// Format: "namespace:kind:name"
	return &core.ServiceApplicationId{
		Namespace: parts[0],
		Kind:      parts[1],
		Name:      parts[2],
	}
}
