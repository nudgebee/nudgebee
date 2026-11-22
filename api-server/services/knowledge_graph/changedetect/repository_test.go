package changedetect

// DB-gated tests for the detector's SQL. Skips cleanly when no Postgres is
// reachable.

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/internal/testenv"
)

const (
	detectTenant  = "b2ca6e00-0000-4000-8000-000000000007"
	detectAccount = "b2ca6e00-0000-4000-8000-00000000eeee"
)

func newTestRepo(t *testing.T) *repository {
	t.Helper()
	dbm := testenv.RequireMetastore(t)
	cleanup := func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_inventory_marks WHERE cloud_account_id = $1`, detectAccount)
		_, _ = dbm.Exec(`DELETE FROM k8s_workloads WHERE tenant_id = $1`, detectTenant)
	}
	cleanup()
	t.Cleanup(cleanup)
	tx, err := dbm.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return &repository{ctx: context.Background(), tx: tx}
}

// A timestamp mark must come back exactly as written, and keep comparing equal
// to the inventory value it was read from, even when the session time zone is
// not UTC.
func TestMarksRoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	if _, err := repo.tx.Exec(`SET LOCAL TimeZone = 'Asia/Kolkata'`); err != nil {
		t.Fatal(err)
	}
	locked, err := tryLock(context.Background(), repo.tx, detectTenant)
	if err != nil || !locked {
		t.Fatalf("tryLock = %v, %v", locked, err)
	}
	exists, err := marksTableExists(context.Background(), repo.tx)
	if err != nil || !exists {
		t.Fatalf("marksTableExists = %v, %v", exists, err)
	}

	created := time.Date(2026, 9, 17, 10, 11, 12, 345678000, time.UTC)
	deployed := time.Date(2026, 9, 17, 11, 0, 0, 0, time.UTC)
	in := &Marks{
		AccountID: detectAccount, TenantID: detectTenant,
		CloudActiveCount: ip(42), CloudCreatedMark: &created,
		K8sActiveCount: ip(7), K8sCreatedMark: &created, K8sDeployedMark: &deployed,
		NodeCount: ip(0),
	}
	if err := repo.saveMarks([]*Marks{in}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.loadMarks([]string{detectAccount})
	if err != nil {
		t.Fatal(err)
	}
	out := got[detectAccount]
	if out == nil {
		t.Fatal("marks not found")
	}
	if *out.CloudActiveCount != 42 || *out.K8sActiveCount != 7 || *out.NodeCount != 0 || out.NodeCreatedMark != nil {
		t.Errorf("counts not round-tripped: %+v", out)
	}
	if !out.CloudCreatedMark.Equal(created) || !out.K8sCreatedMark.Equal(created) {
		t.Errorf("timestamp mark shifted: wrote %s, read %s", created, out.CloudCreatedMark)
	}
	if !out.K8sDeployedMark.Equal(deployed) {
		t.Errorf("timestamptz mark shifted: wrote %s, read %s", deployed, out.K8sDeployedMark)
	}

	// The mark compares equal to the inventory value it came from.
	var later bool
	if err := repo.tx.QueryRow(`SELECT $1::timestamp > $2::timestamp`, noTZValue(created), noTZValue(*out.CloudCreatedMark)).Scan(&later); err != nil {
		t.Fatal(err)
	}
	if later {
		t.Error("re-read mark compares earlier than the value it was saved from")
	}
}

func TestProbeK8sAndDeployedWorkloads(t *testing.T) {
	repo := newTestRepo(t)
	insert := func(id, kind, created string, deployedAgo string, active bool) {
		t.Helper()
		var deployed interface{}
		if deployedAgo != "" {
			var ts time.Time
			if err := repo.tx.QueryRow(`SELECT now() - $1::interval`, deployedAgo).Scan(&ts); err != nil {
				t.Fatal(err)
			}
			deployed = ts
		}
		if _, err := repo.tx.Exec(`
			INSERT INTO k8s_workloads (tenant_id, cloud_account_id, cloud_resource_id, external_id, namespace, name, kind,
			                           is_active, total_pods, ready_pods, last_seen, labels, meta, creation_time, last_deployed_time)
			VALUES ($1, $2, $3::uuid, $3::text, 'shop', $3::text, $4, $5, 1, 1, now(), '{}', '{}', $6::timestamp, $7)
		`, detectTenant, detectAccount, id, kind, active, created, deployed); err != nil {
			t.Fatalf("insert workload: %v", err)
		}
	}
	insert("b2ca6e00-0000-4000-8000-0000000000a1", "Deployment", "2026-09-17 09:00:00", "20 minutes", true)
	insert("b2ca6e00-0000-4000-8000-0000000000a2", "Deployment", "2026-09-17 09:30:00", "1 minute", true) // not settled yet
	insert("b2ca6e00-0000-4000-8000-0000000000a3", "Pod", "2026-09-17 10:00:00", "", true)                // not a detected kind
	insert("b2ca6e00-0000-4000-8000-0000000000a4", "Deployment", "2026-09-17 11:00:00", "", false)        // inactive

	kinds := []string{"Deployment"}
	p, err := repo.probeK8s(detectTenant, detectAccount, kinds, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if p.WorkloadCount != 2 {
		t.Errorf("workload count %d, want 2 (active Deployments only)", p.WorkloadCount)
	}
	if p.CreatedMax == nil || p.CreatedMax.Format("15:04") != "09:30" {
		t.Errorf("created max %v, want 09:30", p.CreatedMax)
	}
	if p.DeployedMax == nil {
		t.Fatal("deployed max is nil")
	}
	if p.NodeCount != 0 || p.NodeCreated != nil {
		t.Errorf("node probe %d/%v, want empty", p.NodeCount, p.NodeCreated)
	}

	ids, err := repo.deployedWorkloads(detectTenant, detectAccount, kinds, p.DeployedMax.Add(-time.Hour), *p.DeployedMax, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "b2ca6e00-0000-4000-8000-0000000000a1" {
		t.Errorf("rolled-out workloads %v, want only the settled one", ids)
	}
}

// The detector's visiting order comes from when each account was last probed:
// loadMarks exposes it per account, tenantsProbedAt as each tenant's stalest
// account, and a tenant with no marks is reported as never probed.
func TestProbedAtIsReadBack(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.saveMarks([]*Marks{{AccountID: detectAccount, TenantID: detectTenant, CloudActiveCount: ip(1)}}); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := repo.tx.QueryRow(`SELECT now()`).Scan(&now); err != nil {
		t.Fatal(err)
	}

	got, err := repo.loadMarks([]string{detectAccount})
	if err != nil {
		t.Fatal(err)
	}
	if m := got[detectAccount]; m == nil || !m.ProbedAt.Equal(now) {
		t.Errorf("ProbedAt = %v, want the save time %v", got[detectAccount], now)
	}

	const neverProbed = "b2ca6e00-0000-4000-8000-000000000008"
	byTenant, err := repo.tenantsProbedAt([]string{detectTenant, neverProbed})
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := byTenant[detectTenant]; !ok || !at.Equal(now) {
		t.Errorf("tenant probed at %v (present %v), want %v", at, ok, now)
	}
	if _, ok := byTenant[neverProbed]; ok {
		t.Error("a tenant with no marks is reported as probed")
	}
}
