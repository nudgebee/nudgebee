package flow_sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"nudgebee/services/internal/database"
)

// ManualDependencyDedupeIndex is the partial unique index from migration
// V743 that enforces "one active row per (tenant, relationship_type, source
// endpoint, dest endpoint)". When a duplicate INSERT trips this index,
// translateDuplicateError converts the raw pq.Error into
// ErrManualDependencyDuplicate so an import can surface a friendly per-item
// rejection.
const ManualDependencyDedupeIndex = "idx_kg_manual_deps_dedupe"

// pgUniqueViolationCode is the Postgres SQLSTATE for unique_violation
// (https://www.postgresql.org/docs/current/errcodes-appendix.html). Pulled
// out as a named constant so a future reader doesn't have to look up "23505".
const pgUniqueViolationCode = "23505"

// ErrManualDependencyDuplicate is returned by Create / Update when the
// dedupe partial unique index rejects the INSERT or UPDATE. Callers
// (notably the declarations import) propagate this verbatim to the operator
// so they understand they need to update the existing row or delete it
// before importing the same document again.
var ErrManualDependencyDuplicate = errors.New("manual dependency already declared")

// ErrTenantIDRequired is the sentinel for the universal "no tenant_id"
// guard at the top of every repository method. Hoisted to a package-level
// var both to satisfy SQLE-style "no duplicated literals" rules and to give
// callers a typed value they can `errors.Is`-check (the handler layer
// surfaces this as 400 rather than 500).
var ErrTenantIDRequired = errors.New("tenant_id is required")

// ErrManualDependencyForbidden: the caller may not write in every account the
// dependency touches.
var ErrManualDependencyForbidden = errors.New("you do not have write access to every account this dependency touches")

// ErrManualDependencyNodeNotFound: a node picked to pin an endpoint is not an
// active node of the tenant.
var ErrManualDependencyNodeNotFound = errors.New("the picked node is not in this tenant's graph")

// ManualDependency is the public DTO returned by RPC handlers. Field shape
// mirrors kg_manual_dependencies one-to-one; nullable text columns surface
// as zero-value strings in this DTO and are translated to NULL at write time
// via the nullable* helpers.
type ManualDependency struct {
	ID                    int64                  `json:"id"`
	TenantID              string                 `json:"tenant_id"`
	SourceNodeType        string                 `json:"source_node_type"`
	SourceName            string                 `json:"source_name"`
	SourceNamespace       string                 `json:"source_namespace,omitempty"`
	SourceCluster         string                 `json:"source_cluster,omitempty"`
	SourceARN             string                 `json:"source_arn,omitempty"`
	SourceAccountID       string                 `json:"source_account_id,omitempty"`
	SourceRegion          string                 `json:"source_region,omitempty"`
	DestNodeType          string                 `json:"dest_node_type"`
	DestName              string                 `json:"dest_name"`
	DestNamespace         string                 `json:"dest_namespace,omitempty"`
	DestCluster           string                 `json:"dest_cluster,omitempty"`
	DestARN               string                 `json:"dest_arn,omitempty"`
	DestAccountID         string                 `json:"dest_account_id,omitempty"`
	DestRegion            string                 `json:"dest_region,omitempty"`
	RelationshipType      string                 `json:"relationship_type"`
	Notes                 string                 `json:"notes,omitempty"`
	DeclaredByUserID      string                 `json:"declared_by_user_id,omitempty"`
	ResolvedSourceNodeID  string                 `json:"resolved_source_node_id,omitempty"`
	ResolvedDestNodeID    string                 `json:"resolved_dest_node_id,omitempty"`
	ResolutionStatus      string                 `json:"resolution_status"`
	ResolutionError       string                 `json:"resolution_error,omitempty"`
	SourceMatchCount      int                    `json:"source_match_count,omitempty"`
	DestMatchCount        int                    `json:"dest_match_count,omitempty"`
	SourceMatchCandidates []ManualMatchCandidate `json:"source_match_candidates,omitempty"`
	DestMatchCandidates   []ManualMatchCandidate `json:"dest_match_candidates,omitempty"`
	LastResolvedAt        *time.Time             `json:"last_resolved_at,omitempty"`
	IsActive              bool                   `json:"is_active"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}

func (d *ManualDependency) sourceEndpoint() ManualEndpoint {
	return ManualEndpoint{
		NodeType: d.SourceNodeType, Name: d.SourceName,
		Namespace: d.SourceNamespace, Cluster: d.SourceCluster,
		ARN: d.SourceARN, AccountID: d.SourceAccountID, Region: d.SourceRegion,
	}
}

func (d *ManualDependency) destEndpoint() ManualEndpoint {
	return ManualEndpoint{
		NodeType: d.DestNodeType, Name: d.DestName,
		Namespace: d.DestNamespace, Cluster: d.DestCluster,
		ARN: d.DestARN, AccountID: d.DestAccountID, Region: d.DestRegion,
	}
}

// ManualDependencyRepository owns all SQL against kg_manual_dependencies
// and the kg_edges-side mutations needed to keep the edge layer in sync
// with row lifecycle events (delete row → delete the matching edge in the
// same transaction).
type ManualDependencyRepository struct {
	dbManager *database.DatabaseManager
	resolver  *ManualEndpointResolver
	logger    *slog.Logger
	// authorize, when set, must allow every account a user write touches
	// (see touchedAccounts). Unset for internal callers such as the build.
	authorize ManualAccountAuthorizer
}

// WithAccountAuthorizer returns the repository with user writes checked
// against authorize: create, update, delete, pin, single re-resolve and every
// imported dependency.
func (r *ManualDependencyRepository) WithAccountAuthorizer(authorize ManualAccountAuthorizer) *ManualDependencyRepository {
	c := *r
	c.authorize = authorize
	return &c
}

// touchedAccounts are the accounts a dependency reaches: those of its endpoint
// nodes (resolved or picked) and of its endpoints' account qualifiers, which
// is where an unmatched endpoint will resolve. "" stands for a tenant-wide
// node (one outside any connected account is keyed to the tenant id).
//
// An unmatched endpoint with no account qualifier constrains nothing here; the
// build may later resolve it to a node anywhere in the tenant.
func (r *ManualDependencyRepository) touchedAccounts(tenantID string, endpoints []ManualEndpoint, nodeIDs []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == tenantID {
			id = ""
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	ids := parseNodeIDs(nodeIDs)
	if len(ids) > 0 {
		// A node whose account no longer exists in the tenant constrains
		// nothing: no one manages that account, and counting it would lock
		// even a tenant admin out of the row (HasAccountAccess only knows
		// existing accounts).
		var accounts []string
		if err := r.dbManager.Db.Select(&accounts, `SELECT DISTINCT COALESCE(n.cloud_account_id::text, '') FROM knowledge_graph_node n
			WHERE n.tenant_id = $1 AND n.id = ANY($2::uuid[])
			  AND (n.cloud_account_id IS NULL OR n.cloud_account_id::text = $3
			       OR EXISTS (SELECT 1 FROM cloud_accounts a WHERE a.tenant = $1 AND a.id::text = n.cloud_account_id::text))`,
			tenantID, pq.Array(ids), tenantID); err != nil {
			return nil, fmt.Errorf("look up endpoint node accounts: %w", err)
		}
		for _, a := range accounts {
			add(a)
		}
	}
	var qualifiers []string
	for _, e := range endpoints {
		if q := strings.TrimSpace(e.AccountID); q != "" {
			qualifiers = append(qualifiers, q)
		}
	}
	if len(qualifiers) > 0 {
		var accounts []string
		if err := r.dbManager.Db.Select(&accounts, `SELECT id::text FROM cloud_accounts
			WHERE tenant = $1 AND (id::text = ANY($2::text[]) OR account_number = ANY($2::text[]))`, tenantID, pq.Array(qualifiers)); err != nil {
			return nil, fmt.Errorf("look up qualifier accounts: %w", err)
		}
		for _, a := range accounts {
			add(a)
		}
	}
	return out, nil
}

// checkWrite refuses the write unless the caller may write in every account
// the endpoints and nodes touch. A no-op without an authorizer.
func (r *ManualDependencyRepository) checkWrite(tenantID string, endpoints []ManualEndpoint, nodeIDs ...string) error {
	if r.authorize == nil {
		return nil
	}
	accounts, err := r.touchedAccounts(tenantID, endpoints, nodeIDs)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if !r.authorize(a) {
			return ErrManualDependencyForbidden
		}
	}
	return nil
}

// checkExisting checks a write to a stored row against what it touches now.
func (r *ManualDependencyRepository) checkExisting(dep *ManualDependency) error {
	return r.checkWrite(dep.TenantID, []ManualEndpoint{dep.sourceEndpoint(), dep.destEndpoint()},
		dep.ResolvedSourceNodeID, dep.ResolvedDestNodeID)
}

// checkProposed checks a write that would store these endpoints, against the
// nodes they resolve to today. The resolver only reads, so nothing is
// written for a refused caller.
func (r *ManualDependencyRepository) checkProposed(ctx context.Context, tenantID string, dep ManualDependency) error {
	if r.authorize == nil {
		return nil
	}
	resolved := func(e ManualEndpoint) (string, error) {
		res, err := r.resolver.Resolve(ctx, tenantID, e)
		if err != nil {
			return "", err
		}
		if res.Status == EndpointStatusResolved {
			return res.NodeID, nil
		}
		return "", nil
	}
	src, err := resolved(dep.sourceEndpoint())
	if err != nil {
		return fmt.Errorf("resolve source endpoint: %w", err)
	}
	dst, err := resolved(dep.destEndpoint())
	if err != nil {
		return fmt.Errorf("resolve dest endpoint: %w", err)
	}
	return r.checkWrite(tenantID, []ManualEndpoint{dep.sourceEndpoint(), dep.destEndpoint()}, src, dst)
}

// CheckExistingWrite is the authorization check for a write to stored row id
// that changes nothing but its resolution (a single re-resolve).
func (r *ManualDependencyRepository) CheckExistingWrite(ctx context.Context, tenantID string, id int64) error {
	dep, err := r.Get(ctx, tenantID, id)
	if err != nil {
		return err
	}
	return r.checkExisting(dep)
}

// NewManualDependencyRepository constructs a repository bound to the given
// DB connection. Resolver is constructed on-demand and shared across calls.
func NewManualDependencyRepository(dbManager *database.DatabaseManager, logger *slog.Logger) *ManualDependencyRepository {
	if logger == nil {
		logger = slog.Default()
	}
	return &ManualDependencyRepository{
		dbManager: dbManager,
		resolver:  NewManualEndpointResolver(dbManager, logger),
		logger:    logger,
	}
}

// ValidateRelationshipType checks against the supported set. Returned error
// is suitable for surfacing back to the caller.
func ValidateRelationshipType(rt string) error {
	switch rt {
	case "CALLS", "PUBLISHES_TO", "SUBSCRIBES_TO":
		return nil
	default:
		return fmt.Errorf("unsupported relationship_type %q (must be one of CALLS, PUBLISHES_TO, SUBSCRIBES_TO)", rt)
	}
}

// ValidateEndpoint enforces the loose match contract: node_type + name
// required, everything else optional. Returns nil when the endpoint is
// acceptable.
func ValidateEndpoint(side EndpointSide, e ManualEndpoint) error {
	if e.NodeType == "" {
		return fmt.Errorf("%s_node_type is required", side)
	}
	if e.Name == "" {
		return fmt.Errorf("%s_name is required", side)
	}
	return nil
}

// List returns every active row for the tenant. statusFilter (when
// non-empty) narrows to rows whose resolution_status is one of the named
// values.
func (r *ManualDependencyRepository) List(ctx context.Context, tenantID string, statusFilter []string) ([]ManualDependency, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	query := `SELECT ` + manualDependencyColumns + ` FROM kg_manual_dependencies
		WHERE tenant_id = $1 AND is_active = true`
	args := []interface{}{tenantID}
	if len(statusFilter) > 0 {
		query += ` AND resolution_status = ANY($2::text[])`
		args = append(args, pq.Array(statusFilter))
	}
	query += ` ORDER BY id`

	rows, err := r.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list manual dependencies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanManualDependencyRows(rows)
}

// Get fetches one row by id, scoped to tenant. Returns sql.ErrNoRows when
// the row is absent (already deleted or different tenant).
func (r *ManualDependencyRepository) Get(ctx context.Context, tenantID string, id int64) (*ManualDependency, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	query := `SELECT ` + manualDependencyColumns + ` FROM kg_manual_dependencies
		WHERE tenant_id = $1 AND id = $2 AND is_active = true`
	rows, err := r.dbManager.Query(query, tenantID, id)
	if err != nil {
		return nil, fmt.Errorf("get manual dependency: %w", err)
	}
	defer func() { _ = rows.Close() }()
	res, err := scanManualDependencyRows(rows)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, sql.ErrNoRows
	}
	return &res[0], nil
}

// Create inserts the row and synchronously resolves both endpoints. The
// returned DTO reflects post-resolve state so the caller can show the
// operator immediately whether the row resolved cleanly. ON CONFLICT clause
// is intentionally absent: the unique dedupe index rejects duplicate
// (tenant, source, dest, relationship_type) tuples. The raw pq error gets
// translated to ErrManualDependencyDuplicate via translateDuplicateError so
// re-imports surface a friendly per-item rejection instead of a SQLSTATE.
func (r *ManualDependencyRepository) Create(ctx context.Context, dep ManualDependency) (*ManualDependency, error) {
	return r.createPinned(ctx, dep, "", "")
}

// createPinned is Create with either side optionally pinned to a node id the
// caller already knows (an import binding a dependency to a node of the same
// document). The pins are checked before the INSERT, so a refused pin leaves
// no row behind; a pinned side is stored as the operator's pick and is not
// sent through the resolver.
func (r *ManualDependencyRepository) createPinned(ctx context.Context, dep ManualDependency, srcPin, dstPin string) (*ManualDependency, error) {
	if dep.TenantID == "" {
		return nil, ErrTenantIDRequired
	}
	if err := ValidateRelationshipType(dep.RelationshipType); err != nil {
		return nil, err
	}
	if err := ValidateEndpoint(EndpointSideSource, dep.sourceEndpoint()); err != nil {
		return nil, err
	}
	if err := ValidateEndpoint(EndpointSideDest, dep.destEndpoint()); err != nil {
		return nil, err
	}
	if err := r.checkProposed(ctx, dep.TenantID, dep); err != nil {
		return nil, err
	}
	srcPin, dstPin = strings.TrimSpace(srcPin), strings.TrimSpace(dstPin)
	if srcPin != "" || dstPin != "" {
		if err := r.requireTenantNodes(dep.TenantID, srcPin, dstPin); err != nil {
			return nil, err
		}
		if err := r.checkWrite(dep.TenantID, nil, srcPin, dstPin); err != nil {
			return nil, err
		}
	}

	insertQuery := `INSERT INTO kg_manual_dependencies (
		tenant_id, source_node_type, source_name, source_namespace, source_cluster,
		source_arn, source_account_id, source_region,
		dest_node_type, dest_name, dest_namespace, dest_cluster,
		dest_arn, dest_account_id, dest_region,
		relationship_type, notes, declared_by_user_id
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	RETURNING id`

	row, err := r.dbManager.QueryRow(insertQuery,
		dep.TenantID,
		dep.SourceNodeType, dep.SourceName, nullableString(dep.SourceNamespace), nullableString(dep.SourceCluster),
		nullableString(dep.SourceARN), nullableString(dep.SourceAccountID), nullableString(dep.SourceRegion),
		dep.DestNodeType, dep.DestName, nullableString(dep.DestNamespace), nullableString(dep.DestCluster),
		nullableString(dep.DestARN), nullableString(dep.DestAccountID), nullableString(dep.DestRegion),
		dep.RelationshipType, nullableString(dep.Notes), nullableUUID(dep.DeclaredByUserID))
	if err != nil {
		return nil, translateDuplicateError(err, dep, "insert manual dependency")
	}
	if err := row.Scan(&dep.ID); err != nil {
		return nil, translateDuplicateError(err, dep, "insert manual dependency returning id")
	}

	switch {
	case srcPin == "" && dstPin == "":
		return r.ResolveAndPersist(ctx, dep.TenantID, dep.ID)
	case srcPin != "" && dstPin != "":
		if err := r.pinBoth(dep.TenantID, dep.ID, srcPin, dstPin); err != nil {
			return nil, err
		}
		return r.Get(ctx, dep.TenantID, dep.ID)
	}
	return r.resolveAndPinPartial(ctx, dep.TenantID, dep.ID, &dep, srcPin, dstPin)
}

// translateDuplicateError converts a unique-violation on the dedupe index
// into ErrManualDependencyDuplicate (with the offending endpoint tuple
// included for operator context). All other errors pass through with the
// supplied prefix. Nil-in, nil-out so callers don't need a guard.
//
// The Postgres-driver-specific pq.Error check is deliberately tight: only
// SQLSTATE 23505 (unique_violation) on the exact constraint name counts.
// Other 23505s (e.g. a future foreign-key violation against a different
// constraint) fall through to the generic wrap so they're not silently
// recategorized as "duplicate."
func translateDuplicateError(err error, dep ManualDependency, wrapPrefix string) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolationCode && pqErr.Constraint == ManualDependencyDedupeIndex {
		return fmt.Errorf(
			"%w: an active row for (%s %s → %s %s, %s) already exists for this tenant; update the existing row or delete it first to re-import",
			ErrManualDependencyDuplicate,
			dep.SourceNodeType, formatEndpointKeyForError(dep.SourceName, dep.SourceNamespace, dep.SourceCluster, dep.SourceARN),
			dep.DestNodeType, formatEndpointKeyForError(dep.DestName, dep.DestNamespace, dep.DestCluster, dep.DestARN),
			dep.RelationshipType,
		)
	}
	return fmt.Errorf("%s: %w", wrapPrefix, err)
}

// formatEndpointKeyForError renders an endpoint in the form an operator
// would type it back: ARN when present, else namespace/name@cluster, else
// the bare name. Used only in the duplicate-error message so the operator
// can immediately recognize which row collided.
func formatEndpointKeyForError(name, namespace, cluster, arn string) string {
	if arn != "" {
		return arn
	}
	if namespace != "" && cluster != "" {
		return fmt.Sprintf("%s/%s@%s", namespace, name, cluster)
	}
	if namespace != "" {
		return fmt.Sprintf("%s/%s", namespace, name)
	}
	return name
}

// Update edits the endpoint identifiers + notes on an existing row and
// re-resolves it. ID and tenant must match.
func (r *ManualDependencyRepository) Update(ctx context.Context, tenantID string, id int64, patch ManualDependency) (*ManualDependency, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	if patch.RelationshipType != "" {
		if err := ValidateRelationshipType(patch.RelationshipType); err != nil {
			return nil, err
		}
	}
	if err := ValidateEndpoint(EndpointSideSource, patch.sourceEndpoint()); err != nil {
		return nil, err
	}
	if err := ValidateEndpoint(EndpointSideDest, patch.destEndpoint()); err != nil {
		return nil, err
	}
	// The row as it is and as it would be: an edit may neither reach into an
	// account the caller does not manage nor take over a row that does.
	if r.authorize != nil {
		current, err := r.Get(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		if err := r.checkExisting(current); err != nil {
			return nil, err
		}
		if err := r.checkProposed(ctx, tenantID, patch); err != nil {
			return nil, err
		}
	}

	_, err := r.dbManager.Exec(`UPDATE kg_manual_dependencies SET
		source_node_type = $1, source_name = $2,
		source_namespace = $3, source_cluster = $4,
		source_arn = $5, source_account_id = $6, source_region = $7,
		dest_node_type = $8, dest_name = $9,
		dest_namespace = $10, dest_cluster = $11,
		dest_arn = $12, dest_account_id = $13, dest_region = $14,
		relationship_type = COALESCE(NULLIF($15, ''), relationship_type),
		notes = $16
		WHERE tenant_id = $17 AND id = $18 AND is_active = true`,
		patch.SourceNodeType, patch.SourceName,
		nullableString(patch.SourceNamespace), nullableString(patch.SourceCluster),
		nullableString(patch.SourceARN), nullableString(patch.SourceAccountID), nullableString(patch.SourceRegion),
		patch.DestNodeType, patch.DestName,
		nullableString(patch.DestNamespace), nullableString(patch.DestCluster),
		nullableString(patch.DestARN), nullableString(patch.DestAccountID), nullableString(patch.DestRegion),
		patch.RelationshipType, nullableString(patch.Notes),
		tenantID, id,
	)
	if err != nil {
		// Endpoint edits can move a row to collide with another active row's
		// dedupe key — same friendly translation as Create.
		return nil, translateDuplicateError(err, patch, "update manual dependency")
	}
	return r.ResolveAndPersist(ctx, tenantID, id)
}

// SoftDelete flips is_active=false on the row AND removes the matching
// kg_edges row (source='manual', manual_dependency_id=id) atomically.
// Mirrors the explicit-delete decision in the design plan: row delete must
// purge the ghost edge in the same transaction so correlation stops seeing
// the deleted dependency on the next event.
//
// Returns sql.ErrNoRows when the row doesn't exist for the supplied tenant.
// Without the rows-affected check, deleting a non-existent or cross-tenant
// id would silently succeed and the handler would return `{deleted: true}`,
// masking auth/existence bugs.
func (r *ManualDependencyRepository) SoftDelete(ctx context.Context, tenantID string, id int64) error {
	if tenantID == "" {
		return ErrTenantIDRequired
	}
	if r.authorize != nil {
		dep, err := r.Get(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if err := r.checkExisting(dep); err != nil {
			return err
		}
	}
	tx, err := r.dbManager.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx for manual delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`UPDATE kg_manual_dependencies
		SET is_active = false WHERE tenant_id = $1 AND id = $2 AND is_active = true`, tenantID, id)
	if err != nil {
		return fmt.Errorf("soft-delete row: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("soft-delete rows-affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	// Guard the bigint cast against rows where dedup priority stripped the
	// manual_dependency_id property (k8s priority 1 wins, manual properties
	// might be merged via MetricsToMerge but the dedup loser could land with
	// no manual_dependency_id at all). `WHERE properties ? 'manual_dependency_id'`
	// short-circuits the cast for those rows so the DELETE doesn't throw
	// 22P02 invalid_text_representation.
	if _, err := tx.Exec(`DELETE FROM knowledge_graph_edge
		WHERE tenant_id = $1 AND source = 'manual'
		AND properties ? 'manual_dependency_id'
		AND NULLIF(properties->>'manual_dependency_id', '')::bigint = $2`, tenantID, id); err != nil {
		return fmt.Errorf("delete manual edge: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit manual delete: %w", err)
	}
	return nil
}

// SoftDeleteAll is the panic-button rollback: deactivates every row for the
// tenant and removes every source='manual' edge. Returns the count of rows
// affected so the caller can confirm in logs.
func (r *ManualDependencyRepository) SoftDeleteAll(ctx context.Context, tenantID string) (rowsDeactivated int64, edgesDeleted int64, err error) {
	if tenantID == "" {
		return 0, 0, ErrTenantIDRequired
	}
	tx, err := r.dbManager.BeginTx()
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx for manual delete-all: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`UPDATE kg_manual_dependencies SET is_active = false
		WHERE tenant_id = $1 AND is_active = true`, tenantID)
	if err != nil {
		return 0, 0, fmt.Errorf("deactivate rows: %w", err)
	}
	rowsDeactivated, _ = res.RowsAffected()

	res, err = tx.Exec(`DELETE FROM knowledge_graph_edge
		WHERE tenant_id = $1 AND source = 'manual'`, tenantID)
	if err != nil {
		return 0, 0, fmt.Errorf("delete manual edges: %w", err)
	}
	edgesDeleted, _ = res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit manual delete-all: %w", err)
	}
	return rowsDeactivated, edgesDeleted, nil
}

// ResolveAndPersist runs the resolver for both endpoints of one row and
// writes the result back. Used by Create / Update / Reresolve. The flow
// source uses the lower-level processRow helper to batch updates across
// many rows per cycle; this single-row path is for the synchronous RPC.
func (r *ManualDependencyRepository) ResolveAndPersist(ctx context.Context, tenantID string, id int64) (*ManualDependency, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	dep, err := r.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	srcResult, err := r.resolver.Resolve(ctx, tenantID, dep.sourceEndpoint())
	if err != nil {
		return nil, fmt.Errorf("resolve source endpoint: %w", err)
	}
	dstResult, err := r.resolver.Resolve(ctx, tenantID, dep.destEndpoint())
	if err != nil {
		return nil, fmt.Errorf("resolve dest endpoint: %w", err)
	}

	update := buildUpdateFromResults(id, srcResult, dstResult)
	if err := persistRowUpdates(r.dbManager, []manualDependencyUpdate{update}); err != nil {
		return nil, fmt.Errorf("persist resolution: %w", err)
	}

	return r.Get(ctx, tenantID, id)
}

// requireTenantNodes refuses node ids that are not active nodes of the tenant.
func (r *ManualDependencyRepository) requireTenantNodes(tenantID string, nodeIDs ...string) error {
	var picked []string
	for _, id := range nodeIDs {
		if id != "" {
			picked = append(picked, id)
		}
	}
	if len(picked) == 0 {
		return nil
	}
	ids := parseNodeIDs(picked)
	if len(ids) != len(picked) {
		return ErrManualDependencyNodeNotFound // not a node id at all
	}
	distinct := map[string]bool{}
	for _, id := range ids {
		distinct[id] = true
	}
	var found int
	if err := r.dbManager.Db.Get(&found, `SELECT count(DISTINCT id) FROM knowledge_graph_node
		WHERE tenant_id = $1 AND is_active = true AND id = ANY($2::uuid[])`, tenantID, pq.Array(ids)); err != nil {
		return fmt.Errorf("check picked nodes: %w", err)
	}
	if found != len(distinct) {
		return ErrManualDependencyNodeNotFound
	}
	return nil
}

// parseNodeIDs keeps the ids that are UUIDs, in canonical form, so lookups
// can use the primary key instead of comparing text.
func parseNodeIDs(nodeIDs []string) []string {
	var out []string
	for _, id := range nodeIDs {
		if u, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
			out = append(out, u.String())
		}
	}
	return out
}

// SetResolvedNodes implements the operator-disambiguate flow: the caller
// supplies a specific candidate node_id for the ambiguous side(s). The
// supplied side is treated as authoritative — the resolver is NOT re-run
// for that side (which would risk overwriting the operator's pick if the
// resolver now matches a different candidate). The unsupplied side, if
// any, is re-resolved and its result determines the row's final
// resolution_status.
//
// When neither side is supplied, this delegates to ResolveAndPersist.
// When both sides are supplied, the row is pinned to fully `resolved`.
func (r *ManualDependencyRepository) SetResolvedNodes(ctx context.Context, tenantID string, id int64, sourceNodeID, destNodeID string) (*ManualDependency, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	dep, err := r.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	srcPick := strings.TrimSpace(sourceNodeID)
	dstPick := strings.TrimSpace(destNodeID)

	// A pin must name nodes of this tenant's graph: the id comes from the
	// caller, and the edge would otherwise point at another tenant's node.
	if err := r.requireTenantNodes(tenantID, srcPick, dstPick); err != nil {
		return nil, err
	}
	if err := r.checkExisting(dep); err != nil {
		return nil, err
	}
	if err := r.checkWrite(tenantID, nil, srcPick, dstPick); err != nil {
		return nil, err
	}

	// Neither side picked: nothing to pin — fall back to plain re-resolve.
	if srcPick == "" && dstPick == "" {
		return r.ResolveAndPersist(ctx, tenantID, id)
	}

	// Both sides picked: pin authoritatively to resolved. No resolver call.
	if srcPick != "" && dstPick != "" {
		if err := r.pinBoth(tenantID, id, srcPick, dstPick); err != nil {
			return nil, err
		}
		return r.Get(ctx, tenantID, id)
	}

	// Partial pick — only one side supplied. Re-resolve the OTHER side via
	// the resolver, then merge with the operator's pin. The pinned side is
	// never sent through the resolver; the resolver's candidate ranking has
	// no authority over an explicit operator choice.
	return r.resolveAndPinPartial(ctx, tenantID, id, dep, srcPick, dstPick)
}

// pinBoth writes both sides as the operator's picks and marks the row
// resolved, without a resolver call.
func (r *ManualDependencyRepository) pinBoth(tenantID string, id int64, srcPick, dstPick string) error {
	_, err := r.dbManager.Exec(`UPDATE kg_manual_dependencies SET
		resolved_source_node_id = $1::uuid,
		resolved_dest_node_id = $2::uuid,
		resolution_status = $3,
		resolution_error = NULL,
		source_match_count = 1,
		dest_match_count = 1,
		source_match_candidates = NULL,
		dest_match_candidates = NULL,
		last_resolved_at = NOW()
		WHERE tenant_id = $4 AND id = $5 AND is_active = true`,
		srcPick, dstPick, ManualResolutionResolved, tenantID, id)
	if err != nil {
		return fmt.Errorf("set resolved nodes: %w", err)
	}
	return nil
}

// resolveAndPinPartial handles the case where exactly one side of the row
// is pinned by the operator. Re-resolves the unpicked side, computes the
// new row-level resolution_status from the picked side (= resolved) plus
// the resolver's result on the unpicked side, and writes the row atomically.
func (r *ManualDependencyRepository) resolveAndPinPartial(
	ctx context.Context, tenantID string, id int64, dep *ManualDependency,
	srcPick, dstPick string,
) (*ManualDependency, error) {
	// Synthesize a resolved-result for the pinned side (no resolver call)
	// and a real resolver-result for the unpicked side.
	var srcResult, dstResult *EndpointResolveResult
	if srcPick != "" {
		srcResult = &EndpointResolveResult{Status: EndpointStatusResolved, NodeID: srcPick, MatchCount: 1}
		r2, err := r.resolver.Resolve(ctx, tenantID, dep.destEndpoint())
		if err != nil {
			return nil, fmt.Errorf("resolve dest endpoint during partial pin: %w", err)
		}
		dstResult = r2
	} else {
		dstResult = &EndpointResolveResult{Status: EndpointStatusResolved, NodeID: dstPick, MatchCount: 1}
		r2, err := r.resolver.Resolve(ctx, tenantID, dep.sourceEndpoint())
		if err != nil {
			return nil, fmt.Errorf("resolve source endpoint during partial pin: %w", err)
		}
		srcResult = r2
	}

	// Feed both results through the same row-update builder the flow source
	// uses, so the status fold is consistent across paths.
	// The re-resolved side may land in another account; check it too.
	other := srcResult
	if srcPick != "" {
		other = dstResult
	}
	if other.Status == EndpointStatusResolved {
		if err := r.checkWrite(tenantID, nil, other.NodeID); err != nil {
			return nil, err
		}
	}

	update := buildUpdateFromResults(id, srcResult, dstResult)
	if err := persistRowUpdates(r.dbManager, []manualDependencyUpdate{update}); err != nil {
		return nil, fmt.Errorf("persist partial pin: %w", err)
	}
	return r.Get(ctx, tenantID, id)
}

// ReresolveAll calls ResolveAndPersist for every row matching the filter,
// returning per-row results. statusFilter narrows; pass nil for "all
// active". Errors on individual rows are logged and aggregated; the call
// returns the per-row outcomes either way.
func (r *ManualDependencyRepository) ReresolveAll(ctx context.Context, tenantID string, statusFilter []string) ([]ManualDependency, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	rows, err := r.List(ctx, tenantID, statusFilter)
	if err != nil {
		return nil, err
	}

	out := make([]ManualDependency, 0, len(rows))
	for _, dep := range rows {
		updated, err := r.ResolveAndPersist(ctx, tenantID, dep.ID)
		if err != nil {
			r.logger.Warn("reresolve row failed",
				"id", dep.ID, "tenant_id", tenantID, "error", err)
			continue
		}
		out = append(out, *updated)
	}
	return out, nil
}

// manualDependencyColumns is the SELECT clause shared by List/Get. Order
// must match scanManualDependencyRows below.
const manualDependencyColumns = `id, tenant_id::text,
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
	resolution_status, COALESCE(resolution_error, ''),
	COALESCE(source_match_count, 0), COALESCE(dest_match_count, 0),
	source_match_candidates, dest_match_candidates,
	last_resolved_at,
	is_active, created_at, updated_at`

// scanManualDependencyRows turns a sqlx.Rows over manualDependencyColumns
// into a slice of ManualDependency.
func scanManualDependencyRows(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]ManualDependency, error) {
	out := make([]ManualDependency, 0)
	for rows.Next() {
		var d ManualDependency
		var srcCandRaw, dstCandRaw []byte
		var lastResolvedAt sql.NullTime
		if err := rows.Scan(
			&d.ID, &d.TenantID,
			&d.SourceNodeType, &d.SourceName,
			&d.SourceNamespace, &d.SourceCluster,
			&d.SourceARN, &d.SourceAccountID, &d.SourceRegion,
			&d.DestNodeType, &d.DestName,
			&d.DestNamespace, &d.DestCluster,
			&d.DestARN, &d.DestAccountID, &d.DestRegion,
			&d.RelationshipType, &d.Notes,
			&d.DeclaredByUserID,
			&d.ResolvedSourceNodeID, &d.ResolvedDestNodeID,
			&d.ResolutionStatus, &d.ResolutionError,
			&d.SourceMatchCount, &d.DestMatchCount,
			&srcCandRaw, &dstCandRaw,
			&lastResolvedAt,
			&d.IsActive, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan manual dependency: %w", err)
		}
		if lastResolvedAt.Valid {
			t := lastResolvedAt.Time
			d.LastResolvedAt = &t
		}
		if len(srcCandRaw) > 0 {
			if err := json.Unmarshal(srcCandRaw, &d.SourceMatchCandidates); err != nil {
				return nil, fmt.Errorf("unmarshal source candidates row %d: %w", d.ID, err)
			}
		}
		if len(dstCandRaw) > 0 {
			if err := json.Unmarshal(dstCandRaw, &d.DestMatchCandidates); err != nil {
				return nil, fmt.Errorf("unmarshal dest candidates row %d: %w", d.ID, err)
			}
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate manual dependencies: %w", err)
	}
	return out, nil
}
