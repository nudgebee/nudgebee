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

const ToolExecuteOracleQuery = "oracle_query_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteOracleQuery, func(accountId string) (core.NBTool, error) {
		return OracleExecuteTool{}, nil
	})
}

type OracleExecuteTool struct {
}

func (m OracleExecuteTool) Name() string {
	return ToolExecuteOracleQuery
}

func (m OracleExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m OracleExecuteTool) Description() string {
	return `Executes read-only 'oracle' queries against the user's Oracle Database. This tool allows you to gather information about the oracle tables, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about oracle database to make decisions or provide accurate responses, use this tool.
		* **Input:** Provide a valid, read-only Oracle SQL query as input. Do not include any other information.
		* **Output:** The tool will return the output of the executed query.
		* **Security:** This tool is strictly limited to read-only operations. It cannot modify any resources within the database.
		* **Input Schema:** JSON object -
		* 'query' - query to execute, required
		* 'database' - service name / PDB to connect to, Optional
		* 'instance' - config/host/env/instance name of oracle database, Optional

		**Important Notes:**

		* Ensure the Oracle SQL query is correctly formatted. Use Oracle SQL syntax (not PostgreSQL or MySQL syntax).
		* Never attempt to use this tool for any operations that modify the database.
		* Use the output of this tool to inform your responses and suggestions to the user.
		* Oracle system views use ALL_*, USER_*, DBA_* prefixes (e.g. ALL_TABLES, V$SESSION).
		`
}

func (m OracleExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Oracle SQL SELECT query to execute. Do NOT use USE statements — use the 'database' parameter instead.",
			},
			"database": {
				Type:        core.ToolSchemaTypeString,
				Description: "Target Oracle service name or PDB to run the query against. Use this instead of USE statements.",
			},
			"instance": {
				Type:        core.ToolSchemaTypeString,
				Description: "Target Oracle instance/environment name (e.g. 'prod', 'dev') when multiple configs exist.",
			},
		},
		Required: []string{"command"},
	}
}

func (m OracleExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("oracle: executing oracle query call")

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	query := input.Command
	database := ""
	if input.Arguments != nil && input.Arguments["database"] != nil {
		database = input.Arguments["database"].(string)
	}

	// json
	if strings.HasPrefix(query, "{") {
		jsonCommand := map[string]any{}
		err := common.UnmarshalJson([]byte(query), &jsonCommand)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("oracle: unable to parse oracle query", "error", err)
			return core.NBToolResponse{}, fmt.Errorf("unable to parse oracle query")
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
			if database1str, ok := database1.(string); ok {
				database = database1str
			}
		}
	}

	query = strings.TrimSuffix(query, ";")

	err := sqlValidateReadOnly(query, "")
	if err != nil {
		return core.NBToolResponse{}, err
	}

	// Oracle-specific write guard: block PL/SQL blocks and Oracle-specific mutation statements
	// that sqlValidateReadOnly (generic) doesn't catch.
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	if !strings.HasPrefix(lowerQuery, "select ") && !strings.HasPrefix(lowerQuery, "with ") {
		return core.NBToolResponse{}, fmt.Errorf("oracle: only read-only SELECT/WITH queries are allowed")
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		// Wrap query in sqlplus command so the workspace shim can intercept it.
		// Uses -Q flag convention (mirroring sqlcmd -Q) because real sqlplus only
		// accepts SQL via stdin (heredoc), which the shim cannot capture from os.Args.
		// The shim captures this -Q flag in os.Args, and forager's sanitizeQuery
		// extracts the SQL from it.
		sqlplusFlags := ""
		if database != "" {
			escapedDb := strings.ReplaceAll(database, `"`, `\"`)
			sqlplusFlags = fmt.Sprintf(` -d "%s"`, escapedDb)
		}
		escapedQuery := strings.ReplaceAll(query, `"`, `\"`)
		wsQuery := fmt.Sprintf(`sqlplus%s -Q "%s"`, sqlplusFlags, escapedQuery)
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, wsQuery, map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})

		response = convertCsvToJsonString(nbRequestContext, response, rune(','))

		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("oracle: unable to execute oracle query in workspace", "error", err.Error())
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
			Type:   core.NBToolResponseTypeTable,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobOracle, query, nbRequestContext.AccountId, map[string]any{
		"database": database,
	}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("oracle: unable to execute oracle query", "error", err.Error())
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
		Data:   data,
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m OracleExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	instanceName := ""

	if input.Arguments != nil {
		if inst, ok := input.Arguments["instance"].(string); ok {
			instanceName = inst
		}
	}

	if instanceName == "" {
		command := struct {
			Instance string `json:"instance"`
		}{}
		_ = common.UnmarshalJson([]byte(input.Command), &command)
		instanceName = command.Instance
	}

	if instanceName != "" {
		for _, cfg := range availableConfigs {
			if strings.EqualFold(cfg.Name, instanceName) {
				return cfg, nil
			}

			var hostPatterns string
			for _, v := range cfg.Values {
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
					if !strings.HasPrefix(trimmedPattern, "(?i)") {
						trimmedPattern = "(?i)" + trimmedPattern
					}
					re, err := regexp.Compile(trimmedPattern)
					if err != nil {
						ctx.Ctx.GetLogger().Warn("oracle: invalid regex pattern in host config", "pattern", trimmedPattern, "error", err)
						continue
					}
					if re.MatchString(instanceName) {
						ctx.Ctx.GetLogger().Info("oracle: identified config via instance name matching", "config", cfg.Name, "instance", instanceName)
						return cfg, nil
					}
				}
			}
		}
	}

	userQuery := strings.ToLower(ctx.Query)
	if userQuery != "" {
		envKeywords := []string{
			"dev", "development",
			"test", "testing",
			"prod", "production",
			"stage", "staging",
			"qa", "uat",
			"demo", "preprod",
		}

		for _, cfg := range availableConfigs {
			if strings.Contains(userQuery, strings.ToLower(cfg.Name)) {
				return cfg, nil
			}
		}

		for _, keyword := range envKeywords {
			if !strings.Contains(userQuery, keyword) {
				continue
			}
			for _, cfg := range availableConfigs {
				if strings.Contains(strings.ToLower(cfg.Name), keyword) {
					return cfg, nil
				}
			}
		}
	}

	for _, cfg := range availableConfigs {
		if cfg.Tags != nil {
			if env, ok := cfg.Tags["environment"]; ok && userQuery != "" {
				if strings.Contains(userQuery, strings.ToLower(env)) {
					return cfg, nil
				}
			}
			if purpose, ok := cfg.Tags["purpose"]; ok && userQuery != "" {
				if strings.Contains(userQuery, strings.ToLower(purpose)) {
					return cfg, nil
				}
			}
		}
	}

	ctx.Ctx.GetLogger().Debug("oracle: could not identify config automatically", "query", userQuery, "instance", instanceName, "available_configs", len(availableConfigs))
	return core.ToolConfig{}, nil
}

func (m OracleExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "oracle",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "Oracle Secret in k8s, Required Keys: ORACLE_HOST, ORACLE_PORT, ORACLE_USER, ORACLE_PASSWORD, ORACLE_SERVICE",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "Oracle Host",
			},
		},
	}
}
