package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

const ToolExecuteRabbitCommand = "rabbit_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteRabbitCommand, func(accountId string) (core.NBTool, error) {
		return RabbitExecuteTool{}, nil
	})
}

type RabbitExecuteTool struct {
}

func (m RabbitExecuteTool) Name() string {
	return ToolExecuteRabbitCommand
}

func (m RabbitExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m RabbitExecuteTool) Description() string {
	return `Executes 'rabbitmqadmin' commands or 'curl' calls against the RabbitMQ HTTP Management API. This tool allows you to gather information about RabbitMQ resources and configuration, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about the user's RabbitMQ resources, use this tool.
		* **Input:** Provide a valid command as input. Input should be a valid JSON, with following attributes -
			"instance" - rabbitmq server/config/env name (optional)
			"args" - the command to run: either a 'rabbitmqadmin' subcommand or a 'curl' call to the HTTP Management API (required)
			"command" - rabbitmqadmin (optional, ignored for curl-based args)
		* **Output:** The tool will return the output of the executed command.

		**Examples (rabbitmqadmin):**
		{"instance":"<server-name>", "args":"list queues", "command": "rabbitmqadmin"} – List Queues.
		{"instance":"<server-name>", "args":"list connections", "command": "rabbitmqadmin"} – List Connections.
		{"instance":"<server-name>", "args":"list consumers", "command": "rabbitmqadmin"} – List all Consumers.

		**Examples (HTTP Management API via curl – use for message rates, cluster overview, health checks, or per-queue consumer detail that rabbitmqadmin can't express):**
		{"args":"curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/overview | jq '{version: .rabbitmq_version, totals: .object_totals, message_stats: .message_stats}'"} – Cluster overview with rates.
		{"args":"curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/queues/%2F/my_queue | jq '.consumer_details[] | {tag: .consumer_tag, pod_ip: .channel_details.peer_host, prefetch: .prefetch_count}'"} – Consumers for a specific queue (replace my_queue; %2F = default vhost /).

		**Important Notes:**

		* Do NOT include credentials in commands – they are injected automatically.
		* curl URLs must target the management API on $RABBITMQ_HOST:$RABBITMQ_PORT (do NOT hardcode a port).
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

// ToolPrompt implements core.NBToolPromptProvider — carries the rabbitmq
// how-to guidance that today lives in RabbitMQAgent.GetSystemPrompt(). The tool
// exposes TWO invocation shapes (rabbitmqadmin CLI + rabbitmq-api HTTP shim);
// both are covered here so a delegate naming this tool directly (post-3c) knows
// which to reach for and how to compose the corresponding jq filters.
func (m RabbitExecuteTool) ToolPrompt() []string {
	return []string{
		// Overall shape and constraints
		"**Two invocation shapes through this tool:** Use `rabbitmqadmin` for list/declare/delete and column-selectable queries. Use `rabbitmq-api METHOD /path [extra curl args]` for the RabbitMQ HTTP Management API (message rates, cluster overview, per-queue consumer detail, health/aliveness, and any mutation the CLI can't express). The `rabbitmq-api` shim targets the configured broker automatically — write just the path (`rabbitmq-api GET /api/overview`), no host or port. Credentials are injected for both; never add them yourself.",
		"**No Credentials:** Do not include user/password/host/port arguments in any command.",
		"**rabbitmq-api scope:** The shim is locked to `$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}` — you cannot redirect it to another host. Supported methods: GET/POST/PUT/DELETE/PATCH/HEAD. Path must start with `/`. Extra curl flags after the path pass through (`-d '{...}'` for a JSON body, `-H '...'` for headers). A plain `curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/...` still works for legacy scripts, but prefer `rabbitmq-api`.",
		"**Choose the right shape:** rabbitmqadmin for listing/mutating queues/exchanges/bindings/consumers/nodes; rabbitmq-api for /api/overview (message rates), /api/queues/%2F/{name} (per-queue consumer_details), /api/aliveness-test/%2F (health), and PUT/DELETE mutations like policies.",
		"**jq for processing:** Pipe `rabbitmqadmin -f raw_json ...` or `rabbitmq-api GET ...` output through `jq` to extract only the fields the user needs (group_by, select, sort_by, map).",
		"**Error Handling:** Handle errors gracefully. If a queue name is not found (HTTP 404), tell the user clearly.",
		"Use ONLY `rabbitmqadmin` or `rabbitmq-api` (or a plain `curl` against the management API for legacy scripts) — no other tools or commands.",
		"`rabbitmq-api` path must start with `/` and contain only URL-legal characters — the shim rejects anything that could break out of the scope-locked URL.",
		// rabbitmqadmin examples
		"Example (rabbitmqadmin) — list all queues: `{\"args\": \"list queues\"}`",
		"Example (rabbitmqadmin) — list all connections: `{\"args\": \"list connections\"}`",
		"Example (rabbitmqadmin) — target a specific pod: `{\"args\": \"list queues\", \"instance\": \"rabbit-0.rabbit.svc.cluster.local\"}`",
		"Example (rabbitmqadmin) — queue health/backlog: `{\"args\": \"-f raw_json list queues name messages messages_ready messages_unacknowledged consumers state | jq '[.[] | {name: .name, messages: .messages, ready: .messages_ready, unacked: .messages_unacknowledged, consumers: .consumers, state: .state}] | sort_by(-.messages)'\"}`",
		"Example (rabbitmqadmin) — consumers for one queue with pod IPs: `{\"args\": \"-f raw_json list consumers | jq '[.[] | select(.queue.name == \\\"anomaly_processing\\\") | {consumer_tag: .consumer_tag, pod_ip: .channel_details.peer_host, active: .active}]'\"}`",
		"Example (rabbitmqadmin) — orphan queues (no consumers): `{\"args\": \"-f raw_json list queues name messages consumers state | jq '[.[] | select(.consumers == 0 and (.name | startswith(\\\"amq.gen-\\\") | not)) | {name: .name, messages: .messages, state: .state}]'\"}`",
		"Example (rabbitmqadmin) — node health: `{\"args\": \"-f raw_json list nodes name running mem_used mem_limit disk_free fd_used fd_total sockets_used | jq '.[] | {name: .name, mem_used_mb: (.mem_used/1048576|floor), disk_free_gb: (.disk_free/1073741824|floor), fd_used: .fd_used, fd_total: .fd_total}'\"}`",
		// rabbitmq-api examples
		"Example (rabbitmq-api) — cluster overview + rates: `{\"args\": \"rabbitmq-api GET /api/overview | jq '{rabbitmq_version: .rabbitmq_version, totals: .object_totals, queue_totals: .queue_totals, message_stats: .message_stats}'\"}`",
		"Example (rabbitmq-api) — publish/deliver rates: `{\"args\": \"rabbitmq-api GET /api/overview | jq '.message_stats | {publish: .publish_details.rate, deliver: .deliver_details.rate, ack: .ack_details.rate}'\"}`",
		"Example (rabbitmq-api) — per-queue consumer_details: `{\"args\": \"rabbitmq-api GET /api/queues/%2F/anomaly_processing | jq '.consumer_details[] | {tag: .consumer_tag, pod_ip: .channel_details.peer_host, prefetch: .prefetch_count, active: .active}'\"}`",
		"Example (rabbitmq-api) — aliveness check: `{\"args\": \"rabbitmq-api GET /api/aliveness-test/%2F\"}` (returns {\"status\":\"ok\"} when healthy)",
		"Example (rabbitmq-api) — set a max-length policy: `{\"args\": \"rabbitmq-api PUT /api/policies/%2F/events-max-length -d '{\\\"pattern\\\":\\\"events-.*\\\",\\\"definition\\\":{\\\"max-length\\\":10000},\\\"priority\\\":0,\\\"apply-to\\\":\\\"queues\\\"}' -H 'content-type: application/json'\"}`",
	}
}

func (m RabbitExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON string with 'command' and 'instance' fields. 'instance' is the host to connect to.",
				Default:     "rabbitmqadmin",
			},
			"args": {
				Type:        core.ToolSchemaTypeString,
				Description: "Args to rabbitmqadmin",
				Default:     "",
			},
			"instance": {
				Type:        core.ToolSchemaTypeString,
				Description: "Server instance/env/config to use",
				Default:     "",
			},
		},
		Required: []string{"args"},
	}
}

type rabbitmqCommand struct {
	Instance string `json:"instance"`
	Args     string `json:"args"`
	Command  string `json:"command"`
}

func (m RabbitExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("rabbit: executing executeShellCommand tool call", "query", input.Command)

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	command := rabbitmqCommand{}
	// Try to parse from Arguments first (new format from executor_planner)
	if input.Arguments != nil {
		if instance, ok := input.Arguments["instance"].(string); ok {
			command.Instance = instance
		}
		if args, ok := input.Arguments["args"].(string); ok {
			command.Args = args
		}
		if cmd, ok := input.Arguments["command"].(string); ok {
			command.Command = cmd
		}
	}

	// If fields are still empty, try to parse input.Command as JSON (fallback/older format)
	if command.Args == "" && input.Command != "" {
		_ = common.UnmarshalJson([]byte(input.Command), &command)
	}

	if command.Args == "" {
		return core.NBToolResponse{}, errors.New("missing 'args' parameter: rabbitmqadmin command, rabbitmq-api METHOD /path, or curl required")
	}

	// Build the command string. `curl ...` (legacy Management-API call) and
	// `rabbitmq-api METHOD /path [extra]` (scoped Management-API shim from
	// PR #35005) are passed through as-is — the RelayJobRabbitmq rewrite in
	// ExecuteContainerJob (common_relay.go) transforms both into an
	// authenticated curl before dispatch. Anything else gets prefixed with the
	// configured CLI (rabbitmqadmin), which is the historical default.
	//
	// Without the rabbitmq-api carve-out, the else branch would produce
	// `rabbitmqadmin rabbitmq-api METHOD /path`, which is not a valid
	// rabbitmqadmin subcommand — so LLM invocations following the updated
	// agent prompt would hit an unrecognised-subcommand error.
	var commandStr string
	trimmed := strings.TrimSpace(command.Args)
	switch {
	case strings.HasPrefix(trimmed, "curl "):
		commandStr = trimmed
	case trimmed == "rabbitmq-api" || strings.HasPrefix(trimmed, "rabbitmq-api "):
		commandStr = trimmed
	default:
		if command.Command == "" {
			command.Command = "rabbitmqadmin"
		}
		commandStr = strings.TrimSpace(command.Command + " " + command.Args)
	}

	// Run through the workspace shell so `rabbitmqadmin` and `rabbitmq-api`
	// (both shimmed in the workspace pod via PR #35005) can compose with
	// local shell primitives — redirects to `/workspace/*.json`, pipes to
	// local `jq`, `&&` chains — and downstream tools like `code_agent` can
	// read the resulting files in follow-up calls (Kind-B cross-boundary
	// chaining, the whole point of restoring this path).
	//
	// The one exception is `curl` against the RabbitMQ HTTP Management API.
	// `curl` is NOT shimmed in the workspace pod (it's a general-purpose
	// binary with many non-rabbit uses); running it there means no
	// $RABBITMQ_HOST / credentials / customer-network route — the exact
	// silent-fail that PR #34906 stopgap-fixed by routing everything through
	// the relay. LLMs following the updated prompt use `rabbitmq-api`
	// instead, but this carve-out preserves back-compat for cached / stored
	// completions that still emit raw curl. The carve-out dispatches such
	// curl commands directly to the relay, where common_relay.go's
	// RelayJobRabbitmq rewrite injects basic-auth against the customer's
	// broker.
	if strings.Contains(commandStr, "curl") && strings.Contains(commandStr, "/api/") {
		response, err := ExecuteContainerJob(nbRequestContext, RelayJobRabbitmq, commandStr, nbRequestContext.AccountId, map[string]any{}, false)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("rabbit: unable to execute curl command via relay", "error", err.Error())
			responseData := ""
			if response != nil {
				if responseData1, ok := response.(string); ok {
					responseData = responseData1
				}
			}
			return core.NBToolResponse{
				Data:   responseData,
				Status: core.NBToolResponseStatusError,
			}, err
		}
		data := response.(string)
		return core.NBToolResponse{
			Data:   data,
			Type:   core.NBToolResponseTypeText,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	wm := workspace.NewWorkspaceManager()
	wsResponse, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, commandStr, map[string]string{
		workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("rabbit: unable to execute shell script", "error", err.Error(), "command", commandStr)
		if wsResponse == "" {
			wsResponse = err.Error()
		}
		// rabbitmqctl uses `help` as a subcommand (no dashes),
		// e.g. `rabbitmqctl help list_queues`.
		return core.NBToolResponse{
			Data:   cliRecoveryEnvelope(wsResponse, "", "rabbitmqctl", "rabbitmqctl help <command>"),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	outputformat := map[string]string{
		"stdout": wsResponse,
	}
	outputformatBytes, err := common.MarshalJson(outputformat)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("rabbit: unable to marshal response", "error", err.Error())
		return core.NBToolResponse{
			Data:   wsResponse,
			Status: core.NBToolResponseStatusError,
		}, err
	}
	wsResponse = string(outputformatBytes)

	return core.NBToolResponse{
		Data:   wsResponse,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m RabbitExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a RabbitMQ security expert. Your task is to classify a 'rabbitmqadmin' command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: rabbitmqadmin list queues
	answer: read

	input: rabbitmqadmin list connections
	answer: read

	input: rabbitmqadmin get queue=my-queue count=1
	answer: read

	input: rabbitmqadmin declare queue name=my-new-queue
	answer: create

	input: rabbitmqadmin declare exchange name=my-exchange type=direct
	answer: create

	input: rabbitmqadmin declare binding source=my-exchange destination=my-queue
	answer: create

	input: rabbitmqadmin publish exchange=amq.default routing_key=my-queue payload="hello"
	answer: create

	input: rabbitmqadmin purge queue name=my-queue
	answer: update

	input: rabbitmqadmin set_permission vhost=/ user=guest configure=".*" write=".*" read=".*"
	answer: update

	input: rabbitmqadmin delete queue name=my-queue
	answer: delete

	input: rabbitmqadmin delete exchange name=my-exchange
	answer: delete

	input: rabbitmqadmin close_connection "127.0.0.1:12345 -> 127.0.0.1:5672"
	answer: delete
	`
	return prompt, nil
}

func (m RabbitExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	instanceName := ""

	// Try to get instance from Arguments
	if input.Arguments != nil {
		if inst, ok := input.Arguments["instance"].(string); ok {
			instanceName = inst
		}
	}

	// Fallback to parsing Command as JSON
	if instanceName == "" {
		command := struct {
			Instance string `json:"instance"`
		}{}
		_ = common.UnmarshalJson([]byte(input.Command), &command)
		instanceName = command.Instance
	}

	if instanceName == "" {
		return core.ToolConfig{}, nil
	}

	for _, config := range availableConfigs {
		// 1. Try matching by config name
		if strings.EqualFold(config.Name, instanceName) {
			return config, nil
		}

		// 2. Try matching by host patterns
		var hostPatterns string
		for _, v := range config.Values {
			if v.Name == "host" {
				hostPatterns = v.Value
				break
			}
		}

		if hostPatterns != "" {
			patterns := strings.Split(hostPatterns, ",")
			for _, pattern := range patterns {
				trimmedPattern := strings.TrimSpace(pattern)
				if trimmedPattern == "" {
					continue
				}
				// Ensure case-insensitive matching if not already present
				if !strings.HasPrefix(trimmedPattern, "(?i)") {
					trimmedPattern = "(?i)" + trimmedPattern
				}
				re, err := regexp.Compile(trimmedPattern)
				if err != nil {
					ctx.Ctx.GetLogger().Warn("rabbit: invalid regex pattern in host config", "pattern", trimmedPattern, "error", err)
					continue
				}

				if re.MatchString(instanceName) {
					return config, nil
				}
			}
		}
	}

	return core.ToolConfig{}, nil
}

func (m RabbitExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "rabbitmq",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "Rabbitmq Secret in k8s, Required Keys, RABBITMQ_HOST, RABBITMQ_PASSWORD, RABBITMQ_PORT, RABBITMQ_USER",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "rabbitmq host",
			},
		},
	}
}
