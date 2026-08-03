package adapter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyRecommendationMergesNotificationTargetsIntoAlarmConfig(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"success":true,"message":"ok","reference_id":"my-alarm"}}`))
	}))
	defer server.Close()

	originalURL := config.Config.CloudCollectorServerUrl
	originalHeader := config.Config.CloudCollectorServerTokenHeader
	config.Config.CloudCollectorServerUrl = server.URL
	config.Config.CloudCollectorServerTokenHeader = "x-collector-token"
	defer func() {
		config.Config.CloudCollectorServerUrl = originalURL
		config.Config.CloudCollectorServerTokenHeader = originalHeader
	}()

	var recJson models.Json
	require.NoError(t, json.Unmarshal([]byte(`{"alarm_config":{"alarm_name":"orig","threshold":10,"metric_name":"CPUUtilization"}}`), &recJson))

	ctx := &testAccountAdapterContext{securityContext: security.NewSecurityContextForAuditRelay("test-tenant", "test-user")}
	awsAdapterInstance := &awsAdapter{}
	resp, err := awsAdapterInstance.ApplyRecommendation(ctx, ApplyRecommendationRequest{
		Data: map[string]any{
			"reason":                      "test",
			"custom_alarm_name":           "custom-name",
			"custom_threshold":            42.5,
			"custom_notification_targets": []any{"arn:aws:sns:us-east-1:111122223333:alerts"},
		},
		Recommendation: models.Recommendation{
			RuleName:       "aws_ec2_cpu_utilization_alarm_missing",
			Recommendation: recJson,
		},
	}, nil, "")

	require.NoError(t, err)
	assert.Equal(t, RecommendationResolutionStatusSuccess, resp.Status)

	data, ok := captured["data"].(map[string]any)
	require.True(t, ok, "payload should carry a data object")
	alarmConfig, ok := data["alarm_config"].(map[string]any)
	require.True(t, ok, "data should carry alarm_config")
	assert.Equal(t, "custom-name", alarmConfig["alarm_name"])
	assert.Equal(t, 42.5, alarmConfig["threshold"])
	assert.Equal(t, []any{"arn:aws:sns:us-east-1:111122223333:alerts"}, alarmConfig["notification_targets"])
	_, hasTopLevel := data["custom_notification_targets"]
	assert.False(t, hasTopLevel, "custom_notification_targets must not leak as a top-level data key")
}

func TestApplyRecommendationWithoutNotificationTargetsLeavesAlarmConfigUntouched(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"success":true,"message":"ok","reference_id":"my-alarm"}}`))
	}))
	defer server.Close()

	originalURL := config.Config.CloudCollectorServerUrl
	originalHeader := config.Config.CloudCollectorServerTokenHeader
	config.Config.CloudCollectorServerUrl = server.URL
	config.Config.CloudCollectorServerTokenHeader = "x-collector-token"
	defer func() {
		config.Config.CloudCollectorServerUrl = originalURL
		config.Config.CloudCollectorServerTokenHeader = originalHeader
	}()

	var recJson models.Json
	require.NoError(t, json.Unmarshal([]byte(`{"alarm_config":{"alarm_name":"orig","threshold":10}}`), &recJson))

	ctx := &testAccountAdapterContext{securityContext: security.NewSecurityContextForAuditRelay("test-tenant", "test-user")}
	awsAdapterInstance := &awsAdapter{}
	_, err := awsAdapterInstance.ApplyRecommendation(ctx, ApplyRecommendationRequest{
		Data: map[string]any{"reason": "test"},
		Recommendation: models.Recommendation{
			RuleName:       "aws_ec2_cpu_utilization_alarm_missing",
			Recommendation: recJson,
		},
	}, nil, "")

	require.NoError(t, err)
	alarmConfig, ok := captured["data"].(map[string]any)["alarm_config"].(map[string]any)
	require.True(t, ok, "data should carry alarm_config")
	_, hasTargets := alarmConfig["notification_targets"]
	assert.False(t, hasTargets, "no override given, so alarm_config must not gain notification_targets")
}
