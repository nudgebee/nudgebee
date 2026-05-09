package scan_orchestrator

import (
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/lib/pq"
)

// Persist archives the previous run's recommendations for (account_id, category,
// rule_name) and UPSERTs the new ones. Mirrors the collector's
// archive_existing_with_rules + upsert_recommendations pattern
// (event_handler.py:1831-1832, 1950-1961) so the UI's open/archive transitions
// behave identically to the legacy path.
//
// Empty `recs` is a valid input — it archives the rule's open rows, which is
// the correct behaviour for "scan succeeded, found nothing".
func Persist(ctx *security.RequestContext, account ScanAccount, scannerName string, recs []Recommendation) error {
	scanner, ok := ScannerCatalog[scannerName]
	if !ok {
		return fmt.Errorf("scan_orchestrator.Persist: unknown scanner %q", scannerName)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("scan_orchestrator.Persist: db: %w", err)
	}

	// Categorise every recommendation by category so archive scopes correctly.
	// Most scanners write to a single category, but kube_bench writes to
	// "Security" and image_scanner also "Security"; the column is part of the
	// recommendation table's PK tuple, so we archive per (rule_name, category).
	type archiveKey struct{ category, ruleName string }
	archiveKeys := map[archiveKey]struct{}{
		// Always archive the rule's primary category, even when recs is empty —
		// no-finding scans should clear out yesterday's open rows.
		{category: scanner.RuleName, ruleName: scanner.RuleName}: {},
	}
	for _, r := range recs {
		archiveKeys[archiveKey{category: r.Category, ruleName: r.RuleName}] = struct{}{}
	}

	// Archive first. The UPSERT below will flip status back to "Open" for any
	// row that survived the new scan; rows that didn't show up stay "Archive".
	for k := range archiveKeys {
		_, err = dbms.Db.Exec(
			`UPDATE recommendation SET status = 'Archive', updated_at = $1
			 WHERE tenant_id = $2 AND cloud_account_id = $3 AND category = $4 AND rule_name = $5 AND status != 'Archive'`,
			time.Now(), account.TenantID, account.AccountID, k.category, k.ruleName,
		)
		if err != nil {
			ctx.GetLogger().Error("scan_orchestrator: archive failed",
				"scanner", scannerName, "category", k.category, "rule_name", k.ruleName, "error", err)
			return fmt.Errorf("archive existing %s/%s: %w", k.category, k.ruleName, err)
		}
	}

	if len(recs) == 0 {
		ctx.GetLogger().Info("scan_orchestrator: no recommendations to upsert (rules archived)",
			"scanner", scannerName, "account_id", account.AccountID)
		return nil
	}

	// UPSERT in a single batch. Mirrors recommendation.upsertRecommendationData
	// (k8s_recommendation_service.go:43-61); the on-conflict tuple is the
	// recommendation table's unique index on
	// (rule_name, cloud_account_id, resource_id, category, account_object_id).
	now := time.Now()
	rows := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		row := map[string]any{
			"status":                 r.Status,
			"tenant_id":              r.TenantID,
			"cloud_account_id":       r.CloudAccountID,
			"recommendation":         r.Recommendation,
			"severity":               r.Severity,
			"category":               r.Category,
			"rule_name":              r.RuleName,
			"estimated_savings":      0,
			"recommendation_action":  r.RecommendationAction,
			"resource_id":            nullIfEmpty(r.ResourceID),
			"account_object_id":      r.AccountObjectID,
			"updated_at":             now,
			"finops_score":           0,
			"finops_band":            "",
			"finops_score_breakdown": "{}",
		}
		// finops_score / finops_band / finops_score_breakdown are populated by the
		// existing recommendation.UpdateRecommendationFinopsScores batch path —
		// keeping it out of line here avoids an import cycle with services/recommendation.
		rows = append(rows, row)
	}

	_, err = dbms.Db.NamedExec(
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
		               status = EXCLUDED.status,
		               updated_at = EXCLUDED.updated_at,
		               estimated_savings = EXCLUDED.estimated_savings,
		               severity = EXCLUDED.severity,
		               recommendation_action = EXCLUDED.recommendation_action,
		               finops_score = EXCLUDED.finops_score,
		               finops_band = EXCLUDED.finops_band,
		               finops_score_breakdown = EXCLUDED.finops_score_breakdown`,
		rows,
	)
	if err != nil {
		// pq error wrapping mostly for column-mismatch debugging during early
		// scanner integration; once the schemas are stable this is just a
		// generic DB error.
		if pgErr, ok := err.(*pq.Error); ok {
			ctx.GetLogger().Error("scan_orchestrator: upsert pg error",
				"scanner", scannerName, "constraint", pgErr.Constraint, "detail", pgErr.Detail, "error", err)
		}
		return fmt.Errorf("upsert recommendations: %w", err)
	}
	ctx.GetLogger().Info("scan_orchestrator: persisted",
		"scanner", scannerName, "account_id", account.AccountID, "rows", len(rows))
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
