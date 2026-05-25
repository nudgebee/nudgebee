package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"
)

// RunK8sVersionUpgrade is the direct-K8s-API counterpart to the Job-based
// scanners for k8s_version_upgrade. Mirrors Robusta's k8s_version_upgrade
// playbook (robusta/playbooks/nudgebee_playbooks/k8s_version_upgrade.py) —
// the playbook ran in-process via the Python K8s client and we couldn't
// model it as a Job without bundling a CLI in a container.
//
// Two get_resource calls:
//
//  1. kube-system pods → find the kube-proxy container, parse version
//  2. nodes (any node) → status.node_info.kubelet_version as a proxy for
//     the control plane version (the K8s /version endpoint isn't exposed
//     via get_resource)
//
// Then ParseKubeProxyVersionRecommendations runs the version-skew math
// (matches the collector's upgrade_handler.py:467-601) and emits a row
// under rule_name = "kube_proxy_version" if there's any skew. Same gating
// as the other direct-API path: caller must check
// IsEnabledForTenant(SERVER_ORCHESTRATED_SCANNERS) first.
//
// Out of scope (collector also handled these but Robusta's playbook
// already moved them server-side and they're not version-skew checks):
//   - K8S_API_DELETED / K8S_API_DEPRECATED — covered by the
//     k8s_helm_compatibility flow, separate scope.
//   - EKS_CLUSTER_UPGRADE / EKS_ADD_ONS_VERSION — EKS-only, separate scope.
func RunK8sVersionUpgrade(ctx *security.RequestContext, account ScanAccount) error {
	logger := ctx.GetLogger().With("scanner", "k8s_version_upgrade", "account_id", account.AccountID, "tenant_id", account.TenantID)

	pods, err := fetchKubeSystemPods(account.AccountID)
	if err != nil {
		return fmt.Errorf("k8s_version_upgrade: fetch kube-system pods: %w", err)
	}
	kpVersion := extractKubeProxyVersion(pods)
	if kpVersion == "" {
		// No kube-proxy in the cluster (e.g. GKE Dataplane V2). Archive any
		// previous Open rows so a stale recommendation doesn't linger if the
		// cluster recently moved off kube-proxy.
		logger.Info("k8s_version_upgrade: no kube-proxy pod found; archiving previous recommendations")
		return persistKubeProxyVersion(ctx, account, nil)
	}

	nodes, err := fetchClusterNodes(account.AccountID)
	if err != nil {
		return fmt.Errorf("k8s_version_upgrade: fetch nodes: %w", err)
	}
	kubeletVersion := extractKubeletVersion(nodes)
	if kubeletVersion == "" {
		logger.Warn("k8s_version_upgrade: no node with kubelet_version found; skipping")
		return nil
	}

	logger.Info("k8s_version_upgrade: versions extracted", "kube_proxy_version", kpVersion, "kubelet_version", kubeletVersion)

	recs, err := ParseKubeProxyVersionRecommendations(kpVersion, kubeletVersion, account)
	if err != nil {
		return fmt.Errorf("k8s_version_upgrade: parse: %w", err)
	}
	return persistKubeProxyVersion(ctx, account, recs)
}

// fetchKubeSystemPods retrieves all pods in the kube-system namespace via
// the agent's get_resource primitive. The agent doesn't support label
// selectors so we filter for kube-proxy client-side in extractKubeProxyVersion.
func fetchKubeSystemPods(accountID string) ([]map[string]any, error) {
	return fetchResourceList(accountID, map[string]any{
		"version":       "v1",
		"resource_type": "pods",
		"namespace":     "kube-system",
	})
}

// fetchClusterNodes lists all Nodes in the cluster.
func fetchClusterNodes(accountID string) ([]map[string]any, error) {
	return fetchResourceList(accountID, map[string]any{
		"version":       "v1",
		"resource_type": "nodes",
	})
}

// fetchResourceList wraps a relay.Execute("get_resource", params) call and
// unwraps the agent's Robusta-shaped Finding response down to the flat
// []map[string]any items. Same wire shape as fetchCertManagerCertificates
// — duplicated here rather than generalised because the unwrap is small
// and a shared helper would obscure where each scanner reads its data.
func fetchResourceList(accountID string, actionParams map[string]any) ([]map[string]any, error) {
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    accountID,
			ActionName:   "get_resource",
			ActionParams: actionParams,
			Origin:       "scan_orchestrator",
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
		return nil, fmt.Errorf("get_resource: response missing data: %+v", resp)
	}
	findings, ok := data["findings"].([]any)
	if !ok || len(findings) == 0 {
		return nil, fmt.Errorf("get_resource: no findings in response (resource may be missing or RBAC denies)")
	}
	finding, ok := findings[0].(map[string]any)
	if !ok || finding == nil {
		return nil, fmt.Errorf("get_resource: finding[0] is not an object")
	}
	evidence, ok := finding["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		return nil, fmt.Errorf("get_resource: no evidence in finding")
	}
	ev, ok := evidence[0].(map[string]any)
	if !ok || ev == nil {
		return nil, fmt.Errorf("get_resource: evidence[0] is not an object")
	}
	evDataStr, _ := ev["data"].(string)
	if evDataStr == "" {
		return nil, fmt.Errorf("get_resource: empty evidence data")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(evDataStr), &blocks); err != nil {
		return nil, fmt.Errorf("get_resource: parse blocks: %w", err)
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	itemsStr, _ := blocks[0]["data"].(string)
	if itemsStr == "" {
		return nil, nil
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(itemsStr), &items); err != nil {
		return nil, fmt.Errorf("get_resource: parse items: %w", err)
	}
	return items, nil
}

// persistKubeProxyVersion archives previous Open rows then UPSERTs the new
// ones (typically 0 or 1 row — kube_proxy_version emits at most one finding
// per account). Same shape as persistCertificates.
func persistKubeProxyVersion(ctx *security.RequestContext, account ScanAccount, recs []Recommendation) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("k8s_version_upgrade: db: %w", err)
	}

	// Single timestamp for both the archive UPDATE and the INSERT/UPSERT so
	// row-update ordering is observable and consistent.
	now := time.Now()

	if _, err := dbms.Db.Exec(
		`UPDATE recommendation SET status = 'Archive', updated_at = $1
		 WHERE tenant_id = $2 AND cloud_account_id = $3
		   AND category = 'InfraUpgrade' AND rule_name = $4 AND status != 'Archive'`,
		now, account.TenantID, account.AccountID, KubeProxyVersionRuleName,
	); err != nil {
		return fmt.Errorf("k8s_version_upgrade: archive: %w", err)
	}

	if len(recs) == 0 {
		ctx.GetLogger().Info("k8s_version_upgrade: no kube-proxy version skew to upsert (rules archived)",
			"account_id", account.AccountID)
		UpsertScheduleJobState(ctx, account, "k8s_version_upgrade")
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

	// ON CONFLICT update mirrors the recommendation table's full updatable set
	// the same way Persist + recommendation.upsertRecommendationData do — keeping
	// estimated_savings/finops_* in sync here matters for the (rare) case where
	// the same row's severity changes between scans.
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
		               recommendation_action = EXCLUDED.recommendation_action,
		               estimated_savings = EXCLUDED.estimated_savings,
		               finops_score = EXCLUDED.finops_score,
		               finops_band = EXCLUDED.finops_band,
		               finops_score_breakdown = EXCLUDED.finops_score_breakdown`,
		rows,
	); err != nil {
		return fmt.Errorf("k8s_version_upgrade: upsert: %w", err)
	}
	ctx.GetLogger().Info("k8s_version_upgrade: persisted",
		"account_id", account.AccountID, "rows", len(rows))
	UpsertScheduleJobState(ctx, account, "k8s_version_upgrade")
	return nil
}
