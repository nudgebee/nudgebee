package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/services/internal/database"
	kgmodels "nudgebee/services/knowledge_graph/models"
	"nudgebee/services/security"

	"github.com/google/uuid"
)

// FilterRepository handles knowledge graph filter operations
type FilterRepository struct {
	dbManager *database.DatabaseManager
	logger    *slog.Logger
}

// NewFilterRepository creates a new filter repository
func NewFilterRepository(dbManager *database.DatabaseManager, logger *slog.Logger) *FilterRepository {
	if logger == nil {
		logger = slog.Default()
	}
	return &FilterRepository{
		dbManager: dbManager,
		logger:    logger,
	}
}

// GetFilterForTenant retrieves the filter configuration for a tenant
// If filterName is provided, it returns that specific filter
// Otherwise, it returns the default filter or the first enabled filter
func (r *FilterRepository) GetFilterForTenant(ctx *security.RequestContext, tenantID string, filterName string) (*kgmodels.KnowledgeGraphTenantFilter, error) {
	var query string
	var args []interface{}

	if filterName != "" {
		// Get specific filter by name
		query = `
			SELECT id, tenant_id, filter_name, account_ids, sources, flow_sources,
				   filters, is_default, enabled, created_at, last_sync_version, last_sync_time
			FROM knowledge_graph_tenant_filters
			WHERE tenant_id = $1 AND filter_name = $2 AND enabled = true
		`
		args = []interface{}{tenantID, filterName}
	} else {
		// Get default filter or first enabled filter
		query = `
			SELECT id, tenant_id, filter_name, account_ids, sources, flow_sources,
				   filters, is_default, enabled, created_at, last_sync_version, last_sync_time
			FROM knowledge_graph_tenant_filters
			WHERE tenant_id = $1 AND enabled = true
			ORDER BY is_default DESC, created_at ASC
			LIMIT 1
		`
		args = []interface{}{tenantID}
	}

	row, err := r.dbManager.QueryRow(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	var filter kgmodels.KnowledgeGraphTenantFilter
	var accountIDsJSON, sourcesJSON, flowSourcesJSON, filtersJSON []byte

	err = row.Scan(
		&filter.ID,
		&filter.TenantID,
		&filter.FilterName,
		&accountIDsJSON,
		&sourcesJSON,
		&flowSourcesJSON,
		&filtersJSON,
		&filter.IsDefault,
		&filter.Enabled,
		&filter.CreatedAt,
		&filter.LastSyncVersion,
		&filter.LastSyncTimestamp,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no filter found for tenant %s", tenantID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query filter: %w", err)
	}

	// Parse account_ids JSONB
	if accountIDsJSON != nil {
		if err := json.Unmarshal(accountIDsJSON, &filter.AccountIDs); err != nil {
			r.logger.Warn("failed to unmarshal account_ids JSON", "error", err)
			filter.AccountIDs = []string{}
		}
	} else {
		filter.AccountIDs = []string{}
	}

	// Parse sources JSONB
	if sourcesJSON != nil {
		if err := json.Unmarshal(sourcesJSON, &filter.Sources); err != nil {
			r.logger.Warn("failed to unmarshal sources JSON", "error", err)
			filter.Sources = []string{}
		}
	} else {
		filter.Sources = []string{}
	}

	// Parse flow_sources JSONB
	if flowSourcesJSON != nil {
		if err := json.Unmarshal(flowSourcesJSON, &filter.FlowSources); err != nil {
			r.logger.Warn("failed to unmarshal flow_sources JSON", "error", err)
			filter.FlowSources = []string{}
		}
	} else {
		filter.FlowSources = []string{}
	}

	// Parse filters JSONB
	if filtersJSON != nil {
		if err := json.Unmarshal(filtersJSON, &filter.Filters); err != nil {
			r.logger.Warn("failed to unmarshal filters JSON", "error", err)
			filter.Filters = make(map[string]interface{})
		}
	} else {
		filter.Filters = make(map[string]interface{})
	}

	r.logger.Info("retrieved filter for tenant",
		"tenant_id", tenantID,
		"filter_name", filter.FilterName,
		"account_count", len(filter.AccountIDs),
		"sources", filter.Sources,
		"flow_sources", filter.FlowSources)

	return &filter, nil
}

// GetAllFiltersForTenant retrieves all filter configurations for a tenant
func (r *FilterRepository) GetAllFiltersForTenant(ctx *security.RequestContext, tenantID string) ([]*kgmodels.KnowledgeGraphTenantFilter, error) {
	query := `
		SELECT id, tenant_id, filter_name, account_ids, sources, flow_sources,
			   filters, is_default, enabled, created_at, last_sync_version, last_sync_time
		FROM knowledge_graph_tenant_filters
		WHERE tenant_id = $1
		ORDER BY is_default DESC, filter_name ASC
	`

	rows, err := r.dbManager.Query(query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query filters: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			r.logger.Warn("failed to close rows", "error", closeErr)
		}
	}()

	var filters []*kgmodels.KnowledgeGraphTenantFilter
	for rows.Next() {
		var filter kgmodels.KnowledgeGraphTenantFilter
		var accountIDsJSON, sourcesJSON, flowSourcesJSON, filtersJSON []byte

		err := rows.Scan(
			&filter.ID,
			&filter.TenantID,
			&filter.FilterName,
			&accountIDsJSON,
			&sourcesJSON,
			&flowSourcesJSON,
			&filtersJSON,
			&filter.IsDefault,
			&filter.Enabled,
			&filter.CreatedAt,
			&filter.LastSyncVersion,
			&filter.LastSyncTimestamp,
		)
		if err != nil {
			r.logger.Warn("failed to scan filter row", "error", err)
			continue
		}

		// Parse account_ids JSONB
		if accountIDsJSON != nil {
			if err := json.Unmarshal(accountIDsJSON, &filter.AccountIDs); err != nil {
				r.logger.Warn("failed to unmarshal account_ids JSON", "error", err)
				filter.AccountIDs = []string{}
			}
		} else {
			filter.AccountIDs = []string{}
		}

		// Parse sources JSONB
		if sourcesJSON != nil {
			if err := json.Unmarshal(sourcesJSON, &filter.Sources); err != nil {
				r.logger.Warn("failed to unmarshal sources JSON", "error", err)
				filter.Sources = []string{}
			}
		} else {
			filter.Sources = []string{}
		}

		// Parse flow_sources JSONB
		if flowSourcesJSON != nil {
			if err := json.Unmarshal(flowSourcesJSON, &filter.FlowSources); err != nil {
				r.logger.Warn("failed to unmarshal flow_sources JSON", "error", err)
				filter.FlowSources = []string{}
			}
		} else {
			filter.FlowSources = []string{}
		}

		// Parse filters JSONB
		if filtersJSON != nil {
			if err := json.Unmarshal(filtersJSON, &filter.Filters); err != nil {
				r.logger.Warn("failed to unmarshal filters JSON", "error", err)
				filter.Filters = make(map[string]interface{})
			}
		} else {
			filter.Filters = make(map[string]interface{})
		}

		filters = append(filters, &filter)
	}

	return filters, nil
}

// CreateFilter creates a new filter configuration for a tenant
func (r *FilterRepository) CreateFilter(ctx *security.RequestContext, filter *kgmodels.KnowledgeGraphTenantFilter) error {
	// Marshal all JSONB fields
	accountIDsJSON, err := json.Marshal(filter.AccountIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal account_ids: %w", err)
	}

	sourcesJSON, err := json.Marshal(filter.Sources)
	if err != nil {
		return fmt.Errorf("failed to marshal sources: %w", err)
	}

	flowSourcesJSON, err := json.Marshal(filter.FlowSources)
	if err != nil {
		return fmt.Errorf("failed to marshal flow_sources: %w", err)
	}

	filtersJSON, err := json.Marshal(filter.Filters)
	if err != nil {
		return fmt.Errorf("failed to marshal filters: %w", err)
	}

	query := `
		INSERT INTO knowledge_graph_tenant_filters (
			tenant_id, filter_name, account_ids, sources, flow_sources,
			filters, is_default, enabled, last_sync_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at
	`

	row, err := r.dbManager.QueryRow(
		query,
		filter.TenantID,
		filter.FilterName,
		accountIDsJSON,
		sourcesJSON,
		flowSourcesJSON,
		filtersJSON,
		filter.IsDefault,
		filter.Enabled,
		0, // Initialize last_sync_version to 0
	)
	if err != nil {
		return fmt.Errorf("failed to execute insert query: %w", err)
	}

	err = row.Scan(&filter.ID, &filter.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create filter: %w", err)
	}

	r.logger.Info("created filter for tenant",
		"tenant_id", filter.TenantID,
		"filter_name", filter.FilterName,
		"filter_id", filter.ID)

	return nil
}

// UpdateFilter updates an existing filter configuration
func (r *FilterRepository) UpdateFilter(ctx *security.RequestContext, filter *kgmodels.KnowledgeGraphTenantFilter) error {
	// Marshal all JSONB fields
	accountIDsJSON, err := json.Marshal(filter.AccountIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal account_ids: %w", err)
	}

	sourcesJSON, err := json.Marshal(filter.Sources)
	if err != nil {
		return fmt.Errorf("failed to marshal sources: %w", err)
	}

	flowSourcesJSON, err := json.Marshal(filter.FlowSources)
	if err != nil {
		return fmt.Errorf("failed to marshal flow_sources: %w", err)
	}

	filtersJSON, err := json.Marshal(filter.Filters)
	if err != nil {
		return fmt.Errorf("failed to marshal filters: %w", err)
	}

	query := `
		UPDATE knowledge_graph_tenant_filters
		SET account_ids = $1,
			sources = $2,
			flow_sources = $3,
			filters = $4,
			is_default = $5,
			enabled = $6
		WHERE id = $7 AND tenant_id = $8
	`

	result, err := r.dbManager.Exec(
		query,
		accountIDsJSON,
		sourcesJSON,
		flowSourcesJSON,
		filtersJSON,
		filter.IsDefault,
		filter.Enabled,
		filter.ID,
		filter.TenantID,
	)

	if err != nil {
		return fmt.Errorf("failed to update filter: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("filter not found")
	}

	r.logger.Info("updated filter for tenant",
		"tenant_id", filter.TenantID,
		"filter_id", filter.ID)

	return nil
}

// DeleteFilter deletes a filter configuration
func (r *FilterRepository) DeleteFilter(ctx *security.RequestContext, tenantID string, filterID uuid.UUID) error {
	query := `
		DELETE FROM knowledge_graph_tenant_filters
		WHERE id = $1 AND tenant_id = $2
	`

	result, err := r.dbManager.Exec(query, filterID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete filter: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("filter not found")
	}

	r.logger.Info("deleted filter", "filter_id", filterID, "tenant_id", tenantID)
	return nil
}

// GetAllEnabledFilters retrieves all enabled filter configurations across all tenants
// This is used when no specific tenant_id is provided
func (r *FilterRepository) GetAllEnabledFilters(ctx *security.RequestContext) ([]*kgmodels.KnowledgeGraphTenantFilter, error) {
	query := `
		SELECT id, tenant_id, filter_name, account_ids, sources, flow_sources,
		       filters, is_default, enabled, created_at, last_sync_version, last_sync_time
		FROM knowledge_graph_tenant_filters
		WHERE enabled = true
		ORDER BY tenant_id ASC, is_default DESC, filter_name ASC
	`

	rows, err := r.dbManager.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query all enabled filters: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			r.logger.Warn("failed to close rows", "error", closeErr)
		}
	}()

	var filters []*kgmodels.KnowledgeGraphTenantFilter
	for rows.Next() {
		var filter kgmodels.KnowledgeGraphTenantFilter
		var accountIDsJSON, sourcesJSON, flowSourcesJSON, filtersJSON []byte

		err := rows.Scan(
			&filter.ID,
			&filter.TenantID,
			&filter.FilterName,
			&accountIDsJSON,
			&sourcesJSON,
			&flowSourcesJSON,
			&filtersJSON,
			&filter.IsDefault,
			&filter.Enabled,
			&filter.CreatedAt,
			&filter.LastSyncVersion,
			&filter.LastSyncTimestamp,
		)
		if err != nil {
			r.logger.Warn("failed to scan filter row", "error", err)
			continue
		}

		// Parse account_ids JSONB
		if accountIDsJSON != nil {
			if err := json.Unmarshal(accountIDsJSON, &filter.AccountIDs); err != nil {
				r.logger.Warn("failed to unmarshal account_ids JSON", "error", err)
				filter.AccountIDs = []string{}
			}
		} else {
			filter.AccountIDs = []string{}
		}

		// Parse sources JSONB
		if sourcesJSON != nil {
			if err := json.Unmarshal(sourcesJSON, &filter.Sources); err != nil {
				r.logger.Warn("failed to unmarshal sources JSON", "error", err)
				filter.Sources = []string{}
			}
		} else {
			filter.Sources = []string{}
		}

		// Parse flow_sources JSONB
		if flowSourcesJSON != nil {
			if err := json.Unmarshal(flowSourcesJSON, &filter.FlowSources); err != nil {
				r.logger.Warn("failed to unmarshal flow_sources JSON", "error", err)
				filter.FlowSources = []string{}
			}
		} else {
			filter.FlowSources = []string{}
		}

		// Parse filters JSONB
		if filtersJSON != nil {
			if err := json.Unmarshal(filtersJSON, &filter.Filters); err != nil {
				r.logger.Warn("failed to unmarshal filters JSON", "error", err)
				filter.Filters = make(map[string]interface{})
			}
		} else {
			filter.Filters = make(map[string]interface{})
		}

		filters = append(filters, &filter)
	}

	r.logger.Info("retrieved all enabled filters",
		"filter_count", len(filters))

	return filters, nil
}

// UpdateSyncVersion updates the sync version for a tenant filter
func (r *FilterRepository) UpdateSyncVersion(ctx *security.RequestContext, filterID uuid.UUID, newVersion int64) error {
	query := `
		UPDATE knowledge_graph_tenant_filters
		SET last_sync_version = $1,
			last_sync_time = NOW()
		WHERE id = $2
	`

	result, err := r.dbManager.Exec(query, newVersion, filterID)
	if err != nil {
		return fmt.Errorf("failed to update sync version: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("filter not found with id %s", filterID)
	}

	r.logger.Info("updated sync version for filter",
		"filter_id", filterID,
		"new_version", newVersion)

	return nil
}

func (r *FilterRepository) UpdateSyncTimeOnly(ctx *security.RequestContext, filterID uuid.UUID, newVersion int64) error {
	query := `
		UPDATE knowledge_graph_tenant_filters
		SET last_sync_time = NOW()
		WHERE id = $2
	`
	result, err := r.dbManager.Exec(query, newVersion, filterID)
	if err != nil {
		return fmt.Errorf("failed to update sync version: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("filter not found with id %s", filterID)
	}

	r.logger.Info("updated sync version for filter",
		"filter_id", filterID,
		"new_version", newVersion)

	return nil
}

// GetAllEnabledFiltersForTenant retrieves all enabled filter configurations for a specific tenant
func (r *FilterRepository) GetAllEnabledFiltersForTenant(ctx *security.RequestContext, tenantID string) ([]*kgmodels.KnowledgeGraphTenantFilter, error) {
	query := `
		SELECT id, tenant_id, filter_name, account_ids, sources, flow_sources,
		       filters, is_default, enabled, created_at, last_sync_version, last_sync_time
		FROM knowledge_graph_tenant_filters
		WHERE tenant_id = $1 AND enabled = true
		ORDER BY is_default DESC, filter_name ASC
	`

	rows, err := r.dbManager.Query(query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query enabled filters for tenant: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			r.logger.Warn("failed to close rows", "error", closeErr)
		}
	}()

	var filters []*kgmodels.KnowledgeGraphTenantFilter
	for rows.Next() {
		var filter kgmodels.KnowledgeGraphTenantFilter
		var accountIDsJSON, sourcesJSON, flowSourcesJSON, filtersJSON []byte

		err := rows.Scan(
			&filter.ID,
			&filter.TenantID,
			&filter.FilterName,
			&accountIDsJSON,
			&sourcesJSON,
			&flowSourcesJSON,
			&filtersJSON,
			&filter.IsDefault,
			&filter.Enabled,
			&filter.CreatedAt,
			&filter.LastSyncVersion,
			&filter.LastSyncTimestamp,
		)
		if err != nil {
			r.logger.Warn("failed to scan filter row", "error", err)
			continue
		}

		// Parse account_ids JSONB
		if accountIDsJSON != nil {
			if err := json.Unmarshal(accountIDsJSON, &filter.AccountIDs); err != nil {
				r.logger.Warn("failed to unmarshal account_ids JSON", "error", err)
				filter.AccountIDs = []string{}
			}
		} else {
			filter.AccountIDs = []string{}
		}

		// Parse sources JSONB
		if sourcesJSON != nil {
			if err := json.Unmarshal(sourcesJSON, &filter.Sources); err != nil {
				r.logger.Warn("failed to unmarshal sources JSON", "error", err)
				filter.Sources = []string{}
			}
		} else {
			filter.Sources = []string{}
		}

		// Parse flow_sources JSONB
		if flowSourcesJSON != nil {
			if err := json.Unmarshal(flowSourcesJSON, &filter.FlowSources); err != nil {
				r.logger.Warn("failed to unmarshal flow_sources JSON", "error", err)
				filter.FlowSources = []string{}
			}
		} else {
			filter.FlowSources = []string{}
		}

		// Parse filters JSONB
		if filtersJSON != nil {
			if err := json.Unmarshal(filtersJSON, &filter.Filters); err != nil {
				r.logger.Warn("failed to unmarshal filters JSON", "error", err)
				filter.Filters = make(map[string]interface{})
			}
		} else {
			filter.Filters = make(map[string]interface{})
		}

		filters = append(filters, &filter)
	}

	r.logger.Info("retrieved enabled filters for tenant",
		"tenant_id", tenantID,
		"filter_count", len(filters))

	return filters, nil
}

// TryClaimTenantProcessing atomically checks whether KG processing for this tenant
// started within lockDuration and, if not, stamps last_process_started_at = NOW().
//
// It uses SELECT ... FOR UPDATE so that concurrent callers (same or different replicas)
// are serialized: the second caller blocks until the first commits, then reads the fresh
// timestamp and returns false — preventing concurrent same-tenant graph builds that
// cause PostgreSQL deadlocks on knowledge_graph_node upserts.
//
// Returns true if the caller should proceed with processing, false if it should skip.
func (r *FilterRepository) TryClaimTenantProcessing(tenantID string, lockDuration time.Duration) (bool, error) {
	tx, err := r.dbManager.BeginTx()
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction for tenant claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Lock all enabled filter rows for this tenant. FOR UPDATE ensures a concurrent
	// caller blocks here until we commit, then re-reads the fresh last_process_started_at.
	rows, err := tx.Queryx(`
		SELECT id, last_process_started_at
		FROM knowledge_graph_tenant_filters
		WHERE tenant_id = $1 AND enabled = true
		FOR UPDATE
	`, tenantID)
	if err != nil {
		return false, fmt.Errorf("failed to select tenant filters for update: %w", err)
	}

	maxStartedAt, err := scanMaxProcessStartedAt(rows)
	_ = rows.Close()
	if err != nil {
		return false, err
	}

	// If the most recent process start is within lockDuration, skip.
	if maxStartedAt != nil && time.Since(*maxStartedAt) < lockDuration {
		return false, nil
	}

	// Stamp the start time on all enabled filters for this tenant.
	_, err = tx.Exec(`
		UPDATE knowledge_graph_tenant_filters
		SET last_process_started_at = NOW()
		WHERE tenant_id = $1 AND enabled = true
	`, tenantID)
	if err != nil {
		return false, fmt.Errorf("failed to stamp last_process_started_at: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit tenant claim: %w", err)
	}

	return true, nil
}

// scanMaxProcessStartedAt iterates rows from a SELECT on knowledge_graph_tenant_filters
// and returns the most recent last_process_started_at value across all rows.
func scanMaxProcessStartedAt(rows interface {
	Next() bool
	StructScan(dest interface{}) error
	Err() error
}) (*time.Time, error) {
	type filterRow struct {
		ID                   string     `db:"id"`
		LastProcessStartedAt *time.Time `db:"last_process_started_at"`
	}

	var maxStartedAt *time.Time
	for rows.Next() {
		var fr filterRow
		if err := rows.StructScan(&fr); err != nil {
			return nil, fmt.Errorf("failed to scan tenant filter row: %w", err)
		}
		if fr.LastProcessStartedAt != nil && (maxStartedAt == nil || fr.LastProcessStartedAt.After(*maxStartedAt)) {
			maxStartedAt = fr.LastProcessStartedAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate tenant filter rows: %w", err)
	}
	return maxStartedAt, nil
}
