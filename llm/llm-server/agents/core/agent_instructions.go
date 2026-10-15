package core

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// Text an agent always gets (#40899) is the skills linked to it with "always
// load" set. The executor resolves them once per agent run into
// NBAgentRequest.AgentInstructions, and every planner renders that one field —
// ReAct planners in the <additional_agent_prompt> system slot, custom planners
// through AgentInstructionsBlock / CombinedAccountPrompt. Per-agent extension
// text ("Add Prompt and Tools") is no longer read: it predates skills and was
// never used outside test data; extensions now carry tools only.
//
// The field is never forwarded to a sub-agent: each agent resolves its own,
// for its own account. Delegators (metrics, traces, …) pass their name down in
// InheritSkillsFromAgents and run the provider agent for the queried account,
// so the provider gets the parent's skills and its own, once each.

var listAlwaysLoadSkillsFn = toolcore.ListAlwaysLoadSkills

// resolveAgentInstructions returns the always-on text for an agent and the ids
// of the skills in it in full (a cut skill stays searchable). Inherited (parent) names come first, then the agent's
// own names.
func resolveAgentInstructions(ctx *security.RequestContext, accountId string, ownNames, inheritedNames []string) (string, []string) {
	if ctx == nil || accountId == "" {
		return "", nil
	}
	names := make([]string, 0, len(inheritedNames)+len(ownNames))
	seenName := make(map[string]bool, cap(names))
	for _, list := range [][]string{inheritedNames, ownNames} {
		for _, n := range list {
			if n == "" || n == toolcore.KBAgentWildcard || seenName[n] {
				continue
			}
			seenName[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "", nil
	}

	// Ordered by names, each skill once.
	skills, err := listAlwaysLoadSkillsFn(ctx, accountId, names)
	if err != nil {
		ctx.GetLogger().Warn("agent_instructions: unable to load always-load skills", "error", err)
		return "", nil
	}
	var parts []string
	var ids []string
	for _, s := range skills {
		body := strings.TrimSpace(s.Data)
		if body == "" {
			continue
		}
		if utf8.RuneCountInString(body) > toolcore.AlwaysLoadMaxChars {
			// Saves refuse marking a skill over the cap, so this only happens when
			// the row was changed some other way. Say the text is cut and leave the
			// skill searchable, so the model can still read the rest.
			ctx.GetLogger().Warn("agent_instructions: always-load skill over the size cap, truncating",
				"kb_id", s.ID, "chars", utf8.RuneCountInString(body), "cap", toolcore.AlwaysLoadMaxChars)
			body = body[:runeByteOffset(body, toolcore.AlwaysLoadMaxChars)] +
				fmt.Sprintf("\n[Cut at %d characters. Search the knowledge base for %q to read the rest.]", toolcore.AlwaysLoadMaxChars, s.Name)
		} else {
			ids = append(ids, s.ID)
		}
		parts = append(parts, fmt.Sprintf("<skill name=%q>\n%s\n</skill>", s.Name, body))
	}
	return strings.Join(parts, "\n\n"), ids
}

// runeByteOffset returns the byte offset of the n-th rune of s, or len(s) when
// s has n runes or fewer.
func runeByteOffset(s string, n int) int {
	count := 0
	for i := range s {
		if count == n {
			return i
		}
		count++
	}
	return len(s)
}

// AgentInstructionsBlock wraps the request's always-on text for a custom
// planner's own system message. "" when there is none.
func AgentInstructionsBlock(request NBAgentRequest) string {
	if strings.TrimSpace(request.AgentInstructions) == "" {
		return ""
	}
	return "<additional_agent_prompt>\n" + request.AgentInstructions + "\n</additional_agent_prompt>"
}

// WithAgentInstructions appends the request's always-on text to a custom
// planner's system message. The system message is returned unchanged when
// there is none.
func WithAgentInstructions(request NBAgentRequest, systemMessage string) string {
	block := AgentInstructionsBlock(request)
	if block == "" {
		return systemMessage
	}
	if strings.TrimSpace(systemMessage) == "" {
		return block
	}
	return systemMessage + "\n\n" + block
}
