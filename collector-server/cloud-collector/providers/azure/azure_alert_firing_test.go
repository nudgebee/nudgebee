package azure

import (
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/stretchr/testify/assert"
)

const (
	testAlertSub        = "00000000-0000-0000-0000-000000000001"
	testMetricRule      = "/subscriptions/" + testAlertSub + "/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts/cpu-high"
	testLogRule         = "/subscriptions/" + testAlertSub + "/resourcegroups/rg-1/providers/microsoft.insights/scheduledqueryrules/pod-stuck"
	testActivityLogRule = "/subscriptions/" + testAlertSub + "/resourceGroups/rg-1/providers/microsoft.insights/activityLogAlerts/admin-op"
	testAlertTarget     = "/subscriptions/" + testAlertSub + "/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm-1"
)

func TestAzureAlertRuleIdentity(t *testing.T) {
	sub, rule, ok := azureAlertRuleIdentity(testMetricRule + ":" + testAlertTarget)
	assert.True(t, ok)
	assert.Equal(t, testAlertSub, sub)
	assert.Equal(t, "/subscriptions/"+testAlertSub+"/resourcegroups/rg-1/providers/microsoft.insights/metricalerts/cpu-high", rule)

	_, rule, ok = azureAlertRuleIdentity(testLogRule + ":" + testAlertTarget)
	assert.True(t, ok)
	assert.Equal(t, testLogRule, rule)

	for _, fingerprint := range []string{
		testActivityLogRule + ":" + testAlertTarget,
		"/subscriptions/" + testAlertSub + "/providers/Microsoft.AlertsManagement/alerts/0f0f0f0f-0000-0000-0000-000000000000",
		"projects/p/alertPolicies/1:gce_instance:vm-1",
	} {
		_, _, ok := azureAlertRuleIdentity(fingerprint)
		assert.False(t, ok, fingerprint)
	}
}

func TestAlertRulesResolveThemselves(t *testing.T) {
	assert.True(t, metricAlertResolvesItself(&armmonitor.MetricAlertProperties{Enabled: to.Ptr(true)}), "auto-mitigate defaults to on")
	assert.True(t, metricAlertResolvesItself(&armmonitor.MetricAlertProperties{Enabled: to.Ptr(true), AutoMitigate: to.Ptr(true)}))
	assert.False(t, metricAlertResolvesItself(&armmonitor.MetricAlertProperties{Enabled: to.Ptr(true), AutoMitigate: to.Ptr(false)}))
	assert.False(t, metricAlertResolvesItself(&armmonitor.MetricAlertProperties{Enabled: to.Ptr(false)}))
	assert.False(t, metricAlertResolvesItself(nil))

	logRule := func(kind armmonitor.Kind, enabled, autoMitigate *bool) *armmonitor.ScheduledQueryRuleResource {
		return &armmonitor.ScheduledQueryRuleResource{
			Kind:       to.Ptr(kind),
			Properties: &armmonitor.ScheduledQueryRuleProperties{Enabled: enabled, AutoMitigate: autoMitigate},
		}
	}
	assert.True(t, logAlertResolvesItself(logRule(armmonitor.KindLogAlert, to.Ptr(true), nil)))
	assert.True(t, logAlertResolvesItself(logRule(armmonitor.KindLogAlert, to.Ptr(true), to.Ptr(true))))
	assert.False(t, logAlertResolvesItself(logRule(armmonitor.KindLogAlert, to.Ptr(true), to.Ptr(false))), "a stateless rule leaves its firings fired")
	assert.False(t, logAlertResolvesItself(logRule(armmonitor.KindLogAlert, to.Ptr(false), to.Ptr(true))))
	assert.False(t, logAlertResolvesItself(logRule(armmonitor.KindLogToMetric, to.Ptr(true), to.Ptr(true))))
}

func TestMarkOngoingAlerts(t *testing.T) {
	otherSub := "00000000-0000-0000-0000-000000000002"
	snapshot := newAlertRuleSnapshot(time.Now())
	addSubscriptionRules(snapshot, testAlertSub, map[string]bool{
		"/subscriptions/" + testAlertSub + "/resourcegroups/rg-1/providers/microsoft.insights/metricalerts/cpu-high": true,
	})

	events := []providers.Event{
		{EventId: testMetricRule + ":" + testAlertTarget, EventStatus: providers.EventStatusFiring},
		{EventId: testMetricRule + ":" + testAlertTarget, EventStatus: providers.EventStatusResolved},
		// A stateless rule: not in the listing.
		{EventId: testLogRule + ":" + testAlertTarget, EventStatus: providers.EventStatusFiring},
		{EventId: testActivityLogRule + ":" + testAlertTarget, EventStatus: providers.EventStatusFiring},
		// A subscription whose rules were not listed.
		{EventId: "/subscriptions/" + otherSub + "/resourceGroups/rg/providers/Microsoft.Insights/metricAlerts/x:" + testAlertTarget, EventStatus: providers.EventStatusFiring},
	}
	markOngoingAlerts(events, snapshot)

	assert.Equal(t, testAlertSub, events[0].OngoingScope)
	for _, event := range events[1:] {
		assert.Empty(t, event.OngoingScope, event.EventId)
	}

	// A stored firing of a listed rule is on with no known start, so the store
	// keeps it until its own resolve; one of a rule no longer listed has ended.
	scope, key, ok := snapshot.Identify(testMetricRule + ":" + testAlertTarget)
	assert.True(t, ok)
	assert.Equal(t, []string{scope}, snapshot.Scopes)
	since, on := snapshot.Since[key]
	assert.True(t, on)
	assert.True(t, since.IsZero())
	_, key, ok = snapshot.Identify(testLogRule + ":" + testAlertTarget)
	assert.True(t, ok)
	_, on = snapshot.Since[key]
	assert.False(t, on)
}
