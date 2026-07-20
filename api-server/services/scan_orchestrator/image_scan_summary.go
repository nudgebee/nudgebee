package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// image_scan_summary is the per-image scan-bookkeeping row. Unlike the per-CVE
// image_scan rows, exactly ONE summary row is written per scanned image
// (account_object_id = <image_name>), on every terminal outcome: clean,
// vulnerable, or failed. It is the piece the original design was missing.
//
// It unlocks three things the CVE-rows-only model could not express:
//   - the discovery loop can skip freshly-scanned images (clean OR vulnerable),
//     so clean/failed images stop recycling through the per-cycle budget and
//     starving the backlog;
//   - failed scans get recorded and back off instead of hot-looping;
//   - a clean image (zero CVEs) has a durable footprint, so the UI can show it
//     as "scanned, clean" instead of it vanishing.
//
// A DISTINCT rule_name (ImageScanSummaryRuleName) keeps it out of every existing
// image_scan CVE query. The Persist archive step scopes to rule_name='image_scan'
// so it never touches these rows; upsert bookkeeping lives here instead.
const (
	imageScanStatusClean      = "clean"
	imageScanStatusVulnerable = "vulnerable"
	imageScanStatusFailed     = "failed"
	// imageScanStatusUnscannable marks a scan that couldn't run because the image
	// could not be pulled — the node-local copy the fs-scan relies on was gone by
	// scan time and the registry pull failed (e.g. a private registry the scanner
	// has no credentials for). Distinct from "failed" (which is a genuine scan
	// error worth retrying soon) so the discovery loop backs it off to the longer
	// window instead of retrying daily, and the UI can tell the user the image
	// couldn't be pulled rather than showing a generic failure.
	imageScanStatusUnscannable = "unscannable"

	// imageScanSummaryErrCap bounds the stored scan error so a pathological
	// trivy/relay error can't bloat the recommendation row.
	imageScanSummaryErrCap = 500
)

// imagePullFailureMarker is the substring the agent's wait_for_k8s_job embeds in
// a Job's failure_reason when the scan pod is wedged on an image pull it can't
// complete (ImagePullBackOff/ErrImagePull). It is a cross-repo contract with
// k8s-agent's scanners.imagePullFailure; keep the two in sync.
const imagePullFailureMarker = "image pull failed"

// isImagePullFailure reports whether a RunScan error is an image-pull failure —
// the scan pod could never start because its image wouldn't pull. Older agents
// (pre-terminal-fail) time out instead of reporting this, so such scans fall
// back to the plain "failed" classification, which is the safe default. The
// match is case-insensitive so a casing change in the agent's message can't
// silently reclassify these as generic failures.
func isImagePullFailure(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), imagePullFailureMarker)
}

// severityRank orders trivySeverity's output vocabulary highest→lowest so the
// summary row's own severity column reflects the worst CVE found.
var severityRank = []string{"Critical", "High", "Medium", "Low", "Info"}

// imageScanSummaryBody is the JSON stored in the summary row's recommendation
// column. `__scan_summary__` is a defensive marker; consumers key off the
// rule_name, not this flag.
type imageScanSummaryBody struct {
	ImageName   string         `json:"image_name"`
	Status      string         `json:"status"` // clean | vulnerable | failed | unscannable
	ScannedAt   string         `json:"scanned_at"`
	Namespace   string         `json:"namespace,omitempty"`
	Workload    string         `json:"workload,omitempty"`
	Counts      map[string]int `json:"counts"`
	Total       int            `json:"total"`
	Error       string         `json:"error,omitempty"`
	ScanSummary bool           `json:"__scan_summary__"`
}

// upsertImageScanSummary writes the one bookkeeping row for account.TargetImage.
// recs are the per-CVE rows the scan produced (nil/empty when clean or failed);
// scanErr is non-nil only on the failed path.
func upsertImageScanSummary(ctx *security.RequestContext, account ScanAccount, status string, recs []Recommendation, scanErr error) error {
	if account.TenantID == "" || account.AccountID == "" || account.TargetImage == "" {
		return fmt.Errorf("scan_orchestrator.upsertImageScanSummary: tenant_id, account_id and image are required")
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("scan_orchestrator.upsertImageScanSummary: db: %w", err)
	}

	counts := map[string]int{}
	for _, r := range recs {
		counts[r.Severity]++
	}
	// Summary row severity = worst CVE present (informational only — summary rows
	// never surface in the CVE list). Clean/failed images have no CVE, so Info.
	severity := "Info"
	for _, s := range severityRank {
		if counts[s] > 0 {
			severity = s
			break
		}
	}

	errMsg := ""
	if scanErr != nil {
		errMsg = truncateUTF8Safe(scanErr.Error(), imageScanSummaryErrCap)
	}
	payload, err := json.Marshal(imageScanSummaryBody{
		ImageName:   account.TargetImage,
		Status:      status,
		ScannedAt:   time.Now().UTC().Format(time.RFC3339),
		Namespace:   account.TargetNamespace,
		Workload:    account.TargetPodName,
		Counts:      counts,
		Total:       len(recs),
		Error:       errMsg,
		ScanSummary: true,
	})
	if err != nil {
		return fmt.Errorf("scan_orchestrator.upsertImageScanSummary: marshal: %w", err)
	}

	// resource_id stays NULL — the unique index is NULLS NOT DISTINCT, so the
	// stable (rule_name, account, NULL, category, image) tuple upserts in place.
	// created_at is left to its column default on first insert and untouched on
	// conflict, so it records the FIRST scan; updated_at tracks the LAST.
	now := time.Now()
	_, err = dbms.Db.Exec(
		`INSERT INTO recommendation
		   (status, tenant_id, cloud_account_id, recommendation, severity, category, rule_name,
		    estimated_savings, recommendation_action, resource_id, account_object_id, updated_at,
		    finops_score, finops_band, finops_score_breakdown)
		 VALUES
		   ('Open', $1, $2, $3, $4, 'Security', $5,
		    0, 'Modify', NULL, $6, $7,
		    0, '', '{}')
		 ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id)
		 DO UPDATE SET recommendation = EXCLUDED.recommendation,
		               status         = EXCLUDED.status,
		               severity       = EXCLUDED.severity,
		               updated_at     = EXCLUDED.updated_at`,
		account.TenantID, account.AccountID, string(payload), severity,
		ImageScanSummaryRuleName, account.TargetImage, now,
	)
	if err != nil {
		return fmt.Errorf("scan_orchestrator.upsertImageScanSummary: upsert %q: %w", account.TargetImage, err)
	}
	ctx.GetLogger().Info("scan_orchestrator: image scan summary persisted",
		"account_id", account.AccountID, "image", account.TargetImage, "status", status, "vulns", len(recs))
	return nil
}
