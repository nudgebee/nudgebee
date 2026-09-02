package scan_orchestrator

import (
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// RunSecretEnvExposureScan lists pods (and ReplicaSets, to roll pods up to
// their owning Deployment) cluster-wide and persists a `secret_env_exposure`
// recommendation for every workload that consumes a Kubernetes Secret through
// a container environment variable.
//
// Direct-K8s-API scanner rather than a Job: the whole check is a read over pod
// specs the agent's get_resource already returns, so there is nothing for a
// scanner image to add. Same shape as RunUnusedPVCScan.
//
// CIS Kubernetes Benchmark 5.4.1 ("Prefer using secrets as files over secrets
// as environment variables") is a Manual control with no automated check behind
// it, in Trivy's k8s-cis-1.23 spec or anywhere else we ship — popeye's Secret
// linter only reports *unreferenced* Secrets, kube-bench never reads workload
// manifests, and the image scanner looks at layers. This scanner is what closes
// that gap.
//
// Secret values are never fetched: only pod specs are read, never a Secret's
// `data`.
func RunSecretEnvExposureScan(ctx *security.RequestContext, account ScanAccount) error {
	logger := ctx.GetLogger().With("scanner", "secret_env_exposure", "account_id", account.AccountID, "tenant_id", account.TenantID)

	pods, err := fetchK8sList(account.AccountID, "pods", "", "v1", true)
	if err != nil {
		return fmt.Errorf("secret_env_exposure: list pods: %w", err)
	}

	// ReplicaSets are the pod → Deployment hop. A cluster where this fetch
	// fails (RBAC gap, agent hiccup) still gets findings — they just anchor on
	// the ReplicaSet instead of the Deployment — so degrade rather than abort.
	replicaSets, err := fetchK8sList(account.AccountID, "replicasets", "apps", "v1", true)
	if err != nil {
		logger.Warn("secret_env_exposure: list replicasets failed, findings will anchor on ReplicaSets", "error", err)
		replicaSets = nil
	}
	logger.Info("secret_env_exposure: fetched", "pods", len(pods), "replicasets", len(replicaSets))

	exposures := IdentifySecretEnvExposures(pods, replicaSets)
	recs, err := ParseSecretEnvExposures(exposures, account)
	if err != nil {
		return fmt.Errorf("secret_env_exposure: parse: %w", err)
	}
	logger.Info("secret_env_exposure: identified", "workloads_with_exposure", len(exposures), "recommendation_count", len(recs))

	return persistSecretEnvExposures(ctx, account, recs)
}

// persistSecretEnvExposures archives the previous scan's rows then UPSERTs the
// new ones, so a workload that moves its Secret to a file mount drops out of
// the UI on the next scan.
//
// Both statements run in one transaction: a failed upsert would otherwise leave
// every row archived and the finding would vanish from the UI until the next
// daily tick.
//
// Only the scanner-owned 'Open' state is archived, and the upsert's status is
// guarded by the same CASE the shared Persist path uses — a re-scan must never
// move a row out of a user-owned state (Dismissed / snoozed / InProgress), or
// it resurrects findings the user has already decided on.
func persistSecretEnvExposures(ctx *security.RequestContext, account ScanAccount, recs []Recommendation) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("secret_env_exposure: db: %w", err)
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		return fmt.Errorf("secret_env_exposure: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(); rbErr != nil {
				ctx.GetLogger().Warn("secret_env_exposure: rollback failed", "error", rbErr)
			}
		}
	}()

	now := time.Now()

	if _, err := tx.Exec(
		`UPDATE recommendation SET status = 'Archive', updated_at = $1
		 WHERE tenant_id = $2 AND cloud_account_id = $3
		   AND category = 'Configuration' AND rule_name = $4 AND status = 'Open'`,
		now, account.TenantID, account.AccountID, SecretEnvExposureRuleName,
	); err != nil {
		return fmt.Errorf("secret_env_exposure: archive: %w", err)
	}

	if len(recs) == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("secret_env_exposure: commit: %w", err)
		}
		committed = true
		ctx.GetLogger().Info("secret_env_exposure: no exposures to upsert (rows archived)",
			"account_id", account.AccountID)
		UpsertScheduleJobState(ctx, account, "secret_env_exposure")
		return nil
	}

	rows := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		rows = append(rows, map[string]any{
			"status":                 r.Status,
			"tenant_id":              r.TenantID,
			"cloud_account_id":       r.CloudAccountID,
			"recommendation":         r.Recommendation,
			"severity":               r.Severity,
			"category":               r.Category,
			"rule_name":              r.RuleName,
			"estimated_savings":      r.EstimatedSavings,
			"recommendation_action":  r.RecommendationAction,
			"resource_id":            nullIfEmpty(r.ResourceID),
			"account_object_id":      r.AccountObjectID,
			"updated_at":             now,
			"finops_score":           0,
			"finops_band":            "",
			"finops_score_breakdown": "{}",
		})
	}

	if _, err := tx.NamedExec(
		`INSERT INTO recommendation
		   (status, tenant_id, cloud_account_id, recommendation, severity, category, rule_name,
		    estimated_savings, recommendation_action, resource_id, account_object_id, updated_at,
		    finops_score, finops_band, finops_score_breakdown)
		 VALUES
		   (:status, :tenant_id, :cloud_account_id, :recommendation, :severity, :category, :rule_name,
		    :estimated_savings, :recommendation_action, :resource_id, :account_object_id, :updated_at,
		    :finops_score, :finops_band, :finops_score_breakdown)
		 ON CONFLICT (rule_name, cloud_account_id, resource_id, category, account_object_id)
		 DO UPDATE SET recommendation = EXCLUDED.recommendation,
		               status = CASE WHEN recommendation.status NOT IN ('Open', 'Archive')
		                             THEN recommendation.status ELSE EXCLUDED.status END,
		               updated_at = EXCLUDED.updated_at,
		               severity = EXCLUDED.severity,
		               recommendation_action = EXCLUDED.recommendation_action`,
		rows,
	); err != nil {
		return fmt.Errorf("secret_env_exposure: upsert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("secret_env_exposure: commit: %w", err)
	}
	committed = true
	ctx.GetLogger().Info("secret_env_exposure: persisted",
		"account_id", account.AccountID, "rows", len(rows))
	UpsertScheduleJobState(ctx, account, "secret_env_exposure")
	return nil
}
