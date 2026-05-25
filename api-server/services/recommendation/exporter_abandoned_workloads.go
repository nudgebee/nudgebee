package recommendation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// AbandonedWorkloadsRow represents a single row in the abandoned workloads export
type AbandonedWorkloadsRow struct {
	ResourceName        string
	ObjectType          string
	Namespace           string
	CurrentTraffic      float64
	TrafficThreshold    float64
	ObservationDuration string
	EstimatedSavings    float64
	UpdatedAt           time.Time
	Status              string
}

// ToStringSlice implements ExportRow interface
func (r *AbandonedWorkloadsRow) ToStringSlice() []string {
	return []string{
		r.ResourceName,
		r.ObjectType,
		r.Namespace,
		fmt.Sprintf("%.2f", r.CurrentTraffic),
		fmt.Sprintf("%.2f", r.TrafficThreshold),
		r.ObservationDuration,
		fmt.Sprintf("%.2f", r.EstimatedSavings),
		r.UpdatedAt.Format(time.RFC3339),
		r.Status,
	}
}

// AbandonedWorkloadsExporter exports abandoned workloads recommendations
type AbandonedWorkloadsExporter struct{}

// GetColumns returns column definitions for abandoned workloads
func (e *AbandonedWorkloadsExporter) GetColumns() []ColumnDefinition {
	return []ColumnDefinition{
		{Name: "Resource Name", Width: 25},
		{Name: "Object Type", Width: 15},
		{Name: "Namespace", Width: 20},
		{Name: "Current Traffic (bytes)", Width: 22},
		{Name: "Traffic Threshold (bytes)", Width: 22},
		{Name: "Observation Duration", Width: 20},
		{Name: "Estimated Savings ($)", Width: 18},
		{Name: "Updated At", Width: 20},
		{Name: "Status", Width: 12},
	}
}

// ValidateFilters validates filters for abandoned workloads
func (e *AbandonedWorkloadsExporter) ValidateFilters(filters ExportFilters) error {
	if filters.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if filters.Category != "RightSizing" {
		return fmt.Errorf("category must be RightSizing for abandoned workloads export")
	}
	if filters.RuleName != "abandoned_resource" {
		return fmt.Errorf("rule_name must be abandoned_resource")
	}
	return nil
}

// FetchData fetches and processes abandoned workloads recommendations
func (e *AbandonedWorkloadsExporter) FetchData(
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
			cr.name,
			ca.account_name,
			cr.meta
		FROM recommendation r
		LEFT JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
		LEFT JOIN cloud_resourses cr ON r.resource_id = cr.id
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
		query += fmt.Sprintf(" AND cr.meta->>'namespace' = $%d", argCount)
		args = append(args, *filters.Namespace)
	}

	if filters.WorkloadType != nil && *filters.WorkloadType != "" {
		argCount++
		query += fmt.Sprintf(" AND cr.meta->>'controllerKind' = $%d", argCount)
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
		var resourceName *string
		var accName *string
		var metaJSON []byte

		err := rows.Scan(
			&recommendationJSON,
			&estimatedSavings,
			&status,
			&updatedAt,
			&resourceName,
			&accName,
			&metaJSON,
		)
		if err != nil {
			ctx.GetLogger().Error("Failed to scan recommendation row", "error", err)
			continue
		}

		if accName != nil {
			accountName = *accName
		}

		// Parse meta JSON to get namespace and object type
		var meta map[string]interface{}
		namespace := ""
		objectType := ""
		workloadName := ""

		if metaJSON != nil {
			if err := json.Unmarshal(metaJSON, &meta); err == nil {
				if ns, ok := meta["namespace"].(string); ok {
					namespace = ns
				}
				if kind, ok := meta["controllerKind"].(string); ok {
					objectType = kind
				}
				if controller, ok := meta["controller"].(string); ok {
					workloadName = controller
				}
				// Fallback to labels if controller not found
				if workloadName == "" {
					if config, ok := meta["config"].(map[string]interface{}); ok {
						if labels, ok := config["labels"].(map[string]interface{}); ok {
							if appName, ok := labels["app.kubernetes.io/name"].(string); ok {
								workloadName = appName
							}
						}
					}
				}
			}
		}

		// Use resource_name if workloadName not found in meta
		if workloadName == "" && resourceName != nil {
			workloadName = *resourceName
		}

		// Parse recommendation JSON
		var recData map[string]interface{}
		currentTraffic := 0.0
		threshold := 0.0
		duration := 7 // Default duration in days

		if err := json.Unmarshal(recommendationJSON, &recData); err == nil {
			if traffic, ok := recData["traffic"].(float64); ok {
				currentTraffic = traffic
			}
			if thresh, ok := recData["threshold"].(float64); ok {
				threshold = thresh
			}
			if dur, ok := recData["duration"].(float64); ok {
				duration = int(dur)
			}
		}

		row := &AbandonedWorkloadsRow{
			ResourceName:        workloadName,
			ObjectType:          objectType,
			Namespace:           namespace,
			CurrentTraffic:      currentTraffic,
			TrafficThreshold:    threshold,
			ObservationDuration: fmt.Sprintf("%d days", duration),
			EstimatedSavings:    estimatedSavings,
			UpdatedAt:           updatedAt,
			Status:              status,
		}

		exportRows = append(exportRows, row)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("error iterating recommendation rows: %w", err)
	}

	return exportRows, accountName, nil
}
