package preferences

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/common"
)

// ListForUser returns all preferences for a user, optionally filtered to
// cross-agent + a specific module. Cross-agent entries (agent_module IS NULL)
// are always included.
func ListForUser(tenantID, userID, agentModule string) ([]Preference, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("preferences.ListForUser: db: %w", err)
	}

	query := `
		SELECT id, tenant_id, user_id, agent_module, key, value,
		       source, confidence, created_at, updated_at
		FROM llm_memory_preferences
		WHERE tenant_id = $1 AND user_id = $2
		  AND (agent_module IS NULL OR agent_module = $3)
		ORDER BY (agent_module IS NULL) DESC, key ASC
	`
	var rows []Preference
	if err := db.Db.Select(&rows, query, tenantID, userID, agentModule); err != nil {
		return nil, fmt.Errorf("preferences.ListForUser: select: %w", err)
	}

	// Unmarshal JSON values into the typed Value field for each row.
	for i := range rows {
		if len(rows[i].ValueJSON) == 0 {
			continue
		}
		var v any
		if jerr := json.Unmarshal(rows[i].ValueJSON, &v); jerr != nil {
			return nil, fmt.Errorf("preferences.ListForUser: unmarshal %s: %w", rows[i].Key, jerr)
		}
		rows[i].Value = v
	}
	return rows, nil
}

// Upsert writes a preference row. If an entry with the same
// (tenant, user, module, key) exists, it is updated.
func Upsert(p *Preference) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("preferences.Upsert: db: %w", err)
	}

	valueJSON, err := json.Marshal(p.Value)
	if err != nil {
		return fmt.Errorf("preferences.Upsert: marshal value: %w", err)
	}

	if p.Source == "" {
		p.Source = SourceExplicit
	}
	if p.Confidence == 0 {
		p.Confidence = 1.0
	}

	query := `
		INSERT INTO llm_memory_preferences
			(tenant_id, user_id, agent_module, key, value, source, confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, user_id, COALESCE(agent_module, ''), key) DO UPDATE SET
			value      = EXCLUDED.value,
			source     = EXCLUDED.source,
			confidence = EXCLUDED.confidence,
			updated_at = NOW()
	`
	_, err = db.Db.Exec(query,
		p.TenantID, p.UserID, p.AgentModule, p.Key, valueJSON,
		p.Source, p.Confidence,
	)
	if err != nil {
		return fmt.Errorf("preferences.Upsert: exec: %w", err)
	}
	return nil
}

// Clear removes a single preference key for a user.
func Clear(tenantID, userID string, agentModule *string, key string) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("preferences.Clear: db: %w", err)
	}
	query := `
		DELETE FROM llm_memory_preferences
		WHERE tenant_id = $1 AND user_id = $2
		  AND COALESCE(agent_module, '') = COALESCE($3, '')
		  AND key = $4
	`
	_, err = db.Db.Exec(query, tenantID, userID, agentModule, key)
	if err != nil {
		return fmt.Errorf("preferences.Clear: exec: %w", err)
	}
	return nil
}

// DeleteAllForUser removes every preference for a user (GDPR erase).
func DeleteAllForUser(tenantID, userID string) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("preferences.DeleteAllForUser: db: %w", err)
	}
	_, err = db.Db.Exec(
		`DELETE FROM llm_memory_preferences WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	)
	if err != nil {
		return fmt.Errorf("preferences.DeleteAllForUser: exec: %w", err)
	}
	return nil
}
