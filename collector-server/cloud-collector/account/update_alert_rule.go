package account

import (
	"fmt"
	"strconv"
	"strings"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/aws"
	"nudgebee/collector/cloud/providers/azure"
	"nudgebee/collector/cloud/providers/gcloud"
	"nudgebee/collector/cloud/security"
)

// UpdateAlertRuleRequest mirrors the payload sent by the api-server CloudAlertRuleSource.
type UpdateAlertRuleRequest struct {
	AccountId      string                 `json:"account_id" validate:"required"`
	ExternalRuleId string                 `json:"external_rule_id"`
	Name           string                 `json:"name"`
	AlertType      string                 `json:"alert_type"`
	Query          string                 `json:"query"`
	Severity       string                 `json:"severity"`
	Duration       string                 `json:"duration"`
	Enabled        bool                   `json:"enabled"`
	ProviderConfig map[string]interface{} `json:"provider_config"`
}

// UpdateAlertRuleResponse is returned to the api-server.
type UpdateAlertRuleResponse struct {
	ExternalRuleId string `json:"external_rule_id"`
	Status         string `json:"status"`
}

// UpdateAlertRule applies a threshold (and optional duration) change to a cloud provider's
// alert rule. Currently implemented for AWS CloudWatch (idempotent PutMetricAlarm via
// describe-then-reput). GCP/Azure return a clear not-implemented error so the api-server can
// continue with the local DB update.
func UpdateAlertRule(ctx *security.RequestContext, req UpdateAlertRuleRequest) (UpdateAlertRuleResponse, error) {
	acct, providerName, err := getAccount(ctx, req.AccountId)
	if err != nil {
		return UpdateAlertRuleResponse{}, fmt.Errorf("failed to get account: %w", err)
	}

	switch strings.ToLower(providerName) {
	case "aws":
		arn := req.ExternalRuleId
		if arn == "" {
			arn = stringFromConfig(req.ProviderConfig, "alarm_arn")
		}
		region, alarmName := parseCloudWatchAlarmARN(arn)
		if alarmName == "" {
			// Fall back to the rule name when no ARN is available.
			alarmName = req.Name
		}
		if region == "" {
			region = stringFromConfig(req.ProviderConfig, "region")
		}
		if region == "" || alarmName == "" {
			return UpdateAlertRuleResponse{}, fmt.Errorf("could not resolve CloudWatch alarm region/name (arn=%q, name=%q)", arn, req.Name)
		}

		threshold, ok := floatFromConfig(req.ProviderConfig, "threshold")
		if !ok {
			return UpdateAlertRuleResponse{}, fmt.Errorf("threshold missing from provider_config")
		}
		durationMin := intFromConfig(req.ProviderConfig, "duration_minutes")

		if err := aws.UpdateCloudWatchAlarmThreshold(ctx.GetContext(), acct, alarmName, region, threshold, durationMin); err != nil {
			return UpdateAlertRuleResponse{}, err
		}
		return UpdateAlertRuleResponse{ExternalRuleId: arn, Status: "updated"}, nil

	case "gcp":
		policyName := req.ExternalRuleId
		if policyName == "" {
			return UpdateAlertRuleResponse{}, fmt.Errorf("external_rule_id (alert policy name) is required for GCP")
		}
		threshold, ok := floatFromConfig(req.ProviderConfig, "threshold")
		if !ok {
			return UpdateAlertRuleResponse{}, fmt.Errorf("threshold missing from provider_config")
		}
		durationMin := intFromConfig(req.ProviderConfig, "duration_minutes")

		cloudCtx := providers.NewCloudProviderContextWithLogger(ctx.GetContext(), ctx.GetLogger())
		if err := gcloud.UpdateGCPAlertPolicyThreshold(cloudCtx, acct, policyName, threshold, durationMin); err != nil {
			return UpdateAlertRuleResponse{}, err
		}
		return UpdateAlertRuleResponse{ExternalRuleId: policyName, Status: "updated"}, nil

	case "azure":
		resourceID := req.ExternalRuleId
		if resourceID == "" {
			return UpdateAlertRuleResponse{}, fmt.Errorf("external_rule_id (alert resource id) is required for Azure")
		}
		threshold, ok := floatFromConfig(req.ProviderConfig, "threshold")
		if !ok {
			return UpdateAlertRuleResponse{}, fmt.Errorf("threshold missing from provider_config")
		}
		durationMin := intFromConfig(req.ProviderConfig, "duration_minutes")

		cloudCtx := providers.NewCloudProviderContextWithLogger(ctx.GetContext(), ctx.GetLogger())
		if err := azure.UpdateAzureMetricAlertThreshold(cloudCtx, acct, resourceID, threshold, durationMin); err != nil {
			return UpdateAlertRuleResponse{}, err
		}
		return UpdateAlertRuleResponse{ExternalRuleId: resourceID, Status: "updated"}, nil

	default:
		return UpdateAlertRuleResponse{}, fmt.Errorf("alert rule update not yet supported for provider %q", providerName)
	}
}

// parseCloudWatchAlarmARN extracts (region, alarmName) from a CloudWatch alarm ARN of the form
// arn:aws:cloudwatch:<region>:<account>:alarm:<name>. Returns empty strings if it is not an ARN.
func parseCloudWatchAlarmARN(arn string) (string, string) {
	if !strings.HasPrefix(arn, "arn:") {
		return "", ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 7 || parts[5] != "alarm" {
		return "", ""
	}
	region := parts[3]
	alarmName := strings.Join(parts[6:], ":")
	return region, alarmName
}

func stringFromConfig(cfg map[string]interface{}, key string) string {
	if cfg == nil {
		return ""
	}
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

func floatFromConfig(cfg map[string]interface{}, key string) (float64, bool) {
	if cfg == nil {
		return 0, false
	}
	switch v := cfg[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func intFromConfig(cfg map[string]interface{}, key string) int {
	if cfg == nil {
		return 0
	}
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return 0
}
