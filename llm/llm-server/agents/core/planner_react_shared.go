package core

import (
	"errors"
	"fmt"
	"strings"

	"nudgebee/llm/common"
	toolcore "nudgebee/llm/tools/core"
)

// This file holds symbols shared across ReAct-family planners. They originally
// lived in planner_react_2.go alongside the (now-deleted) NBReActPlanner2; they
// were extracted here when that legacy planner was removed so the ReAct3 planner
// (planner_react_3.go) and agents that implement these interfaces keep compiling.

func generateToolId(tool string, input string) string {
	// Normalize input for hashing: remove whitespace and use lower case
	normalized := strings.ToLower(strings.TrimSpace(input))
	hash := common.HashString(normalized)
	// Return a deterministic but unique-ish ID (first 8 chars of hash)
	return fmt.Sprintf("%s-%s", strings.ToLower(tool), hash[:8])
}

type NBAgentReActPlannerCritiqueSupport interface {
	CritiqueEnabled() bool
}

type NBAgentReActPlannerSummaryToolProvider interface {
	GetSummaryToolName() string
}

// RetryConfig holds the configuration for the retry mechanism.
type RetryConfig struct {
	MaxRetries int
	Prompts    []string
}

// ErrParseFailure indicates the LLM response could not be parsed into a
// valid action or final answer and is eligible for retrying with a
// reformat prompt.
var ErrParseFailure = errors.New("unable to parse LLM response: no action, final answer, or clarification")

func reActPromptToolNames(tools []toolcore.NBTool) string {
	var tn strings.Builder
	for i, tool := range tools {
		if i > 0 {
			tn.WriteString(", ")
		}
		tn.WriteString(tool.Name())
	}

	return tn.String()
}
