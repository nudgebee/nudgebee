package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const (
	// errRowsAffected is the wrap format for sql.Result.RowsAffected failures.
	errRowsAffected = "failed to get rows affected: %w"
)

// emptyJSONObject is a pre-allocated []byte for empty JSON objects to avoid
// per-iteration json.Marshal calls for nil/empty maps in hot loops.
var emptyJSONObject = []byte("{}")

// marshalMapOrEmpty marshals a map to JSON, returning a pre-allocated empty
// JSON object literal for empty maps to skip encoding overhead.
// Nil maps serialize as JSON null to preserve nil-detection in downstream code.
func marshalMapOrEmpty(m map[string]interface{}) ([]byte, error) {
	if m == nil {
		return json.Marshal(m)
	}
	if len(m) == 0 {
		return emptyJSONObject, nil
	}
	return json.Marshal(m)
}

// marshalStringMapOrEmpty is the string-valued variant of marshalMapOrEmpty.
func marshalStringMapOrEmpty(m map[string]string) ([]byte, error) {
	if m == nil {
		return json.Marshal(m)
	}
	if len(m) == 0 {
		return emptyJSONObject, nil
	}
	return json.Marshal(m)
}

// marshalSingleKeyValue builds a JSON object string for a single key-value pair
// without invoking the full JSON encoder, e.g. `{"key":"value"}`.
// Keys and values are escaped via json.Marshal on the string values only.
func marshalSingleKeyValue(key, value string) (string, error) {
	k, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	v, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(1 + len(k) + 1 + len(v) + 1)
	b.WriteByte('{')
	b.Write(k)
	b.WriteByte(':')
	b.Write(v)
	b.WriteByte('}')
	return b.String(), nil
}

// SourceInterface defines the interface that all sources must implement
type SourceInterface interface {
	GetName() string
	BuildGraph(ctx *security.RequestContext, req *SourceBuildRequest) (*Graph, error)
	IsEnabled() bool
	Validate() error
}

// SourceBuildRequest contains parameters for building a graph from a source
type SourceBuildRequest struct {
	TenantID       string
	CloudAccountID string
	TimeRange      *TimeRange
	Filters        map[string]string
	Region         string // Filter by AWS region
}

// ExternalServiceEnricherInterface defines the interface for enriching external services
// with cloud resources. This is called after all flow sources complete, allowing
// cross-source node matching.
type ExternalServiceEnricherInterface interface {
	// EnrichExternalServices matches external services with cloud resources
	// Parameters:
	//   - reqCtx: Request context for authentication
	//   - externalServices: External service nodes to enrich
	//   - allNodes: All nodes from all flow sources (for node matching)
	//   - allEdges: All edges from all flow sources
	//   - tenantID: Tenant identifier
	// Returns:
	//   - enrichedNodes: All nodes including enriched external services
	//   - enrichedEdges: All edges including new cloud resource edges
	EnrichExternalServices(
		reqCtx *security.RequestContext,
		externalServices []*DbNode,
		allNodes []*DbNode,
		allEdges []*DbEdge,
		tenantID string,
	) ([]*DbNode, []*DbEdge, error)
}

// CrossSourceEnricherInterface defines the interface for enriching nodes
// with cross-source relationships. Called after all infrastructure sources complete
// but before flow sources, enabling cross-account matching (e.g., AWS LoadBalancer -> K8s).
type CrossSourceEnricherInterface interface {
	GetName() string
	EnrichCrossSources(
		reqCtx *security.RequestContext,
		allNodes []*DbNode,
		allEdges []*DbEdge,
		tenantID string,
	) ([]*DbNode, []*DbEdge, error)
}

const (
	accountMappingsCacheTTL    = 1 * time.Hour
	msgAccountMappingsFallback = "failed to get account mappings, proceeding without name conversion"
)

type accountMappingsCacheEntry struct {
	mu        sync.RWMutex
	data      map[string]map[string]string // tenantID -> accountID -> accountName
	expiresAt map[string]time.Time         // tenantID -> expiry time
}

// Service is the main knowledge graph service
type Service struct {
	logger                    *slog.Logger
	sources                   map[string]SourceInterface
	flowSources               map[string]FlowSourceInterface
	externalServiceEnricher   ExternalServiceEnricherInterface
	crossSourceEnrichers      []CrossSourceEnricherInterface
	dbManager                 *database.DatabaseManager
	defaultRelationshipsPath  string
	defaultRelationshipsCache []CrossAccountRelationship
	ctx                       *security.RequestContext
	accountMappingsCache      accountMappingsCacheEntry
}

// FlowSourceInterface defines the interface for flow sources
// Flow sources enrich the graph with flow relationships (CALLS, PUBLISHES_TO, etc.)
type FlowSourceInterface interface {
	GetName() string
	BuildFlowRelationships(ctx *security.RequestContext, req *FlowSourceBuildRequest) ([]*DbEdge, []*DbNode, error)
	IsEnabled() bool
	Validate() error
	GetSourceCategory() FlowSourceCategory
}

// FlowSourceBuildRequest contains parameters for building flow relationships
type FlowSourceBuildRequest struct {
	TenantID        string
	CloudAccountID  string
	CloudAccountIDs []string
	TimeRange       *TimeRange
	Filters         map[string]string
	ExistingNodes   []*DbNode
}

// FlowSourceCategory defines the category of flow source
type FlowSourceCategory string

const (
	FlowSourceCategoryTracing    FlowSourceCategory = "tracing"
	FlowSourceCategoryNetworking FlowSourceCategory = "networking"
	FlowSourceCategoryLogs       FlowSourceCategory = "logs"
	FlowSourceCategoryMetrics    FlowSourceCategory = "metrics"
	FlowSourceCategoryCustom     FlowSourceCategory = "custom"
)

// Batch sizes for database inserts to avoid PostgreSQL parameter limit (65535).
// PostgreSQL caps bind parameters at uint16 max (65535); each row consumes one
// parameter per column in the upsert, so batch_size * len(cols) must stay below it.
// When you add a column to nodeUpsertCols/edgeUpsertCols, recompute these — the
// regression test in service_test.go pins the math.
const (
	EdgeBatchSize = 5000 // × len(edgeUpsertCols)=12 → 60,000 params
	NodeBatchSize = 4000 // × len(nodeUpsertCols)=14 → 56,000 params
)

// nodeUpsertCols are the columns written by SaveNodes. Order must match the
// values slice built per row in SaveNodes.
var nodeUpsertCols = []string{
	"id", "created_at", "updated_at",
	"properties", "labels", "query_attributes",
	"cloud_account_id", "tenant_id",
	"unique_key", "node_type", "level",
	"last_sync_version", "is_active", "source",
}

// edgeUpsertCols are the columns written by SaveEdges. Order must match the
// values slice built per row in SaveEdges.
var edgeUpsertCols = []string{
	"id", "created_at", "updated_at",
	"source_node_id", "destination_node_id", "relationship_type",
	"properties", "cloud_account_id", "tenant_id",
	"level", "is_active", "last_sync_version",
}

// NewService creates a new knowledge graph service
func NewService(ctx *security.RequestContext, logger *slog.Logger, dbManager *database.DatabaseManager) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		sources:     make(map[string]SourceInterface),
		flowSources: make(map[string]FlowSourceInterface),
		logger:      logger,
		dbManager:   dbManager,
		ctx:         ctx,
		accountMappingsCache: accountMappingsCacheEntry{
			data:      make(map[string]map[string]string),
			expiresAt: make(map[string]time.Time),
		},
	}
}

// RegisterSource registers a new source with the service
func (s *Service) RegisterSource(source SourceInterface) error {
	if source == nil {
		return fmt.Errorf("source cannot be nil")
	}

	name := source.GetName()
	if name == "" {
		return fmt.Errorf("source name cannot be empty")
	}

	if err := source.Validate(); err != nil {
		return fmt.Errorf("source validation failed: %w", err)
	}

	s.sources[name] = source
	s.logger.Info("registered knowledge graph source", "source", name, "enabled", source.IsEnabled())
	return nil
}

// RegisterFlowSource registers a new flow source with the service
func (s *Service) RegisterFlowSource(flowSource FlowSourceInterface) error {
	if flowSource == nil {
		return fmt.Errorf("flow source cannot be nil")
	}

	name := flowSource.GetName()
	if name == "" {
		return fmt.Errorf("flow source name cannot be empty")
	}

	if err := flowSource.Validate(); err != nil {
		return fmt.Errorf("flow source validation failed: %w", err)
	}

	s.flowSources[name] = flowSource
	s.logger.Info("registered knowledge graph flow source",
		"flow_source", name,
		"category", flowSource.GetSourceCategory(),
		"enabled", flowSource.IsEnabled())
	return nil
}

// defaultEnabledFlowSources returns the names of all registered flow sources
// that are currently enabled, sorted alphabetically for deterministic ordering.
// Used as the default when a BuildRequest does not specify FlowSources.
func (s *Service) defaultEnabledFlowSources() []string {
	names := make([]string, 0, len(s.flowSources))
	for name, flowSource := range s.flowSources {
		if flowSource.IsEnabled() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// RegisterExternalServiceEnricher registers the external service enricher
// This enricher is called after all flow sources complete to match external services
// with cloud resources across all flow sources
func (s *Service) RegisterExternalServiceEnricher(enricher ExternalServiceEnricherInterface) error {
	if enricher == nil {
		return fmt.Errorf("external service enricher cannot be nil")
	}

	s.externalServiceEnricher = enricher
	s.logger.Info("registered external service enricher")
	return nil
}

// RegisterCrossSourceEnricher registers a cross-source enricher
// Cross-source enrichers are called after all infrastructure sources complete (Phase 2.1)
// but before flow sources, enabling cross-account matching (e.g., AWS LoadBalancer -> K8s)
func (s *Service) RegisterCrossSourceEnricher(enricher CrossSourceEnricherInterface) error {
	if enricher == nil {
		return fmt.Errorf("cross-source enricher cannot be nil")
	}

	// Check for duplicate registration
	enricherName := enricher.GetName()
	for _, existing := range s.crossSourceEnrichers {
		if existing.GetName() == enricherName {
			s.logger.Debug("cross-source enricher already registered, skipping", "name", enricherName)
			return nil // Skip duplicate, not an error
		}
	}

	s.crossSourceEnrichers = append(s.crossSourceEnrichers, enricher)
	s.logger.Info("registered cross-source enricher", "name", enricherName)
	return nil
}

// ListFlowSources returns information about all registered flow sources
func (s *Service) ListFlowSources() []FlowSourceInfo {
	flowSources := make([]FlowSourceInfo, 0, len(s.flowSources))
	for name, flowSource := range s.flowSources {
		flowSources = append(flowSources, FlowSourceInfo{
			Name:     name,
			Category: flowSource.GetSourceCategory(),
			Enabled:  flowSource.IsEnabled(),
		})
	}
	return flowSources
}

// FlowSourceInfo contains information about a flow source
type FlowSourceInfo struct {
	Name     string             `json:"name"`
	Category FlowSourceCategory `json:"category"`
	Enabled  bool               `json:"enabled"`
}

// SetDefaultRelationshipsPath sets the path to the default relationships JSON file
func (s *Service) SetDefaultRelationshipsPath(path string) error {
	s.logger.Info("loading default relationships from file", "path", path)

	relationships, err := LoadDefaultRelationships(path)
	if err != nil {
		return fmt.Errorf("failed to load default relationships: %w", err)
	}

	s.defaultRelationshipsPath = path
	s.defaultRelationshipsCache = relationships
	s.logger.Info("successfully loaded default relationships", "count", len(relationships))
	return nil
}

// GetDefaultRelationships returns the cached default relationships
// If no cache is set (via SetDefaultRelationshipsPath), falls back to embedded defaults
func (s *Service) GetDefaultRelationships() []CrossAccountRelationship {
	if s.defaultRelationshipsCache == nil {
		// Fall back to embedded default relationships
		defaults, err := loadDefaultRelationships()
		if err != nil {
			s.logger.Warn("failed to load embedded default relationships", "error", err)
			return []CrossAccountRelationship{}
		}
		s.logger.Info("using embedded default relationships", "count", len(defaults))
		return defaults
	}
	return s.defaultRelationshipsCache
}

// getActiveAccountsForTenant retrieves all active accounts for a given tenant
func (s *Service) getActiveAccountsForTenant(ctx *security.RequestContext, tenantID string, accountIDs []string) ([]models.Account, error) {
	query := `
		SELECT
			ca.id,
			ca.cloud_provider,
			ca.account_number,
			ca.account_name,
			ca.created_at,
			ca.created_by,
			ca.updated_at,
			ca.updated_by,
			ca.billing_source,
			ca.start_date,
			ca.tenant,
			ca.assume_role,
			ca.region,
			ca.status,
			ca.account_url,
			ca.budget,
			ca.synced_at,
			ca.sync_status,
			ca.account_access,
			ca.account_purpose,
			ca.data,
			ca.access_key,
			ca.access_secret,
			ca.account_type,
			ca.agent_access_key,
			ca.agent_access_secret,
			ca.agent_synced_at,
			ca.sync_status_message,
			ca.external_id,
			ca.etl_attempt,
			ca.parent_account_id,
			ca.access_secret_v2,
			ca.account_env
		FROM cloud_accounts ca
		WHERE ca.tenant = $1
			AND ca.status = 'active'
	`
	args := []interface{}{tenantID}

	// Add account_id filter if provided
	if len(accountIDs) > 0 {
		placeholders := make([]string, len(accountIDs))
		for i, accountID := range accountIDs {
			placeholders[i] = fmt.Sprintf("$%d", i+2)
			args = append(args, accountID)
		}
		query += fmt.Sprintf(" AND ca.id IN (%s)", strings.Join(placeholders, ","))
	}

	query += " ORDER BY ca.cloud_provider, ca.account_name"

	rows, err := s.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query active accounts: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows", "error", closeErr)
		}
	}()

	var accounts []models.Account
	for rows.Next() {
		var account models.Account
		err := rows.Scan(
			&account.Id,
			&account.CloudProvider,
			&account.AccountNumber,
			&account.AccountName,
			&account.CreatedAt,
			&account.CreatedBy,
			&account.UpdatedAt,
			&account.UpdatedBy,
			&account.BillingSource,
			&account.StartDate,
			&account.Tenant,
			&account.AssumeRole,
			&account.Region,
			&account.Status,
			&account.AccountUrl,
			&account.Budget,
			&account.SyncedAt,
			&account.SyncStatus,
			&account.AccountAccess,
			&account.AccountPurpose,
			&account.Data,
			&account.AccessKey,
			&account.AccessSecret,
			&account.AccountType,
			&account.AgentAccessKey,
			&account.AgentAccessSecret,
			&account.AgentSyncedAt,
			&account.SyncStatusMessage,
			&account.ExternalId,
			&account.EtlAttempt,
			&account.ParentAccountId,
			&account.AccessSecretV2,
			&account.AccountEnv,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}
		accounts = append(accounts, account)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating account rows: %w", err)
	}

	return accounts, nil
}

// SourceCategory defines whether a source is account-specific or integration-specific
type SourceCategory string

const (
	SourceCategoryAccount     SourceCategory = "account"     // Account-specific sources (aws, k8s)
	SourceCategoryIntegration SourceCategory = "integration" // Integration-specific sources
)

// getSourceCategory returns the category of a source
func (s *Service) getSourceCategory(sourceName string) SourceCategory {
	integrationSources := map[string]bool{
		// Add other integration sources here as they are implemented
	}

	if integrationSources[sourceName] {
		return SourceCategoryIntegration
	}
	return SourceCategoryAccount
}

// shouldIncrementSyncVersion determines if sync version should be incremented based on sources
// Returns true if any static source (aws, k8s) is present, or if no sources are defined (meaning all sources apply)
// Returns false if only flow sources (ebpf, datadog, traces) are present
func (s *Service) shouldIncrementSyncVersion(sources []string, flowSources []string) bool {
	// If no sources are defined, all sources are applicable (including static ones)
	if len(sources) == 0 {
		return true
	}

	// Define static sources that should increment version
	staticSources := map[string]bool{
		"aws":   true,
		"k8s":   true,
		"azure": true,
		"gcp":   true,
	}

	// Check if any static source is present in sources list
	for _, source := range sources {
		if staticSources[source] {
			return true
		}
	}

	// If no static sources found, don't increment version
	// (flow sources alone don't mark nodes inactive)
	return false
}

// getApplicableSourcesForAccount returns the list of sources that should be used for a given account type
// Returns two lists: account-specific sources and integration-specific sources
func (s *Service) getApplicableSourcesForAccount(cloudProvider string, requestedSources []string) ([]string, []string) {
	// Define mapping of cloud providers to applicable sources
	sourceMapping := map[string][]string{
		"K8s":   {"k8s"}, // K8s accounts can use K8s sources
		"AWS":   {"aws"}, // AWS accounts can use AWS sources
		"Azure": {},      // Azure accounts have no default sources
		"GCP":   {"gcp"}, // GCP accounts can use GCP sources
	}

	applicableSources, exists := sourceMapping[cloudProvider]
	if !exists {
		// Default to empty for unknown providers
		applicableSources = []string{}
	}

	// If specific sources are requested, filter to only applicable ones
	if len(requestedSources) > 0 {
		filtered := make([]string, 0)
		for _, requested := range requestedSources {
			for _, applicable := range applicableSources {
				if requested == applicable {
					filtered = append(filtered, requested)
					break
				}
			}
		}
		applicableSources = filtered
	}

	// Separate into account-specific and integration-specific sources
	accountSources := make([]string, 0)
	integrationSources := make([]string, 0)

	for _, source := range applicableSources {
		if s.getSourceCategory(source) == SourceCategoryAccount {
			accountSources = append(accountSources, source)
		} else {
			integrationSources = append(integrationSources, source)
		}
	}

	return accountSources, integrationSources
}

// BuildGraphs builds knowledge graphs from all specified sources and merges them into a single unified graph
// Architecture:
// Phase 1: Build account-specific graphs (AWS, K8s) for each account
// Phase 2: Build integration-specific graphs (Datadog) once per unique integration
// Phase 3: Merge all graphs and create cross-account relationships
func (s *Service) BuildGraphs(ctx *security.RequestContext, req *BuildRequest) (*BuildResponse, error) {
	response := &BuildResponse{
		Success:         true,
		AccountMetadata: make([]AccountGraphMetadata, 0),
	}

	// If no flow sources requested, default to all enabled registered flow sources.
	// Done up-front so shouldIncrementSyncVersion and any logging below see the
	// final set rather than an empty slice.
	if len(req.FlowSources) == 0 {
		req.FlowSources = s.defaultEnabledFlowSources()
	}

	// Fetch all active accounts for tenant
	s.logger.Info("building knowledge graphs for all active accounts",
		"tenant_id", req.TenantID)
	ctx = security.NewRequestContextForTenantAdmin(req.TenantID, s.logger, ctx.GetTracer(), ctx.GetMeter())
	accounts, err := s.getActiveAccountsForTenant(ctx, req.TenantID, req.AccountIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch active accounts for tenant: %w", err)
	}

	if len(accounts) == 0 {
		s.logger.Warn("no active accounts found for tenant", "tenant_id", req.TenantID)
		return &BuildResponse{
			Success: false,
			Error:   "no active accounts found for tenant",
		}, nil
	}

	s.logger.Info("found active accounts for tenant",
		"tenant_id", req.TenantID,
		"account_count", len(accounts))

	// Get filter for sync version tracking
	filterRepo := NewFilterRepository(s.dbManager, s.logger)
	filter, err := filterRepo.GetFilterForTenant(ctx, req.TenantID, "")
	if err != nil {
		s.logger.Warn("failed to get filter for tenant, will use sync version 0",
			"tenant_id", req.TenantID,
			"error", err)
		return nil, fmt.Errorf("failed to get filter for tenant: %w", err)
	}

	// Determine if sync version should be incremented
	shouldIncrement := s.shouldIncrementSyncVersion(req.Sources, req.FlowSources)
	var currentSyncVersion int64 = 0
	var filterID uuid.UUID

	if filter != nil {
		filterID = filter.ID
		if shouldIncrement {
			currentSyncVersion = filter.LastSyncVersion + 1
			s.logger.Info("incrementing sync version",
				"tenant_id", req.TenantID,
				"previous_version", filter.LastSyncVersion,
				"new_version", currentSyncVersion,
				"sources", req.Sources)
		} else {
			currentSyncVersion = filter.LastSyncVersion
			s.logger.Info("keeping sync version unchanged (flow sources only)",
				"tenant_id", req.TenantID,
				"sync_version", currentSyncVersion,
				"sources", req.Sources,
				"flow_sources", req.FlowSources)
		}
	} else {
		s.logger.Warn("no filter found for tenant, using sync version 0",
			"tenant_id", req.TenantID)
	}

	// Flow sources that completed this sync without error. Populated in Phase 2.5
	// below; consumed by markInactiveNodes to decide which flow-sourced rows are
	// eligible for the infra-authoritative sweep branch. A flow source absent
	// from this list (because it errored or wasn't run) protects its rows from
	// being tombstoned this cycle.
	successfulFlowSources := make([]string, 0, len(req.FlowSources))

	// Unified graph to hold all nodes and edges from all accounts and sources
	unifiedGraph := &Graph{
		Nodes:       make([]*DbNode, 0),
		Edges:       make([]*DbEdge, 0),
		TenantID:    req.TenantID,
		GeneratedAt: time.Now(),
	}

	// ========================================================================
	// PHASE 1: Build account-specific graphs (AWS, K8s)
	// ========================================================================
	s.logger.Info("Phase 1: Building account-specific graphs")

	for _, account := range accounts {
		accountMetadata := AccountGraphMetadata{
			AccountID:      account.Id,
			AccountName:    account.AccountName,
			CloudProvider:  account.CloudProvider,
			SourcesBuilt:   make([]string, 0),
			BuildSucceeded: true,
		}

		s.logger.Info("processing account",
			"account_id", account.Id,
			"account_name", account.AccountName,
			"cloud_provider", account.CloudProvider)

		// Get applicable sources for this account (separated by category)
		accountSources, integrationSources := s.getApplicableSourcesForAccount(account.CloudProvider, req.Sources)

		if len(accountSources) == 0 && len(integrationSources) == 0 {
			s.logger.Warn("no applicable sources for account",
				"account_id", account.Id,
				"cloud_provider", account.CloudProvider)
			accountMetadata.BuildSucceeded = false
			accountMetadata.Error = "no applicable sources for account type"
			response.AccountMetadata = append(response.AccountMetadata, accountMetadata)
			continue
		}

		accountNodeCount := 0
		accountEdgeCount := 0

		// Build graph from account-specific sources only
		for _, sourceName := range accountSources {
			source, exists := s.sources[sourceName]
			if !exists {
				s.logger.Warn("source not found", "source", sourceName, "account_id", account.Id)
				continue
			}

			if !source.IsEnabled() {
				s.logger.Warn("source not enabled", "source", sourceName, "account_id", account.Id)
				continue
			}

			sourceReq := &SourceBuildRequest{
				TenantID:       account.Tenant,
				CloudAccountID: account.Id,
				TimeRange:      req.TimeRange,
				Filters:        req.Filters,
			}

			s.logger.Info("building graph from account source",
				"source", sourceName,
				"account_id", account.Id,
				"account_name", account.AccountName)
			startTime := time.Now()

			graph, err := source.BuildGraph(ctx, sourceReq)
			if err != nil {
				s.logger.Error("failed to build graph from source",
					"source", sourceName,
					"account_id", account.Id,
					"error", err,
					"duration", time.Since(startTime).Seconds())

				if accountMetadata.Error == "" {
					accountMetadata.Error = fmt.Sprintf("source '%s': %v", sourceName, err)
				} else {
					accountMetadata.Error += fmt.Sprintf("; source '%s': %v", sourceName, err)
				}
				accountMetadata.BuildSucceeded = false
				continue
			}

			// Merge nodes and edges from this source into unified graph
			unifiedGraph.Nodes = append(unifiedGraph.Nodes, graph.Nodes...)
			unifiedGraph.Edges = append(unifiedGraph.Edges, graph.Edges...)

			accountNodeCount += len(graph.Nodes)
			accountEdgeCount += len(graph.Edges)
			accountMetadata.SourcesBuilt = append(accountMetadata.SourcesBuilt, sourceName)

			for _, node := range graph.Nodes {
				node.CloudAccountID = account.Id
				node.TenantID = req.TenantID
			}
			for _, edge := range graph.Edges {
				edge.CloudAccountID = account.Id
				edge.TenantID = req.TenantID
			}

			s.logger.Info("successfully built graph from account source",
				"source", sourceName,
				"account_id", account.Id,
				"nodes", len(graph.Nodes),
				"edges", len(graph.Edges),
				"duration", time.Since(startTime).Seconds())
		}

		accountMetadata.NodeCount = accountNodeCount
		accountMetadata.EdgeCount = accountEdgeCount
		response.AccountMetadata = append(response.AccountMetadata, accountMetadata)

		s.logger.Info("completed processing account",
			"account_id", account.Id,
			"account_sources_built", len(accountSources),
			"integration_sources_needed", len(integrationSources),
			"nodes", accountNodeCount,
			"edges", accountEdgeCount)
	}

	response.AccountsProcessed = len(accounts)

	// ========================================================================
	// PHASE 2.1: Cross-source enrichment (AWS LoadBalancer -> K8s)
	// Runs after all infrastructure sources complete, before flow sources
	// Enables cross-account matching between different source types
	// ========================================================================
	if len(s.crossSourceEnrichers) > 0 {
		s.logger.Info("Phase 2.1: Running cross-source enrichers",
			"enricher_count", len(s.crossSourceEnrichers),
			"total_nodes", len(unifiedGraph.Nodes))

		for _, enricher := range s.crossSourceEnrichers {
			startTime := time.Now()
			enrichedNodes, enrichedEdges, err := enricher.EnrichCrossSources(
				ctx,
				unifiedGraph.Nodes,
				unifiedGraph.Edges,
				req.TenantID,
			)
			if err != nil {
				s.logger.Warn("cross-source enricher failed",
					"enricher", enricher.GetName(),
					"error", err)
				continue
			}

			newEdgeCount := len(enrichedEdges) - len(unifiedGraph.Edges)
			unifiedGraph.Nodes = enrichedNodes
			unifiedGraph.Edges = enrichedEdges

			s.logger.Info("cross-source enricher completed",
				"enricher", enricher.GetName(),
				"new_edges", newEdgeCount,
				"duration", time.Since(startTime).Seconds())
		}
	}

	// ========================================================================
	// PHASE 2.5: Build flow relationships from flow sources
	// Flow sources read the existing graph and enrich it with flow edges
	// ========================================================================
	if len(req.FlowSources) > 0 && len(s.flowSources) > 0 {
		s.logger.Info("Phase 2.5: Building flow relationships from flow sources",
			"requested_flow_sources", len(req.FlowSources),
			"registered_flow_sources", len(s.flowSources))

		flowEdges, flowNodes, flowErrors := s.buildFlowRelationships(ctx, req, unifiedGraph.Nodes)

		// Capture which flow sources ran to completion without error. Done right
		// after buildFlowRelationships so the set mirrors exactly what flowErrors
		// observed this cycle; a later error path must not flip success → failure.
		for _, fs := range req.FlowSources {
			if _, errored := flowErrors[fs]; !errored {
				successfulFlowSources = append(successfulFlowSources, fs)
			}
		}

		if len(flowEdges) > 0 {
			// Add flow edges to unified graph
			unifiedGraph.Edges = append(unifiedGraph.Edges, flowEdges...)

			s.logger.Info("added flow relationships to unified graph",
				"flow_edges", len(flowEdges),
				"total_edges", len(unifiedGraph.Edges))
		}

		if len(flowNodes) > 0 {
			// Add flow edges to unified graph
			unifiedGraph.Nodes = append(unifiedGraph.Nodes, flowNodes...)

			s.logger.Info("added flow Nodes to unified graph",
				"flow_edges", len(flowNodes),
				"total_edges", len(unifiedGraph.Nodes))
		}

		// Log any flow source errors
		if len(flowErrors) > 0 {
			s.logger.Warn("encountered errors in flow sources",
				"error_count", len(flowErrors))
			for flowSourceName, err := range flowErrors {
				s.logger.Error("flow source error",
					"flow_source", flowSourceName,
					"error", err)
			}
		}
	}

	// Deduplicate nodes and edges across all sources and accounts
	// Use priority-aware deduplication for edges to handle overlaps from multiple flow sources
	// (e.g., eBPF, Traces, Datadog APM all creating CALLS edges between same nodes)
	//
	// Use DeduplicateNodesWithIDMapping to get a mapping of old node IDs to surviving node IDs.
	// This is necessary because flow sources (like eBPF) may create new nodes that have the same
	// UniqueKey as existing nodes. When deduplicated, the original node's ID is kept, but edges
	// created by flow sources still reference the new node's ID which no longer exists.
	var nodeIDMapping map[string]string
	unifiedGraph.Nodes, nodeIDMapping = DeduplicateNodesWithIDMapping(unifiedGraph.Nodes)

	// Update edge node references to use surviving node IDs after deduplication
	for _, edge := range unifiedGraph.Edges {
		if newID, exists := nodeIDMapping[edge.SourceNodeID]; exists {
			edge.SourceNodeID = newID
		}
		if newID, exists := nodeIDMapping[edge.DestinationNodeID]; exists {
			edge.DestinationNodeID = newID
		}
	}

	unifiedGraph.Edges = DeduplicateEdgesWithPriority(unifiedGraph.Edges)

	// Merge default relationships (from file) with API-provided relationships
	defaultRelationships := s.GetDefaultRelationships()
	mergedRelationships := MergeRelationships(defaultRelationships, req.CrossAccountRelationships)

	// Build cross-account relationships using merged relationships
	if len(mergedRelationships) > 0 {
		s.logger.Info("building cross-account relationships",
			"tenant_id", req.TenantID,
			"default_rules_count", len(defaultRelationships),
			"api_rules_count", len(req.CrossAccountRelationships),
			"merged_rules_count", len(mergedRelationships))

		crossAccountEdges, err := s.BuildCrossAccountRelationships(unifiedGraph, mergedRelationships)
		if err != nil {
			s.logger.Error("failed to build cross-account relationships", "error", err)
			response.Success = false
			if response.Error == "" {
				response.Error = fmt.Sprintf("failed to build cross-account relationships: %v", err)
			} else {
				response.Error += fmt.Sprintf("; failed to build cross-account relationships: %v", err)
			}
		} else if len(crossAccountEdges) > 0 {
			// Add cross-account edges to unified graph
			unifiedGraph.Edges = append(unifiedGraph.Edges, crossAccountEdges...)

			// Deduplicate edges again after adding cross-account relationships
			unifiedGraph.Edges = DeduplicateEdges(unifiedGraph.Edges)

			s.logger.Info("added cross-account relationships to unified graph",
				"cross_account_edges", len(crossAccountEdges),
				"total_edges", len(unifiedGraph.Edges))
		}
	}

	// Phase 3.5: Collapse ExternalService → CloudResource bridges into direct edges.
	// Runs on the fully merged, deduped graph so it absorbs every flow source and
	// any future cross-account rule that emits ROUTES_THROUGH/RESOLVES_TO from ES.
	// Rewriting a CALLS edge can collide with a directly-observed CALLS edge from
	// another source; the priority-aware dedup pass that follows resolves the winner.
	unifiedGraph.Nodes, unifiedGraph.Edges, _ = CollapseEnrichedExternalServices(
		unifiedGraph.Nodes, unifiedGraph.Edges, s.logger,
	)
	unifiedGraph.Edges = DeduplicateEdgesWithPriority(unifiedGraph.Edges)

	// Calculate metadata for unified graph
	unifiedGraph.Metadata = s.calculateMetadata(unifiedGraph)

	response.KnowledgeGraph = ConvertGraphToKnowledgeGraph(unifiedGraph)

	s.logger.Info("successfully built unified knowledge graph",
		"tenant_id", req.TenantID,
		"accounts_processed", response.AccountsProcessed,
		"total_nodes", len(unifiedGraph.Nodes),
		"total_edges", len(unifiedGraph.Edges))

	// TEST ONLY: Dump unified graph to JSON file for inspection
	// if graphJSON, err := json.MarshalIndent(unifiedGraph, "", "  "); err == nil {
	// 	_ = os.WriteFile("nudgebee_graph.json", graphJSON, 0644)
	// }

	// Save to database if requested
	if req.SaveToDB {
		nodesSaved, edgesSaved, err := s.saveGraphToDB(unifiedGraph, currentSyncVersion, s.logger)
		if err != nil {
			s.logger.Error("failed to save unified graph to database", "error", err)
			response.Success = false
			if response.Error == "" {
				response.Error = fmt.Sprintf("failed to save graph to database: %v", err)
			} else {
				response.Error += fmt.Sprintf("; failed to save graph to database: %v", err)
			}
		} else {
			response.SavedToDB = true
			response.NodesSaved = nodesSaved
			response.EdgesSaved = edgesSaved
			s.logger.Info("successfully saved unified graph to database",
				"nodes_saved", nodesSaved,
				"edges_saved", edgesSaved)

			// Mark all nodes belonging to inactive accounts as inactive
			inactiveAccountNodeCount, err := s.markNodesForInactiveAccounts(ctx, req.TenantID)
			if err != nil {
				s.logger.Error("failed to mark nodes for inactive accounts", "error", err)
				// Don't fail the entire operation, just log the error
			} else {
				s.logger.Info("marked nodes for inactive accounts",
					"inactive_account_node_count", inactiveAccountNodeCount)
			}

			// Mark inactive nodes and update sync version if applicable
			if shouldIncrement && filter != nil {
				// Mark nodes that weren't updated in this sync as inactive
				inactiveCount, err := s.markInactiveNodes(ctx, req.TenantID, req.AccountIDs, currentSyncVersion, req.Sources, successfulFlowSources)
				if err != nil {
					s.logger.Error("failed to mark inactive nodes", "error", err)
					// Don't fail the entire operation, just log the error
				} else {
					s.logger.Info("marked inactive nodes",
						"inactive_count", inactiveCount,
						"sync_version", currentSyncVersion)
				}

				// Mark infra edges that weren't re-stamped this sync as inactive.
				// Behavioural edges (CALLS, PUBLISHES_TO, …) are excluded — they go
				// through the time-based MarkStaleEdgesInactive sweep instead.
				inactiveEdgeCount, err := s.markInactiveInfraEdges(req.TenantID, currentSyncVersion)
				if err != nil {
					s.logger.Error("failed to mark inactive infra edges", "error", err)
				} else {
					s.logger.Info("marked inactive infra edges",
						"inactive_count", inactiveEdgeCount,
						"sync_version", currentSyncVersion)
				}

				// Update the filter's sync version
				err = filterRepo.UpdateSyncVersion(ctx, filterID, currentSyncVersion)
				if err != nil {
					s.logger.Error("failed to update sync version in filter", "error", err)
					// Don't fail the entire operation, just log the error
				} else {
					s.logger.Info("updated sync version in filter",
						"filter_id", filterID,
						"new_sync_version", currentSyncVersion)
				}
			} else {
				if err = filterRepo.UpdateSyncTimeOnly(ctx, filterID, currentSyncVersion); err != nil {
					s.logger.Error("failed to update sync time in filter", "error", err)
					// Don't fail the entire operation, just log the error
				}
			}
		}
	}

	return response, nil
}

// groupAccountsByIntegration groups accounts by their unique integration credentials
// Returns a map of integration_key -> []accountIDs
func (s *Service) groupAccountsByIntegration(
	ctx context.Context,
	sourceName string,
	accountIDs []string,
	tenantID string,
) map[string][]string {

	integrationGroups := make(map[string][]string)

	// For now, we'll use a simple approach based on the source name
	// In the future, this could fetch actual credentials and group by them
	// For unknown integration types, build separately for each account
	for _, accountID := range accountIDs {
		integrationKey := fmt.Sprintf("%s_%s", sourceName, accountID)
		integrationGroups[integrationKey] = []string{accountID}
	}

	return integrationGroups
}

// buildFlowRelationships builds flow relationships from all enabled flow sources
// Returns: (edges, errors)
func (s *Service) buildFlowRelationships(
	ctx *security.RequestContext,
	req *BuildRequest,
	existingNodes []*DbNode,
) ([]*DbEdge, []*DbNode, map[string]error) {
	allEdges := make([]*DbEdge, 0)
	allNodes := make([]*DbNode, 0)
	errors := make(map[string]error)

	// Determine which flow sources to use. If caller didn't pick, fall back to
	// all enabled sources (same helper BuildGraphs uses, so the two entry points
	// stay consistent).
	requestedFlowSources := req.FlowSources
	if len(requestedFlowSources) == 0 {
		requestedFlowSources = s.defaultEnabledFlowSources()
	}

	if len(requestedFlowSources) == 0 {
		s.logger.Info("no flow sources to process")
		return allEdges, allNodes, errors
	}

	s.logger.Info("processing flow sources",
		"flow_sources_count", len(requestedFlowSources),
		"existing_nodes_count", len(existingNodes))

	// Set default time range if not provided (last 24 hours)
	timeRange := req.TimeRange
	if timeRange == nil {
		now := time.Now()
		timeRange = &TimeRange{
			StartTime: now.Add(-24 * time.Hour),
			EndTime:   now,
		}
		s.logger.Info("using default 24-hour time range for flow relationships",
			"start_time", timeRange.StartTime,
			"end_time", timeRange.EndTime)
	}

	// Process each requested flow source
	for _, flowSourceName := range requestedFlowSources {
		flowSource, exists := s.flowSources[flowSourceName]
		if !exists {
			s.logger.Warn("flow source not found", "flow_source", flowSourceName)
			errors[flowSourceName] = fmt.Errorf("flow source '%s' not registered", flowSourceName)
			continue
		}

		if !flowSource.IsEnabled() {
			s.logger.Warn("flow source not enabled", "flow_source", flowSourceName)
			continue
		}

		s.logger.Info("building flow relationships from flow source",
			"flow_source", flowSourceName,
			"category", flowSource.GetSourceCategory())

		startTime := time.Now()

		// Build flow source request
		flowReq := &FlowSourceBuildRequest{
			TenantID:        req.TenantID,
			CloudAccountID:  "", // Flow sources work across all accounts
			TimeRange:       timeRange,
			Filters:         req.Filters,
			ExistingNodes:   existingNodes,
			CloudAccountIDs: req.AccountIDs,
		}

		// Build flow relationships
		edges, newNodes, err := flowSource.BuildFlowRelationships(ctx, flowReq)
		if err != nil {
			s.logger.Error("failed to build flow relationships",
				"flow_source", flowSourceName,
				"error", err,
				"duration", time.Since(startTime).Seconds())
			errors[flowSourceName] = err
			continue
		}

		s.logger.Info("successfully built flow relationships",
			"flow_source", flowSourceName,
			"edges_created", len(edges),
			"duration", time.Since(startTime).Seconds())

		// Add edges to the collection
		allEdges = append(allEdges, edges...)
		allNodes = append(allNodes, newNodes...)
	}

	s.logger.Info("completed building flow relationships from all sources",
		"total_edges", len(allEdges),
		"total_nodes", len(allNodes),
		"flow_sources_processed", len(requestedFlowSources),
		"errors", len(errors))

	// ========================================================================
	// POST-PROCESSING: Enrich external services with cloud resources
	// This runs AFTER all flow sources complete, allowing cross-source matching
	// ========================================================================
	if s.externalServiceEnricher != nil {
		// Extract all external service nodes from all flow sources
		externalServices := make([]*DbNode, 0)
		for _, node := range allNodes {
			if node.NodeType == NodeTypeExternalService {
				externalServices = append(externalServices, node)
			}
		}

		if len(externalServices) > 0 {
			s.logger.Info("enriching external services with cloud resources (centralized)",
				"external_services_count", len(externalServices),
				"total_nodes", len(allNodes),
				"total_edges", len(allEdges))

			// Combine existing nodes with new flow nodes for matching
			combinedNodes := append(existingNodes, allNodes...)

			enrichedNodes, enrichedEdges, err := s.externalServiceEnricher.EnrichExternalServices(
				ctx,
				externalServices,
				combinedNodes,
				allEdges,
				req.TenantID,
			)
			if err != nil {
				s.logger.Warn("failed to enrich external services with cloud resources",
					"error", err)
				// Continue with original nodes/edges if enrichment fails
			} else {
				// Replace with enriched nodes and edges
				allNodes = enrichedNodes
				allEdges = enrichedEdges

				s.logger.Info("completed centralized external service enrichment",
					"enriched_nodes_count", len(allNodes),
					"enriched_edges_count", len(allEdges))
			}
		} else {
			s.logger.Debug("no external services to enrich")
		}
	} else {
		s.logger.Debug("no external service enricher registered, skipping enrichment")
	}

	s.logger.Info("completed building flow relationships",
		"total_edges", len(allEdges),
		"total_nodes", len(allNodes),
		"flow_sources_processed", len(requestedFlowSources),
		"errors", len(errors))

	return allEdges, allNodes, errors
}

func (s *Service) SaveEdges(edges []*DbEdge, nodes []*DbNode, syncVersion int64) error {
	if len(edges) == 0 {
		return nil
	}

	// Build node ID mapping from nodes
	nodeIDMap := make(map[string]string)
	for _, node := range nodes {
		nodeIDMap[node.ID] = node.ID
	}

	// Filter edges to only include those with valid node mappings and deduplicate
	edgeMap := make(map[string]*DbEdge)
	for _, edge := range edges {
		sourceID, sourceExists := nodeIDMap[edge.SourceNodeID]
		destID, destExists := nodeIDMap[edge.DestinationNodeID]

		if !sourceExists {
			slog.Warn("Source node not found for edge",
				"source", edge.SourceNodeID,
				"edge_source", edge.Source,
				"edge_relationship", edge.RelationshipType,
				"total_nodes_in_map", len(nodeIDMap))
			continue
		}
		if !destExists {
			slog.Warn("Destination node not found for edge",
				"destination", edge.DestinationNodeID,
				"edge_source", edge.Source,
				"edge_relationship", edge.RelationshipType,
				"total_nodes_in_map", len(nodeIDMap))
			continue
		}

		// Store resolved IDs temporarily
		edge.SourceNodeID = sourceID
		edge.DestinationNodeID = destID

		// Create composite key for deduplication
		compositeKey := fmt.Sprintf("%s:%s:%s",
			edge.SourceNodeID,
			edge.DestinationNodeID,
			edge.TenantID)

		if existing, exists := edgeMap[compositeKey]; exists {
			// Merge properties if the edge already exists
			for k, v := range edge.Properties {
				existing.Properties[k] = v
			}
			existing.UpdatedAt = time.Now()
		} else {
			edgeMap[compositeKey] = edge
		}
	}

	// Convert map back to slice
	validEdges := make([]*DbEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		validEdges = append(validEdges, edge)
	}

	if len(validEdges) == 0 {
		slog.Info("No valid edges to save after node resolution")
		return nil
	}

	// Prepare data for bulk insert
	cols := edgeUpsertCols
	values := make([][]any, len(validEdges))

	now := time.Now()
	for i, edge := range validEdges {
		// Generate ID if not present
		if edge.ID == "" {
			edge.ID = uuid.New().String()
		}

		// Set timestamps if not present
		if edge.CreatedAt.IsZero() {
			edge.CreatedAt = now
		}
		edge.UpdatedAt = now

		// Convert properties to JSON (skip encoding for empty maps)
		propertiesJSON, err := marshalMapOrEmpty(edge.Properties)
		if err != nil {
			return fmt.Errorf("failed to marshal edge properties: %w", err)
		}

		// Set level to "Tenant" for all edges
		if edge.Level == "" {
			edge.Level = "Tenant"
		}

		values[i] = []any{
			edge.ID,
			edge.CreatedAt,
			edge.UpdatedAt,
			edge.SourceNodeID,      // Now contains actual database ID
			edge.DestinationNodeID, // Now contains actual database ID
			string(edge.RelationshipType),
			propertiesJSON,
			edge.CloudAccountID,
			edge.TenantID,
			edge.Level,
			true, // re-touched edges flip back to active; the per-sync sweep (infra) and the time-based sweep (behavioral) mark unseen ones false.
			syncVersion,
		}
	}

	// Sort values by (source_node_id, destination_node_id, relationship_type) — the conflict key columns
	// at indices 3, 4, 5 — to ensure consistent lock acquisition order across concurrent transactions.
	// cols: {"id", "created_at", "updated_at", "source_node_id", "destination_node_id", "relationship_type", ...}
	sort.Slice(values, func(i, j int) bool {
		si, sj := values[i][3].(string), values[j][3].(string)
		if si != sj {
			return si < sj
		}
		di, dj := values[i][4].(string), values[j][4].(string)
		if di != dj {
			return di < dj
		}
		return values[i][5].(string) < values[j][5].(string)
	})

	// Use database manager's bulk insert with conflict resolution
	onConflict := []string{"source_node_id", "destination_node_id", "relationship_type", "cloud_account_id", "tenant_id"}
	onConflictUpdate := []string{"updated_at", "properties", "is_active", "last_sync_version"}

	// Insert in batches to avoid PostgreSQL parameter limit (65535)
	tx, err := s.dbManager.BeginTx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction for edge save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	totalBatches := (len(values) + EdgeBatchSize - 1) / EdgeBatchSize
	for i := 0; i < len(values); i += EdgeBatchSize {
		end := i + EdgeBatchSize
		if end > len(values) {
			end = len(values)
		}

		batch := values[i:end]
		_, err := s.dbManager.Insert(tx, "knowledge_graph_edge", onConflict, onConflictUpdate, nil, cols, batch...)
		if err != nil {
			return fmt.Errorf("failed to upsert edges (batch %d-%d): %w", i, end, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit edge transaction: %w", err)
	}

	slog.Info("Successfully saved edges to knowledge graph", "count", len(validEdges), "batches", totalBatches)
	return nil
}

func (s *Service) SaveNodes(nodes []*DbNode, syncVersion int64) error {
	if len(nodes) == 0 {
		return nil
	}

	// Deduplicate nodes by unique key to avoid constraint violations
	nodeMap := make(map[string]*DbNode)
	for _, node := range nodes {
		// Use a composite key to ensure uniqueness
		compositeKey := fmt.Sprintf("%s:%s:%s", node.UniqueKey, node.CloudAccountID, node.TenantID)
		if existing, exists := nodeMap[compositeKey]; exists {
			// Merge properties if the node already exists
			for k, v := range node.Properties {
				existing.Properties[k] = v
			}
			existing.UpdatedAt = time.Now()
		} else {
			nodeMap[compositeKey] = node
		}
	}

	// Convert map back to slice
	deduplicatedNodes := make([]*DbNode, 0, len(nodeMap))
	for _, node := range nodeMap {
		deduplicatedNodes = append(deduplicatedNodes, node)
	}

	// Prepare data for bulk insert
	cols := nodeUpsertCols
	values := make([][]any, len(deduplicatedNodes))

	now := time.Now()
	for i, node := range deduplicatedNodes {
		// Generate ID if not present
		if node.ID == "" {
			// Generate deterministic ID based on unique_key, cloud_account_id, and tenant_id
			// This ensures the same node always gets the same ID, preventing primary key conflicts
			compositeKey := fmt.Sprintf("%s:%s:%s", node.UniqueKey, node.CloudAccountID, node.TenantID)
			node.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(compositeKey)).String()
		}

		// Set timestamps if not present
		if node.CreatedAt.IsZero() {
			node.CreatedAt = now
		}
		node.UpdatedAt = now

		// Convert properties, labels, query_attributes to JSON
		// (skip encoding for nil/empty maps to reduce allocations)
		propertiesJSON, err := marshalMapOrEmpty(node.Properties)
		if err != nil {
			return fmt.Errorf("failed to marshal node properties: %w", err)
		}

		labelsJSON, err := marshalStringMapOrEmpty(node.Labels)
		if err != nil {
			return fmt.Errorf("failed to marshal node labels: %w", err)
		}

		queryAttributesJSON, err := marshalMapOrEmpty(node.QueryAttributes)
		if err != nil {
			return fmt.Errorf("failed to marshal node query_attributes: %w", err)
		}

		// Set level to "Tenant" for all nodes
		if node.Level == "" {
			node.Level = "Tenant"
		}

		values[i] = []any{
			node.ID,
			node.CreatedAt,
			node.UpdatedAt,
			propertiesJSON,
			labelsJSON,
			queryAttributesJSON,
			node.CloudAccountID,
			node.TenantID,
			node.UniqueKey,
			node.NodeType,
			node.Level,
			syncVersion,
			true,
			node.Source,
		}
	}

	// Sort values by node ID to ensure consistent lock acquisition order across concurrent
	// transactions — prevents PostgreSQL deadlocks when multiple workers process the same tenant.
	sort.Slice(values, func(i, j int) bool {
		return values[i][0].(string) < values[j][0].(string)
	})

	// Use database manager's bulk insert with conflict resolution
	onConflict := []string{"id"}
	onConflictUpdate := []string{"updated_at", "properties", "labels", "query_attributes", "last_sync_version", "is_active", "source"}

	// Insert in batches to avoid PostgreSQL parameter limit (65535)
	tx, err := s.dbManager.BeginTx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction for node save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	totalBatches := (len(values) + NodeBatchSize - 1) / NodeBatchSize
	for i := 0; i < len(values); i += NodeBatchSize {
		end := i + NodeBatchSize
		if end > len(values) {
			end = len(values)
		}

		batch := values[i:end]
		// Pass the transaction object to Insert
		_, err := s.dbManager.Insert(tx, "knowledge_graph_node", onConflict, onConflictUpdate, nil, cols, batch...)
		if err != nil {
			return fmt.Errorf("failed to upsert nodes (batch %d-%d): %w", i, end, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit node transaction: %w", err)
	}

	slog.Info("Successfully saved nodes to knowledge graph", "count", len(deduplicatedNodes), "batches", totalBatches)
	return nil
}

func (s *Service) saveGraphToDB(graph *Graph, syncVersion int64, logger *slog.Logger) (int, int, error) {
	logger.Info("Saving knowledge graph to database",
		"nodes_count", len(graph.Nodes),
		"edges_count", len(graph.Edges),
		"tenant_id", graph.TenantID,
		"cloud_account_id", graph.CloudAccountID,
		"sync_version", syncVersion)

	// Save nodes first (now using DbNode directly)
	if err := s.SaveNodes(graph.Nodes, syncVersion); err != nil {
		logger.Error("Failed to save nodes to database", "error", err)
		return 0, 0, err
	}

	// Save edges (now using DbEdge directly)
	if err := s.SaveEdges(graph.Edges, graph.Nodes, syncVersion); err != nil {
		logger.Error("Failed to save edges to database", "error", err)
		return len(graph.Nodes), 0, err
	}

	logger.Info("Successfully saved knowledge graph to database",
		"nodes_saved", len(graph.Nodes),
		"edges_saved", len(graph.Edges))

	return len(graph.Nodes), len(graph.Edges), nil
}

// markInactiveNodes flips nodes to is_active = false when they weren't re-stamped
// with the current sync version. The predicate has two branches:
//
//  1. Targeted sync (sources != nil): scope the sweep to those sources only.
//  2. All-sources sync with registered flow sources: admit flow-sourced rows of
//     infra-authoritative types (CronJob, Job, Pod, K8sService, Ingress, Namespace,
//     Node, Workload) IFF the flow source that stamped them ran successfully this
//     cycle. Non-infra flow-sourced rows (Service, ExternalService, etc.) are still
//     protected. Rows from a flow source that errored this cycle are protected to
//     avoid mass-tombstoning on transient failure.
func (s *Service) markInactiveNodes(
	ctx *security.RequestContext,
	tenantID string,
	accountIDs []string,
	syncVersion int64,
	sources []string,
	successfulFlowSources []string,
) (int64, error) {
	query := `
	UPDATE knowledge_graph_node
	SET is_active = false,
		updated_at = NOW()
	WHERE tenant_id = $1
		AND last_sync_version < $2
		AND level = 'Tenant'
		AND is_active = true
	`
	args := []interface{}{tenantID, syncVersion}

	if len(sources) > 0 {
		query += fmt.Sprintf(" AND (properties->>'source') = ANY($%d::text[])", len(args)+1)
		args = append(args, pq.Array(sources))
	} else if len(s.flowSources) > 0 {
		flowSourceNames := make([]string, 0, len(s.flowSources))
		for name := range s.flowSources {
			flowSourceNames = append(flowSourceNames, name)
		}
		infraTypes := make([]string, 0, len(InfraAuthoritativeNodeTypes))
		for nt := range InfraAuthoritativeNodeTypes {
			infraTypes = append(infraTypes, string(nt))
		}
		// Stable ordering for deterministic query plans and test assertions.
		sort.Strings(flowSourceNames)
		sort.Strings(infraTypes)
		successSources := append([]string(nil), successfulFlowSources...)
		sort.Strings(successSources)

		flowIdx := len(args) + 1
		infraIdx := len(args) + 2
		successIdx := len(args) + 3
		query += fmt.Sprintf(`
		AND (
			(properties->>'source') != ALL($%d::text[])
			OR (
				node_type = ANY($%d::text[])
				AND (properties->>'source') = ANY($%d::text[])
			)
		)`, flowIdx, infraIdx, successIdx)
		args = append(args,
			pq.Array(flowSourceNames),
			pq.Array(infraTypes),
			pq.Array(successSources),
		)
	}

	if len(accountIDs) > 0 {
		query += fmt.Sprintf(" AND cloud_account_id = ANY($%d::uuid[])", len(args)+1)
		args = append(args, pq.Array(accountIDs))
	}

	s.logger.Info("marking inactive nodes",
		"tenant_id", tenantID,
		"sync_version", syncVersion,
		"sources", sources,
		"account_ids", accountIDs,
		"successful_flow_sources", successfulFlowSources)

	result, err := s.dbManager.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to mark inactive nodes: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf(errRowsAffected, err)
	}

	s.logger.Info("flow-infra sweep summary",
		"tenant_id", tenantID,
		"sync_version", syncVersion,
		"affected", rowsAffected)

	return rowsAffected, nil
}

// markNodesForInactiveAccounts marks all knowledge graph nodes as inactive for accounts
// that are no longer active (status != 'active') in the cloud_accounts table.
func (s *Service) markNodesForInactiveAccounts(ctx *security.RequestContext, tenantID string) (int64, error) {
	query := `
	UPDATE knowledge_graph_node
	SET is_active = false,
		updated_at = NOW()
	WHERE tenant_id = $1
		AND is_active = true
		AND cloud_account_id IN (
			SELECT id FROM cloud_accounts
			WHERE tenant = $1
			AND status != 'active'
		)
	`

	s.logger.Info("marking nodes for inactive accounts",
		"tenant_id", tenantID)

	result, err := s.dbManager.Exec(query, tenantID)
	if err != nil {
		return 0, fmt.Errorf("failed to mark nodes for inactive accounts: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf(errRowsAffected, err)
	}

	return rowsAffected, nil
}

// behavioralEdgeTypes returns the set of relationship types treated as observed
// behaviour (rather than declared infrastructure). These edges get a time-based
// staleness grace period; everything else is marked inactive immediately when not
// re-stamped during a sync. Falls back to DefaultBehavioralEdgeTypes when the env
// override is empty or unset.
func behavioralEdgeTypes() map[RelationshipType]bool {
	raw := strings.TrimSpace(config.Config.KGBehavioralEdgeTypes)
	if raw == "" {
		return DefaultBehavioralEdgeTypes
	}
	out := make(map[RelationshipType]bool)
	for _, part := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out[RelationshipType(t)] = true
		}
	}
	if len(out) == 0 {
		return DefaultBehavioralEdgeTypes
	}
	return out
}

// behavioralEdgeTypesSlice returns the configured behavioural relationship types
// as a sorted []string for use as a SQL ANY($n::text[]) parameter. Sorted for
// deterministic query plans / test assertions.
func behavioralEdgeTypesSlice() []string {
	set := behavioralEdgeTypes()
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}

// markInactiveInfraEdges flips is_active=false on infra edges (every relationship
// type NOT in the behavioural allowlist) that weren't re-stamped with the current
// sync version. Mirrors markInactiveNodes — runs after each successful sync that
// incremented sync_version. Behavioural edges are excluded; they go through the
// time-based MarkStaleEdgesInactive sweep instead.
func (s *Service) markInactiveInfraEdges(tenantID string, syncVersion int64) (int64, error) {
	query := `
		UPDATE knowledge_graph_edge
		SET is_active = false
		WHERE tenant_id = $1
		  AND last_sync_version < $2
		  AND level = 'Tenant'
		  AND is_active = true
		  AND relationship_type != ALL($3::text[])
	`
	behavioralTypes := behavioralEdgeTypesSlice()

	result, err := s.dbManager.Exec(query, tenantID, syncVersion, pq.Array(behavioralTypes))
	if err != nil {
		return 0, fmt.Errorf("failed to mark inactive infra edges: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf(errRowsAffected, err)
	}
	s.logger.Info("kg: marked inactive infra edges",
		"tenant_id", tenantID,
		"sync_version", syncVersion,
		"behavioral_types_excluded", behavioralTypes,
		"rows", rowsAffected)
	return rowsAffected, nil
}

// MarkStaleEdgesInactive flips is_active=false on behavioural edges (CALLS,
// PUBLISHES_TO, etc.) whose updated_at is older than KGEdgeStaleAfterDays. Infra
// edges are handled by markInactiveInfraEdges inside the sync cycle and are
// excluded here. updated_at is intentionally left untouched so the deletion job
// in nb.CleanupData uses true last-seen time as its retention anchor.
// Tenant-agnostic — same shape as the cleanup queries in nb/service.go.
func (s *Service) MarkStaleEdgesInactive() (int64, error) {
	staleDays := config.Config.KGEdgeStaleAfterDays
	if staleDays <= 0 {
		staleDays = 7
	}
	query := `
		UPDATE knowledge_graph_edge
		SET is_active = false
		WHERE is_active = true
		  AND level = 'Tenant'
		  AND updated_at < NOW() - ($1 * interval '1 day')
		  AND relationship_type = ANY($2::text[])
	`
	behavioralTypes := behavioralEdgeTypesSlice()

	result, err := s.dbManager.Exec(query, staleDays, pq.Array(behavioralTypes))
	if err != nil {
		return 0, fmt.Errorf("failed to mark stale behavioural edges inactive: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf(errRowsAffected, err)
	}
	s.logger.Info("kg: marked stale behavioural edges inactive",
		"rows", rowsAffected,
		"stale_days", staleDays,
		"behavioral_types", behavioralTypes)
	return rowsAffected, nil
}

// GetGraph retrieves a knowledge graph from a specific source
func (s *Service) GetGraph(reqCtx *security.RequestContext, req *QueryRequest) (*QueryResponse, error) {
	if req.Source == "" {
		return nil, fmt.Errorf("source must be specified")
	}

	source, exists := s.sources[req.Source]
	if !exists {
		return nil, fmt.Errorf("source '%s' not found", req.Source)
	}

	if !source.IsEnabled() {
		return nil, fmt.Errorf("source '%s' not enabled", req.Source)
	}

	buildReq := &SourceBuildRequest{
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
		Filters:        req.Filters,
	}

	graph, err := source.BuildGraph(reqCtx, buildReq)
	if err != nil {
		return &QueryResponse{
			Error: fmt.Sprintf("failed to build graph: %v", err),
		}, err
	}
	// Apply node type filter if specified
	if req.NodeType != "" {
		graph = s.filterGraphByNodeType(graph, req.NodeType)
	}

	graph.GeneratedAt = time.Now()
	graph.Metadata = s.calculateMetadata(graph)
	return &QueryResponse{
		Graph:          graph,
		KnowledgeGraph: ConvertGraphToKnowledgeGraph(graph),
	}, nil
}

// ListSources returns information about all registered sources
func (s *Service) ListSources() []SourceInfo {
	sources := make([]SourceInfo, 0, len(s.sources))
	for name, source := range s.sources {
		sources = append(sources, SourceInfo{
			Name:    name,
			Enabled: source.IsEnabled(),
		})
	}
	return sources
}

// SourceInfo contains information about a source
type SourceInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// calculateMetadata calculates metadata for a graph by collecting sources from nodes
func (s *Service) calculateMetadata(graph *Graph) Metadata {
	nodeTypeBreakdown := make(map[NodeType]int)
	sourcesMap := make(map[string]bool)

	for _, node := range graph.Nodes {
		nodeTypeBreakdown[node.NodeType]++
		if node.Source != "" {
			sourcesMap[node.Source] = true
		}
	}

	// Convert sources map to slice
	sources := make([]string, 0, len(sourcesMap))
	for source := range sourcesMap {
		sources = append(sources, source)
	}

	return Metadata{
		NodeCount:         len(graph.Nodes),
		EdgeCount:         len(graph.Edges),
		NodeTypeBreakdown: nodeTypeBreakdown,
		Sources:           sources,
	}
}

// queryAccountMappingsFromDB queries account ID -> account name mappings from the DB.
// It does not touch the cache; callers are responsible for cache updates.
func (s *Service) queryAccountMappingsFromDB(tenantID string) (map[string]string, error) {
	query := `
		SELECT id, account_name
		FROM cloud_accounts
		WHERE tenant = $1
	`

	rows, err := s.dbManager.Query(query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query account mappings: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows", "error", closeErr)
		}
	}()

	accountMappings := make(map[string]string)
	for rows.Next() {
		var accountID, accountName string
		if err := rows.Scan(&accountID, &accountName); err != nil {
			return nil, fmt.Errorf("failed to scan account mapping: %w", err)
		}
		accountMappings[accountID] = accountName
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating account mapping rows: %w", err)
	}

	return accountMappings, nil
}

// getAccountMappings returns cached account mappings, fetching from DB on cache miss or expiry.
// Uses double-checked locking to prevent thundering herd under concurrent requests.
func (s *Service) getAccountMappings(tenantID string) (map[string]string, error) {
	// Fast path: return from cache if valid.
	s.accountMappingsCache.mu.RLock()
	if exp, ok := s.accountMappingsCache.expiresAt[tenantID]; ok && time.Now().Before(exp) {
		cached := s.accountMappingsCache.data[tenantID]
		s.accountMappingsCache.mu.RUnlock()
		return cached, nil
	}
	s.accountMappingsCache.mu.RUnlock()

	// Slow path: acquire write lock and double-check before fetching from DB.
	s.accountMappingsCache.mu.Lock()
	defer s.accountMappingsCache.mu.Unlock()

	// Another goroutine may have already populated the cache while we waited.
	if exp, ok := s.accountMappingsCache.expiresAt[tenantID]; ok && time.Now().Before(exp) {
		return s.accountMappingsCache.data[tenantID], nil
	}

	accountMappings, err := s.queryAccountMappingsFromDB(tenantID)
	if err != nil {
		return nil, err
	}
	s.accountMappingsCache.data[tenantID] = accountMappings
	s.accountMappingsCache.expiresAt[tenantID] = time.Now().Add(accountMappingsCacheTTL)
	return accountMappings, nil
}

// refreshAccountMappings forces a DB fetch and updates the cache, bypassing TTL.
// Use when a known account_id is missing from the cached result.
func (s *Service) refreshAccountMappings(tenantID string) (map[string]string, error) {
	s.logger.Debug("refreshing account mappings cache", "tenant_id", tenantID)
	s.accountMappingsCache.mu.Lock()
	defer s.accountMappingsCache.mu.Unlock()

	accountMappings, err := s.queryAccountMappingsFromDB(tenantID)
	if err != nil {
		return nil, err
	}
	s.accountMappingsCache.data[tenantID] = accountMappings
	s.accountMappingsCache.expiresAt[tenantID] = time.Now().Add(accountMappingsCacheTTL)
	return accountMappings, nil
}

// GetCompleteGraphFromDatabase retrieves the complete knowledge graph from the database
// This retrieves all previously saved nodes and edges for a given tenant and cloud account
func (s *Service) GetCompleteGraphFromDatabase(reqCtx *security.RequestContext, tenantID string) (KnowledgeGraph, error) {
	if tenantID == "" {
		return KnowledgeGraph{}, fmt.Errorf("tenant_id is required")
	}
	s.logger.Info("retrieving complete knowledge graph from database",
		"tenant_id", tenantID)

	// Get account mappings (account_id -> account_name) for UI display
	accountMappings, err := s.getAccountMappings(tenantID)
	if err != nil {
		s.logger.Warn(msgAccountMappingsFallback, "error", err)
		accountMappings = make(map[string]string)
	}

	// Get all nodes for this tenant using local method
	dbNodes, err := s.GetNodesByTenant(tenantID)
	if err != nil {
		s.logger.Error("failed to get nodes from database", "error", err)
		return KnowledgeGraph{}, fmt.Errorf("failed to retrieve nodes: %w", err)
	}

	// Get all edges for this tenant using local method
	dbEdges, err := s.GetEdgesByTenant(tenantID)
	if err != nil {
		s.logger.Error("failed to get edges from database", "error", err)
		return KnowledgeGraph{}, fmt.Errorf("failed to retrieve edges: %w", err)
	}

	s.logger.Info("successfully retrieved complete knowledge graph from database",
		"tenant_id", tenantID,
		"nodes_count", len(dbNodes),
		"edges_count", len(dbEdges))

	// Convert to KnowledgeGraph format with account name conversion for UI
	return KnowledgeGraph{
		Nodes:       ConvertDbNodesToKgNodesWithAccountNames(dbNodes, accountMappings),
		Edges:       ConvertDbEdgesToKgEdges(dbEdges),
		TenantID:    tenantID,
		GeneratedAt: time.Now(),
	}, nil
}

// GetCompleteGraphFromDatabaseWithFilters retrieves the knowledge graph with optional filters
// Filters are applied with AND logic: account_id AND node_type AND labels AND label_keys
func (s *Service) GetCompleteGraphFromDatabaseWithFilters(reqCtx *security.RequestContext, tenantID string, filters *GraphFilters) (KnowledgeGraph, error) {
	if tenantID == "" {
		return KnowledgeGraph{}, fmt.Errorf("tenant_id is required")
	}

	// If no filters provided, use the regular method
	if filters == nil || (len(filters.AccountIDs) == 0 && len(filters.NodeTypes) == 0 && len(filters.Labels) == 0 && len(filters.LabelKeys) == 0 && len(filters.Attributes) == 0 && len(filters.AttributeKeys) == 0) {
		return s.GetCompleteGraphFromDatabase(reqCtx, tenantID)
	}

	s.logger.Info("retrieving filtered knowledge graph from database",
		"tenant_id", tenantID,
		"account_ids", filters.AccountIDs,
		"node_types", filters.NodeTypes,
		"label_filters", len(filters.Labels),
		"label_key_filters", len(filters.LabelKeys),
		"attribute_filters", len(filters.Attributes),
		"attribute_key_filters", len(filters.AttributeKeys))

	// Get account mappings (account_id -> account_name) for UI display
	accountMappings, err := s.getAccountMappings(tenantID)
	if err != nil {
		s.logger.Warn(msgAccountMappingsFallback, "error", err)
		accountMappings = make(map[string]string)
	}

	// Get nodes with SQL-level filtering for account_id and node_type
	dbNodes, err := s.GetNodesByTenantWithFilters(tenantID, filters)
	if err != nil {
		s.logger.Error("failed to get filtered nodes from database", "error", err)
		return KnowledgeGraph{}, fmt.Errorf("failed to retrieve filtered nodes: %w", err)
	}

	// Get all edges for this tenant
	dbEdges, err := s.GetEdgesByTenant(tenantID)
	if err != nil {
		s.logger.Error("failed to get edges from database", "error", err)
		return KnowledgeGraph{}, fmt.Errorf("failed to retrieve edges: %w", err)
	}

	// Filter edges: only include edges where both source and destination nodes are in the filtered set
	filteredEdges := s.filterEdgesByNodes(dbEdges, dbNodes)

	s.logger.Info("successfully retrieved filtered knowledge graph from database",
		"tenant_id", tenantID,
		"nodes_count", len(dbNodes),
		"edges_count", len(filteredEdges))

	// Convert to KnowledgeGraph format with account name conversion for UI
	return KnowledgeGraph{
		Nodes:       ConvertDbNodesToKgNodesWithAccountNames(dbNodes, accountMappings),
		Edges:       ConvertDbEdgesToKgEdges(filteredEdges),
		TenantID:    tenantID,
		GeneratedAt: time.Now(),
	}, nil
}

// filterGraphByNodeType filters a graph to only include nodes of a specific type
func (s *Service) filterGraphByNodeType(graph *Graph, nodeType NodeType) *Graph {
	filtered := &Graph{
		Nodes:          make([]*DbNode, 0),
		Edges:          make([]*DbEdge, 0),
		TenantID:       graph.TenantID,
		CloudAccountID: graph.CloudAccountID,
		GeneratedAt:    graph.GeneratedAt,
	}

	// Create a map of node IDs that match the filter
	nodeIDs := make(map[string]bool)
	for _, node := range graph.Nodes {
		if node.NodeType == nodeType {
			filtered.Nodes = append(filtered.Nodes, node)
			nodeIDs[node.ID] = true
		}
	}

	// Include edges where both nodes match the filter
	for _, edge := range graph.Edges {
		if nodeIDs[edge.SourceNodeID] && nodeIDs[edge.DestinationNodeID] {
			filtered.Edges = append(filtered.Edges, edge)
		}
	}
	return filtered
}

// GetNodesByTenant retrieves all nodes for a specific tenant and cloud account from the database
func (s *Service) GetNodesByTenant(tenantID string) ([]*DbNode, error) {
	if s.dbManager == nil {
		return nil, fmt.Errorf("database manager not initialized")
	}

	query := `
		SELECT id, created_at, updated_at, properties, labels, query_attributes, cloud_account_id, tenant_id, unique_key, node_type, level, COALESCE(source, '')
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND level = 'Tenant' AND (NOT jsonb_exists(properties, 'inferred') OR properties->>'inferred' = 'false') AND is_active = true
		ORDER BY created_at DESC
	`

	rows, err := s.dbManager.Query(query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var nodes []*DbNode
	for rows.Next() {
		node := &DbNode{}
		var propertiesJSON []byte
		var labelsJSON []byte
		var queryAttributesJSON []byte

		err := rows.Scan(
			&node.ID,
			&node.CreatedAt,
			&node.UpdatedAt,
			&propertiesJSON,
			&labelsJSON,
			&queryAttributesJSON,
			&node.CloudAccountID,
			&node.TenantID,
			&node.UniqueKey,
			&node.NodeType,
			&node.Level,
			&node.Source,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}

		// Parse properties JSON
		if err := json.Unmarshal(propertiesJSON, &node.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node properties: %w", err)
		}

		// Parse labels JSON
		if err := json.Unmarshal(labelsJSON, &node.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node labels: %w", err)
		}

		// Parse query_attributes JSON
		if err := json.Unmarshal(queryAttributesJSON, &node.QueryAttributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node query_attributes: %w", err)
		}

		nodes = append(nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node rows: %w", err)
	}

	return nodes, nil
}

// GetEdgesByTenant retrieves all edges for a specific tenant and cloud account from the database
func (s *Service) GetEdgesByTenant(tenantID string) ([]*DbEdge, error) {
	if s.dbManager == nil {
		return nil, fmt.Errorf("database manager not initialized")
	}

	query := `
		SELECT
			e.id, e.created_at, e.updated_at, e.relationship_type, e.properties,
			e.cloud_account_id, e.tenant_id, e.source_node_id, e.destination_node_id, e.level
		FROM knowledge_graph_edge e
		WHERE e.tenant_id = $1 AND e.level = 'Tenant' AND e.is_active = true
		ORDER BY e.created_at DESC
	`

	rows, err := s.dbManager.Query(query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query edges: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var edges []*DbEdge
	for rows.Next() {
		edge := &DbEdge{}
		var propertiesJSON []byte
		var relationshipType string

		err := rows.Scan(
			&edge.ID,
			&edge.CreatedAt,
			&edge.UpdatedAt,
			&relationshipType,
			&propertiesJSON,
			&edge.CloudAccountID,
			&edge.TenantID,
			&edge.SourceNodeID,      // Actually unique_key from join
			&edge.DestinationNodeID, // Actually unique_key from join
			&edge.Level,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
		}

		// Parse properties JSON
		if err := json.Unmarshal(propertiesJSON, &edge.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal edge properties: %w", err)
		}

		edge.RelationshipType = RelationshipType(relationshipType)
		edges = append(edges, edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating edge rows: %w", err)
	}

	return edges, nil
}

// GetNodesByTenantWithFilters retrieves nodes for a tenant with optional filtering
// Filters are applied with AND logic for account_id, node_type, labels, and label_keys
func (s *Service) GetNodesByTenantWithFilters(tenantID string, filters *GraphFilters) ([]*DbNode, error) {
	if s.dbManager == nil {
		return nil, fmt.Errorf("database manager not initialized")
	}

	// Build dynamic query with filters
	query := `
		SELECT id, created_at, updated_at, properties, labels, query_attributes, cloud_account_id, tenant_id, unique_key, node_type, level, COALESCE(source, '')
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND level = 'Tenant' AND is_active = true AND (NOT jsonb_exists(properties, 'inferred') OR properties->>'inferred' = 'false')
	`
	args := []interface{}{tenantID}
	argCounter := 2

	// Add account_id filter if provided
	if len(filters.AccountIDs) > 0 {
		placeholders := make([]string, len(filters.AccountIDs))
		for i, accountID := range filters.AccountIDs {
			placeholders[i] = fmt.Sprintf("$%d", argCounter)
			args = append(args, accountID)
			argCounter++
		}
		query += fmt.Sprintf(" AND cloud_account_id IN (%s)", strings.Join(placeholders, ","))
	}

	// For node_type filtering, we need to filter by unique_key pattern since node_type is derived
	// Format of unique_key: "source:NodeType:name:environment"
	if len(filters.NodeTypes) > 0 {
		typeConditions := make([]string, len(filters.NodeTypes))
		for i, nodeType := range filters.NodeTypes {
			typeConditions[i] = fmt.Sprintf("node_type = $%d", argCounter)
			args = append(args, string(nodeType))
			argCounter++
		}
		query += fmt.Sprintf(" AND (%s)", strings.Join(typeConditions, " OR "))
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes with filters: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var nodes []*DbNode
	for rows.Next() {
		node := &DbNode{}
		var propertiesJSON []byte
		var labelsJSON []byte
		var queryAttributesJSON []byte

		err := rows.Scan(
			&node.ID,
			&node.CreatedAt,
			&node.UpdatedAt,
			&propertiesJSON,
			&labelsJSON,
			&queryAttributesJSON,
			&node.CloudAccountID,
			&node.TenantID,
			&node.UniqueKey,
			&node.NodeType,
			&node.Level,
			&node.Source,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}

		// Parse properties JSON
		if err := json.Unmarshal(propertiesJSON, &node.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node properties: %w", err)
		}

		// Parse labels JSON
		if err := json.Unmarshal(labelsJSON, &node.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node labels: %w", err)
		}

		// Parse query_attributes JSON
		if err := json.Unmarshal(queryAttributesJSON, &node.QueryAttributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node query_attributes: %w", err)
		}

		// Apply label and attribute filters (in-memory filtering since labels and attributes are in JSON properties)
		if !s.matchesFilters(node, filters) {
			continue
		}

		nodes = append(nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node rows: %w", err)
	}

	return nodes, nil
}

// matchesFilters checks if a node matches the label, label_key, attribute, and attribute_key filters
// Returns true if the node matches all filters (AND logic)
func (s *Service) matchesFilters(node *DbNode, filters *GraphFilters) bool {
	// Use labels from dedicated Labels field
	labels := node.Labels
	if labels == nil {
		labels = make(map[string]string)
	}

	// Check label key-value filters (all must match)
	for key, expectedValue := range filters.Labels {
		actualValue, exists := labels[key]
		if !exists || actualValue != expectedValue {
			return false
		}
	}

	// Check label key existence filters (all keys must exist)
	for _, key := range filters.LabelKeys {
		if _, exists := labels[key]; !exists {
			return false
		}
	}

	// Use query_attributes from dedicated QueryAttributes field
	queryAttributes := node.QueryAttributes
	if queryAttributes == nil {
		queryAttributes = make(map[string]interface{})
	}

	// Check attribute key-value filters (all must match)
	for key, expectedValue := range filters.Attributes {
		actualValue, exists := queryAttributes[key]
		if !exists {
			return false
		}
		// Convert actual value to string for comparison
		actualValueStr, ok := actualValue.(string)
		if !ok {
			// Try to convert to string if not already a string
			actualValueStr = fmt.Sprintf("%v", actualValue)
		}
		if actualValueStr != expectedValue {
			return false
		}
	}

	// Check attribute key existence filters (all keys must exist)
	for _, key := range filters.AttributeKeys {
		if _, exists := queryAttributes[key]; !exists {
			return false
		}
	}

	return true
}

// filterEdgesByNodes filters edges to only include those where both source and destination nodes are in the provided node set
func (s *Service) filterEdgesByNodes(edges []*DbEdge, nodes []*DbNode) []*DbEdge {
	// Create a set of node IDs for quick lookup
	nodeIDSet := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		nodeIDSet[node.ID] = true
	}

	// Filter edges where both source and destination are in the node set
	filteredEdges := make([]*DbEdge, 0)
	for _, edge := range edges {
		if nodeIDSet[edge.SourceNodeID] && nodeIDSet[edge.DestinationNodeID] {
			filteredEdges = append(filteredEdges, edge)
		}
	}

	return filteredEdges
}

// GetNodeNeighbors retrieves a node and all its neighboring nodes with their connecting edges
func (s *Service) GetNodeNeighbors(reqCtx *security.RequestContext, nodeID string) (KnowledgeGraph, error) {
	if nodeID == "" {
		return KnowledgeGraph{}, fmt.Errorf("node_id is required")
	}

	if s.dbManager == nil {
		return KnowledgeGraph{}, fmt.Errorf("database manager not initialized")
	}

	s.logger.Info("retrieving node and its neighbors from database", "node_id", nodeID)

	// Step 1: Get the target node
	nodeQuery := `
		SELECT id, created_at, updated_at, properties, labels, query_attributes, cloud_account_id, tenant_id, unique_key, node_type, level, COALESCE(source, '')
		FROM knowledge_graph_node
		WHERE id = $1 AND level = 'Tenant'
	`

	rows, err := s.dbManager.Query(nodeQuery, nodeID)
	if err != nil {
		return KnowledgeGraph{}, fmt.Errorf("failed to query node: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var targetNode *DbNode
	if rows.Next() {
		targetNode = &DbNode{}
		var propertiesJSON []byte
		var labelsJSON []byte
		var queryAttributesJSON []byte

		err := rows.Scan(
			&targetNode.ID,
			&targetNode.CreatedAt,
			&targetNode.UpdatedAt,
			&propertiesJSON,
			&labelsJSON,
			&queryAttributesJSON,
			&targetNode.CloudAccountID,
			&targetNode.TenantID,
			&targetNode.UniqueKey,
			&targetNode.NodeType,
			&targetNode.Level,
			&targetNode.Source,
		)
		if err != nil {
			return KnowledgeGraph{}, fmt.Errorf("failed to scan node: %w", err)
		}

		// Parse properties JSON
		if err := json.Unmarshal(propertiesJSON, &targetNode.Properties); err != nil {
			return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal node properties: %w", err)
		}

		// Parse labels JSON
		if err := json.Unmarshal(labelsJSON, &targetNode.Labels); err != nil {
			return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal node labels: %w", err)
		}

		// Parse query_attributes JSON
		if err := json.Unmarshal(queryAttributesJSON, &targetNode.QueryAttributes); err != nil {
			return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal node query_attributes: %w", err)
		}
	}

	if err := rows.Err(); err != nil {
		return KnowledgeGraph{}, fmt.Errorf("error iterating node rows: %w", err)
	}

	if targetNode == nil {
		return KnowledgeGraph{}, fmt.Errorf("node not found with id: %s", nodeID)
	}

	// Step 2: Get all edges connected to this node (both incoming and outgoing)
	edgesQuery := `
		SELECT
			e.id, e.created_at, e.updated_at, e.relationship_type, e.properties,
			e.cloud_account_id, e.tenant_id, e.source_node_id, e.destination_node_id, e.level
		FROM knowledge_graph_edge e
		WHERE (e.source_node_id = $1 OR e.destination_node_id = $1) AND e.level = 'Tenant' AND e.is_active = true
		ORDER BY e.created_at DESC
	`

	edgeRows, err := s.dbManager.Query(edgesQuery, nodeID)
	if err != nil {
		return KnowledgeGraph{}, fmt.Errorf("failed to query edges: %w", err)
	}
	defer func() {
		if closeErr := edgeRows.Close(); closeErr != nil {
			slog.Warn("Failed to close edge rows", "error", closeErr)
		}
	}()

	var edges []*DbEdge
	neighborNodeIDs := make(map[string]bool)

	for edgeRows.Next() {
		edge := &DbEdge{}
		var propertiesJSON []byte
		var relationshipType string

		err := edgeRows.Scan(
			&edge.ID,
			&edge.CreatedAt,
			&edge.UpdatedAt,
			&relationshipType,
			&propertiesJSON,
			&edge.CloudAccountID,
			&edge.TenantID,
			&edge.SourceNodeID,
			&edge.DestinationNodeID,
			&edge.Level,
		)
		if err != nil {
			return KnowledgeGraph{}, fmt.Errorf("failed to scan edge: %w", err)
		}

		// Parse properties JSON
		if err := json.Unmarshal(propertiesJSON, &edge.Properties); err != nil {
			return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal edge properties: %w", err)
		}

		edge.RelationshipType = RelationshipType(relationshipType)
		edges = append(edges, edge)

		// Collect neighbor node IDs
		if edge.SourceNodeID == nodeID {
			neighborNodeIDs[edge.DestinationNodeID] = true
		} else {
			neighborNodeIDs[edge.SourceNodeID] = true
		}
	}

	if err := edgeRows.Err(); err != nil {
		return KnowledgeGraph{}, fmt.Errorf("error iterating edge rows: %w", err)
	}

	// Step 3: Get all neighbor nodes
	var neighborNodes []*DbNode

	if len(neighborNodeIDs) > 0 {
		// Build query with IN clause
		neighborIDs := make([]string, 0, len(neighborNodeIDs))
		for id := range neighborNodeIDs {
			neighborIDs = append(neighborIDs, id)
		}

		// Create placeholders for IN clause
		placeholders := make([]string, len(neighborIDs))
		args := make([]interface{}, len(neighborIDs))
		for i, id := range neighborIDs {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}

		neighborsQuery := fmt.Sprintf(`
			SELECT id, created_at, updated_at, properties, labels, query_attributes, cloud_account_id, tenant_id, unique_key, node_type, level, COALESCE(source, '')
			FROM knowledge_graph_node
			WHERE id IN (%s) AND level = 'Tenant'
		`, strings.Join(placeholders, ","))

		neighborRows, err := s.dbManager.Query(neighborsQuery, args...)
		if err != nil {
			return KnowledgeGraph{}, fmt.Errorf("failed to query neighbor nodes: %w", err)
		}
		defer func() {
			if closeErr := neighborRows.Close(); closeErr != nil {
				slog.Warn("Failed to close neighbor rows", "error", closeErr)
			}
		}()

		for neighborRows.Next() {
			node := &DbNode{}
			var propertiesJSON []byte
			var labelsJSON []byte
			var queryAttributesJSON []byte

			err := neighborRows.Scan(
				&node.ID,
				&node.CreatedAt,
				&node.UpdatedAt,
				&propertiesJSON,
				&labelsJSON,
				&queryAttributesJSON,
				&node.CloudAccountID,
				&node.TenantID,
				&node.UniqueKey,
				&node.NodeType,
				&node.Level,
				&node.Source,
			)
			if err != nil {
				return KnowledgeGraph{}, fmt.Errorf("failed to scan neighbor node: %w", err)
			}

			// Parse properties JSON
			if err := json.Unmarshal(propertiesJSON, &node.Properties); err != nil {
				return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal neighbor node properties: %w", err)
			}

			// Parse labels JSON
			if err := json.Unmarshal(labelsJSON, &node.Labels); err != nil {
				return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal neighbor node labels: %w", err)
			}

			// Parse query_attributes JSON
			if err := json.Unmarshal(queryAttributesJSON, &node.QueryAttributes); err != nil {
				return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal neighbor node query_attributes: %w", err)
			}

			neighborNodes = append(neighborNodes, node)
		}

		if err := neighborRows.Err(); err != nil {
			return KnowledgeGraph{}, fmt.Errorf("error iterating neighbor node rows: %w", err)
		}
	}

	// Step 4: Combine target node with neighbor nodes
	allNodes := append([]*DbNode{targetNode}, neighborNodes...)

	s.logger.Info("successfully retrieved node neighbors",
		"node_id", nodeID,
		"total_nodes", len(allNodes),
		"neighbor_count", len(neighborNodes),
		"edges_count", len(edges))

	// Get account mappings for UI display
	accountMappings, err := s.getAccountMappings(targetNode.TenantID)
	if err != nil {
		s.logger.Warn(msgAccountMappingsFallback, "error", err)
		accountMappings = make(map[string]string)
	}

	// Convert to KnowledgeGraph format with account name conversion for UI
	return KnowledgeGraph{
		Nodes:       ConvertDbNodesToKgNodesWithAccountNames(allNodes, accountMappings),
		Edges:       ConvertDbEdgesToKgEdges(edges),
		TenantID:    targetNode.TenantID,
		AccountID:   targetNode.CloudAccountID,
		GeneratedAt: time.Now(),
	}, nil
}

// GetMultipleNodeNeighbors retrieves multiple nodes and their neighbors from the database.
// The levels parameter controls how deep to traverse: 1 = direct neighbors, 2 = neighbors of neighbors, 3 = 3 hops.
// The nodeTypes parameter filters neighbor nodes by type (original nodes are always included regardless of type).
//
// When subgraph is true (default), every edge whose both endpoints are in the discovered set is
// returned (induced subgraph: includes back-edges, sibling-to-sibling, cycles). When subgraph is
// false, only edges that connect consecutive BFS layers are returned — i.e. a spanning forest
// rooted at nodeIDs, suitable for tree-style rendering.
func (s *Service) GetMultipleNodeNeighbors(reqCtx *security.RequestContext, nodeIDs []string, levels int, nodeTypes []NodeType, subgraph bool) (KnowledgeGraph, error) {
	if len(nodeIDs) == 0 {
		return KnowledgeGraph{}, fmt.Errorf("at least one node_id is required")
	}

	if s.dbManager == nil {
		return KnowledgeGraph{}, fmt.Errorf("database manager not initialized")
	}

	// Validate and default levels
	if levels < 1 {
		levels = 1
	}
	if levels > 3 {
		levels = 3
	}

	// Defense in depth: scope the recursive CTE to the caller's tenant so a caller
	// cannot retrieve nodes from another tenant by guessing UUIDs. Required even
	// though node ids are UUIDs — globally unique does not imply authorized.
	tenantID := reqCtx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return KnowledgeGraph{}, fmt.Errorf("tenant_id is required")
	}

	s.logger.Info("retrieving multiple nodes and their neighbors from database",
		"tenant_id", tenantID,
		"node_count", len(nodeIDs),
		"levels", levels,
		"node_types_filter", nodeTypes,
		"subgraph", subgraph)

	// Step 1: Use recursive CTE to discover all node IDs up to N levels.
	// Also returns the edges the BFS walked and each node's minimum BFS depth, used by
	// the spanning-tree edge filter when subgraph is false.
	discoveredNodeIDs, traversedEdgeIDs, nodeMinDepth, err := s.discoverNeighborNodesRecursive(tenantID, nodeIDs, levels, nodeTypes)
	if err != nil {
		return KnowledgeGraph{}, fmt.Errorf("failed to discover neighbor nodes: %w", err)
	}

	if len(discoveredNodeIDs) == 0 {
		return KnowledgeGraph{}, fmt.Errorf("no nodes found with provided ids")
	}

	// Step 2: Fetch all discovered nodes
	allNodes, err := s.fetchNodesByIDs(discoveredNodeIDs)
	if err != nil {
		return KnowledgeGraph{}, fmt.Errorf("failed to fetch nodes: %w", err)
	}

	if len(allNodes) == 0 {
		return KnowledgeGraph{}, fmt.Errorf("no nodes found with provided ids")
	}

	// Step 3: Fetch edges in the requested mode.
	var edges []*DbEdge
	if subgraph {
		// Induced-subgraph mode: every edge whose both endpoints lie in the discovered set.
		edges, err = s.fetchEdgesBetweenNodes(discoveredNodeIDs)
	} else {
		// Spanning-tree mode: only edges the BFS actually walked, then drop edges between
		// nodes at the same BFS depth (siblings) so the result is a layered DAG / forest
		// rooted at the input nodeIDs. Direction is "Both" because the CTE traverses edges
		// regardless of source/destination orientation.
		edges, err = s.fetchEdgesByIDs(traversedEdgeIDs, discoveredNodeIDs, nil)
		if err == nil {
			edges = filterLayeredEdges(edges, nodeMinDepth, TraverseDirectionBoth)
		}
	}
	if err != nil {
		return KnowledgeGraph{}, fmt.Errorf("failed to fetch edges: %w", err)
	}

	// Use first node's cloud_account_id for graph metadata. tenantID is already
	// resolved from the security context above.
	cloudAccountID := allNodes[0].CloudAccountID

	s.logger.Info("successfully retrieved multi-level node neighbors",
		"requested_node_count", len(nodeIDs),
		"levels", levels,
		"subgraph", subgraph,
		"total_nodes", len(allNodes),
		"edges_count", len(edges))

	// Get account mappings for UI display
	accountMappings, err := s.getAccountMappings(tenantID)
	if err != nil {
		s.logger.Warn(msgAccountMappingsFallback, "error", err)
		accountMappings = make(map[string]string)
	}

	// Convert to KnowledgeGraph format with account name conversion for UI
	return KnowledgeGraph{
		Nodes:       ConvertDbNodesToKgNodesWithAccountNames(allNodes, accountMappings),
		Edges:       ConvertDbEdgesToKgEdges(edges),
		TenantID:    tenantID,
		AccountID:   cloudAccountID,
		GeneratedAt: time.Now(),
	}, nil
}

// discoverNeighborNodesRecursive uses a PostgreSQL recursive CTE to find all nodes
// within N levels of the input nodes, handling cycles automatically via visited_path array.
// nodeTypes filters neighbor nodes by type (original nodes are always included regardless of type).
//
// tenantID scopes every traversal step (seed nodes, recursive nodes, and walked edges) to
// the caller's tenant — required to prevent cross-tenant data exfiltration via guessed UUIDs.
//
// Returns:
//   - discoveredIDs: the set of node IDs reached from any seed (seeds themselves + neighbours).
//   - traversedEdgeIDs: edge IDs the BFS actually walked (empty for the base case).
//   - nodeMinDepth: minimum hop count from any seed to each discovered node. Used by the
//     spanning-tree edge filter (filterLayeredEdges) when callers want a tree response
//     instead of the induced subgraph.
func (s *Service) discoverNeighborNodesRecursive(tenantID string, nodeIDs []string, levels int, nodeTypes []NodeType) (discoveredIDs []string, traversedEdgeIDs []string, nodeMinDepth map[string]int, err error) {
	// Convert NodeType slice to string slice for PostgreSQL
	nodeTypeStrings := make([]string, len(nodeTypes))
	for i, nt := range nodeTypes {
		nodeTypeStrings[i] = string(nt)
	}

	// Build the query with optional node type filter for neighbors.
	// Base case: always include original nodes regardless of type (edge_id is NULL).
	// Recursive case: filter by node_types if specified, project the walked edge id.
	var query string
	var rows *sqlx.Rows

	// Param layout (both CTE branches):
	//   $1 = seed node ids, $2 = tenant id, $3 = max depth, $4 = node type filter (with-type only).
	if len(nodeTypes) > 0 {
		// With node type filter - only filter neighbor nodes, not the original nodes
		query = `
			WITH RECURSIVE neighbor_traversal AS (
				-- Base case: Start with input nodes at depth 0 (no type filter)
				SELECT
					id AS node_id,
					NULL::uuid AS edge_id,
					0 AS depth,
					ARRAY[id] AS visited_path
				FROM knowledge_graph_node
				WHERE id = ANY($1::uuid[])
				  AND tenant_id = $2
				  AND level = 'Tenant'
				  AND is_active = true

				UNION

				-- Recursive case: Find neighbors at next depth (with type filter)
				SELECT
					CASE
						WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						ELSE e.source_node_id
					END AS node_id,
					e.id AS edge_id,
					nt.depth + 1 AS depth,
					nt.visited_path || CASE
						WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						ELSE e.source_node_id
					END AS visited_path
				FROM neighbor_traversal nt
				JOIN knowledge_graph_edge e ON (
					e.source_node_id = nt.node_id OR e.destination_node_id = nt.node_id
				)
				JOIN knowledge_graph_node n ON (
					n.id = CASE
						WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						ELSE e.source_node_id
					END
				)
				WHERE nt.depth < $3
				  AND e.tenant_id = $2
				  AND e.level = 'Tenant'
				  AND e.is_active = true
				  AND n.tenant_id = $2
				  AND n.level = 'Tenant'
				  AND n.is_active = true
				  AND n.node_type = ANY($4::text[])
				  AND NOT (
					  CASE
						  WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						  ELSE e.source_node_id
					  END = ANY(nt.visited_path)
				  )
			)
			SELECT DISTINCT node_id::text, edge_id::text, depth FROM neighbor_traversal
		`
		rows, err = s.dbManager.Query(query, pq.Array(nodeIDs), tenantID, levels, pq.Array(nodeTypeStrings))
	} else {
		// Without node type filter - return all neighbors
		query = `
			WITH RECURSIVE neighbor_traversal AS (
				-- Base case: Start with input nodes at depth 0
				SELECT
					id AS node_id,
					NULL::uuid AS edge_id,
					0 AS depth,
					ARRAY[id] AS visited_path
				FROM knowledge_graph_node
				WHERE id = ANY($1::uuid[])
				  AND tenant_id = $2
				  AND level = 'Tenant'
				  AND is_active = true

				UNION

				-- Recursive case: Find neighbors at next depth
				SELECT
					CASE
						WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						ELSE e.source_node_id
					END AS node_id,
					e.id AS edge_id,
					nt.depth + 1 AS depth,
					nt.visited_path || CASE
						WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						ELSE e.source_node_id
					END AS visited_path
				FROM neighbor_traversal nt
				JOIN knowledge_graph_edge e ON (
					e.source_node_id = nt.node_id OR e.destination_node_id = nt.node_id
				)
				JOIN knowledge_graph_node n ON (
					n.id = CASE
						WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						ELSE e.source_node_id
					END
				)
				WHERE nt.depth < $3
				  AND e.tenant_id = $2
				  AND e.level = 'Tenant'
				  AND e.is_active = true
				  AND n.tenant_id = $2
				  AND n.level = 'Tenant'
				  AND n.is_active = true
				  AND NOT (
					  CASE
						  WHEN e.source_node_id = nt.node_id THEN e.destination_node_id
						  ELSE e.source_node_id
					  END = ANY(nt.visited_path)
				  )
			)
			SELECT DISTINCT node_id::text, edge_id::text, depth FROM neighbor_traversal
		`
		rows, err = s.dbManager.Query(query, pq.Array(nodeIDs), tenantID, levels)
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("recursive CTE query failed: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	// A node reached from k parents at depth d-1 produces k rows (one per incoming
	// edge); a node on multiple paths of different lengths can also appear at
	// multiple depths. Dedupe edge ids and collapse to per-node minimum depth so
	// the layered-DAG edge filter can keep only consecutive-layer edges.
	edgeIDSet := make(map[string]struct{})
	nodeMinDepth = make(map[string]int)
	for rows.Next() {
		var nodeID string
		var edgeID sql.NullString
		var depth int
		if err := rows.Scan(&nodeID, &edgeID, &depth); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan traversal row: %w", err)
		}
		if cur, ok := nodeMinDepth[nodeID]; !ok || depth < cur {
			nodeMinDepth[nodeID] = depth
		}
		if edgeID.Valid {
			edgeIDSet[edgeID.String] = struct{}{}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("error iterating discovered node rows: %w", err)
	}

	discoveredIDs = make([]string, 0, len(nodeMinDepth))
	for id := range nodeMinDepth {
		discoveredIDs = append(discoveredIDs, id)
	}
	traversedEdgeIDs = make([]string, 0, len(edgeIDSet))
	for id := range edgeIDSet {
		traversedEdgeIDs = append(traversedEdgeIDs, id)
	}

	return discoveredIDs, traversedEdgeIDs, nodeMinDepth, nil
}

// fetchNodesByIDs retrieves node details for a list of node IDs
func (s *Service) fetchNodesByIDs(nodeIDs []string) ([]*DbNode, error) {
	if len(nodeIDs) == 0 {
		return []*DbNode{}, nil
	}

	query := `
		SELECT id, created_at, updated_at, properties, labels, query_attributes,
			   cloud_account_id, tenant_id, unique_key, node_type, level, COALESCE(source, '')
		FROM knowledge_graph_node
		WHERE id = ANY($1::uuid[])
		  AND level = 'Tenant'
		  AND is_active = true
	`

	rows, err := s.dbManager.Query(query, pq.Array(nodeIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var nodes []*DbNode
	for rows.Next() {
		node := &DbNode{}
		var propertiesJSON, labelsJSON, queryAttributesJSON []byte

		err := rows.Scan(
			&node.ID,
			&node.CreatedAt,
			&node.UpdatedAt,
			&propertiesJSON,
			&labelsJSON,
			&queryAttributesJSON,
			&node.CloudAccountID,
			&node.TenantID,
			&node.UniqueKey,
			&node.NodeType,
			&node.Level,
			&node.Source,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}

		// Parse JSON fields
		if err := json.Unmarshal(propertiesJSON, &node.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal properties: %w", err)
		}
		if err := json.Unmarshal(labelsJSON, &node.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
		if err := json.Unmarshal(queryAttributesJSON, &node.QueryAttributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal query_attributes: %w", err)
		}

		nodes = append(nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node rows: %w", err)
	}

	return nodes, nil
}

// fetchEdgesBetweenNodes retrieves all edges where both source and destination are in the node set
func (s *Service) fetchEdgesBetweenNodes(nodeIDs []string) ([]*DbEdge, error) {
	if len(nodeIDs) == 0 {
		return []*DbEdge{}, nil
	}

	query := `
		SELECT id, created_at, updated_at, relationship_type, properties,
			   cloud_account_id, tenant_id, source_node_id, destination_node_id, level
		FROM knowledge_graph_edge
		WHERE source_node_id = ANY($1::uuid[])
		  AND destination_node_id = ANY($1::uuid[])
		  AND level = 'Tenant'
		  AND is_active = true
		ORDER BY created_at DESC
	`

	rows, err := s.dbManager.Query(query, pq.Array(nodeIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to query edges: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var edges []*DbEdge
	for rows.Next() {
		edge := &DbEdge{}
		var propertiesJSON []byte
		var relationshipType string

		err := rows.Scan(
			&edge.ID,
			&edge.CreatedAt,
			&edge.UpdatedAt,
			&relationshipType,
			&propertiesJSON,
			&edge.CloudAccountID,
			&edge.TenantID,
			&edge.SourceNodeID,
			&edge.DestinationNodeID,
			&edge.Level,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
		}

		if err := json.Unmarshal(propertiesJSON, &edge.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal edge properties: %w", err)
		}

		edge.RelationshipType = RelationshipType(relationshipType)
		edges = append(edges, edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating edge rows: %w", err)
	}

	return edges, nil
}

// fetchEdgeByID retrieves a single edge by its ID
func (s *Service) fetchEdgeByID(edgeID string) (*DbEdge, error) {
	query := `
		SELECT id, created_at, updated_at, relationship_type, properties,
			   cloud_account_id, tenant_id, source_node_id, destination_node_id, level
		FROM knowledge_graph_edge
		WHERE id = $1 AND level = 'Tenant' AND is_active = true
	`
	rows, err := s.dbManager.Query(query, edgeID)
	if err != nil {
		return nil, fmt.Errorf("failed to query edge: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	if rows.Next() {
		edge := &DbEdge{}
		var propertiesJSON []byte
		var relationshipType string
		err := rows.Scan(
			&edge.ID, &edge.CreatedAt, &edge.UpdatedAt,
			&relationshipType, &propertiesJSON,
			&edge.CloudAccountID, &edge.TenantID,
			&edge.SourceNodeID, &edge.DestinationNodeID, &edge.Level,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
		}
		if err := json.Unmarshal(propertiesJSON, &edge.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal edge properties: %w", err)
		}
		edge.RelationshipType = RelationshipType(relationshipType)
		return edge, nil
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating edge rows: %w", err)
	}
	return nil, nil
}

// GetNodeByID retrieves a single KgNode by its ID with account name applied to unique_key
func (s *Service) GetNodeByID(nodeID string) (*KgNode, error) {
	nodes, err := s.fetchNodesByIDs([]string{nodeID})
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	accountMappings, err := s.getAccountMappings(nodes[0].TenantID)
	if err != nil {
		s.logger.Warn("failed to get account mappings", "error", err)
		accountMappings = make(map[string]string)
	}
	kgNodes := ConvertDbNodesToKgNodesWithAccountNames(nodes, accountMappings)
	return &kgNodes[0], nil
}

// GetEdgeByID retrieves a single KgEdge by its ID
func (s *Service) GetEdgeByID(edgeID string) (*KgEdge, error) {
	dbEdge, err := s.fetchEdgeByID(edgeID)
	if err != nil {
		return nil, err
	}
	if dbEdge == nil {
		return nil, nil
	}
	kgEdge := ConvertDbEdgeToKgEdge(dbEdge)
	return &kgEdge, nil
}

// FilterOptions represents available filter options for the UI
// This includes account IDs, node types, and available label/attribute keys (but not their values)
type FilterOptions struct {
	AccountIDs    []string          `json:"account_ids"`              // Unique cloud account IDs
	NodeTypes     []string          `json:"node_types"`               // Unique node types
	LabelKeys     []string          `json:"label_keys"`               // Available label keys
	AttributeKeys []string          `json:"attribute_keys"`           // Available attribute keys
	LastSyncTime  *time.Time        `json:"last_sync_time,omitempty"` // Last sync time from tenant filters
	NodeIDMap     map[string]string `json:"node_id_map"`              // Map of unique_key -> node id
	NodeCount     int               `json:"node_count"`               // Total number of nodes
}

// FilterValuesRequest represents a request to get values for a specific filter key
type FilterValuesRequest struct {
	FilterType string `json:"filter_type"` // "label" or "attribute"
	FilterKey  string `json:"filter_key"`  // The key to get values for
}

// FilterValuesResponse represents the response with values for a specific filter key
type FilterValuesResponse struct {
	FilterType string   `json:"filter_type"` // "label" or "attribute"
	FilterKey  string   `json:"filter_key"`  // The key
	Values     []string `json:"values"`      // Available values for this key
}

// buildNodeFilterSQL builds SQL WHERE additions and args for graph filters.
// startArgCounter is the next $N placeholder index; the caller already has (startArgCounter-1) args.
// Returns ("", nil, nil) when no filters are active.
func buildNodeFilterSQL(filters *GraphFilters, nodeIDs []string, startArgCounter int) (string, []interface{}, error) {
	var sqlParts []string
	var args []interface{}
	argCounter := startArgCounter

	if len(nodeIDs) > 0 {
		sqlParts = append(sqlParts, fmt.Sprintf("id = ANY($%d::uuid[])", argCounter))
		args = append(args, pq.Array(nodeIDs))
		argCounter++
	}

	if filters != nil {
		if len(filters.AccountIDs) > 0 {
			placeholders := make([]string, len(filters.AccountIDs))
			for i, id := range filters.AccountIDs {
				placeholders[i] = fmt.Sprintf("$%d", argCounter)
				args = append(args, id)
				argCounter++
			}
			sqlParts = append(sqlParts, fmt.Sprintf("cloud_account_id IN (%s)", strings.Join(placeholders, ",")))
		}

		if len(filters.NodeTypes) > 0 {
			conds := make([]string, len(filters.NodeTypes))
			for i, nt := range filters.NodeTypes {
				conds[i] = fmt.Sprintf("node_type = $%d", argCounter)
				args = append(args, string(nt))
				argCounter++
			}
			sqlParts = append(sqlParts, fmt.Sprintf("(%s)", strings.Join(conds, " OR ")))
		}

		for key, value := range filters.Labels {
			filterJSON, err := marshalSingleKeyValue(key, value)
			if err != nil {
				return "", nil, fmt.Errorf("failed to marshal label filter %q: %w", key, err)
			}
			sqlParts = append(sqlParts, fmt.Sprintf("labels @> $%d::jsonb", argCounter))
			args = append(args, filterJSON)
			argCounter++
		}
		for _, key := range filters.LabelKeys {
			sqlParts = append(sqlParts, fmt.Sprintf("jsonb_exists(labels, $%d)", argCounter))
			args = append(args, key)
			argCounter++
		}
		for key, value := range filters.Attributes {
			filterJSON, err := marshalSingleKeyValue(key, value)
			if err != nil {
				return "", nil, fmt.Errorf("failed to marshal attribute filter %q: %w", key, err)
			}
			sqlParts = append(sqlParts, fmt.Sprintf("query_attributes @> $%d::jsonb", argCounter))
			args = append(args, filterJSON)
			argCounter++
		}
		for _, key := range filters.AttributeKeys {
			sqlParts = append(sqlParts, fmt.Sprintf("jsonb_exists(query_attributes, $%d)", argCounter))
			args = append(args, key)
			argCounter++
		}
	}

	if len(sqlParts) == 0 {
		return "", nil, nil
	}
	return " AND " + strings.Join(sqlParts, " AND "), args, nil
}

// GetFilterOptions retrieves available filter options for a tenant
// Returns account IDs, node types, and available label/attribute keys with their possible values
func (s *Service) GetFilterOptions(tenantID string, filters *GraphFilters, nodeIDs []string) (*FilterOptions, error) {
	if s.dbManager == nil {
		return nil, fmt.Errorf("database manager not initialized")
	}

	s.logger.Info("retrieving filter options", "tenant_id", tenantID)

	accountMappings, err := s.getAccountMappings(tenantID)
	if err != nil {
		s.logger.Warn(msgAccountMappingsFallback, "error", err)
		accountMappings = make(map[string]string)
	}

	filterSQL, filterArgs, err := buildNodeFilterSQL(filters, nodeIDs, 2)
	if err != nil {
		return nil, fmt.Errorf("failed to build node filter SQL: %w", err)
	}

	// 1. Get unique account IDs
	accountIDsQuery := `
		SELECT DISTINCT cloud_account_id
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND cloud_account_id IS NOT NULL AND level = 'Tenant' AND is_active = true
	` + filterSQL + ` ORDER BY cloud_account_id`
	accountRows, err := s.dbManager.Query(accountIDsQuery, append([]interface{}{tenantID}, filterArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query account IDs: %w", err)
	}
	defer func() {
		if err := accountRows.Close(); err != nil {
			s.logger.Error("failed to close account rows", "error", err)
		}
	}()

	accountIDs := make([]string, 0)
	for accountRows.Next() {
		var accountID string
		if err := accountRows.Scan(&accountID); err != nil {
			return nil, fmt.Errorf("failed to scan account ID: %w", err)
		}
		accountIDs = append(accountIDs, accountID)
	}

	// 2. Get unique node types
	nodeTypesQuery := `
		SELECT DISTINCT node_type
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND level = 'Tenant' AND is_active = true
		AND (NOT jsonb_exists(properties, 'inferred') OR properties->>'inferred' = 'false')
	` + filterSQL + ` ORDER BY node_type`
	nodeTypeRows, err := s.dbManager.Query(nodeTypesQuery, append([]interface{}{tenantID}, filterArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query node types: %w", err)
	}
	defer func() {
		if err := nodeTypeRows.Close(); err != nil {
			s.logger.Error("failed to close node type rows", "error", err)
		}
	}()

	nodeTypes := make([]string, 0)
	for nodeTypeRows.Next() {
		var nodeType string
		if err := nodeTypeRows.Scan(&nodeType); err != nil {
			return nil, fmt.Errorf("failed to scan node type: %w", err)
		}
		nodeTypes = append(nodeTypes, nodeType)
	}

	// 3. Get all unique label keys (without values)
	labelKeysQuery := `
		SELECT DISTINCT jsonb_object_keys(labels) as label_key
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND labels != '{}'::jsonb AND level = 'Tenant' AND is_active = true
	` + filterSQL + ` ORDER BY label_key`
	labelKeyRows, err := s.dbManager.Query(labelKeysQuery, append([]interface{}{tenantID}, filterArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query label keys: %w", err)
	}
	defer func() {
		if err := labelKeyRows.Close(); err != nil {
			s.logger.Error("failed to close label key rows", "error", err)
		}
	}()

	labelKeys := make([]string, 0)
	for labelKeyRows.Next() {
		var labelKey string
		if err := labelKeyRows.Scan(&labelKey); err != nil {
			return nil, fmt.Errorf("failed to scan label key: %w", err)
		}
		labelKeys = append(labelKeys, labelKey)
	}

	// 4. Get all unique query attribute keys (without values)
	attrKeysQuery := `
		SELECT DISTINCT jsonb_object_keys(query_attributes) as attr_key
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND query_attributes != '{}'::jsonb AND level = 'Tenant' AND is_active = true
	` + filterSQL + ` ORDER BY attr_key`
	attrKeyRows, err := s.dbManager.Query(attrKeysQuery, append([]interface{}{tenantID}, filterArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query attribute keys: %w", err)
	}
	defer func() {
		if err := attrKeyRows.Close(); err != nil {
			s.logger.Error("failed to close attribute key rows", "error", err)
		}
	}()

	attributeKeys := make([]string, 0)
	for attrKeyRows.Next() {
		var attrKey string
		if err := attrKeyRows.Scan(&attrKey); err != nil {
			return nil, fmt.Errorf("failed to scan attribute key: %w", err)
		}
		attributeKeys = append(attributeKeys, attrKey)
	}

	// 5. Get unique_key -> id mapping for all nodes
	nodeIDMapQuery := `
		SELECT unique_key, id
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND level = 'Tenant' AND unique_key IS NOT NULL AND is_active = true
		AND (NOT jsonb_exists(properties, 'inferred') OR properties->>'inferred' = 'false')
	` + filterSQL
	nodeIDMapRows, err := s.dbManager.Query(nodeIDMapQuery, append([]interface{}{tenantID}, filterArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query node ID map: %w", err)
	}
	defer func() {
		if err := nodeIDMapRows.Close(); err != nil {
			s.logger.Error("failed to close node ID map rows", "error", err)
		}
	}()

	nodeIDMap := make(map[string]string)
	for nodeIDMapRows.Next() {
		var uniqueKey, nodeID string
		if err := nodeIDMapRows.Scan(&uniqueKey, &nodeID); err != nil {
			return nil, fmt.Errorf("failed to scan node ID map row: %w", err)
		}
		nodeIDMap[uniqueKey] = nodeID
	}
	if err := nodeIDMapRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node ID map rows: %w", err)
	}

	// Replace account_id with account_name in node_id_map keys.
	// applyAccountNames returns the transformed map and whether any account_id was not found
	// in the mapping (indicating the cache may be stale for a newly added account).
	// hasCloudAccountID returns true when parts[0] (cloud_provider) implies parts[1] is
	// a cloud account ID stored in the cloud_accounts table (aws/azure/gcp). For k8s
	// parts[1] is a cluster name and for external it is a tenant id — neither maps to
	// cloud_accounts, so they must not trigger a cache refresh.
	hasCloudAccountID := func(cloudProvider string) bool {
		switch cloudProvider {
		case CloudProviderAWS, CloudProviderAzure, CloudProviderGCP:
			return true
		}
		return false
	}

	applyAccountNames := func(src map[string]string, mappings map[string]string) (map[string]string, bool) {
		result := make(map[string]string, len(src))
		hasUnmapped := false
		for uniqueKey, nodeID := range src {
			parts := strings.Split(uniqueKey, ":")
			if len(parts) == UniqueKeyPartCount {
				accountID := parts[1]
				if accountID != "" {
					if accountName, exists := mappings[accountID]; exists && accountName != "" {
						parts[1] = accountName
						uniqueKey = strings.Join(parts, ":")
					} else if hasCloudAccountID(parts[0]) {
						hasUnmapped = true
					}
				}
			}
			result[uniqueKey] = nodeID
		}
		return result, hasUnmapped
	}

	namedNodeIDMap, hasUnmapped := applyAccountNames(nodeIDMap, accountMappings)
	if hasUnmapped {
		// At least one account_id was not in the cache — refresh and retry once.
		if fresh, refreshErr := s.refreshAccountMappings(tenantID); refreshErr == nil {
			namedNodeIDMap, _ = applyAccountNames(nodeIDMap, fresh)
		}
	}
	nodeIDMap = namedNodeIDMap

	// 6. Get last sync time from tenant filters
	lastSyncQuery := `
		SELECT last_sync_time
		FROM knowledge_graph_tenant_filters
		WHERE tenant_id = $1 AND enabled = true
		ORDER BY last_sync_time DESC NULLS LAST
		LIMIT 1
	`
	var lastSyncTime *time.Time
	row, err := s.dbManager.QueryRow(lastSyncQuery, tenantID)
	if err != nil {
		s.logger.Debug("failed to query last sync time", "tenant_id", tenantID, "error", err)
	} else if err := row.Scan(&lastSyncTime); err != nil {
		// Log as debug since it's expected when no filters exist
		s.logger.Debug("no last sync time found", "tenant_id", tenantID, "error", err)
	}

	s.logger.Info("successfully retrieved filter options",
		"tenant_id", tenantID,
		"account_ids_count", len(accountIDs),
		"node_types_count", len(nodeTypes),
		"label_keys_count", len(labelKeys),
		"attribute_keys_count", len(attributeKeys),
		"node_id_map_count", len(nodeIDMap),
		"last_sync_time", lastSyncTime)

	return &FilterOptions{
		AccountIDs:    accountIDs,
		NodeTypes:     nodeTypes,
		LabelKeys:     labelKeys,
		AttributeKeys: attributeKeys,
		LastSyncTime:  lastSyncTime,
		NodeIDMap:     nodeIDMap,
		NodeCount:     len(nodeIDMap),
	}, nil
}

// GetFilterValues retrieves values for a specific filter key (label or attribute)
func (s *Service) GetFilterValues(tenantID, filterType, filterKey string) (*FilterValuesResponse, error) {
	if s.dbManager == nil {
		return nil, fmt.Errorf("database manager not initialized")
	}

	// Validate filter type
	if filterType != "label" && filterType != "attribute" {
		return nil, fmt.Errorf("invalid filter_type: must be 'label' or 'attribute'")
	}

	if filterKey == "" {
		return nil, fmt.Errorf("filter_key cannot be empty")
	}

	s.logger.Info("retrieving filter values",
		"tenant_id", tenantID,
		"filter_type", filterType,
		"filter_key", filterKey)

	var query string
	if filterType == "label" {
		query = `
			SELECT DISTINCT labels->>$2 as value
			FROM knowledge_graph_node
			WHERE tenant_id = $1
			  AND level = 'Tenant'
			  AND jsonb_exists(labels, $2)
			  AND labels->>$2 IS NOT NULL
			  AND labels->>$2 != ''
			  AND level = 'Tenant'
			ORDER BY value
			LIMIT 1000
		`
	} else { // attribute
		query = `
			SELECT DISTINCT query_attributes->>$2 as value
			FROM knowledge_graph_node
			WHERE tenant_id = $1
			  AND level = 'Tenant'
			  AND jsonb_exists(query_attributes, $2)
			  AND query_attributes->>$2 IS NOT NULL
			  AND query_attributes->>$2 != ''
			ORDER BY value
			LIMIT 1000
		`
	}

	rows, err := s.dbManager.Query(query, tenantID, filterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to query filter values: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			s.logger.Error("failed to close filter value rows", "error", err)
		}
	}()

	// Initialize as empty array to return [] instead of null in JSON
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("failed to scan filter value: %w", err)
		}
		values = append(values, value)
	}

	s.logger.Info("successfully retrieved filter values",
		"tenant_id", tenantID,
		"filter_type", filterType,
		"filter_key", filterKey,
		"values_count", len(values))

	return &FilterValuesResponse{
		FilterType: filterType,
		FilterKey:  filterKey,
		Values:     values,
	}, nil
}

// --- Agent Traversal API Methods ---

// SearchNodes finds nodes by human-readable attributes (name, namespace, cluster, type, source, labels).
func (s *Service) SearchNodes(tenantID string, params SearchNodesParams) (*SearchNodesResponse, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	query := `SELECT id, node_type, COALESCE(source, '') AS source, query_attributes, labels, properties, cloud_account_id
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND is_active = true`
	args := []interface{}{tenantID}
	argIdx := 2

	if params.Name != "" {
		query += fmt.Sprintf(" AND query_attributes->>'name' = $%d", argIdx)
		args = append(args, params.Name)
		argIdx++
	}
	if params.NamePattern != "" {
		query += fmt.Sprintf(" AND query_attributes->>'name' ILIKE $%d", argIdx)
		args = append(args, params.NamePattern)
		argIdx++
	}
	if params.Namespace != "" {
		query += fmt.Sprintf(" AND query_attributes->>'namespace' = $%d", argIdx)
		args = append(args, params.Namespace)
		argIdx++
	}
	if params.Cluster != "" {
		query += fmt.Sprintf(" AND query_attributes->>'cluster' = $%d", argIdx)
		args = append(args, params.Cluster)
		argIdx++
	}
	if len(params.NodeTypes) > 0 {
		nodeTypeStrings := make([]string, len(params.NodeTypes))
		for i, nt := range params.NodeTypes {
			nodeTypeStrings[i] = string(nt)
		}
		query += fmt.Sprintf(" AND node_type = ANY($%d::text[])", argIdx)
		args = append(args, pq.Array(nodeTypeStrings))
		argIdx++
	}
	if params.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, params.Source)
		argIdx++
	}
	if len(params.AccountIDs) > 0 {
		query += fmt.Sprintf(" AND cloud_account_id = ANY($%d::uuid[])", argIdx)
		args = append(args, pq.Array(params.AccountIDs))
		argIdx++
	}
	if len(params.Labels) > 0 {
		for key, value := range params.Labels {
			query += fmt.Sprintf(" AND labels->>$%d = $%d", argIdx, argIdx+1)
			args = append(args, key, value)
			argIdx += 2
		}
	}

	query += " ORDER BY node_type, query_attributes->>'name'"
	query += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search nodes: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("failed to close rows", "error", closeErr)
		}
	}()

	var results []SearchNodeResult
	for rows.Next() {
		var id, nodeType, source, cloudAccountID string
		var queryAttributesJSON, labelsJSON, propertiesJSON []byte

		if err := rows.Scan(&id, &nodeType, &source, &queryAttributesJSON, &labelsJSON, &propertiesJSON, &cloudAccountID); err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}

		var queryAttrs map[string]interface{}
		if err := json.Unmarshal(queryAttributesJSON, &queryAttrs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal query_attributes: %w", err)
		}
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
		var properties map[string]interface{}
		if err := json.Unmarshal(propertiesJSON, &properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal properties: %w", err)
		}

		name, _ := queryAttrs["name"].(string)
		namespace, _ := queryAttrs["namespace"].(string)
		cluster, _ := queryAttrs["cluster"].(string)

		results = append(results, SearchNodeResult{
			ID:             id,
			NodeType:       NodeType(nodeType),
			Name:           name,
			Namespace:      namespace,
			Cluster:        cluster,
			Source:         source,
			CloudAccountID: cloudAccountID,
			Labels:         labels,
			Properties:     properties,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating search results: %w", err)
	}

	if results == nil {
		results = []SearchNodeResult{}
	}

	return &SearchNodesResponse{
		Nodes:      results,
		TotalCount: len(results),
	}, nil
}

// TraverseDirectional performs directional graph traversal from seed nodes (or inline search).
func (s *Service) TraverseDirectional(tenantID string, params TraverseParams) (*TraverseResponse, error) {
	// Validate direction
	switch params.Direction {
	case TraverseDirectionDownstream, TraverseDirectionUpstream, TraverseDirectionBoth:
		// valid
	default:
		return nil, fmt.Errorf("invalid direction %q: must be 'downstream', 'upstream', or 'both'", params.Direction)
	}

	// Clamp parameters
	if params.MaxDepth <= 0 {
		params.MaxDepth = 1
	}
	if params.MaxDepth > 3 {
		params.MaxDepth = 3
	}
	if params.MaxNodes <= 0 {
		params.MaxNodes = 500
	}
	if params.MaxNodes > 500 {
		params.MaxNodes = 500
	}

	// Determine seed node IDs
	hasNodeIDs := len(params.NodeIDs) > 0
	hasSearchParams := params.Name != "" || params.NamePattern != "" ||
		params.Namespace != "" || params.Cluster != "" ||
		len(params.SearchNodeTypes) > 0

	if hasNodeIDs && hasSearchParams {
		return nil, fmt.Errorf("provide either node_ids or search parameters (name, name_pattern, namespace, cluster, search_node_types), not both")
	}
	if !hasNodeIDs && !hasSearchParams {
		return nil, fmt.Errorf("provide either node_ids or at least one search parameter (name, name_pattern, namespace, cluster, search_node_types)")
	}
	// search_node_types alone is too broad to seed a traversal — it would match
	// every node of that type in the tenant. Require at least one narrowing field.
	if !hasNodeIDs &&
		params.Name == "" && params.NamePattern == "" &&
		params.Namespace == "" && params.Cluster == "" &&
		len(params.SearchNodeTypes) > 0 {
		return nil, fmt.Errorf("search_node_types alone is too broad; combine it with name, name_pattern, namespace, or cluster")
	}

	var seedNodeIDs []string

	if hasNodeIDs {
		if len(params.NodeIDs) > 10 {
			return nil, fmt.Errorf("node_ids limited to 10 entries, got %d", len(params.NodeIDs))
		}
		seedNodeIDs = params.NodeIDs
	} else {
		// Inline search mode
		searchNodeTypes := make([]NodeType, len(params.SearchNodeTypes))
		for i, t := range params.SearchNodeTypes {
			searchNodeTypes[i] = NodeType(t)
		}
		searchResult, err := s.SearchNodes(tenantID, SearchNodesParams{
			Name:        params.Name,
			NamePattern: params.NamePattern,
			Namespace:   params.Namespace,
			Cluster:     params.Cluster,
			NodeTypes:   searchNodeTypes,
			Limit:       10, // cap inline search to 10 seed nodes
		})
		if err != nil {
			return nil, fmt.Errorf("inline search failed: %w", err)
		}
		if len(searchResult.Nodes) == 0 {
			return &TraverseResponse{
				Graph:           KnowledgeGraphSlim{Nodes: []KgNodeSlim{}, Edges: []KgEdgeSlim{}},
				SeedNodeIDs:     []string{},
				Truncated:       false,
				TotalDiscovered: 0,
			}, nil
		}
		for _, node := range searchResult.Nodes {
			seedNodeIDs = append(seedNodeIDs, node.ID)
		}
	}

	// Discover nodes via directional CTE
	discoveredIDs, traversedEdgeIDs, nodeMinDepth, err := s.discoverDirectional(seedNodeIDs, params.Direction, params.MaxDepth, params.RelationshipTypes, params.NodeTypes, params.ExcludeNodeTypes)
	if err != nil {
		return nil, fmt.Errorf("directional traversal failed: %w", err)
	}

	totalDiscovered := len(discoveredIDs)
	truncated := false
	if totalDiscovered > params.MaxNodes {
		discoveredIDs = discoveredIDs[:params.MaxNodes]
		truncated = true
	}

	// Fetch full node and edge data
	dbNodes, err := s.fetchNodesByIDs(discoveredIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch traversed nodes: %w", err)
	}

	// Apply includeNodeTypes as a post-traversal filter. The CTE intentionally
	// traverses through all types (to reach targets via intermediate nodes),
	// then we restrict the returned result to the requested types only.
	if len(params.NodeTypes) > 0 {
		includeSet := make(map[NodeType]struct{}, len(params.NodeTypes))
		for _, nt := range params.NodeTypes {
			includeSet[NodeType(nt)] = struct{}{}
		}
		filtered := dbNodes[:0]
		for _, n := range dbNodes {
			if _, ok := includeSet[n.NodeType]; ok {
				filtered = append(filtered, n)
			}
		}
		dbNodes = filtered
		// Re-derive discoveredIDs from the filtered node set for edge fetching.
		discoveredIDs = make([]string, len(dbNodes))
		for i, n := range dbNodes {
			discoveredIDs[i] = n.ID
		}
	}

	var dbEdges []*DbEdge
	if params.InducedSubgraph {
		// Induced-subgraph mode: every edge whose both endpoints lie in the
		// discovered set (includes sibling-to-sibling edges).
		dbEdges, err = s.fetchEdgesBetweenNodesFiltered(discoveredIDs, params.RelationshipTypes)
	} else {
		// Strict-tree (default): only edges the BFS walked AND that connect
		// consecutive layers of the BFS (depth d -> depth d+1 in the traversal
		// direction). The CTE walks any edge along its path so sibling edges
		// at the same min-depth can leak in at depth>=2; filter them out by
		// each node's globally-minimum BFS depth.
		dbEdges, err = s.fetchEdgesByIDs(traversedEdgeIDs, discoveredIDs, params.RelationshipTypes)
		if err == nil {
			dbEdges = filterLayeredEdges(dbEdges, nodeMinDepth, params.Direction)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch traversed edges: %w", err)
	}

	// Convert to slim response format
	accountMappings, err := s.getAccountMappings(tenantID)
	if err != nil {
		s.logger.Warn("failed to get account mappings for traverse", "error", err)
		accountMappings = make(map[string]string)
	}

	kgNodes := ConvertDbNodesToKgNodesWithAccountNames(dbNodes, accountMappings)
	kgEdges := ConvertDbEdgesToKgEdges(dbEdges)
	kg := KnowledgeGraph{
		Nodes:       kgNodes,
		Edges:       kgEdges,
		TenantID:    tenantID,
		GeneratedAt: time.Now(),
	}

	return &TraverseResponse{
		Graph:           ConvertKnowledgeGraphToSlim(kg),
		SeedNodeIDs:     seedNodeIDs,
		Truncated:       truncated,
		TotalDiscovered: totalDiscovered,
	}, nil
}

// discoverDirectional uses a PostgreSQL recursive CTE with direction control.
// It returns:
//   - discoveredIDs: the set of node IDs reached from any seed.
//   - traversedEdgeIDs: edge IDs the BFS actually walked.
//   - nodeMinDepth: minimum hop count from any seed to each discovered node.
//     Used by the strict-tree edge filter to keep only edges that connect
//     consecutive BFS layers (and drop sibling-to-sibling edges that the CTE
//     happens to walk on longer paths).
//
// Seed-anchor rows contribute no edge ID; only the recursive step does.
func (s *Service) discoverDirectional(
	nodeIDs []string,
	direction TraverseDirection,
	levels int,
	relationshipTypes []string,
	includeNodeTypes []NodeType,
	excludeNodeTypes []NodeType,
) (discoveredIDs []string, traversedEdgeIDs []string, nodeMinDepth map[string]int, err error) {
	// Build the direction-specific edge join and next-node expressions
	var edgeJoin, nextNode string
	switch direction {
	case TraverseDirectionDownstream:
		edgeJoin = "e.source_node_id = t.node_id"
		nextNode = "e.destination_node_id"
	case TraverseDirectionUpstream:
		edgeJoin = "e.destination_node_id = t.node_id"
		nextNode = "e.source_node_id"
	case TraverseDirectionBoth:
		edgeJoin = "(e.source_node_id = t.node_id OR e.destination_node_id = t.node_id)"
		nextNode = "CASE WHEN e.source_node_id = t.node_id THEN e.destination_node_id ELSE e.source_node_id END"
	}

	// Build optional WHERE clauses for the recursive part.
	// NOTE: include_node_types is NOT applied inside the CTE — it must allow
	// traversal through intermediate types (e.g., Namespace) to reach targets
	// (e.g., Workload) at depth > 1. The full subgraph is returned so the
	// agent can see the connectivity path. exclude_node_types IS applied in
	// the CTE to prune noisy branches (e.g., SecurityGroup, NetworkInterface).
	var filterClauses []string
	args := []interface{}{pq.Array(nodeIDs), levels}
	argIdx := 3

	if len(relationshipTypes) > 0 {
		filterClauses = append(filterClauses, fmt.Sprintf("e.relationship_type = ANY($%d::text[])", argIdx))
		args = append(args, pq.Array(relationshipTypes))
		argIdx++
	}

	excludeTypeStrings := make([]string, len(excludeNodeTypes))
	for i, nt := range excludeNodeTypes {
		excludeTypeStrings[i] = string(nt)
	}
	if len(excludeTypeStrings) > 0 {
		filterClauses = append(filterClauses, fmt.Sprintf("NOT n.node_type = ANY($%d::text[])", argIdx))
		args = append(args, pq.Array(excludeTypeStrings))
	}

	additionalFilters := ""
	if len(filterClauses) > 0 {
		additionalFilters = " AND " + strings.Join(filterClauses, " AND ")
	}

	query := fmt.Sprintf(`
		WITH RECURSIVE traversal AS (
			SELECT id AS node_id, NULL::uuid AS edge_id, 0 AS depth, ARRAY[id] AS visited
			FROM knowledge_graph_node
			WHERE id = ANY($1::uuid[])
			  AND level = 'Tenant'
			  AND is_active = true

			UNION

			SELECT %s AS node_id,
				   e.id AS edge_id,
				   t.depth + 1 AS depth,
				   t.visited || %s AS visited
			FROM traversal t
			JOIN knowledge_graph_edge e ON %s
			JOIN knowledge_graph_node n ON n.id = %s
			WHERE t.depth < $2
			  AND e.level = 'Tenant'
			  AND e.is_active = true
			  AND n.level = 'Tenant'
			  AND n.is_active = true
			  AND NOT (%s = ANY(t.visited))
			  %s
		)
		SELECT DISTINCT node_id::text, edge_id::text, depth FROM traversal
	`, nextNode, nextNode, edgeJoin, nextNode, nextNode, additionalFilters)

	rows, err := s.dbManager.Query(query, args...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("directional CTE query failed: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	// The CTE emits one row per (node_id, edge_id, depth) triple. A node at
	// depth d reached from k parents at depth d-1 produces k rows (one per
	// incoming edge); a node reachable on multiple paths of different lengths
	// can also appear at multiple depths. Dedupe per-column in Go and collapse
	// per-node depth to its minimum — the layered-DAG edge filter relies on
	// each node having a single canonical depth.
	edgeIDSet := make(map[string]struct{})
	nodeMinDepth = make(map[string]int)
	for rows.Next() {
		var nodeID string
		var edgeID sql.NullString
		var depth int
		if err := rows.Scan(&nodeID, &edgeID, &depth); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan traversal row: %w", err)
		}
		if cur, ok := nodeMinDepth[nodeID]; !ok || depth < cur {
			nodeMinDepth[nodeID] = depth
		}
		if edgeID.Valid {
			edgeIDSet[edgeID.String] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("error iterating discovered nodes: %w", err)
	}

	discoveredIDs = make([]string, 0, len(nodeMinDepth))
	for id := range nodeMinDepth {
		discoveredIDs = append(discoveredIDs, id)
	}
	traversedEdgeIDs = make([]string, 0, len(edgeIDSet))
	for id := range edgeIDSet {
		traversedEdgeIDs = append(traversedEdgeIDs, id)
	}

	return discoveredIDs, traversedEdgeIDs, nodeMinDepth, nil
}

// filterLayeredEdges keeps only edges that connect consecutive BFS layers in
// the requested direction. For downstream traversal a tree edge has
// depth(dst) = depth(src)+1; for upstream the inverse; for both, |Δdepth|=1.
// Edges between two nodes at the same min-depth (siblings) and edges that
// point against the BFS frontier are dropped. This keeps the response a
// layered DAG of dependency relations rather than the induced subgraph.
func filterLayeredEdges(edges []*DbEdge, nodeMinDepth map[string]int, direction TraverseDirection) []*DbEdge {
	if len(edges) == 0 {
		return edges
	}
	keep := func(srcDepth, dstDepth int) bool {
		switch direction {
		case TraverseDirectionDownstream:
			return dstDepth == srcDepth+1
		case TraverseDirectionUpstream:
			return srcDepth == dstDepth+1
		case TraverseDirectionBoth:
			d := srcDepth - dstDepth
			return d == 1 || d == -1
		}
		return false
	}
	filtered := edges[:0]
	for _, e := range edges {
		sd, sok := nodeMinDepth[e.SourceNodeID]
		dd, dok := nodeMinDepth[e.DestinationNodeID]
		if !sok || !dok {
			continue
		}
		if keep(sd, dd) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// fetchEdgesByIDs retrieves a specific set of edges by ID, restricted to those
// whose source and destination both still belong to surviveNodeIDs (so trims
// from a post-traversal NodeTypes filter are honored). An optional relationship
// type filter narrows the result. Returns an empty slice when edgeIDs is empty.
func (s *Service) fetchEdgesByIDs(edgeIDs, surviveNodeIDs []string, relationshipTypes []string) ([]*DbEdge, error) {
	if len(edgeIDs) == 0 {
		return []*DbEdge{}, nil
	}

	query := `
		SELECT id, created_at, updated_at, relationship_type, properties,
			   cloud_account_id, tenant_id, source_node_id, destination_node_id, level
		FROM knowledge_graph_edge
		WHERE id = ANY($1::uuid[])
		  AND source_node_id = ANY($2::uuid[])
		  AND destination_node_id = ANY($2::uuid[])
		  AND level = 'Tenant'
		  AND is_active = true
	`
	args := []interface{}{pq.Array(edgeIDs), pq.Array(surviveNodeIDs)}

	if len(relationshipTypes) > 0 {
		query += " AND relationship_type = ANY($3::text[])"
		args = append(args, pq.Array(relationshipTypes))
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query edges by IDs: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var edges []*DbEdge
	for rows.Next() {
		edge := &DbEdge{}
		var propertiesJSON []byte
		var relationshipType string

		err := rows.Scan(
			&edge.ID, &edge.CreatedAt, &edge.UpdatedAt,
			&relationshipType, &propertiesJSON,
			&edge.CloudAccountID, &edge.TenantID,
			&edge.SourceNodeID, &edge.DestinationNodeID, &edge.Level,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
		}
		if err := json.Unmarshal(propertiesJSON, &edge.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal edge properties: %w", err)
		}
		edge.RelationshipType = RelationshipType(relationshipType)
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating edge rows: %w", err)
	}

	return edges, nil
}

// fetchEdgesBetweenNodesFiltered retrieves edges between a node set with optional relationship type filter.
func (s *Service) fetchEdgesBetweenNodesFiltered(nodeIDs []string, relationshipTypes []string) ([]*DbEdge, error) {
	if len(nodeIDs) == 0 {
		return []*DbEdge{}, nil
	}

	query := `
		SELECT id, created_at, updated_at, relationship_type, properties,
			   cloud_account_id, tenant_id, source_node_id, destination_node_id, level
		FROM knowledge_graph_edge
		WHERE source_node_id = ANY($1::uuid[])
		  AND destination_node_id = ANY($1::uuid[])
		  AND level = 'Tenant'
		  AND is_active = true
	`
	args := []interface{}{pq.Array(nodeIDs)}

	if len(relationshipTypes) > 0 {
		query += " AND relationship_type = ANY($2::text[])"
		args = append(args, pq.Array(relationshipTypes))
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query filtered edges: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var edges []*DbEdge
	for rows.Next() {
		edge := &DbEdge{}
		var propertiesJSON []byte
		var relationshipType string

		err := rows.Scan(
			&edge.ID, &edge.CreatedAt, &edge.UpdatedAt,
			&relationshipType, &propertiesJSON,
			&edge.CloudAccountID, &edge.TenantID,
			&edge.SourceNodeID, &edge.DestinationNodeID, &edge.Level,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
		}
		if err := json.Unmarshal(propertiesJSON, &edge.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal edge properties: %w", err)
		}
		edge.RelationshipType = RelationshipType(relationshipType)
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating filtered edge rows: %w", err)
	}

	return edges, nil
}
