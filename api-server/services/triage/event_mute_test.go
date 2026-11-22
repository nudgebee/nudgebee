package triage

import (
	"testing"
	"time"

	"nudgebee/services/eventrule"
	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func muteEvent(source, key, ns, owner string) *models.Event {
	return &models.Event{Source: &source, AggregationKey: &key, SubjectNamespace: &ns, SubjectOwner: &owner}
}

// A workload mute silences that alert for that workload only: not the same
// workload name in another namespace, not a workload whose name extends it, not
// another alert on the same workload.
func TestWorkloadMuteMatchesOnlyThatWorkload(t *testing.T) {
	b := eventMuteBasis{
		source: "kubernetes_api_server", alert: "report_crash_loop",
		namespace: "benchmark-scenarios", owner: "payment-worker",
		sources: []string{"kubernetes_api_server"},
	}
	m, err := b.specFor("t", "a", eventrule.MuteScopeWorkload)
	require.NoError(t, err)
	rule := m.matchRule("kubernetes_api_server")

	assert.True(t, MatchesRule(muteEvent("kubernetes_api_server", "report_crash_loop", "benchmark-scenarios", "payment-worker"), rule))
	assert.False(t, MatchesRule(muteEvent("kubernetes_api_server", "report_crash_loop", "prod", "payment-worker"), rule), "same name, other namespace")
	assert.False(t, MatchesRule(muteEvent("kubernetes_api_server", "report_crash_loop", "benchmark-scenarios", "payment-worker-v2"), rule), "longer owner name")
	assert.False(t, MatchesRule(muteEvent("kubernetes_api_server", "report_crash_loop", "benchmark-scenarios", "checkout"), rule), "other workload")
	assert.False(t, MatchesRule(muteEvent("kubernetes_api_server", "pod_oom_killer_enricher", "benchmark-scenarios", "payment-worker"), rule), "other alert")
}

func TestRuleMuteMatchesEveryWorkload(t *testing.T) {
	b := eventMuteBasis{source: "pagerduty_webhook", alert: "OtelDemoGRPCClientErrorRate", namespace: "demo", owner: "fraud-detection", sources: []string{"pagerduty_webhook"}}
	m, err := b.specFor("t", "a", eventrule.MuteScopeRule)
	require.NoError(t, err)
	rule := m.matchRule("pagerduty_webhook")
	assert.Nil(t, rule.MatchNamespace)
	assert.Nil(t, rule.MatchService)
	assert.True(t, MatchesRule(muteEvent("pagerduty_webhook", "OtelDemoGRPCClientErrorRate", "demo", "ad"), rule))
}

// Webhook subject resolution can name the owner and leave the namespace empty; an
// owner-only mute would silence that workload name in every namespace.
func TestWorkloadScopeNeedsNamespaceAndOwner(t *testing.T) {
	for _, tc := range []struct{ ns, owner string }{{"", "payment-worker"}, {"demo", ""}, {"", ""}} {
		b := eventMuteBasis{source: "pagerduty_webhook", alert: "X", namespace: tc.ns, owner: tc.owner, sources: []string{"pagerduty_webhook"}}
		_, err := b.specFor("t", "a", eventrule.MuteScopeWorkload)
		assert.ErrorIs(t, err, eventrule.ErrOperationUnavailable, "%+v", tc)
		assert.Equal(t, []EventMuteScope{{Scope: eventrule.MuteScopeRule}}, b.offeredScopes(), "%+v", tc)
	}
	b := eventMuteBasis{alert: "X", namespace: "demo", owner: "ad"}
	assert.Equal(t, []string{eventrule.MuteScopeWorkload, eventrule.MuteScopeRule},
		[]string{b.offeredScopes()[0].Scope, b.offeredScopes()[1].Scope}, "narrowest first")

	_, err := b.specFor("t", "a", "fingerprint")
	assert.Error(t, err)
}

func TestMuteSourcesFor(t *testing.T) {
	promRule := &eventrule.RuleCapabilities{Source: "prometheus", Operations: eventrule.RuleOperations{Mute: eventrule.Capability{Available: true}}}
	// A Prometheus alert delivered by PagerDuty: the mute covers every delivery path.
	assert.ElementsMatch(t, eventrule.MuteEventSources("prometheus"), muteSourcesFor("pagerduty_webhook", promRule))

	// A rule whose mute is not offered (datadog, built-in checks): the event's own source.
	dd := &eventrule.RuleCapabilities{Source: "datadog_webhook", Operations: eventrule.RuleOperations{Mute: eventrule.Capability{Reason: "no"}}}
	assert.Equal(t, []string{"datadog_webhook"}, muteSourcesFor("datadog_webhook", dd))
	assert.Equal(t, []string{"kubernetes_api_server"}, muteSourcesFor("kubernetes_api_server", nil))

	// Never a set that leaves out the event's own source.
	assert.Equal(t, []string{"elasticsearch_webhook"}, muteSourcesFor("elasticsearch_webhook", promRule))
}

// A mute made on a pushed alarm while it was stored as an EventBridge event
// keeps working once the alarm is stored as a CloudWatch alarm: it silences the
// row, the row reports it, and unmuting from the row reaches it.
func TestMute_EventBridgeMuteCoversAlarmStoredAsCloudWatchAlarm(t *testing.T) {
	const alarm = "orders-queue-depth"
	ebRule := &eventrule.RuleCapabilities{Source: "AWS_EventBridge", Operations: eventrule.RuleOperations{Mute: eventrule.Capability{Available: true}}}
	cwRule := &eventrule.RuleCapabilities{Source: "AWS_CloudWatch_Alarm", Operations: eventrule.RuleOperations{Mute: eventrule.Capability{Available: true}}}
	source, key := "AWS_CloudWatch_Alarm", alarm
	row := &models.Event{Source: &source, AggregationKey: &key}

	// The mute rows as written then: one per source of the EventBridge rule.
	oldMute := muteSpec{ruleID: "eb-rule", alert: alarm, scope: eventrule.MuteScopeRule, sources: []string{"AWS_EventBridge"}}
	assert.True(t, MatchesRule(row, oldMute.matchRule("AWS_EventBridge")), "the old mute row suppresses and is reported on the alarm row")

	// Unmuting from the row selects owned rows by these sources.
	assert.Contains(t, muteSourcesFor(source, ebRule), "AWS_EventBridge")

	// A CloudWatch alarm rule's mute still covers both.
	assert.ElementsMatch(t, []string{"AWS_CloudWatch_Alarm", "AWS_EventBridge"}, muteSourcesFor(source, cwRule))
}

func TestMarkMutedScopes(t *testing.T) {
	day := time.Now().Add(24 * time.Hour)
	week := time.Now().Add(7 * 24 * time.Hour)
	scopes := []EventMuteScope{{Scope: eventrule.MuteScopeWorkload}, {Scope: eventrule.MuteScopeRule}}
	markMutedScopes(scopes, []EventSuppression{
		{IsMute: true, MuteScope: eventrule.MuteScopeWorkload, ExpiresAt: &day},
		{IsMute: true, MuteScope: eventrule.MuteScopeWorkload, ExpiresAt: &week},
		// A suppression that is not a mute never makes a scope look muted.
		{IsMute: false, ExpiresAt: nil},
	})
	assert.True(t, scopes[0].Muted)
	assert.Equal(t, &week, scopes[0].ExpiresAt, "latest end wins")
	assert.False(t, scopes[1].Muted)

	scopes = []EventMuteScope{{Scope: eventrule.MuteScopeRule}}
	markMutedScopes(scopes, []EventSuppression{
		{IsMute: true, MuteScope: eventrule.MuteScopeRule, ExpiresAt: &day},
		{IsMute: true, MuteScope: eventrule.MuteScopeRule, ExpiresAt: nil},
	})
	assert.True(t, scopes[0].Muted)
	assert.Nil(t, scopes[0].ExpiresAt, "a row without an end means until unmuted")
}
