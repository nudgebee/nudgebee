package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const RabbitMQAgentName = "rabbitmq"

// Phase 3c (#32503): agent registration removed. Guidance for both invocation
// shapes (rabbitmqadmin CLI + rabbitmq-api HTTP shim) now lives on
// RabbitExecuteTool.ToolPrompt(). Kept type/methods for one release so external
// references to RabbitMQAgentName still compile; delete after bake.

// RabbitMQAgent is deprecated (Phase 3c #32503). Type kept for Flattenable-path
// tests; runtime registration and guidance live on RabbitExecuteTool now.
type RabbitMQAgent struct{}

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

// Flattenable implements flattenableAgent: rabbitmq's GetSystemPrompt is static (Rag is
// unused) and its whole contribution is that prompt + rabbitmq_execute. A delegate_agent
// call naming "rabbitmq" inlines the leaf tool (single hop) and carries its GetSystemPrompt
// guidance — including its management-API curl/jq examples, which are the real value —
// instead of nesting the whole agent as its own ReAct loop.
func (l RabbitMQAgent) Flattenable() bool { return true }

func (l RabbitMQAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Two commands available:** Use `rabbitmqadmin` for list/declare/delete and column-selectable queries. Use `rabbitmq-api METHOD /path [extra curl args]` for the RabbitMQ HTTP Management API (message rates, cluster overview, per-queue consumer detail, health/aliveness, and any mutation the CLI can't express). The `rabbitmq-api` shim targets the configured broker automatically — write just the path (`rabbitmq-api GET /api/overview`), no host or port. Credentials are injected for both; never add them yourself.",
		"**No Credentials:** Do not include user/password/host/port arguments in any command.",
		"**rabbitmq-api scope:** The shim is locked to `$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}` — you cannot redirect it to another host. Supported methods: GET/POST/PUT/DELETE/PATCH/HEAD. Path must start with `/`. Extra curl flags after the path pass through (`-d '{...}'` for a JSON body, `-H '...'` for headers). A plain `curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/...` still works for legacy scripts, but prefer `rabbitmq-api` — it's more concise and can't be misdirected.",
		"**Choose the right tool:**",
		"   - **Queues (list):** `rabbitmqadmin list queues`",
		"   - **Queues (health/backlog):** `rabbitmqadmin -f raw_json list queues name messages messages_ready messages_unacknowledged consumers state` — pipe to `jq` to sort/filter (e.g. deepest backlogs, or `select(.consumers == 0)` for orphaned queues).",
		"   - **Exchanges / Bindings / Connections:** `rabbitmqadmin list exchanges` / `list bindings` / `list connections`.",
		"   - **Consumers (all, incl. pod IP):** `rabbitmqadmin -f raw_json list consumers` — full objects with `.queue.name`, `.consumer_tag`, `.channel_details.peer_host` (pod IP), `.prefetch_count`, `.ack_required`; `jq` by `.queue.name` to group/filter.",
		"   - **Node health (memory, disk, FDs):** `rabbitmqadmin -f raw_json list nodes name mem_used mem_limit disk_free disk_free_limit fd_used fd_total sockets_used`.",
		"   - **Message rates / cluster overview:** `rabbitmq-api GET /api/overview` — `.message_stats`, `.object_totals`, `.queue_totals` (rabbitmqadmin has no equivalent).",
		"   - **Per-queue consumer detail:** `rabbitmq-api GET /api/queues/%2F/{queue_name}` — `.consumer_details[]`; `%2F` encodes the default vhost `/`.",
		"   - **Health / aliveness:** `rabbitmq-api GET /api/aliveness-test/%2F` or `rabbitmq-api GET /api/healthchecks/node/{node}`.",
		"   - **Set/update a policy (mutation):** `rabbitmq-api PUT /api/policies/%2F/{policy-name} -d '{\"pattern\":\"queue-.*\",\"definition\":{\"max-length\":10000},\"priority\":0,\"apply-to\":\"queues\"}'` — PUT for create/update, `DELETE` to remove.",
		"**jq for processing:** Pipe `rabbitmqadmin -f raw_json ...` or `rabbitmq-api GET ...` output through `jq` to extract only the fields the user needs. Use `group_by`, `select`, `sort_by`, `map` as appropriate.",
		"**Command Schema:** The tool input must be a JSON object:",
		"  - `args`: (string, required) The full command — a `rabbitmqadmin` subcommand, a `rabbitmq-api METHOD /path [extra]` invocation, or a legacy `curl ... | jq ...` pipeline.",
		"  - `instance`: (string, optional) Hostname, config name, or env to target. For a pod use `{pod}.{namespace}.svc.cluster.local`.",
		"**Error Handling:** Handle errors gracefully. If a queue name is not found (HTTP 404), tell the user clearly.",
	}

	constraints := []string{
		"Use ONLY `rabbitmqadmin` or `rabbitmq-api` (or a plain `curl` against `http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/...` for legacy scripts) — no other tools or commands",
		"`rabbitmq-api` path must start with `/` and contain only URL-legal characters — the shim rejects anything that could break out of the scope-locked URL",
		"Do not include credentials in any command — they are injected automatically",
		"Do not add any additional information in the final response, only the command is needed.",
	}
	toolUsage := map[string][]string{
		tools.ToolExecuteRabbitCommand: {
			"Use this tool to execute rabbitmqadmin commands, `rabbitmq-api METHOD /path` calls, or legacy curl calls against the RabbitMQ HTTP Management API.",
			"Input: a valid rabbitmqadmin command, a `rabbitmq-api METHOD /path [extra curl args]` invocation (host/port/creds injected automatically), or a curl command targeting http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/... .",
			"Output: the data returned by the command.",
		},
	}

	toolUsage[tools.ToolExecuteRabbitCommand] = []string{
		"Use this tool to execute rabbitmqadmin commands, `rabbitmq-api METHOD /path` calls, or legacy curl calls against the RabbitMQ HTTP Management API.",
		"Input: a valid rabbitmqadmin command, a `rabbitmq-api METHOD /path [extra curl args]` invocation (host/port/creds injected automatically), or a curl command targeting http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/... .",
		"Output: the data returned by the command.",
		"You can use pipes (|) and jq to process output (e.g. `rabbitmqadmin -f raw_json list queues ... | jq ...` or `rabbitmq-api GET /api/overview | jq ...`).",
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
		// --- richer rabbitmqadmin (-f raw_json | jq) examples ---
		{
			Question: "Show me consumers of the k8s_agent_events queue",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "-f raw_json list consumers | jq '[.[] | select(.queue.name == \"k8s_agent_events\") | {consumer_tag: .consumer_tag, pod_ip: .channel_details.peer_host, channel: .channel_details.name, prefetch: .prefetch_count, active: .active}]'"}`,
				},
			},
			Explanation: "rabbitmqadmin -f raw_json list consumers returns consumers with .queue.name and .channel_details.peer_host (pod IP); filter by queue name with jq.",
		},
		{
			Question: "Which pods are consuming the anomaly_processing queue?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "-f raw_json list consumers | jq '[.[] | select(.queue.name == \"anomaly_processing\") | {consumer_tag: .consumer_tag, pod_ip: .channel_details.peer_host, active: .active}]'"}`,
				},
			},
			Explanation: "channel_details.peer_host is the consuming pod IP; filter the consumer list by queue name.",
		},
		{
			Question: "List all consumers grouped by queue with their pod IPs",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "-f raw_json list consumers | jq 'group_by(.queue.name) | map({queue: .[0].queue.name, consumer_count: length, consumers: map({tag: .consumer_tag, pod_ip: .channel_details.peer_host, ack_required: .ack_required})})'"}`,
				},
			},
			Explanation: "list consumers includes .queue.name; group_by organises them per queue.",
		},
		{
			Question: "Show me queue health — message counts and consumer counts for all queues",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "-f raw_json list queues name messages messages_ready messages_unacknowledged consumers state | jq '[.[] | {name: .name, messages: .messages, ready: .messages_ready, unacked: .messages_unacknowledged, consumers: .consumers, state: .state}] | sort_by(-.messages)'"}`,
				},
			},
			Explanation: "Select the health columns and sort by message depth to surface backlogs.",
		},
		{
			Question: "Which queues have no active consumers?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "-f raw_json list queues name messages consumers state | jq '[.[] | select(.consumers == 0 and (.name | startswith(\"amq.gen-\") | not)) | {name: .name, messages: .messages, state: .state}]'"}`,
				},
			},
			Explanation: "Filter queues where consumers == 0, excluding auto-generated amq.gen-* queues, to find unmonitored queues.",
		},
		{
			Question: "Are there any queues with a message backlog?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "-f raw_json list queues name messages messages_ready messages_unacknowledged consumers | jq '[.[] | select(.messages > 0) | {name: .name, messages: .messages, ready: .messages_ready, unacked: .messages_unacknowledged, consumers: .consumers}] | sort_by(-.messages)'"}`,
				},
			},
			Explanation: "Select only queues with messages > 0 and sort descending to find the deepest backlogs.",
		},
		{
			Question: "Show me node health — memory, disk, file descriptors",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "-f raw_json list nodes name running mem_used mem_limit disk_free fd_used fd_total sockets_used | jq '.[] | {name: .name, running: .running, mem_used_mb: (.mem_used/1048576|floor), mem_limit_mb: (.mem_limit/1048576|floor), disk_free_gb: (.disk_free/1073741824|floor), fd_used: .fd_used, fd_total: .fd_total, sockets_used: .sockets_used}'"}`,
				},
			},
			Explanation: "list nodes with the health columns gives memory, disk, FD and socket usage per node.",
		},
		// --- HTTP Management API (rabbitmq-api shim) examples: for data rabbitmqadmin can't express ---
		{
			Question: "Give me a cluster overview with message rates",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "rabbitmq-api GET /api/overview | jq '{rabbitmq_version: .rabbitmq_version, totals: .object_totals, queue_totals: .queue_totals, message_stats: .message_stats}'"}`,
				},
			},
			Explanation: "rabbitmqadmin has no overview; /api/overview gives version, object/queue totals and message_stats rates. The rabbitmq-api shim targets the configured broker automatically — no host/port needed.",
		},
		{
			Question: "What are the publish/deliver rates right now?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "rabbitmq-api GET /api/overview | jq '.message_stats | {publish: .publish_details.rate, deliver: .deliver_details.rate, ack: .ack_details.rate}'"}`,
				},
			},
			Explanation: "Per-second rates live only in the management API message_stats, not in rabbitmqadmin.",
		},
		{
			Question: "Show consumer_details for the anomaly_processing queue",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "rabbitmq-api GET /api/queues/%2F/anomaly_processing | jq '.consumer_details[] | {tag: .consumer_tag, pod_ip: .channel_details.peer_host, prefetch: .prefetch_count, active: .active}'"}`,
				},
			},
			Explanation: "Per-queue consumer_details via the queue endpoint; %2F encodes the default vhost /.",
		},
		{
			Question: "Is the broker alive?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "rabbitmq-api GET /api/aliveness-test/%2F"}`,
				},
			},
			Explanation: "Aliveness check on the default vhost; returns {\"status\":\"ok\"} when healthy. No rabbitmqadmin equivalent.",
		},
		{
			Question: "Set a max-length policy of 10000 on all queues starting with 'events-'",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteRabbitCommand,
					Input: `{"args": "rabbitmq-api PUT /api/policies/%2F/events-max-length -d '{\"pattern\":\"events-.*\",\"definition\":{\"max-length\":10000},\"priority\":0,\"apply-to\":\"queues\"}' -H 'content-type: application/json'"}`,
				},
			},
			Explanation: "Mutations use PUT (create/update) or DELETE via rabbitmq-api. -d body and -H headers pass through as normal curl args; the shim adds -X PUT, injects basic-auth, and locks the URL to the configured broker.",
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
