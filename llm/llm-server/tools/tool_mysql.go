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

const ToolExecuteMySQLQuery = "mysql_query_execute" // Change the constant name

func init() {
	core.RegisterNBToolFactory(ToolExecuteMySQLQuery, func(accountId string) (core.NBTool, error) {
		return MySQLExecuteTool{}, nil // Register the MySQL tool
	})
}

type MySQLExecuteTool struct {
}

func (m MySQLExecuteTool) Name() string {
	return ToolExecuteMySQLQuery // Use the new constant
}

func (m MySQLExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m MySQLExecuteTool) Description() string {
	return `Executes read-only 'mysql' queries against the user's Database. This tool allows you to gather information about the mysql tables, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about mysql database to make decisions or provide accurate responses, use this tool. 
		* **Input:** Provide a valid, read-only 'mysql query' as input. Do not include any other information.
		* **Output:** The tool will return the output of the executed query.
		* **Security:** This tool is strictly limited to read-only operations. It cannot modify any resources within the database. 

		**Important Notes:**

		* Ensure the 'mysql query' command is correctly formatted.
		* Never attempt to use this tool for any operations that modify database.
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m MySQLExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "mysql query to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m MySQLExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("mysql: executing mysql query call", "query", input.Command)

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

	err := sqlValidateReadOnly(query, "")
	if err != nil {
		return core.NBToolResponse{}, err
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		// For workspace mode, we want raw terminal output
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, query, map[string]string{
			"MYSQL_DATABASE":                  database,
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("mysql: unable to execute shell script", "error", err.Error(), "command", query)
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

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobMysql, query, nbRequestContext.AccountId, map[string]any{
		"database": database,
	}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("mysql: unable to execute mysql query", "error", err.Error())
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

func (m MySQLExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "mysql",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "Mysql Secret in k8s, Required Keys, MYSQL_DATABASE, MYSQL_HOST, MYSQL_USER, MYSQL_PWD",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "MySQL Host",
			},
		},
	}
}
