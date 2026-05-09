package observability

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const errNotImplemented = "not implemented"

// mockLogSource is a minimal LogSource for testing getMergedLabelMapping
// without touching any external provider.
type mockLogSource struct {
	staticMapping map[string]string
}

func (m *mockLogSource) GetLabelMapping() map[string]string { return m.staticMapping }
func (m *mockLogSource) QueryLogs(_ *security.RequestContext, _ FetchLogRequest) ([]OutputLog, error) {
	return nil, errors.New(errNotImplemented)
}
func (m *mockLogSource) QueryLabels(_ *security.RequestContext, _ FetchLogLabelRequest) ([]OutputLogLabel, error) {
	return nil, errors.New(errNotImplemented)
}
func (m *mockLogSource) QueryLabelValues(_ *security.RequestContext, _ FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	return nil, errors.New(errNotImplemented)
}
func (m *mockLogSource) GetQuery(_ *security.RequestContext, _ FetchLogRequest) (string, error) {
	return "", errors.New(errNotImplemented)
}
func (m *mockLogSource) GetSupportedOperators() []string { return nil }

// newLogLabelCtx creates a super-admin request context suitable for all tests here.
func newLogLabelCtx() *security.RequestContext {
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
}

// seedCache writes value into the real in-memory cache and registers a cleanup
// to delete it when the test ends. Used instead of mocking CacheGet to avoid
// gomonkey global-patch contamination issues on ARM64.
func seedCache(t *testing.T, key string, value map[string]string) {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	err = common.CacheSet(logLabelsCacheNamespace, key, b)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = common.CacheDelete(logLabelsCacheNamespace, key)
	})
}

// clearCache removes a cache key before the test runs and re-clears on cleanup.
func clearCache(t *testing.T, key string) {
	t.Helper()
	_ = common.CacheDelete(logLabelsCacheNamespace, key)
	t.Cleanup(func() {
		_ = common.CacheDelete(logLabelsCacheNamespace, key)
	})
}

// mockDBUnavailable patches GetDatabaseManager to return an error, simulating
// an unavailable metastore. Returns the patches; caller must defer Reset().
func mockDBUnavailable(t *testing.T) *gomonkey.Patches {
	t.Helper()
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(database.GetDatabaseManager, func(_ database.DatabaseManagerType) (*database.DatabaseManager, error) {
		return nil, errors.New("metastore unavailable in unit test")
	})
	return patches
}

// ---------------------------------------------------------------------------
// Part 1 – getCustomLogLabels unit tests
// ---------------------------------------------------------------------------

// TestGetCustomLogLabels_EmptyAccountID verifies that an empty account ID returns
// an empty map immediately without touching cache or DB.
func TestGetCustomLogLabels_EmptyAccountID(t *testing.T) {
	result := getCustomLogLabels(newLogLabelCtx(), "")
	assert.Empty(t, result)
}

// TestGetCustomLogLabels_CacheHit verifies that a warm cache entry is returned
// and no DB call is made. Uses the real cache (pre-populated via CacheSet) to
// avoid gomonkey global-patch contamination on ARM64.
func TestGetCustomLogLabels_CacheHit(t *testing.T) {
	const key = "unit-cache-hit"
	clearCache(t, key)

	want := map[string]string{"pod": "k8s_pod", "namespace": "k8s_ns"}
	seedCache(t, key, want)

	// Fail the test if DB is reached — a cache hit must not touch the DB.
	patches := mockDBUnavailable(t)
	patches.ApplyFunc(database.GetDatabaseManager, func(_ database.DatabaseManagerType) (*database.DatabaseManager, error) {
		t.Fatal("DB must not be called on a cache hit")
		return nil, nil
	})
	defer patches.Reset()

	result := getCustomLogLabels(newLogLabelCtx(), key)
	assert.Equal(t, want, result)
}

// TestGetCustomLogLabels_CacheHit_InvalidJSON verifies that a corrupt cache entry
// is ignored and the function falls through to the DB path gracefully.
func TestGetCustomLogLabels_CacheHit_InvalidJSON(t *testing.T) {
	const key = "unit-cache-invalid-json"
	_ = common.CacheDelete(logLabelsCacheNamespace, key)
	// Write raw invalid bytes directly — bypasses seedCache helper
	_ = common.CacheSet(logLabelsCacheNamespace, key, []byte("not-valid-json{{{"))
	t.Cleanup(func() { _ = common.CacheDelete(logLabelsCacheNamespace, key) })

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	result := getCustomLogLabels(newLogLabelCtx(), key)
	assert.Empty(t, result, "corrupt cache + no DB should degrade to empty map")
}

// TestGetCustomLogLabels_DBManagerError verifies graceful degradation when the
// database manager is unavailable (e.g. connection refused).
func TestGetCustomLogLabels_DBManagerError(t *testing.T) {
	const key = "unit-db-error"
	clearCache(t, key)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	result := getCustomLogLabels(newLogLabelCtx(), key)
	assert.Empty(t, result, "should return empty map when DB is unavailable")
}

// ---------------------------------------------------------------------------
// Part 2 – getMergedLabelMapping unit tests
// ---------------------------------------------------------------------------

// TestGetMergedLabelMapping_EmptyCustom verifies that the static provider map is
// returned unchanged when there are no DB-configured overrides for the account.
func TestGetMergedLabelMapping_EmptyCustom(t *testing.T) {
	const key = "unit-merge-no-custom"
	clearCache(t, key)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	staticMap := map[string]string{
		"pod":       "pod_name",
		"namespace": "namespace_name",
		"container": "container_name",
	}
	result := getMergedLabelMapping(newLogLabelCtx(), key, &mockLogSource{staticMapping: staticMap})
	assert.Equal(t, staticMap, result)
}

// TestGetMergedLabelMapping_CustomOverridesStatic verifies that DB-configured labels
// override matching keys in the static provider map while non-overridden keys remain.
func TestGetMergedLabelMapping_CustomOverridesStatic(t *testing.T) {
	const key = "unit-merge-override"
	clearCache(t, key)

	seedCache(t, key, map[string]string{"pod": "k8s_pod_name", "namespace": "kube_ns"})

	staticMap := map[string]string{
		"pod":       "pod_name",       // overridden by custom
		"namespace": "namespace_name", // overridden by custom
		"container": "container_name", // preserved
	}
	result := getMergedLabelMapping(newLogLabelCtx(), key, &mockLogSource{staticMapping: staticMap})

	assert.Equal(t, "k8s_pod_name", result["pod"], "custom should override static pod")
	assert.Equal(t, "kube_ns", result["namespace"], "custom should override static namespace")
	assert.Equal(t, "container_name", result["container"], "non-overridden key should remain")
	assert.Len(t, result, 3)
}

// TestGetMergedLabelMapping_CustomAddsNewKey verifies that a DB-configured label
// for a canonical key absent from the static map is added to the merged result.
func TestGetMergedLabelMapping_CustomAddsNewKey(t *testing.T) {
	const key = "unit-merge-new-key"
	clearCache(t, key)

	seedCache(t, key, map[string]string{"app": "k8s_app"}) // "app" not in static map

	staticMap := map[string]string{"pod": "pod_name", "namespace": "namespace_name"}
	result := getMergedLabelMapping(newLogLabelCtx(), key, &mockLogSource{staticMapping: staticMap})

	assert.Equal(t, "k8s_app", result["app"], "new key from custom should appear in merged map")
	assert.Equal(t, "pod_name", result["pod"], "existing static keys should be preserved")
	assert.Len(t, result, 3)
}

// TestGetMergedLabelMapping_BothEmpty verifies that two empty maps merge to an empty map.
func TestGetMergedLabelMapping_BothEmpty(t *testing.T) {
	const key = "unit-merge-both-empty"
	clearCache(t, key)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	result := getMergedLabelMapping(newLogLabelCtx(), key, &mockLogSource{staticMapping: map[string]string{}})
	assert.Empty(t, result)
}

// TestGetMergedLabelMapping_DefaultQueryExcluded verifies that "defaultQuery" stored
// in log_labels is filtered at DB-read time and never appears in the label mapping.
// (The cache never stores defaultQuery; filtering happens before CacheSet.)
// Full coverage of the DB-path filter is in the integration test.
func TestGetMergedLabelMapping_DefaultQueryExcluded(t *testing.T) {
	const key = "unit-merge-dq"
	clearCache(t, key)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	staticMap := map[string]string{"pod": "pod_name", "namespace": "ns_name"}
	result := getMergedLabelMapping(newLogLabelCtx(), key, &mockLogSource{staticMapping: staticMap})

	_, hasDefaultQuery := result["defaultQuery"]
	assert.False(t, hasDefaultQuery, "defaultQuery must never appear in label mapping")
	// Static map is returned as-is when custom map is empty
	assert.Equal(t, "pod_name", result["pod"])
	assert.Equal(t, "ns_name", result["namespace"])
}

// ---------------------------------------------------------------------------
// Part 3 – getTenantLogLabels unit tests
// ---------------------------------------------------------------------------

// TestGetTenantLogLabels_EmptyTenantID verifies that an empty tenant ID returns
// an empty map immediately without touching cache or DB.
func TestGetTenantLogLabels_EmptyTenantID(t *testing.T) {
	result := getTenantLogLabels(newLogLabelCtx(), "")
	assert.Empty(t, result)
}

// TestGetTenantLogLabels_CacheHit verifies that a warm cache entry (stored under
// the "t:" prefix) is returned and no DB call is made.
func TestGetTenantLogLabels_CacheHit(t *testing.T) {
	const tid = "tenant-cache-hit"
	cacheKey := tenantCacheKeyPrefix + tid
	clearCache(t, cacheKey)

	want := map[string]string{"pod": "tenant_pod", "namespace": "tenant_ns"}
	seedCache(t, cacheKey, want)

	patches := mockDBUnavailable(t)
	patches.ApplyFunc(database.GetDatabaseManager, func(_ database.DatabaseManagerType) (*database.DatabaseManager, error) {
		t.Fatal("DB must not be called on a cache hit")
		return nil, nil
	})
	defer patches.Reset()

	result := getTenantLogLabels(newLogLabelCtx(), tid)
	assert.Equal(t, want, result)
}

// TestGetTenantLogLabels_CacheHit_InvalidJSON verifies that a corrupt cache entry
// is ignored and the function falls through to the DB path gracefully.
func TestGetTenantLogLabels_CacheHit_InvalidJSON(t *testing.T) {
	const tid = "tenant-cache-invalid-json"
	cacheKey := tenantCacheKeyPrefix + tid
	_ = common.CacheDelete(logLabelsCacheNamespace, cacheKey)
	_ = common.CacheSet(logLabelsCacheNamespace, cacheKey, []byte("not-valid-json{{{"))
	t.Cleanup(func() { _ = common.CacheDelete(logLabelsCacheNamespace, cacheKey) })

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	result := getTenantLogLabels(newLogLabelCtx(), tid)
	assert.Empty(t, result, "corrupt cache + no DB should degrade to empty map")
}

// TestGetTenantLogLabels_DBManagerError verifies graceful degradation when the
// database manager is unavailable.
func TestGetTenantLogLabels_DBManagerError(t *testing.T) {
	const tid = "tenant-db-error"
	cacheKey := tenantCacheKeyPrefix + tid
	clearCache(t, cacheKey)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	result := getTenantLogLabels(newLogLabelCtx(), tid)
	assert.Empty(t, result, "should return empty map when DB is unavailable")
}

// ---------------------------------------------------------------------------
// Part 4 – Integration tests  (require TEST_ACCOUNT_ID env var)
//
// Run with:
//   TEST_ACCOUNT_ID=<uuid> go test ./observability/... -run TestLogLabels -v
// ---------------------------------------------------------------------------

const (
	skipShortMode   = "skipping integration test in short mode"
	skipNoAccountID = "TEST_ACCOUNT_ID not set — skipping integration test"
	skipNoTenantID  = "TEST_TENANT_ID not set — skipping integration test"
)

// requireTestAccountID returns TEST_ACCOUNT_ID or skips the test.
func requireTestAccountID(t *testing.T) string {
	t.Helper()
	id := os.Getenv("TEST_ACCOUNT_ID")
	if id == "" {
		t.Skip(skipNoAccountID)
	}
	return id
}

// upsertLogLabels inserts or updates cloud_account_attrs.log_labels for accountID
// and registers cleanup to delete the row and clear the cache.
func upsertLogLabels(t *testing.T, dbMgr *database.DatabaseManager, accountID string, labels map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(labels)
	_, err := dbMgr.Db.Exec(`
		INSERT INTO cloud_account_attrs (cloud_account_id, name, value)
		VALUES ($1, 'log_labels', $2)
		ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value
	`, accountID, string(raw))
	require.NoError(t, err, "failed to upsert log_labels")

	t.Cleanup(func() {
		_, _ = dbMgr.Db.Exec(
			`DELETE FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'log_labels'`,
			accountID,
		)
		_ = common.CacheDelete(logLabelsCacheNamespace, accountID)
	})
}

// TestGetCustomLogLabels_Integration inserts a known log_labels row for the test
// account, reads it back via getCustomLogLabels, and verifies:
//   - pod, namespace, app are returned correctly
//   - defaultQuery is excluded
//   - empty-value fields are excluded
//   - a second call hits the cache (fast path)
func TestGetCustomLogLabels_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "DB access required for integration test")

	upsertLogLabels(t, dbMgr, accountID, map[string]string{
		"pod":          "test_pod_field",
		"namespace":    "test_ns_field",
		"app":          "test_app_field",
		"defaultQuery": "level=error", // must be filtered
		"emptyField":   "",            // must be filtered
	})
	_ = common.CacheDelete(logLabelsCacheNamespace, accountID) // start cold

	ctx := newLogLabelCtx()

	t.Run("DBFetch", func(t *testing.T) {
		result := getCustomLogLabels(ctx, accountID)

		assert.Equal(t, "test_pod_field", result["pod"])
		assert.Equal(t, "test_ns_field", result["namespace"])
		assert.Equal(t, "test_app_field", result["app"])

		_, hasDefaultQuery := result["defaultQuery"]
		assert.False(t, hasDefaultQuery, "defaultQuery must be excluded")
		_, hasEmpty := result["emptyField"]
		assert.False(t, hasEmpty, "empty-value fields must be excluded")
	})

	t.Run("CacheHit", func(t *testing.T) {
		start := time.Now()
		result := getCustomLogLabels(ctx, accountID)
		elapsed := time.Since(start)

		assert.Equal(t, "test_pod_field", result["pod"], "cache hit should return same data")
		assert.Less(t, elapsed, 50*time.Millisecond, "cache hit should be sub-millisecond")
	})
}

// TestGetMergedLabelMapping_Integration verifies that a DB-backed custom label
// correctly overrides a Splunk-like static provider mapping end-to-end.
func TestGetMergedLabelMapping_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)

	upsertLogLabels(t, dbMgr, accountID, map[string]string{"pod": "my_custom_pod_field"})
	_ = common.CacheDelete(logLabelsCacheNamespace, accountID)

	// Use a Splunk-like static mapping without depending on the SplunkLogSource type.
	splunkLike := &mockLogSource{staticMapping: map[string]string{
		"pod":       "pod_name",
		"namespace": "namespace_name",
		"container": "container_name",
		"node":      "host",
	}}
	merged := getMergedLabelMapping(newLogLabelCtx(), accountID, splunkLike)

	assert.Equal(t, "my_custom_pod_field", merged["pod"],
		"custom pod label should override the static pod_name")
	assert.Equal(t, "namespace_name", merged["namespace"],
		"unconfigured fields should keep the provider's static value")
	assert.NotEmpty(t, merged["container"],
		"non-overridden container mapping should be preserved")
}

// TestFetchLogs_WithLogLabels_Integration runs an end-to-end FetchLogs call with
// a custom log_labels override in place, verifying the custom labels are applied.
//
// Requires:
//
//	TEST_ACCOUNT_ID  — account with a configured log provider
//	TEST_LOG_PROVIDER — (optional) override log provider name, e.g. "loki"
func TestFetchLogs_WithLogLabels_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	accountID := requireTestAccountID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)

	upsertLogLabels(t, dbMgr, accountID, map[string]string{
		"pod":       "custom_pod_label",
		"namespace": "custom_ns_label",
	})
	_ = common.CacheDelete(logLabelsCacheNamespace, accountID)

	ctx := newLogLabelCtx()
	now := time.Now()
	req := FetchLogRequest{
		AccountId:   accountID,
		LogProvider: os.Getenv("TEST_LOG_PROVIDER"),
		StartTime:   now.Add(-30 * time.Minute).UnixMilli(),
		EndTime:     now.UnixMilli(),
		Limit:       5,
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{},
		},
	}

	_, err = FetchLogs(ctx, req)
	if err != nil {
		t.Skipf("FetchLogs error (log provider may not be configured for this account): %v", err)
	}

	// After FetchLogs, the cache should be warm with the custom labels.
	cached := getCustomLogLabels(ctx, accountID)
	assert.Equal(t, "custom_pod_label", cached["pod"],
		"custom labels should be cached after FetchLogs")
	assert.Equal(t, "custom_ns_label", cached["namespace"])
}

// ---------------------------------------------------------------------------
// Tenant-level integration tests  (require TEST_TENANT_ID env var)
//
// Run with:
//   TEST_TENANT_ID=<uuid> TEST_ACCOUNT_ID=<uuid> \
//     go test ./observability/... -run TestGetMergedLabelMapping_Tenant -v
// ---------------------------------------------------------------------------

// requireTestTenantID returns TEST_TENANT_ID or skips the test.
func requireTestTenantID(t *testing.T) string {
	t.Helper()
	id := os.Getenv("TEST_TENANT_ID")
	if id == "" {
		t.Skip(skipNoTenantID)
	}
	return id
}

// upsertTenantLogLabels inserts or updates tenant_attrs.log_labels for tenantID
// and registers cleanup to delete the row and clear the cache.
func upsertTenantLogLabels(t *testing.T, dbMgr *database.DatabaseManager, tenantID string, labels map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(labels)
	_, err := dbMgr.Db.Exec(`
		INSERT INTO tenant_attrs (tenant_id, name, value)
		VALUES ($1, 'log_labels', $2)
		ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value
	`, tenantID, string(raw))
	require.NoError(t, err, "failed to upsert tenant log_labels")

	t.Cleanup(func() {
		_, _ = dbMgr.Db.Exec(
			`DELETE FROM tenant_attrs WHERE tenant_id = $1 AND name = 'log_labels'`,
			tenantID,
		)
		_ = common.CacheDelete(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantID)
	})
}

// newLogLabelCtxWithTenant creates a request context scoped to a specific tenant.
// Requires DB access (NewSecurityContextForSuperAdminAndTenant loads accountIds).
func newLogLabelCtxWithTenant(tenantID string) *security.RequestContext {
	return security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForSuperAdminAndTenant(tenantID),
		slog.Default(), nil, nil,
	)
}

// TestGetMergedLabelMapping_TenantOnly_Integration verifies that tenant-level labels
// are applied to all accounts under the tenant when there is no account override.
func TestGetMergedLabelMapping_TenantOnly_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	tenantID := requireTestTenantID(t)
	accountID := requireTestAccountID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)

	upsertTenantLogLabels(t, dbMgr, tenantID, map[string]string{
		"pod":       "tenant_pod_field",
		"namespace": "tenant_ns_field",
		"app":       "tenant_app_field",
	})
	// Ensure no account-level override exists
	_, _ = dbMgr.Db.Exec(
		`DELETE FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'log_labels'`,
		accountID,
	)
	_ = common.CacheDelete(logLabelsCacheNamespace, accountID)
	_ = common.CacheDelete(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantID)

	source := &mockLogSource{staticMapping: map[string]string{
		"pod":       "static_pod",
		"namespace": "static_ns",
		"container": "static_container",
	}}
	ctx := newLogLabelCtxWithTenant(tenantID)
	merged := getMergedLabelMapping(ctx, accountID, source)

	assert.Equal(t, "tenant_pod_field", merged["pod"], "tenant label should override static")
	assert.Equal(t, "tenant_ns_field", merged["namespace"], "tenant label should override static")
	assert.Equal(t, "tenant_app_field", merged["app"], "tenant-only key should appear in merged map")
	assert.Equal(t, "static_container", merged["container"], "non-overridden static key should be preserved")
}

// TestGetMergedLabelMapping_AccountOverridesTenant_Integration verifies that an
// account-level label wins over a tenant-level label for the same canonical key.
func TestGetMergedLabelMapping_AccountOverridesTenant_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	tenantID := requireTestTenantID(t)
	accountID := requireTestAccountID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)

	upsertTenantLogLabels(t, dbMgr, tenantID, map[string]string{
		"pod": "tenant_pod_field",
		"app": "tenant_app_field",
	})
	upsertLogLabels(t, dbMgr, accountID, map[string]string{
		"pod": "account_pod_field", // should win over tenant_pod_field
	})
	_ = common.CacheDelete(logLabelsCacheNamespace, accountID)
	_ = common.CacheDelete(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantID)

	source := &mockLogSource{staticMapping: map[string]string{
		"pod":       "static_pod",
		"namespace": "static_ns",
	}}
	ctx := newLogLabelCtxWithTenant(tenantID)
	merged := getMergedLabelMapping(ctx, accountID, source)

	assert.Equal(t, "account_pod_field", merged["pod"],
		"account label must win over tenant label on same key")
	assert.Equal(t, "tenant_app_field", merged["app"],
		"tenant-only key not overridden at account level should still appear")
	assert.Equal(t, "static_ns", merged["namespace"],
		"key set in neither account nor tenant should fall back to static")
}

// TestGetMergedLabelMapping_TenantAndAccountDisjoint_Integration verifies that
// non-overlapping keys from both tenant and account levels appear together.
func TestGetMergedLabelMapping_TenantAndAccountDisjoint_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip(skipShortMode)
	}
	tenantID := requireTestTenantID(t)
	accountID := requireTestAccountID(t)

	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)

	upsertTenantLogLabels(t, dbMgr, tenantID, map[string]string{
		"namespace": "tenant_ns_field", // tenant-only key
	})
	upsertLogLabels(t, dbMgr, accountID, map[string]string{
		"pod": "account_pod_field", // account-only key
	})
	_ = common.CacheDelete(logLabelsCacheNamespace, accountID)
	_ = common.CacheDelete(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantID)

	source := &mockLogSource{staticMapping: map[string]string{
		"pod":       "static_pod",
		"namespace": "static_ns",
		"container": "static_container",
	}}
	ctx := newLogLabelCtxWithTenant(tenantID)
	merged := getMergedLabelMapping(ctx, accountID, source)

	assert.Equal(t, "account_pod_field", merged["pod"],
		"account-only key should appear in merged map")
	assert.Equal(t, "tenant_ns_field", merged["namespace"],
		"tenant-only key should appear in merged map")
	assert.Equal(t, "static_container", merged["container"],
		"static-only key should be preserved unchanged")
	assert.Len(t, merged, 3)
}
