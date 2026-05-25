package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for PinotSaasSource's DynamicLabelMappingSource implementation.
// Require TEST_ACCOUNT_ID + TEST_TENANT_ID env vars pointing to an account that has
// a "pinot" / source="user" integration configured.
//
// Run with:
//   TEST_ACCOUNT_ID=<uuid> TEST_TENANT_ID=<uuid> \
//     go test ./observability/... -run TestPinotSaas -v

// snapshotTenantLogLabels captures the current tenant_attrs.log_labels row (if any)
// and registers a cleanup that restores it. Used so tests that upsert overrides do
// not destroy existing tenant configuration.
func snapshotTenantLogLabels(t *testing.T, dbMgr *database.DatabaseManager, tenantID string) {
	t.Helper()
	var original string
	hasRow := true
	err := dbMgr.Db.QueryRowx(
		`SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = 'log_labels'`,
		tenantID,
	).Scan(&original)
	if err != nil {
		hasRow = false
	}
	t.Cleanup(func() {
		if hasRow {
			_, _ = dbMgr.Db.Exec(`
				INSERT INTO tenant_attrs (tenant_id, name, value)
				VALUES ($1, 'log_labels', $2)
				ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value
			`, tenantID, original)
		} else {
			_, _ = dbMgr.Db.Exec(
				`DELETE FROM tenant_attrs WHERE tenant_id = $1 AND name = 'log_labels'`,
				tenantID,
			)
		}
		_ = common.CacheDelete(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantID)
	})
}

func upsertTenantLogLabelsRaw(t *testing.T, dbMgr *database.DatabaseManager, tenantID string, labels map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(labels)
	_, err := dbMgr.Db.Exec(`
		INSERT INTO tenant_attrs (tenant_id, name, value)
		VALUES ($1, 'log_labels', $2)
		ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value
	`, tenantID, string(raw))
	require.NoError(t, err)
	_ = common.CacheDelete(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantID)
}

// TestPinotSaas_GetDynamicLabelMapping_Integration verifies that
// GetDynamicLabelMapping reads the Pinot integration config from the DB and
// returns a canonical → Pinot-column map. The mapping must include any column
// names configured on the integration (e.g. severity → "severity").
func TestPinotSaas_GetDynamicLabelMapping_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)
	tenantID := requireTestTenantID(t)

	ctx := security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForTenantAdmin(tenantID),
		slog.Default(), nil, nil,
	)

	src := &PinotSaasSource{}
	m := src.GetDynamicLabelMapping(ctx, accountID)

	// The test integration in DB has level/message/timestamp populated and
	// namespace/pod/container stored as empty strings (which overwrite the
	// defaults baked into GetPinotConfig). GetDynamicLabelMapping must skip
	// the empty values — those canonical keys should be absent so tenant/account
	// log_labels can legitimately fill them in.
	assert.Equal(t, "severity", m["level"], "level should map to integration's severity column")
	assert.Equal(t, "log", m["message"], "message should map to integration's pinot_message_col")
	assert.Equal(t, "timestamp", m["timestamp"], "timestamp should map to integration's pinot_timestamp_col")
	_, hasNs := m["namespace"]
	_, hasPod := m["pod"]
	_, hasContainer := m["container"]
	assert.False(t, hasNs, "namespace should be absent when integration stores empty string")
	assert.False(t, hasPod, "pod should be absent when integration stores empty string")
	assert.False(t, hasContainer, "container should be absent when integration stores empty string")
}

// TestPinotSaas_GetMergedLabelMapping_DynamicWins_Integration is the core
// precedence regression test. It seeds tenant_attrs.log_labels with values that
// would conflict with the Pinot integration config, then asserts the merged
// mapping still uses the integration's column names — i.e. dynamic > tenant.
func TestPinotSaas_GetMergedLabelMapping_DynamicWins_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)
	tenantID := requireTestTenantID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)
	snapshotTenantLogLabels(t, dbMgr, tenantID)

	// Tenant tries to remap level/message to "tenant_wins_*". Integration form
	// has level → severity, message → log. Pinot dynamic must win.
	upsertTenantLogLabelsRaw(t, dbMgr, tenantID, map[string]string{
		"level":   "tenant_wins_level",
		"message": "tenant_wins_message",
		"app":     "tenant_app_col", // non-overlapping — should fall through
	})

	ctx := security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForTenantAdmin(tenantID),
		slog.Default(), nil, nil,
	)
	merged := getMergedLabelMapping(ctx, accountID, &PinotSaasSource{})

	assert.Equal(t, "severity", merged["level"], "Pinot integration must win over tenant_attrs")
	assert.Equal(t, "log", merged["message"], "Pinot integration must win over tenant_attrs")
	assert.Equal(t, "tenant_app_col", merged["app"], "non-overlapping tenant key should pass through")
}

// TestPinotSaas_GetQuery_AppliesDynamicMapping_Integration is the end-to-end SQL
// builder check: build a FetchLogRequest whose WHERE clause uses canonical keys
// (level, namespace), translate via getMergedLabelMapping, then call GetQuery
// and assert the emitted SQL references the integration's actual column names.
func TestPinotSaas_GetQuery_AppliesDynamicMapping_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)
	tenantID := requireTestTenantID(t)

	ctx := security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForTenantAdmin(tenantID),
		slog.Default(), nil, nil,
	)

	src := &PinotSaasSource{}

	// Step 1: get merged mapping (this is what FetchLogs / GetLogsQuery do at the
	// dispatch layer before calling GetQuery).
	mapping := getMergedLabelMapping(ctx, accountID, src)

	// Step 2: build a WHERE clause using canonical names that the user/UI would emit.
	rawWhere := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"level": {query.Eq: "error"},
		},
	}
	translatedWhere := convertWhereClauseWithMApping(rawWhere, mapping)

	// Step 3: call GetQuery — mirrors service.go GetLogsQuery's post-translation step.
	req := FetchLogRequest{
		AccountId: accountID,
		StartTime: 1715900000000, // arbitrary epoch ms
		EndTime:   1715910000000,
		Limit:     10,
		QueryRequest: LogsQueryBuilderRequest{
			Where: translatedWhere,
		},
	}
	sql, err := src.GetQuery(ctx, req)
	require.NoError(t, err)

	t.Logf("generated SQL: %s", sql)

	// SQL must use the Pinot column name "severity" (from integration's
	// pinot_severity_col), not the canonical "level".
	assert.Contains(t, sql, `"severity" = 'error'`,
		"WHERE clause must translate canonical 'level' → integration's 'severity' column")
	// Table and timestamp column must also come from the integration config.
	assert.Contains(t, sql, `FROM "k8s_logs"`, "table must come from integration")
	assert.Contains(t, sql, `"timestamp" BETWEEN`, "timestamp column must come from integration")
	// Canonical 'level' must NOT appear as a column reference.
	assert.NotContains(t, strings.ToLower(sql), `"level"`,
		"canonical 'level' must have been rewritten to integration column name")
}

// TestPinotSaas_GetQuery_TenantNonOverlapAppliedToWhere_Integration verifies that
// non-overlapping canonical keys provided only by tenant_attrs (i.e. keys the
// Pinot integration form does not expose) still get translated into the WHERE
// clause. This proves the dynamic-on-top precedence does not erase lower layers.
func TestPinotSaas_GetQuery_TenantNonOverlapAppliedToWhere_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)
	tenantID := requireTestTenantID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)
	snapshotTenantLogLabels(t, dbMgr, tenantID)
	upsertTenantLogLabelsRaw(t, dbMgr, tenantID, map[string]string{
		"app": "app_label_col",
	})

	ctx := security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForTenantAdmin(tenantID),
		slog.Default(), nil, nil,
	)
	src := &PinotSaasSource{}
	mapping := getMergedLabelMapping(ctx, accountID, src)
	require.Equal(t, "app_label_col", mapping["app"],
		"tenant non-overlap key must be present in merged map")

	rawWhere := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"app": {query.Eq: "checkout"},
		},
	}
	translatedWhere := convertWhereClauseWithMApping(rawWhere, mapping)

	req := FetchLogRequest{
		AccountId: accountID,
		StartTime: 1715900000000,
		EndTime:   1715910000000,
		Limit:     10,
		QueryRequest: LogsQueryBuilderRequest{
			Where: translatedWhere,
		},
	}
	sql, err := src.GetQuery(ctx, req)
	require.NoError(t, err)

	t.Logf("generated SQL: %s", sql)
	assert.Contains(t, sql, `"app_label_col" = 'checkout'`,
		"tenant-only canonical key must be translated to its Pinot column in WHERE")
}

// TestPinotSaas_GetLogsQuery_SortFieldsTranslated_Integration is the regression
// guard for the GetLogsQuery dispatcher: it must translate both WHERE and
// ORDER BY identifiers via the merged mapping (matching FetchLogs). A
// SortField using canonical name "level" must end up as ORDER BY "severity"
// in the emitted SQL when the Pinot integration's pinot_severity_col = "severity".
func TestPinotSaas_GetLogsQuery_SortFieldsTranslated_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)
	tenantID := requireTestTenantID(t)

	ctx := security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForTenantAdmin(tenantID),
		slog.Default(), nil, nil,
	)

	req := FetchLogRequest{
		AccountId:         accountID,
		LogProvider:       "pinot",
		LogProviderSource: "user",
		StartTime:         1715900000000,
		EndTime:           1715910000000,
		Limit:             25,
		SortFields:        []SortField{{ColumnName: "level", Order: "desc"}},
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"level": {query.Eq: "error"},
				},
			},
		},
	}

	resp, err := GetLogsQuery(ctx, req)
	require.NoError(t, err)
	t.Logf("generated SQL: %s", resp.Query)

	assert.Contains(t, resp.Query, `"severity" = 'error'`,
		"WHERE clause canonical 'level' must translate to integration's 'severity'")
	assert.Contains(t, resp.Query, `ORDER BY "severity" DESC`,
		"ORDER BY canonical 'level' must translate to integration's 'severity'")
	assert.NotContains(t, resp.Query, `ORDER BY "level"`,
		"raw canonical 'level' must not survive in ORDER BY")
}

// Ensure the env helper warnings stay localized to this package.
var _ = os.Getenv
