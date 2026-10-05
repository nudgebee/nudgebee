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

// fakeCollector serves a fixed JSON body on every path and records the last request.
func fakeCollector(t *testing.T, status int, body string) (calls *int, captured *map[string]any, path *string) {
	t.Helper()
	n := 0
	var req map[string]any
	var p string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		p = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	originalURL := config.Config.CloudCollectorServerUrl
	originalHeader := config.Config.CloudCollectorServerTokenHeader
	config.Config.CloudCollectorServerUrl = server.URL
	config.Config.CloudCollectorServerTokenHeader = "x-collector-token"
	t.Cleanup(func() {
		config.Config.CloudCollectorServerUrl = originalURL
		config.Config.CloudCollectorServerTokenHeader = originalHeader
	})
	return &n, &req, &p
}

func storedRec(t *testing.T, ruleName, payload string) models.Recommendation {
	t.Helper()
	var recJson models.Json
	require.NoError(t, json.Unmarshal([]byte(payload), &recJson))
	return models.Recommendation{Id: "rec-1", CloudAccountId: "acct-1", RuleName: ruleName, Recommendation: recJson}
}

func testAdapterCtx() *testAccountAdapterContext {
	return &testAccountAdapterContext{securityContext: security.NewSecurityContextForAuditRelay("test-tenant", "test-user")}
}

const ebsPayload = `{"volume_id":"vol-recommended","volume_region":"us-east-1","service_name":"AmazonEC2","recommended_iops":4000}`

func TestApplyEBSUpgradeSendsStoredPayloadOnlyAndStaysInProgress(t *testing.T) {
	calls, captured, _ := fakeCollector(t, 200, `{"data":{"success":true,"message":"initiated","in_progress":true}}`)

	resp, err := (&awsAdapter{}).ApplyRecommendation(testAdapterCtx(), ApplyRecommendationRequest{
		Data: map[string]any{
			"volume_id":        "vol-other",
			"recommended_iops": 16000,
			"alarm_config":     map[string]any{"alarm_name": "x"},
		},
		Recommendation: storedRec(t, "aws_ec2_ebs_generation_upgrade", ebsPayload),
	}, nil, "")

	require.NoError(t, err)
	assert.Equal(t, 1, *calls)
	assert.Equal(t, RecommendationResolutionStatusInProgress, resp.Status)

	data := (*captured)["data"].(map[string]any)
	assert.Equal(t, "vol-recommended", data["volume_id"], "caller data must not retarget the volume")
	assert.Equal(t, float64(4000), data["recommended_iops"])
	_, hasAlarm := data["alarm_config"]
	assert.False(t, hasAlarm, "caller-supplied alarm_config must not reach the collector")
	assert.Equal(t, "AmazonEC2", (*captured)["service_name"], "service_name falls back to the stored payload")
}

func TestApplyRefusesNonAlarmRuleNotOnAllowlist(t *testing.T) {
	calls, _, _ := fakeCollector(t, 200, `{"data":{"success":true}}`)

	resp, err := (&awsAdapter{}).ApplyRecommendation(testAdapterCtx(), ApplyRecommendationRequest{
		Data:           map[string]any{},
		Recommendation: storedRec(t, "azure_disk_unattached_volume", `{"disk_name":"d1","service_name":"Microsoft.Compute/disks"}`),
	}, nil, "")

	require.Error(t, err)
	assert.Equal(t, RecommendationResolutionStatusFailed, resp.Status)
	assert.Equal(t, 0, *calls, "a refused rule must not reach the collector")
}

func TestApplyAlarmRuleStillSettlesSuccess(t *testing.T) {
	fakeCollector(t, 200, `{"data":{"success":true,"message":"ok","reference_id":"my-alarm"}}`)

	resp, err := (&awsAdapter{}).ApplyRecommendation(testAdapterCtx(), ApplyRecommendationRequest{
		Data:           map[string]any{},
		Recommendation: storedRec(t, "aws_ec2_cpu_utilization_alarm_missing", `{"alarm_config":{"alarm_name":"orig"}}`),
	}, nil, "")

	require.NoError(t, err)
	assert.Equal(t, RecommendationResolutionStatusSuccess, resp.Status)
}

func TestResolutionStatusForEBSUpgradeAsksCollector(t *testing.T) {
	tests := []struct {
		name           string
		httpStatus     int
		body           string
		wantStatus     RecommendationResolutionStatus
		wantUnverified bool
	}{
		{"succeeded", 200, `{"data":{"status":"Succeeded","message":"Volume is now gp3."}}`, RecommendationResolutionStatusSuccess, false},
		{"failed", 200, `{"data":{"status":"Failed","message":"modification failed"}}`, RecommendationResolutionStatusFailed, false},
		{"in progress", 200, `{"data":{"status":"InProgress","message":"running"}}`, RecommendationResolutionStatusInProgress, false},
		{"collector error", 500, `{"errors":[{"message":"throttled"}]}`, RecommendationResolutionStatusInProgress, true},
		{"unknown status", 200, `{"data":{"status":"Weird"}}`, RecommendationResolutionStatusInProgress, true},
		{"non-JSON error page", 502, `<html>Bad Gateway</html>`, RecommendationResolutionStatusInProgress, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, captured, path := fakeCollector(t, tt.httpStatus, tt.body)

			resp, err := (&awsAdapter{}).GetRecommendationResolutionStatus(testAdapterCtx(),
				storedRec(t, "aws_ec2_ebs_generation_upgrade", ebsPayload), "ref", models.Json{}, "")

			require.NoError(t, err)
			assert.Equal(t, 1, *calls)
			assert.Equal(t, "/v1/cloud/apply_recommendation_status", *path)
			assert.Equal(t, "AmazonEC2", (*captured)["service_name"])
			assert.Equal(t, tt.wantStatus, resp.Status)
			assert.Equal(t, tt.wantUnverified, resp.Unverified)
			if tt.httpStatus == 502 {
				assert.Contains(t, resp.StatusMessage, "HTTP 502 Bad Gateway")
			}
		})
	}
}

func TestResolutionStatusForAlarmRuleDoesNotCallCollector(t *testing.T) {
	calls, _, _ := fakeCollector(t, 200, `{}`)

	resp, err := (&awsAdapter{}).GetRecommendationResolutionStatus(testAdapterCtx(),
		storedRec(t, "aws_ec2_cpu_utilization_alarm_missing", `{"alarm_config":{}}`), "ref", models.Json{}, "created")

	require.NoError(t, err)
	assert.Equal(t, 0, *calls)
	assert.Equal(t, RecommendationResolutionStatusSuccess, resp.Status)
}

func TestApplyEBSUpgradeAlreadyRunningDoesNotCallCollector(t *testing.T) {
	calls, _, _ := fakeCollector(t, 200, `{"data":{"success":true,"in_progress":true}}`)

	resp, err := (&awsAdapter{}).ApplyRecommendation(testAdapterCtx(), ApplyRecommendationRequest{
		Data:           map[string]any{},
		Recommendation: storedRec(t, "aws_ec2_ebs_generation_upgrade", ebsPayload),
	}, []models.RecommendationResolution{{Id: "res-1", Status: models.RecommendationResolutionStatusInProgress, TypeReferenceId: "ref-1"}}, "res-1")

	require.NoError(t, err)
	assert.Equal(t, 0, *calls, "a running change must not be started again")
	assert.Equal(t, RecommendationResolutionStatusInProgress, resp.Status)
	assert.Equal(t, "ref-1", resp.ResolutionTypeRefrenceId)
}

func TestApplyEBSUpgradeResponseCarriesWhatWasApplied(t *testing.T) {
	fakeCollector(t, 200, `{"data":{"success":true,"in_progress":true}}`)

	resp, err := (&awsAdapter{}).ApplyRecommendation(testAdapterCtx(), ApplyRecommendationRequest{
		Data:           map[string]any{"recommended_iops": 16000},
		Recommendation: storedRec(t, "aws_ec2_ebs_generation_upgrade", ebsPayload),
	}, nil, "")

	require.NoError(t, err)
	assert.Equal(t, float64(4000), resp.Data["recommended_iops"])
}
