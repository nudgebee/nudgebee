package tools

import (
	"fmt"
	"nudgebee/llm/tools/core"
	"regexp"
	"strings"
)

func init() {
	core.RegisterNBToolFactory(MermaidValidationToolName, func(accountId string) (core.NBTool, error) {
		return MermaidValidationTool{}, nil
	})
}

const MermaidValidationToolName = "mermaid_validation"

type MermaidValidationTool struct{}

func (m MermaidValidationTool) Name() string {
	return MermaidValidationToolName
}

func (m MermaidValidationTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m MermaidValidationTool) Description() string {
	return "Validates Mermaid.js diagram syntax and provides feedback on common errors."
}

func (m MermaidValidationTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"code": {
				Type:        core.ToolSchemaTypeString,
				Description: "The Mermaid.js code to validate.",
			},
		},
		Required: []string{"code"},
	}
}

func (m MermaidValidationTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	code, ok := input.Arguments["code"].(string)
	if !ok {
		// Fallback if passed differently, though usually args are strict
		return core.NBToolResponse{}, fmt.Errorf("missing or invalid 'code' argument")
	}

	code = cleanMermaidCode(code)
	errors := validateMermaidCode(code)

	if len(errors) > 0 {
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusSuccess, // Return success so the agent can read the errors in Data
			Data:   fmt.Sprintf("Invalid Mermaid code:\n- %s", strings.Join(errors, "\n- ")),
			Type:   core.NBToolResponseTypeText,
		}, nil
	}

	return core.NBToolResponse{
		Status: core.NBToolResponseStatusSuccess,
		Data:   "Valid Mermaid code.",
		Type:   core.NBToolResponseTypeText,
	}, nil
}

func cleanMermaidCode(code string) string {
	// Remove markdown code blocks if present
	code = strings.TrimSpace(code)
	if after, ok := strings.CutPrefix(code, "```mermaid"); ok {
		code = after
	} else if after0, ok0 := strings.CutPrefix(code, "```"); ok0 {
		code = after0
	}
	code = strings.TrimSuffix(code, "```")
	return strings.TrimSpace(code)
}

func validateMermaidCode(code string) []string {
	// Normalize line endings to ensure consistent splitting
	code = strings.ReplaceAll(code, "\r\n", "\n")
	code = strings.ReplaceAll(code, "\r", "\n")

	var errors []string
	lines := strings.Split(code, "\n")

	// Detect Diagram Type
	var diagramType string
	inFrontMatter := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if strings.HasPrefix(trimmed, "---") {
			inFrontMatter = !inFrontMatter
			continue
		}
		if inFrontMatter {
			continue
		}

		// First valid line is the type
		// Take the first word or the whole line to check prefix
		diagramType = trimmed
		break
	}

	// Determine Validation Rules
	// Node label check is specifically for 'graph' and 'flowchart' which use ID[Label] syntax
	shouldCheckNodes := strings.HasPrefix(diagramType, "graph") || strings.HasPrefix(diagramType, "flowchart")

	// XYChart check
	shouldCheckXyChartArrays := strings.Contains(diagramType, "xychart")

	// Pie chart check
	shouldCheckPie := strings.HasPrefix(diagramType, "pie")

	// Regex for single percent comments (invalid)
	singlePercentRegex := regexp.MustCompile(`^\s*%[^%]`)

	// Regex to find potential node definitions that are NOT quoted.
	// We mask valid quoted strings first, then look for:
	// ID + one or more opening brackets + a character that is NOT a quote and NOT another opener.
	// This detects scenarios like A[Start], A(Start), A([Start]) where 'Start' isn't quoted.
	// Excludes cases where the opener is immediately followed by another opener (nested brackets).
	nodeRegex := regexp.MustCompile(`\b\w+\s*([\[\(\{\>]+)\s*([^"\[\(\{\>])`)

	// Regex to mask Quoted strings: "..."
	quoteRegex := regexp.MustCompile(`"[^"]*"`)

	// Regex for subgraph parsing
	subgraphLineRegex := regexp.MustCompile(`^\s*subgraph\s+(.+)$`)
	subgraphIDLabelRegex := regexp.MustCompile(`^\w+\s*\[".+"\]$`)

	// xychart regex
	xyChartArrayStartRegex := regexp.MustCompile(`^\s*(x-axis|y-axis|bar|line)\b.*\[`)

	// Pie chart negative value regex: looks for : followed by optional whitespace and a minus sign
	pieNegativeValueRegex := regexp.MustCompile(`:\s*-\d+`)

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check 1: Invalid comments
		if singlePercentRegex.MatchString(line) {
			errors = append(errors, fmt.Sprintf("Line %d: Use double percent (%%%%) for comments, single percent is invalid.", lineNum))
		}

		// Check 2: Subgraph titles (Check BEFORE masking, as we need to see the quotes)
		if subMatches := subgraphLineRegex.FindStringSubmatch(trimmed); len(subMatches) > 0 {
			content := strings.TrimSpace(subMatches[1])
			// If it starts with quote, it's likely "Title" -> Valid
			if strings.HasPrefix(content, "\"") {
				// Valid
			} else if subgraphIDLabelRegex.MatchString(content) {
				// If it matches ID ["Title"] -> Valid
			} else {
				// Otherwise assume invalid unquoted title or ID used as title
				errors = append(errors, fmt.Sprintf("Line %d: Subgraph title '%s' should be enclosed in double quotes.", lineNum, content))
			}
		}

		// Check 3: Nodes
		// Only run this check for graph/flowchart types to avoid false positives on Class/ER diagrams
		if shouldCheckNodes {
			// Strip comments first to avoid false positives (e.g. invalid chars in comments)
			checkLine := line
			if idx := strings.Index(checkLine, "%%"); idx != -1 {
				checkLine = checkLine[:idx]
			}

			// Mask quoted strings to avoid false positives inside labels
			maskedLine := quoteRegex.ReplaceAllString(checkLine, "\"\"")

			matches := nodeRegex.FindAllStringSubmatch(maskedLine, -1)
			for range matches {
				errors = append(errors, fmt.Sprintf("Line %d: Node label must be enclosed in double quotes (e.g., [\"Label\"]).", lineNum))
			}
		}

		// Check 4: xychart single-line arrays and numeric types
		if shouldCheckXyChartArrays {
			if xyChartArrayStartRegex.MatchString(trimmed) {
				// Single line check
				if !strings.Contains(trimmed, "]") {
					errors = append(errors, fmt.Sprintf("Line %d: xychart array definitions for x-axis, y-axis, bar, or line must be on a single line.", lineNum))
				} else {
					// Numeric check for bar/line (only if single line check passed)
					// bar and line arrays must be numeric. x-axis can be strings.
					if strings.HasPrefix(trimmed, "bar") || strings.HasPrefix(trimmed, "line") {
						start := strings.Index(trimmed, "[")
						end := strings.LastIndex(trimmed, "]")
						if start != -1 && end != -1 && end > start {
							content := trimmed[start+1 : end]
							if strings.Contains(content, "\"") || strings.Contains(content, "'") {
								errors = append(errors, fmt.Sprintf("Line %d: xychart 'bar' and 'line' arrays must contain numeric values only (no quotes). Example: bar [10, 20]", lineNum))
							}
							if strings.Contains(content, "null") {
								errors = append(errors, fmt.Sprintf("Line %d: xychart does not support 'null' values. Please use 0 or numeric values instead.", lineNum))
							}
						}
					}
				}
			}
		}

		// Check 5: Pie chart negative values
		if shouldCheckPie {
			if pieNegativeValueRegex.MatchString(trimmed) {
				errors = append(errors, fmt.Sprintf("Line %d: Pie charts cannot contain negative values. Use absolute values or exclude this data point.", lineNum))
			}
		}
	}

	return errors
}
