package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"

	"github.com/lib/pq"
)

// RunCertificateScan is the direct-K8s-API counterpart to RunScan: instead of
// scheduling a Job, it calls the agent's `get_resource` primitive to list
// cert-manager.io/v1/certificates cluster-wide, parses the response into
// Recommendation rows, and persists them under rule_name = "certificate_expiry".
//
// This shape exists because Robusta's certificate_scanner action wasn't
// Job-based — it ran in-process via the Python K8s client. Trying to model
// it as a Job would require packaging cert-manager's CLI in a container; way
// more friction than just leveraging the get_resource primitive that's
// already a light-action.
func RunCertificateScan(ctx *security.RequestContext, account ScanAccount) error {
	logger := ctx.GetLogger().With("scanner", "certificate_scanner", "account_id", account.AccountID, "tenant_id", account.TenantID)

	certs, err := fetchCertManagerCertificates(account.AccountID)
	if err != nil {
		return fmt.Errorf("certificate_scanner: fetch: %w", err)
	}
	logger.Info("certificate_scanner: fetched", "cert_count", len(certs))

	recs, err := ParseCertificates(certs, account, time.Now())
	if err != nil {
		return fmt.Errorf("certificate_scanner: parse: %w", err)
	}

	// Persist uses Persist's archive logic but the certificate scanner doesn't
	// have a ScannerCatalog entry (it isn't Job-based), so call the
	// archive-then-upsert path directly.
	return persistCertificates(ctx, account, recs)
}

// fetchCertManagerCertificates calls the agent's get_resource primitive for
// cert-manager.io/v1/certificates and unwraps the Robusta-shaped Finding
// response to a flat []map[string]any.
//
// Wire shape from the agent (kube/handlers.go::wrapAsFinding):
//
//	resp.data.findings[0].evidence[0].data → JSON-string
//	  → blocks[0].data (JSON-string)
//	    → []map[string]any (the actual K8s items array)
func fetchCertManagerCertificates(accountID string) ([]map[string]any, error) {
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  accountID,
			ActionName: "get_resource",
			ActionParams: map[string]any{
				"group":          "cert-manager.io",
				"version":        "v1",
				"resource_type":  "certificates",
				"all_namespaces": true,
			},
			Origin: "scan_orchestrator",
		},
		NoSinks:        true,
		Cache:          false,
		TimeoutSeconds: 60,
		AgentType:      "k8s",
	})
	if err != nil {
		return nil, err
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("get_resource: response missing data: %+v", resp)
	}
	findings, _ := data["findings"].([]any)
	if len(findings) == 0 {
		return nil, fmt.Errorf("get_resource: no findings (cert-manager CRD not installed?)")
	}
	finding, _ := findings[0].(map[string]any)
	evidence, _ := finding["evidence"].([]any)
	if len(evidence) == 0 {
		return nil, fmt.Errorf("get_resource: no evidence in finding")
	}
	ev, _ := evidence[0].(map[string]any)
	evDataStr, _ := ev["data"].(string)
	if evDataStr == "" {
		return nil, fmt.Errorf("get_resource: empty evidence data")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(evDataStr), &blocks); err != nil {
		return nil, fmt.Errorf("get_resource: parse blocks: %w", err)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("get_resource: no blocks")
	}
	itemsStr, _ := blocks[0]["data"].(string)
	if itemsStr == "" {
		return nil, fmt.Errorf("get_resource: empty items in block")
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(itemsStr), &items); err != nil {
		return nil, fmt.Errorf("get_resource: parse items: %w", err)
	}
	return items, nil
}

// persistCertificates archives the previous scan's certificate_expiry rows
// then UPSERTs the new ones. Mirrors Persist's two-step archive+upsert with
// the simpler shape — single (category, rule_name) tuple, no per-scanner
// catalog lookup, no dedup needed (one cert → one row).
func persistCertificates(ctx *security.RequestContext, account ScanAccount, recs []Recommendation) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("certificate_scanner: db: %w", err)
	}

	// Single timestamp for both the archive UPDATE and the INSERT/UPSERT so
	// row-update ordering is observable and consistent.
	now := time.Now()

	// Retire only the certs that vanished from this scan, keyed on the scan's
	// keep-set rather than every row for the rule. A cert that is still present
	// must keep whatever status its user gave it, and the upsert's CASE guard
	// below can only preserve that if the archive has not already overwritten it.
	// Closed rows are left as a terminal record. A fetch or parse failure returns
	// before this point, so an empty keep-set really does mean no certs remain.
	keepObjectIDs := make([]string, 0, len(recs))
	for _, r := range recs {
		keepObjectIDs = append(keepObjectIDs, r.AccountObjectID)
	}
	if _, err := dbms.Db.Exec(
		`UPDATE recommendation SET status = 'Archive', updated_at = $1
		 WHERE tenant_id = $2 AND cloud_account_id = $3
		   AND category = 'Configuration' AND rule_name = $4
		   AND status NOT IN ('Archive', 'Closed')
		   AND NOT (account_object_id = ANY($5))`,
		now, account.TenantID, account.AccountID, CertificateExpiryRuleName, pq.Array(keepObjectIDs),
	); err != nil {
		return fmt.Errorf("certificate_scanner: archive: %w", err)
	}

	if len(recs) == 0 {
		ctx.GetLogger().Info("certificate_scanner: no certificates with status.not_after to upsert (rules archived)",
			"account_id", account.AccountID)
		UpsertScheduleJobState(ctx, account, "certificate_scanner")
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
			"estimated_savings":      0,
			"recommendation_action":  r.RecommendationAction,
			"resource_id":            nullIfEmpty(r.ResourceID),
			"account_object_id":      r.AccountObjectID,
			"updated_at":             now,
			"finops_score":           0,
			"finops_band":            "",
			"finops_score_breakdown": "{}",
		})
	}

	if _, err := dbms.Db.NamedExec(
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
		return fmt.Errorf("certificate_scanner: upsert: %w", err)
	}
	ctx.GetLogger().Info("certificate_scanner: persisted",
		"account_id", account.AccountID, "rows", len(rows))
	UpsertScheduleJobState(ctx, account, "certificate_scanner")
	return nil
}
