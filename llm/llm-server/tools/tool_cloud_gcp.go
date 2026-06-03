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

const ToolExecuteGcpCliCommand = "gcloud_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteGcpCliCommand, func(accountId string) (core.NBTool, error) {
		return GcpCliTool{}, nil
	})
}

type GcpCliTool struct{}

func (t GcpCliTool) Name() string {
	return ToolExecuteGcpCliCommand
}

func (t GcpCliTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t GcpCliTool) Description() string {
	return `Executes 'gcloud' CLI commands. This tool allows gathering information from various GCP services.

		**Usage:**

		* **Prioritize this tool:** When interacting with GCP, use this tool to retrieve information or perform actions.
		* **Input:** A valid 'gcloud' CLI command string. Include necessary options and arguments. Be explicit about projects, zones, and regions.
		* **Output:** The raw output of the executed 'gcloud' CLI command.

		**Examples:**

		* gcloud compute instances list --project my-gcp-project --zone us-central1-a
		* gcloud storage ls gs://my-gcp-bucket
		* gcloud functions list --project my-gcp-project --region us-east1

		**Important Notes:**

		* Ensure correct command formatting and arguments. Always specify the project, zone, and/or region when necessary.
		* Do not include GCP credentials in commands. Assume they are configured correctly in the environment.
		* For complex queries, use tools like 'jq' to parse and filter the JSON output. Indicate this in the command.
		* **Auth commands are blocked:** Do NOT use 'gcloud auth' commands (auth list, auth activate-service-account, etc.). The environment is pre-authenticated. Use 'gcloud config list' to check current identity or 'gcloud iam service-accounts describe' for service account details instead.
		`
}

func (t GcpCliTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "The 'gcloud' CLI command to execute.",
			},
		},
		Required: []string{"command"},
	}
}

func (t GcpCliTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", t.Name())
	}

	command := strings.TrimSpace(input.Command)

	// Handle backslash line continuations
	command = strings.ReplaceAll(command, "\\\r\n", " ")
	command = strings.ReplaceAll(command, "\\\n", " ")

	if !strings.HasPrefix(command, "gcloud") {
		command = "gcloud " + command // Ensure "gcloud" prefix
	}

	// Denylist auth commands
	args, err := shlex.Split(command)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("failed to parse command for denylist check: %w", err)
	}
	if len(args) > 1 && args[1] == "auth" {
		return core.NBToolResponse{}, fmt.Errorf("auth commands not allowed")
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

		auth, err := BuildGcpAuth(creds)
		if err != nil {
			return core.NBToolResponse{}, err
		}

		auth.Env[workspace.ENV_NB_TOOL_CONFIG_NAME] = nbRequestContext.ToolConfig.Name
		fullCommand := WrapCommandWithAuth(command, auth)

		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, fullCommand, auth.Env)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("gcp: unable to execute shell script", "error", err.Error(), "command", fullCommand)
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
		nbRequestContext.Ctx.GetLogger().Error("gcp-cli: command execution failed", "error", err.Error(), "command", command)
		return core.NBToolResponse{}, err
	}

	data := ""
	if response["data"] != nil {
		data = response["data"].(string)
	} else if response["errors"] != nil {
		errorsArr, ok := response["errors"].([]any)
		if ok && len(errorsArr) > 0 {
			errorMap, ok := errorsArr[0].(map[string]any)
			if ok {
				data = errorMap["message"].(string)
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

func (t GcpCliTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	// If project ID or account name is mentioned in the command, try to find matching config
	command := strings.ToLower(input.Command)
	var matches []core.ToolConfig

	for _, config := range availableConfigs {
		matched := false
		// Try matching config name first (e.g., "gcp-dev - my-project-dev")
		if config.Name != "" && strings.Contains(command, strings.ToLower(config.Name)) {
			matched = true
		}

		if !matched {
			// Check values for id (project_id), account_number, etc.
			for _, v := range config.Values {
				lowName := strings.ToLower(v.Name)
				if (lowName == "id" || lowName == "project_id" || lowName == "account_number" ||
					lowName == "account_id" || lowName == "name") && len(v.Value) >= 3 {
					if strings.Contains(command, strings.ToLower(v.Value)) {
						matched = true
						break
					}
				}
			}
		}

		if matched {
			matches = append(matches, config)
		}
	}

	// Ambiguous: multiple configs matched — let the next strategy decide
	if len(matches) != 1 {
		return core.ToolConfig{}, nil
	}
	return matches[0], nil
}

// classifyGcpVerb checks a single verb against GCP verb lists.
// Returns the classified ToolRequestType, or empty string if no match.
func classifyGcpVerb(verb string) core.ToolRequestType {
	// Read verbs — these never modify state
	readPrefixes := []string{
		"list", "describe", "get", "show", "search", "browse",
		"export", "download", "ls", "cat", "tail", "read",
		"check", "lookup", "fetch", "print", "view", "preview",
		"query", "scan", "wait",
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
		"create", "deploy", "import", "upload", "run",
		"submit", "insert", "launch",
		"allocate", "register", "put", "send", "publish",
		"invoke", "execute", "request", "initiate",
	}
	for _, p := range createPrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeCreate
		}
	}

	// Update verbs — includes state-change operations that don't destroy resources
	updatePrefixes := []string{
		"update", "set", "add-tags", "remove-tags", "patch",
		"enable", "disable", "resize", "reset", "restart",
		"move", "promote", "restore", "rollback",
		"attach", "detach", "modify", "assign", "unassign",
		"tag", "untag", "associate", "disassociate",
		"replace", "apply", "configure", "change", "rotate",
		"stop", "start", // state changes, not resource deletion
		"add", // e.g. add-iam-policy-binding
	}
	for _, p := range updatePrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeUpdate
		}
	}

	// Delete verbs — destructive operations that remove resources
	deletePrefixes := []string{
		"delete", "remove", "cancel", "revoke",
		"destroy", "drain", "purge", "terminate",
		"deregister", "release",
	}
	for _, p := range deletePrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeDelete
		}
	}

	return "" // no match
}

// inferGcpVerbType classifies a GCP CLI command by its verb.
// Returns empty string if the verb is not recognized (caller should fall back to LLM).
func inferGcpVerbType(command string) core.ToolRequestType {
	parts := strings.Fields(strings.TrimSpace(command))

	if result := isCloudCLIInfoFlag(parts); result != "" {
		return result
	}

	if len(parts) < 2 {
		return ""
	}
	// Skip "gcloud" prefix
	start := 0
	if strings.EqualFold(parts[0], "gcloud") {
		start = 1
	}

	// Local CLI management commands — these operate on the local gcloud installation
	// or client configuration only and never create, update, or delete GCP resources.
	// Classify all of them as read-only so they never trigger a user confirmation prompt.
	//
	// gcloud config   — e.g. "gcloud config set project ...", "gcloud config list"
	// gcloud components — e.g. "gcloud components update", "gcloud components install sdk"
	//                    (manages the local gcloud CLI binary, not GCP resources)
	localCliGroups := map[string]bool{
		"config":     true,
		"components": true,
	}
	if start < len(parts) && localCliGroups[strings.ToLower(parts[start])] {
		return core.ToolRequestTypeRead
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

	// Try candidates in forward order, skipping the first token (service like "compute").
	// This finds the verb before any positional args that follow it, avoiding false positives
	// where a positional arg (e.g. instance name "delete-old-cache") matches a verb prefix.
	// e.g. "gcloud compute instances stop my-instance" → skips "compute", tries "instances" (no match), tries "stop" (match)
	// e.g. "gcloud compute instances stop remove-cache" → skips "compute", tries "instances" (no match), tries "stop" (match)
	for i := 1; i < len(candidates); i++ {
		if result := classifyGcpVerb(candidates[i]); result != "" {
			return result
		}
	}
	// If only one candidate (service name only, e.g. "gcloud compute"), try it as a fallback
	if len(candidates) == 1 {
		return classifyGcpVerb(candidates[0])
	}

	return "" // unknown — caller decides
}

func (t GcpCliTool) InferToolRequestType(ctx *security.RequestContext, toolName, input string) (core.ToolRequestType, error) {
	requestType := inferGcpVerbType(extractCommandFromToolInput(input))
	if requestType != "" {
		return requestType, nil
	}
	// Unknown verb — return empty so caller falls through to LLM-based classification.
	ctx.GetLogger().Warn("gcp: verb not recognized by heuristic, falling through to LLM classification", "input", input)
	return "", nil
}

func (t GcpCliTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	// Similar to kubectl, determine if the GCP command is a create, update, delete, or read operation.
	prompt := `You are a GCP security expert. Your task is to classify a 'gcloud' CLI command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Commands that only display help, usage, or CLI information (e.g., --help, -h, --version) must be classified as 'read'.

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: gcloud monitoring --help
	answer: read

	input: gcloud compute instances list --help
	answer: read

	input: gcloud compute instances list
	answer: read

	input: gcloud storage ls gs://my-bucket
	answer: read

	input: gcloud projects get-iam-policy my-project
	answer: read

	input: gcloud compute instances describe my-instance
	answer: read

	input: gcloud compute instances create my-instance
	answer: create

	input: gcloud sql instances create my-instance --database-version=POSTGRES_13 --region=us-central1
	answer: create

	input: gcloud storage buckets create gs://my-new-bucket
	answer: create

	input: gcloud compute instances add-tags my-instance --tags=web,dev
	answer: update

	input: gcloud functions deploy my-function --trigger-http
	answer: update

	input: gcloud projects set-iam-policy my-project policy.json
	answer: update

	input: gcloud compute instances delete my-instance
	answer: delete

	input: gcloud sql instances delete my-instance
	answer: delete

	input: gcloud storage objects delete gs://my-bucket/my-object
	answer: delete
	`
	return prompt, nil
}

func (m GcpCliTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"account_id", "account_name", "account_type"},
		ConfigType:   "gcp",
		ConfigSource: core.ToolConfigSourceAccount,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}
