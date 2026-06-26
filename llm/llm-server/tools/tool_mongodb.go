package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"time"
)

// MVP read-only MongoDB troubleshooting tools. Each maps to a single forager
// MongoDB diagnostic command run through the proxy-agent path (the same path
// SSH/DB proxy datasources use — see executeSSHViaProxyAgent in
// common_relay.go). Unlike the SQL tools, MongoDB returns JSON documents, so
// the response is passed through as text/JSON rather than converted to a table.
const (
	ToolMongoServerStatus  = "mongodb_server_status_get"
	ToolMongoReplSetStatus = "mongodb_replset_status_get"
	ToolMongoCurrentOp     = "mongodb_current_op_get"
)

// mongoOp describes a single read-only MongoDB diagnostic operation: the tool
// name the AI calls, its description, and the dedicated forager action it maps
// to.
type mongoOp struct {
	name        string
	description string
	// action is the forager MongoDB action this tool invokes. forager exposes a
	// distinct, parameter-less action per diagnostic (each hardcodes its own
	// command document, e.g. mongo_server_status -> {serverStatus:1}); the
	// generic `mongo_query` action is a collection find and does NOT accept a
	// command document. These action names must match forager
	// (forager/pkg/proxy/mongodb/proxy.go) and the relay signer allowlist
	// (relay-server/pkg/signing/signer.go).
	action string
}

var mongoOps = map[string]mongoOp{
	ToolMongoServerStatus: {
		name: ToolMongoServerStatus,
		description: `Returns MongoDB serverStatus — connections, memory, operation counters, network and asserts. ` +
			`Use this to inspect overall server health (e.g. connection saturation, memory pressure, op throughput). ` +
			`Read-only. Optional input: JSON object with an 'instance' field naming the MongoDB integration to target.`,
		action: "mongo_server_status",
	},
	ToolMongoReplSetStatus: {
		name: ToolMongoReplSetStatus,
		description: `Returns MongoDB replSetGetStatus — replica-set membership, each member's state, and replication lag. ` +
			`Use this to check whether the replica set is healthy or a secondary is lagging. ` +
			`Read-only. Optional input: JSON object with an 'instance' field naming the MongoDB integration to target.`,
		action: "mongo_repl_status",
	},
	ToolMongoCurrentOp: {
		name: ToolMongoCurrentOp,
		description: `Returns MongoDB currentOp — operations in progress right now. ` +
			`Use this to find long-running or slow operations. ` +
			`Read-only. Optional input: JSON object with an 'instance' field naming the MongoDB integration to target.`,
		action: "mongo_current_ops",
	},
}

// mongoRelayExecute is the relay seam. It defaults to relay.Execute and is a
// package-level var so unit tests can substitute a deterministic fake without
// hitting the network. Tests must restore it afterwards.
var mongoRelayExecute = relay.Execute

func init() {
	for name := range mongoOps {
		n := name // capture
		core.RegisterNBToolFactory(n, func(accountId string) (core.NBTool, error) {
			return MongoDBTool{toolName: n}, nil
		})
	}
}

// MongoDBTool is a read-only MongoDB troubleshooting tool. One instance backs
// each of the three MVP operations, selected by toolName.
type MongoDBTool struct {
	toolName string
}

func (m MongoDBTool) op() mongoOp { return mongoOps[m.toolName] }

func (m MongoDBTool) Name() string { return m.toolName }

func (m MongoDBTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m MongoDBTool) Description() string { return m.op().description }

func (m MongoDBTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"instance": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional. Name/host of the MongoDB integration to target when several are configured.",
			},
		},
	}
}

func (m MongoDBTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	logger := nbRequestContext.Ctx.GetLogger()
	logger.Info("mongodb: executing read-only diagnostic", "tool", m.toolName)

	if nbRequestContext.ToolConfig.Name == "" && nbRequestContext.ToolConfig.Id == "" {
		return core.NBToolResponse{}, fmt.Errorf("no MongoDB integration configured for %s, please configure a MongoDB proxy integration", m.Name())
	}

	data, err := executeMongoViaProxyAgent(nbRequestContext, m.op().action, nbRequestContext.AccountId)
	if err != nil {
		// Propagate the forager error message so the LLM can act on it
		// (unreachable host, auth failure, etc.) — mirrors parseProxySSHResponse.
		logger.Error("mongodb: diagnostic failed", "tool", m.toolName, "error", err.Error())
		return core.NBToolResponse{
			Data:   err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	return core.NBToolResponse{
		Data:   data,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// buildMongoActionParams builds the `params` object for a forager MongoDB
// diagnostic action. These actions (mongo_server_status / mongo_repl_status /
// mongo_current_ops) take no command document — they hardcode their own — so
// the only field forager needs is the datasource to target. The signed payload
// is {action, datasource_id, params}.
func buildMongoActionParams(datasourceKey string) map[string]any {
	return map[string]any{
		"datasource_id": datasourceKey,
	}
}

// executeMongoViaProxyAgent sends a signed forager MongoDB diagnostic request
// via the relay, modeled on executeSSHViaProxyAgent. `action` is the forager
// action (mongo_server_status / mongo_repl_status / mongo_current_ops). MongoDB
// datasources are always reached over the proxy-agent path.
func executeMongoViaProxyAgent(toolContext core.NbToolContext, action string, accountId string) (string, error) {
	// Fail fast if the tenant scope is missing: every proxy action is account-scoped,
	// and an empty accountId would execute without tenant isolation.
	if accountId == "" {
		return "", errors.New("accountId is required for tenant scoping")
	}

	datasourceKey := getConfigValue(toolContext.ToolConfig.Values, "datasource_key")
	if datasourceKey == "" {
		if toolContext.ToolConfig.Id != "" {
			datasourceKey = toolContext.ToolConfig.Id
		} else {
			return "", errors.New("MongoDB integration missing datasource_key config value")
		}
	}

	// MongoDB is a proxy-category integration with no k8s-native execution mode —
	// it is always reached via the forager (proxy) agent. Honor an explicit
	// agent_type override if the config sets one, otherwise default to "proxy".
	agentType := getConfigValue(toolContext.ToolConfig.Values, "agent_type")
	if agentType == "" {
		agentType = "proxy"
	}

	timeoutSeconds := config.Config.LlmServerRelayPodExecutionTimeoutSeconds
	actionParam := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   action,
		ActionParams: buildMongoActionParams(datasourceKey),
		AgentType:    agentType,
		Timeout:      time.Second * time.Duration(timeoutSeconds),
	}

	response, err := mongoRelayExecute(actionParam)
	if err != nil {
		return "", fmt.Errorf("proxy agent %s failed: %w", action, err)
	}

	return parseProxyMongoResponse(response)
}

// parseProxyMongoResponse extracts the MongoDB JSON document(s) from the proxy
// agent's response. Unlike parseProxyDBResponse (SQL columns/rows → table),
// MongoDB returns JSON documents, so this is a JSON pass-through: it surfaces
// any forager error and otherwise returns the raw JSON string in response.data.
func parseProxyMongoResponse(response map[string]any) (string, error) {
	dataStr, ok := response["data"].(string)
	if !ok {
		return "", errors.New("proxy mongo_query response missing 'data' field")
	}

	// Inspect the payload only to surface a forager-side error; otherwise pass
	// the JSON through unchanged so document structure is preserved for the LLM.
	var mongoResult map[string]any
	if err := json.Unmarshal([]byte(dataStr), &mongoResult); err == nil && mongoResult != nil {
		if errMsg, ok := mongoResult["error"].(string); ok && errMsg != "" {
			return "", fmt.Errorf("proxy mongo_query error: %s", errMsg)
		}
	}

	return dataStr, nil
}

func (m MongoDBTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	instanceName := ""
	if input.Arguments != nil {
		if inst, ok := input.Arguments["instance"].(string); ok {
			instanceName = inst
		}
	}
	if instanceName == "" {
		return core.ToolConfig{}, nil
	}
	for _, cfg := range availableConfigs {
		if cfg.Name == instanceName {
			return cfg, nil
		}
		if host := getConfigValue(cfg.Values, "host"); host == instanceName {
			return cfg, nil
		}
	}
	return core.ToolConfig{}, nil
}

func (m MongoDBTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"host"},
		ConfigType:   "mongodb_proxy",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "MongoDB host address",
			},
		},
	}
}
