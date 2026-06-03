//go:build recommendation_integration

// Scenario-driven integration test for the K8s spot instance recommendation
// candidate queries. It seeds synthetic k8s_pods / k8s_workloads / k8s_nodes
// rows for a throwaway tenant+account inside a single transaction, runs the
// exact eligibility queries used by processSpotInstanceRecommendations, asserts
// which workloads / CronJobs are surfaced, then ROLLS BACK so nothing is
// persisted. These tables carry no foreign keys, so no scaffolding rows are
// needed, and the rollback keeps the run fully isolated from real data.
//
// Run against the local DB (see api-server/services/.env) with:
//
//	APP_DATABASE_URL="$(grep -E '^APP_DATABASE_URL=' .env | head -1 | cut -d= -f2-)" \
//	  go test -tags recommendation_integration ./recommendation/ \
//	  -run TestSpotRecommendationScenarios -v
package recommendation

import (
	"fmt"
	"testing"
	"time"

	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

const (
	testTenant  = "aaaaaaaa-0000-4000-8000-000000000001"
	testAccount = "aaaaaaaa-0000-4000-8000-000000000002"
)

func mustExec(t *testing.T, tx *sqlx.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(query, args...); err != nil {
		t.Fatalf("exec failed: %v\nquery: %s", err, query)
	}
}

func seedNode(t *testing.T, tx *sqlx.Tx, name, capacityType string) {
	t.Helper()
	capLabel := ""
	if capacityType != "" {
		capLabel = fmt.Sprintf(`"karpenter.sh/capacity-type":%q,`, capacityType)
	}
	meta := fmt.Sprintf(`{"node_info":{"labels":{%s"node.kubernetes.io/instance-type":"m5.large","topology.kubernetes.io/region":"us-east-1"}}}`, capLabel)
	mustExec(t, tx, `INSERT INTO k8s_nodes (tenant_id, cloud_account_id, name, is_active, memory_capacity, cpu_capacity, memory_allocatable, cpu_allocatable, cloud_resource_id, meta)
		VALUES ($1,$2,$3,true,16,8,15,7,gen_random_uuid(),$4)`, testTenant, testAccount, name, meta)
}

// seedWorkload inserts a k8s_workloads row and returns its cloud_resource_id.
func seedWorkload(t *testing.T, tx *sqlx.Tx, name, kind, namespace string, totalPods int, isActive bool, meta string) string {
	t.Helper()
	var crid string
	err := tx.QueryRow(`INSERT INTO k8s_workloads (tenant_id, cloud_account_id, cloud_resource_id, external_id, namespace, is_active, total_pods, ready_pods, name, kind, creation_time, last_seen, labels, meta)
		VALUES ($1,$2,gen_random_uuid(),$3,$4,$5,$6,$6,$7,$8,now(),now(),'{}',$9) RETURNING cloud_resource_id::text`,
		testTenant, testAccount, name+"-ext", namespace, isActive, totalPods, name, kind, meta).Scan(&crid)
	if err != nil {
		t.Fatalf("seedWorkload(%s) failed: %v", name, err)
	}
	return crid
}

func seedPod(t *testing.T, tx *sqlx.Tx, name, namespace, status string, isActive bool, lastSeen time.Time, meta string) {
	t.Helper()
	mustExec(t, tx, `INSERT INTO k8s_pods (tenant_id, cloud_account_id, cloud_resource_id, external_id, name, workload_type, workload_name, namespace, status, node_name, is_active, creation_time, last_seen, meta)
		VALUES ($1,$2,gen_random_uuid(),$3,$4,'','',$5,$6,'',$7,now(),$8,$9)`,
		testTenant, testAccount, name+"-ext", name, namespace, status, isActive, lastSeen, meta)
}

func seedDeploymentPod(t *testing.T, tx *sqlx.Tx, podName, controller, namespace, node, status string, isActive bool) {
	t.Helper()
	meta := fmt.Sprintf(`{"controllerKind":"Deployment","controller":%q,"namespace":%q,"node":%q}`, controller, namespace, node)
	seedPod(t, tx, podName, namespace, status, isActive, time.Now(), meta)
}

func seedJobPod(t *testing.T, tx *sqlx.Tx, podName, jobName, namespace, node, status string, lastSeen time.Time) {
	t.Helper()
	meta := fmt.Sprintf(`{"namespace":%q,"node":%q,"config":{"labels":{"job-name":%q}}}`, namespace, node, jobName)
	seedPod(t, tx, podName, namespace, status, true, lastSeen, meta)
}

func field(row map[string]any, key string) string {
	switch v := row[key].(type) {
	case []byte:
		return string(v)
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// byController indexes result rows by their controller_name and also returns
// how many rows carried each name (to assert de-duplication).
func byController(rows []map[string]any) (map[string]map[string]any, map[string]int) {
	idx := map[string]map[string]any{}
	count := map[string]int{}
	for _, r := range rows {
		name := field(r, "controller_name")
		idx[name] = r
		count[name]++
	}
	return idx, count
}

func TestSpotRecommendationScenarios(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("no metastore DB available (set APP_DATABASE_URL): %v", err)
	}
	tx, err := dbms.Db.Beginx()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // never persist test data

	// cloud_account_id on the k8s tables FKs to cloud_accounts, so seed a
	// synthetic account, mirroring an existing row's created_by/tenant FK refs
	// to stay portable across environments.
	var refTenant, refUser string
	if err := tx.QueryRow(`SELECT tenant::text, created_by::text FROM cloud_accounts WHERE tenant IS NOT NULL AND created_by IS NOT NULL LIMIT 1`).Scan(&refTenant, &refUser); err != nil {
		t.Skipf("no cloud_accounts row to mirror FK refs from: %v", err)
	}
	mustExec(t, tx, `INSERT INTO cloud_accounts (id, cloud_provider, account_name, account_type, created_by, updated_by, tenant, status)
		VALUES ($1,'K8s','spot-rec-scenario-test','kubernetes',$2,$2,$3,'active')`, testAccount, refUser, refTenant)

	// --- nodes ------------------------------------------------------------
	seedNode(t, tx, "node-od", "") // on-demand (no capacity-type label)
	seedNode(t, tx, "node-spot", "spot")

	// --- workload (Deployment / ReplicaSet / Rollout) scenarios -----------
	// W1: multi-pod deployment, running pods on on-demand (+ a terminated pod) -> RECOMMENDED
	wlMeta := func(ns string, total int) string { return fmt.Sprintf(`{"total_pods":%d,"namespace":%q}`, total, ns) }
	seedWorkload(t, tx, "web-od", "Deployment", "default", 3, true, wlMeta("default", 3))
	seedDeploymentPod(t, tx, "web-od-1", "web-od", "default", "node-od", "Running", true)
	seedDeploymentPod(t, tx, "web-od-2", "web-od", "default", "node-od", "Running", true)
	seedDeploymentPod(t, tx, "web-od-3", "web-od", "default", "node-od", "Completed", true) // terminated, must be ignored

	// W2: running pods on spot, only the terminated pod was on on-demand -> NOT recommended
	seedWorkload(t, tx, "web-spot", "Deployment", "default", 3, true, wlMeta("default", 3))
	seedDeploymentPod(t, tx, "web-spot-1", "web-spot", "default", "node-spot", "Running", true)
	seedDeploymentPod(t, tx, "web-spot-2", "web-spot", "default", "node-spot", "Running", true)
	seedDeploymentPod(t, tx, "web-spot-3", "web-spot", "default", "node-od", "Completed", true)

	// W3: single-pod workload -> NOT recommended (total_pods > 1 gate)
	seedWorkload(t, tx, "web-single", "Deployment", "default", 1, true, wlMeta("default", 1))
	seedDeploymentPod(t, tx, "web-single-1", "web-single", "default", "node-od", "Running", true)

	// W4: excluded namespace -> NOT recommended
	seedWorkload(t, tx, "sys-dns", "Deployment", "kube-system", 3, true, wlMeta("kube-system", 3))
	seedDeploymentPod(t, tx, "sys-dns-1", "sys-dns", "kube-system", "node-od", "Running", true)
	seedDeploymentPod(t, tx, "sys-dns-2", "sys-dns", "kube-system", "node-od", "Running", true)

	// W5: active workload but all pods Pending right now -> NOT recommended (status='Running' trade-off)
	seedWorkload(t, tx, "web-pending", "Deployment", "default", 3, true, wlMeta("default", 3))
	seedDeploymentPod(t, tx, "web-pending-1", "web-pending", "default", "node-od", "Pending", true)
	seedDeploymentPod(t, tx, "web-pending-2", "web-pending", "default", "node-od", "Pending", true)

	// W6: inactive workload -> NOT recommended
	seedWorkload(t, tx, "web-inactive", "Deployment", "default", 3, false, wlMeta("default", 3))
	seedDeploymentPod(t, tx, "web-inactive-1", "web-inactive", "default", "node-od", "Running", true)

	// --- CronJob / Job scenarios ------------------------------------------
	// The jobs query joins Job/CronJob workloads on meta->>'namespace', so the
	// namespace must live in the workload meta (not just the column).
	cronMeta := func(ns string) string { return fmt.Sprintf(`{"namespace":%q}`, ns) }
	jobMeta := func(ns, cronjob string) string {
		return fmt.Sprintf(`{"namespace":%q,"job_data":{"parents":[{"kind":"CronJob","name":%q}]}}`, ns, cronjob)
	}

	// C1: recurring CronJob on on-demand with 2 job runs -> ONE recommendation (deduped)
	backupCrid := seedWorkload(t, tx, "backup", "CronJob", "default", 1, true, cronMeta("default"))
	seedWorkload(t, tx, "backup-1", "Job", "default", 1, true, jobMeta("default", "backup"))
	seedWorkload(t, tx, "backup-2", "Job", "default", 1, true, jobMeta("default", "backup"))
	seedJobPod(t, tx, "backup-1-pod", "backup-1", "default", "node-od", "Completed", time.Now())
	seedJobPod(t, tx, "backup-2-pod", "backup-2", "default", "node-od", "Completed", time.Now().Add(-2*time.Hour))

	// C2: one-off Job with no CronJob parent -> NOT recommended
	seedWorkload(t, tx, "migrate", "Job", "default", 1, true, cronMeta("default"))
	seedJobPod(t, tx, "migrate-pod", "migrate", "default", "node-od", "Completed", time.Now())

	// C3: CronJob already on spot -> NOT recommended (capacity filter)
	seedWorkload(t, tx, "spot-cron", "CronJob", "default", 1, true, cronMeta("default"))
	seedWorkload(t, tx, "spot-cron-1", "Job", "default", 1, true, jobMeta("default", "spot-cron"))
	seedJobPod(t, tx, "spot-cron-1-pod", "spot-cron-1", "default", "node-spot", "Completed", time.Now())

	// C4: CronJob whose last run is older than 7 days -> NOT recommended
	seedWorkload(t, tx, "old-cron", "CronJob", "default", 1, true, cronMeta("default"))
	seedWorkload(t, tx, "old-cron-1", "Job", "default", 1, true, jobMeta("default", "old-cron"))
	seedJobPod(t, tx, "old-cron-1-pod", "old-cron-1", "default", "node-od", "Completed", time.Now().Add(-10*24*time.Hour))

	// --- run the exact eligibility queries --------------------------------
	workloads, err := getSpotEligibleWorkloads(tx, testAccount)
	if err != nil {
		t.Fatalf("getSpotEligibleWorkloads: %v", err)
	}
	cronjobs, err := getSpotEligibleCronJobs(tx, testAccount)
	if err != nil {
		t.Fatalf("getSpotEligibleCronJobs: %v", err)
	}

	wl, _ := byController(workloads)
	cj, cjCount := byController(cronjobs)

	// log what was generated so the actual recommendation payloads are visible
	t.Logf("workload recommendations (%d):", len(workloads))
	for _, r := range workloads {
		t.Logf("  controller=%s type=%s ns=%s node_type=%s node_flavor=%s",
			field(r, "controller_name"), field(r, "type"), field(r, "namespace"), field(r, "node_type"), field(r, "node_flavor"))
	}
	t.Logf("cronjob recommendations (%d):", len(cronjobs))
	for _, r := range cronjobs {
		t.Logf("  controller=%s type=%s ns=%s resource_id=%s", field(r, "controller_name"), field(r, "type"), field(r, "namespace"), field(r, "resource_id"))
	}

	// --- workload assertions ----------------------------------------------
	assert.Contains(t, wl, "web-od", "W1: deployment with running on-demand pods should be recommended")
	assert.NotContains(t, wl, "web-spot", "W2: deployment whose only on-demand pod is terminated should NOT be recommended")
	assert.NotContains(t, wl, "web-single", "W3: single-pod workload should NOT be recommended")
	assert.NotContains(t, wl, "sys-dns", "W4: kube-system workload should NOT be recommended")
	assert.NotContains(t, wl, "web-pending", "W5: workload with no Running pods should NOT be recommended")
	assert.NotContains(t, wl, "web-inactive", "W6: inactive workload should NOT be recommended")

	// --- cronjob assertions -----------------------------------------------
	assert.Contains(t, cj, "backup", "C1: recurring on-demand CronJob should be recommended")
	assert.Equal(t, 1, cjCount["backup"], "C1: CronJob with multiple job runs should produce exactly one recommendation")
	if r, ok := cj["backup"]; ok {
		assert.Equal(t, "CronJob", field(r, "type"), "C1: type should be CronJob")
		assert.Equal(t, backupCrid, field(r, "resource_id"), "C1: resource_id should be anchored to the CronJob workload")
	}
	assert.NotContains(t, cj, "migrate", "C2: one-off Job without a CronJob parent should NOT be recommended")
	assert.NotContains(t, cj, "spot-cron", "C3: CronJob already on spot should NOT be recommended")
	assert.NotContains(t, cj, "old-cron", "C4: CronJob with no run in the last 7 days should NOT be recommended")
}
