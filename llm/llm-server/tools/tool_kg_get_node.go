package tools

import (
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolKGGetNode = "kg_get_node"

func init() {
	core.RegisterNBToolFactory(ToolKGGetNode, func(accountId string) (core.NBTool, error) {
		return KGGetNodeTool{accountId: accountId}, nil
	})
}

// KGGetNodeTool exposes the Knowledge Graph `kg_get_node` Hasura action as an
// LLM tool for fetching the full payload of a single node by ID — the
// drill-down companion to kg_search_nodes / kg_traverse.
type KGGetNodeTool struct {
	accountId string
}

func (t KGGetNodeTool) Name() string             { return ToolKGGetNode }
func (t KGGetNodeTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t KGGetNodeTool) Description() string {
	return `Fetch the full payload of a single Knowledge Graph node by ID — the drill-down companion to kg_search_nodes and kg_traverse. ` +
		`Returns the complete node detail including properties, labels, source, category, level, and timestamps. ` +
		`The KG does not carry runtime metrics — use service_dependency_graph for latency / error-rate / traffic-volume data. ` +
		`Typical chain: kg_search_nodes (find candidate IDs) → kg_get_node (inspect one in detail), or kg_traverse (locate a node in the topology) → kg_get_node (inspect properties).`
}

func (t KGGetNodeTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"node_id": {
				Type:        core.ToolSchemaTypeString,
				Description: `Node ID (uuid) returned by kg_search_nodes or kg_traverse.`,
			},
		},
		Required: []string{"node_id"},
	}
}

func (t KGGetNodeTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbCtx.Ctx.GetLogger().Info("kg_get_node: executing", "input", slog.AnyValue(input))

	nodeID := parseKGGetNodeInput(input)
	if nodeID == "" {
		return core.NBToolResponse{}, errors.New("kg_get_node: node_id is required")
	}

	data, err := doKGActionRequest(*nbCtx.Ctx, "kg_get_node", nbCtx.AccountId, map[string]any{"node_id": nodeID})
	if err != nil {
		return core.NBToolResponse{}, err
	}

	formatted := formatKGGetNodeResponse(data)

	return core.NBToolResponse{
		Data:              formatted,
		Type:              core.NBToolResponseTypeJson,
		Status:            core.NBToolResponseStatusSuccess,
		AdditionalDetails: data,
	}, nil
}

// parseKGGetNodeInput accepts JSON Command (`{"node_id":"..."}`),
// plain-string Command (treated as the node_id directly), or flat Arguments
// with a "node_id" key. Returns empty string if no node_id can be resolved.
func parseKGGetNodeInput(input core.NBToolCallRequest) string {
	cmd := strings.TrimSpace(input.Command)

	if strings.HasPrefix(cmd, "{") {
		var parsed struct {
			NodeID string `json:"node_id"`
		}
		if err := common.UnmarshalJson([]byte(cmd), &parsed); err == nil && parsed.NodeID != "" {
			return parsed.NodeID
		}
	} else if cmd != "" {
		return cmd
	}

	if id, ok := input.Arguments["node_id"].(string); ok && id != "" {
		return id
	}
	return ""
}
