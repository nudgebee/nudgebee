package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

const ToolExecuteRedisCommand = "redis_command_executer"

func init() {
	core.RegisterNBToolFactory(ToolExecuteRedisCommand, func(accountId string) (core.NBTool, error) {
		return RedisExecuteTool{}, nil
	})
}

type RedisExecuteTool struct {
}

func (m RedisExecuteTool) Name() string {
	return ToolExecuteRedisCommand
}

func (m RedisExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m RedisExecuteTool) Description() string {
	return `Executes redis-cli commands against the user's Redis instance. This tool allows you to gather information about Redis resources and configuration, enabling you to provide informed assistance and suggestions.

Usage:
Prioritize this tool: Whenever you require information about the user's Redis resources to make decisions or provide accurate responses, use this tool.
Input: Provide a valid redis-cli command as input.
Output: The tool will return the output of the executed command.
Examples:
redis-cli INFO – Retrieves detailed information about the Redis server.
redis-cli KEYS * – Lists all keys in the current database.
redis-cli DBSIZE – Returns the number of keys in the selected database.
redis-cli MONITOR – Streams real-time commands being executed in Redis.
Important Notes:
Ensure the redis-cli command is correctly formatted.
Use the output of this tool to inform your responses and suggestions to the user.
Be cautious when running commands that may impact performance, such as FLUSHALL, which clears all databases.`
}

func (m RedisExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON string with 'command' and 'instance' fields. 'instance' is the host to connect to.",
			},
		},
		Required: []string{"command"},
	}
}

func (m RedisExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("RedisExecuteTool: executing redis command", "query", input.Command)

	var command string
	if strings.HasPrefix(input.Command, "{") {
		jsonCommand := make(map[string]any)
		err := common.UnmarshalJson([]byte(input.Command), &jsonCommand)
		if err != nil {
			return core.NBToolResponse{}, errors.Wrap(err, "invalid input format, expected JSON")
		}
		if cmd, ok := jsonCommand["command"].(string); ok {
			command = cmd
		}
	} else {
		command = input.Command
	}

	if command == "" {
		return core.NBToolResponse{}, errors.New("command is empty")
	}

	command = strings.TrimSpace(command)
	if !strings.Contains(command, "redis-cli") {
		command = fmt.Sprintf("redis-cli %s", command)
	}
	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("redis: unable to execute shell script", "error", err.Error(), "command", command)
			if response == "" {
				response = err.Error()
			}
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}

		// Wrap in JSON to be consistent with non-workspace mode
		outputformat := map[string]string{
			"stdout": response,
		}
		outputformatBytes, err := common.MarshalJson(outputformat)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("redis: unable to marshal response", "error", err.Error())
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}
		response = string(outputformatBytes)

		return core.NBToolResponse{
			Data:   response,
			Type:   core.NBToolResponseTypeText,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobRedis, command, nbRequestContext.AccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("redis: unable to execute shell script", "error", err.Error())
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
		Data:   string(data),
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m RedisExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {

	prompt := `You are a Redis security expert. Your task is to classify a 'redis-cli' command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: redis-cli GET mykey
	answer: read

	input: redis-cli KEYS *
	answer: read

	input: redis-cli PING
	answer: read

	input: redis-cli HELP
	answer: read

	input: redis-cli INFO
	answer: read

	input: redis-cli HGETALL myhash
	answer: read

	input: redis-cli SMEMBERS myset
	answer: read

	input: redis-cli SET mykey "myvalue"
	answer: create

	input: redis-cli LPUSH mylist "world"
	answer: create

	input: redis-cli HSET myhash field1 "Hello"
	answer: create

	input: redis-cli SADD myset "member1"
	answer: create

	input: redis-cli INCR mycounter
	answer: update

	input: redis-cli EXPIRE mykey 10
	answer: update

	input: redis-cli ZADD myzset 1 "one"
	answer: update

	input: redis-cli DEL mykey
	answer: delete

	input: redis-cli FLUSHDB
	answer: delete

	input: redis-cli SREM myset "member1"
	answer: delete
	`
	return prompt, nil
}

func (m RedisExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
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
					ctx.Ctx.GetLogger().Warn("redis: invalid regex pattern in host config", "pattern", trimmedPattern, "error", err)
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

func (m RedisExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {

	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "redis",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "Redis Secret in k8s, Required Keys, REDIS_HOST",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "Redis host",
			},
		},
	}
}
