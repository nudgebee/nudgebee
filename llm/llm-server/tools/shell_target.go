package tools

import (
	"crypto/sha256"
	"fmt"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"sort"
	"strings"
)

// ShellConfigToolName uses the existing detector; script/AST detection is separate work.
func ShellConfigToolName(input string) string {
	_, owner := detectCloudCLI(extractCommandFromToolInput(input))
	return owner
}

// ShellTargetConfigs filters cached tenant inventory for the current caller, on every use.
func ShellTargetConfigs(ctx *security.RequestContext, accountID string, tool core.NBTool) ([]core.ToolConfig, error) {
	configs, err := core.ListToolConfigs(ctx, accountID, tool)
	if err != nil {
		return nil, err
	}
	allowed := make([]core.ToolConfig, 0, len(configs))
	for _, c := range configs {
		for _, v := range c.Values {
			if v.Name == "id" && ctx.GetSecurityContext().HasAccountAccess(v.Value, security.SecurityAccessTypeRead) {
				allowed = append(allowed, c)
				break
			}
		}
	}
	return allowed, nil
}

// ResolveShellTarget never substitutes another account for an invalid saved selection.
func ResolveShellTarget(ctx core.NbToolContext, owner string) (core.ToolConfig, error) {
	tool, ok := core.GetNBTool(ctx.AccountId, owner)
	if !ok {
		return core.ToolConfig{}, fmt.Errorf("shell: target tool %s unavailable", owner)
	}
	configs, err := ShellTargetConfigs(ctx.Ctx, ctx.AccountId, tool)
	if err != nil {
		return core.ToolConfig{}, fmt.Errorf("shell: target lookup: %w", err)
	}
	hint := ctx.QueryConfig.ToolConfigs[owner]
	if hint != "" {
		for _, c := range configs {
			if strings.EqualFold(c.Name, hint) {
				return c, nil
			}
		}
		return core.ToolConfig{}, fmt.Errorf("shell: selected %s configuration is unavailable or unauthorized", owner)
	}
	if len(configs) == 1 {
		return configs[0], nil
	}
	return core.ToolConfig{}, fmt.Errorf("shell: resolve %s account selection before executing this command", owner)
}

// CheckShellTargetWriteAccess keeps source-workspace permissions from authorizing target writes.
func CheckShellTargetWriteAccess(ctx *security.RequestContext, selected core.ToolConfig) error {
	for _, v := range selected.Values {
		if v.Name == "id" && ctx.GetSecurityContext().HasAccountAccess(v.Value, security.SecurityAccessTypeCreate) {
			return nil
		}
	}
	return fmt.Errorf("shell: write access to the selected target account is required")
}

// KubernetesTargetEnv binds the command's execution token to the selected
// target, keeping its workspace account and files unchanged.
func KubernetesTargetEnv(ctx core.NbToolContext, selected core.ToolConfig) (map[string]string, error) {
	for _, v := range selected.Values {
		if v.Name == "id" && v.Value != "" {
			if !ctx.Ctx.GetSecurityContext().HasAccountAccess(v.Value, security.SecurityAccessTypeRead) {
				return nil, fmt.Errorf("target cluster access denied")
			}
			return map[string]string{workspace.ENV_NB_TOOL_CONFIG_NAME: selected.Name, workspace.ENV_NB_TARGET_ACCOUNT_ID: v.Value}, nil
		}
	}
	return nil, fmt.Errorf("selected cluster has no account id")
}

// scanValueFlags finds `<flag> X` / `<flag>=X` flags that stand as their own
// shell word. Text inside quotes and the remote command after a bare `--`
// (e.g. `kubectl exec pod -- sh -c ...`) is never treated as a flag; a shell
// operator (|, ;, &, newline) ends that remote command. It returns the flag
// values and the command with those flags removed. A longer flag that merely
// contains this one (e.g. `--kube-context` for `--context`) does not match.
func scanValueFlags(command, flag string) ([]string, string) {
	var values []string
	var out strings.Builder
	var quote byte
	afterSeparator := false
	n := len(command)
	for i := 0; i < n; {
		c := command[i]
		if quote != 0 {
			out.WriteByte(c)
			if c == '\\' && quote == '"' && i+1 < n {
				out.WriteByte(command[i+1])
				i += 2
				continue
			}
			if c == quote {
				quote = 0
			}
			i++
			continue
		}
		switch c {
		case '\\':
			out.WriteByte(c)
			if i+1 < n {
				out.WriteByte(command[i+1])
			}
			i += 2
			continue
		case '\'', '"':
			quote = c
		case '\n', '|', ';', '&':
			afterSeparator = false
		}
		wordStart := i == 0 || isShellBlank(command[i-1]) || command[i-1] == '\n'
		if wordStart && quote == 0 && !afterSeparator {
			if command[i] == '-' && strings.HasPrefix(command[i:], "--") && (i+2 == n || isShellBlank(command[i+2]) || command[i+2] == '\n') {
				afterSeparator = true
			} else if value, next, ok := matchValueFlag(command, i, flag); ok {
				values = append(values, value)
				i = next
				continue
			}
		}
		out.WriteByte(c)
		i++
	}
	return values, strings.TrimRight(out.String(), " \t")
}

// matchValueFlag reads `flag` starting at i. It returns the value and the
// index just past the flag and any trailing blanks.
func matchValueFlag(command string, i int, flag string) (string, int, bool) {
	if !strings.HasPrefix(command[i:], flag) {
		return "", 0, false
	}
	j := i + len(flag)
	n := len(command)
	switch {
	case j < n && command[j] == '=':
		j++
	case j < n && isShellBlank(command[j]):
		for j < n && isShellBlank(command[j]) {
			j++
		}
	default:
		return "", 0, false
	}
	if j >= n {
		return "", 0, false
	}
	var value string
	if q := command[j]; q == '\'' || q == '"' {
		closing := strings.IndexByte(command[j+1:], q)
		if closing < 0 {
			return "", 0, false
		}
		value = command[j+1 : j+1+closing]
		j += closing + 2
	} else {
		k := j
		for k < n && !isShellBlank(command[k]) && !strings.ContainsRune("\n|;&<>'\"", rune(command[k])) {
			k++
		}
		value = command[j:k]
		j = k
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", 0, false
	}
	for j < n && isShellBlank(command[j]) {
		j++
	}
	return value, j, true
}

func isShellBlank(c byte) bool { return c == ' ' || c == '\t' }

// requestedTarget combines an explicit argument with a selector flag in the
// command. noun names the target in errors ("cluster", "account").
func requestedTarget(command string, args map[string]any, argName, flag, noun string) (string, error) {
	fromArg, _ := args[argName].(string)
	fromArg = strings.TrimSpace(fromArg)
	fromFlag := ""
	values, _ := scanValueFlags(command, flag)
	for _, value := range values {
		if fromFlag != "" && !strings.EqualFold(fromFlag, value) {
			return "", fmt.Errorf("the command names two %ss (%q and %q); run one command per %s", noun, fromFlag, value, noun)
		}
		if fromFlag == "" {
			fromFlag = value
		}
	}
	if fromArg != "" && fromFlag != "" && !strings.EqualFold(fromArg, fromFlag) {
		return "", fmt.Errorf("%s %q and %s %q disagree; pass only one", argName, fromArg, flag, fromFlag)
	}
	if fromArg != "" {
		return fromArg, nil
	}
	return fromFlag, nil
}

// RequestedKubernetesCluster returns the cluster a kubectl call explicitly asks
// for: the `cluster` argument, else a `--context` flag in the command. Empty
// means the call did not ask, so the conversation's saved cluster applies.
func RequestedKubernetesCluster(command string, args map[string]any) (string, error) {
	return requestedTarget(command, args, "cluster", "--context", "cluster")
}

// RequestedAwsAccount returns the AWS account an aws_execute call explicitly
// asks for via its `account` argument. Commands are not parsed for AWS: the
// workspace has no AWS profiles, and shell aws always uses the saved account.
func RequestedAwsAccount(args map[string]any) string {
	account, _ := args["account"].(string)
	return strings.TrimSpace(account)
}

// StripKubectlContextFlag removes `--context` flags. The workspace has no
// kubeconfig contexts; the target travels in the per-call token instead.
func StripKubectlContextFlag(command string) string {
	_, stripped := scanValueFlags(command, "--context")
	return stripped
}

// RequestedShellTarget returns the target a shell command names for the CLI
// that owns it. Only kubectl (--context) can switch targets from a shell
// command; other CLIs always use the saved target.
func RequestedShellTarget(owner, command string) (string, error) {
	if owner == ToolExecuteKubectlCommand {
		return RequestedKubernetesCluster(command, nil)
	}
	return "", nil
}

// RequestedShellTargetFromInput is RequestedShellTarget for a raw
// shell_execute tool input; it also returns the owning tool name.
func RequestedShellTargetFromInput(toolInput string) (string, string, error) {
	owner := ShellConfigToolName(toolInput)
	requested, err := RequestedShellTarget(owner, extractCommandFromToolInput(toolInput))
	return owner, requested, err
}

// TenantAccountLister lists every connected account of a tool's provider in
// the caller's tenant. It is a variable so tests can supply accounts without a
// database.
var TenantAccountLister = core.ListTenantAccountToolConfigs

// targetNoun names what a tool targets, for error messages.
func targetNoun(toolName string) string {
	if toolName == ToolExecuteKubectlCommand {
		return "cluster"
	}
	return "account"
}

// AccountTargetByName returns the connected account (cluster, cloud account)
// for an account-sourced tool that the caller can read, matched by name, or by
// account number when exactly one readable connection has it. Account numbers
// are not unique within a tenant, so an ambiguous number is refused. Errors
// list the usable names so the model can correct itself.
func AccountTargetByName(ctx *security.RequestContext, accountID, toolName, requested string) (core.ToolConfig, error) {
	tool, ok := core.GetNBTool(accountID, toolName)
	if !ok {
		return core.ToolConfig{}, fmt.Errorf("tool %s unavailable", toolName)
	}
	noun := targetNoun(toolName)
	// Not ShellTargetConfigs: that list is narrowed to the conversation's own
	// account when the conversation was opened on one.
	all, err := TenantAccountLister(ctx, tool)
	if err != nil {
		return core.ToolConfig{}, fmt.Errorf("%s lookup: %w", noun, err)
	}
	names := make([]string, 0, len(all))
	var byNumber []core.ToolConfig
	for _, c := range all {
		if !ctx.GetSecurityContext().HasAccountAccess(KubernetesTargetAccountID(c), security.SecurityAccessTypeRead) {
			continue
		}
		if strings.EqualFold(c.Name, requested) {
			return c, nil
		}
		if number := targetAccountNumber(c); number != "" && strings.EqualFold(number, requested) {
			byNumber = append(byNumber, c)
		}
		names = append(names, c.Name)
	}
	switch len(byNumber) {
	case 1:
		return byNumber[0], nil
	case 0:
		sortNames(names)
		return core.ToolConfig{}, fmt.Errorf("%s %q is not connected or you do not have access to it; available %ss: %s", noun, requested, noun, strings.Join(names, ", "))
	default:
		matches := make([]string, 0, len(byNumber))
		for _, c := range byNumber {
			matches = append(matches, c.Name)
		}
		sortNames(matches)
		return core.ToolConfig{}, fmt.Errorf("%s number %q matches several connections (%s); pass the connection name instead", noun, requested, strings.Join(matches, ", "))
	}
}

// KubernetesTargetByName is AccountTargetByName for kubectl clusters.
func KubernetesTargetByName(ctx *security.RequestContext, accountID, name string) (core.ToolConfig, error) {
	targets, err := ListKubernetesTargets(ctx, accountID)
	if err != nil {
		return core.ToolConfig{}, err
	}
	aliases := KubernetesTargetAliases(targets)
	// Prefer persisted names so an alias can never shadow a real connection.
	for _, target := range aliases {
		if strings.EqualFold(target.Config.Name, name) {
			return target.Config, nil
		}
	}
	for _, target := range aliases {
		if strings.EqualFold(target.Name, name) {
			return target.Config, nil
		}
	}
	var byNumber []core.ToolConfig
	for _, target := range aliases {
		if number := targetAccountNumber(target.Config); number != "" && strings.EqualFold(number, name) {
			byNumber = append(byNumber, target.Config)
		}
	}
	if len(byNumber) == 1 {
		return byNumber[0], nil
	}
	if len(byNumber) > 1 {
		matches := make([]string, 0, len(byNumber))
		for _, target := range byNumber {
			matches = append(matches, target.Name)
		}
		sortNames(matches)
		return core.ToolConfig{}, fmt.Errorf("cluster number %q matches several connections (%s); pass the connection name instead", name, strings.Join(matches, ", "))
	}
	names := make([]string, 0, len(aliases))
	for _, target := range aliases {
		names = append(names, target.Name)
	}
	return core.ToolConfig{}, fmt.Errorf("cluster %q is not connected or you do not have access to it; available clusters: %s", name, strings.Join(names, ", "))
}

// KubernetesTargetAlias pairs the shell-safe virtual kubeconfig name with the
// persisted integration config it resolves to.
type KubernetesTargetAlias struct {
	Name   string
	Config core.ToolConfig
}

// KubernetesTargetAliases exposes connection names as tokens that can be
// copied into unquoted shell commands. Persisted integration names are never
// mutated; colliding aliases receive a stable account-bound suffix.
func KubernetesTargetAliases(targets []core.ToolConfig) []KubernetesTargetAlias {
	baseCounts := make(map[string]int, len(targets))
	bases := make([]string, len(targets))
	for i, target := range targets {
		bases[i] = shellSafeKubernetesContext(target.Name)
		baseCounts[strings.ToLower(bases[i])]++
	}

	aliases := make([]KubernetesTargetAlias, 0, len(targets))
	used := make(map[string]struct{}, len(targets))
	for i, target := range targets {
		name := bases[i]
		if baseCounts[strings.ToLower(name)] > 1 {
			sum := sha256.Sum256([]byte(target.Name + "\x00" + KubernetesTargetAccountID(target)))
			name = fmt.Sprintf("%s-%x", name, sum[:4])
		}
		baseName := name
		for suffix := 2; ; suffix++ {
			key := strings.ToLower(name)
			if _, exists := used[key]; !exists {
				used[key] = struct{}{}
				break
			}
			name = fmt.Sprintf("%s-%d", baseName, suffix)
		}
		aliases = append(aliases, KubernetesTargetAlias{Name: name, Config: target})
	}
	return aliases
}

func shellSafeKubernetesContext(name string) string {
	var b strings.Builder
	separator := false
	lastHyphen := false
	for _, r := range name {
		allowed := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if allowed {
			if separator && b.Len() > 0 && !lastHyphen && r != '-' {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			lastHyphen = r == '-'
			separator = false
			continue
		}
		separator = true
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		result = "context"
	}
	if result[0] == '.' {
		result = "context-" + result
	}
	return result
}

// ListKubernetesTargets returns every connected cluster the current caller may
// read. It powers the workspace shim's virtual kubeconfig discovery commands.
func ListKubernetesTargets(ctx *security.RequestContext, accountID string) ([]core.ToolConfig, error) {
	tool, ok := core.GetNBTool(accountID, ToolExecuteKubectlCommand)
	if !ok {
		return nil, fmt.Errorf("kubectl unavailable")
	}
	all, err := TenantAccountLister(ctx, tool)
	if err != nil {
		return nil, err
	}
	readable := make([]core.ToolConfig, 0, len(all))
	for _, candidate := range all {
		if ctx.GetSecurityContext().HasAccountAccess(KubernetesTargetAccountID(candidate), security.SecurityAccessTypeRead) {
			readable = append(readable, candidate)
		}
	}
	sort.Slice(readable, func(i, j int) bool {
		return strings.ToLower(readable[i].Name) < strings.ToLower(readable[j].Name)
	})
	return readable, nil
}

// ResolveRequestedTarget picks the target for one call: the conversation's
// current config when nothing is requested or the request names it (by name
// or account number), otherwise the requested connection. It never changes
// the conversation's saved selection.
func ResolveRequestedTarget(ctx *security.RequestContext, accountID, toolName string, current core.ToolConfig, requested string) (core.ToolConfig, error) {
	if requested == "" || current.Name != "" && (strings.EqualFold(requested, current.Name) || strings.EqualFold(requested, targetAccountNumber(current))) {
		return current, nil
	}
	return AccountTargetByName(ctx, accountID, toolName, requested)
}

// ResolveKubernetesTarget is ResolveRequestedTarget for kubectl clusters.
func ResolveKubernetesTarget(ctx core.NbToolContext, current core.ToolConfig, requested string) (core.ToolConfig, error) {
	if requested == "" || current.Name != "" && (strings.EqualFold(requested, current.Name) || strings.EqualFold(requested, targetAccountNumber(current))) {
		return current, nil
	}
	return KubernetesTargetByName(ctx.Ctx, ctx.AccountId, requested)
}

// KubernetesTargetAccountID returns the cloud account id behind a config.
func KubernetesTargetAccountID(selected core.ToolConfig) string {
	for _, v := range selected.Values {
		if v.Name == "id" {
			return v.Value
		}
	}
	return ""
}

// sortNames orders names case-insensitively, as a person would read them.
func sortNames(names []string) {
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
}

func targetAccountNumber(selected core.ToolConfig) string {
	for _, v := range selected.Values {
		if v.Name == "account_number" {
			return v.Value
		}
	}
	return ""
}

// targetConfirmationKey scopes a write approval to the target the call
// explicitly names, so approving a write on one cluster or account never
// covers another. Calls without an explicit target keep the per-tool key ("").
func targetConfirmationKey(toolName, requested string) string {
	if requested == "" {
		return ""
	}
	return toolName + "@" + strings.ToLower(requested)
}
