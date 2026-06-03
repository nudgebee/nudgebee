package decisions

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/common"
	"time"
)

// Append inserts a decision into the immutable log.
func Append(d *Decision) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("decisions.Append: db: %w", err)
	}
	if d.ContextJSON == nil && d.Context != nil {
		d.ContextJSON, _ = json.Marshal(d.Context)
	}
	if d.OutcomeJSON == nil && d.Outcome != nil {
		d.OutcomeJSON, _ = json.Marshal(d.Outcome)
	}
	if d.ContextJSON == nil {
		d.ContextJSON = []byte("{}")
	}
	if d.DecidedAt.IsZero() {
		d.DecidedAt = time.Now().UTC()
	}

	query := `
		INSERT INTO llm_memory_decisions
			(tenant_id, user_id, conversation_id, agent_module, decision_type,
			 subject, context, outcome, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err = db.Db.Exec(query,
		d.TenantID, d.UserID, d.ConversationID, d.AgentModule, d.DecisionType,
		d.Subject, d.ContextJSON, d.OutcomeJSON, d.DecidedAt,
	)
	if err != nil {
		return fmt.Errorf("decisions.Append: exec: %w", err)
	}
	return nil
}

// RecentForUser returns the most recent decisions for a user, optionally
// matching a keyword against subject via full-text search.
func RecentForUser(tenantID, userID, agentModule, keyword string, since time.Time, limit int) ([]Decision, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("decisions.RecentForUser: db: %w", err)
	}
	if limit <= 0 {
		limit = 10
	}

	var (
		rows  []Decision
		query string
		args  []any
	)
	if keyword != "" {
		query = `
			SELECT id, tenant_id, user_id, conversation_id, agent_module,
			       decision_type, subject, context, outcome, decided_at, created_at
			FROM llm_memory_decisions
			WHERE tenant_id = $1 AND user_id = $2
			  AND (agent_module IS NULL OR agent_module = $3)
			  AND decided_at >= $4
			  AND to_tsvector('english', subject) @@ plainto_tsquery('english', $5)
			ORDER BY decided_at DESC
			LIMIT $6
		`
		args = []any{tenantID, userID, agentModule, since, keyword, limit}
	} else {
		query = `
			SELECT id, tenant_id, user_id, conversation_id, agent_module,
			       decision_type, subject, context, outcome, decided_at, created_at
			FROM llm_memory_decisions
			WHERE tenant_id = $1 AND user_id = $2
			  AND (agent_module IS NULL OR agent_module = $3)
			  AND decided_at >= $4
			ORDER BY decided_at DESC
			LIMIT $5
		`
		args = []any{tenantID, userID, agentModule, since, limit}
	}

	if err := db.Db.Select(&rows, query, args...); err != nil {
		return nil, fmt.Errorf("decisions.RecentForUser: select: %w", err)
	}
	for i := range rows {
		if len(rows[i].ContextJSON) > 0 {
			var v any
			_ = json.Unmarshal(rows[i].ContextJSON, &v)
			rows[i].Context = v
		}
		if len(rows[i].OutcomeJSON) > 0 {
			var v any
			_ = json.Unmarshal(rows[i].OutcomeJSON, &v)
			rows[i].Outcome = v
		}
	}
	return rows, nil
}

// DeleteAllForUser purges all decisions for a user (GDPR).
func DeleteAllForUser(tenantID, userID string) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("decisions.DeleteAllForUser: db: %w", err)
	}
	_, err = db.Db.Exec(
		`DELETE FROM llm_memory_decisions WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	)
	return err
}
