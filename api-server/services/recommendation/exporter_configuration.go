package recommendation

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// ConfigurationRow represents a row in the configuration recommendations export
type ConfigurationRow struct {
	Name        string
	Severity    string
	ObjectType  string
	Namespaces  string
	ObjectNames string
	UpdatedAt   time.Time
	Description string
}

// ToStringSlice implements ExportRow interface
func (r *ConfigurationRow) ToStringSlice() []string {
	return []string{
		r.Name,
		r.Severity,
		r.ObjectType,
		r.Namespaces,
		r.ObjectNames,
		r.UpdatedAt.Format(time.RFC3339),
		r.Description,
	}
}

// ConfigurationExporter handles all Configuration category recommendations
type ConfigurationExporter struct{}

// GetColumns returns column definitions for configuration recommendations
func (e *ConfigurationExporter) GetColumns() []ColumnDefinition {
	return []ColumnDefinition{
		{Name: "Name", Width: 35},
		{Name: "Severity", Width: 12},
		{Name: "Object Type", Width: 20},
		{Name: "Namespaces", Width: 20},
		{Name: "Object Names", Width: 30},
		{Name: "Updated At", Width: 20},
		{Name: "Description", Width: 60},
	}
}

// ValidateFilters validates filters for configuration recommendations
func (e *ConfigurationExporter) ValidateFilters(filters ExportFilters) error {
	if filters.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if filters.Category != "Configuration" {
		return fmt.Errorf("category must be Configuration")
	}
	return nil
}

// FetchData fetches all Configuration recommendations using direct table columns
// for namespace/name/type, only dipping into the recommendation JSON for Description.
func (e *ConfigurationExporter) FetchData(
	ctx *security.RequestContext,
	dbms *database.DatabaseManager,
	filters ExportFilters,
) ([]ExportRow, string, error) {
	if filters.AccountID == "" {
		return nil, "", fmt.Errorf("account_id is required")
	}

	query := `
		SELECT
			r.rule_name,
			r.severity,
			r.account_object_id,
			r.status,
			r.updated_at,
			r.recommendation,
			ca.account_name,
			cr.name AS resource_name,
			CASE
				WHEN cr.meta->>'namespace'                      IS NOT NULL THEN cr.meta->>'namespace'
				WHEN cr.meta->'config'->>'namespace'            IS NOT NULL THEN cr.meta->'config'->>'namespace'
				WHEN r.recommendation->'metadata'->>'namespace' IS NOT NULL THEN r.recommendation->'metadata'->>'namespace'
				ELSE r.recommendation->>'namespace'
			END AS resource_k8s_namespace
		FROM recommendation r
		LEFT JOIN cloud_resourses cr ON cr.id = r.resource_id
		LEFT JOIN cloud_accounts  ca ON ca.id = r.cloud_account_id
		WHERE
			r.cloud_account_id = $1
			AND r.category = $2
	`

	args := []interface{}{filters.AccountID, filters.Category}
	argCount := 2

	if filters.RuleName != "" {
		argCount++
		query += fmt.Sprintf(" AND r.rule_name = $%d", argCount)
		args = append(args, filters.RuleName)
	}

	if filters.Severity != nil && *filters.Severity != "" {
		argCount++
		query += fmt.Sprintf(" AND r.severity = $%d", argCount)
		args = append(args, *filters.Severity)
	}

	if filters.Namespace != nil && *filters.Namespace != "" {
		argCount++
		query += fmt.Sprintf(` AND (
			cr.meta->>'namespace' = $%d OR
			cr.meta->'config'->>'namespace' = $%d OR
			r.recommendation->'metadata'->>'namespace' = $%d OR
			r.recommendation->>'namespace' = $%d
		)`, argCount, argCount, argCount, argCount)
		args = append(args, *filters.Namespace)
	}

	if len(filters.Status) > 0 {
		argCount++
		query += fmt.Sprintf(" AND r.status = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Status))
	}

	// Match list_k8recommendation's order_by exactly: estimated_savings DESC NULLS LAST
	query += " ORDER BY r.estimated_savings DESC NULLS LAST"

	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		ctx.GetLogger().Error("Failed to query configuration recommendations", "error", err)
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
		var ruleName string
		var sev string
		var accountObjectID *string
		var status string
		var updatedAt time.Time
		var rawJSON []byte
		var accName *string
		var resourceName *string
		var resourceNamespace *string

		if err := rows.Scan(&ruleName, &sev, &accountObjectID, &status, &updatedAt, &rawJSON, &accName, &resourceName, &resourceNamespace); err != nil {
			ctx.GetLogger().Error("Failed to scan configuration row", "error", err)
			continue
		}

		if accName != nil {
			accountName = *accName
		}

		// Object Type: strip "_misconfigurations" suffix from rule_name (e.g. "pods_misconfigurations" → "pods").
		// For old-format rows (rule_name = "misconfigurations"), fall back to the "kind" field
		// embedded in each JSON element (matching how recommendation_misconfigs_v2 reads it via
		// rr->>'kind'). Final fallback: middle segment of account_object_id ("namespace/Kind/name").
		objectType := objectTypeFromRuleName(ruleName)
		if objectType == "" {
			objectType = extractKindFromJSON(rawJSON)
		}

		namespace := safeString(resourceNamespace)
		objectName := safeString(resourceName)
		if namespace == "" || objectName == "" || objectType == "" {
			ns, parsedObjectType, on := parseAccountObjectID(accountObjectID)
			if namespace == "" {
				namespace = ns
			}
			if objectName == "" {
				objectName = on
			}
			if objectType == "" {
				objectType = parsedObjectType
			}
		}

		exportRows = append(exportRows, &ConfigurationRow{
			Name:        ruleName,
			Severity:    sev,
			ObjectType:  objectType,
			Namespaces:  namespace,
			ObjectNames: objectName,
			UpdatedAt:   updatedAt,
			Description: extractConfigDescription(rawJSON),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("error iterating configuration rows: %w", err)
	}

	return exportRows, accountName, nil
}

// objectTypeFromRuleName derives the k8s resource type from the rule name.
// "pods_misconfigurations" → "pods", "deployments_misconfigurations" → "deployments".
// Returns empty string for rules that don't follow this pattern.
func objectTypeFromRuleName(ruleName string) string {
	if after, ok := strings.CutSuffix(ruleName, "_misconfigurations"); ok {
		return after
	}
	return ""
}

// parseAccountObjectID splits account_object_id into (namespace, objectType, objectName).
// Configuration rules use two formats:
//   - misconfigurations: "namespace/Kind/name"  → 3 parts
//   - certificate_expiry: "namespace/name"       → 2 parts, objectType is empty
func parseAccountObjectID(id *string) (namespace, objectType, objectName string) {
	if id == nil || *id == "" {
		return "", "", ""
	}
	parts := strings.SplitN(*id, "/", 3)
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return parts[0], "", parts[1]
	default:
		return "", "", parts[0]
	}
}

// extractKindFromJSON returns the "kind" field from the first element of a
// JSON array recommendation (old-format "misconfigurations" rows). This mirrors
// the recommendation_misconfigs_v2 query which reads rr->>'kind' via lateral join.
func extractKindFromJSON(rawJSON []byte) string {
	if len(rawJSON) == 0 || rawJSON[0] != '[' {
		return ""
	}
	var items []map[string]any
	if err := json.Unmarshal(rawJSON, &items); err != nil || len(items) == 0 {
		return ""
	}
	return stringField(items[0], "kind")
}

// extractConfigDescription builds a human-readable description from the raw
// recommendation JSON by inspecting its shape, not the rule name.
// - JSON object: looks for description, message, or expiry fields.
// - JSON array:  joins the "message" field from each element.
func extractConfigDescription(rawJSON []byte) string {
	if len(rawJSON) == 0 {
		return ""
	}
	// Array shape
	if rawJSON[0] == '[' {
		var items []map[string]any
		if err := json.Unmarshal(rawJSON, &items); err != nil {
			return ""
		}
		msgs := make([]string, 0, len(items))
		for _, item := range items {
			if msg := stringField(item, "message"); msg != "" {
				msgs = append(msgs, msg)
			}
		}
		return sanitizeDescription(strings.Join(msgs, " . "))
	}
	// Object shape
	var rec map[string]any
	if err := json.Unmarshal(rawJSON, &rec); err != nil {
		return ""
	}
	if v := stringField(rec, "description"); v != "" {
		return sanitizeDescription(v)
	}
	if v := stringField(rec, "message"); v != "" {
		return sanitizeDescription(v)
	}
	if expiry := stringField(rec, "expiry_date"); expiry != "" {
		days := 0
		if v, ok := rec["days_until_expiry"].(float64); ok {
			days = int(v)
		}
		return fmt.Sprintf("expires in %d day(s) on %s", days, expiry)
	}
	return ""
}

// sanitizeDescription replaces commas with dots so the value is safe
// as an unquoted CSV field.
func sanitizeDescription(s string) string {
	return strings.ReplaceAll(s, ",", ".")
}

// stringField safely extracts a string value from a map.
func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
