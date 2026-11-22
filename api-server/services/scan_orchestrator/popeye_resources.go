package scan_orchestrator

import (
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// popeyeWorkloadKinds are the Popeye kinds that k8s discovery stores in
// cloud_resourses under the same "namespace/Kind/name" key the parser writes to
// account_object_id.
var popeyeWorkloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"ReplicaSet":  true,
	"Job":         true,
	"CronJob":     true,
}

// linkPopeyeResources sets ResourceID on Popeye findings about a workload, a
// pod (linked to its owning workload) or a node, then moves each existing row
// onto the resolved resource_id.
//
// Only active resources are linked: the inactive-resource sweep archives Open
// rows of inactive resources, and Popeye keeps reporting some of them (a
// StatefulSet scaled to zero), so linking them would reopen and re-archive the
// finding every scan.
func linkPopeyeResources(tx *sqlx.Tx, account ScanAccount, recs []Recommendation) error {
	resolved, err := resolvePopeyeResourceIDs(tx, account, recs)
	if err != nil {
		return err
	}
	for i := range recs {
		recs[i].ResourceID = resolved[recs[i].AccountObjectID]
	}
	return adoptExistingRecommendationRows(tx, account, recs)
}

// resolvePopeyeResourceIDs maps account_object_id to the linked resource id.
func resolvePopeyeResourceIDs(tx *sqlx.Tx, account ScanAccount, recs []Recommendation) (map[string]string, error) {
	var workloadKeys, podKeys, nodeNames []string
	nodeKeyByName := map[string]string{}
	for _, r := range recs {
		parts := strings.SplitN(r.AccountObjectID, "/", 3)
		if len(parts) != 3 {
			continue
		}
		switch kind := parts[1]; {
		case popeyeWorkloadKinds[kind]:
			workloadKeys = append(workloadKeys, r.AccountObjectID)
		case kind == "Pod":
			podKeys = append(podKeys, r.AccountObjectID)
		case kind == "Node":
			nodeNames = append(nodeNames, parts[2])
			nodeKeyByName[parts[2]] = r.AccountObjectID
		}
	}

	resolved := map[string]string{}
	type row struct {
		Key string `db:"key"`
		ID  string `db:"id"`
	}
	var rows []row

	if len(workloadKeys) > 0 || len(nodeNames) > 0 {
		if err := tx.Select(&rows,
			`SELECT cr.resourse_id AS key, cr.id::text AS id
			 FROM cloud_resourses cr
			 WHERE cr.account = $1 AND cr.tenant = $2 AND cr.service_name = 'kubernetes'
			   AND cr.is_active IS NOT FALSE
			   AND ((cr.type <> 'node' AND cr.resourse_id = ANY($3)) OR (cr.type = 'node' AND cr.resourse_id = ANY($4)))`,
			account.AccountID, account.TenantID, pq.Array(workloadKeys), pq.Array(nodeNames),
		); err != nil {
			return nil, fmt.Errorf("resolve popeye workload/node resources: %w", err)
		}
		for _, r := range rows {
			if objectID, ok := nodeKeyByName[r.Key]; ok {
				resolved[objectID] = r.ID
			} else {
				resolved[r.Key] = r.ID
			}
		}
	}

	if len(podKeys) > 0 {
		rows = rows[:0]
		// A recreated pod keeps its name under a new uid, so one external_id can
		// have several k8s_pods rows; prefer the live one.
		if err := tx.Select(&rows,
			`SELECT DISTINCT ON (kp.external_id) kp.external_id AS key, kw.cloud_resource_id::text AS id
			 FROM k8s_pods kp
			 JOIN k8s_workloads kw ON kw.tenant_id = kp.tenant_id AND kw.cloud_account_id = kp.cloud_account_id
			   AND kw.namespace = kp.namespace AND kw.name = kp.workload_name AND kw.kind = kp.workload_type
			 JOIN cloud_resourses cr ON cr.id = kw.cloud_resource_id AND cr.is_active IS NOT FALSE
			 WHERE kp.tenant_id = $1 AND kp.cloud_account_id = $2 AND kp.external_id = ANY($3)
			 ORDER BY kp.external_id, kp.is_active DESC, kp.last_seen DESC`,
			account.TenantID, account.AccountID, pq.Array(podKeys),
		); err != nil {
			return nil, fmt.Errorf("resolve popeye pod workloads: %w", err)
		}
		for _, r := range rows {
			resolved[r.Key] = r.ID
		}
	}
	return resolved, nil
}

// adoptExistingRecommendationRows moves each finding's existing row onto the
// resource_id the scan resolved, so the upsert lands on it instead of inserting
// a twin. resource_id is part of the recommendation unique key, and a twin would
// leave the user's Dismissed/InProgress/Closed row behind while the finding
// reappears Open. When several rows share a finding, the one carrying a user
// decision is moved; a row already holding the target key is left alone.
func adoptExistingRecommendationRows(tx *sqlx.Tx, account ScanAccount, recs []Recommendation) error {
	ruleNames := make([]string, 0, len(recs))
	objectIDs := make([]string, 0, len(recs))
	resourceIDs := make([]string, 0, len(recs))
	for _, r := range recs {
		ruleNames = append(ruleNames, r.RuleName)
		objectIDs = append(objectIDs, r.AccountObjectID)
		resourceIDs = append(resourceIDs, r.ResourceID)
	}
	_, err := tx.Exec(
		`WITH v AS (
		   SELECT rule_name, account_object_id, NULLIF(resource_id, '')::uuid AS resource_id
		   FROM unnest($3::text[], $4::text[], $5::text[]) AS v(rule_name, account_object_id, resource_id)
		 ), moves AS (
		   SELECT DISTINCT ON (r.rule_name, r.account_object_id) r.id, v.resource_id
		   FROM recommendation r
		   JOIN v ON v.rule_name = r.rule_name AND v.account_object_id = r.account_object_id
		   WHERE r.tenant_id = $1 AND r.cloud_account_id = $2 AND r.category = 'Configuration'
		     AND r.resource_id IS DISTINCT FROM v.resource_id
		     AND NOT EXISTS (
		       SELECT 1 FROM recommendation t
		       WHERE t.tenant_id = r.tenant_id AND t.cloud_account_id = r.cloud_account_id AND t.rule_name = r.rule_name
		         AND t.category = r.category AND t.account_object_id = r.account_object_id
		         AND t.resource_id IS NOT DISTINCT FROM v.resource_id)
		   ORDER BY r.rule_name, r.account_object_id, r.status IN ('Open', 'Archive'), r.updated_at DESC
		 )
		 UPDATE recommendation r SET resource_id = moves.resource_id
		 FROM moves WHERE r.id = moves.id`,
		account.TenantID, account.AccountID, pq.Array(ruleNames), pq.Array(objectIDs), pq.Array(resourceIDs),
	)
	if err != nil {
		return fmt.Errorf("adopt existing popeye rows: %w", err)
	}
	return nil
}
