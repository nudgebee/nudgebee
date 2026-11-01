package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"

	"github.com/google/shlex"
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

// ShellCommandPrefixes returns the shell command prefixes that map to this
// tool. Implements core.ShellWrappable so shell_execute's classifier can
// delegate confirmation-gate decisions to this tool when the LLM invokes
// the CLI via `shell_execute("mysql ...")` instead of `mysql_execute`.
func (m MySQLExecuteTool) ShellCommandPrefixes() []string { return []string{"mysql"} }

func (m MySQLExecuteTool) Description() string {
	return `Executes read-only 'mysql' queries against the user's Database. This tool allows you to gather information about the mysql tables, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about mysql database to make decisions or provide accurate responses, use this tool. 
		* **Input:** Provide a valid, read-only MySQL query as a plain string, OR a JSON object {query, database?} to target a specific database.
		* **Output:** The tool will return the output of the executed query.
		* **Security:** This tool is strictly limited to read-only operations. It cannot modify any resources within the database. 

		**Important Notes:**

		* Ensure the 'mysql query' command is correctly formatted.
		* Never attempt to use this tool for any operations that modify database.
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m MySQLExecuteTool) InputSchema() core.ToolSchema {
	// 'command'/'query' are aliases (Call() prefers 'command'); RequiredOneOf
	// enforces at least one without rejecting a 'query'-keyed input.
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "MySQL query to execute. Either 'command' or 'query' must be provided.",
			},
			"query": {
				Type:        core.ToolSchemaTypeString,
				Description: "Alias for 'command' — accepted at the top level for backward compatibility.",
			},
			"database": {
				Type:        core.ToolSchemaTypeString,
				Description: "Target MySQL database to run the query against.",
			},
			"instance": instanceProperty,
		},
		RequiredOneOf: [][]string{{"command", "query"}},
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

	wm := workspace.NewWorkspaceManager()
	// The input is SQL. It used to be run as the shell command itself, so
	// "SHOW PROCESSLIST" failed with "sh: SHOW: not found" and any shell text
	// in it ran in the workspace pod. Run it through the mysql shim, quoted.
	query, err = mysqlWorkspaceShellCommand(query)
	if err != nil {
		return core.NBToolResponse{}, err
	}
	response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, query, IntegrationWorkspaceEnv(nbRequestContext, map[string]string{
		"MYSQL_DATABASE": database,
	}))
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("mysql: unable to execute shell script", "error", err.Error(), "command", query)
		if response == "" {
			response = err.Error()
		}
		// mysql CLI uses --help for flags but its interactive help is \h;
		// the model rarely runs the CLI directly, so lean on the SQL side.
		return core.NBToolResponse{
			Data:   cliRecoveryEnvelope(response, "", "mysql", `mysql --help (or \h in interactive mode for SQL syntax)`),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
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

// mysqlWorkspaceShellCommand builds the mysql command the workspace pod's shell
// runs. The input is normally SQL, passed single-quoted to `mysql -e`. A full
// `mysql ...` / `mariadb ...` command is accepted too: its words are quoted
// one by one, so shell text in them stays an argument instead of running in
// the workspace pod.
func mysqlWorkspaceShellCommand(input string) (string, error) {
	input = strings.TrimSpace(input)
	if first := strings.Fields(input); len(first) > 0 && (first[0] == "mysql" || first[0] == "mariadb") {
		words, err := shlex.Split(input)
		if err != nil {
			return "", fmt.Errorf("mysql: unable to parse the command: %w", err)
		}
		// Always `mysql`: the workspace image links only mysql to the shim
		// (llm/code-analysis/Dockerfile), and the proxy routes only that name
		// to the MySQL job, so `mariadb` would run without the integration.
		quoted := []string{"mysql"}
		for _, word := range words[1:] {
			quoted = append(quoted, common.ShellEscape(word))
		}
		return strings.Join(quoted, " "), nil
	}
	return "mysql -e " + common.ShellEscape(input), nil
}

// InferToolRequestType classifies the query with the static SQL rules; ""
// leaves an unclear query to InferToolRequestTypePrompt.
func (m MySQLExecuteTool) InferToolRequestType(_ *security.RequestContext, _, input string) (core.ToolRequestType, error) {
	return inferSQLRequestType(input), nil
}

// InferToolRequestTypePrompt classifies queries the static SQL rules cannot.
func (m MySQLExecuteTool) InferToolRequestTypePrompt(_ *security.RequestContext, _, _ string) (string, error) {
	return sqlRequestTypePrompt("MySQL"), nil
}
