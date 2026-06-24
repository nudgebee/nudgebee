package core

import "strings"

// AgentCapabilities carries the per-request capability constraints forwarded
// from callers (runbook-server, UI) into the agent execution pipeline.
//
// Both lists are matched case-insensitively against tool names and aliases.
// When both are non-empty, DisabledTools wins on conflict.
//
//   - DisabledTools: deny-list — listed tools are removed from the agent's set.
//   - AllowedTools:  allow-list — when non-empty, only listed tools are kept
//     (overrides default tool injection such as shell_execute and load_skills).
type AgentCapabilities struct {
	AllowedTools  []string `json:"allowed_tools,omitempty"`
	DisabledTools []string `json:"disabled_tools,omitempty"`
}

// IsEmpty reports whether no capability constraints are set.
func (c AgentCapabilities) IsEmpty() bool {
	return len(c.AllowedTools) == 0 && len(c.DisabledTools) == 0
}

// HasAllowedTools reports whether a non-empty allow-list is in effect.
func (c AgentCapabilities) HasAllowedTools() bool {
	return len(c.AllowedTools) > 0
}

// Merge returns a new AgentCapabilities that combines c and other.
// c's non-empty fields take precedence; other fills in the zero values.
func (c AgentCapabilities) Merge(other AgentCapabilities) AgentCapabilities {
	result := c
	if len(result.AllowedTools) == 0 {
		result.AllowedTools = other.AllowedTools
	}
	if len(result.DisabledTools) == 0 {
		result.DisabledTools = other.DisabledTools
	}
	return result
}

// NormalizeList trims whitespace and drops empty entries from a string slice.
func NormalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
