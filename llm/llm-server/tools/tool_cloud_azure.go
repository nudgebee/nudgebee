package tools

import (
	"fmt"
	"nudgebee/llm/cloud"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"

	"github.com/google/shlex"
)

const ToolExecuteAzureCliCommand = "azure_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteAzureCliCommand, func(accountId string) (core.NBTool, error) {
		return AzureCliTool{}, nil
	})
}

type AzureCliTool struct{}

func (t AzureCliTool) Name() string {
	return ToolExecuteAzureCliCommand
}

func (t AzureCliTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t AzureCliTool) Description() string {
	return `Executes 'az' CLI commands.  This tool allows gathering information from various Azure services.

		**Usage:**

		* **Prioritize this tool:**  When interacting with Azure, use this tool to retrieve information or perform actions.
		* **Input:**  A valid 'az' CLI command string.  Include necessary options and arguments.
		* **Output:**  The raw output of the executed 'az' CLI command.

		**Examples:**

		* az group list
		* az vm list
		* az storage account list

		**Important Notes:**

		* Ensure correct command formatting and arguments.
		* Do not include Azure credentials in commands.  Assume they are configured correctly in the environment.
		* For complex queries, use tools like 'jq' to parse and filter the JSON output.  Indicate this in the command.
		`
}

func (t AzureCliTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "The 'az' CLI command to execute.",
			},
		},
		Required: []string{"command"},
	}
}

func (t AzureCliTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", t.Name())
	}

	command := strings.TrimSpace(input.Command)

	// Handle backslash line continuations
	command = strings.ReplaceAll(command, "\\\r\n", " ")
	command = strings.ReplaceAll(command, "\\\n", " ")

	if !strings.HasPrefix(command, "az") {
		command = "az " + command // Ensure "az" prefix
	}

	// Denylist auth and account state-changing commands
	args, err := shlex.Split(command)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("failed to parse command for denylist check: %w", err)
	}
	if len(args) > 1 {
		subCmd := args[1]
		if subCmd == "login" || subCmd == "logout" {
			return core.NBToolResponse{}, fmt.Errorf("login/logout commands not allowed")
		}
		// Allow safe account read commands
		if subCmd == "account" && len(args) > 2 {
			subSubCmd := args[2]
			restrictedSubSubCmds := map[string]bool{
				"set":    true,
				"clear":  true,
				"import": true,
			}
			if restrictedSubSubCmds[subSubCmd] {
				return core.NBToolResponse{}, fmt.Errorf("modifying account/subscription state is not allowed")
			}
		}
	}

	accountId := ""
	for _, v := range nbRequestContext.ToolConfig.Values {
		if v.Name == "id" {
			accountId = v.Value
			break
		}
	}

	if accountId == "" {
		return core.NBToolResponse{}, fmt.Errorf("unable to identify accountId - %s, please configure", t.Name())
	}

	if config.Config.LlmServerWorkspaceEnabled {
		creds, err := GetCloudAccountCredentials(accountId)
		if err != nil {
			return core.NBToolResponse{}, err
		}

		auth, err := BuildAzureAuth(creds)
		if err != nil {
			return core.NBToolResponse{}, err
		}

		// Auto-install costmanagement extension if needed
		if strings.Contains(command, "costmanagement") {
			auth.CommandPrefix += " && az extension add --name costmanagement > /dev/null "
		}

		auth.Env[workspace.ENV_NB_TOOL_CONFIG_NAME] = nbRequestContext.ToolConfig.Name
		fullCommand := WrapCommandWithAuth(command, auth)

		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, fullCommand, auth.Env)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("azure: unable to execute shell script", "error", err.Error(), "command", fullCommand)
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

	tenant := nbRequestContext.Ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return core.NBToolResponse{}, err
		}
		tenant = tenant1
	}

	response, err := cloud.Execute(cloud.CloudExecuteCliCommandRequest{
		AccountID: accountId,
		TenantID:  tenant,
		UserID:    nbRequestContext.Ctx.GetSecurityContext().GetUserId(),
		Command:   command,
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("azure-cli: command execution failed", "error", err.Error(), "command", command)
		return core.NBToolResponse{}, err
	}

	data := ""
	if dataRaw := response["data"]; dataRaw != nil {
		if dataStr, ok := dataRaw.(string); ok {
			data = dataStr
		}
	} else if errorsRaw := response["errors"]; errorsRaw != nil {
		if errorsArr, ok := errorsRaw.([]any); ok && len(errorsArr) > 0 {
			if errorMap, ok := errorsArr[0].(map[string]any); ok {
				if msg, ok := errorMap["message"].(string); ok {
					data = msg
				}
			}
		}
	}

	if data == "" {
		dataArr, err := common.MarshalJson(response)
		if err != nil {
			return core.NBToolResponse{}, err
		}
		data = string(dataArr)
	}

	return core.NBToolResponse{
		Data: data,
		Type: core.NBToolResponseTypeText,
	}, nil
}

// classifyAzureVerb checks a single verb against Azure verb lists.
// Returns the classified ToolRequestType, or empty string if no match.
func classifyAzureVerb(verb string) core.ToolRequestType {
	// Read verbs — these never modify state
	readPrefixes := []string{
		"list", "show", "get", "check", "display", "export",
		"browse", "download", "exists", "wait",
		"query", "search", "lookup", "view", "preview", "fetch",
	}
	for _, p := range readPrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeRead
		}
	}
	// Exact read verbs that don't follow prefix patterns
	readExact := map[string]bool{
		"help": true, "verify": true,
	}
	if readExact[verb] {
		return core.ToolRequestTypeRead
	}

	// Create verbs
	createPrefixes := []string{
		"create", "add", "import", "register", "upload",
		"invoke", "run-command", "launch",
		"put", "send", "publish", "execute", "request", "initiate",
	}
	for _, p := range createPrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeCreate
		}
	}

	// Update verbs — includes state-change operations that don't destroy resources
	updatePrefixes := []string{
		"update", "set", "modify", "assign", "unassign",
		"enable", "disable", "attach", "detach", "resize",
		"restart", "redeploy", "reimage", "swap", "move",
		"rotate", "regenerate", "renew",
		"tag", "untag", "associate", "disassociate",
		"replace", "restore", "apply", "configure", "change",
		"stop", "start", "deallocate", // state changes, not resource creation/deletion
	}
	for _, p := range updatePrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeUpdate
		}
	}

	// Delete verbs — destructive operations that remove resources
	deletePrefixes := []string{
		"delete", "remove", "purge", "revoke",
		"cancel", "terminate", "deregister",
		"release", "destroy",
	}
	for _, p := range deletePrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeDelete
		}
	}

	return "" // no match
}

// inferAzureVerbType classifies an Azure CLI command by its verb.
// Returns empty string if the verb is not recognized (caller should fall back to LLM).
func inferAzureVerbType(command string) core.ToolRequestType {
	parts := strings.Fields(strings.TrimSpace(command))

	if result := isCloudCLIInfoFlag(parts); result != "" {
		return result
	}

	if len(parts) < 2 {
		return ""
	}
	// Skip "az" prefix
	start := 0
	if strings.EqualFold(parts[0], "az") {
		start = 1
	}
	// Collect non-flag tokens (resource groups, verb, possible positional args)
	var candidates []string
	for i := start; i < len(parts); i++ {
		if strings.HasPrefix(parts[i], "-") {
			break
		}
		candidates = append(candidates, strings.ToLower(parts[i]))
	}
	if len(candidates) == 0 {
		return ""
	}

	// Try candidates in forward order, skipping the first token (resource group like "vm", "aks").
	// This finds the verb before any positional args that follow it, avoiding false positives
	// where a positional arg (e.g. VM name "delete-old-backup") matches a verb prefix.
	// e.g. "az vm stop myvm" → skips "vm", tries "stop" (match)
	// e.g. "az vm create remove-old-backup" → skips "vm", tries "create" (match)
	for i := 1; i < len(candidates); i++ {
		if result := classifyAzureVerb(candidates[i]); result != "" {
			return result
		}
	}
	// If only one candidate (resource group only, e.g. "az vm"), try it as a fallback
	if len(candidates) == 1 {
		return classifyAzureVerb(candidates[0])
	}

	return "" // unknown — caller decides
}

func (t AzureCliTool) InferToolRequestType(ctx *security.RequestContext, toolName, input string) (core.ToolRequestType, error) {
	requestType := inferAzureVerbType(extractCommandFromToolInput(input))
	if requestType != "" {
		return requestType, nil
	}
	// Unknown verb — return empty so caller falls through to LLM-based classification.
	ctx.GetLogger().Warn("azure: verb not recognized by heuristic, falling through to LLM classification", "input", input)
	return "", nil
}

func (t AzureCliTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are an Azure security expert. Your task is to classify an 'az' CLI command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Commands that only display help, usage, or CLI information (e.g., --help, -h, --version) must be classified as 'read'.

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: az monitor --help
	answer: read

	input: az vm list --help
	answer: read

	input: az group list
	answer: read

	input: az vm list
	answer: read

	input: az storage account list
	answer: read

	input: az ad user show --id johndoe@microsoft.com
	answer: read

	input: az vm run-command invoke --command-id RunPowerShellScript --name MyVM --resource-group MyRG --scripts "Get-Process"
	answer: read

	input: az vm run-command invoke --command-id RunPowerShellScript --name MyVM --resource-group MyRG --scripts "New-Item -Path C:\temp\test.txt -ItemType File"
	answer: create

	input: az group create --name MyResourceGroup --location eastus
	answer: create

	input: az vm create --resource-group MyResourceGroup --name MyVM --image UbuntuLTS
	answer: create

	input: az ad user create --display-name "New User" --password "password" --user-principal-name newuser@microsoft.com
	answer: create

	input: az vm update --resource-group MyResourceGroup --name MyVM --set tags.costCenter=1234
	answer: update

	input: az storage account update --name mystorageaccount --resource-group MyResourceGroup --set tags.department=IT
	answer: update

	input: az ad user update --id johndoe@microsoft.com --set displayName="John Doe Updated"
	answer: update

	input: az group delete --name MyResourceGroup
	answer: delete

	input: az vm delete --resource-group MyResourceGroup --name MyVM
	answer: delete

	input: az ad user delete --id johndoe@microsoft.com
	answer: delete
	`
	return prompt, nil
}

func (m AzureCliTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"account_id", "account_name", "account_type"},
		ConfigType:   "azure",
		ConfigSource: core.ToolConfigSourceAccount,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}
