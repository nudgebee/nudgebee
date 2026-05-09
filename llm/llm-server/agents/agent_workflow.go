package agents

import (
	"encoding/json"
	"fmt"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBAgentFactoryAndTool(WorkflowAgentName, func(accountId string) (core.NBAgent, error) {
		return newWorkflowAgent(accountId), nil
	}, "Manages automations: list, get, trigger, create, and validate.", "Ask to list, get details, trigger, or create an automation.", "Returns automation info or action result.")
}

const WorkflowAgentName = "automation"

type WorkflowAgent struct {
	accountId string
}

func newWorkflowAgent(accountId string) WorkflowAgent {
	return WorkflowAgent{accountId: accountId}
}

func (a WorkflowAgent) GetName() string {
	return WorkflowAgentName
}

func (a WorkflowAgent) GetNameAliases() []string {
	return []string{"Automation", "AutomationManager", "Workflow", "WorkflowManager", "workflow"}
}

func (a WorkflowAgent) GetDescription() string {
	return "Manages automations. Can list, get details, trigger executions, and create new automations (delegating to AutomationBuilder for construction)."
}

func (a WorkflowAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{}

	// If user is viewing a specific automation, add context so the LLM knows which automation to operate on
	if query.QueryConfig.WorkflowId != "" {
		instructions = append(instructions,
			fmt.Sprintf("**Current Automation Context:** The user is viewing automation ID `%s`. When they say 'this automation', 'last execution', 'check status', etc., use this automation ID.", query.QueryConfig.WorkflowId),
			"",
		)
	} else if query.QueryConfig.WorkflowDefinition != nil {
		// Unsaved automation from AI builder — provide the definition as context
		if defBytes, err := json.Marshal(query.QueryConfig.WorkflowDefinition); err == nil {
			instructions = append(instructions,
				fmt.Sprintf("**Current Automation Context (unsaved):** The user is working on an unsaved AI-generated automation with the following definition:\n```json\n%s\n```\nWhen they reference 'this automation' or report errors about tasks, use this definition as context. This automation has NOT been saved yet — it has no ID.", string(defBytes)),
				"",
			)
		}
	}

	instructions = append(instructions,
		"**Safety:** Never modify an automation (via `workflow_update`) without explicit user approval. Always present proposed changes first and wait for the user to confirm before applying.",
		"",
		"**Capabilities:**",
		"- List automations: `workflow_list`",
		"- Get automation details: `workflow_get`",
		"- Trigger an automation: `workflow_trigger`",
		"- Update an automation: `workflow_update`",
		"- List executions: `workflow_executions`",
		"- Get execution details: `workflow_execution_get`",
		"- Retrigger execution: `workflow_execution_retrigger`",
		"- Get automation state: `workflow_state`",
		"- List configs: `workflow_config_list`",
		"- Get a config by key: `workflow_config_get`",
		"- Create/update a config: `workflow_config_save`",
		"- Build an automation definition: Delegate to `automation_builder` tool.",
		"",
		"**Creating & Modifying Automations:**",
		"1. If the user wants to create an automation OR provides feedback/changes to an existing plan, **ALWAYS** delegate to the `automation_builder` tool. It handles the iterative construction and plan state.",
		"2. **STRICT RULE:** Do NOT use `workflow_list`, `workflow_update`, `workflow_get`, or any other tool to handle feedback while building. All changes must go through `automation_builder`.",
		"3. **CRITICAL:** When `automation_builder` returns a result (a plan or a JSON definition), you MUST stop immediately. If it's a JSON definition, return ONLY the raw JSON as your `Final Answer`. Do NOT call `workflow_update`, `workflow_create`, or any other tool with that result. The automation is automatically saved.",
		"4. If you have already delegated to `automation_builder` in this conversation, continue using it for all related requests until a final definition is produced.",
		"5. When delegating to `automation_builder`, describe WHAT the user wants in plain language. Do NOT specify task types, implementation approaches, or technical details — the builder determines those from its own task registry and the account's configured integrations.",
		"",
		"**Listing:**",
		"Use `workflow_list`. You can filter by name or limit results.",
		"",
		"**Triggering:**",
		"Use `workflow_trigger` with the ID. You might need to `workflow_list` first to find the ID by name.",
		"",
		"**Investigating Executions:**",
		"When the user asks about executions, failures, or task status:",
		"1. Use `workflow_executions` to list recent runs (with id of the automation).",
		"   **NOTE:** The `workflow_id` field in each execution is an internal runtime ID and may differ from the automation ID you queried. All returned executions belong to the queried automation — always report them.",
		"2. **ALWAYS** follow up with `workflow_execution_get` for each execution to inspect task-level details.",
		"   - An execution with status COMPLETED can still have individual tasks that failed.",
		"   - Report task-level statuses, errors, and outputs — not just the top-level execution status.",
		"3. If tasks failed, use `workflow_get` to retrieve the current automation definition for context.",
		"",
		"**Fixing Automations:**",
		"**IMPORTANT — Be efficient with your iteration budget (max ~10 steps):**",
		"- If the error is ALREADY KNOWN from conversation history, do NOT re-investigate. Skip directly to step 4.",
		"- NEVER call `workflow_executions` + `workflow_execution_get` + `workflow_get` more than ONCE per message.",
		"- Delegate to `automation_builder` ONCE with all context, then proceed to present changes.",
		"",
		"4. To fix: delegate to `automation_builder` with the automation ID, error details, and current definition.",
		"   Example: 'Fix automation <id>. Error: task get-pods failed with <error>. Current definition: ...'",
		"5. **Before applying changes, you MUST present the proposed changes to the user and ask for confirmation.**",
		"   - Summarize what changed (e.g., 'Changed JSONata expression in task X from A to B')",
		"   - Ask the user: 'Would you like me to apply these changes to the automation?'",
		"   - Only call `workflow_update` AFTER the user explicitly confirms (e.g., 'yes', 'apply', 'go ahead')",
		"   - If the user declines, do NOT update. Ask what they would like to change instead.",
		"6. **After successful update**, offer to re-trigger the automation to verify the fix:",
		"   - 'I've applied the changes. Would you like me to re-trigger the automation to verify the fix works?'",
		"   - Use `workflow_execution_retrigger` or `workflow_trigger` if user confirms.",
		"",
		"**Configs:**",
		"Configs are key-value pairs stored in the automation server, referenced in automation definitions via `{{ Configs.key_name }}`.",
		"- Use `workflow_config_list` to list all configs (optionally filter by labels).",
		"- Use `workflow_config_get` to retrieve a specific config by key.",
		"- Use `workflow_config_save` to create or update a config.",
		"- Secret-type config values are masked in responses for security.",
		"- NEVER use `workflow_config_save` without explicit user approval.",
		"",
		"**Follow-up Questions:**",
		"When the user asks about previous actions (e.g., 'did you apply changes?', 'what did you change?'):",
		"- Check the conversation history for what was discussed and any tool results.",
		"- Give a CONCISE answer (yes/no + brief explanation). Do NOT dump raw JSON.",
		"- If unsure, briefly check with `workflow_get` but summarize the result — do NOT return the full definition.",
	)

	toolUsage := map[string][]string{
		tools.ToolWorkflowList: {
			"List automations. Args: limit (int), name (string).",
		},
		tools.ToolWorkflowGet: {
			"Get full details of an automation. Arg: id (string, required).",
		},
		tools.ToolWorkflowTrigger: {
			"Trigger an automation. Args: id (string, required), inputs (map).",
		},
		tools.ToolWorkflowTaskList: {
			"List available tasks to help understand what can be done.",
		},
		tools.ToolWorkflowUpdate: {
			"Update an existing automation definition. Args: id (string, required), definition (object, required).",
		},
		tools.ToolWorkflowExecutions: {
			"List executions of an automation. Args: id (string, required), limit (optional), status (optional).",
		},
		tools.ToolWorkflowExecutionGet: {
			"Get details of a specific execution including individual task statuses, errors, and outputs. Args: id (string, required), execution_id (string, required). IMPORTANT: Always use this after workflow_executions to get task-level details.",
		},
		tools.ToolWorkflowExecutionRetrigger: {
			"Re-trigger a failed execution. Args: id (string, required), execution_id (string, required), inputs (optional).",
		},
		tools.ToolWorkflowState: {
			"Get persistent state of an automation. Arg: id (string, required).",
		},
		tools.ToolWorkflowConfigList: {
			"List automation configs. Optional arg: labels (object, key-value pairs to filter by). Secret values are masked.",
		},
		tools.ToolWorkflowConfigGet: {
			"Get a specific automation config by key. Arg: key (string, required). Secret values are masked.",
		},
		tools.ToolWorkflowConfigSave: {
			"Create or update an automation config. Args: key (string, required), value (string, required), type (string, default 'config'). NEVER call without user approval.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "You are an Automation Manager. You handle the lifecycle of automations.",
		Instructions: instructions,
		ToolUsage:    toolUsage,
	}
}

func (a WorkflowAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedTools := []string{
		tools.ToolWorkflowList,
		tools.ToolWorkflowGet,
		tools.ToolWorkflowTrigger,
		tools.ToolWorkflowTaskList,
		tools.ToolWorkflowUpdate,
		tools.ToolWorkflowExecutions,
		tools.ToolWorkflowExecutionGet,
		tools.ToolWorkflowExecutionRetrigger,
		tools.ToolWorkflowState,
		tools.ToolWorkflowConfigList,
		tools.ToolWorkflowConfigGet,
		tools.ToolWorkflowConfigSave,
	}

	toolsList := []toolcore.NBTool{}
	for _, toolName := range supportedTools {
		if t, ok := toolcore.GetNBTool(a.accountId, toolName); ok {
			toolsList = append(toolsList, t)
		}
	}

	// If WorkflowBuilderAgent is exposed as a tool (agent-as-a-tool pattern), add it here.
	if t, ok := toolcore.GetNBTool(a.accountId, WorkflowBuilderAgentName); ok {
		toolsList = append(toolsList, t)
	}

	return toolsList
}

func (a WorkflowAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
