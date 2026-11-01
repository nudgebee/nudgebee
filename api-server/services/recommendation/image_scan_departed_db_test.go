package recommendation

import (
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestArchiveDepartedImages_DB runs the departed-image sweep against a real
// Postgres planner. The statement is a destructive UPDATE driven by a three-way
// UNION over jsonb, a text extraction and a guarded timestamp cast; a mistake in
// any of those is invisible to Go and retires findings that still apply.
//
// Runs against throwaway tables, never the real ones, so it is safe against any
// database it is pointed at. DB-gated: skips with no database, fails in the job
// that provisions one.
func TestArchiveDepartedImages_DB(t *testing.T) {
	// Opens its own connection rather than the shared manager. Fetching the
	// shared one caches it for the package, after which a sibling test that
	// registers a mock manager silently keeps getting this real one instead.
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	dbms, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = dbms.Close() })

	const (
		recTable  = "zz_departed_rec_test"
		podTable  = "zz_departed_pod_test"
		wlTable   = "zz_departed_workload_test"
		account   = "acc-1"
		tenant    = "tenant-1"
		staleAcct = "acc-stale"
	)

	mustExec := func(q string, args ...any) {
		_, e := dbms.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() {
		for _, tbl := range []string{recTable, podTable, wlTable} {
			_, _ = dbms.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
		}
	}
	drop()
	t.Cleanup(drop)

	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		id text PRIMARY KEY, cloud_account_id text, tenant_id text, category text,
		rule_name text, status text, account_object_id text, recommendation jsonb)`, recTable))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		cloud_account_id text, tenant_id text, is_active boolean, last_seen timestamptz,
		namespace text, workload_type text, workload_name text, meta jsonb)`, podTable))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		cloud_account_id text, tenant_id text, is_active boolean, namespace text,
		name text, kind text, meta jsonb)`, wlTable))

	containers := func(images ...string) string {
		out := `{"config":{"containers":[`
		for i, img := range images {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf(`{"image":%q}`, img)
		}
		return out + `]}}`
	}
	pod := func(acct string, active bool, changedDaysAgo int, ownerKind, ownerName, meta string) {
		mustExec(fmt.Sprintf(`INSERT INTO %s VALUES ($1,$2,$3,now() - make_interval(days => $4),'ns',$5,$6,$7::jsonb)`, podTable),
			acct, tenant, active, changedDaysAgo, ownerKind, ownerName, meta)
	}
	workload := func(active bool, kind, name, meta string) {
		mustExec(fmt.Sprintf(`INSERT INTO %s VALUES ($1,$2,$3,'ns',$4,$5,$6::jsonb)`, wlTable),
			account, tenant, active, name, kind, meta)
	}

	// Inventory. The account is fresh; a pod reported minutes ago proves the
	// agent is reporting, which is what makes any archiving eligible at all.
	pod(account, true, 0, "Deployment", "web", containers("live:1"))
	// The agent reports a pod only when it changes, so a pod running unchanged
	// for weeks has an old last_seen and is still in the cluster.
	pod(account, true, imageScanFreshDays+23, "Deployment", "api", containers("stable:1"))
	// A pod that finished or restarted a few days ago is still a recent sighting.
	pod(account, false, 3, "Deployment", "worker", containers("recent:1"))
	// A CronJob that runs rarely: its last pod finished long ago and that run's
	// Job has been pruned, but the CronJob still declares the image. Jobs and
	// CronJobs carry their containers under job_data, not config.
	pod(account, false, imageScanFreshDays+13, "Job", "report-123", containers("rare-cronjob:1"))
	workload(true, "CronJob", "report", `{"job_data":{"containers":[{"image":"rare-cronjob:1"}]}}`)
	// Once the CronJob itself is deleted its image is held by nothing current.
	pod(account, false, imageScanFreshDays+13, "Job", "cleanup-100", containers("deleted-cronjob:1"))
	workload(false, "CronJob", "cleanup", `{"job_data":{"containers":[{"image":"deleted-cronjob:1"}]}}`)
	// A pod replaced by a rollout, unchanged since well before it left. Its
	// Deployment still exists but now declares the new image, so the old one is
	// held by nothing but the leftover row.
	pod(account, false, imageScanFreshDays+1, "Deployment", "web", containers("replaced:1"))
	workload(true, "Deployment", "web", containers("live:1"))
	// A workload scaled to zero declares an image but has no pods.
	workload(true, "Deployment", "idle", containers("scaled-to-zero:1"))
	// An account whose agent stopped reporting must be skipped entirely.
	pod(staleAcct, true, 30, "Deployment", "web", containers("whatever:1"))

	// A JSON null container list is not a SQL NULL, so coalesce lets it through
	// and expanding it aborts the statement. One such row anywhere would stop the
	// sweep for every account, so it has to be skipped rather than expanded.
	pod(account, true, 0, "Deployment", "web", `{"config":{"containers":null}}`)
	pod(account, true, 0, "Deployment", "web", `{"config":{}}`)
	workload(true, "Deployment", "odd", `{"config":{"containers":{"image":"not-a-list"}}}`)
	workload(true, "Job", "odd", `{"job_data":{"containers":null}}`)

	// The scanner confirming an image recently counts as a sighting even when the
	// inventory does not model it, such as an init-container-only image.
	mustExec(fmt.Sprintf(`INSERT INTO %s VALUES
		('sum-1',$1,$2,'Security','image_scan_summary','Open','init-only:1',$3::jsonb)`, recTable),
		account, tenant, fmt.Sprintf(`{"scanned_at":%q}`, time.Now().UTC().Format(time.RFC3339)))
	// A summary whose scan is older than the window is not a current sighting.
	mustExec(fmt.Sprintf(`INSERT INTO %s VALUES
		('sum-2',$1,$2,'Security','image_scan_summary','Open','long-ago:1',$3::jsonb)`, recTable),
		account, tenant, fmt.Sprintf(`{"scanned_at":%q}`,
			time.Now().UTC().AddDate(0, 0, -(imageScanFreshDays+30)).Format(time.RFC3339)))

	finding := func(id, image, status, acct string) {
		mustExec(fmt.Sprintf(`INSERT INTO %s VALUES ($1,$2,$3,'Security','image_scan',$4,$5,$6::jsonb)`, recTable),
			id, acct, tenant, status, image+"-CVE-1", fmt.Sprintf(`{"image_name":%q}`, image))
	}
	finding("keep-live", "live:1", "Open", account)
	finding("keep-stable", "stable:1", "Open", account)
	finding("keep-recent", "recent:1", "Open", account)
	finding("keep-rare-cronjob", "rare-cronjob:1", "Open", account)
	finding("keep-scaled", "scaled-to-zero:1", "Open", account)
	finding("keep-init", "init-only:1", "Open", account)
	finding("keep-stale-account", "gone:1", "Open", staleAcct)
	finding("archive-gone", "gone:1", "Open", account)
	finding("archive-long-ago", "long-ago:1", "Open", account)
	finding("archive-replaced", "replaced:1", "Open", account)
	finding("archive-deleted-cronjob", "deleted-cronjob:1", "Open", account)
	// User-owned states are set by a person and must survive.
	finding("keep-dismissed", "gone:1", "Dismissed", account)
	finding("keep-inprogress", "gone:1", "InProgress", account)
	finding("keep-closed", "gone:1", "Closed", account)

	_, err = dbms.Exec(departedImageArchiveQuery(recTable, podTable, wlTable),
		imageScanInventoryFreshHours, imageScanFreshDays, imageScanDepartedBatchLimit,
		models.RecommendationStatusArchive, models.RecommendationStatusOpen)
	require.NoError(t, err)

	want := map[string]string{
		"keep-live":               "Open",
		"keep-stable":             "Open",
		"keep-recent":             "Open",
		"keep-rare-cronjob":       "Open",
		"keep-scaled":             "Open",
		"keep-init":               "Open",
		"keep-stale-account":      "Open",
		"archive-gone":            "Archive",
		"archive-long-ago":        "Archive",
		"archive-replaced":        "Archive",
		"archive-deleted-cronjob": "Archive",
		"keep-dismissed":          "Dismissed",
		"keep-inprogress":         "InProgress",
		"keep-closed":             "Closed",
	}
	for id, expected := range want {
		var got string
		require.NoError(t, dbms.Get(&got,
			fmt.Sprintf(`SELECT status FROM %s WHERE id = $1`, recTable), id))
		if got != expected {
			t.Errorf("%s: status is %q, expected %q", id, got, expected)
		}
	}
}
