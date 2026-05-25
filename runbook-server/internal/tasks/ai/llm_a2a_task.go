package ai

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/runbook/internal/tasks/types"
	"time"

	"github.com/google/uuid"
)

// LLMA2ATask defines a task that interacts with an external AI Agent via JSON-RPC 2.0.
type LLMA2ATask struct{}

// GetName returns the unique name of the task.
func (t *LLMA2ATask) GetName() string {
	return "llm.a2a_call"
}

// GetDescription returns a brief description of the task.
func (t *LLMA2ATask) GetDescription() string {
	return "Call an external AI agent via JSON-RPC 2.0 and get its response. Use this to integrate with third-party AI agents or services that expose an agent-to-agent (A2A) compatible endpoint."
}

// GetDisplayName returns a human-readable name for the task.
func (t *LLMA2ATask) GetDisplayName() string {
	return "AI Agent Call"
}

// Execute runs the core logic of the task.
func (t *LLMA2ATask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	if logger := taskCtx.GetLogger(); logger != nil {
		logger.Debug("Executing LLMA2ATask", "params", params)
	}

	urlVal, ok := params["url"].(string)
	if !ok || urlVal == "" {
		return nil, errors.New("url is required")
	}

	methodVal, ok := params["method"].(string)
	if !ok || methodVal == "" {
		return nil, errors.New("method is required")
	}

	rpcParams := params["params"]

	// Construct JSON-RPC 2.0 Request
	requestID := uuid.New().String()
	rpcRequest := map[string]any{
		"jsonrpc": "2.0",
		"method":  methodVal,
		"id":      requestID,
	}
	if rpcParams != nil {
		rpcRequest["params"] = rpcParams
	}

	reqBody, err := json.Marshal(rpcRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON-RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(taskCtx.GetContext(), "POST", urlVal, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Default Headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Add Custom Headers
	if headers, ok := params["headers"].(map[string]any); ok {
		for k, v := range headers {
			if val, ok := v.(string); ok {
				req.Header.Set(k, val)
			}
		}
	} else if headers, ok := params["headers"].(map[string]string); ok {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	// Configure Client
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
		},
	}

	// Execute Request
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("external agent returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var rpcResponse map[string]any
	if err := json.Unmarshal(bodyBytes, &rpcResponse); err != nil {
		return nil, fmt.Errorf("failed to parse JSON-RPC response: %w", err)
	}

	// Validate JSON-RPC Response
	if errVal, hasError := rpcResponse["error"]; hasError && errVal != nil {
		errorMap, ok := errVal.(map[string]any)
		if ok {
			msg, _ := errorMap["message"].(string)
			code, _ := errorMap["code"].(float64)
			return nil, fmt.Errorf("json-rpc error (code: %v): %s", code, msg)
		}
		return nil, fmt.Errorf("json-rpc error: %v", errVal)
	}

	return map[string]any{
		"result":  rpcResponse["result"],
		"id":      rpcResponse["id"],
		"jsonrpc": rpcResponse["jsonrpc"],
	}, nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *LLMA2ATask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"url": {
				Type:        "string",
				Description: "The external agent endpoint URL.",
				Required:    true,
				Title:       "Agent URL",
				Order:       1,
			},
			"method": {
				Type:        "string",
				Description: "The JSON-RPC method name (e.g., 'agent.chat').",
				Required:    true,
				Title:       "Method",
				Order:       2,
			},
			"params": {
				Type:        "any",
				Description: "The parameters for the JSON-RPC call.",
				Required:    false,
				Title:       "Parameters",
				Order:       3,
				SubType:     "json",
			},
			"headers": {
				Type:        "map[string]string",
				Description: "Custom headers (e.g., Authorization).",
				Required:    false,
				Title:       "Headers",
				Order:       4,
				SubType:     "json",
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *LLMA2ATask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"result": {
				Type:        "any",
				Description: "The result of the JSON-RPC call.",
				Required:    true,
			},
			"id": {
				Type:        "string",
				Description: "The request ID echo.",
				Required:    true,
			},
			"jsonrpc": {
				Type:        "string",
				Description: "The JSON-RPC version.",
				Required:    true,
			},
		},
	}
}
