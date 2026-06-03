package event

import (
	"context"
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"sync"

	"github.com/lib/pq"
	"golang.org/x/sync/errgroup"
)

const (
	defaultFilterLimit = 500
	maxFilterLimit     = 1000
)

// SQL query templates for each filter type - WITH COUNT (slower)
const (
	queryNamespaceFilterWithCount = `
		SELECT subject_namespace as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND subject_namespace IS NOT NULL
		  AND subject_namespace != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY subject_namespace
		ORDER BY count DESC, value ASC
		LIMIT $5
	`

	queryWorkloadFilterWithCount = `
		SELECT subject_owner as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND subject_owner IS NOT NULL
		  AND subject_owner != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY subject_owner
		ORDER BY count DESC, value ASC
		LIMIT $5
	`

	querySubjectTypeFilterWithCount = `
		SELECT subject_type as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND subject_type IS NOT NULL
		  AND subject_type != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY subject_type
		ORDER BY count DESC, value ASC
		LIMIT $5
	`

	queryAggregationKeyFilterWithCount = `
		SELECT aggregation_key as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND aggregation_key IS NOT NULL
		  AND aggregation_key != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY aggregation_key
		ORDER BY count DESC, value ASC
		LIMIT $5
	`

	querySourceFilterWithCount = `
		SELECT source as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND source IS NOT NULL
		  AND source != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY source
		ORDER BY count DESC, value ASC
		LIMIT $5
	`

	queryPriorityFilterWithCount = `
		SELECT priority as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND priority IS NOT NULL
		  AND priority != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY priority
		ORDER BY
		  CASE priority
		    WHEN 'HIGH' THEN 1
		    WHEN 'MEDIUM' THEN 2
		    WHEN 'LOW' THEN 3
		    WHEN 'INFO' THEN 4
		    WHEN 'DEBUG' THEN 5
		    ELSE 6
		  END
		LIMIT $5
	`

	queryNBStatusFilterWithCount = `
		SELECT nb_status as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND nb_status IS NOT NULL
		  AND nb_status != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY nb_status
		ORDER BY count DESC, value ASC
		LIMIT $5
	`

	queryClusterFilterWithCount = `
		SELECT cluster as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND cluster IS NOT NULL
		  AND cluster != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY cluster
		ORDER BY count DESC, value ASC
		LIMIT $5
	`

	queryLabelValuesFilterWithCount = `
		SELECT labels->>$6 as value, COUNT(*) as count
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND labels IS NOT NULL
		  AND jsonb_typeof(labels) = 'object'
		  AND jsonb_exists(labels, $6)
		  AND labels->>$6 IS NOT NULL
		  AND labels->>$6 != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		GROUP BY labels->>$6
		ORDER BY count DESC, value ASC
		LIMIT $5
	`
)

// SQL query templates for each filter type - WITHOUT COUNT (faster, uses DISTINCT)
const (
	queryNamespaceFilter = `
		SELECT DISTINCT subject_namespace as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND subject_namespace IS NOT NULL
		  AND subject_namespace != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	queryWorkloadFilter = `
		SELECT DISTINCT subject_owner as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND subject_owner IS NOT NULL
		  AND subject_owner != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	querySubjectTypeFilter = `
		SELECT DISTINCT subject_type as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND subject_type IS NOT NULL
		  AND subject_type != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	queryAggregationKeyFilter = `
		SELECT DISTINCT aggregation_key as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND aggregation_key IS NOT NULL
		  AND aggregation_key != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	querySourceFilter = `
		SELECT DISTINCT source as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND source IS NOT NULL
		  AND source != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	queryPriorityFilter = `
		SELECT DISTINCT priority as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND priority IS NOT NULL
		  AND priority != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY
		  CASE priority
		    WHEN 'HIGH' THEN 1
		    WHEN 'MEDIUM' THEN 2
		    WHEN 'LOW' THEN 3
		    WHEN 'INFO' THEN 4
		    WHEN 'DEBUG' THEN 5
		    ELSE 6
		  END
		LIMIT $5
	`

	queryNBStatusFilter = `
		SELECT DISTINCT nb_status as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND nb_status IS NOT NULL
		  AND nb_status != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	queryClusterFilter = `
		SELECT DISTINCT cluster as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND cluster IS NOT NULL
		  AND cluster != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	queryLabelKeysFilter = `
		SELECT DISTINCT jsonb_object_keys(labels) as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND labels IS NOT NULL
		  AND labels != '{}'::jsonb
		  AND jsonb_typeof(labels) = 'object'
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`

	queryLabelValuesFilter = `
		SELECT DISTINCT labels->>$6 as value
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = ANY($2)
		  AND labels IS NOT NULL
		  AND jsonb_typeof(labels) = 'object'
		  AND jsonb_exists(labels, $6)
		  AND labels->>$6 IS NOT NULL
		  AND labels->>$6 != ''
		  AND ($3::timestamp IS NULL OR created_at >= $3)
		  AND ($4::timestamp IS NULL OR created_at <= $4)
		ORDER BY value ASC
		LIMIT $5
	`
)

// GetEventFilterValues retrieves filter values based on the request
func GetEventFilterValues(ctx *security.RequestContext, req GetEventFilterValuesRequest) (*GetEventFilterValuesResponse, error) {
	// Validate request
	if err := validateFilterRequest(req); err != nil {
		return nil, err
	}

	// Get accessible account IDs
	accountIDs, err := getAccessibleAccountIDs(ctx, req.AccountID)
	if err != nil {
		return nil, err
	}

	if len(accountIDs) == 0 {
		return nil, fmt.Errorf("no accessible accounts found")
	}

	// Get database manager
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Set defaults
	limit := defaultFilterLimit
	if req.Limit != nil && *req.Limit > 0 && *req.Limit <= maxFilterLimit {
		limit = *req.Limit
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	// Build response with parallel execution
	results := make([]FilterResult, len(req.FilterTypes))
	var mu sync.Mutex

	g, gCtx := errgroup.WithContext(context.Background())

	for i, filterType := range req.FilterTypes {
		i, filterType := i, filterType // capture loop variables

		g.Go(func() error {
			result, err := executeFilterQuery(gCtx, dbms, tenantID, accountIDs, filterType, req, limit)
			if err != nil {
				// Log error but don't fail - return empty result
				ctx.GetLogger().Error("filter query failed", "type", filterType, "error", err)
				result = &FilterResult{FilterType: filterType, Values: []FilterValueItem{}, Total: 0}
			}

			mu.Lock()
			results[i] = *result
			mu.Unlock()
			return nil
		})
	}

	_ = g.Wait() // Errors are logged, not returned

	return &GetEventFilterValuesResponse{
		Filters:   results,
		AccountID: req.AccountID,
	}, nil
}

// validateFilterRequest validates the request parameters
func validateFilterRequest(req GetEventFilterValuesRequest) error {
	if len(req.FilterTypes) == 0 {
		return fmt.Errorf("at least one filter_type is required")
	}

	// Validate filter types
	hasLabelValue := false
	for _, ft := range req.FilterTypes {
		if !IsValidFilterType(ft) {
			return fmt.Errorf("invalid filter_type: %s", ft)
		}
		if ft == EventFilterTypeLabelValue {
			hasLabelValue = true
		}
	}

	// label_key required when requesting label_value
	if hasLabelValue && (req.LabelKey == nil || *req.LabelKey == "") {
		return fmt.Errorf("label_key is required when filter_types includes 'label_value'")
	}

	return nil
}

// getAccessibleAccountIDs returns the list of account IDs the user can access
func getAccessibleAccountIDs(ctx *security.RequestContext, requestedAccountID *string) ([]string, error) {
	sc := ctx.GetSecurityContext()

	// If specific account requested, verify access
	if requestedAccountID != nil && *requestedAccountID != "" {
		if !sc.HasAccountAccess(*requestedAccountID, security.SecurityAccessTypeRead) {
			return nil, fmt.Errorf("access denied to account: %s", *requestedAccountID)
		}
		return []string{*requestedAccountID}, nil
	}

	// Return all accessible accounts
	return sc.ListAccountIds(), nil
}

// executeFilterQuery executes the appropriate query for the filter type
func executeFilterQuery(
	ctx context.Context,
	dbms *database.DatabaseManager,
	tenantID string,
	accountIDs []string,
	filterType EventFilterType,
	req GetEventFilterValuesRequest,
	limit int,
) (*FilterResult, error) {

	var query string
	var args []interface{}

	// Check if counts are requested (default: false for better performance)
	includeCount := req.IncludeCount != nil && *req.IncludeCount

	baseArgs := []interface{}{
		tenantID,
		pq.Array(accountIDs),
		req.StartTime,
		req.EndTime,
		limit,
	}

	// Select query based on filter type and whether counts are needed
	switch filterType {
	case EventFilterTypeNamespace:
		if includeCount {
			query = queryNamespaceFilterWithCount
		} else {
			query = queryNamespaceFilter
		}
		args = baseArgs
	case EventFilterTypeWorkload:
		if includeCount {
			query = queryWorkloadFilterWithCount
		} else {
			query = queryWorkloadFilter
		}
		args = baseArgs
	case EventFilterTypeSubjectType:
		if includeCount {
			query = querySubjectTypeFilterWithCount
		} else {
			query = querySubjectTypeFilter
		}
		args = baseArgs
	case EventFilterTypeAggregationKey:
		if includeCount {
			query = queryAggregationKeyFilterWithCount
		} else {
			query = queryAggregationKeyFilter
		}
		args = baseArgs
	case EventFilterTypeSource:
		if includeCount {
			query = querySourceFilterWithCount
		} else {
			query = querySourceFilter
		}
		args = baseArgs
	case EventFilterTypePriority:
		if includeCount {
			query = queryPriorityFilterWithCount
		} else {
			query = queryPriorityFilter
		}
		args = baseArgs
	case EventFilterTypeNBStatus:
		if includeCount {
			query = queryNBStatusFilterWithCount
		} else {
			query = queryNBStatusFilter
		}
		args = baseArgs
	case EventFilterTypeCluster:
		if includeCount {
			query = queryClusterFilterWithCount
		} else {
			query = queryClusterFilter
		}
		args = baseArgs
	case EventFilterTypeLabelKey:
		// Label keys never have count (uses jsonb_object_keys)
		query = queryLabelKeysFilter
		args = baseArgs
	case EventFilterTypeLabelValue:
		if includeCount {
			query = queryLabelValuesFilterWithCount
		} else {
			query = queryLabelValuesFilter
		}
		args = append(baseArgs, *req.LabelKey)
	default:
		return nil, fmt.Errorf("unsupported filter type: %s", filterType)
	}

	// Execute query
	rows, err := dbms.Db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	values := make([]FilterValueItem, 0)
	for rows.Next() {
		var item FilterValueItem
		if includeCount && filterType != EventFilterTypeLabelKey {
			// Query returns value and count
			if err := rows.Scan(&item.Value, &item.Count); err != nil {
				return nil, fmt.Errorf("failed to scan row: %w", err)
			}
		} else {
			// Query returns only value (DISTINCT queries or label_key)
			var value string
			if err := rows.Scan(&value); err != nil {
				return nil, fmt.Errorf("failed to scan row: %w", err)
			}
			item = FilterValueItem{Value: value, Count: 0}
		}
		values = append(values, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return &FilterResult{
		FilterType: filterType,
		Values:     values,
		Total:      len(values),
	}, nil
}
