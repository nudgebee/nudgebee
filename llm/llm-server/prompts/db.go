package prompts

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/common"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// PromptDB handles database operations for prompt versioning
type PromptDB struct {
	db *common.DatabaseManager
}

// NewPromptDB creates a new PromptDB instance
func NewPromptDB(db *common.DatabaseManager) *PromptDB {
	return &PromptDB{db: db}
}

// GetActiveExperiments retrieves all active experiments for a given prompt, account, and provider
// Returns experiments ordered by created_at DESC (most recent first)
// NOTE: Multiple experiments may be returned if there are overlaps. The validation in CreateExperiment
// should prevent new overlaps, but existing data may still have them. Callers should use the first result.
// TODO: Consider adding a database exclusion constraint to prevent overlaps at the schema level:
//
//	EXCLUDE USING gist (prompt_name WITH =, category WITH =, target_accounts WITH &&, providers WITH &&, tstzrange(start_date, end_date, '[]') WITH &&)
func (p *PromptDB) GetActiveExperiments(ctx context.Context, promptName string, category PromptCategory, provider string, accountID string) ([]DBExperiment, error) {
	query := `
		SELECT
			id, name, prompt_name, category, test_version, control_version,
			target_accounts, providers, start_date, end_date, enabled,
			description, created_at, created_by, updated_at, updated_by
		FROM llm_prompt_experiments
		WHERE prompt_name = $1
			AND category = $2
			AND enabled = TRUE
			AND $3 = ANY(target_accounts)
			AND (start_date IS NULL OR start_date <= NOW())
			AND (end_date IS NULL OR end_date >= NOW())
			AND (providers IS NULL OR cardinality(providers) = 0 OR $4 = ANY(providers))
		ORDER BY created_at DESC
	`

	var experiments []DBExperiment
	err := p.db.QueryAndScan(&experiments, query, promptName, category, accountID, provider)
	if err != nil {
		// Log as debug - caller will handle gracefully with fallback
		slog.Debug("prompts: database query failed, caller will use fallback",
			"prompt", promptName,
			"category", category,
			"account_id", accountID,
			"provider", provider,
			"error", err)
		return nil, err
	}

	return experiments, nil
}

// GetConfig retrieves the configuration for a prompt
// Priority order:
// 1. account_id = specific + provider = specific
// 2. account_id = specific + provider = default
// 3. account_id = NULL + provider = specific
// 4. account_id = NULL + provider = default
func (p *PromptDB) GetConfig(ctx context.Context, promptName string, category PromptCategory, provider string, accountID string) (*DBConfig, error) {
	query := `
		SELECT
			id, prompt_name, category, provider, active_version, account_id,
			enabled, priority, notes, updated_at, updated_by
		FROM llm_prompt_configuration
		WHERE prompt_name = $1
			AND category = $2
			AND enabled = TRUE
			AND (
				(account_id = $3 AND provider = $4) OR
				(account_id = $3 AND provider = 'default') OR
				(account_id IS NULL AND provider = $4) OR
				(account_id IS NULL AND provider = 'default')
			)
		ORDER BY
			CASE
				WHEN account_id = $3 AND provider = $4 THEN 1
				WHEN account_id = $3 AND provider = 'default' THEN 2
				WHEN account_id IS NULL AND provider = $4 THEN 3
				WHEN account_id IS NULL AND provider = 'default' THEN 4
			END,
			priority DESC
		LIMIT 1
	`

	var config DBConfig
	err := p.db.QueryRowAndScan(&config, query, promptName, category, accountID, provider)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No configuration found
		}
		// Log as debug - caller will handle gracefully with fallback
		slog.Debug("prompts: database query failed, caller will use fallback",
			"prompt", promptName,
			"category", category,
			"account_id", accountID,
			"provider", provider,
			"error", err)
		return nil, err
	}

	return &config, nil
}

// UpsertConfig inserts or updates a configuration
func (p *PromptDB) UpsertConfig(ctx context.Context, config *DBConfig) error {
	query := `
		INSERT INTO llm_prompt_configuration
			(prompt_name, category, provider, active_version, account_id, enabled, priority, notes, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (prompt_name, category, provider, account_id)
		DO UPDATE SET
			active_version = EXCLUDED.active_version,
			enabled = EXCLUDED.enabled,
			priority = EXCLUDED.priority,
			notes = EXCLUDED.notes,
			updated_at = NOW(),
			updated_by = EXCLUDED.updated_by
		RETURNING id, updated_at
	`

	err := p.db.QueryRowAndScan(config, query,
		config.PromptName,
		config.Category,
		config.Provider,
		config.ActiveVersion,
		config.AccountID,
		config.Enabled,
		config.Priority,
		config.Notes,
		config.UpdatedBy,
	)

	if err != nil {
		slog.Error("prompts: failed to upsert config",
			"prompt", config.PromptName,
			"category", config.Category,
			"error", err)
		return err
	}

	return nil
}

// CreateExperiment creates a new experiment
// Returns an error if there are overlapping active experiments for the same prompt/accounts/providers
func (p *PromptDB) CreateExperiment(ctx context.Context, exp *DBExperiment) error {
	// Validate: Check for overlapping active experiments
	if exp.Enabled {
		overlaps, err := p.findOverlappingExperiments(ctx, exp)
		if err != nil {
			slog.Error("prompts: failed to check for overlapping experiments",
				"name", exp.Name,
				"error", err)
			return err
		}
		if len(overlaps) > 0 {
			overlapNames := make([]string, len(overlaps))
			for i, o := range overlaps {
				overlapNames[i] = o.Name
			}
			return fmt.Errorf(
				"cannot create experiment '%s': overlaps with %d existing active experiment(s): %v. "+
					"Disable conflicting experiments or adjust target_accounts/providers/dates",
				exp.Name, len(overlaps), overlapNames)
		}
	}

	query := `
		INSERT INTO llm_prompt_experiments
			(name, prompt_name, category, test_version, control_version,
			 target_accounts, providers, start_date, end_date, enabled,
			 description, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at, updated_at
	`

	err := p.db.QueryRowAndScan(exp, query,
		exp.Name,
		exp.PromptName,
		exp.Category,
		exp.TestVersion,
		exp.ControlVersion,
		pq.Array(exp.TargetAccounts),
		pq.Array(exp.Providers),
		exp.StartDate,
		exp.EndDate,
		exp.Enabled,
		exp.Description,
		exp.CreatedBy,
		exp.UpdatedBy,
	)

	if err != nil {
		slog.Error("prompts: failed to create experiment",
			"name", exp.Name,
			"error", err)
		return err
	}

	return nil
}

// findOverlappingExperiments finds active experiments that overlap with the given experiment
// Two experiments overlap if they:
// 1. Target the same prompt_name and category
// 2. Have overlapping target_accounts (any common account)
// 3. Have overlapping providers (nil/empty = all providers, or any common provider)
// 4. Have overlapping time ranges (start_date and end_date)
//
// Uses a single efficient query with PostgreSQL's array overlap operator (&&) to avoid N+1 queries
func (p *PromptDB) findOverlappingExperiments(ctx context.Context, exp *DBExperiment) ([]DBExperiment, error) {
	// Build a single query that checks all overlap conditions
	// Use PostgreSQL's array overlap operator (&&) for efficient array comparisons
	query := `
		SELECT id, name, prompt_name, category, test_version, control_version,
		       target_accounts, providers, start_date, end_date, enabled,
		       description, created_at, created_by, updated_at, updated_by
		FROM llm_prompt_experiments
		WHERE enabled = TRUE
		  AND prompt_name = $1
		  AND category = $2
		  AND name != $3
		  AND target_accounts && $4
		  AND (
		      -- Either this experiment targets all providers (empty array)
		      -- or the new experiment targets all providers (empty array)
		      -- or their provider arrays overlap
		      cardinality(providers) = 0
		      OR $5 = 0
		      OR providers && $6
		  )
		  AND (
		      -- Check for date range overlap
		      -- Ranges overlap if: start1 <= end2 AND start2 <= end1
		      -- Handle NULL as open-ended ranges
		      (start_date IS NULL OR $8 IS NULL OR start_date <= $8)
		      AND ($7 IS NULL OR end_date IS NULL OR $7 <= end_date)
		  )
	`

	// Prepare parameters
	targetAccounts := exp.TargetAccounts
	providers := exp.Providers
	providersCount := len(providers)

	// Convert time.Time pointers to sql-compatible values
	var startDate, endDate interface{}
	if exp.StartDate != nil {
		startDate = *exp.StartDate
	}
	if exp.EndDate != nil {
		endDate = *exp.EndDate
	}

	// Execute the query using QueryAndScan
	var overlaps []DBExperiment
	err := p.db.QueryAndScan(&overlaps, query,
		exp.PromptName,           // $1
		exp.Category,             // $2
		exp.Name,                 // $3 - exclude same experiment for updates
		pq.Array(targetAccounts), // $4 - array overlap check
		providersCount,           // $5 - check if new exp targets all providers
		pq.Array(providers),      // $6 - array overlap check
		startDate,                // $7 - start date for range check
		endDate,                  // $8 - end date for range check
	)
	if err != nil {
		slog.Error("prompts: failed to query overlapping experiments",
			"prompt", exp.PromptName,
			"category", exp.Category,
			"error", err)
		return nil, err
	}

	return overlaps, nil
}

// GetExperiment retrieves an experiment by name
func (p *PromptDB) GetExperiment(ctx context.Context, name string) (*DBExperiment, error) {
	query := `
		SELECT
			id, name, prompt_name, category, test_version, control_version,
			target_accounts, providers, start_date, end_date, enabled,
			description, created_at, created_by, updated_at, updated_by
		FROM llm_prompt_experiments
		WHERE name = $1
	`

	var exp DBExperiment
	err := p.db.QueryRowAndScan(&exp, query, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		slog.Error("prompts: failed to get experiment", "name", name, "error", err)
		return nil, err
	}

	return &exp, nil
}

// GetExperimentByID retrieves an experiment by its UUID primary key.
// Use this when you have an experiment ID (e.g., from PromptMetadata.ExperimentID).
// Use GetExperiment when you have the human-readable name (e.g., from URL params).
func (p *PromptDB) GetExperimentByID(ctx context.Context, id uuid.UUID) (*DBExperiment, error) {
	query := `
		SELECT
			id, name, prompt_name, category, test_version, control_version,
			target_accounts, providers, start_date, end_date, enabled,
			description, created_at, created_by, updated_at, updated_by
		FROM llm_prompt_experiments
		WHERE id = $1
	`

	var exp DBExperiment
	err := p.db.QueryRowAndScan(&exp, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		slog.Error("prompts: failed to get experiment by id", "id", id, "error", err)
		return nil, err
	}

	return &exp, nil
}

// UpdateExperimentAccounts updates the target accounts for an experiment
func (p *PromptDB) UpdateExperimentAccounts(ctx context.Context, name string, action string, accounts []string) error {
	// First get the current experiment
	exp, err := p.GetExperiment(ctx, name)
	if err != nil {
		return err
	}
	if exp == nil {
		return fmt.Errorf("experiment not found: %s", name)
	}

	newAccounts := exp.TargetAccounts
	switch action {
	case "add":
		// Add accounts (avoid duplicates)
		accountSet := make(map[string]bool)
		for _, acc := range newAccounts {
			accountSet[acc] = true
		}
		for _, acc := range accounts {
			if !accountSet[acc] {
				newAccounts = append(newAccounts, acc)
			}
		}
	case "remove":
		// Remove accounts
		accountSet := make(map[string]bool)
		for _, acc := range accounts {
			accountSet[acc] = true
		}
		filtered := make([]string, 0)
		for _, acc := range newAccounts {
			if !accountSet[acc] {
				filtered = append(filtered, acc)
			}
		}
		newAccounts = filtered
	case "set":
		// Replace accounts
		newAccounts = accounts
	default:
		return fmt.Errorf("invalid action: %s", action)
	}

	if len(newAccounts) == 0 {
		return fmt.Errorf("target_accounts cannot be empty")
	}

	query := `
		UPDATE llm_prompt_experiments
		SET target_accounts = $1, updated_at = NOW()
		WHERE name = $2
	`

	_, err = p.db.Exec(query, pq.Array(newAccounts), name)
	if err != nil {
		slog.Error("prompts: failed to update experiment accounts",
			"name", name,
			"action", action,
			"error", err)
		return err
	}

	return nil
}

// DisableExperiment disables an experiment
func (p *PromptDB) DisableExperiment(ctx context.Context, name string) error {
	query := `
		UPDATE llm_prompt_experiments
		SET enabled = FALSE, updated_at = NOW()
		WHERE name = $1
	`

	_, err := p.db.Exec(query, name)
	if err != nil {
		slog.Error("prompts: failed to disable experiment", "name", name, "error", err)
		return err
	}

	return nil
}

// ListActiveExperiments lists all currently active experiments
func (p *PromptDB) ListActiveExperiments(ctx context.Context, filters map[string]string) ([]DBExperiment, error) {
	query := `
		SELECT
			id, name, prompt_name, category, test_version, control_version,
			target_accounts, providers, start_date, end_date, enabled,
			description, created_at, created_by, updated_at, updated_by
		FROM llm_prompt_experiments
		WHERE enabled = TRUE
			AND (start_date IS NULL OR start_date <= NOW())
			AND (end_date IS NULL OR end_date >= NOW())
	`

	args := []any{}
	argIndex := 1

	if promptName, ok := filters["prompt_name"]; ok {
		query += fmt.Sprintf(" AND prompt_name = $%d", argIndex)
		args = append(args, promptName)
		argIndex++
	}

	if category, ok := filters["category"]; ok {
		query += fmt.Sprintf(" AND category = $%d", argIndex)
		args = append(args, category)
		argIndex++
	}

	if accountID, ok := filters["account_id"]; ok {
		query += fmt.Sprintf(" AND $%d = ANY(target_accounts)", argIndex)
		args = append(args, accountID)
		// argIndex++ not needed - last filter
	}

	query += " ORDER BY created_at DESC"

	var experiments []DBExperiment
	err := p.db.QueryAndScan(&experiments, query, args...)
	if err != nil {
		slog.Error("prompts: failed to list active experiments", "error", err)
		return nil, err
	}

	return experiments, nil
}

// CreateAuditLog creates an audit log entry
func (p *PromptDB) CreateAuditLog(ctx context.Context, log *DBAuditLog) error {
	query := `
		INSERT INTO llm_prompt_config_audit
			(prompt_name, category, provider, account_id, action,
			 old_version, new_version, experiment_id, changed_by, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, changed_at
	`

	err := p.db.QueryRowAndScan(log, query,
		log.PromptName,
		log.Category,
		log.Provider,
		log.AccountID,
		log.Action,
		log.OldVersion,
		log.NewVersion,
		log.ExperimentID,
		log.ChangedBy,
		log.Reason,
		log.Metadata,
	)

	if err != nil {
		slog.Error("prompts: failed to create audit log",
			"prompt", log.PromptName,
			"action", log.Action,
			"error", err)
		return err
	}

	return nil
}

// GetAuditLogs retrieves audit logs with optional filters
func (p *PromptDB) GetAuditLogs(ctx context.Context, filters map[string]any, limit int) ([]DBAuditLog, error) {
	query := `
		SELECT
			id, prompt_name, category, provider, account_id, action,
			old_version, new_version, experiment_id, changed_by,
			changed_at, reason, metadata
		FROM llm_prompt_config_audit
		WHERE 1=1
	`

	args := []any{}
	argIndex := 1

	if promptName, ok := filters["prompt_name"].(string); ok {
		query += fmt.Sprintf(" AND prompt_name = $%d", argIndex)
		args = append(args, promptName)
		argIndex++
	}

	if accountID, ok := filters["account_id"].(string); ok {
		query += fmt.Sprintf(" AND account_id = $%d", argIndex)
		args = append(args, accountID)
		argIndex++
	}

	if changedBy, ok := filters["changed_by"].(string); ok {
		query += fmt.Sprintf(" AND changed_by = $%d", argIndex)
		args = append(args, changedBy)
		argIndex++
	}

	if startDate, ok := filters["start_date"].(time.Time); ok {
		query += fmt.Sprintf(" AND changed_at >= $%d", argIndex)
		args = append(args, startDate)
		argIndex++
	}

	if endDate, ok := filters["end_date"].(time.Time); ok {
		query += fmt.Sprintf(" AND changed_at <= $%d", argIndex)
		args = append(args, endDate)
		argIndex++
	}

	query += " ORDER BY changed_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, limit)
	}

	var logs []DBAuditLog
	err := p.db.QueryAndScan(&logs, query, args...)
	if err != nil {
		slog.Error("prompts: failed to get audit logs", "error", err)
		return nil, err
	}

	return logs, nil
}

// RecordMetrics records a metrics entry
func (p *PromptDB) RecordMetrics(ctx context.Context, metrics *DBMetrics) error {
	query := `
		INSERT INTO llm_prompt_usage_metrics
			(prompt_name, category, provider, version, account_id,
			 conversation_id, agent_name, load_time_ms, cache_hit,
			 config_source, experiment_id, experiment_name, error, error_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, timestamp
	`

	// Dereference *uuid.UUID to avoid nil pointer panic when the pq driver
	// calls .Value() on a nil *uuid.UUID (uuid.UUID has a value receiver).
	var experimentID any
	if metrics.ExperimentID != nil {
		experimentID = *metrics.ExperimentID
	}

	err := p.db.QueryRowAndScan(metrics, query,
		metrics.PromptName,
		metrics.Category,
		metrics.Provider,
		metrics.Version,
		metrics.AccountID,
		metrics.ConversationID,
		metrics.AgentName,
		metrics.LoadTimeMs,
		metrics.CacheHit,
		metrics.ConfigSource,
		experimentID,
		metrics.ExperimentName,
		metrics.Error,
		metrics.ErrorMessage,
	)

	if err != nil {
		// Log but don't fail the request if metrics recording fails
		slog.Warn("prompts: failed to record metrics",
			"prompt", metrics.PromptName,
			"error", err)
		return nil // Don't propagate error
	}

	return nil
}

// GetExperimentMetrics retrieves aggregated metrics for an experiment
func (p *PromptDB) GetExperimentMetrics(ctx context.Context, experimentName string, startDate, endDate time.Time) (*ExperimentMetrics, error) {
	query := `
		SELECT
			COUNT(*) as total_requests,
			COUNT(*) FILTER (WHERE version = (SELECT test_version FROM llm_prompt_experiments WHERE name = $1)) as test_version_requests,
			AVG(load_time_ms) as avg_load_time_ms,
			COUNT(*) FILTER (WHERE cache_hit = TRUE)::float / NULLIF(COUNT(*), 0) as cache_hit_rate,
			COUNT(*) FILTER (WHERE error = TRUE)::float / NULLIF(COUNT(*), 0) as error_rate,
			COUNT(DISTINCT account_id) as accounts_served
		FROM llm_prompt_usage_metrics
		WHERE experiment_name = $1
			AND timestamp >= $2
			AND timestamp <= $3
	`

	var metrics ExperimentMetrics
	err := p.db.QueryRowAndScan(&metrics, query, experimentName, startDate, endDate)
	if err != nil {
		slog.Error("prompts: failed to get experiment metrics",
			"experiment", experimentName,
			"error", err)
		return nil, err
	}

	return &metrics, nil
}

// IsAvailable checks if the database is available
func (p *PromptDB) IsAvailable() bool {
	if p.db == nil || p.db.Db == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// First check if DB connection works
	err := p.db.Db.PingContext(ctx)
	if err != nil {
		return false
	}

	// Check if required tables exist
	// We need at least llm_prompt_configuration table for the system to work
	tableCheckQuery := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = 'llm_prompt_configuration'
		)
	`

	var exists bool
	err = p.db.Db.QueryRowContext(ctx, tableCheckQuery).Scan(&exists)
	if err != nil || !exists {
		slog.Warn("prompts: required database tables not found, running without DB",
			"error", err,
			"table_exists", exists)
		return false
	}

	return true
}
