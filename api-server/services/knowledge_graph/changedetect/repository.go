package changedetect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"nudgebee/services/knowledge_graph/sources"
)

// repository holds the detector's SQL. Every method runs on the detector's
// transaction, which also holds the cross-replica advisory lock.
type repository struct {
	ctx context.Context // bounds every query by the run's timeout
	tx  *sqlx.Tx
}

// loadMarks returns the saved marks for the given accounts, keyed by account id.
func (r *repository) loadMarks(accountIDs []string) (map[string]*Marks, error) {
	rows, err := r.tx.QueryContext(r.ctx, `
		SELECT cloud_account_id::text, tenant_id::text,
		       cloud_active_count, cloud_created_mark,
		       k8s_active_count, k8s_created_mark, k8s_deployed_mark,
		       node_count, node_created_mark, updated_at
		FROM knowledge_graph_inventory_marks
		WHERE cloud_account_id = ANY($1::uuid[])
	`, pq.Array(accountIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to load inventory marks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]*Marks, len(accountIDs))
	for rows.Next() {
		m := &Marks{}
		var (
			cloudCount, k8sCount, nodeCount                    sql.NullInt64
			cloudCreated, k8sCreated, k8sDeployed, nodeCreated sql.NullTime
		)
		if err := rows.Scan(&m.AccountID, &m.TenantID,
			&cloudCount, &cloudCreated,
			&k8sCount, &k8sCreated, &k8sDeployed,
			&nodeCount, &nodeCreated, &m.ProbedAt); err != nil {
			return nil, fmt.Errorf("failed to scan inventory marks: %w", err)
		}
		m.CloudActiveCount, m.CloudCreatedMark = nullInt(cloudCount), nullTime(cloudCreated)
		m.K8sActiveCount, m.K8sCreatedMark, m.K8sDeployedMark = nullInt(k8sCount), nullTime(k8sCreated), nullTime(k8sDeployed)
		m.NodeCount, m.NodeCreatedMark = nullInt(nodeCount), nullTime(nodeCreated)
		out[m.AccountID] = m
	}
	return out, rows.Err()
}

// tenantsProbedAt returns, per tenant, when its least recently probed account
// was probed. Tenants with no marks at all are absent: they have never been
// probed.
func (r *repository) tenantsProbedAt(tenantIDs []string) (map[string]time.Time, error) {
	rows, err := r.tx.QueryContext(r.ctx, `
		SELECT tenant_id::text, MIN(updated_at)
		FROM knowledge_graph_inventory_marks
		WHERE tenant_id = ANY($1::uuid[])
		GROUP BY tenant_id
	`, pq.Array(tenantIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to load tenant probe times: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]time.Time, len(tenantIDs))
	for rows.Next() {
		var tenantID string
		var probedAt time.Time
		if err := rows.Scan(&tenantID, &probedAt); err != nil {
			return nil, fmt.Errorf("failed to scan tenant probe time: %w", err)
		}
		out[tenantID] = probedAt
	}
	return out, rows.Err()
}

// saveMarks upserts marks. Timestamps are written back exactly as they were read
// from the inventory columns, so a mark keeps comparing equal to its source row.
func (r *repository) saveMarks(marks []*Marks) error {
	for _, m := range marks {
		if _, err := r.tx.ExecContext(r.ctx, `
			INSERT INTO knowledge_graph_inventory_marks (
				cloud_account_id, tenant_id,
				cloud_active_count, cloud_created_mark,
				k8s_active_count, k8s_created_mark, k8s_deployed_mark,
				node_count, node_created_mark, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
			ON CONFLICT (cloud_account_id) DO UPDATE SET
				tenant_id = excluded.tenant_id,
				cloud_active_count = excluded.cloud_active_count,
				cloud_created_mark = excluded.cloud_created_mark,
				k8s_active_count = excluded.k8s_active_count,
				k8s_created_mark = excluded.k8s_created_mark,
				k8s_deployed_mark = excluded.k8s_deployed_mark,
				node_count = excluded.node_count,
				node_created_mark = excluded.node_created_mark,
				updated_at = now()
		`, m.AccountID, m.TenantID,
			m.CloudActiveCount, noTZ(m.CloudCreatedMark),
			m.K8sActiveCount, noTZ(m.K8sCreatedMark), m.K8sDeployedMark,
			m.NodeCount, noTZ(m.NodeCreatedMark)); err != nil {
			return fmt.Errorf("failed to save inventory marks for account %s: %w", m.AccountID, err)
		}
	}
	return nil
}

// probeCloud reads the active row count and newest created_at for a cloud
// account. Served by idx_cloud_resourses_acct_created_active.
func (r *repository) probeCloud(accountID string) (CloudProbe, error) {
	var (
		p       CloudProbe
		created sql.NullTime
	)
	if err := r.tx.QueryRowContext(r.ctx, `
		SELECT count(*), max(created_at)
		FROM cloud_resourses
		WHERE account = $1 AND is_active = true
	`, accountID).Scan(&p.ActiveCount, &created); err != nil {
		return p, fmt.Errorf("failed to probe cloud inventory: %w", err)
	}
	p.CreatedMax = nullTime(created)
	return p, nil
}

// cloudRowsCreatedAfter returns up to limit active rows created after the mark,
// with the projection the cloud sources build from.
func (r *repository) cloudRowsCreatedAfter(accountID string, mark time.Time, limit int) ([]sources.CloudResourceRow, error) {
	var rows []sources.CloudResourceRow
	if err := r.tx.SelectContext(r.ctx, &rows, `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, COALESCE(cr.arn, '') AS arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.account = $1 AND cr.is_active = true AND cr.created_at > $2
		ORDER BY cr.created_at
		LIMIT $3
	`, accountID, noTZValue(mark), limit); err != nil {
		return nil, fmt.Errorf("failed to read new cloud rows: %w", err)
	}
	return rows, nil
}

// probeK8s reads the workload and node counts and watermarks for a Kubernetes
// account. Rollouts younger than settle are left for the next run, so the
// collector has time to write the rolled-out spec before the workload is
// re-read.
func (r *repository) probeK8s(tenantID, accountID string, kinds []string, settle time.Duration) (K8sProbe, error) {
	var (
		p                            K8sProbe
		created, deployed, nodeFirst sql.NullTime
	)
	if err := r.tx.QueryRowContext(r.ctx, `
		SELECT count(*), max(creation_time),
		       max(last_deployed_time) FILTER (WHERE last_deployed_time <= now() - $4::interval)
		FROM k8s_workloads
		WHERE tenant_id = $1 AND cloud_account_id = $2 AND is_active = true AND kind = ANY($3)
	`, tenantID, accountID, pq.Array(kinds), fmt.Sprintf("%d seconds", int(settle.Seconds()))).Scan(&p.WorkloadCount, &created, &deployed); err != nil {
		return p, fmt.Errorf("failed to probe k8s workloads: %w", err)
	}
	if err := r.tx.QueryRowContext(r.ctx, `
		SELECT count(*), max(node_creation_time)
		FROM k8s_nodes
		WHERE tenant_id = $1 AND cloud_account_id = $2 AND is_active = true
	`, tenantID, accountID).Scan(&p.NodeCount, &nodeFirst); err != nil {
		return p, fmt.Errorf("failed to probe k8s nodes: %w", err)
	}
	p.CreatedMax, p.DeployedMax, p.NodeCreated = nullTime(created), nullTime(deployed), nullTime(nodeFirst)
	return p, nil
}

// deployedWorkloads returns up to limit workload ids rolled out after the mark
// and no later than upTo.
func (r *repository) deployedWorkloads(tenantID, accountID string, kinds []string, after, upTo time.Time, limit int) ([]string, error) {
	var ids []string
	if err := r.tx.SelectContext(r.ctx, &ids, `
		SELECT cloud_resource_id::text
		FROM k8s_workloads
		WHERE tenant_id = $1 AND cloud_account_id = $2 AND is_active = true AND kind = ANY($3)
		  AND last_deployed_time > $4 AND last_deployed_time <= $5
		ORDER BY last_deployed_time
		LIMIT $6
	`, tenantID, accountID, pq.Array(kinds), after, upTo, limit); err != nil {
		return nil, fmt.Errorf("failed to read rolled-out workloads: %w", err)
	}
	return ids, nil
}

// marksTableExists guards the on-prem window in which new pods run before the
// migration that creates the table.
func marksTableExists(ctx context.Context, tx *sqlx.Tx) (bool, error) {
	var name sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.knowledge_graph_inventory_marks')::text`).Scan(&name); err != nil {
		return false, err
	}
	return name.Valid, nil
}

// tryLock takes a tenant's cross-replica detector lock for the transaction.
// False means another replica is probing that tenant.
func tryLock(ctx context.Context, tx *sqlx.Tx, tenantID string) (bool, error) {
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('kg_inventory_detector:' || $1, 0))`, tenantID).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

func nullInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func nullTime(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

// noTZ renders a mark for a timestamp WITHOUT time zone column as its wall-clock
// text. Passing a time.Time would let the driver attach the zone lib/pq read it
// with, and a server whose TimeZone is not UTC would shift it on the way in.
func noTZ(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := noTZValue(*t)
	return &s
}

func noTZValue(t time.Time) string {
	return t.Format("2006-01-02 15:04:05.999999")
}

var errNoLock = errors.New("another replica holds the inventory detector lock")
