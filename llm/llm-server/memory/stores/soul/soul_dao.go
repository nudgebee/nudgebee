package soul

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/common"
)

// Get returns the Soul for a user, or nil if none exists.
// Returning nil (not an error) is the intended cold-start signal.
func Get(tenantID, userID string) (*Soul, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("soul.Get: db: %w", err)
	}

	var s Soul
	query := `
		SELECT tenant_id, user_id, version, style, COALESCE(markdown, '') AS markdown,
		       created_at, updated_at
		FROM llm_memory_soul
		WHERE tenant_id = $1 AND user_id = $2
	`
	err = db.Db.Get(&s, query, tenantID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("soul.Get: select: %w", err)
	}

	if len(s.StyleJSON) > 0 {
		if jerr := json.Unmarshal(s.StyleJSON, &s.Style); jerr != nil {
			return nil, fmt.Errorf("soul.Get: unmarshal style: %w", jerr)
		}
	}
	return &s, nil
}

// Upsert replaces the Soul for a user. Version bumps on every write for
// optimistic-concurrency debuggability (not enforced yet).
func Upsert(s *Soul) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("soul.Upsert: db: %w", err)
	}

	styleJSON, err := json.Marshal(s.Style)
	if err != nil {
		return fmt.Errorf("soul.Upsert: marshal style: %w", err)
	}

	var md interface{}
	if s.Markdown != "" {
		md = s.Markdown
	}

	query := `
		INSERT INTO llm_memory_soul (tenant_id, user_id, version, style, markdown)
		VALUES ($1, $2, 1, $3, $4)
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET
			version    = llm_memory_soul.version + 1,
			style      = EXCLUDED.style,
			markdown   = EXCLUDED.markdown,
			updated_at = NOW()
	`
	_, err = db.Db.Exec(query, s.TenantID, s.UserID, styleJSON, md)
	if err != nil {
		return fmt.Errorf("soul.Upsert: exec: %w", err)
	}
	return nil
}

// Delete removes a user's Soul entirely. Used by Erase (GDPR).
func Delete(tenantID, userID string) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("soul.Delete: db: %w", err)
	}
	_, err = db.Db.Exec(`DELETE FROM llm_memory_soul WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("soul.Delete: exec: %w", err)
	}
	return nil
}
