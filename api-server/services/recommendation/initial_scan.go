package recommendation

import (
	"database/sql"
	"errors"
	"time"

	"nudgebee/services/account"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// initialScanMarkerName is the cloud_account_attrs key recording that the
// first-connect scan batch has already run for an account. Its presence makes
// TriggerInitialScans a no-op on every subsequent connect.
const initialScanMarkerName = "recommendations:initial_scan:triggered_at"

// initialScanJobNames is the ordered set of server-side ("nb") recommendation
// scans kicked off the first time an account's agent connects. Every entry must
// map to provider "nb" in recommendationJobProviderMap, and every "nb" job
// should appear here — both invariants are enforced by TestInitialScanJobNames.
var initialScanJobNames = []string{
	"krr_scan",
	"volume_analyzer",
	"popeye_scan",
	"trivy_cis_scan",
	"kube_bench_scan",
	"helm_chart_upgrade",
	"spot_scan",
	"abandoned_workload_scan",
	"image_scanner",
}

// TriggerInitialScans runs every server-side recommendation scan once, the first
// time a K8s account's agent connects, so a freshly onboarded cluster populates
// without waiting for the daily refresh crons. It is idempotent: a persistent
// cloud_account_attrs marker ensures reconnects/flapping never re-trigger, and an
// in-process guard covers two connect events racing before the marker is written.
func TriggerInitialScans(ctx *security.RequestContext, accountId, tenantId string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Persistent once-ever guard.
	already, err := initialScansAlreadyTriggered(dbms, accountId)
	if err != nil {
		ctx.GetLogger().Error("initial scan: error checking marker", "error", err, "account_id", accountId)
		return err
	}
	if already {
		ctx.GetLogger().Info("initial scan: already triggered, skipping", "account_id", accountId)
		return nil
	}

	// In-process guard against two connect events racing before the marker write.
	key := "initial_scan:" + accountId
	if !tryStartAccountJob(key, "initial_scan") {
		ctx.GetLogger().Info("initial scan: already running, skipping", "account_id", accountId)
		return nil
	}
	defer finishAccountJob(key)

	// Only K8s accounts have these scans.
	a, err := account.GetAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("initial scan: error getting account", "error", err, "account_id", accountId)
		return err
	}
	if a.AccountType != "kubernetes" {
		ctx.GetLogger().Info("initial scan: account is not kubernetes, skipping",
			"account_id", accountId, "account_type", a.AccountType)
		return nil
	}

	ctx.GetLogger().Info("initial scan: triggering server-side scans",
		"account_id", accountId, "tenant_id", tenantId, "jobs", len(initialScanJobNames))

	for _, jobName := range initialScanJobNames {
		if _, err := CreateRecommendationJob(ctx, RecommendationJobCreateRequest{AccountId: accountId, JobName: jobName}); err != nil {
			// Per-job failures must not block the rest of the batch; each job has
			// its own dedup/in-flight guards so a partial retry is safe.
			ctx.GetLogger().Error("initial scan: error triggering job", "error", err,
				"account_id", accountId, "job_name", jobName)
		}
	}

	// Write the marker after the pass so a hard failure above can be retried on
	// the next connect rather than being permanently suppressed.
	if err := markInitialScansTriggered(dbms, accountId); err != nil {
		ctx.GetLogger().Error("initial scan: error writing marker", "error", err, "account_id", accountId)
		return err
	}
	ctx.GetLogger().Info("initial scan: completed", "account_id", accountId)
	return nil
}

func initialScansAlreadyTriggered(dbms *database.DatabaseManager, accountId string) (bool, error) {
	var value string
	err := dbms.Db.Get(&value,
		"SELECT value FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = $2",
		accountId, initialScanMarkerName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func markInitialScansTriggered(dbms *database.DatabaseManager, accountId string) error {
	_, err := dbms.Db.Exec(
		`INSERT INTO cloud_account_attrs (cloud_account_id, name, value) VALUES ($1, $2, $3)
		ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value`,
		accountId, initialScanMarkerName, time.Now().UTC().Format(time.RFC3339))
	return err
}
