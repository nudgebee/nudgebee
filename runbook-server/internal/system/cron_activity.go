package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"nudgebee/runbook/common"
)

// Maximum response body size (1 MiB) — protects against OOM and keeps
// activity results within Temporal's 2-4MB payload limit.
const cronWebhookMaxResponseBytes = 1 << 20

// CronWebhookActivity calls the webhook URL with the configured headers and payload.
// Headers with value_from_env are resolved at execution time so rotated secrets are picked up.
// Returns the parsed JSON response body so it shows up as the workflow result in Temporal UI.
func (a *SystemActivities) CronWebhookActivity(ctx context.Context, input CronWebhookWorkflowInput) (map[string]any, error) {
	slog.Info("Executing cron webhook", "name", input.Name, "webhook", input.Webhook)

	body := map[string]any{
		"name":    input.Name,
		"payload": input.Payload,
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	for _, h := range input.Headers {
		value := h.Value
		if h.ValueFromEnv != "" {
			value = resolveEnvVar(h.ValueFromEnv)
		}
		if value != "" {
			headers[h.Name] = value
		}
	}

	opts := []common.HttpOption{
		common.HttpWithContext(ctx),
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(body),
	}
	if input.TimeoutSeconds > 0 {
		opts = append(opts, common.HttpWithTimeout(time.Duration(input.TimeoutSeconds)*time.Second))
	}

	resp, err := common.HttpPost(input.Webhook, opts...)
	if err != nil {
		return nil, fmt.Errorf("cron %s: HTTP request failed: %w", input.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, cronWebhookMaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("cron %s: failed to read response body: %w", input.Name, err)
	}

	if resp.StatusCode != 200 {
		var parsed map[string]any
		if err := json.Unmarshal(respBody, &parsed); err == nil {
			slog.Error("Cron webhook returned error", "name", input.Name, "status", resp.StatusCode, "response", parsed)
		}
		return nil, fmt.Errorf("cron %s: unexpected status %d: %s", input.Name, resp.StatusCode, string(respBody))
	}

	result := map[string]any{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			// Non-JSON body — return it as a string under "body"
			result["body"] = string(respBody)
		}
	}

	slog.Info("Cron webhook completed successfully", "name", input.Name, "response", result)
	return result, nil
}
