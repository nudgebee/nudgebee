package flow_sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/lib/pq"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
)

// manualNodeDedupeIndex is V989's one-active-declaration-per-node index.
const manualNodeDedupeIndex = "idx_kg_manual_nodes_node"

// ManualNodeGraphWriter writes declared nodes into knowledge_graph_node.
// *core.Service satisfies it.
type ManualNodeGraphWriter interface {
	SaveNodes(nodes []*core.DbNode, syncVersion int64) error
}

// ManualNodeRepository owns kg_manual_nodes and the graph writes that keep a
// declaration and its node in step.
type ManualNodeRepository struct {
	dbManager *database.DatabaseManager
	logger    *slog.Logger
	// deferFilterInvalidation leaves dropping the filter-options cache to the
	// caller: an import writes many nodes and drops it once at the end.
	deferFilterInvalidation bool
}

// NewManualNodeRepository binds a repository to the metastore.
func NewManualNodeRepository(dbManager *database.DatabaseManager, logger *slog.Logger) *ManualNodeRepository {
	if logger == nil {
		logger = slog.Default()
	}
	return &ManualNodeRepository{dbManager: dbManager, logger: logger}
}

// ManualNodeCreate is a declaration request. The UI sends the account's id and
// the picked network node's id; an imported document may name the account by
// its provider account number and the network by its resource id or name.
type ManualNodeCreate struct {
	CloudAccountID string                 `json:"cloud_account_id"`
	CloudAccount   string                 `json:"cloud_account"`
	NodeType       string                 `json:"node_type"`
	SpecificType   string                 `json:"specific_type"`
	Name           string                 `json:"name"`
	Region         string                 `json:"region"`
	Namespace      string                 `json:"namespace"`
	NetworkNodeID  string                 `json:"network_node_id"`
	Network        string                 `json:"network"`
	Description    string                 `json:"description"`
	Properties     map[string]interface{} `json:"properties"`
	Labels         map[string]string      `json:"labels"`

	TenantID         string `json:"-"`
	DeclaredByUserID string `json:"-"`
	// Authorize, when set, is asked about the resolved account before
	// anything is read or written for it.
	Authorize ManualAccountAuthorizer `json:"-"`
}

// ManualNodeSchemaAccount is one place a node can be declared, with the types
// it accepts.
type ManualNodeSchemaAccount struct {
	CloudAccountID string                `json:"cloud_account_id,omitempty"`
	AccountName    string                `json:"account_name,omitempty"`
	AccountNumber  string                `json:"account_number,omitempty"`
	CloudProvider  string                `json:"cloud_provider"`
	Kinds          []core.ManualNodeKind `json:"kinds"`
}

// Schema lists the tenant's connected accounts that accept declarations,
// each with its declarable types, plus the tenant-scoped catalogue.
func (r *ManualNodeRepository) Schema(ctx context.Context, tenantID string) ([]ManualNodeSchemaAccount, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	providers := core.RegisteredManualNodeProviders()
	rows, err := r.dbManager.Query(`SELECT id::text, COALESCE(account_name, ''), COALESCE(account_number, ''), cloud_provider
		FROM cloud_accounts
		WHERE tenant = $1 AND status = 'active' AND cloud_provider = ANY($2::text[])
		ORDER BY cloud_provider, account_name`, tenantID, pq.Array(providers))
	if err != nil {
		return nil, fmt.Errorf("list cloud accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ManualNodeSchemaAccount{}
	for rows.Next() {
		var a ManualNodeSchemaAccount
		if err := rows.Scan(&a.CloudAccountID, &a.AccountName, &a.AccountNumber, &a.CloudProvider); err != nil {
			return nil, fmt.Errorf("scan cloud account: %w", err)
		}
		if b, ok := core.ManualNodeBuilderFor(a.CloudProvider); ok {
			a.Kinds = b.Kinds()
			out = append(out, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cloud accounts: %w", err)
	}
	out = append(out, ManualNodeSchemaAccount{CloudProvider: TenantScopedProvider, Kinds: TenantScopedManualNodeKinds()})
	return out, nil
}

// List returns the tenant's live declarations.
func (r *ManualNodeRepository) List(ctx context.Context, tenantID string) ([]ManualNode, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	return r.query(`SELECT `+manualNodeColumns+` FROM kg_manual_nodes
		WHERE tenant_id = $1 AND is_active = true ORDER BY id`, tenantID)
}

// Get returns one live declaration, or sql.ErrNoRows.
func (r *ManualNodeRepository) Get(ctx context.Context, tenantID string, id int64) (*ManualNode, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	rows, err := r.query(`SELECT `+manualNodeColumns+` FROM kg_manual_nodes
		WHERE tenant_id = $1 AND id = $2 AND is_active = true`, tenantID, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return &rows[0], nil
}

// Create declares a node and writes it into the graph. It refuses a resource
// the graph already has from a collector: the user should depend on that node.
func (r *ManualNodeRepository) Create(ctx context.Context, graph ManualNodeGraphWriter, req ManualNodeCreate) (*ManualNode, error) {
	if req.TenantID == "" {
		return nil, ErrTenantIDRequired
	}
	n, err := r.prepare(req)
	if err != nil {
		return nil, err
	}
	return r.createPrepared(ctx, graph, n, req.Authorize)
}

// createPrepared declares a prepared node: the second half of Create, kept
// apart so an import still knows the node's identity when the declaration is
// refused because the node is already in the graph.
func (r *ManualNodeRepository) createPrepared(ctx context.Context, graph ManualNodeGraphWriter, n *ManualNode, authorize ManualAccountAuthorizer) (*ManualNode, error) {
	// cloudInput already checked an account declaration before its lookups;
	// this covers one outside any account, and must precede the graph lookup
	// below so a refused caller cannot learn whether the resource is collected.
	if authorize != nil && !authorize(n.CloudAccountID) {
		return nil, ErrManualNodeForbidden
	}

	var source string
	switch err := r.dbManager.Db.Get(&source, `SELECT COALESCE(source, '') FROM knowledge_graph_node
		WHERE tenant_id = $1 AND id = $2 AND is_active = true`, n.TenantID, n.KGNodeID); {
	case err == nil && source != core.ManualNodeSource:
		return nil, &ManualNodeExistsError{NodeID: n.KGNodeID, Source: source}
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing node: %w", err)
	}

	base, err := json.Marshal(n.baseProperties)
	if err != nil {
		return nil, fmt.Errorf("marshal base properties: %w", err)
	}
	props, labels, err := marshalDetails(n)
	if err != nil {
		return nil, err
	}
	row, err := r.dbManager.QueryRow(`INSERT INTO kg_manual_nodes (
			tenant_id, cloud_account_id, cloud_provider, node_type, specific_type, name,
			region, namespace, network_node_id, description, properties, labels,
			kg_node_id, unique_key, base_properties, declared_by_user_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id`,
		n.TenantID, nullableUUID(n.CloudAccountID), nullableString(n.CloudProvider), n.NodeType, n.SpecificType, n.Name,
		nullableString(n.Region), nullableString(n.Namespace), nullableUUID(n.NetworkNodeID), nullableString(n.Description),
		props, labels, n.KGNodeID, n.UniqueKey, base, nullableUUID(n.DeclaredByUserID))
	if err != nil {
		return nil, translateManualNodeDuplicate(err)
	}
	if err := row.Scan(&n.ID); err != nil {
		return nil, translateManualNodeDuplicate(err)
	}

	// All or nothing: a declaration whose node never reached the graph would
	// look done in the UI while dependencies still can't resolve to it.
	if err := r.writeNode(graph, n); err != nil {
		if _, delErr := r.dbManager.Exec(`UPDATE kg_manual_nodes SET is_active = false WHERE id = $1`, n.ID); delErr != nil {
			r.logger.Error("manual node: failed to withdraw declaration after graph write failed",
				"id", n.ID, "error", delErr)
		}
		return nil, fmt.Errorf("write node to the graph: %w", err)
	}
	return r.Get(ctx, n.TenantID, n.ID)
}

// prepare validates a request and builds the node it declares.
func (r *ManualNodeRepository) prepare(req ManualNodeCreate) (*ManualNode, error) {
	n := &ManualNode{
		TenantID:         req.TenantID,
		NodeType:         strings.TrimSpace(req.NodeType),
		SpecificType:     strings.TrimSpace(req.SpecificType),
		Name:             strings.TrimSpace(req.Name),
		Region:           strings.TrimSpace(req.Region),
		Namespace:        strings.TrimSpace(req.Namespace),
		Description:      strings.TrimSpace(req.Description),
		Properties:       req.Properties,
		Labels:           req.Labels,
		Status:           ManualNodeStatusActive,
		DeclaredByUserID: req.DeclaredByUserID,
	}

	account := strings.TrimSpace(req.CloudAccountID)
	if account == "" {
		account = strings.TrimSpace(req.CloudAccount)
	}

	var built *core.DbNode
	if account == "" {
		if n.SpecificType == "" {
			n.SpecificType = n.NodeType
		}
		if n.NodeType == "" {
			n.NodeType = n.SpecificType
		}
		node, err := buildTenantScopedNode(n)
		if err != nil {
			return nil, err
		}
		built = node
	} else {
		in, err := r.cloudInput(n, account, req)
		if err != nil {
			return nil, err
		}
		b, ok := core.ManualNodeBuilderFor(in.CloudProvider)
		if !ok {
			return nil, fmt.Errorf("nodes cannot be declared in %s accounts", in.CloudProvider)
		}
		node, err := b.Build(in)
		if err != nil {
			return nil, err
		}
		built = node
		n.NodeType = string(node.NodeType)
		// Only Kubernetes resources live in a namespace; keep what the builder
		// keyed on, and nothing for a cloud resource.
		n.Namespace, _ = node.Properties["namespace"].(string)
	}

	if err := validateManualNodeDetails(n); err != nil {
		return nil, err
	}
	n.KGNodeID = built.ID
	n.UniqueKey = built.UniqueKey
	n.baseProperties = core.BaseProperties(built)
	if !core.IsManualNodeType(core.NodeType(n.NodeType)) {
		return nil, fmt.Errorf("node type %q cannot be declared", n.NodeType)
	}
	return n, nil
}

// cloudInput resolves the account and network a cloud declaration names.
func (r *ManualNodeRepository) cloudInput(n *ManualNode, account string, req ManualNodeCreate) (core.ManualNodeInput, error) {
	var acct struct {
		ID            string `db:"id"`
		CloudProvider string `db:"cloud_provider"`
		AccountNumber string `db:"account_number"`
		AccountName   string `db:"account_name"`
	}
	err := r.dbManager.Db.Get(&acct, `SELECT id::text AS id, cloud_provider, COALESCE(account_number, '') AS account_number,
			COALESCE(account_name, '') AS account_name
		FROM cloud_accounts
		WHERE tenant = $1 AND status = 'active' AND (id::text = $2 OR account_number = $2)
		ORDER BY (id::text = $2) DESC
		LIMIT 1`, n.TenantID, account)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ManualNodeInput{}, fmt.Errorf("no active cloud account %q", account)
	}
	if err != nil {
		return core.ManualNodeInput{}, fmt.Errorf("look up cloud account: %w", err)
	}
	n.CloudAccountID = acct.ID
	n.CloudProvider = acct.CloudProvider
	// Before the network and graph lookups, so a refused caller learns nothing
	// about the account's VPCs or collected resources.
	if req.Authorize != nil && !req.Authorize(acct.ID) {
		return core.ManualNodeInput{}, ErrManualNodeForbidden
	}

	in := core.ManualNodeInput{
		TenantID:       n.TenantID,
		CloudAccountID: acct.ID,
		AccountNumber:  acct.AccountNumber,
		CloudProvider:  acct.CloudProvider,
		SpecificType:   n.SpecificType,
		Name:           n.Name,
		Region:         n.Region,
		Namespace:      n.Namespace,
		// The k8s source stamps the account name as the cluster name.
		AccountName: acct.AccountName,
	}
	network := strings.TrimSpace(req.NetworkNodeID)
	byName := false
	if network == "" {
		network, byName = strings.TrimSpace(req.Network), true
	}
	if network != "" {
		net, nodeID, err := r.lookupNetwork(n.TenantID, acct.ID, network, byName)
		if err != nil {
			return core.ManualNodeInput{}, err
		}
		in.Network = net
		n.NetworkNodeID = nodeID
	}
	return in, nil
}

// lookupNetwork reads the VPC / VNet a declaration sits in off the graph: the
// unique key carries that node's name, which only the graph knows.
func (r *ManualNodeRepository) lookupNetwork(tenantID, accountID, ref string, byName bool) (*core.ManualNodeNetwork, string, error) {
	query := `SELECT id::text, COALESCE(properties->>'resource_id', ''), COALESCE(properties->>'name', '')
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND cloud_account_id = $2 AND node_type = $3 AND is_active = true AND level = 'Tenant'`
	if byName {
		query += ` AND (properties->>'resource_id' = $4 OR properties->>'name' = $4) LIMIT 2`
	} else {
		query += ` AND id::text = $4 LIMIT 2`
	}
	rows, err := r.dbManager.Query(query, tenantID, accountID, string(core.NodeTypeVPC), ref)
	if err != nil {
		return nil, "", fmt.Errorf("look up network: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var found []struct{ id, resourceID, name string }
	for rows.Next() {
		var f struct{ id, resourceID, name string }
		if err := rows.Scan(&f.id, &f.resourceID, &f.name); err != nil {
			return nil, "", fmt.Errorf("scan network: %w", err)
		}
		found = append(found, f)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate networks: %w", err)
	}
	switch len(found) {
	case 0:
		return nil, "", fmt.Errorf("no network %q in this account's graph", ref)
	case 1:
		return &core.ManualNodeNetwork{ResourceID: found[0].resourceID, Name: found[0].name}, found[0].id, nil
	default:
		return nil, "", fmt.Errorf("more than one network matches %q; use its id", ref)
	}
}

// Update changes a declaration's descriptive data and rewrites its node.
func (r *ManualNodeRepository) Update(ctx context.Context, graph ManualNodeGraphWriter, tenantID string, id int64, patch ManualNodeDetailsPatch) (*ManualNode, error) {
	n, err := r.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if n.Status == ManualNodeStatusAdopted {
		return nil, ErrManualNodeAdopted
	}
	n.Description = strings.TrimSpace(patch.Description)
	n.Properties = patch.Properties
	n.Labels = patch.Labels
	if err := validateManualNodeDetails(n); err != nil {
		return nil, err
	}
	props, labels, err := marshalDetails(n)
	if err != nil {
		return nil, err
	}
	if _, err := r.dbManager.Exec(`UPDATE kg_manual_nodes
		SET description = $1, properties = $2, labels = $3
		WHERE tenant_id = $4 AND id = $5 AND is_active = true`,
		nullableString(n.Description), props, labels, tenantID, id); err != nil {
		return nil, fmt.Errorf("update manual node: %w", err)
	}
	if err := r.writeNode(graph, n); err != nil {
		return nil, fmt.Errorf("write node to the graph: %w", err)
	}
	return r.Get(ctx, tenantID, id)
}

// writeNode upserts the declaration's node.
//
// Neither a build lock nor a version above the current build is needed: the
// node is never swept (see core.ManualNodeTypes), so the version only records
// when it was written. A build running now may still write this tenant's
// previous view of the node; the next build re-emits the declaration.
func (r *ManualNodeRepository) writeNode(graph ManualNodeGraphWriter, n *ManualNode) error {
	node, err := n.Materialize()
	if err != nil {
		return err
	}
	version, err := core.NewResourcePatchRepository(r.dbManager, r.logger).CurrentSyncVersion(n.TenantID)
	if err != nil {
		return err
	}
	if err := graph.SaveNodes([]*core.DbNode{node}, version); err != nil {
		return err
	}
	if !r.deferFilterInvalidation {
		r.invalidateFilterOptions(n.TenantID)
	}
	return nil
}

// invalidateFilterOptions drops the tenant's cached filter options (refreshed
// only by a build) so the node appears in the graph's dropdowns at once.
// Best-effort: the next read recomputes them.
func (r *ManualNodeRepository) invalidateFilterOptions(tenantID string) {
	if _, err := r.dbManager.Exec(`DELETE FROM knowledge_graph_filter_options WHERE tenant_id = $1`, tenantID); err != nil {
		r.logger.Warn("manual node: failed to invalidate filter options cache", "tenant_id", tenantID, "error", err)
	}
}

// SoftDelete withdraws a declaration. Its node and the edges touching it are
// tombstoned, unless a collector has adopted the node: then it is no longer
// the declaration's to remove.
func (r *ManualNodeRepository) SoftDelete(ctx context.Context, tenantID string, id int64) (int64, error) {
	if tenantID == "" {
		return 0, ErrTenantIDRequired
	}
	tx, err := r.dbManager.BeginTx()
	if err != nil {
		return 0, fmt.Errorf("begin tx for manual node delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var nodeID string
	if err := tx.QueryRow(`UPDATE kg_manual_nodes SET is_active = false
		WHERE tenant_id = $1 AND id = $2 AND is_active = true
		RETURNING kg_node_id::text`, tenantID, id).Scan(&nodeID); err != nil {
		return 0, err
	}
	edges, err := tombstoneManualNodes(tx, tenantID, []string{nodeID})
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit manual node delete: %w", err)
	}
	r.invalidateFilterOptions(tenantID)
	return edges, nil
}

// SoftDeleteAll is the panic button: every declaration withdrawn, every node
// still owned by a declaration tombstoned with its edges.
func (r *ManualNodeRepository) SoftDeleteAll(ctx context.Context, tenantID string) (rows int64, nodes int64, err error) {
	if tenantID == "" {
		return 0, 0, ErrTenantIDRequired
	}
	tx, err := r.dbManager.BeginTx()
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx for manual node delete-all: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`UPDATE kg_manual_nodes SET is_active = false
		WHERE tenant_id = $1 AND is_active = true`, tenantID)
	if err != nil {
		return 0, 0, fmt.Errorf("withdraw declarations: %w", err)
	}
	rows, _ = res.RowsAffected()

	var ids []string
	if err := tx.Select(&ids, `SELECT id::text FROM knowledge_graph_node
		WHERE tenant_id = $1 AND is_active = true AND source = $2`, tenantID, core.ManualNodeSource); err != nil {
		return 0, 0, fmt.Errorf("list declared nodes: %w", err)
	}
	if _, err := tombstoneManualNodes(tx, tenantID, ids); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit manual node delete-all: %w", err)
	}
	r.invalidateFilterOptions(tenantID)
	return rows, int64(len(ids)), nil
}

// Merge resolves a possible duplicate in favour of the collected node: every
// dependency pinned to the declared node moves to it, and the declaration is
// withdrawn.
func (r *ManualNodeRepository) Merge(ctx context.Context, tenantID string, id int64) (*ManualNode, int64, error) {
	n, err := r.Get(ctx, tenantID, id)
	if err != nil {
		return nil, 0, err
	}
	if n.Status == ManualNodeStatusAdopted {
		return nil, 0, ErrManualNodeAdopted
	}
	if n.Status != ManualNodeStatusPossibleDuplicate || n.DuplicateNodeID == "" {
		return nil, 0, errors.New("no matching collected node to merge into")
	}

	tx, err := r.dbManager.BeginTx()
	if err != nil {
		return nil, 0, fmt.Errorf("begin tx for manual node merge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var repointed int64
	for _, col := range []string{"resolved_source_node_id", "resolved_dest_node_id"} {
		res, err := tx.Exec(`UPDATE kg_manual_dependencies SET `+col+` = $1
			WHERE tenant_id = $2 AND is_active = true AND `+col+` = $3`,
			n.DuplicateNodeID, tenantID, n.KGNodeID)
		if err != nil {
			return nil, 0, fmt.Errorf("repoint dependencies: %w", err)
		}
		affected, _ := res.RowsAffected()
		repointed += affected
	}
	if _, err := tx.Exec(`UPDATE kg_manual_nodes SET is_active = false
		WHERE tenant_id = $1 AND id = $2`, tenantID, id); err != nil {
		return nil, 0, fmt.Errorf("withdraw declaration: %w", err)
	}
	if _, err := tombstoneManualNodes(tx, tenantID, []string{n.KGNodeID}); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("commit manual node merge: %w", err)
	}
	r.invalidateFilterOptions(tenantID)
	return n, repointed, nil
}

// sqlExecer is what tombstoneManualNodes needs from a transaction.
type sqlExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// tombstoneManualNodes retires declared nodes and every edge touching them.
// The source = 'manual' guard is what keeps a delete from removing a node a
// collector has since taken over. Returns the number of edges tombstoned.
func tombstoneManualNodes(tx sqlExecer, tenantID string, nodeIDs []string) (int64, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	res, err := tx.Exec(`WITH retired AS (
			UPDATE knowledge_graph_node SET is_active = false, updated_at = NOW()
			WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND is_active = true AND source = $3
			RETURNING id
		)
		UPDATE knowledge_graph_edge SET is_active = false
		WHERE tenant_id = $1 AND is_active = true
		  AND (source_node_id IN (SELECT id FROM retired) OR destination_node_id IN (SELECT id FROM retired))`,
		tenantID, pq.Array(nodeIDs), core.ManualNodeSource)
	if err != nil {
		return 0, fmt.Errorf("tombstone declared nodes: %w", err)
	}
	edges, _ := res.RowsAffected()
	return edges, nil
}

// ---------------------------------------------------------------------------
// Build cycle (called by ManualFlowSource)
// ---------------------------------------------------------------------------

// ManualNodeCycleResult is what one build does with the tenant's declarations.
type ManualNodeCycleResult struct {
	Nodes      []*core.DbNode
	Adopted    int
	Revived    int
	Reconciled int64
	Duplicates int
}

// RunCycle re-emits every live declaration for one build:
//
//  1. Reconcile: retire declared nodes no live declaration owns. A delete that
//     lands while a build is running is undone by that build's save (it writes
//     every node active), and no sweep retires source = 'manual' nodes, so
//     without this the node would stay forever. It keys on the declaration
//     being live, not on its status, so it never touches a node being adopted.
//  2. Adopt: a declaration whose node a collector now writes (in this build,
//     or already in the graph) is marked adopted and no longer emitted. In-build
//     dedup lets the later node's properties win, so emitting it would
//     overwrite the collector's data, including the source the sweep reads.
//     An adopted declaration whose node the collector no longer writes (the
//     sweep retired it) is revived: it goes back to active and is emitted.
//  3. Emit the rest, built from what the declaration stored.
//  4. Flag possible duplicates: a collected node of the same type and name
//     that did not merge (the key missed, or the declaration is tenant-scoped).
func (r *ManualNodeRepository) RunCycle(ctx context.Context, tenantID string, existing map[string]*core.DbNode) (*ManualNodeCycleResult, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	result := &ManualNodeCycleResult{}

	if result.Reconciled = r.reconcile(tenantID); result.Reconciled > 0 {
		r.logger.Info("manual nodes: retired nodes no declaration owns", "tenant_id", tenantID, "edges_tombstoned", result.Reconciled)
	}

	// Adopted declarations are checked too: when the collector stops writing
	// the node (the resource went away, or its key changed), the declaration
	// takes it back, so a dependency on it does not silently fall to unmatched.
	decls, err := r.query(`SELECT `+manualNodeColumns+` FROM kg_manual_nodes
		WHERE tenant_id = $1 AND is_active = true ORDER BY id`, tenantID)
	if err != nil || len(decls) == 0 {
		return result, err
	}

	collected, err := r.collectedNodeIDs(tenantID, decls)
	if err != nil {
		return nil, err
	}
	var adopted, revived []int64
	live := make([]ManualNode, 0, len(decls))
	for _, d := range decls {
		if n := existing[d.KGNodeID]; (n != nil && n.Source != core.ManualNodeSource) || collected[d.KGNodeID] {
			if d.Status != ManualNodeStatusAdopted {
				adopted = append(adopted, d.ID)
			}
			continue
		}
		if d.Status == ManualNodeStatusAdopted {
			revived = append(revived, d.ID)
		}
		live = append(live, d)
	}
	if len(adopted) > 0 {
		if _, err := r.dbManager.Exec(`UPDATE kg_manual_nodes SET status = $1, duplicate_node_id = NULL
			WHERE tenant_id = $2 AND id = ANY($3::bigint[])`, ManualNodeStatusAdopted, tenantID, pq.Array(adopted)); err != nil {
			return nil, fmt.Errorf("mark adopted declarations: %w", err)
		}
		result.Adopted = len(adopted)
	}
	if len(revived) > 0 {
		// flagDuplicates below decides between active and possible_duplicate.
		if _, err := r.dbManager.Exec(`UPDATE kg_manual_nodes SET status = $1, duplicate_node_id = NULL
			WHERE tenant_id = $2 AND id = ANY($3::bigint[])`, ManualNodeStatusActive, tenantID, pq.Array(revived)); err != nil {
			return nil, fmt.Errorf("revive declarations: %w", err)
		}
		result.Revived = len(revived)
		r.logger.Info("manual nodes: collector no longer writes adopted nodes, declarations take them back",
			"tenant_id", tenantID, "revived", len(revived))
	}

	for i := range live {
		node, err := live[i].Materialize()
		if err != nil {
			r.logger.Warn("manual nodes: skipping a declaration that no longer builds",
				"tenant_id", tenantID, "id", live[i].ID, "error", err)
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}

	dups, err := r.flagDuplicates(tenantID)
	if err != nil {
		// The emitted nodes are still right; only the hint is stale.
		r.logger.Warn("manual nodes: duplicate check failed", "tenant_id", tenantID, "error", err)
	}
	result.Duplicates = dups
	return result, nil
}

// collectedNodeIDs are the declarations' node ids a collector already writes.
func (r *ManualNodeRepository) collectedNodeIDs(tenantID string, decls []ManualNode) (map[string]bool, error) {
	ids := make([]string, 0, len(decls))
	for _, d := range decls {
		ids = append(ids, d.KGNodeID)
	}
	var found []string
	if err := r.dbManager.Db.Select(&found, `SELECT id::text FROM knowledge_graph_node
		WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND is_active = true AND COALESCE(source, '') <> $3`,
		tenantID, pq.Array(ids), core.ManualNodeSource); err != nil {
		return nil, fmt.Errorf("check collected nodes: %w", err)
	}
	out := make(map[string]bool, len(found))
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// reconcile retires declared nodes with no live declaration. Best-effort: a
// failure leaves them for the next build.
func (r *ManualNodeRepository) reconcile(tenantID string) int64 {
	var ids []string
	if err := r.dbManager.Db.Select(&ids, `SELECT n.id::text FROM knowledge_graph_node n
		WHERE n.tenant_id = $1 AND n.is_active = true AND n.source = $2
		  AND NOT EXISTS (
			SELECT 1 FROM kg_manual_nodes m
			WHERE m.tenant_id = $1 AND m.is_active = true AND m.kg_node_id = n.id
		  )`, tenantID, core.ManualNodeSource); err != nil {
		r.logger.Warn("manual nodes: reconcile query failed", "tenant_id", tenantID, "error", err)
		return 0
	}
	if len(ids) == 0 {
		return 0
	}
	edges, err := tombstoneManualNodes(r.dbManager, tenantID, ids)
	if err != nil {
		r.logger.Warn("manual nodes: reconcile failed", "tenant_id", tenantID, "error", err)
		return 0
	}
	return edges
}

// flagDuplicates marks each live declaration possible_duplicate when a
// collected node of the same type and name exists in the same account (any
// account, for a tenant-scoped declaration), and clears the flag when it is
// gone. Returns the number flagged.
func (r *ManualNodeRepository) flagDuplicates(tenantID string) (int, error) {
	res, err := r.dbManager.Exec(`WITH candidates AS (
			SELECT m.id,
				(SELECT n.id FROM knowledge_graph_node n
				 WHERE n.tenant_id = m.tenant_id AND n.is_active = true AND n.level = 'Tenant'
				   AND COALESCE(n.source, '') <> $2
				   AND n.node_type = m.node_type
				   AND n.id <> m.kg_node_id
				   AND (m.cloud_account_id IS NULL OR n.cloud_account_id = m.cloud_account_id)
				   -- query_attributes, not properties: every declarable type hoists
				   -- name (and the Kubernetes ones namespace) into it, and only that
				   -- form is indexed (idx_kg_node_tenant_qa_name_ns). This runs
				   -- once per declaration every build.
				   AND n.query_attributes->>'name' = m.name
				   -- A Kubernetes name is only unique within its namespace.
				   AND (m.cloud_provider IS DISTINCT FROM 'K8s' OR n.query_attributes->>'namespace' = m.namespace)
				 ORDER BY n.updated_at DESC
				 LIMIT 1) AS dup
			FROM kg_manual_nodes m
			WHERE m.tenant_id = $1 AND m.is_active = true AND m.status <> $3
		)
		UPDATE kg_manual_nodes m
		SET status = CASE WHEN c.dup IS NULL THEN $4::text ELSE $5::text END,
			duplicate_node_id = c.dup
		FROM candidates c
		WHERE m.id = c.id
		  AND (m.duplicate_node_id IS DISTINCT FROM c.dup
		       OR m.status <> CASE WHEN c.dup IS NULL THEN $4::text ELSE $5::text END)`,
		tenantID, core.ManualNodeSource, ManualNodeStatusAdopted, ManualNodeStatusActive, ManualNodeStatusPossibleDuplicate)
	if err != nil {
		return 0, err
	}
	changed, _ := res.RowsAffected()
	return int(changed), nil
}

// ---------------------------------------------------------------------------
// Row mapping
// ---------------------------------------------------------------------------

const manualNodeColumns = `id, tenant_id::text,
	COALESCE(cloud_account_id::text, ''), COALESCE(cloud_provider, ''),
	node_type, specific_type, name,
	COALESCE(region, ''), COALESCE(namespace, ''), COALESCE(network_node_id::text, ''),
	COALESCE(description, ''), properties, labels,
	kg_node_id::text, unique_key, base_properties,
	status, COALESCE(duplicate_node_id::text, ''), COALESCE(declared_by_user_id::text, ''),
	created_at, updated_at`

func (r *ManualNodeRepository) query(query string, args ...interface{}) ([]ManualNode, error) {
	rows, err := r.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query manual nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ManualNode{}
	for rows.Next() {
		var n ManualNode
		var props, labels, base []byte
		if err := rows.Scan(&n.ID, &n.TenantID, &n.CloudAccountID, &n.CloudProvider,
			&n.NodeType, &n.SpecificType, &n.Name,
			&n.Region, &n.Namespace, &n.NetworkNodeID,
			&n.Description, &props, &labels,
			&n.KGNodeID, &n.UniqueKey, &base,
			&n.Status, &n.DuplicateNodeID, &n.DeclaredByUserID,
			&n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan manual node: %w", err)
		}
		if err := unmarshalJSONB(props, &n.Properties); err != nil {
			return nil, fmt.Errorf("manual node %d properties: %w", n.ID, err)
		}
		if err := unmarshalJSONB(labels, &n.Labels); err != nil {
			return nil, fmt.Errorf("manual node %d labels: %w", n.ID, err)
		}
		if err := unmarshalJSONB(base, &n.baseProperties); err != nil {
			return nil, fmt.Errorf("manual node %d base properties: %w", n.ID, err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate manual nodes: %w", err)
	}
	return out, nil
}

func unmarshalJSONB(raw []byte, into interface{}) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func marshalDetails(n *ManualNode) ([]byte, []byte, error) {
	props := n.Properties
	if props == nil {
		props = map[string]interface{}{}
	}
	labels := n.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	p, err := json.Marshal(props)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal properties: %w", err)
	}
	l, err := json.Marshal(labels)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal labels: %w", err)
	}
	return p, l, nil
}

// translateManualNodeDuplicate turns a hit on the one-declaration-per-node
// index into ErrManualNodeDuplicate.
func translateManualNodeDuplicate(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolationCode && pqErr.Constraint == manualNodeDedupeIndex {
		return ErrManualNodeDuplicate
	}
	return fmt.Errorf("insert manual node: %w", err)
}
