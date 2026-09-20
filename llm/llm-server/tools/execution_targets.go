package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// maxListedTargets caps how many other targets the execution-targets block
// names, so a tenant with many clusters does not flood the prompt.
const maxListedTargets = 15

// executionTargetKind describes one config-backed CLI whose target a call can
// choose (see RequestedShellTarget).
type executionTargetKind struct {
	tool      string
	title     string
	usage     string
	showIDs   bool
	usedByAny []string
}

var executionTargetKinds = []executionTargetKind{
	{
		tool:      ToolExecuteKubectlCommand,
		title:     "Kubernetes clusters",
		usage:     `kubectl_execute and resource_search_execute: pass cluster="<name>". shell_execute: kubectl --context <name> .... kubectl config does not list these clusters.`,
		usedByAny: []string{ToolExecuteKubectlCommand, ToolResourceSearch, core.ToolExecuteShellCommand},
	},
	{
		tool:      ToolExecuteAwsCliCommand,
		title:     "AWS accounts",
		usage:     `aws_execute: pass account="<name>". aws in shell_execute always uses the current account.`,
		showIDs:   true,
		usedByAny: []string{ToolExecuteAwsCliCommand},
	},
}

// executionTargets returns the conversation's current target for a tool (""
// when none is chosen yet) and the other targets the caller can read.
func executionTargets(ctx *security.RequestContext, accountID string, saved map[string]string, toolName string) (core.ToolConfig, []core.ToolConfig, error) {
	tool, ok := core.GetNBTool(accountID, toolName)
	if !ok {
		return core.ToolConfig{}, nil, nil
	}
	current := core.ToolConfig{}
	if name := saved[toolName]; name != "" {
		current.Name = name
	} else if narrowed, err := core.ListToolConfigs(ctx, accountID, tool); err == nil && len(narrowed) == 1 {
		current = narrowed[0]
	}
	all, err := TenantAccountLister(ctx, tool)
	if err != nil {
		return current, nil, fmt.Errorf("execution targets for %s: %w", toolName, err)
	}
	var others []core.ToolConfig
	for _, c := range all {
		if strings.EqualFold(c.Name, current.Name) {
			continue
		}
		if !ctx.GetSecurityContext().HasAccountAccess(KubernetesTargetAccountID(c), security.SecurityAccessTypeRead) {
			continue
		}
		others = append(others, c)
	}
	return current, others, nil
}

// RenderExecutionTargets tells the planner which clusters and cloud accounts it
// can run commands against, for the tools it has. Per-user and per-request, so
// it belongs in the human message. Empty when no tool has another target.
func RenderExecutionTargets(ctx *security.RequestContext, accountID string, saved map[string]string, toolNames []string) string {
	if ctx == nil || ctx.GetSecurityContext() == nil || accountID == "" {
		return ""
	}
	available := map[string]bool{}
	for _, name := range toolNames {
		available[strings.ToLower(name)] = true
	}
	var sections []string
	for _, kind := range executionTargetKinds {
		used := false
		for _, name := range kind.usedByAny {
			used = used || available[name]
		}
		if !used {
			continue
		}
		current, others, err := executionTargets(ctx, accountID, saved, kind.tool)
		if err != nil {
			ctx.GetLogger().Warn("execution targets: lookup failed", "tool", kind.tool, "error", err)
			continue
		}
		if len(others) == 0 {
			continue
		}
		currentName := current.Name
		if currentName == "" {
			currentName = "none selected yet"
		}
		names := make([]string, 0, len(others))
		for _, c := range others {
			label := c.Name
			if number := targetAccountNumber(c); kind.showIDs && number != "" {
				label += " (" + number + ")"
			}
			names = append(names, label)
		}
		sortNames(names)
		if len(names) > maxListedTargets {
			names = append(names[:maxListedTargets], fmt.Sprintf("and %d more", len(names)-maxListedTargets))
		}
		sections = append(sections, fmt.Sprintf("%s: current %s; others you can use: %s.\n%s",
			kind.title, currentName, strings.Join(names, ", "), kind.usage))
	}
	if len(sections) == 0 {
		return ""
	}
	return "<execution_targets>\nCommands run on the current target unless a call names another one.\n" +
		strings.Join(sections, "\n") + "\n</execution_targets>"
}

// kubectlUsesConfigSubcommand reports whether any kubectl invocation in a
// command (including shell pipelines) runs `kubectl config ...`.
func kubectlUsesConfigSubcommand(command string) bool {
	for _, words := range shellSegments(command) {
		if len(words) == 0 || filepath.Base(words[0]) != "kubectl" {
			continue
		}
		rest := words[1:]
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			flag := rest[0]
			rest = rest[1:]
			if !strings.Contains(flag, "=") && kubectlGlobalFlagsWithValue[flag] && len(rest) > 0 {
				rest = rest[1:]
			}
		}
		if len(rest) > 0 && rest[0] == "config" {
			return true
		}
	}
	return false
}

// shellSegments splits a command into the words of each simple command,
// breaking at unquoted |, ;, & and newlines. Quotes are removed from words.
func shellSegments(command string) [][]string {
	var segments [][]string
	var words []string
	var word strings.Builder
	inWord := false
	var quote byte
	flushWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	flushSegment := func() {
		flushWord()
		if len(words) > 0 {
			segments = append(segments, words)
			words = nil
		}
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == '\\' && i+1 < len(command):
			i++
			word.WriteByte(command[i])
			inWord = true
		case c == '|' || c == ';' || c == '&' || c == '\n':
			flushSegment()
		case isShellBlank(c):
			flushWord()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	flushSegment()
	return segments
}

// kubeconfigNotAvailable explains that kubeconfig commands do not apply and
// how to pick a cluster instead, naming the clusters the caller can use.
func kubeconfigNotAvailable(ctx core.NbToolContext) error {
	current, others, err := executionTargets(ctx.Ctx, ctx.AccountId, ctx.QueryConfig.ToolConfigs, ToolExecuteKubectlCommand)
	currentName := current.Name
	if currentName == "" {
		currentName = "none selected yet"
	}
	names := make([]string, 0, len(others))
	for _, c := range others {
		names = append(names, c.Name)
	}
	sortNames(names)
	otherText := "none"
	if err == nil && len(names) > 0 {
		otherText = strings.Join(names, ", ")
	}
	return fmt.Errorf(`kubectl config is not available: Nubi does not use kubeconfig contexts, so it would show nothing useful. Current cluster: %s. Other clusters you can use: %s. To run a command on another cluster, pass cluster="<name>" to kubectl_execute, or add --context <name> in shell_execute`, currentName, otherText)
}
