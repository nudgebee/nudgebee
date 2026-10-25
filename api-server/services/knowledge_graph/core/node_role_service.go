package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Setting and removing the role a user gives a node. See node_roles.go for the
// model; this file is the write the role RPCs make.

var (
	// ErrNodeRoleNotFound: the node is not an active node of the tenant.
	ErrNodeRoleNotFound = errors.New("node not found")
	// ErrNodeRoleForbidden: the caller may not change nodes of its account.
	ErrNodeRoleForbidden = errors.New("not allowed to change the role of this node")
	// ErrNodeRoleNotTarget: the node's type cannot carry a role (IsNodeRoleTarget).
	ErrNodeRoleNotTarget = errors.New("this type of node cannot carry a role")
	// ErrNodeRoleInvalid wraps an unknown role or a malformed engine.
	ErrNodeRoleInvalid = errors.New("invalid role")
)

// NodeRoleWrite is one request to set (Role non-empty) or clear a node's role.
type NodeRoleWrite struct {
	TenantID string
	NodeID   string
	Role     string
	Engine   string
	UserID   string
	// Authorize answers whether the caller may change a node of the given
	// account; "" asks about a node outside any account (cloud_account_id is
	// the tenant), which needs tenant-wide access.
	Authorize func(cloudAccountID string) bool
}

// NodeRoleSnapshot is the role part of a node, before or after a change.
type NodeRoleSnapshot struct {
	Role           string `json:"role,omitempty"`
	Engine         string `json:"engine,omitempty"`
	RoleSource     string `json:"role_source,omitempty"`
	DetectedRole   string `json:"detected_role,omitempty"`
	DetectedEngine string `json:"detected_engine,omitempty"`
}

func roleSnapshot(props map[string]interface{}) NodeRoleSnapshot {
	return NodeRoleSnapshot{
		Role:           getNodeProp(props, PropRole),
		Engine:         getNodeProp(props, PropEngine),
		RoleSource:     getNodeProp(props, PropRoleSource),
		DetectedRole:   getNodeProp(props, PropDetectedRole),
		DetectedEngine: getNodeProp(props, PropDetectedEngine),
	}
}

// NodeRoleChange records one node whose role changed, for the audit log.
type NodeRoleChange struct {
	NodeID         string           `json:"node_id"`
	Name           string           `json:"name"`
	NodeType       NodeType         `json:"node_type"`
	CloudAccountID string           `json:"cloud_account_id,omitempty"` // "" for a node outside any account
	Before         NodeRoleSnapshot `json:"before"`
	After          NodeRoleSnapshot `json:"after"`
}

// NodeRoleResult is what a role write did.
type NodeRoleResult struct {
	// Node is the node asked about, as kg_get_node returns it.
	Node *KgNode `json:"node"`
	// Applied lists the linked instances (SAME_AS twins) that got the same
	// change; Skipped those the caller may not change.
	Applied []string `json:"applied"`
	Skipped []string `json:"skipped"`
	// BuildInProgress: a build of the node's account was running, so its save
	// may overwrite the live node until the next build re-applies the role.
	BuildInProgress bool             `json:"build_in_progress"`
	Changes         []NodeRoleChange `json:"-"`
}

type lockedRoleNode struct {
	ID             string `db:"id"`
	UniqueKey      string `db:"unique_key"`
	NodeType       string `db:"node_type"`
	CloudAccountID string `db:"cloud_account_id"`
	Properties     []byte `db:"properties"`
	QueryAttrs     []byte `db:"query_attributes"`
}

// SetNodeRole gives a node a role, and the same role to the cloud instances
// it is one machine with (SAME_AS twins: a self-hosted VM host and its EC2
// instance), so either card and every walk agree.
func (s *Service) SetNodeRole(req NodeRoleWrite) (*NodeRoleResult, error) {
	engine, err := ValidateNodeRole(req.Role, req.Engine)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeRoleInvalid, err)
	}
	req.Engine = engine
	return s.writeNodeRole(req, true)
}

// ClearNodeRole removes the role a user gave a node (and its twins); the
// detected role, if any, shows again.
func (s *Service) ClearNodeRole(req NodeRoleWrite) (*NodeRoleResult, error) {
	return s.writeNodeRole(req, false)
}

func (s *Service) writeNodeRole(req NodeRoleWrite, set bool) (*NodeRoleResult, error) {
	if s.dbManager == nil {
		return nil, errors.New("knowledge graph: db manager is not configured")
	}
	if req.TenantID == "" || req.NodeID == "" {
		return nil, fmt.Errorf("%w: tenant and node id are required", ErrNodeRoleInvalid)
	}
	authorize := req.Authorize
	if authorize == nil {
		authorize = func(string) bool { return false }
	}

	// Twins are found outside the transaction: an edge walk, read-only.
	targets := s.withSameAsTwins(req.TenantID, []string{req.NodeID})

	tx, err := s.dbManager.BeginTx()
	if err != nil {
		return nil, fmt.Errorf("begin node role transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// One query locks every target in id order, so two requests from either
	// end of a twin pair cannot deadlock.
	var locked []lockedRoleNode
	if err := sqlx.Select(tx, &locked, `SELECT `+lockRoleNodeColumns+`
		FROM knowledge_graph_node
		WHERE id = ANY($1::uuid[]) AND tenant_id = $2 AND is_active = true AND level = 'Tenant'
		ORDER BY id FOR UPDATE`, pq.Array(targets), req.TenantID); err != nil {
		return nil, fmt.Errorf("lock nodes: %w", err)
	}
	byID := make(map[string]lockedRoleNode, len(locked))
	for _, n := range locked {
		byID[n.ID] = n
	}
	seed, ok := byID[req.NodeID]
	if !ok {
		return nil, ErrNodeRoleNotFound
	}
	if !IsNodeRoleTarget(NodeType(seed.NodeType)) {
		return nil, ErrNodeRoleNotTarget
	}
	accountSlot := func(n lockedRoleNode) string {
		if n.CloudAccountID == req.TenantID {
			return ""
		}
		return n.CloudAccountID
	}
	if !authorize(accountSlot(seed)) {
		return nil, ErrNodeRoleForbidden
	}

	result := &NodeRoleResult{Applied: []string{}, Skipped: []string{}}
	change := []lockedRoleNode{seed}
	for _, id := range targets {
		if id == req.NodeID {
			continue
		}
		twin, ok := byID[id]
		if !ok || !IsNodeRoleTarget(NodeType(twin.NodeType)) {
			continue
		}
		if !authorize(accountSlot(twin)) {
			result.Skipped = append(result.Skipped, id)
			continue
		}
		change = append(change, twin)
		result.Applied = append(result.Applied, id)
	}
	sort.Strings(result.Applied)
	sort.Strings(result.Skipped)

	for _, n := range change {
		c, err := applyRoleToLockedNode(tx, req, n, set)
		if err != nil {
			return nil, err
		}
		c.CloudAccountID = accountSlot(n)
		result.Changes = append(result.Changes, c)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit node role: %w", err)
	}

	s.InvalidateFilterOptionsCache(req.TenantID)

	running, err := NewBuildRequestRepository(s.dbManager, s.logger).BuildRunningFor(req.TenantID, seed.CloudAccountID)
	if err != nil {
		s.logger.Warn("node role: could not check for a running build", "tenant_id", req.TenantID, "error", err)
	}
	result.BuildInProgress = running

	node, err := s.GetNodeByID(req.NodeID)
	if err != nil {
		return nil, fmt.Errorf("reload node: %w", err)
	}
	result.Node = node
	return result, nil
}

// applyRoleToLockedNode writes the row and patches the live node in place.
func applyRoleToLockedNode(tx *sqlx.Tx, req NodeRoleWrite, n lockedRoleNode, set bool) (NodeRoleChange, error) {
	props, qa, err := n.decode()
	if err != nil {
		return NodeRoleChange{}, err
	}
	c := NodeRoleChange{NodeID: n.ID, Name: getNodeProp(props, "name"), NodeType: NodeType(n.NodeType), Before: roleSnapshot(props)}

	if set {
		if err := UpsertNodeRole(tx, NodeRole{
			TenantID: req.TenantID, KGNodeID: n.ID, UniqueKey: n.UniqueKey,
			Role: req.Role, Engine: req.Engine, SetByUserID: req.UserID,
		}); err != nil {
			return NodeRoleChange{}, err
		}
		props, qa = ApplyUserRole(props, qa, req.Role, req.Engine)
	} else {
		if _, err := DeleteNodeRole(tx, req.TenantID, n.ID); err != nil {
			return NodeRoleChange{}, err
		}
		props, qa = ClearUserRole(props, qa)
	}
	c.After = roleSnapshot(props)
	if err := writeLiveNodeRole(tx, req.TenantID, n.ID, props, qa); err != nil {
		return NodeRoleChange{}, err
	}
	return c, nil
}

const lockRoleNodeColumns = `id::text AS id, unique_key, node_type,
	cloud_account_id::text AS cloud_account_id, properties, query_attributes`

func (n lockedRoleNode) decode() (map[string]interface{}, map[string]interface{}, error) {
	props := map[string]interface{}{}
	qa := map[string]interface{}{}
	if len(n.Properties) > 0 {
		if err := json.Unmarshal(n.Properties, &props); err != nil {
			return nil, nil, fmt.Errorf("decode node properties: %w", err)
		}
	}
	if len(n.QueryAttrs) > 0 {
		if err := json.Unmarshal(n.QueryAttrs, &qa); err != nil {
			return nil, nil, fmt.Errorf("decode node query_attributes: %w", err)
		}
	}
	return props, qa, nil
}

// writeLiveNodeRole stores a node's role-bearing maps, leaving updated_at
// alone: it is the node's last-seen time, which a role change does not renew.
func writeLiveNodeRole(tx *sqlx.Tx, tenantID, nodeID string, props, qa map[string]interface{}) error {
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("encode node properties: %w", err)
	}
	qaJSON, err := json.Marshal(qa)
	if err != nil {
		return fmt.Errorf("encode node query_attributes: %w", err)
	}
	if _, err := tx.Exec(`UPDATE knowledge_graph_node SET properties = $1::jsonb, query_attributes = $2::jsonb
		WHERE id = $3 AND tenant_id = $4`, string(propsJSON), string(qaJSON), nodeID, tenantID); err != nil {
		return fmt.Errorf("update node: %w", err)
	}
	return nil
}

// RefreshLiveNodeRole re-applies a node's stored role row to the live node,
// for a write that changed the row without writing the node: a manual-node
// Merge handing the declaration's role to the collected node, which would
// otherwise show it only from its next build. A node that is not active is
// left alone; its next write applies the row.
func RefreshLiveNodeRole(tx *sqlx.Tx, tenantID, nodeID string) error {
	var n lockedRoleNode
	err := sqlx.Get(tx, &n, `SELECT `+lockRoleNodeColumns+` FROM knowledge_graph_node
		WHERE id = $1 AND tenant_id = $2 AND is_active = true AND level = 'Tenant' FOR UPDATE`, nodeID, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock node: %w", err)
	}
	rows, err := ListNodeRolesByIDs(tx, tenantID, []string{nodeID})
	if err != nil {
		return err
	}
	props, qa, err := n.decode()
	if err != nil {
		return err
	}
	if r, ok := rows[nodeID]; ok {
		props, qa = ApplyUserRole(props, qa, r.Role, r.Engine)
	} else {
		props, qa = ClearUserRole(props, qa)
	}
	return writeLiveNodeRole(tx, tenantID, nodeID, props, qa)
}

// InvalidateFilterOptionsCache drops the tenant's cached filter options
// (refreshed only by a build), so a change shows in the graph's dropdowns at
// once. Best-effort: the next read recomputes them.
func (s *Service) InvalidateFilterOptionsCache(tenantID string) {
	if _, err := s.dbManager.Exec(`DELETE FROM knowledge_graph_filter_options WHERE tenant_id = $1`, tenantID); err != nil {
		s.logger.Warn("failed to invalidate filter options cache", "tenant_id", tenantID, "error", err)
	}
}
