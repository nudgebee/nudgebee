package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
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
		return core.NBToolResponse{}, errors.New("missing 'args' parameter: rabbitmqadmin command or curl required")
	}

	// Build the command string. A curl call to the HTTP Management API is used
	// as-is; anything else is prefixed with the configured CLI (rabbitmqadmin).
	var commandStr string
	if strings.HasPrefix(strings.TrimSpace(command.Args), "curl ") {
		commandStr = strings.TrimSpace(command.Args)
	} else {
		if command.Command == "" {
			command.Command = "rabbitmqadmin"
		}
		commandStr = strings.TrimSpace(command.Command + " " + command.Args)
	}

	// Always execute in the customer environment via the relay — never the
	// workspace pod. rabbitmqadmin and curl-against-the-HTTP-Management-API both
	// need the customer's RabbitMQ host, credentials, and network route, none of
	// which exist in the workspace pod (where curl, being un-shimmed, would run
	// locally against nothing). Routing every command through ExecuteContainerJob
	// keeps both in the customer env and preserves shell piping (`... | jq ...`)
	// since the whole command runs in the relay pod.
	response, err := ExecuteContainerJob(nbRequestContext, RelayJobRabbitmq, commandStr, nbRequestContext.AccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("rabbit: unable to execute shell script", "error", err.Error())
		responseData := ""
		if response != nil {
			if responseData1, ok := response.(string); ok {
				responseData = responseData1
			}
		}
		// Fall back to err.Error() when ExecuteContainerJob returned a nil /
		// non-string response, so the LLM always sees the failure reason
		// rather than an empty envelope.
		if responseData == "" {
			responseData = err.Error()
		}
		return core.NBToolResponse{
			Data:   cliRecoveryEnvelope(responseData, "", "rabbitmqctl", ""),
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
