package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const RedisAgentName = "redis"

func init() {
	// This describes the 'redis' agent when it is used as a tool by another agent.
	toolDescription := `Executes Redis operations by translating natural language questions into redis-cli commands. This tool is "smart" and handles its own instance discovery. Use this agent directly to query, update, or check Redis status and health without needing separate reconnaissance. Returns command results or summaries for automation and troubleshooting.`
	toolInput := "Provide a question in natural language to query, update, or check the status of Redis."
	toolOutput := "Returns command results or summaries for Redis operations."

	core.RegisterNBAgentFactoryAndTool(RedisAgentName, func(accountId string) (core.NBAgent, error) {
		return RedisAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)

}

type RedisAgent struct {
	accountId string
}

func (l RedisAgent) GetName() string {
	return RedisAgentName
}

func (l RedisAgent) GetNameAliases() []string {
	return []string{"Redis"}
}

func (l RedisAgent) GetDescription() string {
	return `Executes Redis operations by translating natural language questions into redis-cli commands. This tool is "smart" and handles its own instance discovery. Use this agent directly to query, update, or check Redis status and health without needing separate reconnaissance. Returns command results or summaries for automation and troubleshooting.`
}

func (l RedisAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{tools.RedisExecuteTool{}}
	return tools
}
func (l RedisAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Redis Interaction:** Use `redis-cli` to interact with Redis instances. Identify the target instance and database.",
		"**Safe Operations:** Prioritize read-only commands (e.g., `GET`, `KEYS`, `INFO`, `SCAN`, `LLEN`, `HGETALL`). Avoid destructive commands unless explicitly requested.",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	constraints := []string{
		"Always use the `redis_execute` tool for Redis interactions.",
		"Avoid fetching large amounts of data at once; use `SCAN` instead of `KEYS *` if possible.",
	}
	toolUsage := map[string][]string{
		tools.ToolExecuteRedisCommand: {
			"Executes Redis commands using `redis-cli`.",
			"Input: A valid Redis command.",
			"Output: Data returned by Redis.",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteRedisCommand] = []string{
			"Executes Redis commands using `redis-cli`.",
			"Input: A valid Redis command.",
			"Output: Data returned by Redis.",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the redis output.",
		}
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "check if Redis is running",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRedisCommand,
					Input: "redis-cli PING",
				},
			},
			Explanation: "This command checks if the Redis server is running.",
		},
		{
			Question: "get the value of a key mykey",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRedisCommand,
					Input: "redis-cli GET mykey",
				},
			},
			Explanation: "This command retrieves the value of the key 'mykey' from the Redis server.",
		},
		{
			Question: "set a key with an expiration time",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRedisCommand,
					Input: "redis-cli SET mykey 'value' EX 60",
				},
			},
			Explanation: "This command sets the key 'mykey' to the value 'value' with an expiration time of 60 seconds.",
		},
		{
			Question: "list all keys",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRedisCommand,
					Input: "redis-cli KEYS *",
				},
			},
			Explanation: "This command lists all keys in the Redis server.",
		},
		{
			Question: "check Redis memory usage",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRedisCommand,
					Input: "redis-cli INFO memory",
				},
			},
			Explanation: "This command checks the memory usage of the Redis server.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "a knowledgeable and concise Redis expert, acting as an SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "redis",
		},
	}

}

func (l RedisAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l RedisAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}
