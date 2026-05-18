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

const ToolExecutePostgresQuery = "postgres_query_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecutePostgresQuery, func(accountId string) (core.NBTool, error) {
		return PostgresExecuteTool{}, nil
	})
}

type PostgresExecuteTool struct {
}

func (m PostgresExecuteTool) Name() string {
	return ToolExecutePostgresQuery
}

func (m PostgresExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m PostgresExecuteTool) Description() string {
	return `Executes read-only 'postgres' queries against the user's Database. This tool allows you to gather information about the postgres tables, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about postgres database to make decisions or provide accurate responses, use this tool. 
		* **Input:** Provide a valid, read-only 'postgresql query' as input. Do not include any other information.
		* **Output:** The tool will return the output of the executed query.
		* **Security:** This tool is strictly limited to read-only operations. It cannot modify any resources within the database. 
		* **Input Schema:** JSON object -
		* 'query' - query to execute, required
		* 'database' - database to connect to, Optional
		* 'instance' - config/host/env/instance name of postgres database, Optional

		**Important Notes:**

		* Ensure the 'postgres query' command is correctly formatted.
		* Never attempt to use this tool for any operations that modify database.
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m PostgresExecuteTool) InputSchema() core.ToolSchema {
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

func (m PostgresExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("postgres: executing postgres query call", "query", input.Command)

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

	query = strings.TrimSuffix(query, ";")

	// Pre-process query: replace placeholders if it's an EXPLAIN query
	if strings.Contains(strings.ToLower(query), "explain ") && strings.Contains(query, "$") {
		nbRequestContext.Ctx.GetLogger().Info("postgres: substituting placeholders in EXPLAIN query")
		query = common.SubstituteSqlMacros(query)
	}

	err := sqlValidateReadOnly(query, "")
	if err != nil {
		return core.NBToolResponse{}, err
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		// For workspace mode, we want raw terminal output
		pgFlags := ""
		if database != "" {
			pgFlags = "--dbname " + common.ShellEscape(database)
		}

		explainQuery := false
		if (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain ")) || (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain analyze")) {
			query = fmt.Sprintf(`psql %s -c %s`, pgFlags, common.ShellEscape(query))
			explainQuery = true
		} else {
			query = strings.TrimSpace(query)
			query = strings.TrimSuffix(query, ";")
			copyCmd := fmt.Sprintf(`\copy (%s) TO stdout WITH CSV HEADER`, query)
			query = fmt.Sprintf(`psql %s -c %s`, pgFlags, common.ShellEscape(copyCmd))
		}

		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, query, map[string]string{
			"PGDATABASE":                      database,
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})

		if explainQuery {
			response = fmt.Sprintf(`[{"plan": %s}]`, response)
		} else {
			response = convertCsvToJsonString(nbRequestContext, response, rune(','))
		}

		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("postgres: unable to execute shell script", "error", err.Error(), "command", query)
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

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobPostgres, query, nbRequestContext.AccountId, map[string]any{
		"database": database,
	}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("postgres: unable to execute postgres query", "error", err.Error())
		responseData := ""
		if response != nil {
			if responseData1, ok := response.(string); ok {
				responseData = responseData1
			}
		}
		return core.NBToolResponse{
			Data:   responseData,
			Status: core.NBToolResponseStatusError,
		}, err
	}

	data := response.(string)
	return core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m PostgresExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
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

	// Strategy 1: Try to match the instance name from tool input
	if instanceName != "" {
		for _, config := range availableConfigs {
			// 1. Try matching by config name (highest confidence)
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
						ctx.Ctx.GetLogger().Warn("postgres: invalid regex pattern in host config", "pattern", trimmedPattern, "error", err)
						continue
					}

					if re.MatchString(instanceName) {
						ctx.Ctx.GetLogger().Info("postgres: identified config via instance name matching", "config", config.Name, "instance", instanceName)
						return config, nil
					}
				}
			}
		}
	}

	// Strategy 2: Extract config hints from user's original query
	userQuery := strings.ToLower(ctx.Query)
	if userQuery != "" {
		ctx.Ctx.GetLogger().Debug("postgres: attempting to extract config from user query", "query", userQuery)

		// Common environment keywords - comprehensive but simple list
		envKeywords := []string{
			"dev", "development",
			"test", "testing",
			"prod", "production",
			"stage", "staging",
			"qa", "uat",
			"demo", "preprod",
		}

		// Pass 1: Try exact config name match first (highest confidence)
		for _, config := range availableConfigs {
			configNameLower := strings.ToLower(config.Name)
			if strings.Contains(userQuery, configNameLower) {
				ctx.Ctx.GetLogger().Info("postgres: identified config by exact name in query",
					"config", config.Name,
					"query", userQuery)
				return config, nil
			}
		}

		// Pass 2: Try keyword matching (medium confidence)
		for _, keyword := range envKeywords {
			if !strings.Contains(userQuery, keyword) {
				continue // keyword not in query, skip
			}

			// Found keyword in query, now check which config matches
			for _, config := range availableConfigs {
				configNameLower := strings.ToLower(config.Name)
				if strings.Contains(configNameLower, keyword) {
					ctx.Ctx.GetLogger().Info("postgres: identified config from query keyword",
						"config", config.Name,
						"keyword", keyword,
						"query", userQuery)
					return config, nil
				}
			}
		}
	}

	// Strategy 3: Check config tags for hints
	for _, config := range availableConfigs {
		if config.Tags != nil {
			// If query mentions environment, match against config tags
			if env, ok := config.Tags["environment"]; ok && userQuery != "" {
				if strings.Contains(userQuery, strings.ToLower(env)) {
					ctx.Ctx.GetLogger().Info("postgres: identified config from environment tag", "config", config.Name, "env", env)
					return config, nil
				}
			}
			// Check purpose tag
			if purpose, ok := config.Tags["purpose"]; ok && userQuery != "" {
				if strings.Contains(userQuery, strings.ToLower(purpose)) {
					ctx.Ctx.GetLogger().Info("postgres: identified config from purpose tag", "config", config.Name, "purpose", purpose)
					return config, nil
				}
			}
		}
	}

	ctx.Ctx.GetLogger().Debug("postgres: could not identify config automatically", "query", userQuery, "instance", instanceName, "available_configs", len(availableConfigs))
	return core.ToolConfig{}, nil
}

func (m PostgresExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "postgresql",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "PostgreSql Secret in k8s, Required Keys, PGDATABASE, PGHOST, PGUSER, PGPASSWORD",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "PostgreSql Host",
			},
		},
	}
}
