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

	// Archive previous run's rows so resources that disappear from the new
	// scan transition Open → Archive. Two shapes here:
	//
	//   1. Most scanners write under a single (category, rule_name) tuple — we
	//      can archive by exact match. archiveKeys collects them: always
	//      include the scanner's primary tuple (so empty scans still clear
	//      yesterday's rows) plus every distinct tuple in `recs`.
	//   2. popeye writes per-linter rule_names ("deployments_misconfigurations",
	//      "clusterroles_misconfigurations", ...). Exact-match archive can't
	//      cover all of them up-front, so we archive by LIKE pattern instead.
	if scannerName == "popeye_scan" {
		_, err = dbms.Db.Exec(
			`UPDATE recommendation SET status = 'Archive', updated_at = $1
			 WHERE tenant_id = $2 AND cloud_account_id = $3 AND category = 'Configuration'
			   AND rule_name LIKE '%_misconfigurations' AND status != 'Archive'`,
			time.Now(), account.TenantID, account.AccountID,
		)
		if err != nil {
			ctx.GetLogger().Error("scan_orchestrator: popeye archive failed",
				"account_id", account.AccountID, "error", err)
			return fmt.Errorf("archive popeye misconfigurations: %w", err)
		}
	} else {
		type archiveKey struct{ category, ruleName string }
		archiveKeys := map[archiveKey]struct{}{
			{category: scanner.RuleName, ruleName: scanner.RuleName}: {},
		}
		for _, r := range recs {
			archiveKeys[archiveKey{category: r.Category, ruleName: r.RuleName}] = struct{}{}
		}
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
	}

	// Dedupe by conflict tuple. Some scanners (notably trivy_cis) emit the
	// same (check, class, target) finding multiple times in one report; the
	// UPSERT below would otherwise fail with "ON CONFLICT DO UPDATE command
	// cannot affect row a second time". Mirrors the collector's
	// dedup_recommendations (event_handler.py:1963-1979) — keep the first
	// occurrence, drop later ones with the same conflict key.
	recs = dedupeByConflictKey(recs)

	if len(recs) == 0 {
		ctx.GetLogger().Info("scan_orchestrator: no recommendations to upsert (rules archived)",
			"scanner", scannerName, "account_id", account.AccountID)
		UpsertScheduleJobState(ctx, account, scannerName)
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
	UpsertScheduleJobState(ctx, account, scannerName)
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// dedupeByConflictKey returns recs with duplicates by the recommendation
// table's unique conflict tuple removed (first occurrence kept). The tuple is
// (rule_name, cloud_account_id, resource_id, category, account_object_id) —
// matches the unique index recommendation_cloud_account_id_rule_name_resource_id_category_.
func dedupeByConflictKey(recs []Recommendation) []Recommendation {
	type key struct {
		ruleName, accountID, resourceID, category, objectID string
	}
	seen := make(map[key]struct{}, len(recs))
	out := make([]Recommendation, 0, len(recs))
	for _, r := range recs {
		k := key{r.RuleName, r.CloudAccountID, r.ResourceID, r.Category, r.AccountObjectID}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	return out
}
