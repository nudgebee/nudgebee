package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const RabbitMQAgentName = "rabbitmq"

func init() {
	// This describes the 'rabbitmq' agent when it is used as a tool by another agent.
	toolDescription := `Executes RabbitMQ operations by translating natural language questions into rabbitmqadmin commands or RabbitMQ HTTP Management API calls. Use this agent to list, query, or manage queues, exchanges, connections, consumers, and other RabbitMQ resources. Supports consumer-by-queue identification, queue health/backlog analysis, and node metrics. Returns command results or summaries for automation and troubleshooting.`
	toolInput := "Provide a question in natural language to list, query, or manage RabbitMQ resources."
	toolOutput := "Returns command results or summaries for RabbitMQ operations."

	core.RegisterNBAgentFactoryAndTool(RabbitMQAgentName, func(accountId string) (core.NBAgent, error) {
		return RabbitMQAgent{accountId: accountId}, nil
	}, toolDescription, toolInput, toolOutput)
}

type RabbitMQAgent struct {
	accountId string
}

func (l RabbitMQAgent) GetName() string {
	return RabbitMQAgentName
}

func (l RabbitMQAgent) GetNameAliases() []string {
	return []string{"RabbitMq"}
}

func (l RabbitMQAgent) GetDescription() string {
	return `Executes RabbitMQ operations by translating natural language questions into rabbitmqadmin commands or RabbitMQ HTTP Management API calls. Use this agent to list, query, or manage queues, exchanges, connections, consumers, and other RabbitMQ resources. Supports consumer-by-queue identification, queue health/backlog analysis, and node metrics. Returns command results or summaries for automation and troubleshooting.`
}

func (l RabbitMQAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolList := []toolcore.NBTool{tools.RabbitExecuteTool{}}
	return toolList
}

func (l RabbitMQAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Two tools available:** Use `rabbitmqadmin` for simple list/declare/delete operations. Use `curl` against the RabbitMQ HTTP Management API (`http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/...`) for richer queries — especially anything involving consumers per queue, queue health, message rates, or node metrics. Credentials are injected automatically for both; never add them yourself.",
		"**No Credentials:** Do not include user/password/host/port arguments in any command.",
		"**Choose the right tool:**",
		"   - **Queues (list):** `rabbitmqadmin list queues`",
		"   - **Queues (health/backlog):** `curl .../api/queues` — returns messages, messages_ready, messages_unacknowledged, consumers, state, consumer_utilisation per queue.",
		"   - **Exchanges:** `rabbitmqadmin list exchanges`",
		"   - **Bindings:** `rabbitmqadmin list bindings`",
		"   - **Connections:** `rabbitmqadmin list connections`",
		"   - **Consumers (all, no queue context):** `rabbitmqadmin list consumers`",
		"   - **Consumers grouped by queue:** `curl .../api/consumers` — returns full objects with `.queue.name`, `.consumer_tag`, `.channel_details.peer_host` (pod IP), `.channel_details.name` (connection string), `.prefetch_count`, `.active`, `.ack_required`.",
		"   - **Consumers for one specific queue:** `curl .../api/queues/%2F/{queue_name}` — returns `.consumer_details[]` with the same fields. Use `%2F` to encode the default vhost `/`.",
		"   - **Queues with no consumers (orphaned):** filter `curl .../api/queues` with `jq '[.[] | select(.consumers == 0)]'`.",
		"   - **Node health (memory, disk, FDs):** `curl .../api/nodes` — returns mem_used, mem_limit, disk_free, disk_free_limit, fd_used, fd_total, proc_used, sockets_used.",
		"   - **Cluster overview (totals, rates):** `curl .../api/overview`",
		"**jq for processing:** Always pipe `curl` output through `jq` to extract only the fields the user needs. Use `group_by`, `select`, `sort_by`, `map` as appropriate.",
		"**Command Schema:** The tool input must be a JSON object:",
		"  - `args`: (string, required) The full command string — either `rabbitmqadmin` subcommand args, or a complete `curl ... | jq ...` pipeline.",
		"  - `instance`: (string, optional) Hostname, config name, or env to target. For a pod use `{pod}.{namespace}.svc.cluster.local`.",
		"**Error Handling:** Handle errors gracefully. If a queue name is not found (HTTP 404), tell the user clearly.",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	constraints := []string{
		"Use ONLY `rabbitmqadmin` or `curl` against the RabbitMQ HTTP Management API — no other tools or commands",
		"Do not include credentials in any command — they are injected automatically",
		"Do not add any additional information in the final response, only the command is needed.",
	}
	toolUsage := map[string][]string{
		tools.ToolExecuteRabbitCommand: {
			"Use this tool to execute rabbitmqadmin commands or curl calls against the RabbitMQ HTTP Management API.",
			"Input: valid rabbitmqadmin command, or a curl command targeting http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/... (credentials are injected automatically)",
			"Output: the data returned by the command.",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteRabbitCommand] = []string{
			"Use this tool to execute rabbitmqadmin commands or curl calls against the RabbitMQ HTTP Management API.",
			"Input: valid rabbitmqadmin command, or a curl command targeting http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/... (credentials are injected automatically)",
			"Output: the data returned by the command.",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) as well as jq to process the output.",
		}
	}
	examples := []core.NBAgentPromptExample{
		// --- rabbitmqadmin examples ---
		{
			Question: "List all queues",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list queues"}`,
				},
			},
			Explanation: "Simple list via rabbitmqadmin.",
		},
		{
			Question: "List all exchanges",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list exchanges"}`,
				},
			},
			Explanation: "Simple list via rabbitmqadmin.",
		},
		{
			Question: "Show me active connections",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list connections"}`,
				},
			},
			Explanation: "Simple list via rabbitmqadmin.",
		},
		{
			Question: "List all consumers",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list consumers"}`,
				},
			},
			Explanation: "rabbitmqadmin list consumers returns all consumers without queue context.",
		},
		{
			Question: "List all bindings",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list bindings"}`,
				},
			},
			Explanation: "Simple list via rabbitmqadmin.",
		},
		{
			Question: "List all bindings in dev env",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list bindings", "instance":"dev"}`,
				},
			},
			Explanation: "User specified env, so instance is dev.",
		},
		{
			Question: "List all queues in config abc",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list queues", "instance":"abc"}`,
				},
			},
			Explanation: "User specified config, so instance is abc.",
		},
		{
			Question: "List all queues for pod rabbit-0 in namespace rabbit",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "list queues", "instance":"rabbit-0.rabbit.svc.cluster.local"}`,
				},
			},
			Explanation: "Use pod.namespace.svc.cluster.local as instance.",
		},
		// --- HTTP Management API examples ---
		{
			Question: "Show me consumers of the k8s_agent_events queue",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/queues/%2F/k8s_agent_events | jq '.consumer_details[] | {consumer_tag: .consumer_tag, pod_ip: .channel_details.peer_host, channel: .channel_details.name, prefetch: .prefetch_count, active: .active}'"}`,
				},
			},
			Explanation: "Use the HTTP API for per-queue consumer detail. %2F encodes the default vhost /. consumer_details[] contains pod IP, consumer tag, channel name, prefetch.",
		},
		{
			Question: "Which pods are consuming the anomaly_processing queue?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/queues/%2F/anomaly_processing | jq '[.consumer_details[] | {consumer_tag: .consumer_tag, pod_ip: .channel_details.peer_host, active: .active}]'"}`,
				},
			},
			Explanation: "HTTP API queue endpoint gives consumer_details with peer_host = pod IP.",
		},
		{
			Question: "List all consumers grouped by queue with their pod IPs",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/consumers | jq 'group_by(.queue.name) | map({queue: .[0].queue.name, consumer_count: length, consumers: map({tag: .consumer_tag, pod_ip: .channel_details.peer_host, ack_required: .ack_required})})'"}`,
				},
			},
			Explanation: "HTTP API /api/consumers returns all consumers with queue.name included. group_by organises them per queue.",
		},
		{
			Question: "Show me queue health — message counts and consumer counts for all queues",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/queues | jq '[.[] | {name: .name, messages: .messages, ready: .messages_ready, unacked: .messages_unacknowledged, consumers: .consumers, state: .state}] | sort_by(-.messages)'"}`,
				},
			},
			Explanation: "HTTP API /api/queues returns per-queue message counts, consumer count and state. Sort by message depth to surface backlogs.",
		},
		{
			Question: "Which queues have no active consumers?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/queues | jq '[.[] | select(.consumers == 0 and (.name | startswith(\"amq.gen-\") | not)) | {name: .name, messages: .messages, state: .state}]'"}`,
				},
			},
			Explanation: "Filter queues where consumers == 0, excluding auto-generated amq.gen-* queues, to find unmonitored queues.",
		},
		{
			Question: "Are there any queues with a message backlog?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/queues | jq '[.[] | select(.messages > 0) | {name: .name, messages: .messages, ready: .messages_ready, unacked: .messages_unacknowledged, consumers: .consumers}] | sort_by(-.messages)'"}`,
				},
			},
			Explanation: "Select only queues with messages > 0 and sort descending to find the deepest backlogs.",
		},
		{
			Question: "Show me node health — memory, disk, file descriptors",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/nodes | jq '.[] | {name: .name, running: .running, mem_used_mb: (.mem_used/1048576|floor), mem_limit_mb: (.mem_limit/1048576|floor), disk_free_gb: (.disk_free/1073741824|floor), fd_used: .fd_used, fd_total: .fd_total, sockets_used: .sockets_used}'"}`,
				},
			},
			Explanation: "HTTP API /api/nodes gives memory, disk, FD and socket usage per node.",
		},
		{
			Question: "Give me a cluster overview",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "curl http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/overview | jq '{rabbitmq_version: .rabbitmq_version, totals: .object_totals, queue_totals: .queue_totals}'"}`,
				},
			},
			Explanation: "HTTP API /api/overview gives version, object counts and queue message totals.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "a knowledgeable and concise RabbitMQ expert, acting as an SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "rabbitmq",
		},
	}
}

func (l RabbitMQAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l RabbitMQAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}
