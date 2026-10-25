package core

import (
	"fmt"
	"sync/atomic"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Reads and writes of kg_node_roles. Each takes a sqlx.Queryer or
// sqlx.Execer, so the same function runs on the database or inside the
// caller's transaction (SaveNodes reads the roles inside the transaction that
// upserts the nodes).

const nodeRoleColumns = `kg_node_id::text AS kg_node_id, tenant_id::text AS tenant_id, unique_key, role,
	COALESCE(engine, '') AS engine, COALESCE(set_by_user_id::text, '') AS set_by_user_id`

var nodeRolesTableSeen atomic.Bool

// nodeRolesTableReady reports whether kg_node_roles exists. New pods start
// before the post-upgrade migration job creates it, and a failed query inside
// a transaction aborts the whole transaction, so SaveNodes would fail every
// build in that window. Readers ask first and treat a missing table as "no
// roles". Once the table is seen it is never asked about again.
func nodeRolesTableReady(q sqlx.Queryer) (bool, error) {
	if nodeRolesTableSeen.Load() {
		return true, nil
	}
	var ok bool
	if err := sqlx.Get(q, &ok, `SELECT to_regclass('public.kg_node_roles') IS NOT NULL`); err != nil {
		return false, fmt.Errorf("check node roles table: %w", err)
	}
	if ok {
		nodeRolesTableSeen.Store(true)
	}
	return ok, nil
}

// ListNodeRolesByTenant returns the tenant's roles keyed by node id.
func ListNodeRolesByTenant(q sqlx.Queryer, tenantID string) (map[string]NodeRole, error) {
	if ready, err := nodeRolesTableReady(q); err != nil || !ready {
		return map[string]NodeRole{}, err
	}
	var rows []NodeRole
	if err := sqlx.Select(q, &rows, `SELECT `+nodeRoleColumns+` FROM kg_node_roles WHERE tenant_id = $1`, tenantID); err != nil {
		return nil, fmt.Errorf("list node roles: %w", err)
	}
	return nodeRolesByID(rows), nil
}

// ListNodeRolesByIDs returns the roles of the given nodes, keyed by node id.
func ListNodeRolesByIDs(q sqlx.Queryer, tenantID string, nodeIDs []string) (map[string]NodeRole, error) {
	if len(nodeIDs) == 0 {
		return map[string]NodeRole{}, nil
	}
	if ready, err := nodeRolesTableReady(q); err != nil || !ready {
		return map[string]NodeRole{}, err
	}
	var rows []NodeRole
	if err := sqlx.Select(q, &rows, `SELECT `+nodeRoleColumns+` FROM kg_node_roles
		WHERE tenant_id = $1 AND kg_node_id = ANY($2::uuid[])`, tenantID, pq.Array(nodeIDs)); err != nil {
		return nil, fmt.Errorf("list node roles: %w", err)
	}
	return nodeRolesByID(rows), nil
}

// UpsertNodeRole sets a node's role, replacing the one it had.
func UpsertNodeRole(e sqlx.Execer, r NodeRole) error {
	_, err := e.Exec(`INSERT INTO kg_node_roles (tenant_id, kg_node_id, unique_key, role, engine, set_by_user_id)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, '')::uuid)
		ON CONFLICT (tenant_id, kg_node_id) DO UPDATE SET
			unique_key = EXCLUDED.unique_key, role = EXCLUDED.role,
			engine = EXCLUDED.engine, set_by_user_id = EXCLUDED.set_by_user_id`,
		r.TenantID, r.KGNodeID, r.UniqueKey, r.Role, r.Engine, r.SetByUserID)
	if err != nil {
		return fmt.Errorf("upsert node role: %w", err)
	}
	return nil
}

// DeleteNodeRole removes a node's role and reports whether it had one.
func DeleteNodeRole(e sqlx.Execer, tenantID, nodeID string) (bool, error) {
	res, err := e.Exec(`DELETE FROM kg_node_roles WHERE tenant_id = $1 AND kg_node_id = $2`, tenantID, nodeID)
	if err != nil {
		return false, fmt.Errorf("delete node role: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MoveNodeRole hands a node's role to another node, as a manual-node Merge
// does when a declaration gives way to the collected node it duplicates. A
// role the target already has wins; the source's row goes either way. Before
// the table exists it does nothing, so a Merge in the deploy window still runs.
func MoveNodeRole(e sqlx.Ext, tenantID, fromID, toID string) error {
	if fromID == toID {
		return nil
	}
	if ready, err := nodeRolesTableReady(e); err != nil || !ready {
		return err
	}
	if _, err := e.Exec(`UPDATE kg_node_roles r SET kg_node_id = $3,
			unique_key = COALESCE((SELECT unique_key FROM knowledge_graph_node WHERE id = $3 AND tenant_id = $1), r.unique_key)
		WHERE r.tenant_id = $1 AND r.kg_node_id = $2
		  AND NOT EXISTS (SELECT 1 FROM kg_node_roles t WHERE t.tenant_id = $1 AND t.kg_node_id = $3)`,
		tenantID, fromID, toID); err != nil {
		return fmt.Errorf("move node role: %w", err)
	}
	if _, err := DeleteNodeRole(e, tenantID, fromID); err != nil {
		return err
	}
	return nil
}

func nodeRolesByID(rows []NodeRole) map[string]NodeRole {
	out := make(map[string]NodeRole, len(rows))
	for _, r := range rows {
		out[r.KGNodeID] = r
	}
	return out
}

// ApplyStoredUserRoles loads the user roles of the tenants nodes belong to
// and layers them onto nodes (ApplyUserRoles). Nodes need their ids set.
// Writers outside this package (the legacy trace graph writer) call this.
func ApplyStoredUserRoles(q sqlx.Queryer, nodes []*DbNode) error {
	byTenant := map[string][]*DbNode{}
	for _, n := range nodes {
		if n != nil {
			byTenant[n.TenantID] = append(byTenant[n.TenantID], n)
		}
	}
	for tenantID, group := range byTenant {
		if tenantID == "" {
			continue
		}
		rows, err := ListNodeRolesByTenant(q, tenantID)
		if err != nil {
			return err
		}
		ApplyUserRoles(group, rows)
	}
	return nil
}
