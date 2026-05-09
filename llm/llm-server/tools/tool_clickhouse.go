package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"strings"
)

const ToolExecuteClickhouseQuery = "clickhouse_query_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteClickhouseQuery, func(accountId string) (core.NBTool, error) {
		return ClickhouseExecuteTool{}, nil
	})
}

type ClickhouseExecuteTool struct {
}

func (m ClickhouseExecuteTool) Name() string {
	return ToolExecuteClickhouseQuery
}

func (m ClickhouseExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ClickhouseExecuteTool) Description() string {
	return `Executes read-only 'ClickHouse' queries against the user's Database. This tool allows you to gather information about the ClickHouse tables and system views, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about a ClickHouse database to make decisions or provide accurate responses, use this tool.
		* **Input:** Provide a valid, read-only 'ClickHouse SQL query' (SELECT or SHOW) as input. Do not include any other information.
		* **Output:** The tool will return the output of the executed query, typically in CSV format, which is then converted to JSON.
		* **Security:** This tool is strictly limited to read-only operations (SELECT, SHOW, DESCRIBE). It cannot modify any data or schema within the database.

		**Important Notes:**

		* Ensure the 'ClickHouse SQL query' command is correctly formatted.
		* Never attempt to use this tool for any operations that modify the database (INSERT, ALTER, CREATE, etc.).
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m ClickhouseExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON string with 'command' (or 'query') and 'instance' fields. 'instance' is the host to connect to.",
			},
		},
		Required: []string{"command"},
	}
}

func (m ClickhouseExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("clickhouse: executing clickhouse query call", "query", input.Command)

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	query := input.Command
	database := ""
	if input.Arguments != nil && input.Arguments["database"] != nil {
		database = input.Arguments["database"].(string)
	}

	//json
	if strings.HasPrefix(query, "{") {
		jsonCommand := map[string]any{}
		err := common.UnmarshalJson([]byte(query), &jsonCommand)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("postgres: unable to parse postgres query", "error", err.Error, "query", query)
			return core.NBToolResponse{}, fmt.Errorf("unable to parse postgres query")
		}
		if command, ok := jsonCommand["command"]; ok {
			if command1, ok := command.(string); ok {
				query = command1
			}
		} else if command, ok := jsonCommand["query"]; ok {
			if command1, ok := command.(string); ok {
				query = command1
			}
		}
		if database1, ok := jsonCommand["database"]; ok {
			if database1, ok := database1.(string); ok {
				database = database1
			}
		}

	}

	// Validate that the query is read-only
	err := sqlValidateReadOnly(query, "")
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("clickhouse: validation failed for query", "query", query, "error", err)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error: Query is not read-only. %v", err),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		// For workspace mode, we want raw terminal output
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, query, map[string]string{
			"CLICKHOUSE_DATABASE":             database,
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("clickhouse: unable to execute shell script", "error", err.Error(), "command", query)
			if response == "" {
				response = err.Error()
			}
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}

		return core.NBToolResponse{
			Data:   response,
			Type:   core.NBToolResponseTypeText,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	// apiModuleClickhouse will be added in a later step to tools/common.go
	response, err := ExecuteContainerJob(nbRequestContext, RelayJobClickhouse, query, nbRequestContext.AccountId, map[string]any{
		"database": database,
	}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("clickhouse: unable to execute clickhouse query", "error", err.Error())
		responseData := ""
		if response != nil {
			if responseDataStr, ok := response.(string); ok {
				responseData = responseDataStr
			}
		}
		return core.NBToolResponse{
			Data:   responseData,
			Status: core.NBToolResponseStatusError,
		}, err
	}

	data, ok := response.(string)
	if !ok {
		nbRequestContext.Ctx.GetLogger().Error("clickhouse: unexpected response type from ExecuteApiCall", "response", response)
		return core.NBToolResponse{
			Data:   "Error: Unexpected response format from query execution.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("unexpected response type from ExecuteApiCall: %T", response)
	}

	return core.NBToolResponse{
		Data:   data,
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m ClickhouseExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	instanceName := ""

	// Try to get instance from Arguments
	if input.Arguments != nil {
		if inst, ok := input.Arguments["instance"].(string); ok {
			instanceName = inst
		}
	}

	// Fallback to parsing Command as JSON
	if instanceName == "" {
		command := struct {
			Instance string `json:"instance"`
		}{}
		_ = common.UnmarshalJson([]byte(input.Command), &command)
		instanceName = command.Instance
	}

	if instanceName == "" {
		return core.ToolConfig{}, nil
	}

	for _, config := range availableConfigs {
		// 1. Try matching by config name
		if strings.EqualFold(config.Name, instanceName) {
			return config, nil
		}

		// 2. Try matching by host patterns
		var hostPatterns string
		for _, v := range config.Values {
			if v.Name == "host" {
				hostPatterns = v.Value
				break
			}
		}

		if hostPatterns != "" {
			patterns := strings.Split(hostPatterns, ",")
			for _, pattern := range patterns {
				trimmedPattern := strings.TrimSpace(pattern)
				if trimmedPattern == "" {
					continue
				}
				// Ensure case-insensitive matching if not already present
				if !strings.HasPrefix(trimmedPattern, "(?i)") {
					trimmedPattern = "(?i)" + trimmedPattern
				}
				re, err := regexp.Compile(trimmedPattern)
				if err != nil {
					ctx.Ctx.GetLogger().Warn("clickhouse: invalid regex pattern in host config", "pattern", trimmedPattern, "error", err)
					continue
				}

				if re.MatchString(instanceName) {
					return config, nil
				}
			}
		}
	}

	return core.ToolConfig{}, nil
}

func (m ClickhouseExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "clickhouse",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "ClickHouse Secret in k8s. Required keys: CLICKHOUSE_USER (or user), CLICKHOUSE_PASSWORD (or password). Example: my-namespace/clickhouse-secret",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "ClickHouse Host",
			},
			"port": {
				Type:        core.ToolSchemaTypeString,
				Description: "ClickHouse native protocol port",
				Default:     "8123",
			},
			"secret_user_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name in the k8s secret for the ClickHouse username (default: 'CLICKHOUSE_USER'). Optional.",
				Default:     "CLICKHOUSE_USER",
			},
			"secret_password_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name in the k8s secret for the ClickHouse password (default: 'CLICKHOUSE_PASSWORD'). Optional.",
				Default:     "CLICKHOUSE_PASSWORD",
			},
			"secure_connection": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Whether to use a secure (TLS) connection to ClickHouse (default: false). Optional.",
			},
		},
	}
}
