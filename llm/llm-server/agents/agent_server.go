package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const ServerAgentName = "server"

func init() {
	// This describes the 'server' agent when it is used as a tool by another agent.
	toolDescription := `Interacts with linux/mac/windows servers by executing shell commands based on natural language questions to retrieve data or check status.`
	toolInput := "Provide question related to linux/mac/windows in natural language."
	toolOutput := "shell command response"

	core.RegisterNBAgentFactoryAndTool(ServerAgentName, func(accountId string) (core.NBAgent, error) {
		return ServerAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)

}

type ServerAgent struct {
	accountId string
}

func (l ServerAgent) GetName() string {
	return ServerAgentName
}

func (l ServerAgent) GetNameAliases() []string {
	return []string{"Server"}
}

func (l ServerAgent) GetDescription() string {
	return `The linux/mac/windows agent is a knowledgeable and concise Infrastructe expert specialized in Linux, acting as an SRE. The primary tool is the 'shell' command-line utility.`
}

func (l ServerAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{tools.ServerExecuteTool{}}
	return toolsList
}
func (l ServerAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"You are an expert SRE agent responsible for managing and troubleshooting Virtual Machines.",
		"Your primary function is to execute shell commands to diagnose issues, retrieve information, and perform administrative tasks.",
		"**Guiding Principles:**",
		"1.  **Identify the Operating System**: Start by determining the OS distribution and version using commands like `uname -a`, `cat /etc/os-release`, or `lsb_release -a`. This will help you choose the correct commands and package managers.",
		"2.  **Use Non-Interactive Commands**: Only execute commands that can run without user intervention. Avoid interactive tools like `vi`, `nano`, or `top`.",
		"3.  **Be Cautious**: Do not execute commands that could have destructive consequences, such as `rm`, `mkfs`, or `reboot`, unless explicitly instructed to do so.",
		"4.  **Summarize When Necessary**: If the output of a command is verbose, provide a concise summary of the most relevant information.",
		"**Command Schema:**",
		"Command must be a valid JSON object with the following keys:",
		"'command', static fixed value 'shell'",
		"'args', the shell command to execute (e.g., 'ls -l', 'free -h'). Ensure it is a valid JSON string with properly escaped characters.",
		"'instance', hostname",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	constraints := []string{
		"Always use shell as the primary tool to interact with the server.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteServerCommand: {
			"Use this tool to execute a shell command.",
			"Input: A JSON object with 'command', 'args', and 'instance' keys.",
			"Output: the data returned by the shell command.",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteServerCommand] = []string{
			"Executes shell commands on a remote server using SSH.",
			"Input: A JSON object with 'command', 'args', and 'instance' keys.",
			"Output: Data returned by the remote server.",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the server output.",
		}
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "Can disk usage of machine abc",
			Answer:      `{"command":"shell", "args": "du -sh /", "instance":"abc"}`,
			Explanation: "This command checks the disk usage of the root directory on the abc server..",
		},
		{
			Question:    "can you get the process list of instance abc",
			Answer:      `{"command":"shell", "args": "ps -ef", "instance":"abc"}`,
			Explanation: "This command retrieves the process list from the abc server.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "a knowledgeable and concise mac/linux/windows expert, acting as an SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "server",
		},
	}

}

func (l ServerAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
