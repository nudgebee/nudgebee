package recommendation

import (
	"fmt"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// ColumnDefinition defines a single column in the export
type ColumnDefinition struct {
	Name  string
	Width int // Width for Excel columns (in characters)
}

// ExportRow is a generic interface for any export row
type ExportRow interface {
	// ToStringSlice converts the row to a slice of strings for CSV/Excel
	ToStringSlice() []string
}

// RecommendationExporter defines the interface for exporting recommendations
type RecommendationExporter interface {
	// FetchData fetches recommendations from database and converts to export rows
	FetchData(
		ctx *security.RequestContext,
		dbms *database.DatabaseManager,
		filters ExportFilters,
	) ([]ExportRow, string, error)

	// GetColumns returns the column definitions for this export type
	GetColumns() []ColumnDefinition

	// ValidateFilters validates that the provided filters are appropriate for this export type
	ValidateFilters(filters ExportFilters) error
}

// GetExporter returns the appropriate exporter for the given category and rule name
func GetExporter(category string, ruleName string) (RecommendationExporter, error) {
	// Factory pattern to return the correct exporter based on category and rule
	switch category {
	case "RightSizing":
		switch ruleName {
		case "pod_right_sizing":
			return &PodRightSizingExporter{}, nil
		case "replica_right_sizing":
			return &ReplicaRightSizingExporter{}, nil
		case "abandoned_resource":
			return &AbandonedWorkloadsExporter{}, nil
		case "unused_pvc":
			return &UnusedVolumesExporter{}, nil
		case "pv_rightsize":
			return &PVCRightSizingExporter{}, nil
		default:
			return nil, fmt.Errorf("unsupported RightSizing rule: %s", ruleName)
		}
	case "K8sSpotRecommendation":
		// Spot recommendations don't have specific rule names
		return &SpotRecommendationsExporter{}, nil
	case "Configuration":
		return &ConfigurationExporter{}, nil
	// Add more categories here
	// case "Security"
	// case "InfraUpgrade"
	default:
		return nil, fmt.Errorf("unsupported category: %s", category)
	}
}
