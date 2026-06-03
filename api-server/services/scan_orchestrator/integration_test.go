//go:build relay_integration

// Run with:
//
//   RELAY_ENDPOINT=http://localhost:8088 \
//   RELAY_SECRET=<relay-shared-secret> \
//   RELAY_ACCOUNT_ID=<canary-cloud-account-id> \
//   RELAY_SERVICE_ACCOUNT=nudgebee-agent-runner \
//   go test -tags=relay_integration -run TestRelay -v ./services/scan_orchestrator/...
//
// Pre-flight: a Go agent running the new primitives must be connected to the
// relay (post-PR-#34 image; verify in `agent` table that the account's
// status=CONNECTED and last_connected_at is recent). This test is a real-world
// E2E — it creates Jobs in the customer cluster — so only run it against a
// sandbox/canary tenant.

package scan_orchestrator

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// testCtx returns a super-admin RequestContext for relay integration tests.
// RunScan / RunOne / Persist all assume a non-nil ctx (project convention) so
// tests can't pass nil; build a minimal one with default logger and no tracer.
func testCtx() *security.RequestContext {
	return security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
}

// scanAccountForTest is the small builder TestRelay_PersistEndToEnd uses so
// the test reads cleanly. Kept inline; not exported.
func scanAccountForTest(accountID, tenantID, serviceAccount string) ScanAccount {
	return ScanAccount{
		AccountID:      accountID,
		TenantID:       tenantID,
		ServiceAccount: serviceAccount,
	}
}

func setupRelay(t *testing.T) (accountID, serviceAccount string) {
	t.Helper()
	endpoint := os.Getenv("RELAY_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:8088"
	}
	secret := os.Getenv("RELAY_SECRET")
	if secret == "" {
		t.Skip("RELAY_SECRET not set — skipping integration test")
	}
	accountID = os.Getenv("RELAY_ACCOUNT_ID")
	if accountID == "" {
		t.Skip("RELAY_ACCOUNT_ID not set — point at a canary cloud_account whose Go agent is connected")
	}
	serviceAccount = os.Getenv("RELAY_SERVICE_ACCOUNT")
	if serviceAccount == "" {
		serviceAccount = "nudgebee-agent-runner"
	}

	// Wire the relay's package-level config. relay.Execute reads from
	// config.Config.RelayServerEndpoint + RelayServerSecretKey.
	config.Config.RelayServerEndpoint = endpoint
	config.Config.RelayServerSecretKey = secret

	// Loop fast — we don't want a 15-min wait blocking CI/dev.
	OverallTimeout = 6 * time.Minute
	InitialPollInterval = 3 * time.Second
	MaxPollInterval = 15 * time.Second
	ScheduleRetryWait = 5 * time.Second
	return accountID, serviceAccount
}

// Smoke: schedule a tiny non-scanner Job (busybox echo) so we don't need the
// Trivy/Popeye images, then poll + fetch logs. Verifies the three primitives
// agree on the wire shape against the live relay+agent path.
func TestRelay_SchedulePollFetchSmoke(t *testing.T) {
	accountID, sa := setupRelay(t)

	jobName, jobUUID, scheduled, err := scheduleJob(accountID, JobSpec{
		NamePrefix:     "orch-smoke",
		Image:          "busybox:1.36",
		Command:        []string{"sh", "-c"},
		Args:           []string{"echo hello-from-orchestrator; sleep 2"},
		ServiceAccount: sa,
	})
	if err != nil {
		t.Fatalf("scheduleJob: %v", err)
	}
	if !scheduled {
		t.Skip("agent at concurrency cap; rerun later")
	}
	t.Logf("scheduled job=%s uuid=%s", jobName, jobUUID)

	deadline := time.Now().Add(2 * time.Minute)
	final, err := pollUntilDone(accountID, jobName, deadline, slog.Default())
	if err != nil {
		t.Fatalf("pollUntilDone: %v", err)
	}
	if final.Status != "Complete" {
		t.Fatalf("status = %s, reason=%q (logs: kubectl -n nudgebee-agent logs job/%s)", final.Status, final.FailureReason, jobName)
	}

	logs, err := getJobLogs(accountID, jobName)
	if err != nil {
		t.Fatalf("getJobLogs: %v", err)
	}
	if !strings.Contains(logs.Stdout, "hello-from-orchestrator") {
		t.Errorf("logs did not contain marker; got: %q", logs.Stdout)
	}
}

// End-to-end through RunScan against the real popeye scanner. Slow (~3-5 min)
// because popeye actually scans the cluster; opt in via long-running CI lane.
func TestRelay_RunScanPopeye(t *testing.T) {
	accountID, sa := setupRelay(t)
	if testing.Short() {
		t.Skip("popeye E2E is long; skipping under -short")
	}

	recs, err := RunScan(testCtx(), ScanAccount{
		AccountID:      accountID,
		TenantID:       os.Getenv("RELAY_TENANT_ID"),
		ServiceAccount: sa,
	}, "popeye_scan", nil)
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	t.Logf("RunScan(popeye_scan) returned %d recommendations", len(recs))
	if len(recs) == 0 {
		t.Skip("popeye returned 0 recommendations — likely a clean cluster, but verify manually if unexpected")
	}
	// Sanity check the shape of the first row.
	r := recs[0]
	if r.RuleName != "popeye_misconfigurations" || r.Category != "Configuration" {
		t.Errorf("rec[0] shape unexpected: %+v", r)
	}
	if r.AccountObjectID == "" || !strings.Contains(r.AccountObjectID, "/") {
		t.Errorf("rec[0].AccountObjectID = %q; want namespace/Kind/Name", r.AccountObjectID)
	}
}

// End-to-end against real trivy CIS — schedules a cluster-scan against the dev
// cluster's API server. Trivy CIS is heavier than popeye (~1-2 min on a busy
// cluster); skip under -short.
func TestRelay_RunScanTrivyCIS(t *testing.T) {
	accountID, sa := setupRelay(t)
	if testing.Short() {
		t.Skip("trivy_cis E2E is long; skipping under -short")
	}
	OverallTimeout = 12 * time.Minute
	recs, err := RunScan(testCtx(), ScanAccount{
		AccountID:      accountID,
		TenantID:       os.Getenv("RELAY_TENANT_ID"),
		ServiceAccount: sa,
	}, "trivy_cis_scan", nil)
	if err != nil {
		t.Fatalf("RunScan(trivy_cis_scan): %v", err)
	}
	t.Logf("trivy_cis_scan returned %d recommendations (stub: 1 raw row)", len(recs))
	if len(recs) == 0 {
		t.Fatalf("expected at least one recommendation row from stub parser")
	}
	if recs[0].RuleName != TrivyCISRuleName {
		t.Errorf("rec[0].RuleName = %q", recs[0].RuleName)
	}
}

// End-to-end against kube-bench. Needs HostPID + 11 hostPath volumes; verifies
// the JobSpec round-trips through the agent's BuildJob and that the kube-bench
// container actually starts (some clusters reject hostPath mounts).
func TestRelay_RunScanKubeBench(t *testing.T) {
	accountID, sa := setupRelay(t)
	if testing.Short() {
		t.Skip("kube_bench E2E creates hostPath-mounted Pod; skipping under -short")
	}
	OverallTimeout = 6 * time.Minute
	recs, err := RunScan(testCtx(), ScanAccount{
		AccountID:      accountID,
		TenantID:       os.Getenv("RELAY_TENANT_ID"),
		ServiceAccount: sa,
	}, "kube_bench_scan", nil)
	if err != nil {
		t.Fatalf("RunScan(kube_bench_scan): %v", err)
	}
	t.Logf("kube_bench_scan returned %d recommendations", len(recs))
	if len(recs) == 0 {
		t.Fatalf("expected at least one recommendation row from stub parser")
	}
	if recs[0].RuleName != KubeBenchRuleName {
		t.Errorf("rec[0].RuleName = %q", recs[0].RuleName)
	}
}

// End-to-end against Nova `find` — Helm chart upgrade discovery. The Nova
// image takes a long time to start (image pull + Helm release scan); skip
// under -short and use a generous deadline.
func TestRelay_RunScanHelmChartUpgrade(t *testing.T) {
	accountID, sa := setupRelay(t)
	if testing.Short() {
		t.Skip("helm_chart_upgrade E2E pulls Nova image; skipping under -short")
	}
	OverallTimeout = 15 * time.Minute
	recs, err := RunScan(testCtx(), ScanAccount{
		AccountID:      accountID,
		TenantID:       os.Getenv("RELAY_TENANT_ID"),
		ServiceAccount: sa,
	}, "helm_chart_upgrade", nil)
	if err != nil {
		t.Fatalf("RunScan(helm_chart_upgrade): %v", err)
	}
	t.Logf("helm_chart_upgrade returned %d recommendations", len(recs))
	if len(recs) == 0 {
		t.Fatalf("expected at least one recommendation row from stub parser")
	}
	if recs[0].RuleName != HelmChartUpgradeRuleName {
		t.Errorf("rec[0].RuleName = %q", recs[0].RuleName)
	}
}

// End-to-end through RunOne: RunScan + Persist. Verifies the orchestrator
// writes to the recommendation table — without this we only know parsing
// returns rows in memory.
//
// Required env (in addition to RELAY_*):
//
//	APP_DATABASE_URL=postgres://postgres:<pass>@localhost:5432/nudgebee?sslmode=disable
//
// (Use the cloud-sql-proxy tunnel that's already running on the dev box.)
func TestRelay_PersistEndToEnd(t *testing.T) {
	accountID, sa := setupRelay(t)
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("APP_DATABASE_URL not set — skipping persist verification (need cloud-sql-proxy tunnel)")
	}
	tenantID := os.Getenv("RELAY_TENANT_ID")
	if tenantID == "" {
		t.Skip("RELAY_TENANT_ID not set")
	}
	config.Config.ServiceDBUrl = dbURL

	ctx := testCtx()
	account := scanAccountForTest(accountID, tenantID, sa)

	// Popeye writes per-linter rule_names ("deployments_misconfigurations",
	// "clusterroles_misconfigurations", ...). Filter on category=Configuration
	// to count all popeye-shaped rows in one query.
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	const popeyeFilter = `AND category = 'Configuration' AND rule_name LIKE '%_misconfigurations'`
	var beforeOpen, beforeArchive int
	_ = dbms.Db.Get(&beforeOpen, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 `+popeyeFilter+` AND status = 'Open'`, accountID)
	_ = dbms.Db.Get(&beforeArchive, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 `+popeyeFilter+` AND status = 'Archive'`, accountID)
	t.Logf("pre-run: open=%d archive=%d", beforeOpen, beforeArchive)

	if err := RunOne(ctx, account, "popeye_scan", nil); err != nil {
		t.Fatalf("RunOne(popeye_scan): %v", err)
	}

	var afterOpen, afterArchive int
	if err := dbms.Db.Get(&afterOpen, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 `+popeyeFilter+` AND status = 'Open'`, accountID); err != nil {
		t.Fatalf("post-run open count: %v", err)
	}
	if err := dbms.Db.Get(&afterArchive, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 `+popeyeFilter+` AND status = 'Archive'`, accountID); err != nil {
		t.Fatalf("post-run archive count: %v", err)
	}
	t.Logf("post-run: open=%d archive=%d", afterOpen, afterArchive)

	if afterOpen == 0 {
		t.Fatalf("no Open popeye rows after RunOne — Persist didn't write")
	}
	// Schema sanity: verify a sample row matches the Recommendation shape we expect.
	type sampleRow struct {
		Category             string `db:"category"`
		RuleName             string `db:"rule_name"`
		Severity             string `db:"severity"`
		AccountObjectID      string `db:"account_object_id"`
		Recommendation       string `db:"recommendation"`
		RecommendationAction string `db:"recommendation_action"`
	}
	var s sampleRow
	if err := dbms.Db.Get(&s, `SELECT category, rule_name, severity, account_object_id, recommendation::text, recommendation_action FROM recommendation WHERE cloud_account_id = $1 `+popeyeFilter+` AND status = 'Open' ORDER BY updated_at DESC LIMIT 1`, accountID); err != nil {
		t.Fatalf("sample row: %v", err)
	}
	if s.Category != "Configuration" {
		t.Errorf("category = %q; want Configuration", s.Category)
	}
	if !strings.HasSuffix(s.RuleName, "_misconfigurations") {
		t.Errorf("rule_name = %q; want suffix _misconfigurations", s.RuleName)
	}
	if s.RecommendationAction != "Modify" {
		t.Errorf("recommendation_action = %q; want Modify", s.RecommendationAction)
	}
	if s.AccountObjectID == "" || !strings.Contains(s.AccountObjectID, "/") {
		t.Errorf("account_object_id = %q; want namespace/Kind/Name", s.AccountObjectID)
	}
}

// Persist verification for the stub-parser scanners (trivy_cis, kube_bench,
// helm_chart_upgrade). Each writes a 1-row "raw report attached" recommendation
// in Phase 2a; after Phase 2b's per-scanner parsers ship this test should be
// extended to assert the per-issue row counts. helm_chart_upgrade takes ~5+
// minutes (Nova image pull on first run); skip via env if unwanted.
func TestRelay_PersistAllScanners(t *testing.T) {
	accountID, sa := setupRelay(t)
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("APP_DATABASE_URL not set")
	}
	tenantID := os.Getenv("RELAY_TENANT_ID")
	if tenantID == "" {
		t.Skip("RELAY_TENANT_ID not set")
	}
	config.Config.ServiceDBUrl = dbURL

	ctx := testCtx()
	account := scanAccountForTest(accountID, tenantID, sa)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	// Per-scanner lookup: rule_name + category for the stub parsers (these
	// match the constants in parser_stubs.go).
	type scannerCheck struct {
		scannerName string
		ruleName    string
		category    string
	}
	cases := []scannerCheck{
		{"trivy_cis_scan", TrivyCISRuleName, "Security"},
		{"kube_bench_scan", KubeBenchRuleName, "Security"},
	}
	if os.Getenv("SKIP_HELM_E2E") == "" {
		cases = append(cases, scannerCheck{"helm_chart_upgrade", HelmChartUpgradeRuleName, "InfraUpgrade"})
	}

	OverallTimeout = 15 * time.Minute

	for _, c := range cases {
		t.Run(c.scannerName, func(t *testing.T) {
			var beforeOpen int
			_ = dbms.Db.Get(&beforeOpen, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 AND rule_name = $2 AND category = $3 AND status = 'Open'`, accountID, c.ruleName, c.category)
			t.Logf("%s pre-run: open=%d", c.scannerName, beforeOpen)

			if err := RunOne(ctx, account, c.scannerName, nil); err != nil {
				t.Fatalf("RunOne(%s): %v", c.scannerName, err)
			}

			var afterOpen int
			if err := dbms.Db.Get(&afterOpen, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 AND rule_name = $2 AND category = $3 AND status = 'Open'`, accountID, c.ruleName, c.category); err != nil {
				t.Fatalf("post-run open count: %v", err)
			}
			t.Logf("%s post-run: open=%d", c.scannerName, afterOpen)
			if afterOpen == 0 {
				t.Fatalf("no Open rows for %s after RunOne — Persist didn't write", c.scannerName)
			}
		})
	}
}

// End-to-end for the certificate scanner — direct-K8s-API path (not Job-based).
// Calls RunCertificateScan which fetches cert-manager.io/v1/certificates via
// the agent's get_resource primitive, parses, persists.
func TestRelay_PersistCertificates(t *testing.T) {
	accountID, _ := setupRelay(t)
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("APP_DATABASE_URL not set")
	}
	tenantID := os.Getenv("RELAY_TENANT_ID")
	if tenantID == "" {
		t.Skip("RELAY_TENANT_ID not set")
	}
	config.Config.ServiceDBUrl = dbURL

	ctx := testCtx()
	account := ScanAccount{AccountID: accountID, TenantID: tenantID}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	var beforeOpen int
	_ = dbms.Db.Get(&beforeOpen, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 AND rule_name = $2 AND status = 'Open'`, accountID, CertificateExpiryRuleName)
	t.Logf("certificate_expiry pre-run: open=%d", beforeOpen)

	if err := RunCertificateScan(ctx, account); err != nil {
		t.Fatalf("RunCertificateScan: %v", err)
	}

	var afterOpen int
	if err := dbms.Db.Get(&afterOpen, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 AND rule_name = $2 AND status = 'Open'`, accountID, CertificateExpiryRuleName); err != nil {
		t.Fatalf("post-run open count: %v", err)
	}
	t.Logf("certificate_expiry post-run: open=%d", afterOpen)
	// dev cluster has cert-manager certificates; we expect at least one row
	// with a populated status.not_after (skipped certs without status are
	// expected too — see ParseCertificates).
}

// End-to-end for the k8s_version_upgrade scanner — direct-K8s-API path
// (calls get_resource for kube-system pods + nodes; no Job).
func TestRelay_PersistK8sVersionUpgrade(t *testing.T) {
	accountID, _ := setupRelay(t)
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("APP_DATABASE_URL not set")
	}
	tenantID := os.Getenv("RELAY_TENANT_ID")
	if tenantID == "" {
		t.Skip("RELAY_TENANT_ID not set")
	}
	config.Config.ServiceDBUrl = dbURL

	ctx := testCtx()
	account := ScanAccount{AccountID: accountID, TenantID: tenantID}
	if err := RunK8sVersionUpgrade(ctx, account); err != nil {
		t.Fatalf("RunK8sVersionUpgrade: %v", err)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	var open int
	if err := dbms.Db.Get(&open, `SELECT COUNT(*) FROM recommendation WHERE cloud_account_id = $1 AND rule_name = $2 AND status = 'Open'`, accountID, KubeProxyVersionRuleName); err != nil {
		t.Fatalf("count: %v", err)
	}
	t.Logf("kube_proxy_version post-run: open=%d", open)
	// Note: GKE Dataplane V2 clusters don't run kube-proxy, so 0 rows is a
	// valid outcome. We just want to confirm the path executes without error.
}

// Concurrency-cap fast-path: schedule N+1 long-running Jobs in quick succession
// to confirm the agent's concurrent_job_limit response surfaces as
// scheduled=false (not an error) so the orchestrator can back off.
func TestRelay_ConcurrencyCapBackoff(t *testing.T) {
	accountID, sa := setupRelay(t)
	if testing.Short() {
		t.Skip("opens 6 Jobs; skipping under -short")
	}

	// Hold-open Jobs that sleep beyond the test window.
	const holdN = 5
	jobs := make([]string, 0, holdN)
	for i := 0; i < holdN; i++ {
		jobName, _, scheduled, err := scheduleJob(accountID, JobSpec{
			NamePrefix:     "orch-hold",
			Image:          "busybox:1.36",
			Command:        []string{"sh", "-c"},
			Args:           []string{"sleep 120"},
			ServiceAccount: sa,
		})
		if err != nil {
			t.Fatalf("scheduleJob[%d]: %v", i, err)
		}
		if !scheduled {
			// Cap was hit earlier than expected — fine for this assertion;
			// just verify we reached scheduled=false at some point.
			t.Logf("agent already at cap by attempt %d", i)
			return
		}
		jobs = append(jobs, jobName)
	}
	// One more should trip scheduled=false.
	_, _, scheduled, err := scheduleJob(accountID, JobSpec{
		NamePrefix:     "orch-overflow",
		Image:          "busybox:1.36",
		Command:        []string{"sh", "-c"},
		Args:           []string{"sleep 30"},
		ServiceAccount: sa,
	})
	if err != nil {
		t.Fatalf("scheduleJob (overflow): %v", err)
	}
	if scheduled {
		t.Errorf("expected scheduled=false from concurrency cap; got success — agent may have raised the cap (default is 5)")
	}
}
