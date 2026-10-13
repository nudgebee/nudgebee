package eventrule

import (
	"testing"

	"nudgebee/services/observability/alertrule"

	"github.com/stretchr/testify/assert"
)

// event_rules.severity is FK-constrained to exactly {critical, warning}; a
// provider reporting anything else must be clamped, not passed through, or the
// insert fails with 23503.
func TestNormalizeSyncSeverity(t *testing.T) {
	allowed := map[string]bool{"critical": true, "warning": true}
	for _, in := range []string{"critical", "warning", "high", "P1", "info", "", "banana"} {
		got := normalizeSyncSeverity(in)
		assert.True(t, allowed[got], "severity %q produced %q", in, got)
	}
	assert.Equal(t, "critical", normalizeSyncSeverity("critical"))
	assert.Equal(t, "warning", normalizeSyncSeverity("high"))
}

// Every syncable source must resolve to a provider pair the alertrule registry
// knows, and must be a bare provider name rather than a *_webhook source — a
// synced rule is the definition, not a firing of it.
func TestSyncableProvidersAreWellFormed(t *testing.T) {
	// Webhook sources named before the *_webhook convention settled.
	webhookSourceExceptions := map[string]string{"prometheus": "prometheus_alertmanager_webhook"}
	assert.NotEmpty(t, SyncableProviders())
	for source, entry := range syncableProviders {
		assert.NotEmpty(t, entry.provider, "source %q has no provider", source)
		assert.NotEmpty(t, entry.providerSource, "source %q has no provider source", source)
		// The API-facing name is the bare provider; the row lands on that
		// provider's webhook source so a synced definition and the webhook
		// reporting its firings share one row.
		assert.NotContains(t, source, "_webhook", "request source %q must be the provider, not its webhook", source)
		want := source + "_webhook"
		if exception, ok := webhookSourceExceptions[source]; ok {
			want = exception
		}
		assert.Equal(t, want, entry.ruleSource,
			"source %q must write to its own *_webhook event_rules.source", source)
	}
}

// Prometheus rules must land on the Alertmanager webhook's source so the firings
// that webhook reports and the definitions the sync reads share one row.
func TestPrometheusWritesToAlertmanagerWebhookSource(t *testing.T) {
	entry := syncableProviders["prometheus"]
	assert.Equal(t, "prometheus", entry.provider)
	assert.Equal(t, "user", entry.providerSource, "only the direct integration is synced; the agent pushes its own inventory")
	assert.Equal(t, "prometheus_alertmanager_webhook", entry.ruleSource)
}

// Datadog rules must land on datadog_webhook so they merge with the rows the
// webhook already created, rather than appearing as a separate source.
func TestDatadogWritesToWebhookSource(t *testing.T) {
	assert.Equal(t, "datadog_webhook", syncableProviders["datadog"].ruleSource)
	assert.Equal(t, "elasticsearch_webhook", syncableProviders["elasticsearch"].ruleSource)
}

// Kibana's _find is filtered by the caller's feature privileges and still
// returns 200, so absence from a listing cannot be read as deletion. Guarded by
// a test because the consequence — disabling live rules — is silent.
func TestElasticsearchNeverReconcilesDeletions(t *testing.T) {
	entry, ok := syncableProviders["elasticsearch"]
	assert.True(t, ok, "elasticsearch must be syncable")
	assert.Equal(t, "ES", entry.provider)
	assert.Equal(t, "elasticsearch_webhook", entry.ruleSource)
	assert.False(t, entry.reconcileDeletions,
		"a privilege-filtered Kibana listing would disable rules the credential simply cannot see")

	// Providers with an authoritative listing keep reconciliation.
	assert.True(t, syncableProviders["datadog"].reconcileDeletions)
}

func TestSyncProviderRulesRejectsUnknownInput(t *testing.T) {
	// Every guard runs before any DB or provider call, so this needs no fixture.
	_, err := SyncProviderRules(nil, "", "datadog")
	assert.ErrorContains(t, err, "account_id is required")

	_, err = SyncProviderRules(nil, "acct-1", "not-a-provider")
	assert.ErrorContains(t, err, "not supported")

	// A nil security context must be reported, not dereferenced: everything
	// past this point reads the tenant id and logger off it.
	_, err = SyncProviderRules(nil, "acct-1", "datadog")
	assert.ErrorContains(t, err, "security context is required")

	_, err = SyncProviderRulesForAccount(nil, SyncProviderRulesRequest{})
	assert.ErrorContains(t, err, "account_id is required")

	_, err = SyncProviderRulesForAccount(nil, SyncProviderRulesRequest{AccountId: "acct-1"})
	assert.ErrorContains(t, err, "security context is required")
}

// CubeAPM implements alertrule.AlertRuleLister, so it must be registered here or
// the listing is never called and synced rules never appear.
func TestCubeAPMIsSyncable(t *testing.T) {
	entry, ok := syncableProviders["cubeapm"]
	assert.True(t, ok, "cubeapm must be syncable")
	assert.Equal(t, "cubeapm", entry.provider)
	assert.Equal(t, "user", entry.providerSource)
	assert.Equal(t, "cubeapm_webhook", entry.ruleSource,
		"synced definitions must land on the same row as the webhook reporting their firings")
	// The admin API returns the whole rule set in one call with no permission
	// filtering, so an absent rule can be trusted to mean deleted upstream.
	assert.True(t, entry.reconcileDeletions)
}

// SolarWinds is the first provider synced without an inventory call. Its API has
// no list, search or enumerate operation, so the only rules it can answer for
// are those Nudgebee already holds an id for — which a firing supplies.
func TestSolarWindsIsSyncedByKnownIDs(t *testing.T) {
	entry, ok := syncableProviders["solarwinds"]
	assert.True(t, ok, "solarwinds must be syncable")
	assert.Equal(t, "solarwinds", entry.provider)
	assert.Equal(t, "user", entry.providerSource)
	assert.Equal(t, "solarwinds_webhook", entry.ruleSource)
	assert.True(t, entry.byKnownIDs,
		"SolarWinds has no list operation, so it can only be synced by reading ids back")

	// The provider must be able to answer a single read, and must not claim it
	// can be listed — a listing would have to invent the inventory.
	assert.True(t, alertrule.SupportsGet(entry.provider, entry.providerSource),
		"sync by id needs AlertRuleGetter")
	assert.False(t, alertrule.SupportsListing(entry.provider, entry.providerSource),
		"listing is impossible for SolarWinds, not merely unimplemented")
}

// The id set for a byKnownIDs provider comes from our own rows, so "absent from
// the result" only ever means "we never knew about it" — never "deleted
// upstream". Reconciling deletions on that basis would disable a rule whose read
// merely failed. This encodes the invariant for any provider added later.
func TestByKnownIDsProvidersNeverReconcileDeletions(t *testing.T) {
	for source, entry := range syncableProviders {
		if !entry.byKnownIDs {
			continue
		}
		assert.False(t, entry.reconcileDeletions,
			"%s is synced by known ids, so an unreadable rule would look deleted and be disabled", source)
	}
}
