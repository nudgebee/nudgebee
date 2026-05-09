package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	toolcore "nudgebee/llm/tools/core"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/prompts"
)

// builds instructions based if tools support multiple subcommands
// specially instructs llms to return json response like `{"command":"", "args":{"arg1":"value1", "arg2": "value2"}}`
func UpdatePromptForMultiCommandTool(query NBAgentRequest, tools []toolcore.NBTool, existingPrompt NBAgentPrompt) (NBAgentPrompt, error) {
	// Start with the existing prompt
	prompt := existingPrompt

	// Ensure ToolUsage map is initialized
	if prompt.ToolUsage == nil {
		prompt.ToolUsage = make(map[string][]string)
	}

	// Define multi-command specific additions
	multiCmdInstruction := "Remember: For tools listed with subcommands, your response *must* be ONLY a JSON object specifying the 'command' and 'args'."
	multiCmdConstraint1 := "When a tool requires a subcommand, you MUST respond *only* with a valid JSON object in the format: `{\"command\": \"<subcommand_name>\", \"args\": {<arguments>}}`."
	multiCmdConstraint2 := "Ensure the arguments provided in the JSON `args` object match the required schema for the chosen subcommand, including required fields."
	multiCmdConstraint3 := "Do not add any explanatory text, comments, or markdown formatting before or after the required JSON output or direct tool input. Your response must be *only* the JSON or the direct input." // Emphasized only JSON/direct input
	multiCmdOutputFormat := "If using a tool with subcommands, respond ONLY with a valid JSON object: `{\"command\": \"<subcommand_name>\", \"args\": {<arguments>}}`. Otherwise, provide the direct input required by the tool."
	defaultOutputFormat := "Provide the direct input required by the tool (string or JSON object)."

	hasMultiCommandTool := false

	for _, tool := range tools {
		toolName := tool.Name()
		// Generate usage details for the current tool
		usage := []string{tool.Description()} // Start with the basic description

		if multiCmdTool, ok := tool.(toolcore.NBMultiCommandTool); ok {
			hasMultiCommandTool = true
			usage = append(usage, "**This tool uses subcommands.** Respond ONLY with JSON: `{\"command\": \"<subcommand_name>\", \"args\": {<arguments>}}`")
			usage = append(usage, "**Available Subcommands:**")

			subCmds, err := multiCmdTool.GetSubCommands()
			if err != nil {
				slog.Error("Error getting subcommands", "tool", toolName, "error", err)
				usage = append(usage, fmt.Sprintf("  - *Error retrieving subcommands: %v*", err))
				// Overwrite ToolUsage for this tool with the generated details (including error)
				prompt.ToolUsage[toolName] = usage
				continue // Move to the next tool
			}

			if len(subCmds) == 0 {
				usage = append(usage, "  - *Warning: No subcommands defined for this multi-command tool.*")
			} else {
				for _, subCmd := range subCmds {
					subCmdName := subCmd.Name
					subCmdDesc := subCmd.Description
					schema := subCmd.InputSchema

					var argsDesc strings.Builder
					fmt.Fprintf(&argsDesc, "  - **%s**: %s", subCmdName, subCmdDesc) // Subcommand name and description

					// Describe arguments
					if len(schema.Properties) > 0 {
						argsDesc.WriteString("\n    - **Arguments:**")
						propNames := lo.Keys(schema.Properties) // Get keys to sort
						slices.Sort(propNames)                  // Sort for consistent order
						for _, argName := range propNames {
							prop := schema.Properties[argName]
							required := ""
							// Check if the argument is required
							for _, req := range schema.Required {
								if req == argName {
									required = " (required)"
									break
								}
							}

							// Format: `arg_name` (type)[ (required)]: description
							fmt.Fprintf(&argsDesc, "\n      - `%s` (%s)%s: %s", argName, prop.Type, required, prop.Description)

							// Add enum values if present
							if len(prop.Enum) > 0 {
								enumStrings := lo.Map(prop.Enum, func(item any, index int) string {
									return fmt.Sprintf("%v", item)
								})
								fmt.Fprintf(&argsDesc, " (Enum: %s)", strings.Join(enumStrings, ", "))
							}
						}
					} else {
						argsDesc.WriteString("\n    - *No arguments required for this subcommand.*")
					}
					usage = append(usage, argsDesc.String())
				}
			}
		} else {
			// Handle standard single-command tools
			schema := tool.InputSchema()
			if len(schema.Properties) > 0 {
				usage = append(usage, "**Input Schema:**")
				propNames := lo.Keys(schema.Properties)
				slices.Sort(propNames)
				for _, argName := range propNames {
					prop := schema.Properties[argName]
					required := ""
					// Check if required
					for _, req := range schema.Required {
						if req == argName {
							required = " (required)"
							break
						}
					}
					usage = append(usage, fmt.Sprintf("  - `%s` (%s)%s: %s", argName, prop.Type, required, prop.Description))

					// Add enum values
					if len(prop.Enum) > 0 {
						enumStrings := lo.Map(prop.Enum, func(item any, index int) string {
							return fmt.Sprintf("%v", item)
						})
						usage = append(usage, fmt.Sprintf(" (Enum: %s)", strings.Join(enumStrings, ", ")))
					}
				}
				// Provide an example input format
				if len(schema.Properties) > 1 || schema.Type == toolcore.ToolSchemaTypeObject {
					exampleArgs := []string{}
					requiredArgs := lo.Map(schema.Required, func(req string, _ int) string {
						propType := "..."
						if prop, exists := schema.Properties[req]; exists {
							propType = fmt.Sprintf("<%s>", prop.Type)
						}
						return fmt.Sprintf("\"%s\": \"%s\"", req, propType)
					})
					exampleArgs = append(exampleArgs, requiredArgs...)

					// Add one optional arg example if no required args shown
					if len(exampleArgs) == 0 && len(schema.Properties) > 0 {
						for argName, prop := range schema.Properties {
							// Ensure it's not already added if it was somehow required but not listed
							isAlreadyAdded := false
							for _, req := range schema.Required {
								if req == argName {
									isAlreadyAdded = true
									break
								}
							}
							if !isAlreadyAdded {
								exampleArgs = append(exampleArgs, fmt.Sprintf("\"%s\": \"<%s>\"", argName, prop.Type))
								break // Just show one optional arg example
							}
						}
					}
					usage = append(usage, fmt.Sprintf("  - *Example Input (JSON object):* `{%s}`", strings.Join(exampleArgs, ", ")))

				} else if len(schema.Required) == 1 {
					prop, exists := schema.Properties[schema.Required[0]]
					if exists && prop.Type == toolcore.ToolSchemaTypeString && schema.Type != toolcore.ToolSchemaTypeObject {
						usage = append(usage, fmt.Sprintf("  - *Example Input (string):* `\"Your %s value\"`", schema.Required[0]))
					} else if exists {
						usage = append(usage, fmt.Sprintf("  - *Example Input:* Provide value for `%s` (%s)", schema.Required[0], prop.Type))
					}
				} else if len(schema.Properties) == 1 && len(schema.Required) == 0 {
					for argName, prop := range schema.Properties {
						if prop.Type == toolcore.ToolSchemaTypeString && schema.Type != toolcore.ToolSchemaTypeObject {
							usage = append(usage, fmt.Sprintf("  - *Example Input (optional string):* `\"Your %s value\"`", argName))
						} else {
							usage = append(usage, fmt.Sprintf("  - *Example Input (optional):* Provide value for `%s` (%s)", argName, prop.Type))
						}
						break
					}
				}

			}
		}
		// Overwrite ToolUsage for this tool with the newly generated details
		prompt.ToolUsage[toolName] = usage
	}

	// Append general instructions/constraints and set OutputFormat if multi-command tools exist
	if hasMultiCommandTool {
		// Append only if not already present
		if !lo.Contains(prompt.Instructions, multiCmdInstruction) {
			prompt.Instructions = append(prompt.Instructions, multiCmdInstruction)
		}
		if !lo.Contains(prompt.Constraints, multiCmdConstraint1) {
			prompt.Constraints = append(prompt.Constraints, multiCmdConstraint1)
		}
		if !lo.Contains(prompt.Constraints, multiCmdConstraint2) {
			prompt.Constraints = append(prompt.Constraints, multiCmdConstraint2)
		}
		if !lo.Contains(prompt.Constraints, multiCmdConstraint3) {
			prompt.Constraints = append(prompt.Constraints, multiCmdConstraint3)
		}
		// Set the specific output format for multi-command scenarios
		prompt.OutputFormat = multiCmdOutputFormat
	} else {
		// If no multi-command tools were found, only set a default OutputFormat
		// if the existing prompt didn't already have one.
		if prompt.OutputFormat == "" {
			prompt.OutputFormat = defaultOutputFormat
		}
	}

	// Examples and RAG are already part of the existingPrompt, no need to set them here.

	return prompt, nil
}

// formatRAGDocument parses a single RAG document and formats it using the configured keys.
func formatRAGDocument(ragConfig NBAgentPromptRag, rawDocument string) string {
	if ragConfig.Format == NBAgentPromptRagFormatJson {
		questionKey := ragConfig.QuestionKey
		if questionKey == "" {
			questionKey = "question"
		}
		explanationKey := ragConfig.ExplanationKey
		if explanationKey == "" {
			explanationKey = "explanation"
		}
		answerKey := ragConfig.AnswerKey
		if answerKey == "" {
			answerKey = "answer"
		}

		mappedDoc := map[string]any{}
		err := json.Unmarshal([]byte(rawDocument), &mappedDoc)
		if err != nil {
			slog.Warn("unable to unmarshal matchingDoc as JSON, falling back to raw document", "document", slog.AnyValue(rawDocument), "error", err.Error())
			return rawDocument
		}

		if val, ok := mappedDoc[questionKey]; ok {
			var sb strings.Builder
			sb.WriteString("Question: " + val.(string) + "\n")
			if val, ok := mappedDoc[answerKey]; ok {
				switch v := val.(type) {
				case string:
					sb.WriteString("Answer: " + v + "\n")
				default:
					marshalledData, e := json.Marshal(v)
					if e != nil {
						slog.Error("unable to serialize doc", "error", e, "data", slog.AnyValue(v))
					} else {
						sb.WriteString("Answer: " + string(marshalledData) + "\n")
					}
				}
			}
			if val, ok := mappedDoc[explanationKey]; ok {
				switch v := val.(type) {
				case string:
					if v != "" {
						sb.WriteString("Explanation: " + v + "\n")
					}
				default:
					planbytes, err := json.Marshal(v)
					if err != nil {
						slog.Error("unable to marshal plan", "plan", slog.AnyValue(v), "error", err.Error())
					} else {
						sb.WriteString("Explanation: " + string(planbytes) + "\n")
					}
				}
			}
			return sb.String()
		}
	}
	return rawDocument
}

// isReActStylePlanner returns true for planner types that use ReAct-style XML
// formatting in their prompts (thought_action, final_answer, etc.).
// defaultReactOutputFormat is injected as FINAL ANSWER REQUIREMENTS for react-style planners
// when the agent does not specify its own OutputFormat. It conditionally applies the
// investigation format (5-Whys, evidence chain) only for troubleshooting queries.
const defaultReactOutputFormat = `Choose the format based on the type of user request:

**FOR INVESTIGATION / TROUBLESHOOTING QUERIES** (e.g. "why is X failing", "debug Y", "show me recent issues"):

**Investigation Summary:**
- **Symptom:** [What user reported]
- **Signal:** [What metrics/logs/events showed]

### Causality Chain (5-Whys)
- **Symptom:** (The primary issue reported/observed)
- **Why?** (Immediate cause of the symptom)
- **Why?** (Next layer of causality)
- **Root Cause:** (The foundational reason discovered)

**Evidence Chain:**
1. [Tool Name - ID](#task-ID) -> [Key finding]
2. [Tool Name - ID](#task-ID) -> [Key finding]

**CRITICAL: Citation Format Rule**
You MUST use the full markdown link format for EVERY reference: [Short Tool Name - ID](#task-ID).
Example: ...found in [Kubectl Execute - E1](#task-E1) and [Logs - E3](#task-E3).

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]

**FOR ALL OTHER QUERIES** (generation, listing, explanation, how-to, etc.):
Answer the user's question directly in clear markdown. Do NOT use the investigation format above. Use code blocks, tables, or bullet points as appropriate for the content.`

func isReActStylePlanner(plannerType AgentPlannerType) bool {
	return plannerType == AgentPlannerTypeReAct || plannerType == AgentPlannerTypeReAct3
}

func GetPromptTemplate(p NBAgentPrompt, query NBAgentRequest, plannerType AgentPlannerType) prompts.PromptTemplate {
	var sb strings.Builder

	// Add Role
	if p.Role != "" {
		fmt.Fprintf(&sb, "You are %s.\n\n", p.Role)
	}

	// Add Instructions
	if len(p.Instructions) > 0 {
		if isReActStylePlanner(plannerType) {
			sb.WriteString("<instructions>\n")
		} else {
			sb.WriteString("Instructions:\n")
		}
		for _, instruction := range p.Instructions {
			fmt.Fprintf(&sb, "- %s\n", instruction)
		}
		if isReActStylePlanner(plannerType) {
			sb.WriteString("</instructions>\n\n")
		} else {
			sb.WriteString("\n\n")
		}
	}

	// Add Constraints
	if len(p.Constraints) > 0 {
		if isReActStylePlanner(plannerType) {
			sb.WriteString("<constraints>\n")
		} else {
			sb.WriteString("Constraints:\n")
		}
		for _, constraint := range p.Constraints {
			fmt.Fprintf(&sb, "- %s\n", constraint)
		}
		if isReActStylePlanner(plannerType) {
			sb.WriteString("</constraints>\n\n")
		} else {
			sb.WriteString("\n\n")
		}
	}

	// Add Schema
	if len(p.Schema) > 0 {
		if isReActStylePlanner(plannerType) {
			sb.WriteString("<schema>\n")
		} else {
			sb.WriteString("Schema Information:\n")
		}
		for _, schema := range p.Schema {
			fmt.Fprintf(&sb, "- %s\n", schema)
		}
		if isReActStylePlanner(plannerType) {
			sb.WriteString("</schema>\n\n")
		} else {
			sb.WriteString("\n\n")
		}
	}

	// Add Tool Usage
	if len(p.ToolUsage) > 0 {
		if isReActStylePlanner(plannerType) {
			sb.WriteString("<tool_usage_instructions>\n")
		} else {
			sb.WriteString("Instructions on tool usage:\n")
		}
		for toolName, usage := range p.ToolUsage {
			fmt.Fprintf(&sb, "**%s**\n", toolName)
			for _, usage := range usage {
				fmt.Fprintf(&sb, "- %s\n", usage)
			}
		}
		if isReActStylePlanner(plannerType) {
			sb.WriteString("</tool_usage_instructions>\n\n")
		} else {
			sb.WriteString("\n\n")
		}
	}

	// Add o/p format
	outputFormat := p.OutputFormat
	// React-style planners get a default investigation summary format when agents don't set
	// their own. This ensures 5-Whys, Evidence Chain, and structured resolution sections are
	// enforced via the "FINAL ANSWER REQUIREMENTS" block for all react_3 agents.
	if outputFormat == "" && isReActStylePlanner(plannerType) {
		outputFormat = defaultReactOutputFormat
	}
	if outputFormat != "" {
		if isReActStylePlanner(plannerType) {
			sb.WriteString("FINAL ANSWER REQUIREMENTS (CRITICAL):\n")
			sb.WriteString("- You MUST provide your entire final response wrapped in a `<final_answer>` block.\n")
			sb.WriteString("- Inside the `<final_answer>` block, you MUST have a `<thought>` tag and a `<content>` tag.\n")
			fmt.Fprintf(&sb, "- The text inside the `<content>` tag MUST follow this specific format: %s\n", outputFormat)
			sb.WriteString("- Do NOT include any explanations or markdown backticks outside of the XML tags.\n\n")
		} else {
			sb.WriteString("Output Format:\n")
			fmt.Fprintf(&sb, "- %s\n\n", outputFormat)
		}
	}

	// Add Examples
	if len(p.Examples) > 0 {
		if isReActStylePlanner(plannerType) {
			sb.WriteString("<examples>\n")
		} else {
			sb.WriteString("Examples:\n")
		}
		for _, example := range p.Examples {
			if example.Question == "" {
				continue
			}
			if isReActStylePlanner(plannerType) {
				sb.WriteString("<example>\n")
				fmt.Fprintf(&sb, "<question>%s</question>\n", example.Question)
			} else {
				fmt.Fprintf(&sb, "question: %s\n", example.Question)
			}
			if example.Answer != "" {
				if isReActStylePlanner(plannerType) {
					sb.WriteString("<answer>\n")
					sb.WriteString("<final_answer>\n")
					if example.Explanation != "" {
						fmt.Fprintf(&sb, "<thought>%s</thought>\n", example.Explanation)
					}
					fmt.Fprintf(&sb, "<content>%s</content>\n", example.Answer)
					sb.WriteString("</final_answer>\n")
					sb.WriteString("</answer>\n")
				} else {
					fmt.Fprintf(&sb, "answer: %s\n", example.Answer)
					if example.Explanation != "" {
						fmt.Fprintf(&sb, "explanation: %s\n", example.Explanation)
					}
				}
			} else if len(example.AnswerSteps) > 0 {
				if isReActStylePlanner(plannerType) {
					sb.WriteString("<answer>\n")
					if example.Explanation != "" {
						fmt.Fprintf(&sb, "<thought>%s</thought>\n", example.Explanation)
					}
					for _, step := range example.AnswerSteps {
						sb.WriteString("<thought_action>\n")
						if step.Explanation != "" {
							fmt.Fprintf(&sb, "<thought>%s</thought>\n", step.Explanation)
						} else {
							fmt.Fprintf(&sb, "<thought>I will use %s to process the request.</thought>\n", step.Tool)
						}
						sb.WriteString("<action>\n")
						fmt.Fprintf(&sb, "    <tool_name>%s</tool_name>\n", step.Tool)
						fmt.Fprintf(&sb, "    <tool_input>%s</tool_input>\n", step.Input)
						sb.WriteString("</action>\n")
						sb.WriteString("</thought_action>\n")
					}
					sb.WriteString("</answer>\n")
				} else {
					sb.WriteString("answer_steps:\n")
					for _, step := range example.AnswerSteps {
						fmt.Fprintf(&sb, "- tool: %s\n", step.Tool)
						fmt.Fprintf(&sb, "  input: %s\n", step.Input)
						if step.Explanation != "" {
							fmt.Fprintf(&sb, "  explanation: %s\n", step.Explanation)
						}
					}
					if example.Explanation != "" {
						fmt.Fprintf(&sb, "explanation: %s\n", example.Explanation)
					}
				}
			}
			if isReActStylePlanner(plannerType) {
				sb.WriteString("</example>\n")
			}
		}
		if isReActStylePlanner(plannerType) {
			sb.WriteString("</examples>\n\n")
		} else {
			sb.WriteString("\n\n")
		}
	}

	// if RAG is defined then populate that
	if p.Rag.Module != "" {
		numberOfResults := p.Rag.Records
		if numberOfResults <= 1 {
			// Single-result path (existing behavior)
			document := toolcore.GetRAG(query.UserId, query.AccountId, query.Query, p.Rag.Module, query.ConversationId, query.MessageId, query.ParentAgentId, true)
			if document.Document != "" {
				matchingDocString := formatRAGDocument(p.Rag, document.Document)
				if isReActStylePlanner(plannerType) {
					sb.WriteString("<rag_examples>\n")
				} else {
					sb.WriteString("Examples from RAG:\n")
				}
				sb.WriteString(matchingDocString)
				if isReActStylePlanner(plannerType) {
					sb.WriteString("</rag_examples>\n\n")
				} else {
					sb.WriteString("\n\n")
				}
			}
		} else {
			// Multi-result path: fetch N results from RAG
			documents := toolcore.QueryRAG(query.UserId, query.AccountId, query.Query,
				p.Rag.Module, numberOfResults, query.ConversationId,
				query.MessageId, query.ParentAgentId, true)

			if len(documents) > 0 {
				if isReActStylePlanner(plannerType) {
					sb.WriteString("<rag_examples>\n")
				} else {
					sb.WriteString("Examples from RAG:\n")
				}
				for i, doc := range documents {
					if doc.Document == "" {
						continue
					}
					if isReActStylePlanner(plannerType) {
						fmt.Fprintf(&sb, "<rag_example id=\"%d\">\n", i+1)
					} else {
						fmt.Fprintf(&sb, "--- Example %d ---\n", i+1)
					}
					matchingDocString := formatRAGDocument(p.Rag, doc.Document)
					sb.WriteString(matchingDocString)
					if isReActStylePlanner(plannerType) {
						sb.WriteString("</rag_example>\n")
					} else {
						sb.WriteString("\n")
					}
				}
				if isReActStylePlanner(plannerType) {
					sb.WriteString("</rag_examples>\n\n")
				} else {
					sb.WriteString("\n")
				}
			}
		}
	}

	return prompts.NewPromptTemplate(sb.String(), p.Variables)
}
