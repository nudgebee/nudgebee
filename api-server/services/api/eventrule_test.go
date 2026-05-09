package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExecutePlaybookForAws(t *testing.T) {
	start := time.UnixMilli(1755047287955)
	end := time.UnixMilli(1755050887955)
	response, err := eventrule.ExecutePlaybook(security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), slog.Default(), nil, nil), os.Getenv("TEST_CLOUD_ACCOUNT"), playbooks.PlaybookEvent{
		Name: "HighCPUUtilization",
		Labels: map[string]string{
			"alertname":                  "HighCPUUtilization",
			"rule_id":                    "HighCPUUtilization",
			"aws_event_arn":              "arn:aws:cloudwatch:us-east-1:864186153326:alarm:HighCPUUtilization",
			"aws_event_name":             "HighCPUUtilization",
			"aws_event_state":            "firing",
			"aws_region":                 "us-east-1",
			"aws_event_metric_name":      "CPUUtilization",
			"aws_event_metric_namespace": "AWS/EC2",
			"aws_event_metric_statistic": "Average",
			"aws_event_threshold":        "70",
			"aws_event_instance":         "i-0695d9d318b7bbf30",
			"aws_service_name":           "amazonec2",
			"aws_account":                "864186153326",
		},
		Annotations: map[string]string{},
		StartedAt:   &start,
		EndedAt:     &end,
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
	response2 := []any{}
	for _, r := range response {
		response2 = append(response2, r.Response)
	}
	respB, err := common.MarshalJson(response2)
	assert.Nil(t, err)
	print(string(respB))
}
