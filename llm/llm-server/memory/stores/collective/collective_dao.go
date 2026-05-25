package collective

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/common"
)

// TopForTenant returns the top-N collective entries for a tenant, filtered by
// module. Phase 2 uses keyword + recency; Phase 2f adds RAG semantic overlay.
func TopForTenant(tenantID, agentModule, keyword string, limit int) ([]Entry, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("collective.TopForTenant: db: %w", err)
	}
	if limit <= 0 {
		limit = 10
	}

	var (
		rows  []Entry
		query string
		args  []any
	)
	if keyword != "" {
		query = `
			SELECT id, tenant_id, agent_module, entry_kind, subject, body,
			       metadata, source_event_id, confidence, curated_by,
			       created_at, updated_at
			FROM llm_memory_collective
			WHERE tenant_id = $1
			  AND (agent_module IS NULL OR agent_module = $2)
			  AND to_tsvector('english', subject || ' ' || body) @@ plainto_tsquery('english', $3)
			ORDER BY confidence DESC, updated_at DESC
			LIMIT $4
		`
		args = []any{tenantID, agentModule, keyword, limit}
	} else {
		query = `
			SELECT id, tenant_id, agent_module, entry_kind, subject, body,
			       metadata, source_event_id, confidence, curated_by,
			       created_at, updated_at
			FROM llm_memory_collective
			WHERE tenant_id = $1
			  AND (agent_module IS NULL OR agent_module = $2)
			ORDER BY confidence DESC, updated_at DESC
			LIMIT $3
		`
		args = []any{tenantID, agentModule, limit}
	}

	if err := db.Db.Select(&rows, query, args...); err != nil {
		return nil, fmt.Errorf("collective.TopForTenant: select: %w", err)
	}
	for i := range rows {
		if len(rows[i].MetaJSON) > 0 {
			var m any
			_ = json.Unmarshal(rows[i].MetaJSON, &m)
			rows[i].Metadata = m
		}
	}
	return rows, nil
}

// Upsert inserts or updates a collective entry by (tenant, module, kind, subject).
// Bumps confidence and refreshes body on re-assertion.
func Upsert(e *Entry) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("collective.Upsert: db: %w", err)
	}
	if e.MetaJSON == nil && e.Metadata != nil {
		e.MetaJSON, _ = json.Marshal(e.Metadata)
	}
	if e.MetaJSON == nil {
		e.MetaJSON = []byte("{}")
	}
	if e.Confidence == 0 {
		e.Confidence = 0.7
	}

	query := `
		INSERT INTO llm_memory_collective
			(tenant_id, agent_module, entry_kind, subject, body, metadata,
			 source_event_id, confidence, curated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, COALESCE(agent_module, ''), entry_kind, subject)
		DO UPDATE SET
			body        = EXCLUDED.body,
			metadata    = EXCLUDED.metadata,
			confidence  = GREATEST(llm_memory_collective.confidence, EXCLUDED.confidence),
			curated_by  = COALESCE(EXCLUDED.curated_by, llm_memory_collective.curated_by),
			updated_at  = NOW()
	`
	_, err = db.Db.Exec(query,
		e.TenantID, e.AgentModule, e.EntryKind, e.Subject, e.Body, e.MetaJSON,
		e.SourceEventID, e.Confidence, e.CuratedBy,
	)
	if err != nil {
		return fmt.Errorf("collective.Upsert: exec: %w", err)
	}
	return nil
}

// DeleteByID removes a curated entry (admin-only).
func DeleteByID(tenantID, id string) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("collective.DeleteByID: db: %w", err)
	}
	_, err = db.Db.Exec(
		`DELETE FROM llm_memory_collective WHERE tenant_id = $1 AND id = $2`,
		tenantID, id,
	)
	return err
}
