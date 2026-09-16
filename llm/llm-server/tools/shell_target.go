package tools

import (
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

// KubernetesTargetEnv binds the shim's authority to the selected target, keeping
// its workspace account and files unchanged.
func KubernetesTargetEnv(ctx core.NbToolContext, selected core.ToolConfig) (map[string]string, error) {
	for _, v := range selected.Values {
		if v.Name == "id" && v.Value != "" {
			token, err := workspace.KubernetesTargetToken(ctx.Ctx, ctx.AccountId, v.Value)
			if err != nil {
				return nil, err
			}
			return map[string]string{workspace.ENV_NB_TOOL_CONFIG_NAME: selected.Name, workspace.ENV_NB_WORKSPACE_TOKEN: token}, nil
		}
	}
	return nil, fmt.Errorf("selected cluster has no account id")
}

// scanKubectlContextFlags finds `--context X` / `--context=X` flags that stand
// as their own shell word. Text inside quotes and the remote command after a
// bare `--` (e.g. `kubectl exec pod -- sh -c ...`) is never treated as a flag;
// a shell operator (|, ;, &, newline) ends that remote command. It returns the
// flag values and the command with those flags removed. `--kube-context`
// (helm) does not match.
func scanKubectlContextFlags(command string) ([]string, string) {
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
			} else if value, next, ok := matchContextFlag(command, i); ok {
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

// matchContextFlag reads a `--context` flag starting at i. It returns the
// value and the index just past the flag and any trailing blanks.
func matchContextFlag(command string, i int) (string, int, bool) {
	const flag = "--context"
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

// RequestedKubernetesCluster returns the cluster a kubectl call explicitly asks
// for: the `cluster` argument, else a `--context` flag in the command. Empty
// means the call did not ask, so the conversation's saved cluster applies.
func RequestedKubernetesCluster(command string, args map[string]any) (string, error) {
	fromArg, _ := args["cluster"].(string)
	fromArg = strings.TrimSpace(fromArg)
	fromFlag := ""
	values, _ := scanKubectlContextFlags(command)
	for _, value := range values {
		if fromFlag != "" && !strings.EqualFold(fromFlag, value) {
			return "", fmt.Errorf("the command names two clusters (%q and %q); run one command per cluster", fromFlag, value)
		}
		if fromFlag == "" {
			fromFlag = value
		}
	}
	if fromArg != "" && fromFlag != "" && !strings.EqualFold(fromArg, fromFlag) {
		return "", fmt.Errorf("cluster %q and --context %q disagree; pass only one", fromArg, fromFlag)
	}
	if fromArg != "" {
		return fromArg, nil
	}
	return fromFlag, nil
}

// RequestedShellKubernetesCluster is RequestedKubernetesCluster for a raw
// shell_execute tool input. Non-kubectl shell commands never request a cluster.
func RequestedShellKubernetesCluster(toolInput string) (string, error) {
	if ShellConfigToolName(toolInput) != ToolExecuteKubectlCommand {
		return "", nil
	}
	return RequestedKubernetesCluster(extractCommandFromToolInput(toolInput), nil)
}

// StripKubectlContextFlag removes `--context` flags. The workspace has no
// kubeconfig contexts; the target travels in the per-call token instead.
func StripKubectlContextFlag(command string) string {
	_, stripped := scanKubectlContextFlags(command)
	return stripped
}

// TenantClusterLister lists every connected cluster in the caller's tenant.
// It is a variable so tests can supply clusters without a database.
var TenantClusterLister = core.ListTenantAccountToolConfigs

// KubernetesTargetByName returns the connected cluster with this name, limited
// to clusters the caller can read. The error lists the usable names so the
// model can correct itself.
func KubernetesTargetByName(ctx *security.RequestContext, accountID, name string) (core.ToolConfig, error) {
	tool, ok := core.GetNBTool(accountID, ToolExecuteKubectlCommand)
	if !ok {
		return core.ToolConfig{}, fmt.Errorf("kubectl: tool %s unavailable", ToolExecuteKubectlCommand)
	}
	// Not ShellTargetConfigs: that list is narrowed to the conversation's own
	// cluster when the conversation was opened on one.
	all, err := TenantClusterLister(ctx, tool)
	if err != nil {
		return core.ToolConfig{}, fmt.Errorf("kubectl: cluster lookup: %w", err)
	}
	names := make([]string, 0, len(all))
	for _, c := range all {
		if !ctx.GetSecurityContext().HasAccountAccess(KubernetesTargetAccountID(c), security.SecurityAccessTypeRead) {
			continue
		}
		if strings.EqualFold(c.Name, name) {
			return c, nil
		}
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return core.ToolConfig{}, fmt.Errorf("cluster %q is not connected or you do not have access to it; available clusters: %s", name, strings.Join(names, ", "))
}

// ResolveKubernetesTarget picks the cluster for one kubectl call: the
// explicitly requested one when given, otherwise the conversation's current
// cluster. It never changes the conversation's saved selection.
func ResolveKubernetesTarget(ctx core.NbToolContext, current core.ToolConfig, requested string) (core.ToolConfig, error) {
	if requested == "" || strings.EqualFold(requested, current.Name) {
		return current, nil
	}
	return KubernetesTargetByName(ctx.Ctx, ctx.AccountId, requested)
}

// KubernetesTargetAccountID returns the cloud account id behind a cluster config.
func KubernetesTargetAccountID(selected core.ToolConfig) string {
	for _, v := range selected.Values {
		if v.Name == "id" {
			return v.Value
		}
	}
	return ""
}

// kubernetesConfirmationKey scopes a write approval to the cluster the call
// explicitly targets, so approving a write on one cluster never covers another.
// Calls without an explicit cluster keep the historical per-tool key ("").
func kubernetesConfirmationKey(toolName, requested string) string {
	if requested == "" {
		return ""
	}
	return toolName + "@" + strings.ToLower(requested)
}
