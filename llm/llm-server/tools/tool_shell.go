package tools

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/shlex"
)

var wm = workspace.NewWorkspaceManager()

// shellEmptyCommandRejectionMsg is a defensive check inside Call() for
// direct-invocation callers (tests, custom invokers). The planner-level
// path is handled by the generic missing-fields hint in
// executor_planner.go:validateToolInput, which already includes the
// universal "populate the field or drop this action" guidance that
// applies to every tool — no per-tool nuance is needed here.
const shellEmptyCommandRejectionMsg = "shell_execute called with empty 'command'. Populate 'command' or drop this action from your batch."

func init() {
	core.RegisterNBToolFactory(core.ToolExecuteShellCommand, func(accountId string) (core.NBTool, error) {
		return ShellTool{AccountId: accountId}, nil
	})
}

func isAlphaNum(c uint8) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

type ShellTool struct {
	AccountId        string
	workspaceManager workspace.WorkspaceManager
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

	**Working directory:** Each call runs in a per-conversation directory. Relative paths (` + "`cat foo.json`, `ls`, `kubectl get pods > pods.json`" + `) read and write there. Tools that save large output (` + "`logs_*.txt`, `metrics_*.json`, `traces_*.json`" + `) put their files in this same directory, so the same relative paths read those artifacts back. Run ` + "`ls -la`" + ` first when you resume a conversation or want to see what earlier tool calls left behind — re-running the upstream query is wasteful. An optional ` + "`work_dir`" + ` field sets the working directory for this call (equivalent to a leading ` + "`cd`" + `).

	**Persistence:** Files at relative paths persist across turns within this conversation. Files at absolute ` + "`/tmp/...`" + ` paths are shared with other conversations on the same account — do NOT write secrets, credentials, or per-chat state there. Treat absolute ` + "`/tmp/`" + ` as system scratch only.

	**Stateless shell:** Each call is a fresh ` + "`sh -c`" + `, so ` + "`cd`" + ` and environment variables do NOT persist. Files do (they live on disk). Put environment setup and the command that consumes it in the same call (` + "`export FOO=bar && command`" + `), or pass the value through the CLI's explicit flags. Shell profile files are not sourced automatically.

	**Credentials handled automatically:** Infrastructure CLIs receive credentials through their configured execution boundary; ` + "`GITHUB_TOKEN`" + ` and ` + "`GITLAB_TOKEN`" + ` are injected when their CLIs are invoked. You do NOT need to plan an ` + "`aws configure`, `gcloud auth`, `gh auth login`, or `glab auth login`" + ` step.

	**Large Data Redirection:** Identify large output by command intent (log dumps, metric time-series, multi-resource JSON, or queries expecting >50 records). Redirect stdout to a relative workspace file (` + "`command > data.json`" + `). Then inspect using ` + "`jq`, `grep`, `awk`, or `head` (`jq .key data.json | head -n 50`)" + ` to keep context usage bounded.

	**Collection and analysis:** Invoke infrastructure CLIs directly in shell commands; use local tools to analyze the saved output. Prefer jq for JSON selection and aggregation, and grep/head for bounded text inspection. Use Python when the analysis needs more complex parsing or joins; keep it focused on local files, with standard-library modules. Reuse existing captures when they cover the requested scope and time range.

	**Python quoting:** For multiline Python, use a quoted heredoc (python3 - <<'PY', followed by the script and a closing PY on its own line). Avoid packing multiline logic or nested shell quotes into python3 -c. Pass file paths as arguments instead of interpolating them into source code.

	**Preserve failures:** Check collection succeeded before analyzing its output. Keep stderr visible; avoid blanket error suppression or appending || true to every command. A partial or failed collection must not be presented as an empty successful result.

	**Execution target:** ` + "`kubectl`" + ` runs on the conversation's current cluster. Add ` + "`--context <cluster-name>`" + ` to run a command on another connected cluster (one cluster per command). ` + "`aws`" + ` runs on the current AWS account; add ` + "`--profile <account-name>`" + ` to use another connected account. For ` + "`gcloud`/`gsutil`/`bq`/`az`" + ` commands, pass the connected Nudgebee account name in shell_execute's ` + "`account`" + ` field; provider-native project/subscription flags do not select a Nudgebee connection. Unknown names return the available targets.

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
			"account": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional connected GCP or Azure account name for a shell command owned by gcloud, gsutil, bq, or az. Do not use this for local-only commands.",
			},
		},
		Required: []string{"command"},
	}
}

// resolveShellTarget picks the target for a shell command owned by a
// config-backed CLI: the cluster kubectl --context names when present,
// otherwise the conversation's saved target.
func (m ShellTool) resolveShellTarget(nbRequestContext core.NbToolContext, owner, command string, args map[string]any) (core.ToolConfig, error) {
	_, requested, err := RequestedShellTargetFromCall(core.NBToolCallRequest{Command: command, Arguments: args})
	if err != nil {
		return core.ToolConfig{}, err
	}
	if requested == "" {
		return ResolveShellTarget(nbRequestContext, owner)
	}
	// The saved target may be unresolvable (no selection yet, several
	// configs); only use it to short-circuit a request that names it.
	saved, _ := ResolveShellTarget(nbRequestContext, owner)
	return ResolveRequestedTarget(nbRequestContext.Ctx, nbRequestContext.AccountId, owner, saved, requested)
}

func (m ShellTool) PrepareDelegatedTargetInput(target *core.ExecutionTarget, input string) (string, error) {
	if target == nil {
		return input, nil
	}
	owner, requested, err := RequestedShellTargetFromInput(input)
	if errors.Is(err, ErrAmbiguousShellTarget) {
		// A script's aws invocations without --profile still take the default.
		err = nil
	}
	if err != nil || requested != "" || !delegatedTargetMatchesShellOwner(target.Provider, owner) {
		return input, err
	}
	field := "account"
	if target.Provider == "kubernetes" {
		field = "cluster"
	}
	return addDelegatedTargetArgument(input, field, target.Name)
}

func (m ShellTool) ValidateWriteTargetInput(input string) error {
	req := core.ParseNBToolCallRequestFromInput(input)
	owner, _, err := RequestedShellTargetFromCall(req)
	if err != nil {
		return err
	}
	if owner == "" || strings.ContainsAny(req.Command, "|;&\n$()`") {
		return fmt.Errorf("shell: write target is ambiguous; run one remote CLI command per approved call")
	}
	return nil
}

// ConfirmationKey scopes a shell kubectl write approval to the cluster the
// command names, so approving a write on one cluster never covers another.
func (m ShellTool) ConfirmationKey(toolInput string) string {
	_, requested, err := RequestedShellTargetFromInput(toolInput)
	if err != nil {
		return m.Name() + "@" + toolInput
	}
	return targetConfirmationKey(m.Name(), requested)
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
			Data:   shellEmptyCommandRejectionMsg,
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
	// downstream wrapping (work_dir prefix,
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

	// Handle optional work_dir (sanitized and escaped to prevent command injection)
	if wd, ok := input.Arguments["work_dir"].(string); ok && wd != "" {
		sanitizedWd := common.SanitizePath(wd)
		if sanitizedWd != "" {
			command = fmt.Sprintf("cd %s && %s", common.ShellEscape(sanitizedWd), command)
		}
	}

	// Prepare per-command routing metadata. Cloud credentials are resolved only
	// after the shim reaches the server-side cloud execution boundary.
	env := map[string]string{}
	// This is scoped to the one shell execution, not the shared workspace or
	// conversation. If a script invokes a CLI that the shell owner detector
	// cannot see, its shim must still not fall back to the origin account.
	commandOwner, concreteRequested, targetErr := RequestedShellTargetFromCall(input)
	if errors.Is(targetErr, ErrAmbiguousShellTarget) {
		// Reads such as region loops run as before: the shim routes each aws
		// invocation by its own --profile, else the default. Writes were already
		// refused before approval by the planner's shell write-target check.
		targetErr = nil
	}
	if targetErr != nil {
		return core.NBToolResponse{Data: targetErr.Error(), Status: core.NBToolResponseStatusError}, targetErr
	}
	if target := nbRequestContext.QueryConfig.DelegatedTarget; target != nil && (commandOwner == "" || delegatedTargetMatchesShellOwner(target.Provider, commandOwner)) {
		env[workspace.ENV_NB_TOOL_CONFIG_NAME] = target.Name
	}
	if selected := nbRequestContext.QueryConfig.ToolConfigs[commandOwner]; env[workspace.ENV_NB_TOOL_CONFIG_NAME] == "" && commandOwner != "" && selected != "" {
		// The shim treats this as the default for a command that does not name
		// its own --context/--profile. The saved conversation default applies
		// only when this child has no separate delegated target.
		env[workspace.ENV_NB_TOOL_CONFIG_NAME] = selected
	}
	requested := requestedCloudAccountFromCall(input)
	if bound := nbRequestContext.QueryConfig.DelegatedTarget; bound != nil && bound.Provider == "aws" && commandOwner == ToolExecuteAwsCliCommand && requested == "" {
		requested = concreteRequested // Preserve an explicit --profile over this child's default.
	}
	if requested == "" {
		if bound := nbRequestContext.QueryConfig.DelegatedTarget; bound != nil &&
			(commandOwner == ToolExecuteAwsCliCommand || commandOwner == ToolExecuteGcpCliCommand || commandOwner == ToolExecuteAzureCliCommand) &&
			delegatedTargetMatchesShellOwner(bound.Provider, commandOwner) {
			requested = bound.Name
		}
	}
	if commandOwner == ToolExecuteKubectlCommand || commandOwner == ToolExecuteHelmCommand {
		if cluster, ok := input.Arguments["cluster"].(string); ok && cluster != "" {
			requested = cluster
		}
	}
	if requested != "" && commandOwner != "" {
		saved, _ := ResolveShellTarget(nbRequestContext, commandOwner)
		target, err := ResolveRequestedTarget(nbRequestContext.Ctx, nbRequestContext.AccountId, commandOwner, saved, requested)
		if err != nil {
			return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
		}
		if bound := nbRequestContext.QueryConfig.DelegatedTarget; bound != nil && delegatedTargetMatchesShellOwner(bound.Provider, commandOwner) {
			if err := VerifyDelegatedTargetIdentity(nbRequestContext, bound.Provider, target, concreteRequested); err != nil {
				return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
			}
		}
		env[workspace.ENV_NB_TOOL_CONFIG_NAME] = target.Name
		env[workspace.ENV_NB_TARGET_ACCOUNT_ID] = TargetAccountID(target)
	}
	if commandOwner == ToolExecuteKubectlCommand {
		// Keep the existing target-bound credential path for writes that were
		// visible to the planner and passed its confirmation gate. Reads and
		// kubeconfig discovery are resolved from concrete argv by the shim.
		if directShellKubectlType(originalCommand) != core.ToolRequestTypeRead && !kubectlUsesConfigSubcommand(originalCommand) {
			selected, err := m.resolveShellTarget(nbRequestContext, ToolExecuteKubectlCommand, originalCommand, input.Arguments)
			if err != nil {
				return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
			}
			if err := VerifyDelegatedTargetIdentity(nbRequestContext, "kubernetes", selected, concreteRequested); err != nil {
				return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
			}
			targetEnv, err := KubernetesTargetEnv(nbRequestContext, selected)
			if err != nil {
				return core.NBToolResponse{}, err
			}
			for k, v := range targetEnv {
				env[k] = v
			}
		}
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

	// Same shape for `glab`: GITLAB_TOKEN, plus GITLAB_HOST when the tenant
	// runs a self-hosted GitLab.
	if glAuth, err := m.buildGitlabAuthEnv(nbRequestContext, command); err != nil {
		slog.Warn("shell: gitlab auth injection skipped", "account_id", m.AccountId, "error", err)
	} else if glAuth != nil {
		for k, v := range glAuth.Env {
			env[k] = v
		}
	}

	manager := m.workspaceManager
	if manager == nil {
		manager = wm
	}
	response, err := manager.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, env)

	// Scrub any sensitive credential values from the output to prevent accidental
	// exposure (e.g. if the LLM runs "env" or "printenv" on a cloud account).
	if len(env) > 0 {
		response = ScrubCredentials(response, env)
	}

	if err != nil {
		// grep/find/jq/etc exit with status 1 when they ran successfully but found
		// no matches. That is normal Unix semantics, not a tool failure — surface
		// it as success-with-no-matches so the LLM doesn't retry the same command.
		//
		// Exception: a piped command whose FIRST segment fails on a missing
		// dynamic-workspace file (e.g. `grep A logs_kubectl_<nano>.txt | grep B`)
		// also exits 1 here — the tail segment "succeeds" with zero matches
		// because it never received real input, silently masking the file-not-
		// found error as a benign no-match. Route that case to the error path
		// below instead, so the dynamic-file hint in shellErrorHint actually
		// fires (this is the dominant real-world shape of the fetch_logs
		// file_ref race — Call A/Call B are piped by design, see agent_log.go).
		if isNoMatchExit(err, originalCommand) && !looksLikeMissingDynamicFile(response) {
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

// directShellKubectlType classifies the kubectl argv portion of a direct shell
// command without treating local redirection as part of the Kubernetes verb.
// Opaque, chained, and pipelined shapes remain unknown and therefore fail
// secure into the target-bound token path.
func directShellKubectlType(command string) core.ToolRequestType {
	if hasShellGateControlSyntax(command) || len(splitShellPipeline(command)) > 1 {
		return ""
	}
	words, err := shlex.Split(command)
	if err != nil || len(words) == 0 || filepath.Base(words[0]) != "kubectl" {
		return ""
	}
	argv := make([]string, 0, len(words))
	for _, word := range words {
		switch word {
		case "|", "||", "&", "&&", ";", ">", ">>", "<":
			return InferKubectlVerbType(strings.Join(argv, " "))
		default:
			argv = append(argv, word)
		}
	}
	return InferKubectlVerbType(strings.Join(argv, " "))
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
	return wrapCliError(rawError, shellErrorHint(rawError, originalCommand))
}

// dynamicWorkspaceFilePattern matches the names tools and the system write into
// the per-conversation workspace: fetch_logs / metrics / traces tool output
// (logs_/metrics_/traces_…) and offloaded investigation artifacts written by the
// event-analyzer and planner guardrails (evidence_logs_/event_labels_/
// event_investigation_context_/event_<id>_evidence/event_<id>_full_event/
// investigation_query_…). Their exact names are
// assigned at write time — a unix timestamp for tool output, an event/message
// UUID for offloaded artifacts — so a missing file of this shape is almost always
// a guessed or reconstructed name, not a stale cross-conversation file. The model
// should `ls` the workspace rather than invent filenames.
//
// The label/suffix segment is intentionally permissive ([a-zA-Z0-9._-]+, any
// length, no fixed timestamp width): in production the model invents names with
// short unix-second timestamps, hyphens, and pod-name segments that a strict
// <unix-nano> pattern missed entirely, which let a piped `grep <guess> | head`
// be reclassified as a benign no-match instead of surfacing the file-not-found
// hint (see looksLikeMissingDynamicFile / shellErrorHint).
var dynamicWorkspaceFilePattern = regexp.MustCompile(`\b(?:logs|metrics|traces|evidence_logs|event|investigation_query)_[a-zA-Z0-9._-]+\.(?:txt|json)\b`)

// looksLikeMissingDynamicFile reports whether response carries a "no such
// file or directory" signal for a dynamic-workspace-shaped filename. In a
// piped command, an upstream segment failing this way still lets the tail
// segment "succeed" with zero matches (empty input, no match), so the
// overall exit code alone can't distinguish "genuinely no matches" from
// "the file never existed" — this checks the captured output text instead.
func looksLikeMissingDynamicFile(response string) bool {
	return strings.Contains(strings.ToLower(response), "no such file or directory") && dynamicWorkspaceFilePattern.MatchString(response)
}

func shellErrorHint(rawError, originalCommand string) string {
	lower := strings.ToLower(rawError)
	switch {
	case strings.Contains(lower, "unterminated quoted string"),
		strings.Contains(lower, "syntax error: unterminated"):
		return "Your command has unbalanced quotes. Common cause: nested escaping (\\\" inside a JSON-in-shell value). Try simpler quoting, write the payload to a /tmp/ file with cat <<'EOF', or split the command into two steps."
	case strings.Contains(lower, "no such file or directory") && firstTokenIsFileReader(originalCommand):
		// One generic hint for every missing file-read: the advice is always
		// "list the directory, do not guess". We deliberately do NOT branch on the
		// filename shape here — the fix for a missing file is the same whether it
		// was a reconstructed name, a system-offloaded artifact, a tool-output
		// race, or a cross-conversation file. (dynamicWorkspaceFilePattern is only
		// used for the harder classification job in looksLikeMissingDynamicFile,
		// where combined stdout+stderr is genuinely ambiguous.)
		return "File not found. Run `ls -la` first to see what is actually in the workspace before grepping/catting — do NOT guess or reconstruct filenames. Files written by tools or the system (fetch_logs/metrics/traces output, or offloaded evidence/query/label files) are named at write time; if you requested a producing tool in this SAME batch, wait for it to finish and use its file_ref in a later iteration. The workspace is per-account and persists across turns in this conversation, but files created in a different conversation will NOT exist here."
	case strings.Contains(lower, "command not found"),
		looksLikeShellNotFound(rawError):
		return "Command not found in the workspace pod. Available CLIs include kubectl, aws, gcloud, az, gh, glab, helm, jq, curl, python3. If a specialized *_execute tool exists for this CLI (kubectl_execute, aws_execute, gcloud_execute, azure_execute, github_execute, gitlab_execute), prefer that tool."
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
	{keywords: []string{"kubectl"}, provider: "k8s", toolName: ToolExecuteKubectlCommand},
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

// detectCLIToken returns true if the command invokes `kw` as a distinct token
// (i.e. not a substring of an unrelated word like `ghost` for `gh`).
func detectCLIToken(command, kw string) bool {
	lowerCmd := strings.ToLower(command)
	if !strings.Contains(lowerCmd, kw) {
		return false
	}
	tokens, err := shlex.Split(command)
	if err != nil {
		return false
	}
	for _, token := range tokens {
		lowerToken := strings.ToLower(token)
		if strings.HasPrefix(lowerToken, kw) {
			if len(lowerToken) == len(kw) || !isAlphaNum(lowerToken[len(kw)]) {
				return true
			}
		}
	}
	return false
}

// detectGithubCLI returns true if the command invokes the `gh` CLI as a
// distinct token (i.e. not a substring of an unrelated word like `ghost`).
func detectGithubCLI(command string) bool { return detectCLIToken(command, "gh") }

// detectGitlabCLI returns true if the command invokes the `glab` CLI as a
// distinct token. No overlap with detectGithubCLI: `glab` does not start with
// `gh`, so a glab command never trips the github matcher and vice versa.
func detectGitlabCLI(command string) bool { return detectCLIToken(command, "glab") }

// resolveScmToolConfig picks the tool config to use for credential injection:
// the planner-supplied hint when it names one, otherwise the tenant's sole
// config. Returns (nil, nil) when nothing usable exists or the choice would be
// ambiguous — injection is skipped rather than guessed at.
func (m ShellTool) resolveScmToolConfig(nbRequestContext core.NbToolContext, toolName string) (*core.ToolConfig, error) {
	if m.AccountId == "" {
		return nil, nil
	}

	scmTool, ok := core.GetNBTool(m.AccountId, toolName)
	if !ok {
		return nil, nil
	}

	configs, err := core.ListToolConfigs(nbRequestContext.Ctx, m.AccountId, scmTool)
	if err != nil {
		return nil, fmt.Errorf("shell: %s tool config lookup failed: %w", toolName, err)
	}
	if len(configs) == 0 {
		return nil, nil
	}

	// Strategy 1: planner-supplied hint.
	if hint := nbRequestContext.QueryConfig.ToolConfigs[toolName]; hint != "" {
		for i := range configs {
			if configs[i].Name == hint {
				return &configs[i], nil
			}
		}
	}

	// Strategy 2: sole config in the tenant.
	if len(configs) != 1 {
		slog.Info("shell: multiple scm configs, skipping injection (no hint)",
			"account_id", m.AccountId, "tool", toolName, "count", len(configs))
		return nil, nil
	}
	return &configs[0], nil
}

// buildGithubAuthEnv resolves the github tool config available to this tenant
// and returns the env (GITHUB_TOKEN) needed to run `gh` commands. Returns
// (nil, nil) when the command isn't a `gh` invocation or no usable config
// exists. Non-`gh` shell commands incur only the lightweight detect step.
func (m ShellTool) buildGithubAuthEnv(nbRequestContext core.NbToolContext, command string) (*CloudAuthResult, error) {
	if !detectGithubCLI(command) {
		return nil, nil
	}

	chosen, err := m.resolveScmToolConfig(nbRequestContext, ToolExecuteGithubCliCommand)
	if err != nil || chosen == nil {
		return nil, err
	}

	return BuildGithubAuth(nbRequestContext.Ctx.GetContext(), *chosen)
}

// buildGitlabAuthEnv resolves the gitlab tool config available to this tenant
// and returns the env (GITLAB_TOKEN, plus GITLAB_HOST for self-hosted) needed to
// run `glab` commands. Returns (nil, nil) when the command isn't a `glab`
// invocation or no usable config exists. Non-`glab` shell commands incur only
// the lightweight detect step.
func (m ShellTool) buildGitlabAuthEnv(nbRequestContext core.NbToolContext, command string) (*CloudAuthResult, error) {
	if !detectGitlabCLI(command) {
		return nil, nil
	}

	chosen, err := m.resolveScmToolConfig(nbRequestContext, ToolExecuteGitlabCliCommand)
	if err != nil || chosen == nil {
		return nil, err
	}

	return BuildGitlabAuth(*chosen)
}

// successResponseNoMatchesOnCluster is successResponseNoMatches for a kubectl
// call that ran on a cluster other than the conversation's saved one.
func successResponseNoMatchesOnCluster(nbRequestContext core.NbToolContext, stdout, cluster string) (core.NBToolResponse, error) {
	resp, err := successResponseNoMatches(nbRequestContext, stdout)
	if err != nil || !strings.HasPrefix(resp.Data, "{") {
		return resp, err
	}
	payload := map[string]any{}
	if common.UnmarshalJson([]byte(resp.Data), &payload) != nil {
		return resp, nil
	}
	payload["cluster"] = cluster
	if body, mErr := common.MarshalJson(payload); mErr == nil {
		resp.Data = string(body)
	}
	return resp, nil
}
