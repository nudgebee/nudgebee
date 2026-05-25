package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

const ToolExecuteServerCommand = "server_command_executor"

func init() {
	core.RegisterNBToolFactory(ToolExecuteServerCommand, func(accountId string) (core.NBTool, error) {
		return ServerExecuteTool{}, nil
	})
}

type ServerExecuteTool struct {
}

func (m ServerExecuteTool) Name() string {
	return ToolExecuteServerCommand
}

func (m ServerExecuteTool) GetNameAliases() []string {
	return []string{"server", "server_command_executor"}
}

func (m ServerExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ServerExecuteTool) Description() string {
	return `Executes Shell commands against the provided Server instance. This tool allows you to gather information about perticular Server instance, enabling you to provide informed assistance and suggestions.

Usage:
Prioritize this tool: Whenever you want to execute any shell command on given Server.
Input: Provide a valid shell command and Server name as input.
Output: The tool will return the output of the executed command.
Examples:
{"instance":"<server-name>", "args":"du -sh /", "command": "shell"} – Retrieves detailed information of disk usage on instance.
{"instance":"<server-name>", "args":"ps -ef | grep -i xyz", "command": "shell"} – Search for process xyz in Server.
{"instance":"<server-name>", "args":"free", "command": "shell"} – Get available memory for for the Server.

Important Notes:
Ensure the shell command is correctly formatted.
Use the output of this tool to inform your responses and suggestions to the user.
Be cautious when running commands that may impact state of Server.`
}

func (m ServerExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Server command to execute (e.g., 'shell')",
			},
			"args": {
				Type:        core.ToolSchemaTypeString,
				Description: "Shell command and arguments to execute",
			},
			"instance": {
				Type:        core.ToolSchemaTypeString,
				Description: "Server instance or hostname to use",
			},
		},
		Required: []string{"command", "instance", "args"},
	}
}

type serverCommand struct {
	Instance string `json:"instance"`
	Args     string `json:"args"`
	Command  string `json:"command"`
}

func (m ServerExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("server: executing Server command", "query", input.Command)
	command := serverCommand{}

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
	if command.Instance == "" || command.Args == "" {
		var cmdMap serverCommand
		err := common.UnmarshalJson([]byte(input.Command), &cmdMap)
		if err == nil {
			if command.Instance == "" {
				command.Instance = cmdMap.Instance
			}
			if command.Args == "" {
				command.Args = cmdMap.Args
			}
			if command.Command == "" {
				command.Command = cmdMap.Command
			}
		}
	}

	if command.Command == "" {
		command.Command = "shell"
	}

	if command.Instance == "" || command.Args == "" {
		return core.NBToolResponse{}, errors.New("missing args or instance field")
	}

	response, err := m.executeShellCommand(nbRequestContext, command.Instance, command.Args)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("server: unable to execute shell script", "error", err.Error())
		return core.NBToolResponse{
			Data:   err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}
	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m ServerExecuteTool) executeShellCommand(nbRequestContext core.NbToolContext, server, command string) (string, error) {
	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		// Use the command as-is, the shim for 'ssh' in the workspace pod will handle the relay call
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("server: unable to execute shell script in workspace", "error", err.Error(), "command", command)
			return response, err
		}

		// Wrap in JSON to be consistent with non-workspace mode
		outputformat := map[string]string{
			"stdout": response,
		}
		outputformatBytes, err := common.MarshalJson(outputformat)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("server: unable to marshal response", "error", err.Error())
			return response, err
		}
		response = string(outputformatBytes)

		return response, nil
	}

	data, err := ExecuteContainerJob(nbRequestContext, RelayJobSSH, command, nbRequestContext.AccountId, map[string]any{}, false)
	if err != nil {
		return "", err
	}
	if dataStr, ok := data.(string); ok {
		return dataStr, nil
	}
	return "", errors.New("unable to parse data")
}

func (m ServerExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {

	prompt := `You are a Linux security expert. Your task is to classify a shell command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: ls -l
	answer: read

	input: cat /etc/passwd
	answer: read

	input: ps aux
	answer: read

	input: df -h
	answer: read

	input: touch newfile.txt
	answer: create

	input: mkdir newdir
	answer: create

	input: useradd newuser
	answer: create

	input: echo "hello" >> file.txt
	answer: update

	input: chmod 755 script.sh
	answer: update

	input: apt-get install nginx
	answer: update

	input: systemctl restart nginx
	answer: update

	input: rm oldfile.txt
	answer: delete

	input: rmdir olddir
	answer: delete

	input: userdel olduser
	answer: delete

	input: kill -9 12345
	answer: delete
	`
	return prompt, nil
}

func (m ServerExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {

	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "host"},
		ConfigType:   "ssh",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "SSH Key of the Server, Required Keys, SSH_KEY, SSH_HOST, SSH_USER",
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "Server Host",
			},
		},
	}
}

func (m ServerExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	instanceName := ""

	// Try to get instance from Arguments
	if input.Arguments != nil {
		if inst, ok := input.Arguments["instance"].(string); ok {
			instanceName = inst
		}
	}

	// Fallback to parsing Command as JSON
	if instanceName == "" {
		command := serverCommand{}
		err := common.UnmarshalJson([]byte(input.Command), &command)
		if err == nil {
			instanceName = command.Instance
		}
	}

	if instanceName == "" {
		return core.ToolConfig{}, errors.New("missing instance json field")
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
				// Fix the backward HasPrefix bug
				if !strings.HasPrefix(trimmedPattern, "(?i)") {
					trimmedPattern = "(?i)" + trimmedPattern
				}
				re, err := regexp.Compile(trimmedPattern)
				if err != nil {
					ctx.Ctx.GetLogger().Warn("server: invalid regex pattern in host config", "pattern", trimmedPattern, "error", err)
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
