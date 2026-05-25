package core

import (
	"strconv"
	"strings"
)

// ParsePromptToNBAgentPrompt parses a structured markdown prompt file into NBAgentPrompt.
//
// Standard prompt file format:
//
//	## Role
//	You are a {role description}.
//
//	## Instructions
//	{instruction paragraph 1}
//
//	{instruction paragraph 2}
//
//	## Constraints
//	{constraint 1}
//
//	## Tool Usage
//	### {tool_name}
//	{usage line 1}
//	{usage line 2}
//
//	## Output Format
//	{format description}
//
//	## Examples
//	**Question:** {question}
//	**Answer:** {answer}
//	---
//	**Question:** {question}
//	**Answer Steps:**
//	1. Tool: {tool}
//	   Input: {input}
//	---
//
//	## RAG Configuration
//	- Module: {module}
//	- Format: json
//	- QuestionKey: {key}
//	- AnswerKey: {key}
//	- ExplanationKey: {key}
//	- Records: {n}
//
//	## Schema
//	{schema paragraph 1}
//
// If no ## sections are found (unstructured content), the entire text is
// treated as Instructions (paragraphs separated by blank lines).
func ParsePromptToNBAgentPrompt(text string) NBAgentPrompt {
	prompt := NBAgentPrompt{}
	sections := splitIntoSections(text)

	if len(sections) == 0 {
		// Unstructured file — treat whole content as Instructions
		prompt.Instructions = parseParagraphs(text)
		return prompt
	}

	for _, s := range sections {
		switch normalizeHeader(s.header) {
		case "role":
			prompt.Role = parseRole(s.content)
		case "instructions", "primary directive":
			prompt.Instructions = append(prompt.Instructions, parseParagraphs(s.content)...)
		case "constraints":
			prompt.Constraints = parseParagraphs(s.content)
		case "tool usage":
			prompt.ToolUsage = parseToolUsage(s.content)
		case "output format":
			prompt.OutputFormat = strings.TrimSpace(s.content)
		case "examples":
			prompt.Examples = parseExamples(s.content)
		case "rag configuration":
			prompt.Rag = parseRagConfig(s.content)
		case "schema", "database schema":
			prompt.Schema = parseParagraphs(s.content)
		}
		// Unknown sections (e.g. "## Primary Directive" already handled above) are ignored.
	}

	return prompt
}

// --- internal types and helpers ---

type promptSection struct {
	header  string
	content string
}

// splitIntoSections splits text by lines starting with "## ".
// Returns nil if no sections are found.
func splitIntoSections(text string) []promptSection {
	var sections []promptSection
	var current *promptSection

	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			if current != nil {
				sections = append(sections, *current)
			}
			current = &promptSection{header: strings.TrimPrefix(line, "## ")}
			continue
		}
		if current != nil {
			current.content += line + "\n"
		}
		// Lines before the first ## section are ignored (e.g. the # Title line)
	}
	if current != nil {
		sections = append(sections, *current)
	}
	return sections
}

// normalizeHeader lowercases and trims a section header for switch matching.
func normalizeHeader(h string) string {
	return strings.ToLower(strings.TrimSpace(h))
}

// parseRole extracts the role string.
// If the content starts with "You are ", strips the prefix so that
// GetPromptTemplate can prepend it in its standard way.
// Trailing period is also stripped to avoid "You are expert.." double punctuation.
func parseRole(content string) string {
	role := strings.TrimSpace(content)
	role = strings.TrimPrefix(role, "You are ")
	role = strings.TrimPrefix(role, "you are ")
	role = strings.TrimSuffix(role, ".")
	return strings.TrimSpace(role)
}

// parseParagraphs splits content into non-empty paragraphs (blocks separated
// by one or more blank lines). Each paragraph becomes one item in the slice.
func parseParagraphs(content string) []string {
	var result []string
	for _, para := range strings.Split(content, "\n\n") {
		para = strings.TrimSpace(para)
		if para != "" {
			result = append(result, para)
		}
	}
	return result
}

// parseToolUsage splits content by "### {tool_name}" sub-sections.
// Returns a map of tool name → list of non-empty lines.
func parseToolUsage(content string) map[string][]string {
	result := map[string][]string{}
	var currentTool string

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "### ") {
			currentTool = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if currentTool != "" {
				// Initialize entry so the name is registered even with no description lines
				if _, exists := result[currentTool]; !exists {
					result[currentTool] = []string{}
				}
			}
			continue
		}
		if currentTool == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			result[currentTool] = append(result[currentTool], trimmed)
		}
	}
	return result
}

// parseExamples parses Q&A blocks separated by "---" lines.
func parseExamples(content string) []NBAgentPromptExample {
	var examples []NBAgentPromptExample

	for _, block := range strings.Split(content, "---") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		ex := parseExampleBlock(block)
		if ex.Question != "" {
			examples = append(examples, ex)
		}
	}
	return examples
}

func parseExampleBlock(block string) NBAgentPromptExample {
	var ex NBAgentPromptExample

	const (
		stateNone   = 0
		stateAnswer = 1
		stateSteps  = 2
		stateInput  = 3
	)

	state := stateNone
	var currentStep NBAgentPromptExampleAnswerStep
	var buf strings.Builder

	flushBuf := func() string {
		s := strings.TrimSpace(buf.String())
		buf.Reset()
		return s
	}

	// commitStep appends the current step if it has any content and resets it.
	commitStep := func() {
		if state == stateInput {
			currentStep.Input = flushBuf()
		}
		if currentStep.Tool != "" || currentStep.Input != "" || currentStep.Explanation != "" {
			ex.AnswerSteps = append(ex.AnswerSteps, currentStep)
			currentStep = NBAgentPromptExampleAnswerStep{}
		}
	}

	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "**Question:**"):
			if state == stateAnswer {
				ex.Answer = flushBuf()
			}
			commitStep()
			state = stateNone
			ex.Question = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Question:**"))

		case strings.HasPrefix(trimmed, "**Answer:**"):
			if state == stateAnswer {
				ex.Answer = flushBuf()
			}
			commitStep()
			state = stateAnswer
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "**Answer:**")); rest != "" {
				buf.WriteString(rest)
			}

		case strings.HasPrefix(trimmed, "**Answer Steps:**"):
			if state == stateAnswer {
				ex.Answer = flushBuf()
			}
			commitStep()
			state = stateSteps

		case strings.HasPrefix(trimmed, "**Explanation:**"):
			if state == stateAnswer {
				ex.Answer = flushBuf()
			}
			commitStep()
			state = stateNone
			ex.Explanation = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Explanation:**"))

		case state == stateAnswer:
			// Accumulate multi-line answer content; skip blank lines.
			if trimmed != "" {
				if buf.Len() > 0 {
					buf.WriteString("\n")
				}
				buf.WriteString(trimmed)
			}

		case (state == stateSteps || state == stateInput) && containsToolPrefix(trimmed):
			commitStep()
			state = stateSteps
			currentStep.Tool = extractAfterColon(trimmed, "Tool:")

		case (state == stateSteps || state == stateInput) && strings.HasPrefix(trimmed, "Input:"):
			// Finalise ongoing input accumulation.
			if state == stateInput {
				currentStep.Input = flushBuf()
			}
			// A new Input: always starts a new step if the previous one has content.
			if currentStep.Input != "" {
				ex.AnswerSteps = append(ex.AnswerSteps, currentStep)
				currentStep = NBAgentPromptExampleAnswerStep{}
			}
			state = stateInput
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "Input:")); rest != "" {
				buf.WriteString(rest)
			}

		case state == stateInput:
			// Accumulate multi-line input content; skip blank lines.
			if trimmed != "" {
				if buf.Len() > 0 {
					buf.WriteString("\n")
				}
				buf.WriteString(trimmed)
			}

		case (state == stateSteps || state == stateInput) && strings.HasPrefix(trimmed, "Explanation:"):
			// Per-step explanation (no ** prefix).
			if state == stateInput {
				currentStep.Input = flushBuf()
			}
			state = stateSteps
			currentStep.Explanation = strings.TrimSpace(strings.TrimPrefix(trimmed, "Explanation:"))
		}
	}

	// Flush any remaining buffered content.
	if state == stateAnswer {
		ex.Answer = flushBuf()
	}
	commitStep()

	return ex
}

// containsToolPrefix returns true if the line looks like "Tool:" or "1. Tool:" etc.
func containsToolPrefix(s string) bool {
	// Strip optional numbering like "1. " or "- "
	s = strings.TrimLeft(s, "0123456789")
	s = strings.TrimPrefix(s, ".")
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "Tool:")
}

// extractAfterColon extracts the value after "Tool:" in a string, handling numbering prefix.
func extractAfterColon(s, prefix string) string {
	// Find the prefix regardless of leading numbering
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(s[idx+len(prefix):])
}

// parseRagConfig parses "- Key: value" lines into NBAgentPromptRag.
func parseRagConfig(content string) NBAgentPromptRag {
	var rag NBAgentPromptRag
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch strings.ToLower(key) {
		case "module":
			rag.Module = val
		case "format":
			rag.Format = NBAgentPromptRagFormat(val)
		case "questionkey":
			rag.QuestionKey = val
		case "answerkey":
			rag.AnswerKey = val
		case "explanationkey":
			rag.ExplanationKey = val
		case "records":
			if n, err := strconv.Atoi(val); err == nil {
				rag.Records = n
			}
		}
	}
	return rag
}
