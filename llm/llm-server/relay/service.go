package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"strings"
	"time"
)

// errMsgAgentNotConnected matches the message api-server's relay client and the
// relay server itself use for this condition, so downstream string matching on
// either side keeps working regardless of which client short-circuited.
const errMsgAgentNotConnected = "agent not connected"

func Execute(ctx context.Context, body ActionExecuteBody) (map[string]any, error) {

	relayRequest := RelayExecuteRequest{
		Body:    body,
		NoSinks: true,
		Cache:   false,
	}
	relayRequest.Body.Origin = "llm-server"

	data := make(map[string]any)

	// Skip the round-trip when the account's k8s agent is not registered: the
	// relay would answer 503 "agent not connected" anyway. Gating here rather
	// than at the event or account level is deliberate — the account is not
	// disabled, only its agent is unreachable, so everything that does NOT
	// route through the agent (user-configured metric/log integrations, event
	// storage, the investigation itself) must keep working.
	//
	// Only the k8s agent is gated. An empty AgentType means k8s (the relay
	// defaults the missing X-NB-Agent-Type header); other agent types are
	// separate registrations this lookup says nothing about.
	//
	// NoAgent actions (direct-mode MCP) are never gated: the relay serves them
	// itself before it checks for any agent.
	//
	// EqualFold because AgentType is not a constant here — callers read it from
	// tool config (`getConfigValue(..., "agent_type")` in tools/common_relay.go
	// and tool_mongodb.go), so a config written "K8s" must still gate. Note the
	// value is deliberately NOT trimmed: line ~60 sends it as the
	// X-NB-Agent-Type header verbatim, and trimming only here would let the
	// guard classify a padded value as k8s while the relay routed it as a
	// different agent type.
	if !body.NoAgent && (body.AgentType == "" || strings.EqualFold(body.AgentType, k8sAgentType)) {
		if !AgentConnected(body.AccountID) {
			slog.Debug("relay: skipping request, k8s agent not connected",
				"account_id", body.AccountID, "action", body.ActionName)
			return data, errors.New(errMsgAgentNotConnected)
		}
	}

	timeout := time.Duration(config.Config.LlmServerRelayCommandExecutionTimeoutSeconds) * time.Second
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

	resp, err := common.HttpPost(fmt.Sprintf("%s/request", config.Config.RelayServerEndpoint), common.HttpWithContext(ctx), common.HttpWithHeaders(headers), common.HttpWithJsonBody(relayRequest), common.HttpWithTimeout(timeout))

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
