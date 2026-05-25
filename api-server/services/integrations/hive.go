package integrations

import (
	"context"
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/integrations/hiveclient"
	"nudgebee/services/security"
	"strings"
	"time"
)

// hiveColumnAutogenFunc names the autogen handler that returns the live
// column list for the configured Hive table. Used as the AutoGenerateFunc
// on every column-name field in Hive.ConfigSchema so the frontend renders
// them as free-text autocompletes.
const hiveColumnAutogenFunc = "listHiveColumns"

// hiveColumnAutogenDeps lists every form field whose value influences the
// column list. The frontend watches these and refetches suggestions when
// any of them changes.
var hiveColumnAutogenDeps = []string{"hive_url", "hive_database", "hive_table", "auth_type", "username", "password", "hive_tls_skip_verify"}

func init() {
	core.RegisterIntegrationWithSource("hive", "user", Hive{})
	core.RegisterAutoGenHandler(hiveColumnAutogenFunc, listHiveColumns)
}

const IntegrationHive = "hive"

type Hive struct{}

func (h Hive) Name() string {
	return IntegrationHive
}

func (h Hive) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (h Hive) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:        core.ToolSchemaTypeObject,
		Description: "For this integration to work correctly, the Hive table must be partitioned by time (e.g. year / month / day / hour). Log queries must include at least one of those partition columns in the filter — without partition pruning Hive scans the entire table, which is slow and may fail on malformed rows.",
		Required:    []string{"hive_url", "hive_table", "hive_timestamp_col", "hive_message_col"},
		Testable:    true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"hive_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "HiveServer2 endpoint as host:port (e.g., hiveserver2.hive.svc.cluster.local:10000)",
				Priority:    100,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Hive integration",
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
			"hive_database": {
				Type:        core.ToolSchemaTypeString,
				Description: "Hive database (defaults to 'default')",
				Default:     "default",
				Priority:    85,
			},
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     "none",
				Enum:        []any{"none", "ldap"},
				Priority:    80,
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Username for LDAP auth",
				ShowWhen:     map[string]any{"auth_type": "ldap"},
				RequiredWhen: map[string]any{"auth_type": "ldap"},
				Priority:     75,
			},
			"password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Password for LDAP auth",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "ldap"},
				RequiredWhen: map[string]any{"auth_type": "ldap"},
				Priority:     74,
			},
			"hive_tls_skip_verify": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Skip TLS certificate verification (only for self-signed HiveServer2 deployments — leaves credentials vulnerable to MITM)",
				Default:     false,
				Priority:    73,
			},
			"hive_table": {
				Type:        core.ToolSchemaTypeString,
				Description: "Hive table name containing log data (e.g., k8s_logs)",
				Priority:    70,
			},
			"hive_timestamp_col": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Column storing the log timestamp (numeric epoch or formatted string)",
				Priority:         65,
				AutoGenerateFunc: hiveColumnAutogenFunc,
				DependsOn:        hiveColumnAutogenDeps,
			},
			"hive_message_col": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Column storing the log message body",
				Priority:         64,
				AutoGenerateFunc: hiveColumnAutogenFunc,
				DependsOn:        hiveColumnAutogenDeps,
			},
			"hive_severity_col": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Column storing the log severity/level (optional)",
				Priority:         50,
				AutoGenerateFunc: hiveColumnAutogenFunc,
				DependsOn:        hiveColumnAutogenDeps,
			},
			"hive_namespace_col": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Column for Kubernetes namespace (e.g., kubernetes.namespace_name) — used for log grouping",
				Priority:         40,
				AutoGenerateFunc: hiveColumnAutogenFunc,
				DependsOn:        hiveColumnAutogenDeps,
			},
			"hive_pod_col": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Column for pod name (e.g., kubernetes.pod_name) — used for log grouping",
				Priority:         39,
				AutoGenerateFunc: hiveColumnAutogenFunc,
				DependsOn:        hiveColumnAutogenDeps,
			},
			"hive_container_col": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Column for container name (e.g., kubernetes.container_name) — used for log grouping",
				Priority:         38,
				AutoGenerateFunc: hiveColumnAutogenFunc,
				DependsOn:        hiveColumnAutogenDeps,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Hive default Logs Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         10,
			},
		},
	}
}

// ValidateConfig runs both required-field checks and a strict live probe
// against HiveServer2 — mirrors the Pinot validation pattern. If the
// integration is invalid we fail at save time so the customer hits a single
// clear error rather than a stream of runtime errors on every query.
//
// Steps:
//  1. Required scalars + LDAP credentials (cheap, no connection).
//  2. Connect to HiveServer2 (fail fast on unreachable / bad creds).
//  3. DESCRIBE the table (catches typos in db/table name).
//  4. Verify each configured column (timestamp, message, severity, k8s grouping
//     columns) actually exists in the schema — accepting dot-paths into
//     struct columns like `kubernetes.namespace_name`.
//  5. For STRING-typed timestamp columns, sample one row and confirm we can
//     auto-detect the layout. If detection fails the column would render as
//     a raw string and break sort/time-range filters, so we refuse to save.
func (h Hive) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	hiveURL := strings.TrimSpace(configMap["hive_url"])
	database := strings.TrimSpace(configMap["hive_database"])
	if database == "" {
		database = "default"
	}
	table := strings.TrimSpace(configMap["hive_table"])
	tsCol := strings.TrimSpace(configMap["hive_timestamp_col"])
	msgCol := strings.TrimSpace(configMap["hive_message_col"])
	sevCol := strings.TrimSpace(configMap["hive_severity_col"])
	nsCol := strings.TrimSpace(configMap["hive_namespace_col"])
	podCol := strings.TrimSpace(configMap["hive_pod_col"])
	containerCol := strings.TrimSpace(configMap["hive_container_col"])
	authType := configMap["auth_type"]
	if authType == "" {
		authType = "none"
	}

	// Step 1 — required scalars.
	var errs []error
	if hiveURL == "" {
		errs = append(errs, fmt.Errorf("hive_url is required"))
	}
	if table == "" {
		errs = append(errs, fmt.Errorf("hive_table is required"))
	}
	if tsCol == "" {
		errs = append(errs, fmt.Errorf("hive_timestamp_col is required"))
	}
	if msgCol == "" {
		errs = append(errs, fmt.Errorf("hive_message_col is required"))
	}
	if authType == "ldap" {
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for LDAP auth"))
		}
		if configMap["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for LDAP auth"))
		}
	}
	if len(errs) > 0 {
		return errs
	}

	host, port, useTLS, err := hiveclient.ParseHostPort(hiveURL)
	if err != nil {
		return []error{fmt.Errorf("hive_url: %w", err)}
	}

	// Step 2 — connect. Tight timeouts so a save-form click can't hang.
	conn, err := hiveclient.Connect(hiveclient.ConnectConfig{
		Host:           host,
		Port:           port,
		Auth:           authType,
		Username:       configMap["username"],
		Password:       configMap["password"],
		Database:       database,
		UseTLS:         useTLS,
		TLSSkipVerify:  strings.EqualFold(strings.TrimSpace(configMap["hive_tls_skip_verify"]), "true"),
		ConnectTimeout: 15 * time.Second,
		SocketTimeout:  30 * time.Second,
	})
	if err != nil {
		return []error{fmt.Errorf("cannot reach HiveServer2 at %s:%d: %w", host, port, err)}
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 3 — DESCRIBE the table.
	cols, err := hiveclient.DescribeColumns(ctx, conn, database, table)
	if err != nil {
		return []error{fmt.Errorf("table %s.%s: %w", database, table, err)}
	}
	if len(cols) == 0 {
		return []error{fmt.Errorf("table %s.%s exists but has no columns", database, table)}
	}

	// Flatten so dot-paths like `kubernetes.namespace_name` resolve.
	specByName := make(map[string]hiveclient.ColumnSpec, len(cols))
	for _, c := range hiveclient.FlattenColumns(cols) {
		specByName[c.Name] = c
	}

	// Step 4 — column existence checks.
	tsSpec, tsFound := specByName[tsCol]
	if !tsFound {
		errs = append(errs, fmt.Errorf("timestamp column %q not found in %s.%s", tsCol, database, table))
	}
	if _, ok := specByName[msgCol]; !ok {
		errs = append(errs, fmt.Errorf("message column %q not found in %s.%s", msgCol, database, table))
	}
	for _, pair := range []struct{ key, value string }{
		{"hive_severity_col", sevCol},
		{"hive_namespace_col", nsCol},
		{"hive_pod_col", podCol},
		{"hive_container_col", containerCol},
	} {
		if pair.value == "" {
			continue
		}
		if _, ok := specByName[pair.value]; !ok {
			errs = append(errs, fmt.Errorf("%s column %q not found in %s.%s", pair.key, pair.value, database, table))
		}
	}

	// Step 5 — timestamp column type + format probe.
	if tsFound {
		t := strings.ToLower(strings.TrimSpace(tsSpec.Type))
		switch {
		case strings.HasPrefix(t, "bigint") || strings.HasPrefix(t, "int") || strings.HasPrefix(t, "double") || strings.HasPrefix(t, "float") || strings.HasPrefix(t, "decimal"):
			// Numeric epoch — scale factor inferred from name suffix at query
			// time. Nothing to probe here.
		case strings.HasPrefix(t, "string") || strings.HasPrefix(t, "varchar") || strings.HasPrefix(t, "char") || strings.HasPrefix(t, "timestamp"):
			sample, sampleErr := hiveclient.SampleColumn(ctx, conn, database, table, tsCol)
			switch {
			case sampleErr != nil:
				errs = append(errs, fmt.Errorf("timestamp column %q: %w", tsCol, sampleErr))
			case sample == "":
				errs = append(errs, fmt.Errorf("timestamp column %q exists but the table appears empty — can't auto-detect the format. Add a sample row or use a numeric epoch column", tsCol))
			case hiveclient.DetectTimestampFormat(sample) == "":
				errs = append(errs, fmt.Errorf("timestamp column %q sample value %q does not match any known time layout (RFC3339Nano, 2006-01-02 15:04:05, etc.)", tsCol, sample))
			}
		default:
			errs = append(errs, fmt.Errorf("timestamp column %q has unsupported type %q — expected numeric epoch or string/timestamp", tsCol, tsSpec.Type))
		}
	}

	return errs
}

// listHiveColumns is the AutoGenHandler that powers the column-name
// autocomplete on the Hive integration form. It connects to HiveServer2 with
// the partial form values the user has typed and returns the flattened
// column list (top-level columns plus struct/array<struct> leaves) as
// suggestions. The handler is best-effort: on any failure it returns an
// empty list with a user-visible message; the form stays usable as plain
// text (freeSolo).
func listHiveColumns(rctx *security.RequestContext, formValues map[string]any) (core.AutoGenResult, error) {
	url := stringFromForm(formValues, "hive_url")
	table := stringFromForm(formValues, "hive_table")
	if url == "" || table == "" {
		return core.AutoGenResult{Message: "Fill hive_url and hive_table to load column suggestions."}, nil
	}

	database := stringFromForm(formValues, "hive_database")
	if database == "" {
		database = "default"
	}
	authType := strings.ToLower(stringFromForm(formValues, "auth_type"))
	username := stringFromForm(formValues, "username")
	password := stringFromForm(formValues, "password")

	// Edit-existing flow: password isn't echoed back into the form state. We
	// can't connect without it, so bail with a clear message.
	if authType == "ldap" && password == "" {
		return core.AutoGenResult{Message: "Re-enter password to load column suggestions."}, nil
	}

	host, port, useTLS, err := hiveclient.ParseHostPort(url)
	if err != nil {
		return core.AutoGenResult{}, fmt.Errorf("hive_url: %w", err)
	}

	conn, err := hiveclient.Connect(hiveclient.ConnectConfig{
		Host:           host,
		Port:           port,
		Auth:           authType,
		Username:       username,
		Password:       password,
		Database:       database,
		UseTLS:         useTLS,
		TLSSkipVerify:  boolFromForm(formValues, "hive_tls_skip_verify"),
		ConnectTimeout: 15 * time.Second,
		SocketTimeout:  30 * time.Second,
	})
	if err != nil {
		return core.AutoGenResult{}, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Derive the timeout context from the inbound request so a client
	// disconnect cancels DescribeColumns instead of leaving it to run for
	// the full timeout.
	parent := context.Background()
	if rctx != nil {
		parent = rctx.GetContext()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	cols, err := hiveclient.DescribeColumns(ctx, conn, database, table)
	if err != nil {
		return core.AutoGenResult{}, fmt.Errorf("describe: %w", err)
	}

	flat := hiveclient.FlattenColumns(cols)
	opts := make([]core.AutoGenOption, 0, len(flat))
	for _, c := range flat {
		label := c.Name
		if c.IsPartition {
			label += " (partition)"
		}
		opts = append(opts, core.AutoGenOption{Label: label, Value: c.Name})
	}
	return core.AutoGenResult{Options: opts}, nil
}

// stringFromForm safely reads a string field from the autogen form_values
// map, returning "" for missing/non-string values.
func stringFromForm(form map[string]any, key string) string {
	if form == nil {
		return ""
	}
	if v, ok := form[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// boolFromForm safely reads a boolean field from the autogen form_values map.
// Accepts native bool and the "true"/"false" string forms a JSON-encoded
// checkbox value typically arrives as. Returns false for anything else.
func boolFromForm(form map[string]any, key string) bool {
	if form == nil {
		return false
	}
	switch v := form[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}
