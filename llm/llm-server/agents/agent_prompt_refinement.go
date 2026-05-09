package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const PromptRefinementAgentName = "prompt_refinement"

func init() {
	toolDescription := `Improves user-defined prompts by analyzing and refining them for clarity, specificity, and effectiveness with large language models (LLMs). Use this agent to optimize prompts for better results, automation, or downstream tasks. Returns a refined prompt ready for use with LLMs.`
	toolInput := "Provide a user-defined prompt in natural language to be refined."
	toolOutput := "Returns a refined and optimized prompt for LLMs."

	core.RegisterNBAgentFactoryAndTool(PromptRefinementAgentName, func(accountId string) (core.NBAgent, error) {
		return newPromptRefinementAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newPromptRefinementAgent(accountId string) PromptRefinementAgent {
	return PromptRefinementAgent{
		accountId: accountId,
	}
}

type PromptRefinementAgent struct {
	accountId string
}

func (l PromptRefinementAgent) GetName() string {
	return PromptRefinementAgentName
}

func (l PromptRefinementAgent) GetNameAliases() []string {
	return []string{"PromptRefiner", "PromptOptimizer"}
}

func (l PromptRefinementAgent) GetDescription() string {
	return `Improves user-defined prompts by analyzing and refining them for clarity, specificity, and effectiveness with large language models (LLMs). Use this agent to optimize prompts for better results, automation, or downstream tasks. Returns a refined prompt ready for use with LLMs.`
}

func (l PromptRefinementAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedTools := []toolcore.NBTool{}
	llmTool, ok := toolcore.GetNBTool(l.accountId, core.ToolLlm)
	if ok {
		supportedTools = append(supportedTools, llmTool)
	}
	return supportedTools
}

func (l PromptRefinementAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze the User's Goal:** Deeply understand the user's intended purpose for the prompt. What is the user trying to achieve?",
		"**Identify Weaknesses:** Pinpoint weaknesses in the user's prompt. Common weaknesses include:",
		"  - **Vagueness:** The prompt is not specific enough.",
		"  - **Ambiguity:** The prompt could be interpreted in multiple ways.",
		"  - **Lack of Context:** The prompt does not provide enough background information.",
		"  - **Lack of Constraints:** The prompt does not specify any constraints for the output.",
		"**Preserve Technical Structure:** If the original prompt contains step-by-step instructions, diagnostic procedures, or structured formatting (e.g., YAML/Markdown/code blocks), retain that structure unless it is incorrect or inefficient.",
		"**Avoid Generic Substitution:** Do not replace a specific, technical prompt with general or vague SRE/DevOps advice. Focus only on improving the clarity, conciseness, or flow of the *existing* instructions.",
		"**Clarify When Needed:** If part of the prompt is vague, suggest improvements or clarifying questions — do not hallucinate or make assumptions.",
		"**Suggest Specific Improvements:** Suggest concrete improvements to address the identified weaknesses. Explain *why* these improvements are necessary.",
		"**Provide a Refined Prompt:** Provide a refined version of the prompt that incorporates the suggested improvements. The refined prompt should be ready to be used with a large language model.",
	}
	constraints := []string{
		"You MUST retain all technical instructions, step flows, metrics, and formatting unless there is a clear issue.",
		"You MUST NOT replace structured content with generic SRE/DevOps advice.",
		"You MAY clarify, reformat, or simplify the prompt — but not remove meaningful information.",
		"The refined prompt MUST be output inside a markdown code block.",
		"If the original prompt is already strong, make minimal improvements and explain why no major changes were made.",
	}
	toolUsage := map[string][]string{
		core.ToolLlm: {
			"Use this tool to generate the refined prompt and the explanation.",
			"Use this tool to provide a clear explanation for your final answer, with the summary in markdown format. Ensure the output is complete and readable markdown.",
		},
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Refine this prompt: 'Context: You are a Kubernetes assistant responsible for monitoring and diagnosing...'",
			Answer:      "```\nYou are a Kubernetes assistant responsible for monitoring and diagnosing the health of applications running in the 'nudgebee' namespace.\n\nThese applications rely on:\n- PostgreSQL\n- RabbitMQ\n- Prometheus (for metrics)\n- OpenTelemetry (for traces, if available)\n\n### Task Overview\nEvaluate application health using the following steps:\n\n1. **Pod Health Check**\n   - List all pods in the 'nudgebee' namespace.\n   - Flag pods not in 'Running' or 'Completed'.\n   - For pods in CrashLoopBackOff/OOMKilled, retrieve the last 50 log lines and report errors.\n\n2. **Dependency Auto-Discovery**\n   - Detect presence of PostgreSQL and RabbitMQ (via services or deployments).\n   - If found, assess their health using Prometheus metrics.\n\n3. **Metrics Analysis (via Prometheus)**\n   - General: `up`, `container_cpu_usage_seconds_total`, `container_memory_usage_bytes`\n   - PostgreSQL: connection count, cache hit ratio (<0.99 triggers alert), db size\n   - RabbitMQ: queue depth, unacked messages, disk space\n   - Error & Latency: HTTP 5xx rate > 1 per 5min, 95th percentile latency\n\n4. **Anomaly Detection**\n   - Compare current metrics (last 1h) to 24h baseline\n   - Highlight spikes in errors, CPU, memory, or latency\n\n5. **Trace Analysis (optional)**\n   - If tracing is enabled, look for slow/error spans in last 30min\n   - Identify top bottlenecks or repeated DB issues\n\n6. **Final Report**\n   - Status: Healthy / Degraded / Failing\n   - Key Issues & Metrics\n   - Recommendations (e.g., improve cache hit rate, consumer lag handling)\n```",
			Explanation: "The original prompt was technically detailed and structurally sound. The refined version improves clarity and formatting, keeps the diagnostic logic intact, and enhances readability without losing any essential steps or metrics.",
		},
		{
			Question:    "Make my prompt better.",
			Answer:      "I need more information to refine your prompt. Please provide the prompt you want me to refine.",
			Explanation: "The user's request is too vague. I need the prompt to refine.",
		},
		{
			Question:    "Refine this prompt: 'Write a story.'",
			Answer:      "```\nWrite a short story (under 1000 words) about a robot who discovers a hidden talent for painting. The story should be set in a futuristic city and have a surprise ending.\n```",
			Explanation: "The original prompt was too broad. The refined prompt provides more specific constraints (word count, character, setting, plot twist) and a clear direction for the story.",
		},
		{
			Question:    "Refine this prompt: 'Explain quantum computing.'",
			Answer:      "```\nExplain the concept of quantum computing to a high school student with a basic understanding of physics. Use an analogy to explain the difference between a classical bit and a qubit. The explanation should be no more than 3 paragraphs.\n```",
			Explanation: "The original prompt was too general. The refined prompt specifies the target audience (high school student), a specific concept to explain (qubit vs. bit), and a length constraint.",
		},
		{
			Question:    "Refine this prompt: 'Generate a marketing slogan for my new coffee shop.'",
			Answer:      "```\nGenerate 5 catchy marketing slogans for a new coffee shop called 'The Daily Grind'. The coffee shop is located in a busy downtown area and targets young professionals. The slogans should be short, memorable, and highlight the quality of the coffee and the convenience of the location.\n```",
			Explanation: "The original prompt lacked context. The refined prompt provides the name of the coffee shop, the target audience, the location, and the desired qualities of the slogans.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "a prompt engineering expert",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "markdown, with the refined prompt in a code block",
	}
}

func (l PromptRefinementAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (p PromptRefinementAgent) GetSummaryToolName() string {
	return core.ToolLlm
}
