package eventrule

import (
	"testing"

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
	assert.NotEmpty(t, SyncableProviders())
	for source, entry := range syncableProviders {
		assert.NotEmpty(t, entry.provider, "source %q has no provider", source)
		assert.NotEmpty(t, entry.providerSource, "source %q has no provider source", source)
		// The API-facing name is the bare provider; the row lands on that
		// provider's webhook source so a synced definition and the webhook
		// reporting its firings share one row.
		assert.NotContains(t, source, "_webhook", "request source %q must be the provider, not its webhook", source)
		assert.Equal(t, source+"_webhook", entry.ruleSource,
			"source %q must write to its own *_webhook event_rules.source", source)
	}
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
