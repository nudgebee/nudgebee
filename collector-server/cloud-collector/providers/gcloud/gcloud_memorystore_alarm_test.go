package gcloud

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/collector/cloud/providers"
)

func TestLoadMemorystoreAlarmTemplates(t *testing.T) {
	templates, err := LoadGCPAlarmTemplates(ServiceNameMemorystore)
	require.NoError(t, err)
	require.Len(t, templates, 4)

	var lessThan int
	for _, tpl := range templates {
		assert.NotEmpty(t, tpl.Configuration.MetricTypeFilter, "template %s: metric_type_filter is required", tpl.Name)
		assert.Equal(t, "redis_instance", tpl.Configuration.ResourceType, "template %s", tpl.Name)
		assert.Equal(t, "NATIVE", tpl.MetricType, "template %s: all Memorystore templates are unconditional", tpl.Name)
		if tpl.Configuration.ComparisonOperator == "COMPARISON_LT" {
			lessThan++
		}
	}
	assert.Equal(t, 1, lessThan, "exactly the cache-hit-ratio template alerts on dropping BELOW the threshold")
}

func TestGetResourceFilterForServiceMemorystore(t *testing.T) {
	// redis_instance's instance_id label carries the full resource path — the
	// validation regex must accept the slashes
	got := GetResourceFilterForService(ServiceNameMemorystore, "projects/acme-project/locations/us-central1/instances/orders-cache")
	want := `resource.type="redis_instance" AND resource.labels.instance_id="projects/acme-project/locations/us-central1/instances/orders-cache"`
	assert.Equal(t, want, got)
}

func memorystoreTestInstance(name string) providers.Resource {
	fullPath := "projects/acme-project/locations/us-central1/instances/" + name
	return providers.Resource{
		Id:          name,
		Name:        name,
		Type:        "memorystore",
		Arn:         fullPath,
		ServiceName: ServiceNameMemorystore,
		Status:      providers.ResourceStatusActive,
		Region:      "us-central1",
		Tags:        map[string][]string{"team": {"payments"}},
		Meta: map[string]interface{}{
			"selfLink":       fullPath,
			"tier":           "STANDARD_HA",
			"memory_size_gb": int32(4),
		},
	}
}

// TestMemorystoreAlarmRecommendations proves the full wiring: every template
// fires for an unmonitored instance, and the alarm config scopes the alert to
// the full instance path (what redis_instance's instance_id label carries).
func TestMemorystoreAlarmRecommendations(t *testing.T) {
	s := &memorystoreService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	instance := memorystoreTestInstance("orders-cache")
	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{instance})
	require.NoError(t, err)

	var rules []string
	for _, rec := range recs {
		require.True(t, strings.HasSuffix(rec.RuleName, "_alarm_missing"), "unexpected rule %s", rec.RuleName)
		rules = append(rules, rec.RuleName)

		assert.Equal(t, "acme-project", rec.Data["project_id"], "rule %s", rec.RuleName)

		cfg, ok := rec.Data["alarm_config"].(providers.AlarmCreationConfig)
		require.True(t, ok, "rule %s: alarm_config must be an AlarmCreationConfig", rec.RuleName)
		require.Len(t, cfg.Dimensions, 1)
		assert.Equal(t, "instance_id", cfg.Dimensions[0].Name)
		assert.Equal(t, "projects/acme-project/locations/us-central1/instances/orders-cache",
			cfg.Dimensions[0].Value, "rule %s: alert must scope to the full instance path", rec.RuleName)
	}

	assert.ElementsMatch(t, []string{
		"gcp_memorystore_memory_usage_alarm_missing",
		"gcp_memorystore_evicted_keys_alarm_missing",
		"gcp_memorystore_connected_clients_alarm_missing",
		"gcp_memorystore_cache_hit_ratio_alarm_missing",
	}, rules)
}

// TestMemorystoreAlarmFallbackPathReconstruction proves that when selfLink is
// absent from Meta, the alert still scopes to the full instance path rebuilt
// from project + region + id — a short id would never match redis_instance's
// instance_id label.
func TestMemorystoreAlarmFallbackPathReconstruction(t *testing.T) {
	s := &memorystoreService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	instance := memorystoreTestInstance("orders-cache")
	delete(instance.Meta, "selfLink")

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{instance})
	require.NoError(t, err)
	require.NotEmpty(t, recs)

	cfg, ok := recs[0].Data["alarm_config"].(providers.AlarmCreationConfig)
	require.True(t, ok)
	require.Len(t, cfg.Dimensions, 1)
	assert.Equal(t, "projects/acme-project/locations/us-central1/instances/orders-cache",
		cfg.Dimensions[0].Value, "fallback must reconstruct the full path")
}

// TestMemorystoreAlarmSuppressedByExistingPolicy proves an existing alert on
// the same metric+instance suppresses only that recommendation.
func TestMemorystoreAlarmSuppressedByExistingPolicy(t *testing.T) {
	s := &memorystoreService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	instance := memorystoreTestInstance("orders-cache")
	instance.Meta["AlertPolicies"] = []interface{}{
		map[string]interface{}{
			"displayName": "orders-cache memory",
			"conditions": []interface{}{
				map[string]interface{}{
					"displayName": "memory usage above 85%",
					"conditionThreshold": map[string]interface{}{
						"filter":         `resource.type="redis_instance" AND resource.labels.instance_id="projects/acme-project/locations/us-central1/instances/orders-cache" AND metric.type="redis.googleapis.com/stats/memory/usage_ratio"`,
						"thresholdValue": 0.85,
					},
				},
			},
		},
	}

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{instance})
	require.NoError(t, err)

	var rules []string
	for _, rec := range recs {
		rules = append(rules, rec.RuleName)
	}
	assert.NotContains(t, rules, "gcp_memorystore_memory_usage_alarm_missing",
		"existing memory alert must suppress the memory recommendation")
	assert.Len(t, rules, 3, "the other three templates still fire")
}
