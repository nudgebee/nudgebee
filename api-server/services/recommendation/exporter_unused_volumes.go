package recommendation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// UnusedVolumesRow represents a single row in the unused volumes export
type UnusedVolumesRow struct {
	Name             string
	LastNamespace    string
	LastClaim        string
	Size             string
	EstimatedSavings float64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Status           string
}

// ToStringSlice implements ExportRow interface
func (r *UnusedVolumesRow) ToStringSlice() []string {
	return []string{
		r.Name,
		r.LastNamespace,
		r.LastClaim,
		r.Size,
		fmt.Sprintf("%.2f", r.EstimatedSavings),
		r.CreatedAt.Format(time.RFC3339),
		r.UpdatedAt.Format(time.RFC3339),
		r.Status,
	}
}

// UnusedVolumesExporter exports unused volumes recommendations
type UnusedVolumesExporter struct{}

// GetColumns returns column definitions for unused volumes
func (e *UnusedVolumesExporter) GetColumns() []ColumnDefinition {
	return []ColumnDefinition{
		{Name: "Name", Width: 30},
		{Name: "Last Namespace", Width: 20},
		{Name: "Last Claim", Width: 25},
		{Name: "Size", Width: 12},
		{Name: "Estimated Savings ($)", Width: 18},
		{Name: "Created At", Width: 20},
		{Name: "Updated At", Width: 20},
		{Name: "Status", Width: 12},
	}
}

// ValidateFilters validates filters for unused volumes
func (e *UnusedVolumesExporter) ValidateFilters(filters ExportFilters) error {
	if filters.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if filters.Category != "RightSizing" {
		return fmt.Errorf("category must be RightSizing for unused volumes export")
	}
	if filters.RuleName != "unused_pvc" {
		return fmt.Errorf("rule_name must be unused_pvc")
	}
	return nil
}

// FetchData fetches and processes unused volumes recommendations
func (e *UnusedVolumesExporter) FetchData(
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

		// Extract metadata
		name := ""
		createdAtStr := ""
		if metadata, ok := recData["metadata"].(map[string]interface{}); ok {
			if n, ok := metadata["name"].(string); ok {
				name = n
			}
			if created, ok := metadata["creationTimestamp"].(string); ok {
				createdAtStr = created
			}
		}

		// Parse creation timestamp
		createdAt := time.Time{}
		if createdAtStr != "" {
			if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
				createdAt = t
			}
		}

		// Extract spec details
		lastNamespace := ""
		lastClaim := ""
		size := ""
		if spec, ok := recData["spec"].(map[string]interface{}); ok {
			if claimRef, ok := spec["claimRef"].(map[string]interface{}); ok {
				if ns, ok := claimRef["namespace"].(string); ok {
					lastNamespace = ns
				}
				if claim, ok := claimRef["name"].(string); ok {
					lastClaim = claim
				}
			}
			if capacity, ok := spec["capacity"].(map[string]interface{}); ok {
				if storage, ok := capacity["storage"].(string); ok {
					size = storage
				}
			}
		}

		row := &UnusedVolumesRow{
			Name:             name,
			LastNamespace:    lastNamespace,
			LastClaim:        lastClaim,
			Size:             size,
			EstimatedSavings: estimatedSavings,
			CreatedAt:        createdAt,
			UpdatedAt:        updatedAt,
			Status:           status,
		}

		exportRows = append(exportRows, row)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("error iterating recommendation rows: %w", err)
	}

	return exportRows, accountName, nil
}
