package gcloud

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/collector/cloud/providers"
)

func TestLoadPubSubAlarmTemplates(t *testing.T) {
	templates, err := LoadGCPAlarmTemplates(ServiceNamePubSub)
	require.NoError(t, err)
	require.Len(t, templates, 3)

	var conditional int
	for _, tpl := range templates {
		assert.NotEmpty(t, tpl.Configuration.MetricTypeFilter, "template %s: metric_type_filter is required", tpl.Name)
		assert.Equal(t, "pubsub_subscription", tpl.Configuration.ResourceType, "template %s", tpl.Name)
		if tpl.MetricType == "CONDITIONAL" {
			conditional++
			assert.NotEmpty(t, tpl.Conditions, "CONDITIONAL template %s must carry conditions", tpl.Name)
		}
	}
	assert.Equal(t, 1, conditional, "exactly the dead-letter template is CONDITIONAL")
}

func TestGetResourceFilterForServicePubSub(t *testing.T) {
	got := GetResourceFilterForService(ServiceNamePubSub, "orders-sub")
	want := `resource.type="pubsub_subscription" AND resource.labels.subscription_id="orders-sub"`
	assert.Equal(t, want, got)
}

// pubsubTestSubscription builds a subscription resource the way
// subscriptionToResourceV2 shapes it: full-path Id, short Name, meta keys.
func pubsubTestSubscription(name, deadLetterTopic string) providers.Resource {
	return providers.Resource{
		Id:          "projects/acme-project/subscriptions/" + name,
		Name:        name,
		Type:        "pubsub.googleapis.com/Subscription",
		ServiceName: ServiceNamePubSub,
		Status:      providers.ResourceStatusActive,
		Region:      "global",
		Tags:        map[string][]string{"team": {"payments"}},
		Meta: map[string]interface{}{
			"topic":              "orders",
			"dead_letter_topic":  deadLetterTopic,
			"retention_duration": "24h0m0s",
		},
	}
}

// TestPubSubAlarmRecommendations proves the full wiring: NATIVE templates fire
// for every subscription without a matching alert, the CONDITIONAL dead-letter
// template fires only when a dead-letter topic is configured, and topics never
// get subscription-metric recommendations.
func TestPubSubAlarmRecommendations(t *testing.T) {
	s := &pubSubService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	withDLQ := pubsubTestSubscription("orders-sub", "projects/acme-project/topics/orders-dlq")
	withoutDLQ := pubsubTestSubscription("audit-sub", "")
	topic := providers.Resource{
		Id:          "orders",
		Name:        "orders",
		Type:        "pubsub.googleapis.com/Topic",
		ServiceName: ServiceNamePubSub,
		Status:      providers.ResourceStatusActive,
		Region:      "global",
		Tags:        map[string][]string{"team": {"payments"}},
		Meta:        map[string]interface{}{},
	}

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{withDLQ, withoutDLQ, topic})
	require.NoError(t, err)

	alarmRules := map[string][]string{} // resource Name -> alarm rule names
	for _, rec := range recs {
		if !strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			continue
		}
		name, _ := rec.Data["subscription_name"].(string)
		alarmRules[name] = append(alarmRules[name], rec.RuleName)

		// Evidence and dimensions must carry the short name; ResourceId the full path
		assert.Equal(t, "acme-project", rec.Data["project_id"], "rule %s", rec.RuleName)
		assert.True(t, strings.HasPrefix(rec.ResourceId, "projects/acme-project/subscriptions/"),
			"rule %s: ResourceId must keep the full path (extractProjectID depends on it)", rec.RuleName)
	}

	// Subscription with DLQ: all 3 templates (dead-letter condition matches)
	assert.ElementsMatch(t, []string{
		"gcp_pubsub_oldest_unacked_age_alarm_missing",
		"gcp_pubsub_undelivered_messages_alarm_missing",
		"gcp_pubsub_dead_letter_rate_alarm_missing",
	}, alarmRules["orders-sub"])

	// Subscription without DLQ: only the two NATIVE templates
	assert.ElementsMatch(t, []string{
		"gcp_pubsub_oldest_unacked_age_alarm_missing",
		"gcp_pubsub_undelivered_messages_alarm_missing",
	}, alarmRules["audit-sub"])

	// Topic resources never get subscription-metric recommendations
	assert.Empty(t, alarmRules[""], "topic must not receive alarm recommendations")
}

// TestPubSubAlarmSuppressedByExistingPolicy proves an existing alert policy on
// the same metric+subscription suppresses the recommendation (checker path).
func TestPubSubAlarmSuppressedByExistingPolicy(t *testing.T) {
	s := &pubSubService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	sub := pubsubTestSubscription("orders-sub", "")
	sub.Meta["AlertPolicies"] = []interface{}{
		map[string]interface{}{
			"displayName": "orders-sub backlog age",
			"conditions": []interface{}{
				map[string]interface{}{
					"displayName": "oldest unacked age",
					"conditionThreshold": map[string]interface{}{
						"filter":         `resource.type="pubsub_subscription" AND resource.labels.subscription_id="orders-sub" AND metric.type="pubsub.googleapis.com/subscription/oldest_unacked_message_age"`,
						"thresholdValue": 600,
					},
				},
			},
		},
	}

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{sub})
	require.NoError(t, err)

	var alarmRules []string
	for _, rec := range recs {
		if strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			alarmRules = append(alarmRules, rec.RuleName)
		}
	}
	// Age alert exists → suppressed; undelivered still missing → recommended
	assert.ElementsMatch(t, []string{"gcp_pubsub_undelivered_messages_alarm_missing"}, alarmRules)
}
