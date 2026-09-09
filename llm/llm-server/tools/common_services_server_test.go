package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClampLogLimitForProvider pins the per-provider limit cap added 2026-06-22
// after a 30-day production sample showed 12 fetch_logs failures matching
// `loki: limit exceeds maximum of 5000`. The clamp lives in the tool layer so
// any caller (current agent prompt, a future integration, a raw API hit) gets
// a capped fetch instead of an opaque backend error.
func TestClampLogLimitForProvider(t *testing.T) {
	cases := []struct {
		name        string
		provider    string
		requested   int
		wantLimit   int
		wantClamped bool
	}{
		{
			name:        "loki at cap — no clamp, returns same",
			provider:    "loki",
			requested:   lokiMaxLogLimit,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: false,
		},
		{
			name:        "loki above cap — clamps to cap",
			provider:    "loki",
			requested:   10000,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "loki at 5001 — clamps to cap",
			provider:    "loki",
			requested:   lokiMaxLogLimit + 1,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "loki well below cap — no clamp",
			provider:    "loki",
			requested:   1000,
			wantLimit:   1000,
			wantClamped: false,
		},
		{
			name:        "case-insensitive provider match",
			provider:    "Loki",
			requested:   9999,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "whitespace-tolerant provider match (Gemini #32847 review)",
			provider:    "  loki  ",
			requested:   9999,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "unknown provider — passes through unchanged, no warning fires",
			provider:    "elasticsearch",
			requested:   10000,
			wantLimit:   10000,
			wantClamped: false,
		},
		{
			name:        "empty provider — passes through unchanged",
			provider:    "",
			requested:   10000,
			wantLimit:   10000,
			wantClamped: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, clamped := clampLogLimitForProvider(c.provider, c.requested)
			assert.Equal(t, c.wantLimit, got)
			assert.Equal(t, c.wantClamped, clamped)
		})
	}
}

// TestCloudProviderToObservabilityFallback pins the pure mapping used by the
// Get{Metrics,Log,Trace}Provider CLI fallback: only GCP/Azure cloud accounts
// route to a cloud-CLI observability backend; AWS and unknowns keep the
// existing prometheus/k8s/clickhouse defaults. Case and surrounding whitespace
// in cloud_accounts.cloud_provider must not defeat the match.
func TestCloudProviderToObservabilityFallback(t *testing.T) {
	cases := []struct {
		name          string
		cloudProvider string
		want          string
	}{
		{"gcp lowercase", "gcp", "gcp"},
		{"gcp uppercase", "GCP", "gcp"},
		{"gcp padded", "  gcp ", "gcp"},
		{"azure lowercase", "azure", "azure"},
		{"azure mixed case", "Azure", "azure"},
		{"aws lowercase", "aws", "aws"},
		{"aws uppercase", "AWS", "aws"},
		{"empty", "", ""},
		{"unknown", "digitalocean", ""},
		{"gke is not a cloud_provider value", "gke", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, cloudProviderToObservabilityFallback(c.cloudProvider))
		})
	}
}

func TestIsCloudObservabilityProvider(t *testing.T) {
	for _, provider := range []string{"aws", " AWS ", "gcp", "azure"} {
		assert.True(t, isCloudObservabilityProvider(provider), provider)
	}
	for _, provider := range []string{"", "prometheus", "datadog"} {
		assert.False(t, isCloudObservabilityProvider(provider), provider)
	}
}

// TestGetLogProvider_ServesCachedEntry asserts the cache-hit path short-circuits
// before the services-server round-trip. A single fetch_logs call resolves the
// log provider three times (LogAgent construction, FetchLogsAgent construction,
// logs_execute_v2 construction) and none of them share an instance, so this
// short-circuit is what keeps those three calls to one round-trip.
//
// The seeded provider is deliberately NOT "k8s": on a cache miss the uncached
// resolver returns the "k8s" default for a non-UUID account, so getting "loki"
// back is unambiguous proof the cached value was served.
func TestEffectiveLogProvider(t *testing.T) {
	resolved := services_server.ObservabilityProvider{Provider: "loki", DefaultIndex: "logs-*"}

	if got := EffectiveLogProvider(resolved, "  "); got.Provider != resolved.Provider || got.DefaultIndex != resolved.DefaultIndex {
		t.Fatalf("empty override changed resolved provider: %#v", got)
	}
	if got := EffectiveLogProvider(resolved, "LOKI"); got.Provider != resolved.Provider || got.DefaultIndex != resolved.DefaultIndex {
		t.Fatalf("equivalent override discarded resolved config: %#v", got)
	}
	if got := EffectiveLogProvider(resolved, " datadog "); got.Provider != "datadog" || got.DefaultIndex != "" {
		t.Fatalf("override was not isolated to provider name: %#v", got)
	}
}

func TestGetLogProvider_ServesCachedEntry(t *testing.T) {
	acct := "cached-provider-" + t.Name()
	seeded := services_server.ObservabilityProvider{
		Provider:          "loki",
		IntegrationSource: "integration",
		DefaultIndex:      "app-logs-*",
		Capabilities: services_server.ProviderCapabilities{
			SupportedOperators: []string{"_eq", "_ilike"},
			LabelMappings:      map[string]string{"app": "deployment.keyword", "namespace": "namespace"},
		},
	}

	data, err := common.MarshalJson(cachedLogProvider{Provider: seeded, ExpiresAt: time.Now().Add(logProviderCacheTTL)})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(logProviderCacheNS, acct, data, common.CacheSetWithExpiration(logProviderCacheTTL)))

	got, err := GetLogProvider(acct)
	require.NoError(t, err)
	assert.Equal(t, seeded.Provider, got.Provider)
	assert.Equal(t, seeded.IntegrationSource, got.IntegrationSource)
	assert.Equal(t, seeded.DefaultIndex, got.DefaultIndex)
	// Capabilities must survive the JSON round-trip: LabelMappings is what
	// buildCanonicalLogQueryPrompt renders as the canonical_name → backend_field
	// block. Losing it would silently drop the canonical vocabulary and push the
	// generator onto provider-native guesses, which match zero rows on backends
	// whose real keys differ (the exact failure the canonical path exists to fix).
	assert.Equal(t, seeded.Capabilities.LabelMappings, got.Capabilities.LabelMappings)
	assert.Equal(t, seeded.Capabilities.SupportedOperators, got.Capabilities.SupportedOperators)

	t.Run("invalidation drops the entry", func(t *testing.T) {
		require.NoError(t, common.CacheDelete(logProviderCacheNS, acct))
		_, ok := common.CacheGet(logProviderCacheNS, acct)
		assert.False(t, ok, "integration-change invalidation must drop the cached provider")
	})
}

// TestGetLogProvider_IgnoresExpiredEntry pins the stamped-expiry check. The
// default in_memory cache backend (bigcache) drops per-entry TTLs, so without
// this check a cached provider would live until the global LifeWindow evicted
// it — long past logProviderCacheTTL. The seeded entry is already expired, so
// serving it would be the bug; falling through to a fresh resolve (which yields
// the "k8s" default for this non-UUID account) is correct.
func TestGetLogProvider_IgnoresExpiredEntry(t *testing.T) {
	acct := "expired-provider-" + t.Name()
	stale := services_server.ObservabilityProvider{Provider: "loki"}

	data, err := common.MarshalJson(cachedLogProvider{Provider: stale, ExpiresAt: time.Now().Add(-time.Minute)})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(logProviderCacheNS, acct, data))

	got, err := GetLogProvider(acct)
	require.NoError(t, err)
	assert.NotEqual(t, "loki", got.Provider, "an expired entry must not be served")
}

func TestHasConnectedK8sAgent_InvalidUUID(t *testing.T) {
	assert.False(t, hasConnectedK8sAgent(""), "empty account ID must return false")
	assert.False(t, hasConnectedK8sAgent("not-a-uuid"), "non-UUID account ID must return false")
}

func TestHasConnectedK8sAgent_IgnoresCloudCollectorAgent(t *testing.T) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil || dbms == nil || dbms.Db == nil {
		t.Skip("metastore DB not available in this test environment")
	}

	// Verify against an active AWS cloud account that has a CONNECTED AWS agent row in agent table.
	// It must NOT be treated as having a connected K8s agent.
	var awsAccountID string
	err = dbms.Db.Get(&awsAccountID, `
		SELECT cloud_account_id::text
		FROM agent
		WHERE status = 'CONNECTED' AND type = 'AWS'
		LIMIT 1
	`)
	if err == nil && awsAccountID != "" {
		assert.False(t, hasConnectedK8sAgent(awsAccountID),
			"an account with only a cloud-collector agent (type=AWS) must not report hasConnectedK8sAgent=true")
	}

	// Verify that an account with a real connected K8s agent DOES report true.
	var k8sAccountID string
	err = dbms.Db.Get(&k8sAccountID, `
		SELECT cloud_account_id::text
		FROM agent
		WHERE status = 'CONNECTED' AND lower(type) = 'k8s'
		LIMIT 1
	`)
	if err == nil && k8sAccountID != "" {
		assert.True(t, hasConnectedK8sAgent(k8sAccountID),
			"an account with a connected K8s agent (type=k8s) must report hasConnectedK8sAgent=true")
	}
}

func TestHasConnectedK8sAgentInDb_Sqlmock(t *testing.T) {
	t.Run("returns false when no connected k8s agent exists", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		accountID := "00666e9f-3774-4f5d-b86c-60b201ae18c5"
		mock.ExpectQuery(`select count\(\*\) from agent where status = 'CONNECTED' and lower\(type\) = 'k8s' and cloud_account_id = \$1`).
			WithArgs(accountID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		sqlxDb := sqlx.NewDb(db, "sqlmock")
		got := hasConnectedK8sAgentInDb(sqlxDb, accountID)
		assert.False(t, got, "expected false when connected k8s agent count is 0")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns true when connected k8s agent exists", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		accountID := "21a3b9a9-8d4e-4a8f-9744-dcd61d828344"
		mock.ExpectQuery(`select count\(\*\) from agent where status = 'CONNECTED' and lower\(type\) = 'k8s' and cloud_account_id = \$1`).
			WithArgs(accountID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		sqlxDb := sqlx.NewDb(db, "sqlmock")
		got := hasConnectedK8sAgentInDb(sqlxDb, accountID)
		assert.True(t, got, "expected true when connected k8s agent count is > 0")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fails closed on database error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		accountID := "00666e9f-3774-4f5d-b86c-60b201ae18c5"
		mock.ExpectQuery(`select count\(\*\) from agent where status = 'CONNECTED' and lower\(type\) = 'k8s' and cloud_account_id = \$1`).
			WithArgs(accountID).
			WillReturnError(assert.AnError)

		sqlxDb := sqlx.NewDb(db, "sqlmock")
		got := hasConnectedK8sAgentInDb(sqlxDb, accountID)
		assert.True(t, got, "must fail closed (return true) when db query fails")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
