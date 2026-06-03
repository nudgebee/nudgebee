package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"
)

// RunUnusedPVCScan is the direct-K8s-API counterpart that lists PVs, PVCs,
// and Pods cluster-wide, computes the set of PVs not actively serving any
// running pod, and persists them as `unused_pvc` recommendations.
//
// Mirrors the legacy `unused_pv` action that lived in the Robusta agent
// (robusta/playbooks/nudgebee_playbooks/unused_pv.py) — moved server-side
// so the canary go-agent install doesn't need a dedicated enricher. Three
// get_resource calls instead of a Job because the underlying scan is
// purely a read-cross-product over K8s API state.
func RunUnusedPVCScan(ctx *security.RequestContext, account ScanAccount) error {
	logger := ctx.GetLogger().With("scanner", "unused_pvc", "account_id", account.AccountID, "tenant_id", account.TenantID)

	pods, err := fetchK8sList(account.AccountID, "pods", "", "v1", true)
	if err != nil {
		return fmt.Errorf("unused_pvc: list pods: %w", err)
	}
	pvcs, err := fetchK8sList(account.AccountID, "persistentvolumeclaims", "", "v1", true)
	if err != nil {
		return fmt.Errorf("unused_pvc: list pvcs: %w", err)
	}
	pvs, err := fetchK8sList(account.AccountID, "persistentvolumes", "", "v1", false)
	if err != nil {
		return fmt.Errorf("unused_pvc: list pvs: %w", err)
	}
	logger.Info("unused_pvc: fetched", "pods", len(pods), "pvcs", len(pvcs), "pvs", len(pvs))

	unused := IdentifyUnusedPVs(pods, pvcs, pvs)
	recs, err := ParseUnusedPVs(unused, account)
	if err != nil {
		return fmt.Errorf("unused_pvc: parse: %w", err)
	}
	logger.Info("unused_pvc: identified", "unused_pv_count", len(unused), "recommendation_count", len(recs))

	return persistUnusedPVCs(ctx, account, recs)
}

// fetchK8sList wraps the agent's get_resource and unwraps the
// Robusta-shaped Finding response. Same envelope traversal as
// fetchCertManagerCertificates — kept inlined here (per-scanner) so each
// scan file is self-contained and its panic blast radius is limited.
//
// `allNamespaces=true` for namespaced kinds (pods, pvcs);
// `allNamespaces=false` for cluster-scoped (persistentvolumes) — the agent
// rejects all_namespaces=true on cluster-scoped resources.
func fetchK8sList(accountID, resourceType, group, version string, allNamespaces bool) ([]map[string]any, error) {
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  accountID,
			ActionName: "get_resource",
			ActionParams: map[string]any{
				"group":          group,
				"version":        version,
				"resource_type":  resourceType,
				"all_namespaces": allNamespaces,
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
	if !ok || data == nil {
		return nil, fmt.Errorf("get_resource %s: response missing data: %+v", resourceType, resp)
	}
	findings, ok := data["findings"].([]any)
	if !ok || len(findings) == 0 {
		return nil, fmt.Errorf("get_resource %s: no findings", resourceType)
	}
	finding, ok := findings[0].(map[string]any)
	if !ok || finding == nil {
		return nil, fmt.Errorf("get_resource %s: invalid finding shape", resourceType)
	}
	evidence, ok := finding["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		return nil, fmt.Errorf("get_resource %s: no evidence", resourceType)
	}
	ev, ok := evidence[0].(map[string]any)
	if !ok || ev == nil {
		return nil, fmt.Errorf("get_resource %s: invalid evidence shape", resourceType)
	}
	evDataStr, ok := ev["data"].(string)
	if !ok || evDataStr == "" {
		return nil, fmt.Errorf("get_resource %s: empty evidence data", resourceType)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(evDataStr), &blocks); err != nil {
		return nil, fmt.Errorf("get_resource %s: parse blocks: %w", resourceType, err)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("get_resource %s: no blocks", resourceType)
	}
	block := blocks[0]
	if block == nil {
		return nil, fmt.Errorf("get_resource %s: invalid block shape", resourceType)
	}
	itemsStr, ok := block["data"].(string)
	if !ok || itemsStr == "" {
		// Empty list case — return [] not an error. Some agents emit an empty
		// string when the result set is genuinely zero items (e.g. no PVs in
		// the cluster); the parser should treat this as "scan succeeded, nothing
		// to do" rather than a fetch error.
		return []map[string]any{}, nil
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(itemsStr), &items); err != nil {
		return nil, fmt.Errorf("get_resource %s: parse items: %w", resourceType, err)
	}
	return items, nil
}

// persistUnusedPVCs archives the previous scan's unused_pvc rows then
// UPSERTs the new ones. Same archive-then-upsert two-step as
// persistCertificates; only difference is the savings column is real
// (cert scan hardcodes 0).
func persistUnusedPVCs(ctx *security.RequestContext, account ScanAccount, recs []Recommendation) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("unused_pvc: db: %w", err)
	}

	now := time.Now()

	// Archive — transition all currently-open unused_pvc rows for this
	// account to Archive so dropped PVs disappear from the UI.
	if _, err := dbms.Db.Exec(
		`UPDATE recommendation SET status = 'Archive', updated_at = $1
		 WHERE tenant_id = $2 AND cloud_account_id = $3
		   AND category = 'RightSizing' AND rule_name = $4 AND status != 'Archive'`,
		now, account.TenantID, account.AccountID, UnusedPVCRuleName,
	); err != nil {
		return fmt.Errorf("unused_pvc: archive: %w", err)
	}

	if len(recs) == 0 {
		ctx.GetLogger().Info("unused_pvc: no unused PVs to upsert (rows archived)",
			"account_id", account.AccountID)
		UpsertScheduleJobState(ctx, account, "unused_pv")
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
		               status = EXCLUDED.status,
		               updated_at = EXCLUDED.updated_at,
		               severity = EXCLUDED.severity,
		               estimated_savings = EXCLUDED.estimated_savings,
		               recommendation_action = EXCLUDED.recommendation_action`,
		rows,
	); err != nil {
		return fmt.Errorf("unused_pvc: upsert: %w", err)
	}
	ctx.GetLogger().Info("unused_pvc: persisted",
		"account_id", account.AccountID, "rows", len(rows))
	UpsertScheduleJobState(ctx, account, "unused_pv")
	return nil
}
