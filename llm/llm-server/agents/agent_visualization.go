package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const VisualizationAgentName = "visualizer"

func init() {
	toolDescription := `Generates Mermaid.js diagrams (flowcharts, architecture, timelines, charts like line/bar/pie) based on natural language descriptions or data flows. Use this to create visual aids for complex information.`
	toolInput := "Provide a description of the flow, architecture, or charts like line/bar/pie to visualize."
	toolOutput := "A markdown block containing the Mermaid.js code."

	core.RegisterNBAgentFactoryAndTool(VisualizationAgentName, func(accountId string) (core.NBAgent, error) {
		return &VisualizationAgent{
				accountId: accountId,
			},
			nil
	}, toolDescription, toolInput, toolOutput)
}

type VisualizationAgent struct {
	accountId string
}

func (l *VisualizationAgent) GetName() string {
	return VisualizationAgentName
}

func (a *VisualizationAgent) GetNameAliases() []string {
	return []string{"Visualizer", "MermaidGenerator"}
}

func (l *VisualizationAgent) GetDescription() string {
	return `Generates Mermaid.js diagrams (flowcharts, architecture, timelines, charts like line/bar/pie) based on natural language descriptions or data flows.`
}

func (l *VisualizationAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l *VisualizationAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}

func (l *VisualizationAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l *VisualizationAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	systemPrompt := `
		<role>
			You are an expert in Mermaid.js diagram generation. 
			Your goal is to convert technical descriptions, data flows, or architectural information into valid Mermaid.js syntax.
		</role>
		<instructions>
			1. **Quoting (CRITICAL):** ALWAYS enclose node labels AND subgraph titles in double quotes. This is mandatory to avoid parse errors with spaces, parentheses, or other special characters.
			   - **Nodes:** Use ID["Label"] or ID(["Label"]) or ID{{"Label"}}. NEVER omit the double quotes inside the brackets.
			     - Correct: S1["Service (v1)"]
			     - Incorrect: S1(Service (v1))
			   - **Subgraphs:** Use subgraph "Label" or subgraph ID ["Label"].
			     - Correct: subgraph "Cluster (Prod)"
			     - Correct: subgraph C1 ["Cluster (Prod)"]
			     - Incorrect: subgraph Cluster (Prod)
			2. **Output Format:** Provide ONLY the Mermaid code block. Do not add introductory text, summaries, or conclusions.
			3. **Diagram Types:** 
			   - Use 'graph TD' or 'graph LR' for flowcharts and system architectures.
			   - Use 'sequenceDiagram' for step-by-step service interactions.
			   - Use 'classDiagram' or 'stateDiagram-v2' for code/state modeling.
			   - Use 'gantt' or 'timeline' for sequences of events.
			4. **Clarity:** Keep node IDs concise and strictly alphanumeric (e.g., S1, DB1). Put all descriptive or special-character text inside the quoted label.
			5. **Arrow Notation:** If the input provided is in "Arrow Notation" (e.g., A -> B -> C), convert it into a professional Mermaid diagram.
			6. **Comments (CRITICAL):** ALWAYS use double percent signs (%%) for comments. NEVER use a single percent sign (%), as it is invalid syntax.
			     - Correct: %% This is a comment
			     - Incorrect: % This is a comment
			7. **XYChart Arrays (CRITICAL):** For 'xychart' or 'xychart-beta', ALL array definitions (x-axis, y-axis, bar, line) MUST be on a single line. Multi-line arrays will cause syntax errors.
			   - **Numeric Data:** 'bar' and 'line' arrays MUST contain ONLY numeric values (no quotes, NO 'null' values). Use 0 for missing data points if necessary.
			     - Correct: bar "Requests/sec" [10.5, 0, 15.2]
			     - Incorrect: bar "Requests/sec" [10.5, null, 15.2]
			     - Incorrect: bar "Requests/sec" ["10.5", "0"]
			     - Correct: x-axis ["Jan", "Feb"]
			     - Incorrect: x-axis [
			                  "Jan",
			                  "Feb"
			                  ]
			   - **Series Labels (CRITICAL):** EVERY 'bar' and 'line' series MUST include a descriptive label in double quotes BEFORE the data array. Without a label, the chart legend will show a generic placeholder. Derive the label from the source data (e.g., pod name, namespace, metric name).
			     - Correct: line "rabbitmq-0" [10.5, 12.1, 15.2]
			     - Correct: bar "api-server (requests/s)" [50, 60, 85]
			     - Incorrect: line [10.5, 12.1, 15.2]  (missing label)
			     - Incorrect: bar [50, 60, 85]  (missing label)
			8. **Multiline Labels:** Use ` + "`" + `<br/>` + "`" + ` for line breaks inside standard quoted labels, or use Markdown strings (quoted backticks) if styling is needed.
			     - Standard: ID["Line1<br/>Line2"]
			     - Markdown: ID["` + "`" + `**Bold**\n_Italic_` + "`" + `"]
			9. **Grounding (CRITICAL):** Do not invent services, connections, or data points that are not present in the input description. Ground all diagram elements strictly in the provided text. If information is missing, do not guess; visualize only what is explicitly stated.
			10. **Pie Charts:** Do NOT use negative values in pie charts. If a value is negative (e.g., a credit), use 0 or the absolute value and note it in the label, or exclude it.
		</instructions>
		<examples>
			<example type="graph">
				graph TD
				    subgraph "Service Mesh"
				        S1["API Gateway"] --> S2["Auth Service"]
				        S2 --> DB1[("User DB")]
				    end
			</example>
			<example type="sequence">
				sequenceDiagram
				    Alice->>Bob: Hello Bob
				    alt is sick
				        Bob->>Alice: Not so good :(
				    else is well
				        Bob->>Alice: Feeling fresh
				    end
			</example>
			<example type="class">
				classDiagram
				    class BankAccount {
				        +String owner
				        +BigDecimal balance
				        +deposit(amount)
				        +withdrawal(amount)
				    }
			</example>
			<example type="xychart">
				xychart
				    title "Server Performance"
				    x-axis ["00:00", "01:00", "02:00"]
				    y-axis "Value"
				    bar "Requests/sec" [50, 60, 85]
				    line "Latency (ms)" [40, 50, 60]
			</example>
			<example type="xychart-multi-series">
				xychart
				    title "RabbitMQ CPU Utilization (Core)"
				    x-axis ["Apr 01", "Apr 02", "Apr 03", "Apr 04"]
				    y-axis "CPU Usage (cores)"
				    line "rabbitmq-0" [0.012, 0.015, 0.018, 0.042]
				    line "rabbitmq-1" [0.008, 0.011, 0.009, 0.015]
				    line "rabbitmq-2" [0.004, 0.006, 0.005, 0.007]
			</example>
			<example type="pie">
				pie title "Resource Usage"
				    "CPU" : 45
				    "Memory" : 30
				    "Disk" : 25
			</example>
		</examples>
		<outputformat>
			Return only a markdown code block:
			` + "```mermaid" + `
			[MERMAID CODE HERE]
			` + "```" + `
		</outputformat>
	`

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("Please visualize the following information:\n\n%s", request.Query)),
	}

	var lastContent string
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false)
		if err != nil {
			ctx.GetLogger().Error("visualizer: unable to generate content", "error", err)
			return core.NBAgentResponse{Response: nil}, err
		}

		content := strings.TrimSpace(completion.Choices[0].Content)
		if len(content) == 0 {
			if i == maxRetries-1 {
				return core.NBAgentResponse{Response: []string{"Failed to generate visualization"}}, nil
			}
			continue
		}
		lastContent = content

		// Use the validation tool
		tool, ok := toolcore.GetNBTool(request.AccountId, tools.MermaidValidationToolName)
		if !ok {
			ctx.GetLogger().Warn("visualizer: unable to get validation tool, skipping validation")
			break
		}

		callReq := toolcore.NBToolCallRequest{
			Command: tools.MermaidValidationToolName,
			Arguments: map[string]any{
				"code": content,
			},
		}

		toolCtx := toolcore.NbToolContext{
			Ctx:            ctx,
			AccountId:      request.AccountId,
			UserId:         request.UserId,
			ConversationId: request.ConversationId,
			MessageId:      request.MessageId,
			ParentAgentId:  request.AgentId,
		}

		resp, err := tool.Call(toolCtx, callReq)
		if err != nil {
			ctx.GetLogger().Warn("visualizer: validation tool error", "error", err)
			break // If tool fails, assume valid or give up on validation
		}

		if strings.HasPrefix(resp.Data, "Invalid Mermaid code:") {
			ctx.GetLogger().Info("visualizer: generated code is invalid, retrying", "attempt", i+1, "errors", resp.Data)

			messageContent = append(messageContent, llms.TextParts(llms.ChatMessageTypeAI, content))
			messageContent = append(messageContent, llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("The generated code has the following validation errors:\n%s\n\nPlease fix these errors and regenerate the full mermaid code.", resp.Data)))
			continue
		} else {
			// Valid
			break
		}
	}

	return core.NBAgentResponse{Response: []string{lastContent}, Status: core.ConversationStatusCompleted}, nil
}
