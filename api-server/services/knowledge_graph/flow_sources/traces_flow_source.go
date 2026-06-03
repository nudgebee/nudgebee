package flow_sources

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"nudgebee/services/traces"
	"strings"
	"sync"
	"time"
)

// isRawIPName reports whether the given name is a raw IP address or IP:port —
// such names create opaque ExternalService leaves that cannot be enriched to
// real cloud resources, so we skip them at node creation. Empty names return
// false (callers handle empty separately).
func isRawIPName(name string) bool {
	if name == "" {
		return false
	}
	host := name
	if h, _, err := net.SplitHostPort(name); err == nil {
		host = h
	}
	return net.ParseIP(host) != nil
}

// K8s DNS suffix constants
const (
	k8sSvcClusterLocalSuffix = ".svc.cluster.local"
	k8sPodClusterLocalSuffix = ".pod.cluster.local"
	k8sSvcSuffix             = ".svc"
)

// knownK8sNamespaces contains common K8s namespaces for service detection
var knownK8sNamespaces = []string{
	"default", "kube-system", "kube-public", "kube-node-lease",
	"monitoring", "logging", "istio-system", "ingress-nginx",
	"cert-manager", "flux-system", "argocd", "prometheus",
}

func init() {
	// Register the traces flow source factory in the global registry
	RegisterFlowSourceFactory(
		"traces",
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewTracesFlowSource(logger), nil
		},
		"Distributed tracing flow source that enriches graph with trace-based service relationships",
		string(core.FlowSourceCategoryTracing),
	)
}

// TracesFlowSource creates flow relationships from distributed traces
// Pattern:
// 1. Get traces from traces module (traces.FetchTracesAndBuildServiceMap)
// 2. Convert service map to knowledge graph (ConvertServiceMapToGraph)
// NOTE: External service enrichment is done centrally in core/service.go after all flow sources complete
type TracesFlowSource struct {
	*BaseFlowSource
}

// NewTracesFlowSource creates a new traces flow source
func NewTracesFlowSource(logger *slog.Logger) *TracesFlowSource {
	base := NewBaseFlowSource(
		"traces",
		core.FlowSourceCategoryTracing,
		true,
		logger,
	)

	return &TracesFlowSource{
		BaseFlowSource: base,
	}
}

// Validate validates the traces flow source configuration
func (s *TracesFlowSource) Validate() error {
	return s.BaseFlowSource.Validate()
}

// GetSourceCategory returns the source category
func (s *TracesFlowSource) GetSourceCategory() core.FlowSourceCategory {
	return core.FlowSourceCategoryTracing
}

// BuildFlowRelationships builds flow relationships from traces
// It queries all K8s accounts with connected agents and creates edges based on trace data
func (s *TracesFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	ctx := reqCtx.GetContext()
	startTime := TimeNow()
	defer s.TrackBuildTime(startTime)

	s.logger.Info("building flow relationships from traces",
		"source", s.GetName(),
		"tenant_id", req.TenantID,
		"existing_nodes", len(req.ExistingNodes))

	// Initialize node matcher with existing nodes
	s.InitializeNodeMatcher(req.ExistingNodes)

	edges := make([]*core.DbEdge, 0)
	nodes := make([]*core.DbNode, 0)

	// Build the ClusterIP + Node-IP resolvers once per build (not per account).
	// req.ExistingNodes is constant across the per-account loop below — both
	// resolvers read only that, no I/O — so rebuilding inside processK8sAccount
	// would be wasted work.
	ipResolver := NewK8sServiceIPResolver(req.ExistingNodes)
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

	// Determine time range
	var queryStartTime, queryEndTime time.Time
	if req.TimeRange != nil {
		queryStartTime = req.TimeRange.StartTime
		queryEndTime = req.TimeRange.EndTime
	} else {
		// Default to last 15 minutes
		queryEndTime = time.Now()
		queryStartTime = queryEndTime.Add(-15 * time.Minute)
	}

	// Process each K8s account
	for _, account := range k8sAccounts {
		s.logger.Info("processing K8s account for traces flow source",
			"cloud_account_id", account.CloudAccountID,
			"tenant", account.Tenant)

		// podIPResolver is built per-account because kube_pod_info is fetched from
		// the per-account Prometheus relay. Mirrors the wiring in
		// ebpf_flow_source.go's processK8sAccount.
		podIPResolver := NewPodIPResolver(account.CloudAccountID, req.ExistingNodes, s.logger)
		accountEdges, accountNodes, err := s.processK8sAccount(ctx, reqCtx, req, account, queryStartTime, queryEndTime, ipResolver, podIPResolver, nodeIPResolver)
		if err != nil {
			s.logger.Error("failed to process K8s account",
				"cloud_account_id", account.CloudAccountID,
				"error", err)
			s.IncrementErrorCount()
			continue
		}

		edges = append(edges, accountEdges...)
		nodes = append(nodes, accountNodes...)
	}

	s.LogMetrics()

	s.logger.Info("completed building flow relationships from traces",
		"total_edges_created", len(edges),
		"total_nodes_created", len(nodes),
		"k8s_accounts_processed", len(k8sAccounts),
		"duration", time.Since(startTime).Seconds())

	return edges, nodes, nil
}

func (s *TracesFlowSource) getServicesForSync(ctx context.Context, tenantID, cloudAccountID string, limit int) ([]string, error) {
	query := `
		SELECT DISTINCT 
			attribute_value->>'service_name' as service_name,
			MIN(last_sync_at) as min_last_sync_at
		FROM knowledge_graph_metadata 
		WHERE tenant_id = $1 
		AND cloud_account_id = $2 
		AND attribute_type = 'service'
		AND attribute_value->>'service_name' IS NOT NULL
		GROUP BY attribute_value->>'service_name'
		ORDER BY min_last_sync_at ASC NULLS FIRST, service_name
		LIMIT $3
	`

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}
	rows, err := dbManager.Query(query, tenantID, cloudAccountID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query services for sync: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var services []string
	for rows.Next() {
		var serviceName string
		var minLastSyncAt any // Can be nil or timestamp
		if err := rows.Scan(&serviceName, &minLastSyncAt); err != nil {
			return nil, fmt.Errorf("failed to scan service name: %w", err)
		}
		if serviceName != "" {
			services = append(services, serviceName)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating service rows: %w", err)
	}

	return services, nil
}

// processK8sAccount processes a single K8s account
// Pattern (same as ebpf_flow_source.go):
// 1. traces.FetchTracesAndBuildServiceMap() - Get service map from traces module
// 2. For each application, try to match to existing nodes first
// 3. Only create external service nodes when no match is found
// NOTE: EnrichExternalServices and LinkLoadBalancersToBackendServices are now called
// centrally in core/service.go after ALL flow sources complete
func (s *TracesFlowSource) processK8sAccount(
	ctx context.Context,
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
	account core.K8sAccount,
	startTime, endTime time.Time,
	ipResolver *K8sServiceIPResolver,
	podIPResolver *PodIPResolver,
	nodeIPResolver *K8sNodeIPResolver,
) ([]*core.DbEdge, []*core.DbNode, error) {

	s.logger.Info("processK8sAccount - using service map approach",
		"tenant_id", account.Tenant,
		"cloud_account_id", account.CloudAccountID,
		"start_time", startTime,
		"end_time", endTime)

	var serviceMaps []*traces.ServiceMap

	// STEP 1: First fetch service map for ALL services (without filter)
	params := traces.TraceQueryParams{
		AccountID: account.CloudAccountID,
		StartTime: endTime.Add(-2 * time.Hour),
		EndTime:   endTime,
	}

	s.logger.Info("calling traces.FetchTracesAndBuildServiceMap for all services",
		"account_id", account.CloudAccountID)

	serviceMap, err := traces.FetchTracesAndBuildServiceMap(reqCtx, params)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to fetch traces and build service map: %w", err)
	}

	if serviceMap != nil && len(serviceMap.Applications) > 0 {
		serviceMaps = append(serviceMaps, serviceMap)
		s.logger.Info("received service map for all services",
			"applications", len(serviceMap.Applications),
			"account_id", account.CloudAccountID)
	}

	// STEP 2: Get services that need syncing from knowledge_graph_metadata
	const serviceSyncLimit = 50
	servicesToSync, err := s.getServicesForSync(ctx, req.TenantID, account.CloudAccountID, serviceSyncLimit)
	if err != nil {
		s.logger.Warn("failed to get services for sync",
			"error", err,
			"cloud_account_id", account.CloudAccountID)
	}

	// STEP 3: Fetch service maps for each service that needs syncing (parallel)
	if len(servicesToSync) > 0 {
		s.logger.Info("fetching service maps for services that need syncing",
			"count", len(servicesToSync),
			"services", servicesToSync,
			"cloud_account_id", account.CloudAccountID)

		const maxConcurrentFetches = 5
		sem := make(chan struct{}, maxConcurrentFetches)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, serviceName := range servicesToSync {
			wg.Add(1)
			go func(svcName string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				serviceParams := traces.TraceQueryParams{
					AccountID:    account.CloudAccountID,
					StartTime:    endTime.Add(-2 * time.Hour),
					EndTime:      endTime,
					WorkloadName: svcName,
				}

				s.logger.Debug("calling traces.FetchTracesAndBuildServiceMap for service",
					"service_name", svcName,
					"account_id", account.CloudAccountID)

				svcServiceMap, svcErr := traces.FetchTracesAndBuildServiceMap(reqCtx, serviceParams)
				if svcErr != nil {
					s.logger.Warn("failed to fetch service map for service",
						"service_name", svcName,
						"error", svcErr)
					return
				}

				if svcServiceMap != nil && len(svcServiceMap.Applications) > 0 {
					mu.Lock()
					serviceMaps = append(serviceMaps, svcServiceMap)
					mu.Unlock()
					s.logger.Debug("received service map for service",
						"service_name", svcName,
						"applications", len(svcServiceMap.Applications))
				}
			}(serviceName)
		}
		wg.Wait()
	}

	// STEP 4: Merge all service maps
	if len(serviceMaps) == 0 {
		s.logger.Info("no service maps returned from traces",
			"account_id", account.CloudAccountID)
		return []*core.DbEdge{}, []*core.DbNode{}, nil
	}

	mergedServiceMap := s.mergeServiceMaps(serviceMaps)

	s.logger.Info("merged service maps from traces",
		"service_maps_count", len(serviceMaps),
		"total_applications", len(mergedServiceMap.Applications),
		"account_id", account.CloudAccountID)

	// STEP 5: Process merged service map following ebpf pattern
	edges := make([]*core.DbEdge, 0)
	newNodes := make([]*core.DbNode, 0)
	externalServiceNodes := make(map[string]*core.DbNode)

	// ipResolver is built once per BuildFlowRelationships invocation and
	// passed in — used to short-circuit raw-IP destinations into K8sService
	// CALLS edges via ResolveIPToK8sService (which owns port stripping and
	// the special-IP skip list).

	// Build lookup map for applications by name
	appLookup := make(map[string]*traces.ServiceApplication)
	for i := range mergedServiceMap.Applications {
		app := &mergedServiceMap.Applications[i]
		appLookup[fmt.Sprintf("%s:%s", app.Id.Kind, app.Id.Name)] = app
	}

	// Track unmatched services for analysis
	unmatchedSources := make(map[string]int)
	unmatchedDestinations := make(map[string]int)

	// Process each application and its connections (same pattern as ebpf)
	for i := range mergedServiceMap.Applications {
		app := &mergedServiceMap.Applications[i]

		// Try to match the source application to an existing node
		sourceNode, sourceErr := s.matchServiceApplicationToNode(app, account.CloudAccountID)
		if sourceErr != nil {
			// No match found - create new node for this application
			sourceName := app.Id.Name
			s.logger.Debug("source service not found in knowledge graph, creating new node",
				"service", sourceName,
				"kind", app.Id.Kind,
				"namespace", app.Id.Namespace)

			sourceNode = s.createNodeForServiceApplication(app, req.TenantID, account)
			if sourceNode == nil {
				// k8s-authoritative type with no match — skip this application.
				continue
			}
			newNodes = append(newNodes, sourceNode)
			unmatchedSources[sourceName]++
		} else {
			// Enrich matched node with trace metadata
			s.enrichNodeWithTraceMetadata(sourceNode, app)
		}

		// Process upstream connections (services this app calls)
		for _, upstream := range app.Upstreams {
			targetName, targetKind := traces.ParseUpstreamId(upstream.Id)
			if targetName == "" {
				continue
			}

			var upstreamNode *core.DbNode

			// Check if upstream exists in applications list
			lookupKey := fmt.Sprintf("%s:%s", targetKind, targetName)
			if upstreamApp, exists := appLookup[lookupKey]; exists {
				// Try to match upstream application to existing node
				var upstreamErr error
				upstreamNode, upstreamErr = s.matchServiceApplicationToNode(upstreamApp, account.CloudAccountID)
				if upstreamErr != nil {
					// Create new node for unmatched upstream
					s.logger.Debug("upstream service not found in knowledge graph, creating new node",
						"service", targetName,
						"kind", targetKind)

					upstreamNode = s.createNodeForServiceApplication(upstreamApp, req.TenantID, account)
					if upstreamNode == nil {
						// k8s-authoritative type with no match — drop this edge.
						continue
					}
					newNodes = append(newNodes, upstreamNode)
					unmatchedDestinations[targetName]++
				} else {
					// Enrich matched node with trace metadata
					s.enrichNodeWithTraceMetadata(upstreamNode, upstreamApp)
				}
			} else {
				// Upstream not found in applications list - treat as external service.
				// Source-level short-circuit: if targetName is a raw IP, try to
				// resolve it (ClusterIP first, then pod IP) before falling back to
				// an orphan ExternalService. Loopback / link-local / metadata IPs
				// get dropped entirely (no useful topology signal). Provenance
				// gets stamped on the edge below.
				resolvedNode, resolvedFromIP, resolvedReason, resolutionSource, resolvedOK :=
					resolveIPNamedExternalService(targetName, stringProp(sourceNode, "cluster"), ipResolver, podIPResolver, nodeIPResolver)
				switch {
				case resolvedOK:
					upstreamNode = resolvedNode
				case IsSpecialIPName(targetName):
					// Loopback / link-local / metadata IPs carry no topology signal.
					// Drop the edge instead of creating an orphan ExternalService.
					continue
				default:
					// First try to match by name/kind to existing nodes
					upstreamNode = s.tryMatchExternalService(targetName, targetKind, account.CloudAccountID)
					if upstreamNode == nil {
						// No match found - create external service node
						upstreamNode = s.createExternalServiceNode(targetName, targetKind, req.TenantID, account)
						if upstreamNode == nil {
							// Empty or raw-IP name — skip the edge entirely.
							continue
						}
						newNodes = append(newNodes, upstreamNode)
						externalServiceNodes[targetName] = upstreamNode
						unmatchedDestinations[targetName]++
					}
				}

				// Create edge: source -> upstream (this service calls upstream)
				edge := s.createEdgeFromUpstream(sourceNode, upstreamNode, &upstream, req.TenantID, account.CloudAccountID)
				if edge != nil {
					if resolvedOK {
						edge.Properties["resolved_from_ip"] = resolvedFromIP
						edge.Properties["resolution_source"] = resolutionSource
						edge.Properties["resolution_reason"] = resolvedReason
					}
					edges = append(edges, edge)
				}
				continue
			}

			// Create edge: source -> upstream (this service calls upstream)
			edge := s.createEdgeFromUpstream(sourceNode, upstreamNode, &upstream, req.TenantID, account.CloudAccountID)
			if edge != nil {
				edges = append(edges, edge)
			}
		}

		// Process downstream connections (services that call this app)
		for _, downstream := range app.Downstreams {
			downstreamName := downstream.Id.Name
			downstreamKind := downstream.Id.Kind

			var downstreamNode *core.DbNode

			// Check if downstream exists in applications list
			lookupKey := fmt.Sprintf("%s:%s", downstreamKind, downstreamName)
			if downstreamApp, exists := appLookup[lookupKey]; exists {
				// Try to match downstream application to existing node
				var downstreamErr error
				downstreamNode, downstreamErr = s.matchServiceApplicationToNode(downstreamApp, account.CloudAccountID)
				if downstreamErr != nil {
					// Create new node for unmatched downstream
					s.logger.Debug("downstream service not found in knowledge graph, creating new node",
						"service", downstreamName,
						"kind", downstreamKind)

					downstreamNode = s.createNodeForServiceApplication(downstreamApp, req.TenantID, account)
					if downstreamNode == nil {
						// k8s-authoritative type with no match — drop this edge.
						continue
					}
					newNodes = append(newNodes, downstreamNode)
					unmatchedSources[downstreamName]++
				} else {
					// Enrich matched node with trace metadata
					s.enrichNodeWithTraceMetadata(downstreamNode, downstreamApp)
				}
			} else {
				// Downstream not found in applications list - treat as external service.
				// Source-level short-circuit: if downstreamName is a raw IP, try to
				// resolve it (ClusterIP first, then pod IP) before falling back to
				// an orphan ExternalService. Loopback / link-local / metadata IPs
				// get dropped entirely (no useful topology signal). For downstream
				// edges the caller cluster is the downstream node's own cluster (the
				// service making the call into this app) — unknown at this point,
				// so we pass "" and rely on the resolvers' global-unique fallback.
				resolvedNode, resolvedFromIP, resolvedReason, resolutionSource, resolvedOK :=
					resolveIPNamedExternalService(downstreamName, "", ipResolver, podIPResolver, nodeIPResolver)
				switch {
				case resolvedOK:
					downstreamNode = resolvedNode
				case IsSpecialIPName(downstreamName):
					// Loopback / link-local / metadata IPs carry no topology signal.
					// Drop the edge instead of creating an orphan ExternalService.
					continue
				default:
					// First try to match by name/kind to existing nodes
					downstreamNode = s.tryMatchExternalService(downstreamName, downstreamKind, account.CloudAccountID)
					if downstreamNode == nil {
						// No match found - create external service node
						downstreamNode = s.createExternalServiceNode(downstreamName, downstreamKind, req.TenantID, account)
						if downstreamNode == nil {
							// Empty or raw-IP name — skip the edge entirely.
							continue
						}
						newNodes = append(newNodes, downstreamNode)
						externalServiceNodes[downstreamName] = downstreamNode
						unmatchedSources[downstreamName]++
					}
				}

				// Create edge: downstream -> source (downstream calls this service)
				edge := s.createEdgeFromDownstream(downstreamNode, sourceNode, &downstream, req.TenantID, account.CloudAccountID)
				if edge != nil {
					if resolvedOK {
						edge.Properties["resolved_from_ip"] = resolvedFromIP
						edge.Properties["resolution_source"] = resolutionSource
						edge.Properties["resolution_reason"] = resolvedReason
					}
					edges = append(edges, edge)
				}
				continue
			}

			// Create edge: downstream -> source (downstream calls this service)
			edge := s.createEdgeFromDownstream(downstreamNode, sourceNode, &downstream, req.TenantID, account.CloudAccountID)
			if edge != nil {
				edges = append(edges, edge)
			}
		}
	}

	// Log unmatched services for analysis
	if len(unmatchedSources) > 0 || len(unmatchedDestinations) > 0 {
		s.logger.Info("created new nodes for unmatched services in traces",
			"cloud_account_id", account.CloudAccountID,
			"new_nodes_count", len(newNodes),
			"unmatched_sources_count", len(unmatchedSources),
			"unmatched_destinations_count", len(unmatchedDestinations))
	}

	s.logger.Info("completed processK8sAccount",
		"nodes", len(newNodes),
		"edges", len(edges),
		"external_services", len(externalServiceNodes))

	return edges, newNodes, nil
}

// ParsedK8sServiceDNS contains parsed components of a K8s service DNS name
type ParsedK8sServiceDNS struct {
	ServiceName   string // The K8s service name (e.g., "ocean-service")
	Namespace     string // The K8s namespace (e.g., "ocean-service-new")
	ClusterDomain string // The cluster domain (e.g., "svc.cluster.local")
	IsPodDNS      bool   // True if this is a pod DNS name (has pod prefix)
	PodName       string // Pod name if IsPodDNS is true
}

// parseK8sServiceDNS parses a K8s internal DNS name into its components
// Handles formats:
//   - <service>.<namespace>.svc.cluster.local (e.g., "ocean-service.ocean-service-new.svc.cluster.local")
//   - <service>.<namespace>.svc (e.g., "redis.default.svc")
//   - <service>.<namespace> (short form, e.g., "postgres.database")
//   - <pod>.<service>.<namespace>.svc.cluster.local (headless service pod DNS)
//
// Returns nil if the name is not a K8s internal DNS pattern
func parseK8sServiceDNS(name string) *ParsedK8sServiceDNS {
	if name == "" {
		return nil
	}

	nameLower := strings.ToLower(name)

	// Check if it's a K8s internal DNS pattern
	if !isK8sInternalDNS(nameLower) {
		return nil
	}

	parsed := &ParsedK8sServiceDNS{}

	// Format 1: <service>.<namespace>.svc.cluster.local
	if strings.HasSuffix(nameLower, k8sSvcClusterLocalSuffix) {
		prefix := strings.TrimSuffix(nameLower, k8sSvcClusterLocalSuffix)
		parts := strings.Split(prefix, ".")
		parsed.ClusterDomain = k8sSvcClusterLocalSuffix[1:] // Remove leading dot

		if len(parts) == 2 {
			// Standard format: service.namespace
			parsed.ServiceName = parts[0]
			parsed.Namespace = parts[1]
			return parsed
		} else if len(parts) == 3 {
			// Headless service pod DNS: pod.service.namespace
			parsed.IsPodDNS = true
			parsed.PodName = parts[0]
			parsed.ServiceName = parts[1]
			parsed.Namespace = parts[2]
			return parsed
		} else if len(parts) >= 1 {
			// Fallback: just take first part as service name
			parsed.ServiceName = parts[0]
			if len(parts) > 1 {
				parsed.Namespace = parts[1]
			}
			return parsed
		}
	}

	// Format 2: <service>.<namespace>.svc (without cluster.local)
	if strings.Contains(nameLower, k8sSvcSuffix) && !strings.HasSuffix(nameLower, k8sSvcClusterLocalSuffix) {
		idx := strings.Index(nameLower, k8sSvcSuffix)
		prefix := nameLower[:idx]
		parts := strings.Split(prefix, ".")

		if len(parts) >= 2 {
			parsed.ServiceName = parts[0]
			parsed.Namespace = parts[1]
			parsed.ClusterDomain = nameLower[idx+1:]
			return parsed
		} else if len(parts) == 1 {
			parsed.ServiceName = parts[0]
			parsed.ClusterDomain = nameLower[idx+1:]
			return parsed
		}
	}

	// Format 3: <service>.<namespace> (short form - check for known K8s namespaces)
	parts := strings.Split(nameLower, ".")
	if len(parts) == 2 {
		// Check if second part looks like a K8s namespace
		for _, ns := range knownK8sNamespaces {
			if parts[1] == ns {
				parsed.ServiceName = parts[0]
				parsed.Namespace = parts[1]
				return parsed
			}
		}
		// Even if not a known namespace, if it looks like service.namespace pattern
		// and doesn't contain dots/special chars that suggest external domain
		if !strings.Contains(parts[1], "-") || len(parts[1]) < 64 {
			parsed.ServiceName = parts[0]
			parsed.Namespace = parts[1]
			return parsed
		}
	}

	// Format 4: pod.cluster.local pattern
	if strings.HasSuffix(nameLower, k8sPodClusterLocalSuffix) {
		prefix := strings.TrimSuffix(nameLower, k8sPodClusterLocalSuffix)
		parts := strings.Split(prefix, ".")
		parsed.ClusterDomain = k8sPodClusterLocalSuffix[1:] // Remove leading dot
		parsed.IsPodDNS = true

		if len(parts) >= 2 {
			// Format: <pod-ip-dashed>.<namespace>.pod.cluster.local
			parsed.PodName = parts[0]
			parsed.Namespace = parts[1]
			return parsed
		}
	}

	return nil
}

// isK8sInternalDNS checks if a hostname is a Kubernetes internal DNS name
func isK8sInternalDNS(hostname string) bool {
	if hostname == "" {
		return false
	}

	hostnameLower := strings.ToLower(hostname)

	// Quick suffix checks
	if strings.HasSuffix(hostnameLower, k8sSvcClusterLocalSuffix) ||
		strings.HasSuffix(hostnameLower, k8sPodClusterLocalSuffix) ||
		strings.HasSuffix(hostnameLower, k8sSvcSuffix) {
		return true
	}

	// Check for .svc. pattern in the middle
	if strings.Contains(hostnameLower, ".svc.") {
		return true
	}

	// Check for known K8s namespace patterns (e.g., ".default.", ".kube-system.")
	for _, ns := range knownK8sNamespaces {
		if strings.Contains(hostnameLower, "."+ns+".") {
			return true
		}
	}

	// Check for short form: service.namespace where namespace is known
	parts := strings.Split(hostnameLower, ".")
	if len(parts) == 2 {
		for _, ns := range knownK8sNamespaces {
			if parts[1] == ns {
				return true
			}
		}
	}

	return false
}

// matchK8sInternalDNSToNode attempts to match a K8s internal DNS name to existing nodes
// Parses names like "ocean-service.ocean-service-new.svc.cluster.local" and matches by
// extracted service name and namespace
func (s *TracesFlowSource) matchK8sInternalDNSToNode(name, k8sAccountID string) *core.DbNode {
	parsedDNS := parseK8sServiceDNS(name)
	if parsedDNS == nil {
		return nil
	}

	s.logger.Debug("parsed K8s internal DNS name",
		"original_name", name,
		"parsed_service", parsedDNS.ServiceName,
		"parsed_namespace", parsedDNS.Namespace,
		"is_pod_dns", parsedDNS.IsPodDNS)

	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil
	}

	// Node types to try for K8s internal services - prioritize K8sService and Workload
	k8sNodeTypes := []core.NodeType{core.NodeTypeK8sService, core.NodeTypeWorkload, core.NodeTypeService}

	// Try matching with namespace + service name (same account, then any account)
	if parsedDNS.Namespace != "" && parsedDNS.ServiceName != "" {
		if node := s.matchByNamespaceAndName(matcher, parsedDNS.ServiceName, parsedDNS.Namespace, k8sAccountID, k8sNodeTypes, true); node != nil {
			s.logger.Debug("matched K8s DNS by parsed namespace and name (same account)",
				"original_name", name,
				"parsed_service", parsedDNS.ServiceName,
				"parsed_namespace", parsedDNS.Namespace,
				"node", node.UniqueKey)
			return node
		}
		if node := s.matchByNamespaceAndName(matcher, parsedDNS.ServiceName, parsedDNS.Namespace, "", k8sNodeTypes, false); node != nil {
			s.logger.Debug("matched K8s DNS by parsed namespace and name (any account)",
				"original_name", name,
				"parsed_service", parsedDNS.ServiceName,
				"parsed_namespace", parsedDNS.Namespace,
				"node", node.UniqueKey)
			return node
		}
	}

	// Try matching with service name only (same account, then any account)
	if parsedDNS.ServiceName != "" {
		if node := s.matchByNameOnly(matcher, parsedDNS.ServiceName, k8sAccountID, k8sNodeTypes, true); node != nil {
			s.logger.Debug("matched K8s DNS by parsed service name (same account)",
				"original_name", name,
				"parsed_service", parsedDNS.ServiceName,
				"node", node.UniqueKey)
			return node
		}
		if node := s.matchByNameOnly(matcher, parsedDNS.ServiceName, "", k8sNodeTypes, false); node != nil {
			s.logger.Debug("matched K8s DNS by parsed service name (any account)",
				"original_name", name,
				"parsed_service", parsedDNS.ServiceName,
				"node", node.UniqueKey)
			return node
		}
	}

	s.logger.Debug("K8s DNS parsing found no match, falling through to other strategies",
		"original_name", name,
		"parsed_service", parsedDNS.ServiceName,
		"parsed_namespace", parsedDNS.Namespace)

	return nil
}

// matchByNamespaceAndName is a helper that matches nodes by namespace and name
func (s *TracesFlowSource) matchByNamespaceAndName(
	matcher *NodeMatcher,
	serviceName, namespace, accountID string,
	nodeTypes []core.NodeType,
	filterByAccount bool,
) *core.DbNode {
	for _, nt := range nodeTypes {
		criteria := MatchCriteria{
			NodeType: nt,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "namespace",
					Value:         namespace,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
				{
					PropertyPath:  "name",
					Value:         serviceName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		}
		if filterByAccount {
			criteria.AccountID = accountID
		}
		result, err := matcher.FindNode(criteria)
		if err == nil && result.Matched {
			return result.Node
		}
	}
	return nil
}

// matchByNameOnly is a helper that matches nodes by name only
func (s *TracesFlowSource) matchByNameOnly(
	matcher *NodeMatcher,
	serviceName, accountID string,
	nodeTypes []core.NodeType,
	filterByAccount bool,
) *core.DbNode {
	for _, nt := range nodeTypes {
		criteria := MatchCriteria{
			NodeType: nt,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         serviceName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		}
		if filterByAccount {
			criteria.AccountID = accountID
		}
		result, err := matcher.FindNode(criteria)
		if err == nil && result.Matched {
			return result.Node
		}
	}
	return nil
}

// matchServiceApplicationToNode matches a traces.ServiceApplication to an existing knowledge graph node
// Follows the same pattern as matchApplicationToNode in ebpf_flow_source.go
func (s *TracesFlowSource) matchServiceApplicationToNode(
	app *traces.ServiceApplication,
	k8sAccountID string,
) (*core.DbNode, error) {
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil, fmt.Errorf("node matcher not initialized")
	}

	nodeType := s.inferNodeType(app.Id.Kind, app.Type)

	// Build list of node types to try. OTel `service.name` is a logical
	// identifier that may align with any of:
	//   - a NodeTypeService node (another traces observation of the same logical service)
	//   - a NodeTypeWorkload (the convention when service.name == deployment name; e.g. OTel demo)
	//   - a NodeTypeK8sService (when the team aligns Service object name with service.name)
	// Try each in turn before falling back to creating a stand-alone Service node.
	//
	// ExternalService observations get the same Workload+K8sService probe
	// because the trace agent sometimes mis-labels an in-cluster service
	// as external when it fails to identify it as internal.
	// Same-account strategies (1, 2) probe multi-type; cross-account
	// strategies (3, 4) stay strict on the inferred type to avoid false
	// Workload/Service collisions across clusters. See ebpf_flow_source
	// matchApplicationToNode for the rationale.
	nodeTypesToTry := []core.NodeType{nodeType}
	if nodeType == core.NodeTypeService || nodeType == core.NodeTypeExternalService {
		nodeTypesToTry = append(nodeTypesToTry, core.NodeTypeWorkload, core.NodeTypeK8sService)
	}

	// Strategy 0: Parse K8s internal DNS names and match by extracted service name + namespace
	// Handles cases like "ocean-service.ocean-service-new.svc.cluster.local" where:
	//   - Name contains full K8s DNS path
	//   - Namespace field is empty
	//   - Kind is "ExternalService" (because traces couldn't identify it as internal)
	if node := s.matchK8sInternalDNSToNode(app.Id.Name, k8sAccountID); node != nil {
		return node, nil
	}

	// Strategy 1: Match by namespace and name (same account)
	if app.Id.Namespace != "" && app.Id.Name != "" {
		for _, nt := range nodeTypesToTry {
			result, err := matcher.FindNode(MatchCriteria{
				AccountID: k8sAccountID,
				NodeType:  nt,
				PropertyMatches: []PropertyMatch{
					{
						PropertyPath:  "namespace",
						Value:         app.Id.Namespace,
						MatchType:     core.MatchTypeExact,
						CaseSensitive: false,
					},
					{
						PropertyPath:  "name",
						Value:         app.Id.Name,
						MatchType:     core.MatchTypeExact,
						CaseSensitive: false,
					},
				},
			})
			if err == nil && result.Matched {
				s.logger.Debug("matched application by namespace and name (strategy 1)",
					"namespace", app.Id.Namespace,
					"name", app.Id.Name,
					"node_type", nt,
					"node", result.Node.UniqueKey)
				return result.Node, nil
			}
		}
	}

	// Strategy 2: Exact match by name and kind (same account)
	if app.Id.Name != "" {
		for _, nt := range nodeTypesToTry {
			result, err := matcher.FindNode(MatchCriteria{
				AccountID: k8sAccountID,
				NodeType:  nt,
				PropertyMatches: []PropertyMatch{
					{
						PropertyPath:  "name",
						Value:         app.Id.Name,
						MatchType:     core.MatchTypeExact,
						CaseSensitive: false,
					},
				},
			})
			if err == nil && result.Matched {
				s.logger.Debug("matched application by name (strategy 2)",
					"name", app.Id.Name,
					"node_type", nt,
					"node", result.Node.UniqueKey)
				return result.Node, nil
			}
		}
	}

	// Strategy 3: Match by namespace and name (any account, inferred type only)
	if app.Id.Namespace != "" && app.Id.Name != "" {
		result, err := matcher.FindNode(MatchCriteria{
			NodeType: nodeType,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "namespace",
					Value:         app.Id.Namespace,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
				{
					PropertyPath:  "name",
					Value:         app.Id.Name,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by namespace and name (strategy 3)",
				"namespace", app.Id.Namespace,
				"name", app.Id.Name,
				"node_type", nodeType,
				"node", result.Node.UniqueKey)
			return result.Node, nil
		}
	}

	// Strategy 4: Exact match by name (any account, inferred type only)
	if app.Id.Name != "" {
		result, err := matcher.FindNode(MatchCriteria{
			NodeType: nodeType,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         app.Id.Name,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by name (strategy 4)",
				"name", app.Id.Name,
				"node_type", nodeType,
				"node", result.Node.UniqueKey)
			return result.Node, nil
		}
	}

	// Strategy 5: Match by service name in labels
	if serviceName, ok := app.Labels["service"]; ok && serviceName != "" {
		result, err := matcher.FindNode(MatchCriteria{
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "properties.service_name",
					Value:         serviceName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched application by service label (strategy 5)",
				"service", serviceName,
				"node", result.Node.UniqueKey)
			return result.Node, nil
		}
	}

	// Strategy 6: Match AWS hostname patterns (ELB, RDS, ElastiCache, etc.)
	// Handles hostnames like: af0cda30e9e064065bc19f0b2abcc1e9-b0011dc7589f04d1.elb.us-east-1.amazonaws.com
	if awsNode := s.matchAWSHostnameToNode(app.Id.Name, k8sAccountID); awsNode != nil {
		s.logger.Debug("matched application by AWS hostname (strategy 6)",
			"name", app.Id.Name,
			"node", awsNode.UniqueKey)
		return awsNode, nil
	}

	return nil, fmt.Errorf("no matching node found for application: %s (kind: %s)", app.Id.Name, app.Id.Kind)
}

// matchAWSHostnameToNode attempts to match an AWS hostname to existing nodes
// Handles patterns like:
//   - ELB: af0cda30e9e064065bc19f0b2abcc1e9-b0011dc7589f04d1.elb.us-east-1.amazonaws.com
//   - RDS: mydb.abc123.us-east-1.rds.amazonaws.com
//   - ElastiCache: mycluster.abc123.cache.amazonaws.com
func (s *TracesFlowSource) matchAWSHostnameToNode(name, k8sAccountID string) *core.DbNode {
	nameLower := strings.ToLower(name)

	// Check if this is an AWS hostname
	if !strings.Contains(nameLower, ".amazonaws.com") {
		return nil
	}

	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil
	}

	// Determine the AWS service type and corresponding node type
	var nodeType core.NodeType
	switch {
	case strings.Contains(nameLower, ".elb.") || strings.Contains(nameLower, "elasticloadbalancing"):
		nodeType = core.NodeTypeLoadBalancer
	case strings.Contains(nameLower, ".rds."):
		nodeType = core.NodeTypeDatabase
	case strings.Contains(nameLower, ".cache.") || strings.Contains(nameLower, ".elasticache."):
		nodeType = core.NodeTypeCache
	case strings.Contains(nameLower, ".s3."):
		nodeType = core.NodeTypeStorage
	case strings.Contains(nameLower, ".eks."):
		nodeType = core.NodeTypeCluster
	default:
		// Unknown AWS service, try generic matching
		nodeType = core.NodeTypeExternalService
	}

	// Strategy 1: Match by dns_name property (exact match)
	result, err := matcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  "dns_name",
				Value:         name,
				MatchType:     core.MatchTypeExact,
				CaseSensitive: false,
			},
		},
	})
	if err == nil && result.Matched {
		s.logger.Debug("matched AWS hostname by dns_name",
			"hostname", name,
			"node_type", nodeType,
			"node", result.Node.UniqueKey)
		return result.Node
	}

	// Strategy 2: Match by name property containing the hostname
	result, err = matcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  "name",
				Value:         name,
				MatchType:     core.MatchTypeExact,
				CaseSensitive: false,
			},
		},
	})
	if err == nil && result.Matched {
		s.logger.Debug("matched AWS hostname by name property",
			"hostname", name,
			"node_type", nodeType,
			"node", result.Node.UniqueKey)
		return result.Node
	}

	// Strategy 3: Match by dns_name property without specific node type
	result, err = matcher.FindNode(MatchCriteria{
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  "dns_name",
				Value:         name,
				MatchType:     core.MatchTypeExact,
				CaseSensitive: false,
			},
		},
	})
	if err == nil && result.Matched {
		s.logger.Debug("matched AWS hostname by dns_name (any type)",
			"hostname", name,
			"node", result.Node.UniqueKey)
		return result.Node
	}

	// Strategy 4: For ELB, try to extract identifier and match
	// ELB format: {identifier}.elb.{region}.amazonaws.com
	// or: {identifier}-{account}.{region}.elb.amazonaws.com
	if strings.Contains(nameLower, ".elb.") {
		parts := strings.Split(name, ".")
		if len(parts) >= 4 {
			identifier := parts[0] // e.g., "af0cda30e9e064065bc19f0b2abcc1e9-b0011dc7589f04d1"

			// Try matching by dns_name containing the identifier
			result, err = matcher.FindNode(MatchCriteria{
				NodeType: core.NodeTypeLoadBalancer,
				PropertyMatches: []PropertyMatch{
					{
						PropertyPath:  "dns_name",
						Value:         identifier,
						MatchType:     core.MatchTypeContains,
						CaseSensitive: false,
					},
				},
			})
			if err == nil && result.Matched {
				s.logger.Debug("matched ELB by identifier in dns_name",
					"hostname", name,
					"identifier", identifier,
					"node", result.Node.UniqueKey)
				return result.Node
			}
		}
	}

	return nil
}

// tryMatchExternalService attempts to match an external service to existing nodes
func (s *TracesFlowSource) tryMatchExternalService(
	name, kind, k8sAccountID string,
) *core.DbNode {
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil
	}

	// First, try to match K8s internal DNS names
	if node := s.matchK8sInternalDNSToNode(name, k8sAccountID); node != nil {
		s.logger.Debug("matched external service via K8s DNS parsing",
			"name", name,
			"kind", kind,
			"node", node.UniqueKey)
		return node
	}

	// Try to match by name across all node types
	result, err := matcher.FindNode(MatchCriteria{
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  "name",
				Value:         name,
				MatchType:     core.MatchTypeExact,
				CaseSensitive: false,
			},
		},
	})
	if err == nil && result.Matched {
		s.logger.Debug("matched external service to existing node",
			"name", name,
			"kind", kind,
			"node", result.Node.UniqueKey)
		return result.Node
	}

	return nil
}

// createNodeForServiceApplication creates a new node for a traces.ServiceApplication.
// Returns nil for k8s-authoritative types (Node / Namespace / Cluster); see
// isK8sAuthoritativeType in ebpf_flow_source.go for the rationale. Callers
// must handle a nil return by skipping the corresponding edge.
func (s *TracesFlowSource) createNodeForServiceApplication(
	app *traces.ServiceApplication,
	tenantID string,
	account core.K8sAccount,
) *core.DbNode {
	nodeType := s.inferNodeType(app.Id.Kind, app.Type)

	if isK8sAuthoritativeType(nodeType) {
		s.logger.Debug("skipping traces node creation for k8s-authoritative type",
			"node_type", nodeType,
			"name", app.Id.Name,
			"kind", app.Id.Kind)
		return nil
	}

	// Build unique key keyed on cloud_provider (not observer). For traces-observed
	// K8s workloads cloud_provider is "k8s" so the same node merges with views from
	// k8s_source and other flow sources. Observer ("traces") is recorded on node.Source.
	uniqueKey := core.BuildUniqueKey(
		core.DeriveCloudProvider("traces", nodeType),
		account.CloudAccountID,
		"",
		nodeType,
		app.Id.Namespace,
		app.Id.Name,
	)

	properties := make(map[string]interface{})
	properties["name"] = app.Id.Name
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

	// Add health information
	properties["is_healthy"] = app.IsHealthy
	if app.HealthReason != "" {
		properties["health_reason"] = app.HealthReason
	}

	// Add node stats if available
	if app.NodeStats != nil {
		properties["request_count_per_second"] = app.NodeStats.RequestsPerSecond
		properties["failure_count"] = app.NodeStats.FailureCount
		properties["latency"] = app.NodeStats.Latency
	}

	if len(app.Type) > 0 {
		properties["types"] = app.Type
		if language := core.GetPrimaryLanguage(app.Type); language != "" {
			properties["language"] = language
		}
	}

	properties["subtype"] = app.Id.Kind

	node := core.NewNode(
		nodeType,
		uniqueKey,
		properties,
		tenantID,
		account.CloudAccountID,
		"traces",
	)

	s.logger.Debug("created new node for application",
		"node_type", nodeType,
		"unique_key", uniqueKey,
		"name", app.Id.Name)

	return node
}

// createExternalServiceNode creates a node for an external service.
// Returns nil when name is empty or a raw IP address — callers must
// handle nil by skipping the edge.
func (s *TracesFlowSource) createExternalServiceNode(
	name, kind, tenantID string,
	account core.K8sAccount,
) *core.DbNode {
	if name == "" {
		return nil
	}
	if isRawIPName(name) {
		s.logger.Debug("skipping ExternalService creation for raw-IP name",
			"name", name)
		return nil
	}

	uniqueKey := core.BuildUniqueKey(
		core.DeriveCloudProvider("traces", core.NodeTypeExternalService),
		account.CloudAccountID,
		"",
		core.NodeTypeExternalService,
		"",
		name,
	)

	properties := make(map[string]interface{})
	properties["name"] = name
	properties["kind"] = "ExternalService"
	properties["is_external"] = true
	properties["subtype"] = "ExternalService"
	properties["original_kind"] = kind

	node := core.NewNode(
		core.NodeTypeExternalService,
		uniqueKey,
		properties,
		tenantID,
		account.CloudAccountID,
		"traces",
	)

	s.logger.Debug("created external service node",
		"unique_key", uniqueKey,
		"name", name)

	return node
}

// enrichNodeWithTraceMetadata enriches a matched node with trace metadata
func (s *TracesFlowSource) enrichNodeWithTraceMetadata(node *core.DbNode, app *traces.ServiceApplication) {
	if node == nil || app == nil {
		return
	}

	// Add types if not present
	if _, hasTypes := node.Properties["types"]; !hasTypes && len(app.Type) > 0 {
		node.Properties["types"] = app.Type
	}

	// Add language if not present
	if _, hasLang := node.Properties["language"]; !hasLang {
		if language := core.GetPrimaryLanguage(app.Type); language != "" {
			node.Properties["language"] = language
		}
	}

	// Add health information if not present
	if _, hasHealth := node.Properties["is_healthy"]; !hasHealth {
		node.Properties["is_healthy"] = app.IsHealthy
	}

	if app.HealthReason != "" {
		if _, hasReason := node.Properties["health_reason"]; !hasReason {
			node.Properties["health_reason"] = app.HealthReason
		}
	}

	// Add node stats if available
	if app.NodeStats != nil {
		if _, hasReqRate := node.Properties["request_count_per_second"]; !hasReqRate {
			node.Properties["request_count_per_second"] = app.NodeStats.RequestsPerSecond
		}
		if _, hasFailCount := node.Properties["failure_count"]; !hasFailCount {
			node.Properties["failure_count"] = app.NodeStats.FailureCount
		}
		if _, hasLatency := node.Properties["latency"]; !hasLatency {
			node.Properties["latency"] = app.NodeStats.Latency
		}
	}

	// Enrich with label information
	if app.Labels != nil {
		if sdk, ok := app.Labels["telemetry.sdk.language"]; ok {
			if _, hasLang := node.Properties["programming_language"]; !hasLang {
				node.Properties["programming_language"] = sdk
			}
		}
		if runtime, ok := app.Labels["process.runtime.version"]; ok {
			if _, hasRuntime := node.Properties["runtime_version"]; !hasRuntime {
				node.Properties["runtime_version"] = runtime
			}
		}
		if cluster, ok := app.Labels["k8s_cluster"]; ok {
			if _, hasCluster := node.Properties["cluster"]; !hasCluster {
				node.Properties["cluster"] = cluster
			}
		}
	}
}

// inferNodeType infers the knowledge graph node type from the application kind and type.
//
// kind="Service" maps to NodeTypeService — the logical OTel `service.name`
// identifier. This is distinct from NodeTypeK8sService (the K8s Service
// object that eBPF observes at the network layer) and from NodeTypeWorkload
// (the K8s Deployment/StatefulSet/...). All three may share a name in
// practice and matchServiceApplicationToNode tries each in turn before we
// fall back to creating a stand-alone NodeTypeService node. Mapping
// straight to K8sService would be wrong: OTel service.name is frequently
// the workload name (not the Service object) and just as often a custom
// identifier that doesn't correspond to any K8s entity at all.
func (s *TracesFlowSource) inferNodeType(kind string, appType []string) core.NodeType {
	kindMap := map[string]core.NodeType{
		"Service":         core.NodeTypeService, // OTel logical service identifier
		"Deployment":      core.NodeTypeWorkload,
		"StatefulSet":     core.NodeTypeWorkload,
		"DaemonSet":       core.NodeTypeWorkload,
		"Pod":             core.NodeTypeWorkload,
		"Runner":          core.NodeTypeWorkload,
		"Database":        core.NodeTypeDatabase,
		"ExternalService": core.NodeTypeExternalService,
	}

	// Default for unknown kinds: NodeTypeService (logical OTel service).
	// Match strategies will still try Workload and K8sService before
	// materialising a stand-alone Service node.
	nodeType := core.NodeTypeService
	if nt, ok := kindMap[kind]; ok {
		nodeType = nt
	}

	// Infer more specific node type from appType for non-external services
	if kind != "ExternalService" {
		for _, t := range appType {
			switch strings.ToLower(t) {
			case "database", "postgres", "postgresql", "mysql", "mongodb", "elasticsearch":
				return core.NodeTypeDatabase
			case "cache", "redis":
				return core.NodeTypeCache
			case "messaging", "kafka", "rabbitmq", "sqs", "amqp":
				return core.NodeTypeMessageQueue
			}
		}
	}

	return nodeType
}

// createEdgeFromUpstream creates an edge from the upstream link
func (s *TracesFlowSource) createEdgeFromUpstream(
	sourceNode, upstreamNode *core.DbNode,
	upstream *traces.UpstreamLink,
	tenantID, cloudAccountID string,
) *core.DbEdge {
	properties := make(map[string]interface{})

	if upstream.Protocol != "" {
		properties["protocol"] = upstream.Protocol
	}
	if upstream.Latency > 0 {
		properties["latency_ms"] = upstream.Latency
	}
	if upstream.RequestCount > 0 {
		properties["request_count"] = upstream.RequestCount
	}
	if upstream.FailureCount > 0 {
		properties["failure_count"] = upstream.FailureCount
	}
	if upstream.BytesSent > 0 {
		properties["bytes_sent"] = upstream.BytesSent
	}
	if upstream.BytesReceived > 0 {
		properties["bytes_received"] = upstream.BytesReceived
	}
	if upstream.Status > 0 {
		properties["status"] = upstream.Status
	}

	properties["source_service"] = sourceNode.Properties["name"]
	properties["dest_service"] = upstreamNode.Properties["name"]
	properties["connection_type"] = "service"

	// Add sample trace IDs from DrillDown if available
	if upstream.DrillDown != nil && len(upstream.DrillDown.SampleTraceIds) > 0 {
		properties["sample_trace_ids"] = upstream.DrillDown.SampleTraceIds
	}

	return s.CreateEdge(
		sourceNode,
		upstreamNode,
		core.RelationshipCalls,
		properties,
		tenantID,
		cloudAccountID,
	)
}

// createEdgeFromDownstream creates an edge from the downstream link
func (s *TracesFlowSource) createEdgeFromDownstream(
	downstreamNode, sourceNode *core.DbNode,
	downstream *traces.DownstreamLink,
	tenantID, cloudAccountID string,
) *core.DbEdge {
	properties := make(map[string]interface{})

	if downstream.Protocol != "" {
		properties["protocol"] = downstream.Protocol
	}
	if downstream.Latency > 0 {
		properties["latency_ms"] = downstream.Latency
	}
	if downstream.RequestCount > 0 {
		properties["request_count"] = downstream.RequestCount
	}
	if downstream.FailureCount > 0 {
		properties["failure_count"] = downstream.FailureCount
	}
	if downstream.BytesSent > 0 {
		properties["bytes_sent"] = downstream.BytesSent
	}
	if downstream.BytesReceived > 0 {
		properties["bytes_received"] = downstream.BytesReceived
	}
	if downstream.Status > 0 {
		properties["status"] = downstream.Status
	}

	properties["source_service"] = downstreamNode.Properties["name"]
	properties["dest_service"] = sourceNode.Properties["name"]
	properties["connection_type"] = "service"

	// Add sample trace IDs from DrillDown if available
	if downstream.DrillDown != nil && len(downstream.DrillDown.SampleTraceIds) > 0 {
		properties["sample_trace_ids"] = downstream.DrillDown.SampleTraceIds
	}

	return s.CreateEdge(
		downstreamNode,
		sourceNode,
		core.RelationshipCalls,
		properties,
		tenantID,
		cloudAccountID,
	)
}

// mergeServiceMaps merges multiple service maps into a single service map
// It deduplicates applications by their unique key (Kind:Namespace:Name)
// and preserves the most recent data for each application
func (s *TracesFlowSource) mergeServiceMaps(serviceMaps []*traces.ServiceMap) *traces.ServiceMap {
	if len(serviceMaps) == 0 {
		return &traces.ServiceMap{
			Applications: []traces.ServiceApplication{},
			GeneratedAt:  time.Now(),
		}
	}

	if len(serviceMaps) == 1 {
		return serviceMaps[0]
	}

	// Use a map to deduplicate applications by unique key
	appMap := make(map[string]*traces.ServiceApplication)
	labelsSet := make(map[string]struct{})
	var latestGeneratedAt time.Time

	for _, serviceMap := range serviceMaps {
		if serviceMap == nil {
			continue
		}

		// Track the latest generated time
		if serviceMap.GeneratedAt.After(latestGeneratedAt) {
			latestGeneratedAt = serviceMap.GeneratedAt
		}

		// Collect unique labels
		for _, label := range serviceMap.Labels {
			labelsSet[label] = struct{}{}
		}

		// Merge applications, using unique key to deduplicate
		for i := range serviceMap.Applications {
			app := &serviceMap.Applications[i]
			uniqueKey := fmt.Sprintf("%s:%s:%s", app.Id.Kind, app.Id.Namespace, app.Id.Name)

			existingApp, exists := appMap[uniqueKey]
			if !exists {
				// First time seeing this application, add it
				appCopy := serviceMap.Applications[i]
				appMap[uniqueKey] = &appCopy
			} else {
				// Merge with existing application - combine upstreams, downstreams, and stats
				s.mergeApplicationData(existingApp, app)
			}
		}
	}

	// Convert map back to slice
	mergedApps := make([]traces.ServiceApplication, 0, len(appMap))
	for _, app := range appMap {
		mergedApps = append(mergedApps, *app)
	}

	// Convert labels set to slice
	mergedLabels := make([]string, 0, len(labelsSet))
	for label := range labelsSet {
		mergedLabels = append(mergedLabels, label)
	}

	s.logger.Debug("merged service maps",
		"input_maps", len(serviceMaps),
		"merged_applications", len(mergedApps),
		"merged_labels", len(mergedLabels))

	return &traces.ServiceMap{
		Applications: mergedApps,
		GeneratedAt:  latestGeneratedAt,
		Labels:       mergedLabels,
	}
}

// mergeApplicationData merges data from a source application into an existing application
func (s *TracesFlowSource) mergeApplicationData(existing, source *traces.ServiceApplication) {
	// Merge upstreams - deduplicate by ID
	upstreamMap := make(map[string]traces.UpstreamLink)
	for _, u := range existing.Upstreams {
		upstreamMap[u.Id] = u
	}
	for _, u := range source.Upstreams {
		if existingU, exists := upstreamMap[u.Id]; exists {
			// Merge metrics - sum counts, use max latency
			existingU.RequestCount += u.RequestCount
			existingU.FailureCount += u.FailureCount
			existingU.BytesSent += u.BytesSent
			existingU.BytesReceived += u.BytesReceived
			if u.Latency > existingU.Latency {
				existingU.Latency = u.Latency
			}
			upstreamMap[u.Id] = existingU
		} else {
			upstreamMap[u.Id] = u
		}
	}
	existing.Upstreams = make([]traces.UpstreamLink, 0, len(upstreamMap))
	for _, u := range upstreamMap {
		existing.Upstreams = append(existing.Upstreams, u)
	}

	// Merge downstreams - deduplicate by ID (Name:Namespace:Kind)
	downstreamMap := make(map[string]traces.DownstreamLink)
	for _, d := range existing.Downstreams {
		key := fmt.Sprintf("%s:%s:%s", d.Id.Name, d.Id.Namespace, d.Id.Kind)
		downstreamMap[key] = d
	}
	for _, d := range source.Downstreams {
		key := fmt.Sprintf("%s:%s:%s", d.Id.Name, d.Id.Namespace, d.Id.Kind)
		if existingD, exists := downstreamMap[key]; exists {
			// Merge metrics
			existingD.RequestCount += d.RequestCount
			existingD.FailureCount += d.FailureCount
			existingD.BytesSent += d.BytesSent
			existingD.BytesReceived += d.BytesReceived
			if d.Latency > existingD.Latency {
				existingD.Latency = d.Latency
			}
			downstreamMap[key] = existingD
		} else {
			downstreamMap[key] = d
		}
	}
	existing.Downstreams = make([]traces.DownstreamLink, 0, len(downstreamMap))
	for _, d := range downstreamMap {
		existing.Downstreams = append(existing.Downstreams, d)
	}

	// Merge labels
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	for k, v := range source.Labels {
		if _, exists := existing.Labels[k]; !exists {
			existing.Labels[k] = v
		}
	}

	// Merge instances - deduplicate by ID (Name:Namespace:Kind)
	instanceMap := make(map[string]traces.Instance)
	for _, inst := range existing.Instances {
		key := fmt.Sprintf("%s:%s:%s", inst.Id.Name, inst.Id.Namespace, inst.Id.Kind)
		instanceMap[key] = inst
	}
	for _, inst := range source.Instances {
		key := fmt.Sprintf("%s:%s:%s", inst.Id.Name, inst.Id.Namespace, inst.Id.Kind)
		if _, exists := instanceMap[key]; !exists {
			instanceMap[key] = inst
		}
	}
	existing.Instances = make([]traces.Instance, 0, len(instanceMap))
	for _, inst := range instanceMap {
		existing.Instances = append(existing.Instances, inst)
	}

	// Merge node stats - sum the metrics
	if source.NodeStats != nil {
		if existing.NodeStats == nil {
			existing.NodeStats = source.NodeStats
		} else {
			existing.NodeStats.RequestsPerSecond += source.NodeStats.RequestsPerSecond
			existing.NodeStats.FailureCount += source.NodeStats.FailureCount
			if source.NodeStats.Latency > existing.NodeStats.Latency {
				existing.NodeStats.Latency = source.NodeStats.Latency
			}
		}
	}

	// Update health status - unhealthy takes precedence
	if !source.IsHealthy {
		existing.IsHealthy = false
		if source.HealthReason != "" {
			existing.HealthReason = source.HealthReason
		}
	}
}
