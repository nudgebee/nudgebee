package tools

import (
	"fmt"
	"nudgebee/llm/cloud"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"strings"
)

const ToolExecuteAwsCliCommand = "aws_execute"

// Pre-compiled regexes for shell syntax detection in aws_execute commands.
var shellSyntaxRegexes = []*regexp.Regexp{
	regexp.MustCompile(`\bfor\b.*\bdo\b`),   // for ... do loop
	regexp.MustCompile(`\bwhile\b.*\bdo\b`), // while ... do loop
	regexp.MustCompile(`\bif\b.*\bthen\b`),  // if ... then conditional
	regexp.MustCompile(`(?m)^\s*done\s*$`),  // standalone "done" on its own line
}

func init() {
	core.RegisterNBToolFactory(ToolExecuteAwsCliCommand, func(accountId string) (core.NBTool, error) {
		return AwsCliTool{}, nil
	})
}

type AwsCliTool struct{}

func (t AwsCliTool) Name() string {
	return ToolExecuteAwsCliCommand
}

func (t AwsCliTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t AwsCliTool) Description() string {
	return `Executes 'aws' CLI commands.  This tool allows gathering information from various AWS services.

		**Usage:**

		* **Prioritize this tool:**  When interacting with AWS, use this tool to retrieve information or perform actions.
		* **Input:**  A valid 'aws' CLI command string.  Include necessary options and arguments. Be explicit about regions.
		* **Output:**  The raw output of the executed 'aws' CLI command.

		**Examples:**

		* aws s3 ls s3://my-bucket --region us-west-2
		* aws ec2 describe-instances --region eu-central-1 --filters "Name=tag:Environment,Values=production"
		* aws lambda list-functions --region us-east-1

		**Important Notes:**

		* Ensure correct command formatting and arguments. Always specify the region.
		* Do not include AWS credentials in commands.  Assume they are configured correctly in the environment.
		* For complex queries, use tools like 'jq' to parse and filter the JSON output.  Indicate this in the command.
		`
}

func (t AwsCliTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "The 'aws' CLI command to execute.",
			},
		},
		Required: []string{"command"},
	}
}

func (t AwsCliTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", t.Name())
	}

	command := strings.TrimSpace(input.Command)

	if !strings.HasPrefix(command, "aws") {
		command = "aws " + command // Ensure "aws" prefix
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

		auth, err := BuildAwsAuth(nbRequestContext.Ctx.GetContext(), creds)
		if err != nil {
			// Check for permanent STS errors that should not be retried
			errMsg := err.Error()
			if strings.Contains(errMsg, "PERMANENT ERROR") {
				return core.NBToolResponse{
					Data:   errMsg,
					Status: core.NBToolResponseStatusError,
				}, nil
			}
			return core.NBToolResponse{}, err
		}

		auth.Env[workspace.ENV_NB_TOOL_CONFIG_NAME] = nbRequestContext.ToolConfig.Name

		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, auth.Env)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("aws: unable to execute shell script", "error", err.Error(), "command", command)
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

	// Reject shell syntax in non-workspace mode — cloud.Execute only supports single CLI commands.
	// When workspace is enabled (above), commands run in an isolated pod where shell syntax is safe.
	if isShellSyntax(command) {
		return core.NBToolResponse{
			Data:   "ERROR: aws_execute accepts a single AWS CLI command, not shell scripts or loops. Call aws_execute once per command instead of using for-loops or pipes.",
			Status: core.NBToolResponseStatusError,
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
		nbRequestContext.Ctx.GetLogger().Error("aws-cli: command execution failed", "error", err.Error(), "command", command)
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

// classifyAwsVerb checks a single verb against AWS verb lists.
// Returns the classified ToolRequestType, or empty string if no match.
func classifyAwsVerb(verb string) core.ToolRequestType {
	// Exact-match overrides — these verbs have misleading prefixes
	// (e.g. "create-tags" starts with "create" but is an update, not a create)
	updateExact := map[string]bool{
		"create-tags":        true, // tagging is an update, not resource creation
		"put-bucket-logging": true, "put-bucket-versioning": true,
		"put-bucket-policy": true, "put-bucket-encryption": true,
		"put-bucket-tagging": true, // bucket config updates, not new resources
	}
	if updateExact[verb] {
		return core.ToolRequestTypeUpdate
	}

	// Read verbs — these never modify state
	readPrefixes := []string{
		"describe", "list", "get", "lookup", "search", "check", "show",
		"head", "scan", "query", "fetch", "download", "view", "preview",
		"batch-get", "batch-describe",
		"wait", // waits are polling, not mutations
	}
	for _, p := range readPrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeRead
		}
	}

	// Exact read verbs that don't follow prefix patterns
	readExact := map[string]bool{
		// s3 commands
		"ls": true, "presign": true,
		// sts
		"assume-role": true, "get-caller-identity": true, "decode-authorization-message": true,
		// iam report generation (read-only, generates a report to fetch later)
		"generate-credential-report": true, "generate-service-last-accessed-details": true,
		// cloudformation
		"validate-template": true, "estimate-template-cost": true,
		// general
		"help": true, "verify": true, "test-invoke-method": true,
		// eks
		"update-kubeconfig": true, // writes local kubeconfig, not a cloud mutation
		// read-only "start-*" commands — these initiate queries/streams, not state changes
		"start-query": true, "start-query-execution": true, "start-live-tail": true,
	}
	if readExact[verb] {
		return core.ToolRequestTypeRead
	}

	// Create verbs
	createPrefixes := []string{
		"create", "run", "launch", "allocate", "import",
		"register", "put", "send", "publish", "invoke", "execute",
		"request", "initiate",
	}
	for _, p := range createPrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeCreate
		}
	}
	createExact := map[string]bool{
		"mb": true, "cp": true, "sync": true, "mv": true, // s3 write ops
	}
	if createExact[verb] {
		return core.ToolRequestTypeCreate
	}

	// Update verbs — includes state-change operations (stop, start) that don't destroy resources
	updatePrefixes := []string{
		"update", "modify", "attach", "detach", "enable", "disable",
		"set", "tag", "untag", "associate", "disassociate",
		"add", "assign", "unassign", "replace", "reset", "restore",
		"apply", "configure", "change", "rotate",
		"stop", "start", // state changes, not resource creation/deletion
	}
	for _, p := range updatePrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeUpdate
		}
	}

	// Delete verbs — destructive operations that remove resources
	deletePrefixes := []string{
		"delete", "remove", "terminate", "deregister", "release",
		"revoke", "cancel", "purge", "destroy",
	}
	for _, p := range deletePrefixes {
		if strings.HasPrefix(verb, p) {
			return core.ToolRequestTypeDelete
		}
	}
	deleteExact := map[string]bool{
		"rb": true, "rm": true, // s3 delete ops
	}
	if deleteExact[verb] {
		return core.ToolRequestTypeDelete
	}

	return "" // no match
}

// inferAwsVerbType classifies an AWS CLI command by its verb prefix.
// Returns empty string if the verb is not recognized (caller should fall back to LLM).
func inferAwsVerbType(command string) core.ToolRequestType {
	parts := strings.Fields(strings.TrimSpace(command))

	if result := isCloudCLIInfoFlag(parts); result != "" {
		return result
	}

	if len(parts) < 2 {
		return ""
	}
	// Skip "aws" prefix
	start := 0
	if strings.EqualFold(parts[0], "aws") {
		start = 1
	}
	// Collect non-flag tokens (service name, verb, possible positional args)
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

	// Try candidates in forward order, skipping the first token (service name like "ec2", "s3").
	// This finds the verb before any positional args that follow it, avoiding false positives
	// where a positional arg (e.g. filename "delete-report.csv") matches a verb prefix.
	// e.g. "aws ec2 stop-instances i-1234" → skips "ec2", tries "stop-instances" (match)
	// e.g. "aws s3 cp delete-me.txt s3://bucket/" → skips "s3", tries "cp" (match)
	for i := 1; i < len(candidates); i++ {
		if result := classifyAwsVerb(candidates[i]); result != "" {
			return result
		}
	}
	// If only one candidate (service name only, e.g. "aws ec2"), try it as a fallback
	if len(candidates) == 1 {
		return classifyAwsVerb(candidates[0])
	}

	return "" // unknown — fall back to LLM
}

func (t AwsCliTool) InferToolRequestType(ctx *security.RequestContext, toolName, input string) (core.ToolRequestType, error) {
	requestType := inferAwsVerbType(extractCommandFromToolInput(input))
	if requestType != "" {
		return requestType, nil
	}
	// Unknown verb — return empty so caller falls through to LLM-based classification.
	ctx.GetLogger().Warn("aws: verb not recognized by heuristic, falling through to LLM classification", "input", input)
	return "", nil
}

func (t AwsCliTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	//  Similar to kubectl, determine if the AWS command is a create, update, delete, or read operation.
	prompt := `You are an AWS security expert. Your task is to classify an 'aws' CLI command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Commands that only display help, usage, or CLI information (e.g., --help, -h, --version) must be classified as 'read'.

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: aws ec2 --help
	answer: read

	input: aws s3 ls s3://my-bucket
	answer: read

	input: aws ec2 describe-instances
	answer: read

	input: aws lambda list-functions
	answer: read

	input: aws iam get-user --user-name Bob
	answer: read

	input: aws pi describe-dimension-keys --service-type RDS --identifier db-XXXXX --metric db.load.avg
	answer: read

	input: aws pi get-resource-metrics --service-type RDS --identifier db-XXXXX --metric-queries '[{"Metric":"db.load.avg"}]'
	answer: read

	input: aws pi get-dimension-key-details --service-type RDS --identifier db-XXXXX --group db.sql
	answer: read

	input: aws rds describe-db-instances --db-instance-identifier my-database
	answer: read

	input: aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name CPUUtilization
	answer: read

	input: aws logs describe-log-groups --log-group-name-prefix /aws/lambda/
	answer: read

	input: aws cloudtrail lookup-events --lookup-attributes AttributeKey=EventName,AttributeValue=RunInstances
	answer: read

	input: aws iam generate-credential-report
	answer: read

	input: aws iam get-credential-report --query 'Content' --output text
	answer: read

	input: aws s3 mb s3://my-new-bucket
	answer: create

	input: aws ec2 run-instances --image-id ami-12345678 --instance-type t2.micro
	answer: create

	input: aws iam create-user --user-name Bob
	answer: create

	input: aws ec2 modify-instance-attribute --instance-id i-1234567890abcdef0 --no-disable-api-termination
	answer: update

	input: aws s3api put-bucket-policy --bucket my-bucket --policy file://policy.json
	answer: update

	input: aws lambda update-function-code --function-name my-function --zip-file fileb://function.zip
	answer: update

	input: aws iam attach-user-policy --user-name Bob --policy-arn arn:aws:iam::aws:policy/IAMReadOnlyAccess
	answer: update

	input: aws s3 rb s3://my-bucket --force
	answer: delete

	input: aws ec2 terminate-instances --instance-ids i-1234567890abcdef0
	answer: delete

	input: aws iam delete-user --user-name Bob
	answer: delete

	input: aws s3 rm s3://my-bucket/my-object
	answer: delete
	`
	return prompt, nil
}

func (m AwsCliTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"account_id", "account_name", "account_type"},
		ConfigType:   "aws",
		ConfigSource: core.ToolConfigSourceAccount,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}

func (m AwsCliTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	for _, config := range availableConfigs {
		var accountNumber string
		for _, v := range config.Values {
			if v.Name == "account_number" {
				accountNumber = v.Value
				break
			}
		}

		if accountNumber != "" && ctx.QueryConfig.Labels != nil {
			var cloudAccountID string
			if val, ok := ctx.QueryConfig.Labels["nb_cloud_account_id"].(string); ok {
				cloudAccountID = val
			}

			if cloudAccountID != "" && cloudAccountID == accountNumber {
				return config, nil
			}
		}
	}

	return core.ToolConfig{}, nil
}

// isShellSyntax detects bash/shell constructs that don't belong in a single AWS CLI command.
// It strips content inside quoted strings first to avoid false positives on legitimate AWS CLI
// arguments like JMESPath queries which use &&, ||, and pipe operators inside quotes.
func isShellSyntax(command string) bool {
	// Strip quoted content so operators inside quotes (e.g. JMESPath) are not flagged.
	stripped := stripQuotedContent(command)

	// Structurally unambiguous shell operators
	unambiguousPatterns := []string{"$(", "&&", "||", "; do", ";do", "\ndo ", "\ndone"}
	for _, p := range unambiguousPatterns {
		if strings.Contains(stripped, p) {
			return true
		}
	}
	// Word-boundary-aware checks on shell keywords
	for _, re := range shellSyntaxRegexes {
		if re.MatchString(stripped) {
			return true
		}
	}
	return false
}

// stripQuotedContent removes content inside single-quoted and double-quoted strings,
// preserving the quote delimiters. This prevents operators inside CLI arguments
// (e.g. JMESPath --query '... && ...') from being misidentified as shell syntax.
func stripQuotedContent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch == '\'' || ch == '"' {
			b.WriteByte(ch)
			quote := ch
			i++
			// Skip until closing quote (handle backslash escapes in double quotes)
			for i < len(s) {
				if s[i] == '\\' && quote == '"' && i+1 < len(s) {
					i += 2 // skip escaped char in double quotes
					continue
				}
				if s[i] == quote {
					b.WriteByte(quote)
					i++
					break
				}
				i++
			}
		} else {
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}
