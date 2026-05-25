package recommendation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// PodRightSizingRow represents a single row in the pod right sizing export
type PodRightSizingRow struct {
	WorkloadName      string
	PodName           string
	Namespace         string
	Kind              string
	CPURequest        float64
	CPURecommended    float64
	CPUSavings        float64
	MemoryRequest     float64
	MemoryRecommended float64
	MemorySavings     float64
	MonthlySavings    float64
	UpdatedAt         time.Time
	Status            string
	AccountName       string
}

// ToStringSlice implements ExportRow interface
func (r *PodRightSizingRow) ToStringSlice() []string {
	return []string{
		r.WorkloadName,
		r.PodName,
		r.Namespace,
		r.Kind,
		fmt.Sprintf("%.3f", r.CPURequest),
		fmt.Sprintf("%.3f", r.CPURecommended),
		fmt.Sprintf("%.2f", r.CPUSavings),
		fmt.Sprintf("%.2f", r.MemoryRequest),
		fmt.Sprintf("%.2f", r.MemoryRecommended),
		fmt.Sprintf("%.2f", r.MemorySavings),
		fmt.Sprintf("%.2f", r.MonthlySavings),
		r.UpdatedAt.Format(time.RFC3339),
		r.Status,
	}
}

// PodRightSizingExporter exports pod right sizing recommendations
type PodRightSizingExporter struct{}

// GetColumns returns column definitions for pod right sizing
func (e *PodRightSizingExporter) GetColumns() []ColumnDefinition {
	return []ColumnDefinition{
		{Name: "Workload Name", Width: 20},
		{Name: "Container Name", Width: 25},
		{Name: "Namespace", Width: 15},
		{Name: "Kind", Width: 12},
		{Name: "CPU Request (cores)", Width: 18},
		{Name: "CPU Recommended (cores)", Width: 18},
		{Name: "CPU Savings (%)", Width: 18},
		{Name: "Memory Request (MB)", Width: 18},
		{Name: "Memory Recommended (MB)", Width: 18},
		{Name: "Memory Savings (%)", Width: 18},
		{Name: "Monthly Savings ($)", Width: 15},
		{Name: "Updated At", Width: 20},
		{Name: "Status", Width: 12},
	}
}

// ValidateFilters validates filters for pod right sizing
func (e *PodRightSizingExporter) ValidateFilters(filters ExportFilters) error {
	if filters.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if filters.Category != "RightSizing" {
		return fmt.Errorf("category must be RightSizing for pod right sizing export")
	}
	if filters.RuleName != "pod_right_sizing" {
		return fmt.Errorf("rule_name must be pod_right_sizing")
	}
	return nil
}

// FetchData fetches and processes pod right sizing recommendations
func (e *PodRightSizingExporter) FetchData(
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
			r.account_object_id,
			ca.account_name,
			COALESCE(kw.name, cr.meta->>'controller', cr.meta->'config'->>'controller') as workload_name,
			COALESCE(kw.namespace, cr.meta->>'namespace', cr.meta->'config'->>'namespace') as namespace,
			COALESCE(kw.kind, cr.meta->>'controllerKind', cr.meta->'config'->>'controllerKind') as kind
		FROM recommendation r
		LEFT JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
		LEFT JOIN cloud_resourses cr ON r.resource_id = cr.id
		LEFT JOIN k8s_workloads kw ON r.resource_id = kw.cloud_resource_id
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
		query += fmt.Sprintf(" AND COALESCE(kw.namespace, cr.meta->>'namespace', cr.meta->'config'->>'namespace') = $%d", argCount)
		args = append(args, *filters.Namespace)
	}

	if filters.WorkloadType != nil && *filters.WorkloadType != "" {
		argCount++
		query += fmt.Sprintf(" AND COALESCE(kw.kind, cr.meta->>'controllerKind', cr.meta->'config'->>'controllerKind') = $%d", argCount)
		args = append(args, *filters.WorkloadType)
	}

	if filters.WorkloadName != nil && *filters.WorkloadName != "" {
		argCount++
		query += fmt.Sprintf(" AND COALESCE(kw.name, cr.meta->>'controller', cr.meta->'config'->>'controller') = $%d", argCount)
		args = append(args, *filters.WorkloadName)
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
		var accountObjectID *string
		var accName *string
		var workloadName *string
		var namespace *string
		var kind *string

		err := rows.Scan(
			&recommendationJSON,
			&estimatedSavings,
			&status,
			&updatedAt,
			&accountObjectID,
			&accName,
			&workloadName,
			&namespace,
			&kind,
		)
		if err != nil {
			ctx.GetLogger().Error("Failed to scan recommendation row", "error", err)
			continue
		}

		if accName != nil {
			accountName = *accName
		}

		// Parse recommendation JSON
		var recMap map[string][]ContainerRecommendation
		if err := json.Unmarshal(recommendationJSON, &recMap); err != nil {
			ctx.GetLogger().Error("Failed to parse recommendation JSON", "error", err)
			continue
		}

		// Extract container recommendations
		for containerName, containerRecs := range recMap {
			var cpuRec, memoryRec *ContainerRecommendation
			for i := range containerRecs {
				switch containerRecs[i].Resource {
				case "cpu":
					cpuRec = &containerRecs[i]
				case "memory":
					memoryRec = &containerRecs[i]
				}
			}

			row := &PodRightSizingRow{
				WorkloadName:   safeString(workloadName),
				PodName:        containerName,
				Namespace:      safeString(namespace),
				Kind:           safeString(kind),
				MonthlySavings: estimatedSavings,
				UpdatedAt:      updatedAt,
				Status:         status,
				AccountName:    accountName,
			}

			// Extract CPU metrics
			if cpuRec != nil {
				if cpuRec.Allocated.Request != nil {
					row.CPURequest = *cpuRec.Allocated.Request
				}
				if cpuRec.Recommended.Request != nil {
					row.CPURecommended = *cpuRec.Recommended.Request
				}
				if row.CPURequest > 0 {
					row.CPUSavings = ((row.CPURequest - row.CPURecommended) / row.CPURequest) * 100
				}
			}

			// Extract Memory metrics (convert bytes to MB)
			if memoryRec != nil {
				if memoryRec.Allocated.Request != nil {
					row.MemoryRequest = *memoryRec.Allocated.Request / (1024 * 1024)
				}
				if memoryRec.Recommended.Request != nil {
					row.MemoryRecommended = *memoryRec.Recommended.Request / (1024 * 1024)
				}
				if row.MemoryRequest > 0 {
					row.MemorySavings = ((row.MemoryRequest - row.MemoryRecommended) / row.MemoryRequest) * 100
				}
			}

			exportRows = append(exportRows, row)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("error iterating recommendation rows: %w", err)
	}

	return exportRows, accountName, nil
}
