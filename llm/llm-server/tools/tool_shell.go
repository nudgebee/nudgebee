package tools

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"

	"github.com/google/shlex"
	"github.com/lib/pq"
)

var wm = workspace.NewWorkspaceManager()

func init() {
	core.RegisterNBToolFactory(core.ToolExecuteShellCommand, func(accountId string) (core.NBTool, error) {
		return ShellTool{AccountId: accountId}, nil
	})
}

func isAlphaNum(c uint8) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

type ShellTool struct {
	AccountId string
}

func (m ShellTool) Name() string {
	return core.ToolExecuteShellCommand
}

func (m ShellTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ShellTool) Description() string {
	return `Executes raw shell commands in a secure workspace environment for system operations and ad-hoc tasks.
	Use this tool for:
	* Direct terminal access (ls, grep, curl, cat, jq, etc.).
	* Checking system state, file contents, or network connectivity.
	* Running specific scripts or simple command chains.

	**Do NOT use for:**
	* Complex code analysis or debugging (use 'agent_code_2').
	* Automated Root Cause Analysis (use 'agent_code_2').
	* Deep Git repository analysis for bug fixing (use 'agent_code_2').
	* Creating Pull Requests (use 'agent_code_2').

	**IMPORTANT CONSTRAINTS:**
	* **Stateless:** Each command runs in a new shell instance. 'cd' commands do not persist. You MUST chain commands using '&&' (e.g., 'cd /target && ls').
	* **Persistence:** To persist environment variables across steps, append them to '.nb_profile' (e.g., 'echo "export VAR=val" >> .nb_profile').
	* **Non-Interactive:** Do NOT run commands that require user input (e.g., 'vim', 'top', 'python' without a script). Use non-interactive flags.
	* **Timeout:** Commands have a strict execution time limit.

	**Examples:**
	* 'curl -s https://example.com | grep "title"'
	* 'cd /app/workspaces && ls -la'
	* 'jq .key data.json'
	`
}

func (m ShellTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Shell command to execute. Must be non-interactive.",
			},
			"work_dir": {
				Type:        core.ToolSchemaTypeString,
				Description: "Working directory to execute the command in (optional).",
			},
		},
		Required: []string{"command"},
	}
}

func (m ShellTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	command := input.Command
	if command == "" {
		if cmd, ok := input.Arguments["command"].(string); ok {
			command = cmd
		}
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return core.NBToolResponse{
			Data:   "Empty command provided.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("empty command")
	}

	// Size-limited logging to avoid excessive output in logs
	cmdLog := command
	if len(cmdLog) > 100 {
		cmdLog = cmdLog[:100] + "..."
	}
	nbRequestContext.Ctx.GetLogger().Info("shell: executing shell command", "command_preview", cmdLog)

	// Recon-pattern observability: label matches are best-effort and trivially
	// bypassable; logged so operators can alert on them, never used as a gate.
	if labels := detectSuspiciousShellPatterns(input.Command); len(labels) > 0 {
		nbRequestContext.Ctx.GetLogger().Warn(
			"shell: suspicious command pattern detected",
			"suspicious_patterns", labels,
			"command_preview", cmdLog,
		)
	}

	if !config.Config.LlmServerShellToolEnabled {
		return core.NBToolResponse{
			Data:   "Shell tool is only available when shell tool is enabled.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("shell tool is disabled")
	}

	// Handle optional work_dir (sanitized and escaped to prevent command injection)
	if wd, ok := input.Arguments["work_dir"].(string); ok && wd != "" {
		sanitizedWd := common.SanitizePath(wd)
		if sanitizedWd != "" {
			command = fmt.Sprintf("cd %s && %s", common.ShellEscape(sanitizedWd), command)
		}
	}

	// Auto-persistence: touch the profile to ensure it exists, then source it, then run command
	// We use '.' instead of 'source' for better POSIX compatibility (e.g. Alpine ash)
	const profileFile = ".nb_profile"
	command = fmt.Sprintf("touch %s && . ./%s && %s", profileFile, profileFile, command)

	// Prepare env — inject cloud credentials if the account is a cloud account (AWS/GCP/Azure).
	// This allows the shell tool to run cloud CLI commands (aws, gcloud, az) without requiring
	// the planner to route through specialized cloud tools.
	env := map[string]string{}
	if config.Config.LlmServerWorkspaceEnabled {
		cloudAuth, err := m.buildCloudAuthEnv(nbRequestContext, command)
		if err != nil {
			// Non-fatal: log the warning and proceed without cloud auth.
			// The account may not be a cloud account (e.g. K8s-only), or creds may be missing.
			slog.Warn("shell: cloud auth injection skipped", "account_id", m.AccountId, "error", err)
		} else if cloudAuth != nil {
			for k, v := range cloudAuth.Env {
				env[k] = v
			}
			command = WrapCommandWithBestEffortAuth(command, cloudAuth)
		}

		// Inject GITHUB_TOKEN when the command invokes `gh`. Same shape as the
		// cloud cross-account path: hint via QueryConfig.ToolConfigs first, then
		// fall back to the sole active github integration in the tenant.
		if ghAuth, err := m.buildGithubAuthEnv(nbRequestContext, command); err != nil {
			slog.Warn("shell: github auth injection skipped", "account_id", m.AccountId, "error", err)
		} else if ghAuth != nil {
			for k, v := range ghAuth.Env {
				env[k] = v
			}
		}
	}

	response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, env)

	// Scrub any sensitive credential values from the output to prevent accidental
	// exposure (e.g. if the LLM runs "env" or "printenv" on a cloud account).
	if len(env) > 0 {
		response = ScrubCredentials(response, env)
	}

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("shell: unable to execute shell command", "error", err.Error(), "command_preview", cmdLog)
		if response == "" {
			response = err.Error()
		}
		return core.NBToolResponse{
			Data:   ScrubCredentials(response, env),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// Wrap in JSON to be consistent with other execution tools
	outputformat := map[string]string{
		"stdout": response,
	}
	outputformatBytes, err := common.MarshalJson(outputformat)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("shell: unable to marshal response", "error", err.Error())
		return core.NBToolResponse{
			Data:   response,
			Status: core.NBToolResponseStatusError,
		}, err
	}
	response = string(outputformatBytes)

	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// buildCloudAuthEnv checks if the shell tool's account is a cloud account (AWS/GCP/Azure)
// and returns the auth environment + command wrappers needed for CLI access.
// For non-cloud accounts (e.g. K8s), it falls back to cross-account auth when the
// command is a recognized cloud CLI (gcloud, aws, az).
func (m ShellTool) buildCloudAuthEnv(nbRequestContext core.NbToolContext, command string) (*CloudAuthResult, error) {
	if m.AccountId == "" {
		return nil, nil
	}

	creds, err := GetCloudAccountCredentials(m.AccountId)
	if err != nil {
		if errors.Is(err, ErrAccountNumberNotFound) || errors.Is(err, ErrCloudProviderNotFound) {
			return m.buildCrossAccountCloudAuth(nbRequestContext, command)
		}
		return nil, err
	}

	provider := strings.ToLower(creds.CloudProvider)
	switch provider {
	case "aws":
		return BuildAwsAuth(nbRequestContext.Ctx.GetContext(), creds)
	case "gcp":
		return BuildGcpAuth(creds)
	case "azure":
		return BuildAzureAuth(creds)
	default:
		// Not a cloud account (e.g. kubernetes) — try cross-account auth
		// only if the command is a recognized cloud CLI.
		return m.buildCrossAccountCloudAuth(nbRequestContext, command)
	}
}

// cloudCLIMapping maps command substrings to their cloud provider and
// corresponding dedicated tool name (used for tool_configs hint resolution).
var cloudCLIMapping = []struct {
	keywords []string // substrings to look for in the command
	provider string   // cloud_accounts.cloud_provider value
	toolName string   // dedicated tool name for config hint lookup
}{
	{keywords: []string{"gcloud", "gsutil", "bq"}, provider: "gcp", toolName: ToolExecuteGcpCliCommand},
	{keywords: []string{"aws"}, provider: "aws", toolName: ToolExecuteAwsCliCommand},
	{keywords: []string{"az"}, provider: "azure", toolName: ToolExecuteAzureCliCommand},
}

// detectCloudCLI checks if the command invokes a cloud CLI and returns the
// cloud provider and the corresponding tool name for config hint resolution.
func detectCloudCLI(command string) (provider, toolName string) {
	// Fast-path: skip shlex parsing entirely if no cloud keyword appears anywhere.
	lowerCmd := strings.ToLower(command)
	found := false
	for _, m := range cloudCLIMapping {
		for _, kw := range m.keywords {
			if strings.Contains(lowerCmd, kw) {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return "", ""
	}

	tokens, err := shlex.Split(command)
	if err != nil {
		// If the command is malformed (e.g., mismatched quotes), fail gracefully
		return "", ""
	}

	for _, token := range tokens {
		lowerToken := strings.ToLower(token)
		for _, m := range cloudCLIMapping {
			for _, kw := range m.keywords {
				if strings.HasPrefix(lowerToken, kw) {
					// Check if it's an exact match OR followed by a non-alphanumeric character (e.g. aws|grep, gcloud;ls)
					if len(lowerToken) == len(kw) || !isAlphaNum(lowerToken[len(kw)]) {
						return m.provider, m.toolName
					}
				}
			}
		}
	}
	return "", ""
}

// buildCrossAccountCloudAuth injects cloud credentials for a non-cloud account
// (e.g. Kubernetes) when the shell command is a recognized cloud CLI.
//
// Resolution order:
//  1. If QueryConfig.ToolConfigs has a hint for the detected CLI tool (e.g.
//     gcloud_execute → "gcp-dev - nudgebee-dev"), use that specific account.
//  2. If no hint exists and exactly one account of that provider is active in
//     the tenant, use it.
//  3. If multiple accounts exist with no hint, skip — ambiguous.
func (m ShellTool) buildCrossAccountCloudAuth(nbRequestContext core.NbToolContext, command string) (*CloudAuthResult, error) {
	provider, toolName := detectCloudCLI(command)
	if provider == "" {
		return nil, nil // not a cloud CLI command
	}

	sc := nbRequestContext.Ctx.GetSecurityContext()
	tenantId := sc.GetTenantId()
	if tenantId == "" {
		return nil, nil
	}

	// Strategy 1: use the conversation's tool_config hint if the planner already
	// resolved which cloud account to use for this tool.
	if configName := nbRequestContext.QueryConfig.ToolConfigs[toolName]; configName != "" {
		accountId, err := resolveAccountByName(sc, provider, configName)
		if err != nil {
			slog.Warn("shell: cross-account config hint lookup failed", "config_name", configName, "provider", provider, "error", err)
		} else if accountId != "" {
			return buildAuthForAccount(nbRequestContext, provider, accountId)
		}
	}

	// Strategy 2: if exactly one account exists for this provider, use it.
	// Multiple accounts → ambiguous, skip.
	accountId, err := resolveSoleAccount(sc, provider)
	if err != nil {
		return nil, fmt.Errorf("shell: cross-account lookup failed: %w", err)
	}
	if accountId == "" {
		return nil, nil // 0 or 2+ accounts
	}

	return buildAuthForAccount(nbRequestContext, provider, accountId)
}

// resolveAccountByName finds a cloud account by its display name or account number within a tenant.
// Matches on account_name first; falls back to account_number so callers can pass either the
// human-readable name or the provider-assigned ID (e.g. a GCP project ID).
func resolveAccountByName(sc *security.SecurityContext, provider, accountName string) (string, error) {
	tenantId := sc.GetTenantId()
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `SELECT id::text FROM cloud_accounts
		 WHERE tenant = $1 AND (account_name = $2 OR account_number = $2) AND lower(cloud_provider) = $3 AND status = 'active'`
	args := []any{tenantId, accountName, provider}

	// Admins can access any account in the tenant.
	// Non-admins are restricted to their authorized accounts.
	if !sc.IsSuperAdmin() && !sc.IsTenantAdmin() {
		query += " AND id = ANY($4)"
		args = append(args, pq.Array(sc.GetAccountIds()))
	}
	query += " LIMIT 1"

	row, err := dbms.QueryRow(query, args...)
	if err != nil {
		return "", err
	}

	var accountId string
	if err := row.Scan(&accountId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Log available names to help diagnose mismatches.
			// Apply the same permission boundary as the primary query.
			debugQuery := `SELECT id::text, account_name, account_number FROM cloud_accounts WHERE tenant = $1 AND lower(cloud_provider) = $2 AND status = 'active'`
			debugArgs := []any{tenantId, provider}
			if !sc.IsSuperAdmin() && !sc.IsTenantAdmin() {
				debugQuery += " AND id = ANY($3)"
				debugArgs = append(debugArgs, pq.Array(sc.GetAccountIds()))
			}
			debugRows, dErr := dbms.Query(debugQuery, debugArgs...)
			if dErr == nil {
				defer func() { _ = debugRows.Close() }()
				type acctRow struct{ id, name, number string }
				var available []acctRow
				for debugRows.Next() {
					var r acctRow
					_ = debugRows.Scan(&r.id, &r.name, &r.number)
					available = append(available, r)
				}
				slog.Warn("shell: resolveAccountByName found no match",
					"provider", provider, "searched_name", accountName, "tenant", tenantId,
					"available_accounts", available)
			}
			return "", nil
		}
		return "", fmt.Errorf("resolveAccountByName: scan failed: %w", err)
	}
	return accountId, nil
}

// resolveSoleAccount returns the account ID if exactly one active account exists
// for the given provider in the tenant. Returns "" if zero or 2+ exist.
func resolveSoleAccount(sc *security.SecurityContext, provider string) (string, error) {
	tenantId := sc.GetTenantId()
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `SELECT id::text FROM cloud_accounts
		 WHERE tenant = $1 AND lower(cloud_provider) = $2 AND status = 'active'`
	args := []any{tenantId, provider}

	// Admins can access any account in the tenant.
	// Non-admins are restricted to their authorized accounts.
	if !sc.IsSuperAdmin() && !sc.IsTenantAdmin() {
		query += " AND id = ANY($3)"
		args = append(args, pq.Array(sc.GetAccountIds()))
	}
	query += " LIMIT 2"

	rows, err := dbms.Query(query, args...)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("shell: failed to close rows", "error", err)
		}
	}()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			slog.Warn("shell: failed to scan cloud account row", "error", err)
			continue
		}
		ids = append(ids, id)
	}

	if len(ids) != 1 {
		if len(ids) > 1 {
			slog.Info("shell: multiple cloud accounts for provider, skipping cross-account auth",
				"provider", provider, "count", len(ids))
		}
		return "", nil
	}
	return ids[0], nil
}

// detectGithubCLI returns true if the command invokes the `gh` CLI as a
// distinct token (i.e. not a substring of an unrelated word like `ghost`).
func detectGithubCLI(command string) bool {
	lowerCmd := strings.ToLower(command)
	if !strings.Contains(lowerCmd, "gh") {
		return false
	}
	tokens, err := shlex.Split(command)
	if err != nil {
		return false
	}
	for _, token := range tokens {
		lowerToken := strings.ToLower(token)
		const kw = "gh"
		if strings.HasPrefix(lowerToken, kw) {
			if len(lowerToken) == len(kw) || !isAlphaNum(lowerToken[len(kw)]) {
				return true
			}
		}
	}
	return false
}

// buildGithubAuthEnv resolves the github tool config available to this tenant
// and returns the env (GITHUB_TOKEN) needed to run `gh` commands. Returns
// (nil, nil) when the command isn't a `gh` invocation or no usable config
// exists. Non-`gh` shell commands incur only the lightweight detect step.
func (m ShellTool) buildGithubAuthEnv(nbRequestContext core.NbToolContext, command string) (*CloudAuthResult, error) {
	if !detectGithubCLI(command) {
		return nil, nil
	}
	if m.AccountId == "" {
		return nil, nil
	}

	githubTool, ok := core.GetNBTool(m.AccountId, ToolExecuteGithubCliCommand)
	if !ok {
		return nil, nil
	}

	configs, err := core.ListToolConfigs(nbRequestContext.Ctx, m.AccountId, githubTool)
	if err != nil {
		return nil, fmt.Errorf("shell: github tool config lookup failed: %w", err)
	}
	if len(configs) == 0 {
		return nil, nil
	}

	// Strategy 1: planner-supplied hint.
	var chosen *core.ToolConfig
	if hint := nbRequestContext.QueryConfig.ToolConfigs[ToolExecuteGithubCliCommand]; hint != "" {
		for i := range configs {
			if configs[i].Name == hint {
				chosen = &configs[i]
				break
			}
		}
	}

	// Strategy 2: sole config in the tenant.
	if chosen == nil {
		if len(configs) != 1 {
			slog.Info("shell: multiple github configs, skipping injection (no hint)",
				"account_id", m.AccountId, "count", len(configs))
			return nil, nil
		}
		chosen = &configs[0]
	}

	return BuildGithubAuth(nbRequestContext.Ctx.GetContext(), *chosen)
}

// buildAuthForAccount builds cloud auth for a specific account ID.
func buildAuthForAccount(nbRequestContext core.NbToolContext, provider, accountId string) (*CloudAuthResult, error) {
	creds, err := GetCloudAccountCredentials(accountId)
	if err != nil {
		return nil, fmt.Errorf("shell: cross-account credentials unavailable for %s: %w", provider, err)
	}

	switch provider {
	case "aws":
		return BuildAwsAuth(nbRequestContext.Ctx.GetContext(), creds)
	case "gcp":
		return BuildGcpAuth(creds)
	case "azure":
		return BuildAzureAuth(creds)
	}
	return nil, nil
}
