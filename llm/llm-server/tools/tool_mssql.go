package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
)

const ToolExecuteMSSQLQuery = "mssql_query_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteMSSQLQuery, func(accountId string) (core.NBTool, error) {
		return MSSQLExecuteTool{}, nil
	})
}

type MSSQLExecuteTool struct {
}

func (m MSSQLExecuteTool) Name() string {
	return ToolExecuteMSSQLQuery
}

func (m MSSQLExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m MSSQLExecuteTool) Description() string {
	return `Executes read-only 'mssql' queries against the user's Microsoft SQL Server Database. This tool allows you to gather information about the mssql tables, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about mssql database to make decisions or provide accurate responses, use this tool.
		* **Input:** Provide a valid, read-only T-SQL query as input. Do not include any other information.
		* **Output:** The tool will return the output of the executed query.
		* **Security:** This tool is strictly limited to read-only operations. It cannot modify any resources within the database.

		**Important Notes:**

		* Ensure the T-SQL query is correctly formatted.
		* Never attempt to use this tool for any operations that modify database.
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m MSSQLExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "T-SQL query to execute. Do NOT use USE statements to switch databases — use the 'database' parameter instead.",
			},
			"database": {
				Type:        core.ToolSchemaTypeString,
				Description: "Target database name to run the query against. Use this instead of USE statements.",
			},
		},
		Required: []string{"command"},
	}
}

func (m MSSQLExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("mssql: executing mssql query call", "query", input.Command)

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
			nbRequestContext.Ctx.GetLogger().Error("mssql: unable to parse mssql query", "error", err.Error(), "query", query)
			return core.NBToolResponse{}, fmt.Errorf("unable to parse mssql query")
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

	err := sqlValidateReadOnly(query, "")
	if err != nil {
		return core.NBToolResponse{}, err
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		// Wrap query in sqlcmd command so the workspace shim can intercept it.
		// Same pattern as Postgres (psql -c "query") and other DB tools.
		sqlcmdFlags := ""
		if database != "" {
			escapedDb := strings.ReplaceAll(database, `"`, `\"`)
			sqlcmdFlags = fmt.Sprintf(` -d "%s"`, escapedDb)
		}
		escapedQuery := strings.ReplaceAll(query, `"`, `\"`)
		wsQuery := fmt.Sprintf(`sqlcmd%s -Q "%s" -s "	" -W`, sqlcmdFlags, escapedQuery)
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, wsQuery, map[string]string{
			"MSSQL_DATABASE":                  database,
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("mssql: unable to execute mssql query in workspace", "error", err.Error(), "tool_config", nbRequestContext.ToolConfig.Name)
			if response == "" {
				response = err.Error()
			}
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}

		response = convertCsvToJsonString(nbRequestContext, response, rune('\t'))

		return core.NBToolResponse{
			Data:   response,
			Type:   core.NBToolResponseTypeTable,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobMssql, query, nbRequestContext.AccountId, map[string]any{
		"database": database,
	}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("mssql: unable to execute mssql query", "error", err.Error())
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

	data, ok := response.(string)
	if !ok {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("%v", response),
			Type:   core.NBToolResponseTypeTable,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}
	return core.NBToolResponse{
		Data:   data,
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m MSSQLExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "mssql",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "MSSQL Secret in k8s, Required Keys: MSSQL_HOST, MSSQL_PORT, MSSQL_USER, MSSQL_PASSWORD, MSSQL_DATABASE",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "MSSQL Host",
			},
		},
	}
}
