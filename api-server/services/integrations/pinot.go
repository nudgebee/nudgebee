package integrations

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegrationWithSource("pinot", "user", Pinot{})
}

const IntegrationPinot = "pinot"

type Pinot struct{}

func (p Pinot) Name() string {
	return IntegrationPinot
}

func (p Pinot) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (p Pinot) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"pinot_url", "pinot_table", "pinot_timestamp_col", "pinot_message_col"},
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"pinot_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Pinot controller URL (e.g., http://pinot-controller:9000)",
				Priority:    100,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Pinot integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         95,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         90,
			},
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     "none",
				Enum:        []any{"none", "basic", "bearer_token"},
				Priority:    80,
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Username for basic auth",
				ShowWhen:     map[string]any{"auth_type": "basic"},
				RequiredWhen: map[string]any{"auth_type": "basic"},
				Priority:     75,
			},
			"password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Password for basic auth",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "basic"},
				RequiredWhen: map[string]any{"auth_type": "basic"},
				Priority:     74,
			},
			"bearer_token": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Bearer token for authentication",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "bearer_token"},
				RequiredWhen: map[string]any{"auth_type": "bearer_token"},
				Priority:     73,
			},
			"pinot_table": {
				Type:        core.ToolSchemaTypeString,
				Description: "Pinot table name containing log data",
				Priority:    70,
			},
			"pinot_timestamp_col": {
				Type:        core.ToolSchemaTypeString,
				Description: "Column storing the log timestamp (numeric epoch or formatted string)",
				Priority:    65,
			},
			"pinot_message_col": {
				Type:        core.ToolSchemaTypeString,
				Description: "Column storing the log message body",
				Priority:    64,
			},
			"pinot_severity_col": {
				Type:        core.ToolSchemaTypeString,
				Description: "Column storing the log severity/level (optional)",
				Priority:    50,
			},
			"pinot_namespace_col": {
				Type:        core.ToolSchemaTypeString,
				Description: "Column for Kubernetes namespace (default: namespace) — used for log grouping",
				Priority:    40,
			},
			"pinot_pod_col": {
				Type:        core.ToolSchemaTypeString,
				Description: "Column for pod name (default: pod) — used for log grouping",
				Priority:    39,
			},
			"pinot_container_col": {
				Type:        core.ToolSchemaTypeString,
				Description: "Column for container name (default: container) — used for log grouping",
				Priority:    38,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Pinot default Logs Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         10,
			},
		},
	}
}

// pinotValidateFieldSpec is used only during ValidateConfig schema parsing.
type pinotValidateFieldSpec struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
	Format   string `json:"format,omitempty"`
}

type pinotValidateSchema struct {
	DimensionFieldSpecs []pinotValidateFieldSpec `json:"dimensionFieldSpecs"`
	MetricFieldSpecs    []pinotValidateFieldSpec `json:"metricFieldSpecs"`
	DateTimeFieldSpecs  []pinotValidateFieldSpec `json:"dateTimeFieldSpecs"`
}

func (p Pinot) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	pinotURL := strings.TrimRight(configMap["pinot_url"], "/")
	table := configMap["pinot_table"]
	tsCol := configMap["pinot_timestamp_col"]
	msgCol := configMap["pinot_message_col"]
	sevCol := configMap["pinot_severity_col"]
	authType := configMap["auth_type"]
	if authType == "" {
		authType = "none"
	}

	// Required field check
	var errs []error
	if pinotURL == "" {
		errs = append(errs, fmt.Errorf("pinot_url is required"))
	}
	if table == "" {
		errs = append(errs, fmt.Errorf("pinot_table is required"))
	}
	if tsCol == "" {
		errs = append(errs, fmt.Errorf("pinot_timestamp_col is required"))
	}
	if msgCol == "" {
		errs = append(errs, fmt.Errorf("pinot_message_col is required"))
	}
	switch authType {
	case "basic":
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for basic auth"))
		}
		if configMap["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for basic auth"))
		}
	case "bearer_token":
		if configMap["bearer_token"] == "" {
			errs = append(errs, fmt.Errorf("bearer_token is required for bearer_token auth"))
		}
	}
	if len(errs) > 0 {
		return errs
	}

	// Build auth header
	var authHeader string
	switch authType {
	case "basic":
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(configMap["username"]+":"+configMap["password"]))
	case "bearer_token":
		authHeader = "Bearer " + configMap["bearer_token"]
	}

	headers := map[string]string{"Accept": "application/json"}
	if authHeader != "" {
		headers["Authorization"] = authHeader
	}

	// Step 1: Connectivity — GET /health
	healthResp, err := common.HttpGet(
		fmt.Sprintf("%s/health", pinotURL),
		common.HttpWithInsecureSkipVerify(),
	)
	if err != nil {
		return []error{fmt.Errorf("cannot reach Pinot at %s: %w", pinotURL, err)}
	}
	_ = healthResp.Body.Close()
	switch healthResp.StatusCode {
	case http.StatusOK:
		// connected
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("Pinot authentication failed: invalid credentials (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("Pinot authorization failed: insufficient permissions (HTTP 403)")}
	default:
		return []error{fmt.Errorf("Pinot health check returned unexpected status: HTTP %d", healthResp.StatusCode)}
	}

	// Step 2: Schema fetch — GET /schemas/{table}
	schemaResp, err := common.HttpGet(
		fmt.Sprintf("%s/schemas/%s", pinotURL, table),
		common.HttpWithHeaders(headers),
		common.HttpWithInsecureSkipVerify(),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to fetch schema for table %q: %w", table, err)}
	}
	defer func() { _ = schemaResp.Body.Close() }()

	switch schemaResp.StatusCode {
	case http.StatusNotFound:
		return []error{fmt.Errorf("table %q not found in Pinot", table)}
	case http.StatusOK:
		// proceed
	default:
		return []error{fmt.Errorf("failed to fetch schema for table %q: HTTP %d", table, schemaResp.StatusCode)}
	}

	bodyBytes, err := io.ReadAll(schemaResp.Body)
	if err != nil {
		return []error{fmt.Errorf("failed to read schema for table %q: %w", table, err)}
	}
	var schema pinotValidateSchema
	if err := json.Unmarshal(bodyBytes, &schema); err != nil {
		return []error{fmt.Errorf("failed to parse schema for table %q: %w", table, err)}
	}

	// Build column lookup map
	type colInfo struct {
		DataType   string
		IsDateTime bool
		Format     string
	}
	allCols := make(map[string]colInfo)
	for _, f := range schema.DimensionFieldSpecs {
		allCols[f.Name] = colInfo{DataType: strings.ToUpper(f.DataType)}
	}
	for _, f := range schema.MetricFieldSpecs {
		allCols[f.Name] = colInfo{DataType: strings.ToUpper(f.DataType)}
	}
	for _, f := range schema.DateTimeFieldSpecs {
		allCols[f.Name] = colInfo{DataType: strings.ToUpper(f.DataType), IsDateTime: true, Format: f.Format}
	}

	// Step 3: Validate timestamp column — existence + type + format
	tsInfo, tsFound := allCols[tsCol]
	if !tsFound {
		errs = append(errs, fmt.Errorf("timestamp column %q not found in table %q", tsCol, table))
	} else if tsInfo.IsDateTime {
		parts := strings.SplitN(tsInfo.Format, ":", 4)
		if len(parts) < 3 {
			errs = append(errs, fmt.Errorf("timestamp column %q has malformed dateTime format %q", tsCol, tsInfo.Format))
		} else {
			switch strings.ToUpper(parts[2]) {
			case "EPOCH":
				// numeric epoch — OK, unit auto-detected from format
			case "SIMPLE_DATE_FORMAT":
				javaFmt := ""
				if len(parts) == 4 {
					javaFmt = parts[3]
				}
				effectiveGoFmt := pinotJavaToGoFormat(javaFmt)
				if effectiveGoFmt == "" {
					// Java→Go conversion failed; sample a row and try to auto-detect.
					detected, sErr := probePinotTimestampFormat(pinotURL, table, tsCol, headers)
					if sErr != nil || detected == "" {
						errs = append(errs, fmt.Errorf(
							"timestamp column %q uses SIMPLE_DATE_FORMAT %q and auto-detection from a sample row failed: %v",
							tsCol, javaFmt, sErr))
					}
				}
			default:
				errs = append(errs, fmt.Errorf("timestamp column %q has unrecognized dateTime format type %q in format string %q", tsCol, parts[2], tsInfo.Format))
			}
		}
	} else {
		numericTypes := map[string]bool{"LONG": true, "INT": true, "DOUBLE": true, "FLOAT": true, "BIG_DECIMAL": true}
		switch {
		case tsInfo.DataType == "STRING" || tsInfo.DataType == "TEXT":
			detected, sErr := probePinotTimestampFormat(pinotURL, table, tsCol, headers)
			if sErr != nil || detected == "" {
				errs = append(errs, fmt.Errorf(
					"timestamp column %q is %s type: could not auto-detect format from a sample row: %v",
					tsCol, tsInfo.DataType, sErr))
			}
		case numericTypes[tsInfo.DataType]:
			// epoch ms assumed — OK
		default:
			errs = append(errs, fmt.Errorf("timestamp column %q has unsupported type %q; expected numeric epoch (LONG/INT) or a dateTime column", tsCol, tsInfo.DataType))
		}
	}

	// Step 4: Validate message column
	if _, msgFound := allCols[msgCol]; !msgFound {
		errs = append(errs, fmt.Errorf("message column %q not found in table %q", msgCol, table))
	}

	// Step 5: Validate severity column (if provided)
	if sevCol != "" {
		if _, sevFound := allCols[sevCol]; !sevFound {
			errs = append(errs, fmt.Errorf("severity column %q not found in table %q", sevCol, table))
		}
	}

	// Step 6: Validate optional log-group grouping columns (if user-provided)
	for _, pair := range []struct{ key, value string }{
		{"pinot_namespace_col", configMap["pinot_namespace_col"]},
		{"pinot_pod_col", configMap["pinot_pod_col"]},
		{"pinot_container_col", configMap["pinot_container_col"]},
	} {
		if pair.value == "" {
			continue
		}
		if _, found := allCols[pair.value]; !found {
			errs = append(errs, fmt.Errorf("%s column %q not found in table %q", pair.key, pair.value, table))
		}
	}

	return errs
}

// pinotValidateKnownLayouts lists Go time layouts attempted against a sample
// timestamp value when auto-detecting the format of a STRING-typed Pinot column.
var pinotValidateKnownLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.000000Z",
	"2006-01-02T15:04:05.000Z",
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"01/02/2006 15:04:05",
	"02/01/2006 15:04:05",
	"02-Jan-2006 15:04:05",
	"02-Jan-2006",
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
}

// pinotQuoteIdent wraps a Pinot SQL identifier in double quotes, escaping any
// embedded double quote by doubling. Required because Pinot's Calcite parser
// reserves words like "timestamp", "date", "level", "value", "user", etc.
// Twin of observability.pinotQuoteIdent; duplicated because the integrations
// package cannot import observability.
func pinotQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// pinotDetectTimestampFormat tries known Go time layouts against the sample
// value and returns the first one that parses, or "" when no layout matches.
func pinotDetectTimestampFormat(sample string) string {
	s := strings.TrimSpace(sample)
	if s == "" {
		return ""
	}
	for _, layout := range pinotValidateKnownLayouts {
		if _, err := time.Parse(layout, s); err == nil {
			return layout
		}
	}
	return ""
}

// probePinotTimestampFormat samples one row from {table}.{tsCol} via POST /sql
// and runs pinotDetectTimestampFormat on the value. Used by ValidateConfig to
// fail fast when we can't auto-detect a STRING/SIMPLE_DATE_FORMAT column's layout.
func probePinotTimestampFormat(pinotURL, table, tsCol string, headers map[string]string) (string, error) {
	tsColQ := pinotQuoteIdent(tsCol)
	sqlBody := map[string]string{
		"sql": fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 1", tsColQ, pinotQuoteIdent(table), tsColQ),
	}
	h := map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	resp, err := common.HttpPost(pinotURL+"/sql",
		common.HttpWithHeaders(h),
		common.HttpWithJsonBody(sqlBody),
		common.HttpWithInsecureSkipVerify(),
	)
	if err != nil {
		return "", fmt.Errorf("sample query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sample query returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read sample response: %w", err)
	}
	var r struct {
		ResultTable struct {
			Rows [][]any `json:"rows"`
		} `json:"resultTable"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse sample response: %w", err)
	}
	if len(r.ResultTable.Rows) == 0 || len(r.ResultTable.Rows[0]) == 0 {
		return "", fmt.Errorf("no sample rows found")
	}
	sample, ok := r.ResultTable.Rows[0][0].(string)
	if !ok {
		return "", fmt.Errorf("sample value is not a string (got %T)", r.ResultTable.Rows[0][0])
	}
	detected := pinotDetectTimestampFormat(sample)
	if detected == "" {
		return "", fmt.Errorf("unrecognized layout for sample %q", sample)
	}
	return detected, nil
}

// pinotJavaToGoFormat converts a Pinot/Java SimpleDateFormat pattern to a Go time layout.
// Returns empty string when the pattern cannot be reliably converted.
func pinotJavaToGoFormat(javaFmt string) string {
	if javaFmt == "" {
		return ""
	}
	s := strings.ReplaceAll(javaFmt, "'T'", "T")
	if strings.Contains(s, "'") {
		// Quoted literals other than 'T' are not supported
		return ""
	}
	// Apply substitutions longest-first to avoid partial matches
	type sub struct{ java, goFmt string }
	subs := []sub{
		{"yyyy", "2006"},
		{"yy", "06"},
		{"SSSSSS", "000000"},
		{"SSS", "000"},
		{"MM", "01"},
		{"dd", "02"},
		{"HH", "15"},
		{"mm", "04"},
		{"ss", "05"},
		{"XXX", "-07:00"},
		{"XX", "-0700"},
		{"Z", "-07:00"},
		{"z", "MST"},
	}
	for _, sub := range subs {
		s = strings.ReplaceAll(s, sub.java, sub.goFmt)
	}
	// Validate via round-trip using Go's reference time
	ref := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	if _, err := time.Parse(s, ref.Format(s)); err != nil {
		return ""
	}
	return s
}
