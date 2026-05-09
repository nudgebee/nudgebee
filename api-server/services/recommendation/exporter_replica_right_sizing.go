package recommendation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// ReplicaRightSizingRow represents a single row in the replica right sizing export
type ReplicaRightSizingRow struct {
	AppName             string
	Namespace           string
	CurrentReplicas     int
	RecommendedReplicas int
	Rule                string
	Details             string
	ObservationDuration string
	EstimatedSavings    float64
	UpdatedAt           time.Time
	Status              string
}

// ToStringSlice implements ExportRow interface
func (r *ReplicaRightSizingRow) ToStringSlice() []string {
	return []string{
		r.AppName,
		r.Namespace,
		fmt.Sprintf("%d", r.CurrentReplicas),
		fmt.Sprintf("%d", r.RecommendedReplicas),
		r.Rule,
		r.Details,
		r.ObservationDuration,
		fmt.Sprintf("%.2f", r.EstimatedSavings),
		r.UpdatedAt.Format(time.RFC3339),
		r.Status,
	}
}

// ReplicaRightSizingExporter exports replica right sizing recommendations
type ReplicaRightSizingExporter struct{}

// GetColumns returns column definitions for replica right sizing
func (e *ReplicaRightSizingExporter) GetColumns() []ColumnDefinition {
	return []ColumnDefinition{
		{Name: "App Name", Width: 20},
		{Name: "Namespace", Width: 15},
		{Name: "Current Replicas", Width: 15},
		{Name: "Recommended Replicas", Width: 20},
		{Name: "Rule", Width: 20},
		{Name: "Details", Width: 40},
		{Name: "Observation Duration", Width: 20},
		{Name: "Estimated Savings ($)", Width: 18},
		{Name: "Updated At", Width: 20},
		{Name: "Status", Width: 12},
	}
}

// ValidateFilters validates filters for replica right sizing
func (e *ReplicaRightSizingExporter) ValidateFilters(filters ExportFilters) error {
	if filters.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if filters.Category != "RightSizing" {
		return fmt.Errorf("category must be RightSizing for replica right sizing export")
	}
	if filters.RuleName != "replica_right_sizing" {
		return fmt.Errorf("rule_name must be replica_right_sizing")
	}
	return nil
}

// FetchData fetches and processes replica right sizing recommendations
func (e *ReplicaRightSizingExporter) FetchData(
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
		query += fmt.Sprintf(" AND r.recommendation->>'metadata'->>'namespace' = $%d", argCount)
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
		appName := ""
		namespace := ""
		if metadata, ok := recData["metadata"].(map[string]interface{}); ok {
			if name, ok := metadata["name"].(string); ok {
				appName = name
			}
			if ns, ok := metadata["namespace"].(string); ok {
				namespace = ns
			}
		}

		// Extract recommendation details
		currentReplicas := 0
		recommendedReplicas := 0
		recommendedType := ""
		details := ""
		duration := 7 // Default duration in days

		if rec, ok := recData["recommendation"].(map[string]interface{}); ok {
			// Get current replicas
			if allocatedReplica, ok := rec["allocated_replica"].(float64); ok {
				currentReplicas = int(allocatedReplica)
			} else if allocated, ok := rec["allocated"].([]interface{}); ok && len(allocated) > 0 {
				if lastItem, ok := allocated[len(allocated)-1].(map[string]interface{}); ok {
					if replicas, ok := lastItem["replicas"].(float64); ok {
						currentReplicas = int(replicas)
					}
				}
			}

			// Get recommended replicas
			if recommendedReplica, ok := rec["recommended_replica"].(float64); ok {
				recommendedReplicas = int(recommendedReplica)
			} else if recommended, ok := rec["recommended"].([]interface{}); ok && len(recommended) > 0 {
				// Get the next hour recommendation (similar to UI logic)
				now := time.Now().UTC()
				nextHour := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, 0, 0, 0, time.UTC)
				targetTimestamp := nextHour.Format("2006-01-02 15:04:05")

				for _, item := range recommended {
					if recItem, ok := item.(map[string]interface{}); ok {
						if timestamp, ok := recItem["timestamp"].(string); ok && timestamp == targetTimestamp {
							if replicas, ok := recItem["replicas"].(float64); ok {
								recommendedReplicas = int(replicas)
								break
							}
						}
					}
				}
				// If no match found, use first recommendation
				if recommendedReplicas == 0 && len(recommended) > 0 {
					if firstItem, ok := recommended[0].(map[string]interface{}); ok {
						if replicas, ok := firstItem["replicas"].(float64); ok {
							recommendedReplicas = int(replicas)
						}
					}
				}
			}

			// Get recommended type (rule)
			if recType, ok := rec["recommended_type"].(string); ok {
				recommendedType = recType
			}

			// Get duration
			if dur, ok := rec["duration"].(float64); ok {
				duration = int(dur)
			}
		}

		// Get duration from top-level if available
		if dur, ok := recData["duration"].(float64); ok {
			duration = int(dur)
		}

		// Format rule name
		ruleName := getRecommendedTypeText(recommendedType)

		// Create details text
		if currentReplicas > recommendedReplicas {
			details = fmt.Sprintf("Application is over-provisioned. Reduce replicas from %d to %d", currentReplicas, recommendedReplicas)
		} else if currentReplicas < recommendedReplicas {
			details = fmt.Sprintf("Application usage is higher than configured. Recommended to increase replicas from %d to %d or enable HPA", currentReplicas, recommendedReplicas)
		} else {
			details = "Application is optimally configured"
		}

		row := &ReplicaRightSizingRow{
			AppName:             appName,
			Namespace:           namespace,
			CurrentReplicas:     currentReplicas,
			RecommendedReplicas: recommendedReplicas,
			Rule:                ruleName,
			Details:             details,
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

// getRecommendedTypeText converts recommendation type to readable text
func getRecommendedTypeText(recommendedType string) string {
	switch recommendedType {
	case "SPOT_INSTANCE_DEPLOYMENT":
		return "Spot Deployment"
	case "NB_ML":
		return "Replica RightSizing"
	default:
		return "Usage"
	}
}
