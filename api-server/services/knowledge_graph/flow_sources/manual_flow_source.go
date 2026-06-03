package flow_sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"

	"github.com/google/uuid"
)

func init() {
	RegisterFlowSourceFactory(
		ManualFlowSourceName,
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewManualFlowSource(logger), nil
		},
		"User-declared service-to-service dependencies (CSV upload / single-row form); supports cross-stack k8s<->AWS",
		string(core.FlowSourceCategoryManual),
	)
}

// ManualFlowSource reads kg_manual_dependencies rows, resolves each endpoint
// to a KG node (with on-the-fly re-resolution if the resolved node was
// deleted or deactivated since the last cycle), and emits CALLS /
// PUBLISHES_TO / SUBSCRIBES_TO edges with source = ManualFlowSourceName.
//
// The source is also the *primary writer* of the kg_manual_dependencies
// resolution columns. RPC handlers can call ResolveAndPersist for a single
// row on demand; this flow source runs the same logic for every active row
// per build cycle.
type ManualFlowSource struct {
	*BaseFlowSource
}

// NewManualFlowSource returns a flow source registered under "manual".
func NewManualFlowSource(logger *slog.Logger) *ManualFlowSource {
	base := NewBaseFlowSource(
		ManualFlowSourceName,
		core.FlowSourceCategoryManual,
		true,
		logger,
	)
	return &ManualFlowSource{BaseFlowSource: base}
}

// GetSourceCategory returns the source category.
func (s *ManualFlowSource) GetSourceCategory() core.FlowSourceCategory {
	return core.FlowSourceCategoryManual
}

// Validate implements core.FlowSourceInterface.
func (s *ManualFlowSource) Validate() error {
	return s.BaseFlowSource.Validate()
}

// BuildFlowRelationships pulls active rows for the tenant, re-checks /
// re-resolves each one, persists the updated resolution state, and returns
// edges for resolved rows. It does not create any new nodes.
func (s *ManualFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	startTime := time.Now()
	defer s.TrackBuildTime(startTime)

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("manual flow source: db manager unavailable: %w", err)
	}

	rows, err := loadActiveManualDependencies(reqCtx.GetContext(), dbManager, req.TenantID)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, err
	}

	if len(rows) == 0 {
		s.logger.Info("manual flow source: no active rows", "tenant_id", req.TenantID)
		return []*core.DbEdge{}, []*core.DbNode{}, nil
	}

	nodeIndex := buildNodeIndex(req.ExistingNodes)
	resolver := NewManualEndpointResolver(dbManager, s.logger)

	edges := make([]*core.DbEdge, 0, len(rows))
	emittedCount := 0
	rowUpdates := make([]manualDependencyUpdate, 0, len(rows))

	for _, row := range rows {
		update, edge, err := s.processRow(reqCtx.GetContext(), resolver, nodeIndex, row, req.TenantID)
		if err != nil {
			s.logger.Warn("manual flow source: row processing failed; skipping",
				"row_id", row.ID, "tenant_id", req.TenantID, "error", err)
			s.IncrementErrorCount()
			continue
		}
		rowUpdates = append(rowUpdates, update)
		if edge != nil {
			edges = append(edges, edge)
			emittedCount++
		}
	}

	// Persist row updates BEFORE returning edges. If the writeback fails the
	// emitted edges and the on-disk resolution_status diverge — the next
	// build cycle would re-emit stale state, ambiguity-resolve via the UI
	// would race against rows that look resolved but aren't, and downstream
	// consumers querying the table would see lies. Skip the edge yield on
	// persist failure so callers don't observe an inconsistent build cycle.
	if err := persistRowUpdates(dbManager, rowUpdates); err != nil {
		s.logger.Error("manual flow source: failed to persist row updates; skipping edge yield to keep DB and in-memory views consistent",
			"tenant_id", req.TenantID,
			"rows_pending_persist", len(rowUpdates),
			"edges_dropped", emittedCount,
			"error", err)
		s.IncrementErrorCount()
		s.LogMetrics()
		return []*core.DbEdge{}, []*core.DbNode{}, fmt.Errorf("manual flow source persist failure: %w", err)
	}

	s.LogMetrics()
	s.logger.Info("completed building flow relationships from manual",
		"tenant_id", req.TenantID,
		"rows_processed", len(rows),
		"edges_emitted", emittedCount,
		"duration_seconds", time.Since(startTime).Seconds())

	return edges, []*core.DbNode{}, nil
}

// processRow runs one row through the recheck-then-resolve pipeline and
// returns (updated row state, optional edge). When both endpoints are
// cleanly resolved against still-active nodes the edge is emitted; otherwise
// the row is updated with diagnostics and no edge is produced.
func (s *ManualFlowSource) processRow(
	ctx context.Context,
	resolver *ManualEndpointResolver,
	nodeIndex map[string]*core.DbNode,
	row manualDependencyRow,
	tenantID string,
) (manualDependencyUpdate, *core.DbEdge, error) {
	srcResult, dstResult, err := s.resolveBothEndpoints(ctx, resolver, nodeIndex, row, tenantID)
	if err != nil {
		return manualDependencyUpdate{}, nil, err
	}

	update := buildUpdateFromResults(row.ID, srcResult, dstResult)

	if update.ResolutionStatus != ManualResolutionResolved {
		return update, nil, nil
	}

	edge := s.buildEdge(row, srcResult.NodeID, dstResult.NodeID, tenantID, nodeIndex)
	if edge == nil {
		// Resolved row pointed at nodes that vanished between the resolver
		// and the build-edge phase. resolveBothEndpoints already trusts the
		// node index, so this is a paranoid guard; if it ever fires the row
		// is downgraded to node_inactive on the next cycle.
		s.logger.Warn("manual flow source: resolved node missing from index at edge-build time; skipping",
			"row_id", row.ID, "src_node_id", srcResult.NodeID, "dst_node_id", dstResult.NodeID)
		return update, nil, nil
	}
	return update, edge, nil
}

// resolveBothEndpoints handles the recheck-or-resolve flow for source and
// destination. When the row was previously resolved and both target nodes
// are still active we short-circuit with synthetic "resolved" results; any
// drift triggers a full resolver run for the affected side.
func (s *ManualFlowSource) resolveBothEndpoints(
	ctx context.Context,
	resolver *ManualEndpointResolver,
	nodeIndex map[string]*core.DbNode,
	row manualDependencyRow,
	tenantID string,
) (*EndpointResolveResult, *EndpointResolveResult, error) {
	var srcResult, dstResult *EndpointResolveResult

	if row.ResolutionStatus == ManualResolutionResolved &&
		row.ResolvedSourceNodeID != "" && nodeIndex[row.ResolvedSourceNodeID] != nil {
		srcResult = &EndpointResolveResult{Status: EndpointStatusResolved, NodeID: row.ResolvedSourceNodeID, MatchCount: 1}
	} else {
		r, err := resolver.Resolve(ctx, tenantID, row.sourceEndpoint())
		if err != nil {
			return nil, nil, err
		}
		srcResult = r
	}

	if row.ResolutionStatus == ManualResolutionResolved &&
		row.ResolvedDestNodeID != "" && nodeIndex[row.ResolvedDestNodeID] != nil {
		dstResult = &EndpointResolveResult{Status: EndpointStatusResolved, NodeID: row.ResolvedDestNodeID, MatchCount: 1}
	} else {
		r, err := resolver.Resolve(ctx, tenantID, row.destEndpoint())
		if err != nil {
			return nil, nil, err
		}
		dstResult = r
	}

	return srcResult, dstResult, nil
}

// buildEdge emits the resolved CALLS / PUBLISHES_TO / SUBSCRIBES_TO edge.
// We populate the first-class Source field and seed ContributingSources
// (DeduplicateEdgesWithPriority will merge with other asserters of the same
// edge). manual_dependency_id is included so the panic-button delete and
// the explicit row-delete RPC can find and remove the exact edge.
//
// CloudAccountID is mandatory on knowledge_graph_edge (UUID NOT NULL), so we
// inherit it from the resolved source node — matching the convention used by
// cross_account_relationships.go. Cross-stack edges (e.g. k8s Workload → AWS
// Database) ride on the source's account, which is consistent with how
// trace / ebpf edges work today (always tied to the calling-side account).
// Returns nil when either resolved node id is missing from the node index;
// the caller logs and skips so a single bad row doesn't break SaveEdges'
// batch INSERT (Postgres rolls the whole batch back on the first bad UUID).
func (s *ManualFlowSource) buildEdge(
	row manualDependencyRow,
	srcNodeID, dstNodeID, tenantID string,
	nodeIndex map[string]*core.DbNode,
) *core.DbEdge {
	srcNode := nodeIndex[srcNodeID]
	dstNode := nodeIndex[dstNodeID]
	if srcNode == nil || dstNode == nil {
		return nil
	}
	cloudAccountID := srcNode.CloudAccountID
	if cloudAccountID == "" {
		cloudAccountID = dstNode.CloudAccountID
	}
	if cloudAccountID == "" {
		// Both nodes lack an account — should never happen for nodes that came
		// through SaveNodes (which enforces non-empty cloud_account_id), but
		// guard explicitly so SaveEdges doesn't drop the whole batch.
		return nil
	}

	props := map[string]interface{}{
		"created_by_flow_source": ManualFlowSourceName,
		"flow_source_category":   string(core.FlowSourceCategoryManual),
		"source_priority":        int(GetEdgeSourcePriority(ManualFlowSourceName, core.RelationshipType(row.RelationshipType))),
		"manual_declared":        true,
		"manual_dependency_id":   row.ID,
	}
	if row.DeclaredByUserID != "" {
		props["declared_by_user_id"] = row.DeclaredByUserID
	}
	if row.Notes != "" {
		props["notes"] = row.Notes
	}

	now := time.Now()
	s.metrics.RelationshipsCreated++

	return &core.DbEdge{
		ID:                uuid.New().String(),
		SourceNodeID:      srcNodeID,
		DestinationNodeID: dstNodeID,
		RelationshipType:  core.RelationshipType(row.RelationshipType),
		Properties:        props,
		CloudAccountID:    cloudAccountID,
		TenantID:          tenantID,
		Level:             "Tenant",
		Source:            ManualFlowSourceName,
		ContributingSources: []core.EdgeContributingSource{
			{Source: ManualFlowSourceName, LastSeenAt: now},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// manualDependencyRow mirrors one row of kg_manual_dependencies. Kept private
// to the flow_sources package — RPC handlers exchange ManualDependency
// (public DTO) and the package converts at the boundary.
type manualDependencyRow struct {
	ID                   int64
	TenantID             string
	SourceNodeType       string
	SourceName           string
	SourceNamespace      string
	SourceCluster        string
	SourceARN            string
	SourceAccountID      string
	SourceRegion         string
	DestNodeType         string
	DestName             string
	DestNamespace        string
	DestCluster          string
	DestARN              string
	DestAccountID        string
	DestRegion           string
	RelationshipType     string
	Notes                string
	DeclaredByUserID     string
	ResolvedSourceNodeID string
	ResolvedDestNodeID   string
	ResolutionStatus     string
}

func (r manualDependencyRow) sourceEndpoint() ManualEndpoint {
	return ManualEndpoint{
		NodeType: r.SourceNodeType, Name: r.SourceName,
		Namespace: r.SourceNamespace, Cluster: r.SourceCluster,
		ARN: r.SourceARN, AccountID: r.SourceAccountID, Region: r.SourceRegion,
	}
}

func (r manualDependencyRow) destEndpoint() ManualEndpoint {
	return ManualEndpoint{
		NodeType: r.DestNodeType, Name: r.DestName,
		Namespace: r.DestNamespace, Cluster: r.DestCluster,
		ARN: r.DestARN, AccountID: r.DestAccountID, Region: r.DestRegion,
	}
}

// manualDependencyUpdate is the per-row writeback after a build cycle (or
// on-demand resolve). NULLable resolved_*_node_id is represented as the
// empty string here and translated to NULL by persistRowUpdates.
type manualDependencyUpdate struct {
	ID                    int64
	ResolutionStatus      string
	ResolvedSourceNodeID  string
	ResolvedDestNodeID    string
	ResolutionError       string
	SourceMatchCount      int
	DestMatchCount        int
	SourceMatchCandidates []ManualMatchCandidate
	DestMatchCandidates   []ManualMatchCandidate
}

// buildUpdateFromResults composes the row-level update payload from the
// per-endpoint resolver results.
func buildUpdateFromResults(rowID int64, src, dst *EndpointResolveResult) manualDependencyUpdate {
	u := manualDependencyUpdate{
		ID:                    rowID,
		ResolutionStatus:      RowResolutionFromEndpoints(src, dst),
		SourceMatchCount:      src.MatchCount,
		DestMatchCount:        dst.MatchCount,
		SourceMatchCandidates: src.Candidates,
		DestMatchCandidates:   dst.Candidates,
	}

	if src.Status == EndpointStatusResolved {
		u.ResolvedSourceNodeID = src.NodeID
	}
	if dst.Status == EndpointStatusResolved {
		u.ResolvedDestNodeID = dst.NodeID
	}

	switch {
	case src.ErrorMessage != "" && dst.ErrorMessage != "":
		u.ResolutionError = fmt.Sprintf("source: %s; dest: %s", src.ErrorMessage, dst.ErrorMessage)
	case src.ErrorMessage != "":
		u.ResolutionError = "source: " + src.ErrorMessage
	case dst.ErrorMessage != "":
		u.ResolutionError = "dest: " + dst.ErrorMessage
	}

	return u
}

// buildNodeIndex builds a map[id]*DbNode over the request's pre-loaded
// nodes. Used for both staleness checks (presence == active, since
// ExistingNodes is already filtered to is_active=true by the loader) AND
// downstream lookups that need fields from the node — notably
// CloudAccountID, which the edge writer inherits onto emitted manual edges
// to satisfy the NOT NULL constraint on knowledge_graph_edge.
func buildNodeIndex(nodes []*core.DbNode) map[string]*core.DbNode {
	idx := make(map[string]*core.DbNode, len(nodes))
	for _, n := range nodes {
		idx[n.ID] = n
	}
	return idx
}

// loadActiveManualDependencies returns every active row for the tenant. The
// flow source then per-row re-resolves; a tenant with thousands of rows
// would justify a batched approach, but ~50 rows (FK PoC sizing) reads
// comfortably in one shot.
func loadActiveManualDependencies(ctx context.Context, dbManager *database.DatabaseManager, tenantID string) ([]manualDependencyRow, error) {
	query := `SELECT id, tenant_id::text,
		source_node_type, source_name,
		COALESCE(source_namespace, ''), COALESCE(source_cluster, ''),
		COALESCE(source_arn, ''), COALESCE(source_account_id, ''), COALESCE(source_region, ''),
		dest_node_type, dest_name,
		COALESCE(dest_namespace, ''), COALESCE(dest_cluster, ''),
		COALESCE(dest_arn, ''), COALESCE(dest_account_id, ''), COALESCE(dest_region, ''),
		relationship_type, COALESCE(notes, ''),
		COALESCE(declared_by_user_id::text, ''),
		COALESCE(resolved_source_node_id::text, ''),
		COALESCE(resolved_dest_node_id::text, ''),
		resolution_status
		FROM kg_manual_dependencies
		WHERE tenant_id = $1 AND is_active = true
		ORDER BY id`

	rs, err := dbManager.Query(query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load manual dependencies: %w", err)
	}
	defer func() { _ = rs.Close() }()

	out := make([]manualDependencyRow, 0)
	for rs.Next() {
		var r manualDependencyRow
		if err := rs.Scan(
			&r.ID, &r.TenantID,
			&r.SourceNodeType, &r.SourceName,
			&r.SourceNamespace, &r.SourceCluster,
			&r.SourceARN, &r.SourceAccountID, &r.SourceRegion,
			&r.DestNodeType, &r.DestName,
			&r.DestNamespace, &r.DestCluster,
			&r.DestARN, &r.DestAccountID, &r.DestRegion,
			&r.RelationshipType, &r.Notes,
			&r.DeclaredByUserID,
			&r.ResolvedSourceNodeID, &r.ResolvedDestNodeID,
			&r.ResolutionStatus,
		); err != nil {
			return nil, fmt.Errorf("scan manual dependency row: %w", err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("iterate manual dependency rows: %w", err)
	}
	return out, nil
}

// persistRowUpdates batches per-row UPDATEs in a single transaction. Empty
// resolved node IDs flip to NULL; empty error / candidates flip to NULL too.
// last_resolved_at is always advanced to NOW().
func persistRowUpdates(dbManager *database.DatabaseManager, updates []manualDependencyUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	tx, err := dbManager.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx for manual row updates: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, u := range updates {
		srcCandidatesJSON, err := candidatesToJSON(u.SourceMatchCandidates)
		if err != nil {
			return err
		}
		dstCandidatesJSON, err := candidatesToJSON(u.DestMatchCandidates)
		if err != nil {
			return err
		}

		_, err = tx.Exec(`UPDATE kg_manual_dependencies SET
			resolution_status = $1,
			resolved_source_node_id = $2,
			resolved_dest_node_id = $3,
			resolution_error = $4,
			source_match_count = $5,
			dest_match_count = $6,
			source_match_candidates = $7,
			dest_match_candidates = $8,
			last_resolved_at = NOW()
			WHERE id = $9`,
			u.ResolutionStatus,
			nullableUUID(u.ResolvedSourceNodeID),
			nullableUUID(u.ResolvedDestNodeID),
			nullableString(u.ResolutionError),
			nullableInt(u.SourceMatchCount),
			nullableInt(u.DestMatchCount),
			srcCandidatesJSON,
			dstCandidatesJSON,
			u.ID,
		)
		if err != nil {
			return fmt.Errorf("update manual dependency row %d: %w", u.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit manual row updates: %w", err)
	}
	return nil
}

// candidatesToJSON marshals the candidate slice for jsonb storage. Empty
// slices map to SQL NULL (not "[]") so the column accurately reflects "no
// candidates known" vs "we tried to populate this".
func candidatesToJSON(c []ManualMatchCandidate) (interface{}, error) {
	if len(c) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal candidates: %w", err)
	}
	return b, nil
}

// nullableUUID returns a sql.NullString that translates "" -> NULL. The DB
// columns are UUID — passing the empty string as text fails the implicit
// cast, so we must explicitly hand sql.NullString to the driver.
func nullableUUID(s string) sql.NullString {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullableInt(n int) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(n), Valid: true}
}
