package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func imageScanTestContext() core.NbToolContext {
	return core.NbToolContext{
		AccountId: "550e8400-e29b-41d4-a716-446655440000",
		Ctx:       security.NewRequestContext(context.Background(), nil, nil, nil, nil),
	}
}

func TestWaitForImageScansReturnsWhenEveryImageIsDone(t *testing.T) {
	images := []string{"web:1", "sidecar:2"}
	polls := []map[string]imageScanResult{
		{},
		{"web:1": {Image: "web:1", Status: "clean"}},
		{"web:1": {Image: "web:1", Status: "clean"}, "sidecar:2": {Image: "sidecar:2", Status: "vulnerable", Total: 3}},
	}
	calls, sleeps := 0, 0
	got, err := waitForImageScans(imageScanTestContext(), images, func() (map[string]imageScanResult, error) {
		r := polls[calls]
		calls++
		return r, nil
	}, func(time.Duration) { sleeps++ })
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, 3, calls, "stops polling once every image has a result")
	assert.Equal(t, 2, sleeps)
}

func TestWaitForImageScansStopsAtTheBudget(t *testing.T) {
	calls := 0
	var waited time.Duration
	got, err := waitForImageScans(imageScanTestContext(), []string{"web:1", "sidecar:2"}, func() (map[string]imageScanResult, error) {
		calls++
		return map[string]imageScanResult{"web:1": {Image: "web:1", Status: "clean"}}, nil
	}, func(d time.Duration) { waited += d })
	require.NoError(t, err, "a scan still running is not an error")
	assert.Len(t, got, 1)
	assert.Equal(t, imageScanWait.attempts, calls)
	assert.Greater(t, waited, 5*time.Minute)
	assert.Less(t, waited, 9*time.Minute)
}

func TestWaitForImageScansErrorsOnlyWhenNoPollSucceeded(t *testing.T) {
	boom := errors.New("db down")
	_, err := waitForImageScans(imageScanTestContext(), []string{"web:1"}, func() (map[string]imageScanResult, error) {
		return nil, boom
	}, func(time.Duration) {})
	assert.ErrorIs(t, err, boom)

	calls := 0
	got, err := waitForImageScans(imageScanTestContext(), []string{"web:1"}, func() (map[string]imageScanResult, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return map[string]imageScanResult{"web:1": {Image: "web:1", Status: "clean"}}, nil
	}, func(time.Duration) {})
	require.NoError(t, err, "a failed poll is retried")
	assert.Equal(t, "clean", got["web:1"].Status)
}

func decodeImageScanReport(t *testing.T, resp core.NBToolResponse) map[string]any {
	t.Helper()
	var report map[string]any
	require.NoError(t, common.UnmarshalJson([]byte(resp.Data), &report), resp.Data)
	return report
}

func TestImageScanResponseStillRunningSaysWhereResultsLand(t *testing.T) {
	resp, err := ImageScanTool{}.scanResponse(imageScanTestContext(), "web", "default", []string{"web:1", "sidecar:2"},
		map[string]imageScanResult{"web:1": {Image: "web:1", Status: "clean"}})
	require.NoError(t, err, "a scan still running when the wait ends is not a failure")
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	report := decodeImageScanReport(t, resp)
	assert.Contains(t, report["message"], "still running for sidecar:2")
	assert.Contains(t, report["message"], imageScanTabHint)
	images := report["images"].([]any)
	require.Len(t, images, 2)
	assert.Equal(t, "clean", images[0].(map[string]any)["status"])
	assert.Equal(t, imageScanRunning, images[1].(map[string]any)["status"])
	require.Len(t, resp.References, 1)
}

func TestImageScanResponseFailsOnlyWhenEveryImageFailed(t *testing.T) {
	resp, err := ImageScanTool{}.scanResponse(imageScanTestContext(), "web", "default", []string{"web:1"},
		map[string]imageScanResult{"web:1": {Image: "web:1", Status: "unscannable", Error: "image pull failed"}})
	require.Error(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	report := decodeImageScanReport(t, resp)
	assert.Equal(t, "image pull failed", report["images"].([]any)[0].(map[string]any)["error"])

	resp, err = ImageScanTool{}.scanResponse(imageScanTestContext(), "web", "default", []string{"web:1", "sidecar:2"},
		map[string]imageScanResult{"web:1": {Image: "web:1", Status: "failed"}, "sidecar:2": {Image: "sidecar:2", Status: "clean"}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
}

// TestImageScanReadsTheServerRunScan runs the result path against a real
// Postgres: the scan's image_scan_summary row says it ended, and the report
// carries the open findings, most severe first. A summary from before the
// request does not count. Set METASTORE_SQL_TEST_DB_URL to a disposable
// database to run it; the test works in its own schema and drops it
// afterwards.
func TestImageScanReadsTheServerRunScan(t *testing.T) {
	dsn := os.Getenv("METASTORE_SQL_TEST_DB_URL")
	if dsn == "" {
		t.Skip("METASTORE_SQL_TEST_DB_URL not set")
	}
	schema := fmt.Sprintf("image_scan_test_%d", time.Now().UnixNano())
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	admin.MustExec("CREATE SCHEMA " + schema)
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	const (
		tenant = "660e8400-e29b-41d4-a716-446655440000"
		fresh  = "registry.example.com/web:1.0"
		stale  = "registry.example.com/sidecar:2.0"
	)
	nbCtx := imageScanTestContext()
	admin.MustExec(fmt.Sprintf(`
		CREATE TABLE %[1]s.k8s_pods (workload_name text, workload_type text, namespace text,
			cloud_account_id uuid, tenant_id uuid, meta jsonb, is_active boolean);
		CREATE TABLE %[1]s.vulnerabilities (id uuid PRIMARY KEY, vuln_id text, package_name text,
			severity text, description text, details jsonb);
		CREATE TABLE %[1]s.recommendation (id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			cloud_account_id uuid, tenant_id uuid, severity text, status text, category text,
			rule_name text, account_object_id text, recommendation jsonb, vulnerability_id uuid,
			created_at timestamptz DEFAULT now(), updated_at timestamptz DEFAULT now());`, schema))
	for _, image := range []string{fresh, stale} {
		admin.MustExec(fmt.Sprintf(`INSERT INTO %s.k8s_pods VALUES ('web', 'Deployment', 'default', $1, $2,
			jsonb_build_object('config', jsonb_build_object('containers', jsonb_build_array(jsonb_build_object('image', $3::text)))), true)`, schema),
			nbCtx.AccountId, tenant, image)
	}
	since := time.Now().UTC().Truncate(time.Second)
	summary := func(image, status string, scannedAt time.Time, total int) {
		admin.MustExec(fmt.Sprintf(`INSERT INTO %s.recommendation
			(cloud_account_id, tenant_id, severity, status, category, rule_name, account_object_id, recommendation)
			VALUES ($1, $2, 'Info', 'Open', 'Security', 'image_scan_summary', $3,
			jsonb_build_object('image_name', $3::text, 'status', $4::text, 'scanned_at', $5::text, 'total', $6::int,
				'counts', jsonb_build_object('High', $6::int), '__scan_summary__', true))`, schema),
			nbCtx.AccountId, tenant, image, status, scannedAt.Format(time.RFC3339), total)
	}
	summary(fresh, "vulnerable", since, 2)
	summary(stale, "clean", since.Add(-time.Hour), 0)
	finding := func(image, status, severity, cve string) {
		admin.MustExec(fmt.Sprintf(`INSERT INTO %s.recommendation
			(cloud_account_id, tenant_id, severity, status, category, rule_name, account_object_id, recommendation)
			VALUES ($1, $2, $3, $4, 'Security', 'image_scan', $5, jsonb_build_object('image_name', $6::text, 'VulnerabilityID', $7::text))`, schema),
			nbCtx.AccountId, tenant, severity, status, image+"-"+cve, image, cve)
	}
	finding(fresh, "Open", "Medium", "CVE-2026-0003")
	finding(fresh, "Open", "Critical", "CVE-2026-0001")
	finding(fresh, "Dismissed", "Critical", "CVE-2026-0002")

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sqlx.Connect("postgres", dsn+sep+"search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	common.ResetDatabaseManager(common.Metastore)
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })

	got, err := queryImageScanSummaries(nbCtx.AccountId, []string{fresh, stale}, since)
	require.NoError(t, err)
	require.Len(t, got, 1, "a summary from before the request is an older scan: %v", got)
	assert.Equal(t, "vulnerable", got[fresh].Status)
	assert.Equal(t, 2, got[fresh].Total)
	assert.Equal(t, map[string]int{"High": 2}, got[fresh].Counts)

	resp, err := ImageScanTool{}.scanResponse(nbCtx, "web", "default", []string{fresh}, got)
	require.NoError(t, err)
	report := decodeImageScanReport(t, resp)
	findings, ok := report["findings"].([]any)
	require.True(t, ok, "report: %v", report)
	require.Len(t, findings, 2, "open findings only: %v", findings)
	assert.Equal(t, "CVE-2026-0001", findings[0].(map[string]any)["vulnerability_id"], "most severe first")
	assert.Equal(t, "CVE-2026-0003", findings[1].(map[string]any)["vulnerability_id"])
}
