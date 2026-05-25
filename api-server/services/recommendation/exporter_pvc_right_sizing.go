package recommendation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// PVCRightSizingRow represents a single row in the PVC right sizing export
type PVCRightSizingRow struct {
	PVCName               string
	Namespace             string
	CurrentAllocation     float64 // in GB
	CurrentUsage          float64 // in GB
	RecommendedAllocation float64 // in GB
	ObservationDuration   string
	EstimatedSavings      float64
	UpdatedAt             time.Time
	Status                string
}

// ToStringSlice implements ExportRow interface
func (r *PVCRightSizingRow) ToStringSlice() []string {
	return []string{
		r.PVCName,
		r.Namespace,
		fmt.Sprintf("%.2f GB", r.CurrentAllocation),
		fmt.Sprintf("%.2f GB", r.CurrentUsage),
		fmt.Sprintf("%.2f GB", r.RecommendedAllocation),
		r.ObservationDuration,
		fmt.Sprintf("%.2f", r.EstimatedSavings),
		r.UpdatedAt.Format(time.RFC3339),
		r.Status,
	}
}

// PVCRightSizingExporter exports PVC right sizing recommendations
type PVCRightSizingExporter struct{}

// GetColumns returns column definitions for PVC right sizing
func (e *PVCRightSizingExporter) GetColumns() []ColumnDefinition {
	return []ColumnDefinition{
		{Name: "PVC Name", Width: 25},
		{Name: "Namespace", Width: 20},
		{Name: "Current Allocation", Width: 20},
		{Name: "Current Usage", Width: 20},
		{Name: "Recommended Allocation", Width: 22},
		{Name: "Observation Duration", Width: 20},
		{Name: "Estimated Savings ($)", Width: 18},
		{Name: "Updated At", Width: 20},
		{Name: "Status", Width: 12},
	}
}

// ValidateFilters validates filters for PVC right sizing
func (e *PVCRightSizingExporter) ValidateFilters(filters ExportFilters) error {
	if filters.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if filters.Category != "RightSizing" {
		return fmt.Errorf("category must be RightSizing for PVC right sizing export")
	}
	if filters.RuleName != "pv_rightsize" {
		return fmt.Errorf("rule_name must be pv_rightsize")
	}
	return nil
}

// FetchData fetches and processes PVC right sizing recommendations
func (e *PVCRightSizingExporter) FetchData(
	ctx *security.RequestContext,
	dbms *database.DatabaseManager,
	filters ExportFilters,
) ([]ExportRow, string, error) {
	query := `
		SELECT
			r.recommendation,
			r.estimated_savings,
			r.status,
			r.updated_at,
			ca.account_name
		FROM recommendation r
		LEFT JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
		WHERE
			r.cloud_account_id = $1
			AND r.category = $2
			AND r.rule_name = $3
	`

	args := []interface{}{filters.AccountID, filters.Category, filters.RuleName}
	argCount := 3

	// Add optional filters
	if filters.Namespace != nil && *filters.Namespace != "" {
		argCount++
		// Namespace is in recommendation JSON
		query += fmt.Sprintf(" AND r.recommendation->'spec'->'claimRef'->>'namespace' = $%d", argCount)
		args = append(args, *filters.Namespace)
	}

	if len(filters.Status) > 0 {
		argCount++
		query += fmt.Sprintf(" AND r.status = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Status))
	}

	query += " ORDER BY r.updated_at DESC"

	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		ctx.GetLogger().Error("Failed to query recommendations", "error", err)
		return nil, "", err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close rows", "error", err)
		}
	}()

	exportRows := make([]ExportRow, 0, 64)
	var accountName string

	for rows.Next() {
		var recommendationJSON []byte
		var estimatedSavings float64
		var status string
		var updatedAt time.Time
		var accName *string

		err := rows.Scan(
			&recommendationJSON,
			&estimatedSavings,
			&status,
			&updatedAt,
			&accName,
		)
		if err != nil {
			ctx.GetLogger().Error("Failed to scan recommendation row", "error", err)
			continue
		}

		if accName != nil {
			accountName = *accName
		}

		// Parse recommendation JSON
		var recData map[string]interface{}
		if err := json.Unmarshal(recommendationJSON, &recData); err != nil {
			ctx.GetLogger().Error("Failed to parse recommendation JSON", "error", err)
			continue
		}

		// Extract spec details
		pvcName := ""
		namespace := ""
		if spec, ok := recData["spec"].(map[string]interface{}); ok {
			if claimRef, ok := spec["claimRef"].(map[string]interface{}); ok {
				if name, ok := claimRef["name"].(string); ok {
					pvcName = name
				}
				if ns, ok := claimRef["namespace"].(string); ok {
					namespace = ns
				}
			}
		}

		// Extract recommendation metrics
		currentAllocation := 0.0 // in GB
		currentUsage := 0.0      // in GB
		recommendedSize := 0.0   // in GB
		duration := 7            // Default duration in days

		if rec, ok := recData["recommendation"].(map[string]interface{}); ok {
			// Current capacity (convert from bytes to GB)
			if capacity, ok := rec["capacity"].(float64); ok {
				currentAllocation = capacity / (1024 * 1024 * 1024)
			}

			// Current usage (convert from bytes to GB)
			if usage, ok := rec["usage"].(map[string]interface{}); ok {
				if current, ok := usage["current"].(float64); ok {
					currentUsage = current / (1024 * 1024 * 1024)
				}
			}

			// Recommended size (already in GB)
			if recommended, ok := rec["recommend_size"].(float64); ok {
				recommendedSize = recommended
			}
		}

		// Get duration from top-level
		if dur, ok := recData["duration"].(float64); ok {
			duration = int(dur)
		}

		row := &PVCRightSizingRow{
			PVCName:               pvcName,
			Namespace:             namespace,
			CurrentAllocation:     currentAllocation,
			CurrentUsage:          currentUsage,
			RecommendedAllocation: recommendedSize,
			ObservationDuration:   fmt.Sprintf("%d days", duration),
			EstimatedSavings:      estimatedSavings,
			UpdatedAt:             updatedAt,
			Status:                status,
		}

		exportRows = append(exportRows, row)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("error iterating recommendation rows: %w", err)
	}

	return exportRows, accountName, nil
}
