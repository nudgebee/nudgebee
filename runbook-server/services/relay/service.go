package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"time"
)

func ExecuteRelay(body ActionExecuteBody) (map[string]any, error) {

	relayRequest := RelayExecuteRequest{
		Body:    body,
		NoSinks: true,
		Cache:   false,
	}
	relayRequest.Body.Origin = "runbook-server"

	data := make(map[string]any)

	timeout := time.Duration(config.Config.RunbookServerRelayCommandExecutionTimeoutSeconds) * time.Second
	if body.Timeout > 0 {
		timeout = body.Timeout
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"X-SECRET-KEY": config.Config.RelayServerSecretKey,
	}
	if body.AgentType != "" {
		headers["X-NB-Agent-Type"] = body.AgentType
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/request", config.Config.RelayServerEndpoint), common.HttpWithHeaders(headers), common.HttpWithJsonBody(relayRequest), common.HttpWithTimeout(timeout))

	if err != nil {
		slog.Error("unable to access relay server", "error", err)
		return data, fmt.Errorf("unable to access relay server %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("relay: failed to close response body", "error", err)
		}
	}()
	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return data, err
	}

	if resp.StatusCode != 200 {
		slog.Error("failed to fetch data from relay", "status", resp.StatusCode, "data", string(jsonBody))
		return data, fmt.Errorf("relay error: %s", extractRelayErrorMessage(jsonBody, resp.Status))
	}

	responseData := map[string]any{}
	err = common.UnmarshalJson(jsonBody, &responseData)
	if err != nil {
		return data, err
	}

	if data, ok := responseData["data"].(map[string]any); ok {
		if errorMsg, ok := data["error_msg"].(string); ok {
			return responseData, common.ErrorBadRequest(errorMsg)
		}
	}
	if resp.StatusCode != 200 {
		slog.Error("failed to fetch data from relay", "status", resp)
		return nil, err
	}

	return responseData, err
}

// extractRelayErrorMessage parses the relay's JSON error body to get the human-readable
// message. The relay uses two formats:
//
//	{"errors": [{"code": 400, "message": "agent not connected"}]}
//	{"error": "some message"}
//
// Falls back to the HTTP status string if the body can't be parsed.
func extractRelayErrorMessage(body []byte, fallback string) string {
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		if len(envelope.Errors) > 0 && envelope.Errors[0].Message != "" {
			return envelope.Errors[0].Message
		}
		if envelope.Error != "" {
			return envelope.Error
		}
	}
	return fallback
}
