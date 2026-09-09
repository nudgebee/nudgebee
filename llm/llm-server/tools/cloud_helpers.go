package tools

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"regexp"
	"strings"
)

var (
	ErrAccountNumberNotFound = errors.New("account number not found")
	ErrCloudProviderNotFound = errors.New("cloud provider not found")
)

type CloudAccountCredentials struct {
	ID         string
	AssumeRole *string
	// ExternalId is the sts:ExternalId to present when assuming AssumeRole.
	// Omitting it fails any trust policy carrying an sts:ExternalId condition.
	ExternalId    *string
	AccessKey     *string
	AccessSecret  *string
	Region        *string
	Data          *string
	AccountNumber string
	AccountName   string
	CloudProvider string
}

func GetCloudAccountCredentials(accountId string) (CloudAccountCredentials, error) {
	databaseManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return CloudAccountCredentials{}, err
	}

	query := `
		SELECT
			assume_role,
			external_id,
			access_key,
			access_secret,
			region,
			data::varchar,
			cloud_provider,
			account_number,
			account_name
		FROM cloud_accounts
		WHERE id = $1
	`
	r, err := databaseManager.QueryRow(query, accountId)
	if err != nil {
		return CloudAccountCredentials{}, err
	}

	var (
		assumeRole, externalId, accessKey, accessSecret, region, cloudProvider, accountNumber, accountName *string
		data                                                                                               sql.NullString
	)

	err = r.Scan(&assumeRole, &externalId, &accessKey, &accessSecret, &region, &data, &cloudProvider, &accountNumber, &accountName)
	if err != nil {
		if err == sql.ErrNoRows {
			return CloudAccountCredentials{}, fmt.Errorf("account with id %s not found", accountId)
		}
		return CloudAccountCredentials{}, err
	}

	if accountNumber == nil {
		return CloudAccountCredentials{}, ErrAccountNumberNotFound
	}
	if cloudProvider == nil {
		return CloudAccountCredentials{}, ErrCloudProviderNotFound
	}

	if accountName == nil {
		accountName = accountNumber
	}

	var dataValue *string
	if data.Valid {
		dataValue = &data.String
	}

	creds := CloudAccountCredentials{
		ID:            accountId,
		AssumeRole:    assumeRole,
		ExternalId:    externalId,
		AccessKey:     accessKey,
		AccessSecret:  accessSecret,
		Region:        region,
		Data:          dataValue,
		AccountNumber: *accountNumber,
		AccountName:   *accountName,
		CloudProvider: *cloudProvider,
	}

	// Attempt to decrypt AccessSecret
	if creds.AccessSecret != nil {
		decrypted, err := common.Decrypt(*creds.AccessSecret)
		if err != nil {
			return CloudAccountCredentials{}, fmt.Errorf("failed to decrypt access secret: %w", err)
		}
		creds.AccessSecret = &decrypted
	}

	return creds, nil
}

// extractCommandFromToolInput extracts the "command" field from a JSON tool input string.
// Tool inputs arrive as JSON (e.g. {"command":"az vm list --help"}) but the verb-classification
// heuristics expect a plain command string. Returns the input unchanged if it's not valid JSON
// or doesn't contain a "command" string field.
func extractCommandFromToolInput(input string) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &parsed); err == nil && parsed.Command != "" {
		return parsed.Command
	}
	return input
}

// isCloudCLIInfoFlag checks if the command parts contain flags that only
// display help, usage, or version information (always read-only operations).
func isCloudCLIInfoFlag(parts []string) core.ToolRequestType {
	for _, p := range parts {
		if p == "--help" || p == "-h" || p == "--version" {
			return core.ToolRequestTypeRead
		}
	}
	return ""
}

// CloudCliToolFor returns the registered cloud-CLI tool that owns this command, or "" when the
// command is not a cloud CLI.
//
// The generic executors (remediation, shell) run whatever they are given in a workspace pod carrying
// no provider credentials. A cloud CLI invoked that way does not fail loudly — it prints its
// auth-required hint to stdout and exits 0, so the caller records a success for an action that never
// ran (see #28804). Routing the command to its own tool is what gets the credentials injected.
func CloudCliToolFor(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	switch strings.ToLower(fields[0]) {
	case "aws":
		return ToolExecuteAwsCliCommand
	case "az":
		return ToolExecuteAzureCliCommand
	case "gcloud", "gsutil":
		return ToolExecuteGcpCliCommand
	default:
		return ""
	}
}

// wrapCloudCliErr wraps a cloud CLI execution failure. The %w matters: workspaceOutcome unwraps to
// *workspace.CommandFailure to recover the process exit code an operator is shown, and %v would
// break that silently.
func wrapCloudCliErr(toolName string, err error) error {
	return fmt.Errorf("cloud cli: %s failed: %w", toolName, err)
}

// ExecuteCloudCli runs a cloud CLI command through its own NBTool, which resolves the account's
// credentials and executes in a workspace pod. Returns the command output and any execution error.
//
// conversationId names the working directory the workspace runs the command in. The workspace
// rejects an empty one outright ("Conversation ID is empty"), so a caller that has no conversation
// -- the remediation panel's Run button is triggered by a click, not a chat -- must still supply
// something; DefaultCloudCliConversationId builds a stable per-account value for that case.
func ExecuteCloudCli(ctx *security.RequestContext, toolName, accountId, conversationId, command string) (string, error) {
	// Guarded here rather than at the GetUserId call below: ListToolConfigs dereferences the security
	// context first (for the tenant id), so a nil one panics before that line is ever reached.
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return "", fmt.Errorf("cloud cli: a security context is required to run %s", toolName)
	}
	if strings.TrimSpace(accountId) == "" {
		return "", fmt.Errorf("cloud cli: account ID cannot be empty")
	}

	nbTool, found := core.GetNBTool(accountId, toolName)
	if !found {
		return "", fmt.Errorf("cloud cli: %s is not available", toolName)
	}

	// These tools source their config from the account list, which covers every active cloud account
	// of that provider in the tenant — so the account's own row has to be picked out by id. Its
	// absence is the useful signal that the command does not belong to this account, which is how an
	// `aws` command on a Kubernetes or Azure account is caught.
	configs, err := core.ListToolConfigs(ctx, accountId, nbTool)
	if err != nil {
		return "", fmt.Errorf("cloud cli: unable to resolve %s config: %w", toolName, err)
	}
	configName := ""
	for _, cfg := range configs {
		for _, v := range cfg.Values {
			if v.Name == "id" && strings.EqualFold(strings.TrimSpace(v.Value), strings.TrimSpace(accountId)) {
				configName = cfg.Name
				break
			}
		}
		if configName != "" {
			break
		}
	}
	if configName == "" {
		return "", fmt.Errorf("cloud cli: this account has no %s credentials configured, so the command cannot be run", toolName)
	}

	// Empty is not a usable value downstream, so normalize here rather than trusting every caller:
	// the workspace names its working directory after this and fails the run when it is blank.
	if strings.TrimSpace(conversationId) == "" {
		conversationId = DefaultCloudCliConversationId(accountId)
	}

	queryConfig := core.NBQueryConfig{ToolConfigs: map[string]string{nbTool.Name(): configName}}
	toolCtx := core.NewNbToolContext(ctx, nbTool, accountId, ctx.GetSecurityContext().GetUserId(), conversationId, "", "", command, nil, "", queryConfig, "")

	resp, err := nbTool.Call(toolCtx, core.NBToolCallRequest{Command: command})
	if err != nil {
		return resp.Data, wrapCloudCliErr(toolName, err)
	}
	// A credential or STS failure comes back as an error status with a nil error (the tool reports it
	// as data so the model can read it). Without this the caller would record that run as a success.
	if resp.Status == core.NBToolResponseStatusError {
		return resp.Data, fmt.Errorf("cloud cli: %s reported an error: %s", toolName, resp.Data)
	}
	return resp.Data, nil
}

// cloudCliConversationIdUnsafe matches every character the workspace's own path check rejects
// (it accepts ^[a-zA-Z0-9_-]+$), so an id built from an unexpected account id shape still lands
// inside a directory name the workspace will accept instead of failing the run.
var cloudCliConversationIdUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// DefaultCloudCliConversationId is the workspace working directory used by a cloud CLI command that
// did not originate in a conversation. It is per-account so those runs share one directory (and so
// reuse the account's workspace) while staying clear of the raw conversation ids the agent path uses.
func DefaultCloudCliConversationId(accountId string) string {
	return "remediation-" + cloudCliConversationIdUnsafe.ReplaceAllString(strings.TrimSpace(accountId), "-")
}

// RemediationSubstrate says how one remediation command should be run.
// Exactly one of CloudCliTool, RelayTool or Reject is set.
type RemediationSubstrate struct {
	// CloudCliTool is the credentialed cloud CLI tool to run this through (workspace pod).
	CloudCliTool string
	// RelayJob / RelayTool address the customer's own network through the relay.
	RelayJob  RelayJob
	RelayTool string
	// Reject, when set, is an operator-facing reason this command cannot run on this account.
	Reject string
}

// RemediationSubstrateFor picks the executor from the ACCOUNT'S PROVIDER first, falling back to the
// command's shape only within what that provider can actually run.
//
// Dispatching on the command's first word alone (CloudCliToolFor) had two failure modes that a
// provider check removes outright. It missed cloud CLIs it did not enumerate -- `bq` is a GCP CLI
// that GcpCliTool advertises but the prefix table never listed, so it fell through to the shell path
// and ran with no GCP credentials, printing its auth hint and exiting 0: a remediation recorded as
// applied that never happened. And it happily sent `kubectl` from a cloud account down the relay to
// an agent that cannot exist there, which surfaced as `agent not connected` -- a connectivity error
// for what is really a category error, telling the operator nothing about why.
//
// An unknown or unreadable provider falls back to the old prefix dispatch rather than refusing to
// run: a metastore hiccup must not take every remediation offline.
func RemediationSubstrateFor(provider, command string) RemediationSubstrate {
	var binary string
	if fields := strings.Fields(command); len(fields) > 0 {
		binary = strings.ToLower(fields[0])
	}

	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "k8s":
		// A Kubernetes account is not kubectl-only: helm and argocd carry their own credentials and
		// are ordinary remediations on one. All three need the customer's cluster, so all three go
		// down the relay; anything else is a plain shell command.
		switch binary {
		case "kubectl":
			return RemediationSubstrate{RelayJob: RelayJobKubectl, RelayTool: ToolExecuteKubectlCommand}
		case "helm":
			return RemediationSubstrate{RelayJob: RelayJobHelm, RelayTool: ToolExecuteHelmCommand}
		case "argocd":
			return RemediationSubstrate{RelayJob: RelayJobArgoCD, RelayTool: ToolExecuteArgoCDCommand}
		case "aws", "az", "gcloud", "gsutil", "bq":
			return RemediationSubstrate{Reject: rejectWrongProvider(binary, "Kubernetes")}
		default:
			return RemediationSubstrate{RelayJob: RelayJobShell, RelayTool: ToolExecuteServerCommand}
		}
	case "aws":
		return cloudSubstrate(binary, "AWS", map[string]bool{"aws": true}, ToolExecuteAwsCliCommand)
	case "azure":
		return cloudSubstrate(binary, "Azure", map[string]bool{"az": true}, ToolExecuteAzureCliCommand)
	case "gcp":
		return cloudSubstrate(binary, "GCP", map[string]bool{"gcloud": true, "gsutil": true, "bq": true}, ToolExecuteGcpCliCommand)
	}

	// Unknown provider (CloudFoundry, a new one, or the lookup failed): behave exactly as before.
	if tool := CloudCliToolFor(command); tool != "" {
		return RemediationSubstrate{CloudCliTool: tool}
	}
	return RemediationSubstrate{RelayJob: RelayJobShell, RelayTool: ToolExecuteServerCommand}
}

// cloudSubstrate resolves one cloud provider's arm of RemediationSubstrateFor.
func cloudSubstrate(binary, label string, own map[string]bool, tool string) RemediationSubstrate {
	if own[binary] {
		return RemediationSubstrate{CloudCliTool: tool}
	}
	switch binary {
	case "kubectl", "helm", "argocd":
		// There is no cluster and no relay agent behind a cloud account, so this can only ever fail.
		// Saying which account this is beats the `agent not connected` the relay would have returned.
		return RemediationSubstrate{Reject: fmt.Sprintf(
			"this is a %s account, so %q cannot run here — there is no Kubernetes cluster attached to it. Regenerate the plan to get a %s CLI command.",
			label, binary, label)}
	case "aws", "az", "gcloud", "gsutil", "bq":
		return RemediationSubstrate{Reject: rejectWrongProvider(binary, label)}
	default:
		// A shell command still has somewhere to go: the account's workspace pod.
		return RemediationSubstrate{RelayJob: RelayJobShell, RelayTool: ToolExecuteServerCommand}
	}
}

func rejectWrongProvider(binary, label string) string {
	return fmt.Sprintf("this is a %s account, so the %q CLI has no credentials here. Regenerate the plan to get a command for %s.", label, binary, label)
}

// GetCloudProviderForAccount returns the account's cloud_provider ("AWS", "Azure", "GCP", "K8s",
// "CloudFoundry"), or "" when it cannot be read. It lives here rather than in agents because both
// remediation callers dispatch on it and tools cannot import agents.
//
// An empty return is deliberately not an error: callers fall back to command-shaped dispatch, so a
// metastore hiccup degrades routing instead of taking remediation offline.
func GetCloudProviderForAccount(accountId string) string {
	if strings.TrimSpace(accountId) == "" {
		return ""
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}
	var cloudProvider string
	if err := dbms.Db.Get(&cloudProvider, "SELECT cloud_provider FROM cloud_accounts WHERE id = $1", accountId); err != nil {
		return ""
	}
	return cloudProvider
}
