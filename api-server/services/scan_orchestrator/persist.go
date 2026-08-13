package scan_orchestrator

import (
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
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

	// Every archive/upsert below is scoped by tenant_id + cloud_account_id. Fail
	// fast on empty identifiers rather than run queries with blank scoping keys —
	// a bad upstream caller shouldn't reach the DB with an empty tenant/account.
	if account.TenantID == "" || account.AccountID == "" {
		return fmt.Errorf("scan_orchestrator.Persist: tenant_id and account_id are required (scanner=%s)", scannerName)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("scan_orchestrator.Persist: db: %w", err)
	}

	// Dedupe by conflict tuple before opening the transaction — pure, no DB
	// dependency. Some scanners (notably trivy_cis) emit the same (check,
	// class, target) finding multiple times in one report; the UPSERT below
	// would otherwise fail with "ON CONFLICT DO UPDATE command cannot affect
	// row a second time". Mirrors the collector's dedup_recommendations
	// (event_handler.py:1963-1979) — keep the first occurrence, drop later
	// ones with the same conflict key.
	recs = dedupeByConflictKey(recs)

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if err := persistArchive(tx, ctx, account, scannerName, scanner, recs); err != nil {
			return nil, err
		}

		if len(recs) == 0 {
			ctx.GetLogger().Info("scan_orchestrator: no recommendations to upsert (rules archived)",
				"scanner", scannerName, "account_id", account.AccountID)
			return nil, nil
		}

		vulnIDs, err := models.UpsertVulnerabilities(tx, collectVulnerabilityRows(recs))
		if err != nil {
			return nil, fmt.Errorf("upsert vulnerabilities: %w", err)
		}

		if err := persistUpsertRecommendations(tx, recs, vulnIDs); err != nil {
			if pgErr, ok := err.(*pq.Error); ok {
				ctx.GetLogger().Error("scan_orchestrator: upsert pg error",
					"scanner", scannerName, "constraint", pgErr.Constraint, "detail", pgErr.Detail, "error", err)
			}
			return nil, fmt.Errorf("upsert recommendations: %w", err)
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	if len(recs) > 0 {
		ctx.GetLogger().Info("scan_orchestrator: persisted",
			"scanner", scannerName, "account_id", account.AccountID, "rows", len(recs))
	}
	UpsertScheduleJobState(ctx, account, scannerName)
	return nil
}

// persistArchive runs the archive step (see the scanner-shape comment on
// Persist above), scoped by tx so it commits/rolls back with the upsert.
func persistArchive(tx *sqlx.Tx, ctx *security.RequestContext, account ScanAccount, scannerName string, scanner Scanner, recs []Recommendation) error {
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
	//
	// Every archive below tombstones status = 'Open' rows ONLY. Open is the sole
	// scanner-owned live state; the archive must never overwrite a user-owned
	// state (Dismissed/snoozed, InProgress, Closed). A rescan of a still-present
	// finding resurrects Open below via the upsert; a user decision must survive
	// it. Pairs with the CASE guard on the upsert's status column.
	switch scannerName {
	case "popeye_scan":
		_, err := tx.Exec(
			`UPDATE recommendation SET status = 'Archive', updated_at = $1
			 WHERE tenant_id = $2 AND cloud_account_id = $3 AND category = 'Configuration'
			   AND rule_name LIKE '%_misconfigurations' AND status = 'Open'`,
			time.Now(), account.TenantID, account.AccountID,
		)
		if err != nil {
			ctx.GetLogger().Error("scan_orchestrator: popeye archive failed",
				"account_id", account.AccountID, "error", err)
			return fmt.Errorf("archive popeye misconfigurations: %w", err)
		}
	case "image_scanner":
		// image_scanner runs once PER IMAGE — archiving the whole (Security,
		// image_scan) rule (the generic branch below) would flip every OTHER
		// image's open rows to Archive on each call, leaving only the last image
		// scanned in a cycle visible in the UI. Scope the archive to THIS image,
		// keyed on the image_name the parser embeds. account.TargetImage is the
		// image being scanned; rides idx_recommendation_security_account_image_name.
		// Empty TargetImage matches no rows (image_name is never "") — a safe no-op.
		_, err := tx.Exec(
			`UPDATE recommendation SET status = 'Archive', updated_at = $1
			 WHERE tenant_id = $2 AND cloud_account_id = $3 AND category = 'Security'
			   AND rule_name = $4 AND recommendation->>'image_name' = $5 AND status = 'Open'`,
			time.Now(), account.TenantID, account.AccountID, scanner.RuleName, account.TargetImage,
		)
		if err != nil {
			ctx.GetLogger().Error("scan_orchestrator: image_scan archive failed",
				"account_id", account.AccountID, "image", account.TargetImage, "error", err)
			return fmt.Errorf("archive image_scan for %q: %w", account.TargetImage, err)
		}
	default:
		type archiveKey struct{ category, ruleName string }
		archiveKeys := map[archiveKey]struct{}{
			{category: scanner.RuleName, ruleName: scanner.RuleName}: {},
		}
		for _, r := range recs {
			archiveKeys[archiveKey{category: r.Category, ruleName: r.RuleName}] = struct{}{}
		}
		for k := range archiveKeys {
			_, err := tx.Exec(
				`UPDATE recommendation SET status = 'Archive', updated_at = $1
				 WHERE tenant_id = $2 AND cloud_account_id = $3 AND category = $4 AND rule_name = $5 AND status = 'Open'`,
				time.Now(), account.TenantID, account.AccountID, k.category, k.ruleName,
			)
			if err != nil {
				ctx.GetLogger().Error("scan_orchestrator: archive failed",
					"scanner", scannerName, "category", k.category, "rule_name", k.ruleName, "error", err)
				return fmt.Errorf("archive existing %s/%s: %w", k.category, k.ruleName, err)
			}
		}
	}
	return nil
}

// collectVulnerabilityRows extracts the non-nil Vulnerability from each rec,
// for image_scanner's rows (every other scanner leaves it nil).
func collectVulnerabilityRows(recs []Recommendation) []models.Vulnerability {
	vulnRows := make([]models.Vulnerability, 0, len(recs))
	for _, r := range recs {
		if r.Vulnerability != nil {
			vulnRows = append(vulnRows, *r.Vulnerability)
		}
	}
	return vulnRows
}

// persistUpsertRecommendations UPSERTs recs in a single batch. Mirrors
// recommendation.upsertRecommendationData (k8s_recommendation_service.go:43-61);
// the on-conflict tuple is the recommendation table's unique index on
// (rule_name, cloud_account_id, resource_id, category, account_object_id).
//
// The status column is guarded by a CASE (below): a re-scan may move a row
// between the scanner-owned Open/Archive states, but never OUT of a
// user-owned state (Dismissed/snoozed, InProgress, Closed). Without it a
// rescan reopened a finding the user had dismissed. Matches the legacy
// collector's upsert (event_handler.py upsert_recommendations).
//
// vulnIDs maps models.VulnerabilityKey(...) to the upserted vulnerabilities
// row's id; rows whose Vulnerability is nil (every non-image_scanner rec) get
// vulnerability_id = NULL.
func persistUpsertRecommendations(tx *sqlx.Tx, recs []Recommendation, vulnIDs map[string]string) error {
	now := time.Now()
	rows := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		var vulnerabilityID any
		if r.Vulnerability != nil {
			key := models.VulnerabilityKey(r.Vulnerability.Source, r.Vulnerability.VulnId,
				r.Vulnerability.PackageName, r.Vulnerability.PackageArch)
			if id, ok := vulnIDs[key]; ok {
				vulnerabilityID = id
			}
		}
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
			"vulnerability_id":       vulnerabilityID,
		}
		// finops_score / finops_band / finops_score_breakdown are inserted as zero
		// values and filled by the 6h score-recompute cron — computing them inline
		// would create an import cycle with services/recommendation. They are NOT
		// in the ON CONFLICT update below: overwriting on re-scan wiped real
		// scores back to zero until the next cron pass.
		rows = append(rows, row)
	}

	_, err := tx.NamedExec(
		`INSERT INTO recommendation
		   (status, tenant_id, cloud_account_id, recommendation, severity, category, rule_name,
		    estimated_savings, recommendation_action, resource_id, account_object_id, updated_at,
		    finops_score, finops_band, finops_score_breakdown, vulnerability_id)
		 VALUES
		   (:status, :tenant_id, :cloud_account_id, :recommendation, :severity, :category, :rule_name,
		    :estimated_savings, :recommendation_action, :resource_id, :account_object_id, :updated_at,
		    :finops_score, :finops_band, :finops_score_breakdown, :vulnerability_id)
		 ON CONFLICT (rule_name, cloud_account_id, resource_id, category, account_object_id)
		 DO UPDATE SET recommendation = EXCLUDED.recommendation,
		               status = CASE WHEN recommendation.status NOT IN ('Open', 'Archive')
		                             THEN recommendation.status ELSE EXCLUDED.status END,
		               updated_at = EXCLUDED.updated_at,
		               estimated_savings = EXCLUDED.estimated_savings,
		               severity = EXCLUDED.severity,
		               recommendation_action = EXCLUDED.recommendation_action,
		               vulnerability_id = EXCLUDED.vulnerability_id`,
		rows,
	)
	return err
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
