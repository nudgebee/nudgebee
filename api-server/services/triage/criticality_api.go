package triage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// WorkloadCriticalityListItem is one row of the Service Criticality review screen: an active workload
// with its resolved criticality. Workloads with no persisted row resolve to source='default'
// criticality='medium' (the scoring default), so the screen always shows the full inventory.
type WorkloadCriticalityListItem struct {
	CloudResourceID string  `db:"cloud_resource_id" json:"cloud_resource_id"`
	Namespace       string  `db:"namespace" json:"namespace"`
	Name            string  `db:"name" json:"name"`
	Kind            string  `db:"kind" json:"kind"`
	Criticality     string  `db:"criticality" json:"criticality"`
	Source          string  `db:"source" json:"source"` // user | fact_signal | llm_inferred | default
	Confidence      float64 `db:"confidence" json:"confidence"`
	Rationale       string  `db:"rationale" json:"rationale"`
	IsUserOverride  bool    `db:"is_user_override" json:"is_user_override"`
}

// ListWorkloadCriticalityRequest filters the review-screen inventory.
type ListWorkloadCriticalityRequest struct {
	CloudAccountID string
	Namespace      string // optional exact-match filter
	TieredOnly     bool   // only rows with a persisted (non-default) criticality
	Limit          int
	Offset         int
}

// isValidCriticality reports whether s is one of the four named levels.
func isValidCriticality(s string) bool {
	switch s {
	case CriticalityCritical, CriticalityHigh, CriticalityMedium, CriticalityLow:
		return true
	}
	return false
}

// ListWorkloadCriticality returns the full workload inventory for an account joined to their resolved
// criticality (defaulting to medium/default), most-critical first. Returns the page plus the total
// matching count for pagination.
func ListWorkloadCriticality(ctx context.Context, db *sqlx.DB, req ListWorkloadCriticalityRequest) ([]WorkloadCriticalityListItem, int, error) {
	if req.CloudAccountID == "" {
		return nil, 0, fmt.Errorf("cloud_account_id is required")
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	// Shared FROM/WHERE so the count and the page stay in lockstep.
	where := `
		FROM k8s_workloads w
		LEFT JOIN workload_criticality c
		  ON c.cloud_account_id = w.cloud_account_id
		 AND c.cloud_resource_id = w.cloud_resource_id
		WHERE w.is_active AND w.cloud_account_id = $1
		  AND ($2 = '' OR w.namespace = $2)
		  AND (NOT $3 OR c.id IS NOT NULL)`

	var total int
	if err := db.GetContext(ctx, &total, `SELECT count(*) `+where, req.CloudAccountID, req.Namespace, req.TieredOnly); err != nil {
		return nil, 0, err
	}

	items := []WorkloadCriticalityListItem{}
	query := `
		SELECT
			w.cloud_resource_id::text AS cloud_resource_id,
			w.namespace, w.name, w.kind,
			COALESCE(c.criticality, 'medium')     AS criticality,
			COALESCE(c.source, 'default')         AS source,
			COALESCE(c.confidence, 0)             AS confidence,
			COALESCE(c.rationale, '')             AS rationale,
			COALESCE(c.is_user_override, false)   AS is_user_override ` + where + `
		ORDER BY w.namespace,
		         CASE COALESCE(c.criticality, 'medium')
		           WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
		         w.name
		LIMIT $4 OFFSET $5`
	if err := db.SelectContext(ctx, &items, query, req.CloudAccountID, req.Namespace, req.TieredOnly, limit, req.Offset); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UpsertUserCriticality records an operator's explicit criticality for a workload. User rows are
// authoritative and sticky: source='user', is_user_override=true, and the nightly discovery sweep
// never overwrites them.
func UpsertUserCriticality(ctx context.Context, db *sqlx.DB, tenant, account, cloudResourceID, namespace, criticality, userID string) error {
	if account == "" || cloudResourceID == "" {
		return fmt.Errorf("cloud_account_id and cloud_resource_id are required")
	}
	if !isValidCriticality(criticality) {
		return fmt.Errorf("invalid criticality %q (want critical|high|medium|low)", criticality)
	}
	resourceID, err := uuid.Parse(cloudResourceID)
	if err != nil {
		return fmt.Errorf("invalid cloud_resource_id: %w", err)
	}
	var updatedBy interface{}
	if userID != "" {
		updatedBy = userID
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO workload_criticality
		    (tenant_id, cloud_account_id, cloud_resource_id, namespace,
		     criticality, source, confidence, rationale, status, is_user_override, updated_by)
		VALUES ($1, $2, $3, $4, $5, 'user', 1.0, 'operator-declared', 'active', true, $6)
		ON CONFLICT (cloud_account_id, cloud_resource_id)
		DO UPDATE SET
		    criticality      = EXCLUDED.criticality,
		    source           = 'user',
		    is_user_override = true,
		    confidence       = 1.0,
		    rationale        = 'operator-declared',
		    updated_by       = EXCLUDED.updated_by,
		    namespace        = EXCLUDED.namespace,
		    updated_at       = now()`,
		tenant, account, resourceID, namespace, criticality, updatedBy)
	return err
}

// ResetWorkloadCriticality removes an operator override, letting the workload revert to the derived
// criticality on the next discovery sweep (and to the medium default until then).
func ResetWorkloadCriticality(ctx context.Context, db *sqlx.DB, account, cloudResourceID string) error {
	if account == "" || cloudResourceID == "" {
		return fmt.Errorf("cloud_account_id and cloud_resource_id are required")
	}
	resourceID, err := uuid.Parse(cloudResourceID)
	if err != nil {
		return fmt.Errorf("invalid cloud_resource_id: %w", err)
	}
	// Compare against the uuid column directly (no ::text cast) so the delete uses the index.
	_, err = db.ExecContext(ctx, `
		DELETE FROM workload_criticality
		WHERE cloud_account_id = $1 AND cloud_resource_id = $2`,
		account, resourceID)
	return err
}
