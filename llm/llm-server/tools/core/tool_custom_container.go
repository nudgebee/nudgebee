package core

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/relay"
	"strings"

	"github.com/google/uuid"
)

// nbCustomContainerTool represents a custom tool executed within a container.
type nbCustomContainerTool struct {
	tool ToolDto
}

func (c nbCustomContainerTool) Name() string {
	return c.tool.Name
}

func (c nbCustomContainerTool) Description() string {
	return c.tool.Description
}

func (c nbCustomContainerTool) InputSchema() ToolSchema {
	if c.tool.InputSchema.Type == "" {
		return ToolSchema{
			Type: ToolSchemaTypeObject,
			Properties: map[string]ToolSchemaProperty{
				"command": {
					Type:        ToolSchemaTypeArray,
					Description: "OPTIONAL: The command to run in the container.",
					Items:       map[string]any{"type": "string"},
				},
			},
			Required: []string{},
		}
	}
	return c.tool.InputSchema
}

func (c nbCustomContainerTool) GetExecutorType() ToolExecutorType {
	return ToolExecutorTypeContainer
}

func (c nbCustomContainerTool) Call(nbRequestContext NbToolContext, input NBToolCallRequest) (NBToolResponse, error) {
	log := nbRequestContext.Ctx.GetLogger().With("toolName", c.Name(), "toolId", c.tool.Id, "accountId", nbRequestContext.AccountId, "toolCallId", nbRequestContext.ToolCallId)
	log.Info("customcontainer: executing custom container tool", "input", input)

	// 1. Determine Image
	containerImage, ok := c.tool.Config["image"].(string)
	if !ok || containerImage == "" {
		log.Error("customcontainer: Container tool configured without a valid 'image' in tool.Config")
		return NBToolResponse{Status: NBToolResponseStatusError, Data: "Tool configuration error: base image not found."}, errors.New("tool configuration error: base image not found")
	}

	if overrideImage, present := input.Arguments["image"].(string); present && overrideImage != "" {
		containerImage = overrideImage
		log.Info("customcontainer: Overriding container image with input argument", "newImage", containerImage)
	}

	// 2. Determine Command
	var commandToRunParts []string
	if input.Command != "" {
		commandToRunParts = strings.Split(input.Command, " ")
		log.Info("customcontainer: using 'command' from input arguments", "command", commandToRunParts)
	} else if cmdOverridePayload, present := input.Arguments["command"]; present {
		if cmdOverride, ok := cmdOverridePayload.([]any); ok {
			for _, part := range cmdOverride {
				if partStr, isStr := part.(string); isStr {
					commandToRunParts = append(commandToRunParts, partStr)
				} else {
					log.Warn("customcontainer: invalid type in command array", "part", part)
				}
			}
		} else if cmdOverride, ok := cmdOverridePayload.([]string); ok {
			commandToRunParts = cmdOverride
		} else {
			log.Warn("customcontainer: command was present but not an array of strings", "value", cmdOverridePayload)
		}
		log.Info("customcontainer: using 'command' from input arguments", "command", commandToRunParts)
	} else if defaultConfigCmdPayload, ok := c.tool.Config["command"].([]any); ok {
		for _, part := range defaultConfigCmdPayload {
			if partStr, isStr := part.(string); isStr {
				commandToRunParts = append(commandToRunParts, partStr)
			}
		}
		log.Info("customcontainer: using 'command' from tool.Config as default", "command", commandToRunParts)
	} else if defaultConfigCmdPayload, ok := c.tool.Config["command"].([]string); ok {
		commandToRunParts = defaultConfigCmdPayload
		log.Info("customcontainer: using 'command' from tool.Config as default", "command", commandToRunParts)
	}
	commandString := strings.Join(commandToRunParts, " ")

	// 3. Determine Environment Variables
	envVars := make(map[string]string)
	if defaultEnvsPayload, ok := c.tool.Config["env_vars"].(map[string]any); ok {
		for k, v := range defaultEnvsPayload {
			if vStr, isStr := v.(string); isStr {
				envVars[k] = vStr
			}
		}
	}
	if envOverridePayload, present := input.Arguments["env_vars"].(map[string]any); present {
		for key, value := range envOverridePayload {
			if valueStr, isStr := value.(string); isStr {
				envVars[key] = valueStr
				log.Info("customcontainer: applying env_vars", "key", key)
			} else {
				log.Warn("customcontainer: invalid type for env_vars value", "key", key, "value", value)
			}
		}
	}

	// 4. Prepare actionParams for pod_script_run_enricher
	actionParams := map[string]any{
		"image":    containerImage,
		"command":  commandString,
		"pod_name": "nb-llm-ct-" + uuid.NewString(),
	}
	if len(envVars) > 0 {
		actionParams["env_variables"] = envVars
	}

	// 5. Create relay.ActionExecuteBody
	relayActionBody := relay.ActionExecuteBody{
		AccountID:    nbRequestContext.AccountId,
		ActionName:   "pod_script_run_enricher",
		ActionParams: actionParams,
	}

	log.Info("customcontainer: sending request to relay for container execution", "relayRequest", relayActionBody)
	relayResponse, err := relay.Execute(relayActionBody)
	if err != nil {
		log.Error("customcontainer: relay execution failed for container tool", "error", err)
		return NBToolResponse{Status: NBToolResponseStatusError, Data: "Failed to execute container: " + err.Error()}, err
	}
	log.Debug("customcontainer: received response from relay", "relayResponse", relayResponse)

	parsedResponse, err := parsePodScriptRunEnricherResponse(log, relayResponse)
	if err != nil {
		log.Error("customcontainer: failed to parse relay response for container tool", "error", err, "relayResponse", relayResponse)
		rawRelayResponseBytes, _ := common.MarshalJson(relayResponse)
		return NBToolResponse{Status: NBToolResponseStatusError, Data: "Failed to parse container output: " + string(rawRelayResponseBytes)}, err
	}

	response := ""
	if parsedResponse["response"] != nil {
		if stdout, ok := parsedResponse["response"].(string); ok {
			response = stdout
		}
	}

	return NBToolResponse{Data: response, Type: NBToolResponseTypeText, Status: NBToolResponseStatusSuccess}, nil
}

func (c nbCustomContainerTool) GetType() NBToolType {
	if c.tool.NBToolType != "" {
		return c.tool.NBToolType
	}
	return NBToolTypeTool
}

// customToolContainerValidateConfigAndReturnSchema validates the tool's configuration
// for container executor type and returns its schema.
func customToolContainerValidateConfigAndReturnSchema(config map[string]any, accountId string) (ToolSchema, error) {
	if config["image"] == nil || config["image"] == "" {
		return ToolSchema{}, errors.New("tools: config 'image' (default image) is required for container tool")
	}
	slog.Debug("customcontainer: validating container tool config", "config", config)

	return ToolSchema{
		Type: ToolSchemaTypeObject,
		Properties: map[string]ToolSchemaProperty{
			"image": {
				Type:        ToolSchemaTypeString,
				Description: "OPTIONAL: Override the default container image.",
			},
			"command": {
				Type:        ToolSchemaTypeArray,
				Items:       map[string]any{"type": "string"},
				Description: "OPTIONAL: Override the default command for the container.",
			},
			"env_vars": {
				Type:        ToolSchemaTypeObject,
				Description: "OPTIONAL: Key-value pairs for environment variables. These will be merged with (and override) any default environment variables from the tool's configuration.",
			},
		},
	}, nil
}

func parsePodScriptRunEnricherResponse(log *slog.Logger, relayResponse map[string]any) (map[string]any, error) {
	data1, ok := relayResponse["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, errors.New("parser: 'data' field not found or is nil in relay response")
	}

	findings, ok := data1["findings"].([]any)
	if !ok || findings == nil {
		log.Debug("parser: 'findings' field not found or is nil in relay response data. This might be okay if output is elsewhere.")
		// Check if stdout/stderr might be directly in data1 for simpler cases
		if directResponse, rok := data1["response"].(string); rok {
			return map[string]any{"response": directResponse}, nil
		}
		return nil, errors.New("parser: 'findings' field not found or is nil, and no direct response field found")
	}

	if len(findings) == 0 {
		return map[string]any{}, nil // No findings, potentially means no structured output like stdout/stderr via this path
	}

	firstArrayMap, ok := findings[0].(map[string]any)
	if !ok {
		return nil, errors.New("parser: first item in 'findings' is not a map")
	}

	evidenceDataRaw := firstArrayMap["evidence"]
	if evidenceDataRaw == nil {
		return map[string]any{}, nil // No evidence
	}

	evidenceDataArray, ok := evidenceDataRaw.([]any)
	if !ok || len(evidenceDataArray) == 0 {
		return map[string]any{}, nil // Evidence is not an array or is empty
	}

	firstEvidence, ok := evidenceDataArray[0].(map[string]any)
	if !ok {
		return nil, errors.New("parser: first item in 'evidence' is not a map")
	}

	firstEvidenceDataRaw := firstEvidence["data"]
	if firstEvidenceDataRaw == nil {
		return map[string]any{}, nil // No raw data string in first evidence
	}
	firstEvidenceDataStr, ok := firstEvidenceDataRaw.(string)
	if !ok {
		return nil, fmt.Errorf("parser: 'data' in first evidence is not a string, got %T", firstEvidenceDataRaw)
	}

	// This string is expected to be a JSON array, wrapping the actual command output map.
	var commandResponseWrapperArray []map[string]any // Expecting [{"data": "{\"response\":\"stdout...\"}"}]
	if err := common.UnmarshalJson([]byte(firstEvidenceDataStr), &commandResponseWrapperArray); err != nil || len(commandResponseWrapperArray) == 0 {
		return nil, fmt.Errorf("parser: failed to unmarshal evidence data string '%s' into wrapper array: %w", firstEvidenceDataStr, err)
	}

	finalDataStr, ok := commandResponseWrapperArray[0]["data"].(string)
	if !ok {
		return nil, errors.New("parser: 'data' field in command response wrapper is not a string")
	}

	var commandOutput map[string]any // Expecting {"response": "stdout_content", "stderr": "stderr_content"}
	if err := common.UnmarshalJson([]byte(finalDataStr), &commandOutput); err != nil {
		// If this fails, the finalDataStr might be the raw output itself.
		log.Warn("parser: failed to unmarshal final data string into map, assuming it's raw output", "error", err, "data", finalDataStr)
		return map[string]any{"response": finalDataStr}, nil // Treat as raw stdout
	}
	return commandOutput, nil
}
