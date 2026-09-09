package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"

	"github.com/lib/pq"
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
	// StorageClasses refine the per-GB rate; a fetch failure (old agent, RBAC)
	// must not fail the scan — pricing degrades to provider defaults.
	storageClasses := map[string]map[string]any{}
	if scs, scErr := fetchK8sList(account.AccountID, "storageclasses", "storage.k8s.io", "v1", false); scErr != nil {
		logger.Warn("unused_pvc: list storageclasses failed, pricing falls back to provider defaults", "error", scErr)
	} else {
		for _, sc := range scs {
			if name := getStringField(getMapField(sc, "metadata"), "name"); name != "" {
				storageClasses[name] = sc
			}
		}
	}
	logger.Info("unused_pvc: fetched", "pods", len(pods), "pvcs", len(pvcs), "pvs", len(pvs), "storage_classes", len(storageClasses))

	unused := IdentifyUnusedPVs(pods, pvcs, pvs)
	recs, err := ParseUnusedPVs(unused, storageClasses, fetchK8sProvider(account), account)
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

// fetchK8sProvider returns the account's canonical cloud provider
// ("aws"/"gcp"/"azure") from agent telemetry, or "" — the same backstop rung
// the python producers' ladders use (get_k8s_provider), so both unused_pvc
// writers price a signal-less PV identically. Lookup failure degrades
// pricing, never the scan.
func fetchK8sProvider(account ScanAccount) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}
	var provider string
	if err := dbms.Db.Get(&provider,
		`SELECT k8s_provider FROM agent
		 WHERE cloud_account_id = $1
		   AND k8s_provider IS NOT NULL AND k8s_provider != ''
		 LIMIT 1`,
		account.AccountID,
	); err != nil {
		return ""
	}
	switch strings.ToLower(provider) {
	case "eks":
		return "aws"
	case "gke":
		return "gcp"
	case "aks":
		return "azure"
	}
	return strings.ToLower(provider)
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

	// Retire only the PVs that vanished from this scan, keyed on the scan's
	// keep-set rather than every row for the rule. A PV that is still present
	// must keep whatever status its user gave it, and the upsert's CASE guard
	// below can only preserve that if the archive has not already overwritten it.
	// Closed rows are left as a terminal record. A failed scan returns before this
	// point, so an empty keep-set really does mean no unused PVs remain.
	keepObjectIDs := make([]string, 0, len(recs))
	for _, r := range recs {
		keepObjectIDs = append(keepObjectIDs, r.AccountObjectID)
	}
	if _, err := dbms.Db.Exec(
		`UPDATE recommendation SET status = 'Archive', updated_at = $1
		 WHERE tenant_id = $2 AND cloud_account_id = $3
		   AND category = 'RightSizing' AND rule_name = $4
		   AND status NOT IN ('Archive', 'Closed')
		   AND NOT (account_object_id = ANY($5))`,
		now, account.TenantID, account.AccountID, UnusedPVCRuleName, pq.Array(keepObjectIDs),
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
		               status = CASE WHEN recommendation.status NOT IN ('Open', 'Archive')
		                             THEN recommendation.status ELSE EXCLUDED.status END,
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
