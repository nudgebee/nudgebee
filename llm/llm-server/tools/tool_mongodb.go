package tools

import (
	"fmt"
	"regexp"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
)

const (
	ToolMongoServerStatus      = "mongodb_server_status"
	ToolMongoReplicaSetStatus  = "mongodb_replica_set_status"
	ToolMongoCurrentOperations = "mongodb_current_operations"
)

func init() {
	core.RegisterNBToolFactory(ToolMongoServerStatus, func(accountId string) (core.NBTool, error) {
		return MongoExecuteTool{
			toolName:    ToolMongoServerStatus,
			description: "Returns MongoDB serverStatus output as readable JSON.",
			actionName:  "mongo_server_status",
		}, nil
	})

	core.RegisterNBToolFactory(ToolMongoReplicaSetStatus, func(accountId string) (core.NBTool, error) {
		return MongoExecuteTool{
			toolName:    ToolMongoReplicaSetStatus,
			description: "Returns MongoDB replSetGetStatus output as readable JSON.",
			actionName:  "mongo_repl_status",
		}, nil
	})

	core.RegisterNBToolFactory(ToolMongoCurrentOperations, func(accountId string) (core.NBTool, error) {
		return MongoExecuteTool{
			toolName:    ToolMongoCurrentOperations,
			description: "Returns MongoDB currentOp output as readable JSON.",
			actionName:  "mongo_current_ops",
		}, nil
	})
}

type MongoExecuteTool struct {
	toolName    string
	description string
	actionName  string
}

func (m MongoExecuteTool) Name() string {
	return m.toolName
}

func (m MongoExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m MongoExecuteTool) Description() string {
	return m.description
}

func (m MongoExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"instance": {
				Type:        core.ToolSchemaTypeString,
				Description: "MongoDB integration name or instance hint used to resolve the correct proxy config.",
			},
		},
	}
}

func (m MongoExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	response, err := executeMongoViaProxyAgent(nbRequestContext, m.actionName, nbRequestContext.AccountId, nil)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("mongo: unable to execute mongodb action", "error", err.Error())
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

	responseData, ok := response.(string)
	if !ok {
		return core.NBToolResponse{}, fmt.Errorf("mongo: unexpected response type %T", response)
	}

	responseData = strings.TrimSpace(responseData)
	if responseData == "" {
		return core.NBToolResponse{}, fmt.Errorf("no data returned from MongoDB")
	}

	responseType := core.NBToolResponseTypeText
	if strings.HasPrefix(responseData, "{") || strings.HasPrefix(responseData, "[") {
		responseType = core.NBToolResponseTypeJson
	}

	return core.NBToolResponse{
		Data:   responseData,
		Type:   responseType,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m MongoExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	instanceName := extractMongoInstance(input)

	if instanceName != "" {
		for _, config := range availableConfigs {
			if strings.EqualFold(config.Name, instanceName) {
				return config, nil
			}

			var hostPatterns string
			for _, value := range config.Values {
				if value.Name == "host" {
					hostPatterns = value.Value
					break
				}
			}

			if hostPatterns != "" {
				for _, pattern := range strings.Split(hostPatterns, ";") {
					trimmedPattern := strings.TrimSpace(pattern)
					if trimmedPattern == "" {
						continue
					}
					if !strings.HasPrefix(trimmedPattern, "(?i)") {
						trimmedPattern = "(?i)" + trimmedPattern
					}
					re, err := regexp.Compile(trimmedPattern)
					if err != nil {
						ctx.Ctx.GetLogger().Warn("mongo: invalid regex pattern in host config", "pattern", trimmedPattern, "error", err)
						continue
					}
					if re.MatchString(instanceName) {
						ctx.Ctx.GetLogger().Info("mongo: identified config via instance name matching", "config", config.Name, "instance", instanceName)
						return config, nil
					}
				}
			}
		}
	}

	userQuery := strings.ToLower(ctx.Query)
	if userQuery != "" {
		envKeywords := []string{"dev", "development", "test", "testing", "prod", "production", "stage", "staging", "qa", "uat", "demo", "preprod"}

		for _, config := range availableConfigs {
			if strings.Contains(userQuery, strings.ToLower(config.Name)) {
				ctx.Ctx.GetLogger().Info("mongo: identified config by exact name in query", "config", config.Name, "query", userQuery)
				return config, nil
			}
		}

		for _, keyword := range envKeywords {
			if !strings.Contains(userQuery, keyword) {
				continue
			}

			for _, config := range availableConfigs {
				if strings.Contains(strings.ToLower(config.Name), keyword) {
					ctx.Ctx.GetLogger().Info("mongo: identified config from query keyword", "config", config.Name, "keyword", keyword, "query", userQuery)
					return config, nil
				}
			}
		}
	}

	for _, config := range availableConfigs {
		if config.Tags == nil || userQuery == "" {
			continue
		}
		if env, ok := config.Tags["environment"]; ok && strings.Contains(userQuery, strings.ToLower(env)) {
			ctx.Ctx.GetLogger().Info("mongo: identified config from environment tag", "config", config.Name, "env", env)
			return config, nil
		}
		if purpose, ok := config.Tags["purpose"]; ok && strings.Contains(userQuery, strings.ToLower(purpose)) {
			ctx.Ctx.GetLogger().Info("mongo: identified config from purpose tag", "config", config.Name, "purpose", purpose)
			return config, nil
		}
	}

	ctx.Ctx.GetLogger().Debug("mongo: could not identify config automatically", "query", userQuery, "instance", instanceName, "available_configs", len(availableConfigs))
	return core.ToolConfig{}, nil
}

func extractMongoInstance(input core.NBToolCallRequest) string {
	if input.Arguments != nil {
		if instance, ok := input.Arguments["instance"].(string); ok && instance != "" {
			return instance
		}
	}

	if strings.HasPrefix(strings.TrimSpace(input.Command), "{") {
		parsed := struct {
			Instance string `json:"instance"`
		}{}
		_ = common.UnmarshalJson([]byte(input.Command), &parsed)
		return parsed.Instance
	}

	return ""
}
