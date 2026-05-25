package patterns

import (
	"encoding/json"
	"fmt"
	"math"
	"nudgebee/llm/common"
	"time"
)

// TopForUser returns the top-N patterns for a user, filtered by module,
// ordered by computed score (frequency × recency decay).
func TopForUser(tenantID, userID, agentModule string, limit int) ([]Pattern, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("patterns.TopForUser: db: %w", err)
	}
	if limit <= 0 {
		limit = 10
	}

	// Compute decayed score in SQL so ordering is consistent with storage.
	// Uses DefaultDecayLambda; Phase 7 will make this per-module tunable.
	query := `
		SELECT id, tenant_id, user_id, agent_module, pattern_kind, subject,
		       metadata, count, score, last_seen_at, created_at, updated_at
		FROM llm_memory_patterns
		WHERE tenant_id = $1 AND user_id = $2
		  AND (agent_module IS NULL OR agent_module = $3)
		ORDER BY score * exp(-($4::double precision) * EXTRACT(EPOCH FROM NOW() - last_seen_at) / 86400) DESC,
		         last_seen_at DESC
		LIMIT $5
	`
	var rows []Pattern
	if err := db.Db.Select(&rows, query, tenantID, userID, agentModule, DefaultDecayLambda, limit); err != nil {
		return nil, fmt.Errorf("patterns.TopForUser: select: %w", err)
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

// Upsert inserts or increments a pattern. On conflict bumps count, refreshes
// last_seen_at, and rescores.
func Upsert(p *Pattern) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("patterns.Upsert: db: %w", err)
	}
	if p.MetaJSON == nil && p.Metadata != nil {
		p.MetaJSON, _ = json.Marshal(p.Metadata)
	}
	if p.MetaJSON == nil {
		p.MetaJSON = []byte("{}")
	}

	query := `
		INSERT INTO llm_memory_patterns
			(tenant_id, user_id, agent_module, pattern_kind, subject,
			 metadata, count, score, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, 1, 1.0, NOW())
		ON CONFLICT (tenant_id, user_id, COALESCE(agent_module, ''), pattern_kind, subject)
		DO UPDATE SET
			count        = llm_memory_patterns.count + 1,
			score        = llm_memory_patterns.score + 1.0,
			last_seen_at = NOW(),
			metadata     = EXCLUDED.metadata,
			updated_at   = NOW()
	`
	_, err = db.Db.Exec(query,
		p.TenantID, p.UserID, p.AgentModule, p.Kind, p.Subject, p.MetaJSON,
	)
	if err != nil {
		return fmt.Errorf("patterns.Upsert: exec: %w", err)
	}
	return nil
}

// DecayedScore computes score × exp(-lambda × days_since_last).
// Used by the ranker when combining patterns with other signals.
func DecayedScore(p Pattern, lambda float64) float64 {
	if lambda <= 0 {
		lambda = DefaultDecayLambda
	}
	days := time.Since(p.LastSeenAt).Hours() / 24.0
	return p.Score * math.Exp(-lambda*days)
}

// DeleteAllForUser purges all patterns for a user (GDPR).
func DeleteAllForUser(tenantID, userID string) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("patterns.DeleteAllForUser: db: %w", err)
	}
	_, err = db.Db.Exec(
		`DELETE FROM llm_memory_patterns WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	)
	return err
}
