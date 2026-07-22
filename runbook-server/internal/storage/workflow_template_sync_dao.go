package storage

import (
	"context"
	"fmt"
	"time"

	"nudgebee/runbook/internal/model"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// SystemTemplateInput is one template to upsert as a system (tenant/account-less)
// workflow template. Definition/TemplateVariables/Tags are raw JSON (jsonb).
type SystemTemplateInput struct {
	ExternalKey           string
	Name                  string
	Description           string
	Category              string
	Icon                  string
	Status                string
	DefinitionJSON        string
	TemplateVariablesJSON string
	TagsJSON              string
	Checksum              string
}

// ReconcileResult summarizes one reconcile pass.
type ReconcileResult struct {
	Upserted    int
	Deactivated int
}

// ReconcileSystemTemplates upserts every input row and (optionally) soft-deactivates
// system rows whose slug is absent from the input set, all inside one transaction so
// readers never observe a half-reconciled state. The caller is responsible for never
// passing an empty/failed bundle (an empty rows slice skips deactivation entirely as
// a defensive guard against mass-wipes).
func (s *WorkflowTemplateDao) ReconcileSystemTemplates(ctx context.Context, source, sourceRef string, rows []SystemTemplateInput, deactivateMissing bool) (ReconcileResult, error) {
	var result ReconcileResult
	if len(rows) == 0 {
		return result, fmt.Errorf("refusing to reconcile an empty template set")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("failed to begin reconcile tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := range rows {
		if err := s.upsertSystemTemplate(ctx, tx, source, sourceRef, rows[i]); err != nil {
			return result, fmt.Errorf("failed to upsert template %q: %w", rows[i].ExternalKey, err)
		}
		result.Upserted++
	}

	if deactivateMissing {
		present := make([]string, len(rows))
		for i := range rows {
			present[i] = rows[i].ExternalKey
		}
		n, err := s.deactivateMissing(ctx, tx, present)
		if err != nil {
			return result, fmt.Errorf("failed to deactivate drifted templates: %w", err)
		}
		result.Deactivated = n
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("failed to commit reconcile tx: %w", err)
	}
	return result, nil
}

// upsertSystemTemplate inserts or updates one system template, keyed on external_key.
// source is excluded from the conflict target on purpose: a migration-seeded row and
// the same-slug bundled/github row collapse onto the SAME row (its source flips), so
// the id stays stable across the migration -> bundled -> github progression. The WHERE
// on DO UPDATE makes an unchanged row a no-op (updated_at/synced_at left untouched).
func (s *WorkflowTemplateDao) upsertSystemTemplate(ctx context.Context, tx *sqlx.Tx, source, sourceRef string, t SystemTemplateInput) error {
	const q = `
		INSERT INTO workflow_templates
		  (tenant_id, account_id, name, description, category, icon, definition,
		   template_variables, tags, is_system, status, source, external_key,
		   checksum, source_ref, synced_at, created_at, updated_at)
		VALUES
		  (NULL, NULL, $1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, true, $8,
		   $9, $10, $11, $12, now(), now(), now())
		ON CONFLICT (external_key)
		  WHERE external_key IS NOT NULL AND tenant_id IS NULL AND account_id IS NULL
		DO UPDATE SET
		  name = EXCLUDED.name,
		  description = EXCLUDED.description,
		  category = EXCLUDED.category,
		  icon = EXCLUDED.icon,
		  definition = EXCLUDED.definition,
		  template_variables = EXCLUDED.template_variables,
		  tags = EXCLUDED.tags,
		  status = EXCLUDED.status,
		  source = EXCLUDED.source,
		  source_ref = EXCLUDED.source_ref,
		  checksum = EXCLUDED.checksum,
		  synced_at = now(),
		  updated_at = now()
		WHERE workflow_templates.checksum IS DISTINCT FROM EXCLUDED.checksum
		   OR workflow_templates.status   IS DISTINCT FROM EXCLUDED.status
		   OR workflow_templates.source   IS DISTINCT FROM EXCLUDED.source`

	_, err := tx.ExecContext(ctx, q,
		t.Name, t.Description, t.Category, t.Icon, t.DefinitionJSON,
		t.TemplateVariablesJSON, t.TagsJSON, t.Status, source, t.ExternalKey,
		t.Checksum, sourceRef,
	)
	return err
}

// deactivateMissing soft-deactivates any ACTIVE system template whose slug is not in
// presentSlugs, regardless of which source originally wrote it. Returns the row count.
func (s *WorkflowTemplateDao) deactivateMissing(ctx context.Context, tx *sqlx.Tx, presentSlugs []string) (int, error) {
	if len(presentSlugs) == 0 {
		return 0, nil // never deactivate everything on an empty set
	}
	const q = `
		UPDATE workflow_templates
		SET status = 'INACTIVE', updated_at = now()
		WHERE is_system = true AND tenant_id IS NULL AND account_id IS NULL
		  AND external_key IS NOT NULL
		  AND status = 'ACTIVE'
		  AND NOT (external_key = ANY($1))`
	res, err := tx.ExecContext(ctx, q, pq.Array(presentSlugs))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListBySource returns system templates with the given source (any status). For
// verification/debugging the sync pipeline; not on a hot path.
func (s *WorkflowTemplateDao) ListBySource(ctx context.Context, source string) ([]model.WorkflowTemplate, error) {
	const q = `
		SELECT id::text, name, COALESCE(description,''), COALESCE(category,''), COALESCE(icon,''),
			status, source, COALESCE(external_key,''), COALESCE(checksum,''), COALESCE(source_ref,''),
			created_at, updated_at
		FROM workflow_templates
		WHERE is_system = true AND tenant_id IS NULL AND account_id IS NULL AND source = $1
		ORDER BY external_key`
	rows, err := s.db.QueryContext(ctx, q, source)
	if err != nil {
		return nil, fmt.Errorf("failed to list templates by source: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.WorkflowTemplate
	for rows.Next() {
		var t model.WorkflowTemplate
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Category, &t.Icon,
			&t.Status, &t.Source, &t.ExternalKey, &t.Checksum, &t.SourceRef,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan template by source: %w", err)
		}
		t.IsSystem = true
		t.CreatedAt = createdAt
		t.UpdatedAt = updatedAt
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
