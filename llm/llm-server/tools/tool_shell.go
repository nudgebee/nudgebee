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
	return `Executes a shell command in an Alpine Linux workspace and returns the command output.

	**Available CLIs:** ` + "`grep`, `awk`, `sed`, `jq`, `find`, `xargs`, `curl`, `tar`, `unzip`" + `, plus ` + "`kubectl`, `helm`, `psql`, `mysql`, `redis-cli`, `argocd`, `clickhouse-client`, `rabbitmqadmin`, `sqlcmd`, `sqlplus`, `ssh`, `aws`, `gcloud`, `az`, `gh`, `python3`" + `. Pipes (` + "`|`" + `), redirection (` + "`>`, `>>`, `<`" + `), command substitution (` + "`$()`" + `), env vars all work.

	**Working directory:** Each call runs in a per-conversation directory. Relative paths (` + "`cat foo.json`, `ls`, `kubectl get pods > pods.json`" + `) read and write there. Tools that save large output (` + "`logs_*.txt`, `metrics_*.json`, `traces_*.json`" + `) put their files in this same directory, so the same relative paths read those artifacts back. Run ` + "`ls -la`" + ` first when you resume a conversation or want to see what earlier tool calls left behind — re-running the upstream query is wasteful.

	**Persistence:** Files at relative paths persist across turns within this conversation. Files at absolute ` + "`/tmp/...`" + ` paths are shared with other conversations on the same account — do NOT write secrets, credentials, or per-chat state there. Treat absolute ` + "`/tmp/`" + ` as system scratch only.

	**Stateless shell:** Each call is a fresh ` + "`sh -c`" + `, so ` + "`cd`" + ` and unexported variables do NOT persist. Files do (they live on disk). For env vars that must survive across calls, append to ` + "`.nb_profile`" + ` (` + "`echo 'export FOO=bar' >> .nb_profile`" + `).

	**Credentials auto-injected:** AWS / GCP / Azure credentials and ` + "`GITHUB_TOKEN`" + ` are injected automatically when the command invokes the corresponding CLI. You do NOT need to plan an ` + "`aws configure`, `gcloud auth`, or `gh auth login`" + ` step.

	**Empty-match is success.** When grep-family searchers (` + "`grep` / `egrep` / `fgrep` / `rg` / `ack` / `ag`" + `) find no matches, the observation comes back as ` + "`{\"stdout\":\"\",\"no_matches\":true}`" + ` with a Success status — the command ran fine and there is nothing to find. Do NOT retry the same command — either widen the pattern or conclude that no match exists. This semantic also applies when the searcher is the last segment of a pipeline (` + "`kubectl get pods | grep ready`" + `). It does NOT apply to ` + "`find`" + ` (whose exit 1 is a real path / permission error) or plain ` + "`jq`" + ` (which exits 0 and returns ` + "`null`" + ` on missing keys).

	**Error responses.** A real failure comes back as a JSON envelope: ` + "`{\"error_hint\":\"<actionable advice>\",\"original_error\":\"<raw stderr verbatim>\"}`" + `. Three well-known stderr patterns are wrapped: unbalanced quotes, no-such-file on file/text readers (` + "`cat`/`grep`/`jq`" + `), and command-not-found. Read the ` + "`error_hint`" + ` first — it tells you what to fix — then ` + "`original_error`" + ` for the underlying signal. For any other failure mode the response is the raw stderr unchanged.

	**Cleanup:** Only delete files you yourself wrote during this conversation. Never delete tool-saved artifacts (` + "`logs_*`, `metrics_*`, `traces_*`" + `) — they are part of the evidence chain a later step may need.

	**Non-Interactive:** Do NOT run commands that require user input (e.g. ` + "`vim`, `top`, `python` without a script" + `). Use non-interactive flags.

	**Timeout:** Commands have a strict execution time limit.

	**Examples:**
	* ` + "`curl -s https://example.com | grep \"title\"`" + `
	* ` + "`ls -la`" + `
	* ` + "`jq .key data.json`" + `
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
			Data:   "shell_execute requires a non-empty 'command'. If you meant to call a different tool, use that tool name directly instead of shell_execute with an empty input.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("empty command")
	}
	if looksLikeJSONEnvelope(command) {
		return core.NBToolResponse{
			Data:   "shell_execute received what looks like a JSON tool-input envelope as the command itself. Pass the shell command string in the 'command' field, not the whole tool input as a JSON string.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("command appears to be a JSON envelope")
	}

	// originalCommand snapshots the user-issued command before any
	// downstream wrapping (work_dir prefix, .nb_profile sourcing,
	// cloud-auth env). Captured here while command is still the
	// trimmed, validated user input so first-token-based classification
	// (e.g. grep-exit-1 success reinterpretation) and error-hint
	// matching see the LLM's actual intent, not the wrapper noise.
	originalCommand := command

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
		// grep/find/jq/etc exit with status 1 when they ran successfully but found
		// no matches. That is normal Unix semantics, not a tool failure — surface
		// it as success-with-no-matches so the LLM doesn't retry the same command.
		if isNoMatchExit(err, originalCommand) {
			nbRequestContext.Ctx.GetLogger().Info("shell: reclassifying exit 1 as no-matches",
				"command_preview", cmdLog, "first_token", firstShellToken(strings.ToLower(originalCommand)))
			return successResponseNoMatches(nbRequestContext, response)
		}

		nbRequestContext.Ctx.GetLogger().Error("shell: unable to execute shell command", "error", err.Error(), "command_preview", cmdLog)
		if response == "" {
			response = err.Error()
		}
		return core.NBToolResponse{
			Data:   wrapShellError(ScrubCredentials(response, env), originalCommand),
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

// successResponseNoMatches renders an exit-1-as-empty-match outcome
// using the same JSON shape as a normal success, with an explicit
// no_matches flag so the LLM can distinguish "ran, found nothing"
// from "ran, found content."
func successResponseNoMatches(nbRequestContext core.NbToolContext, stdout string) (core.NBToolResponse, error) {
	payload := map[string]any{
		"stdout":     stdout,
		"no_matches": true,
	}
	body, err := common.MarshalJson(payload)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("shell: unable to marshal no-matches response", "error", err.Error())
		return core.NBToolResponse{
			Data:   stdout,
			Status: core.NBToolResponseStatusSuccess,
			Type:   core.NBToolResponseTypeText,
		}, nil
	}
	return core.NBToolResponse{
		Data:   string(body),
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// noMatchExitCommands lists commands whose exit code 1 means
// "ran successfully but found no matches", not failure. The list is
// deliberately narrow to grep-family searchers:
//
//   - grep / egrep / fgrep / rg / ack / ag — POSIX grep convention:
//     exit 0 = match, exit 1 = no match, exit 2+ = real error.
//
// NOT included, despite a tempting initial intuition:
//
//   - find — exit 1 is ALWAYS a real error (bad path, unreadable
//     directory, permission denied). No-match on find is exit 0.
//     Reclassifying find exit 1 silently masks real errors.
//   - jq — plain `jq '.missing'` returns null with exit 0; jq only
//     exits 1 with `-e` (and that path is uncommon enough that the
//     rule is largely dead code). Real jq errors are exit 2/5.
var noMatchExitCommands = map[string]bool{
	"grep":  true,
	"egrep": true,
	"fgrep": true,
	"ack":   true,
	"rg":    true,
	"ag":    true,
}

// isNoMatchExit returns true when err is a workspace-reported
// "exit status 1" for one of the no-matches-is-fine commands.
// Looks at the LAST pipeline segment (where the exit status of `a | b`
// comes from b), so `kubectl get pods | grep X` with no match is
// reclassified just like a bare `grep X file`.
func isNoMatchExit(err error, originalCommand string) bool {
	if !workspace.IsExitStatus1Failure(err) {
		return false
	}
	tail := lastPipelineSegment(originalCommand)
	first := firstShellToken(strings.ToLower(tail))
	return noMatchExitCommands[first]
}

// lastPipelineSegment returns the substring after the final shell pipe
// (`|`) in command, or command itself if there is no pipe. Distinguishes
// `|` (pipe) from `||` (logical OR), respects basic single- and
// double-quoting so `echo "a|b"` is not split, and respects backslash
// escapes. Subshells (`$(...)`, “ `...` “) and complex word splits are
// NOT parsed — the parser is intentionally minimal because the only
// caller is isNoMatchExit, where a false split just falls through to
// the original "not reclassified" behavior.
func lastPipelineSegment(command string) string {
	last := -1
	inSingle := false
	inDouble := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '\\' && i+1 < len(command):
			i++ // skip the escaped char
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '|' && !inSingle && !inDouble:
			// Skip || (logical OR) — neither half is a pipe segment for our purposes.
			if i+1 < len(command) && command[i+1] == '|' {
				i++
				continue
			}
			last = i
		}
	}
	if last < 0 {
		return command
	}
	return command[last+1:]
}

// looksLikeJSONEnvelope returns true when the command appears to be the
// raw tool-input JSON envelope (e.g. `{"command":"ls"}`) rather than a
// shell command. Three independent guards keep false positives down:
//
//  1. The trimmed input must start with `{`.
//  2. The first non-whitespace token after that `{` must be a quote
//     (`"` or `'`). In any JSON object the first element is a quoted
//     key; in a bash grouped command (`{ echo ... ; }`) it is a
//     command name, which never starts with a quote. This eliminates
//     a class of false positives where the command body itself
//     embeds a quoted `"command":"..."` substring — e.g.
//     `{ echo '{"command":"ls"}'; }`.
//  3. Somewhere in the input, a quoted "command"/'command' key must
//     be followed (after optional whitespace) by `:` — the JSON
//     key-value shape.
//
// Bare unquoted `command` is intentionally not matched either:
// `command` is a bash builtin (`{ command -v jq; }`) and flagging it
// would reject legitimate use.
func looksLikeJSONEnvelope(command string) bool {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	afterBrace := strings.TrimLeft(trimmed[1:], " \t\n\r")
	if !strings.HasPrefix(afterBrace, `"`) && !strings.HasPrefix(afterBrace, `'`) {
		return false
	}
	for _, key := range []string{`"command"`, `'command'`} {
		idx := strings.Index(trimmed, key)
		if idx == -1 {
			continue
		}
		after := strings.TrimLeft(trimmed[idx+len(key):], " \t")
		if strings.HasPrefix(after, ":") {
			return true
		}
	}
	return false
}

// wrapShellError adds a one-line, structured hint above the raw stderr
// for a small set of well-known failure patterns. The original error is
// preserved verbatim under `original_error` so the LLM still sees the
// underlying signal.
func wrapShellError(rawError, originalCommand string) string {
	hint := shellErrorHint(rawError, originalCommand)
	if hint == "" {
		return rawError
	}
	wrapped := map[string]string{
		"error_hint":     hint,
		"original_error": rawError,
	}
	body, err := common.MarshalJson(wrapped)
	if err != nil {
		// Marshal can't realistically fail on a string-only map, but
		// degrade gracefully rather than masking the underlying error.
		return rawError
	}
	return string(body)
}

func shellErrorHint(rawError, originalCommand string) string {
	lower := strings.ToLower(rawError)
	switch {
	case strings.Contains(lower, "unterminated quoted string"),
		strings.Contains(lower, "syntax error: unterminated"):
		return "Your command has unbalanced quotes. Common cause: nested escaping (\\\" inside a JSON-in-shell value). Try simpler quoting, write the payload to a /tmp/ file with cat <<'EOF', or split the command into two steps."
	case strings.Contains(lower, "no such file or directory") && firstTokenIsFileReader(originalCommand):
		return "File not found. The workspace pod is per-account and persists across turns in the same conversation, but files created in a different conversation will NOT exist here. Recreate the file with the upstream command (e.g. `kubectl get ... > /tmp/...`) before grepping/catting it."
	case strings.Contains(lower, "command not found"),
		looksLikeShellNotFound(rawError):
		return "Command not found in the workspace pod. Available CLIs include kubectl, aws, gcloud, az, gh, helm, jq, curl, python3. If a specialized *_execute tool exists for this CLI (kubectl_execute, aws_execute, gcloud_execute, azure_execute, github_execute), prefer that tool."
	}
	return ""
}

// looksLikeShellNotFound returns true for the "<shell>: <cmd>: not found"
// form Alpine ash and POSIX sh emit when a command is missing, while
// rejecting CLI resource-not-found messages that just happen to end with
// `: not found` (helm's `Error: release: not found`, custom script errors
// like `error: user: not found`, etc.). Two anchors: leading shell-prefix
// AND trailing `: not found`. Multi-line stderr is checked line-by-line
// so a command-not-found buried among other output still hits.
func looksLikeShellNotFound(rawError string) bool {
	for _, line := range strings.Split(rawError, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if !strings.HasSuffix(lower, ": not found") {
			continue
		}
		for _, prefix := range []string{
			"sh:", "bash:", "ash:", "zsh:", "ksh:", "dash:",
			"/bin/sh:", "/bin/bash:", "/bin/ash:", "/usr/bin/sh:", "/usr/bin/bash:",
		} {
			if strings.HasPrefix(lower, prefix) {
				return true
			}
		}
	}
	return false
}

// firstTokenIsFileReader returns true when the leading shell token is
// one of the small file/text utilities whose "no such file" stderr
// matches the workspace-lifecycle hint shape.
func firstTokenIsFileReader(originalCommand string) bool {
	switch firstShellToken(strings.ToLower(originalCommand)) {
	case "cat", "grep", "egrep", "fgrep", "sed", "awk", "jq", "head", "tail", "less", "more", "wc":
		return true
	}
	return false
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
//     gcloud_execute → "gcp-dev - my-project-dev"), use that specific account.
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
