package recommendation

import (
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

const (
	MaxExportSizeBytes = 10 * 1024 * 1024 // 10MB limit
)

// ExportFilters defines the filters for exporting recommendations
type ExportFilters struct {
	AccountID    string
	Category     string
	RuleName     string
	Namespace    *string
	WorkloadType *string
	WorkloadName *string
	Status       []string
	Severity     *string // For Security and Configuration recommendations
	Image        *string // For Security image scan recommendations
}

// ExportResult contains the base64 encoded export data
type ExportResult struct {
	FileData    string `json:"file_data"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	RecordCount int    `json:"record_count"`
}

// RecommendationExportRow represents a single row in the export
type RecommendationExportRow struct {
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

// ContainerRecommendation represents the recommendation for a single container
type ContainerRecommendation struct {
	Resource  string `json:"resource"`
	Allocated struct {
		Request *float64 `json:"request"`
		Limit   *float64 `json:"limit"`
	} `json:"allocated"`
	Recommended struct {
		Request *float64 `json:"request"`
		Limit   *float64 `json:"limit"`
	} `json:"recommended"`
}

// GenerateRecommendationExport generates an export file and returns base64 encoded data
func GenerateRecommendationExport(
	ctx *security.RequestContext,
	filters ExportFilters,
	format string,
) (*ExportResult, error) {
	// Get the appropriate exporter for this category and rule
	exporter, err := GetExporter(filters.Category, filters.RuleName)
	if err != nil {
		return nil, fmt.Errorf("failed to get exporter: %w", err)
	}

	// Validate filters
	if err := exporter.ValidateFilters(filters); err != nil {
		return nil, fmt.Errorf("filter validation failed: %w", err)
	}

	// Get database manager
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Fetch data using the exporter
	rows, accountName, err := exporter.FetchData(ctx, dbms, filters)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recommendations: %w", err)
	}

	if len(rows) == 0 {
		ctx.GetLogger().Info("No recommendations found for export", "filters", filters)
	}

	// Get column definitions from exporter
	columns := exporter.GetColumns()

	// Generate file based on format
	var fileBytes []byte
	var contentType string
	var fileExt string

	switch format {
	case "csv":
		fileBytes, err = generateGenericCSV(rows, columns)
		contentType = "text/csv"
		fileExt = "csv"
	case "xlsx":
		fileBytes, err = generateGenericExcel(rows, columns)
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		fileExt = "xlsx"
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate %s: %w", format, err)
	}

	// Check size limit
	if len(fileBytes) > MaxExportSizeBytes {
		return nil, fmt.Errorf("export size exceeds limit of 10MB. Please apply more filters to reduce the dataset")
	}

	// Base64 encode
	encoded := base64.StdEncoding.EncodeToString(fileBytes)

	// Generate filename
	filename := generateFilename(accountName, filters.RuleName, fileExt)

	return &ExportResult{
		FileData:    encoded,
		Filename:    filename,
		ContentType: contentType,
		RecordCount: len(rows),
	}, nil
}

// generateGenericCSV creates a CSV file from generic export rows
func generateGenericCSV(rows []ExportRow, columns []ColumnDefinition) ([]byte, error) {
	var buf strings.Builder
	writer := csv.NewWriter(&buf)

	// Write headers
	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.Name
	}
	if err := writer.Write(headers); err != nil {
		return nil, err
	}

	// Write data rows
	for _, row := range rows {
		record := row.ToStringSlice()
		if err := writer.Write(record); err != nil {
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}

	return []byte(buf.String()), nil
}

// generateGenericExcel creates an Excel file from generic export rows
func generateGenericExcel(rows []ExportRow, columns []ColumnDefinition) ([]byte, error) {
	f := excelize.NewFile()
	sheet := "Recommendations"
	index, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(index)

	// Write headers with bold style
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})
	if err != nil {
		return nil, err
	}

	// Set headers and column widths
	for i, col := range columns {
		colLetter := string(rune('A' + i))
		cell := fmt.Sprintf("%s1", colLetter)

		if err := f.SetCellValue(sheet, cell, col.Name); err != nil {
			return nil, err
		}
		if err := f.SetCellStyle(sheet, cell, cell, headerStyle); err != nil {
			return nil, err
		}

		// Set column width
		if err := f.SetColWidth(sheet, colLetter, colLetter, float64(col.Width)); err != nil {
			return nil, err
		}
	}

	// Write data rows
	for rowIdx, row := range rows {
		rowNum := rowIdx + 2 // Start from row 2 (row 1 is headers)
		values := row.ToStringSlice()

		for colIdx, value := range values {
			colLetter := string(rune('A' + colIdx))
			cell := fmt.Sprintf("%s%d", colLetter, rowNum)
			if err := f.SetCellValue(sheet, cell, value); err != nil {
				return nil, err
			}
		}
	}

	// Write to buffer
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// generateFilename creates a sanitized filename for the export
func generateFilename(accountName, ruleName, extension string) string {
	// Sanitize account name (allow only alphanumeric, dash, underscore)
	reg := regexp.MustCompile("[^a-zA-Z0-9-_]+")
	sanitizedAccount := reg.ReplaceAllString(accountName, "-")
	sanitizedRule := reg.ReplaceAllString(ruleName, "-")

	// Trim dashes from ends
	sanitizedAccount = strings.Trim(sanitizedAccount, "-")
	sanitizedRule = strings.Trim(sanitizedRule, "-")

	if sanitizedAccount == "" {
		sanitizedAccount = "account"
	}
	if sanitizedRule == "" {
		sanitizedRule = "recommendations"
	}

	return fmt.Sprintf("recommendations-%s-%s.%s", sanitizedAccount, sanitizedRule, extension)
}

// safeString safely dereferences a string pointer
func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
