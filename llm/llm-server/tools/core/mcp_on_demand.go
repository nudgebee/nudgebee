package core

import (
	"fmt"
	"sort"
	"strings"
)

// MCPIntegrationsPromptBlock is the orchestrator prompt's line naming the
// account's MCP integrations, built from its MCP tool list, for when the
// orchestrator does not declare their tools. Names are sorted and
// deduplicated, so the same integrations always give the same bytes (the line
// sits in the cached system prompt). Empty when there are none.
func MCPIntegrationsPromptBlock(tools []NBTool) string {
	reachable := map[string]bool{}
	unreachable := map[string]bool{}
	for _, t := range tools {
		switch v := t.(type) {
		case mcpIntegrationTool:
			reachable[v.config.IntegrationName] = true
		case mcpUnavailableTool:
			unreachable[v.integrationName] = true
		}
	}
	for name := range reachable {
		delete(unreachable, name)
	}
	if len(reachable) == 0 && len(unreachable) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("MCP integrations connected to this account: ")
	if len(reachable) == 0 {
		b.WriteString("none reachable")
	} else {
		b.WriteString(quotedSortedNames(reachable))
	}
	if len(unreachable) > 0 {
		fmt.Fprintf(&b, " (unreachable right now: %s — tell the user these cannot be used instead of trying their tools)", quotedSortedNames(unreachable))
	}
	b.WriteString(". Their tools are not in your tool list. Find them with search_tools, by integration name or by what you need, and run them through delegate_agent.")
	return b.String()
}

func quotedSortedNames(set map[string]bool) string {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		names[i] = "`" + n + "`"
	}
	return strings.Join(names, ", ")
}

// MCPToolParts splits an MCP integration tool's description into its
// integration's name and instructions and the tool's own description, for
// search_tools to print the instructions once per integration. ok is false for
// any other tool.
func MCPToolParts(t NBTool) (integration, instructions, description string, ok bool) {
	m, ok := t.(mcpIntegrationTool)
	if !ok {
		return "", "", "", false
	}
	return m.config.IntegrationName, strings.TrimSpace(m.config.LLMInstructions), m.toolDesc, true
}
