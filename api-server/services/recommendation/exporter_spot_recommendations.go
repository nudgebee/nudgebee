package recommendation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// SpotRecommendationsRow represents a single row in the spot recommendations export
type SpotRecommendationsRow struct {
	Application      string
	Type             string
	Namespace        string
	EstimatedSavings float64
	UpdatedAt        time.Time
	Status           string
}

// ToStringSlice implements ExportRow interface
func (r *SpotRecommendationsRow) ToStringSlice() []string {
	return []string{
		r.Application,
		r.Type,
		r.Namespace,
		fmt.Sprintf("%.2f", r.EstimatedSavings),
		r.UpdatedAt.Format(time.RFC3339),
		r.Status,
	}
}

// SpotRecommendationsExporter exports spot instance recommendations
type SpotRecommendationsExporter struct{}

// GetColumns returns column definitions for spot recommendations
func (e *SpotRecommendationsExporter) GetColumns() []ColumnDefinition {
	return []ColumnDefinition{
		{Name: "Application", Width: 30},
		{Name: "Type", Width: 15},
		{Name: "Namespace", Width: 20},
		{Name: "Estimated Savings ($)", Width: 18},
		{Name: "Updated At", Width: 20},
		{Name: "Status", Width: 12},
	}
}

// ValidateFilters validates filters for spot recommendations
func (e *SpotRecommendationsExporter) ValidateFilters(filters ExportFilters) error {
	if filters.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if filters.Category != "K8sSpotRecommendation" {
		return fmt.Errorf("category must be K8sSpotRecommendation for spot recommendations export")
	}
	// Note: Spot recommendations may not have a specific rule_name
	return nil
}

// FetchData fetches and processes spot instance recommendations
func (e *SpotRecommendationsExporter) FetchData(
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
			r.recommendation->>'namespace',
			ca.account_name
		FROM recommendation r
		LEFT JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
		WHERE
			r.cloud_account_id = $1
			AND r.category = $2
	`

	args := []interface{}{filters.AccountID, filters.Category}
	argCount := 2

	// Add optional filters
	if filters.Namespace != nil && *filters.Namespace != "" {
		argCount++
		query += fmt.Sprintf(" AND r.recommendation->>'namespace' = $%d", argCount)
		args = append(args, *filters.Namespace)
	}

	if filters.WorkloadType != nil && *filters.WorkloadType != "" {
		argCount++
		query += fmt.Sprintf(" AND r.recommendation->>'type' = $%d", argCount)
		args = append(args, *filters.WorkloadType)
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
		var namespace *string
		var accName *string

		err := rows.Scan(
			&recommendationJSON,
			&estimatedSavings,
			&status,
			&updatedAt,
			&namespace,
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
		application := ""
		workloadType := "Job" // Default type

		if err := json.Unmarshal(recommendationJSON, &recData); err == nil {
			if controllerName, ok := recData["controller_name"].(string); ok {
				application = controllerName
			}
			if recType, ok := recData["type"].(string); ok && recType != "" {
				workloadType = recType
			}
		}

		ns := ""
		if namespace != nil {
			ns = *namespace
		}

		row := &SpotRecommendationsRow{
			Application:      application,
			Type:             workloadType,
			Namespace:        ns,
			EstimatedSavings: estimatedSavings,
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
