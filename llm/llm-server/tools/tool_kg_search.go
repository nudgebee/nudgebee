package tools

import (
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolKGSearchNodes = "kg_search_nodes"

func init() {
	core.RegisterNBToolFactory(ToolKGSearchNodes, func(accountId string) (core.NBTool, error) {
		return KGSearchNodesTool{accountId: accountId}, nil
	})
}

// KGSearchNodesTool exposes the Knowledge Graph `kg_search_nodes` Hasura action
// as an LLM tool so the agent can discover infrastructure resources by name,
// type, namespace, source, or labels.
type KGSearchNodesTool struct {
	accountId string
}

func (t KGSearchNodesTool) Name() string             { return ToolKGSearchNodes }
func (t KGSearchNodesTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t KGSearchNodesTool) Description() string {
	return `Search the infrastructure knowledge graph to find resources by name, type, namespace, source, or labels. ` +
		`Use this as the PRIMARY tool for DISCOVERY (finding what exists — e.g. "list all databases", "find workloads named redis", "what services exist in namespace X"). ` +
		`The KG includes both static infrastructure AND call/dependency relationships (CALLS edges). ` +
		`Use service_dependency_graph ONLY when you need runtime METRICS (latency, error rates, traffic volume) — the KG does not carry metrics. ` +
		`Returned rows include node IDs so you can chain into kg_traverse. ` +
		`Supported node types: ` +
		`Service, Workload, Database, MessageQueue, Queue, Topic, Cache, ExternalService, ComputeInstance, ComputeInstancePool; ` +
		`Cluster, Namespace, Pod, Node, Job, CronJob, CustomResource; ` +
		`LoadBalancer, BackendPool, Storage, VPC, SecurityGroup, Subnet, NetworkInterface, RouteTable, CloudResource, InfraStack; ` +
		`ContainerRegistry, ContainerImage, Artifact, DNSZone, DNSRecord, CDN, NetworkGateway, PrivateEndpoint, APIGateway, SecretVault, EncryptionKey, MonitoringService, LogAggregator, ServerlessFunction, ManagedCluster, BackupVault, BackupPolicy, PublicIP, SecurityService, EmailService, AIService, ServiceIdentity; ` +
		`K8sService, Ingress, NetworkPolicy, ConfigMap, K8sSecret, PersistentVolumeClaim, PersistentVolume; ` +
		`HelmChart, HelmRelease, Configuration, Repository.`
}

func (t KGSearchNodesTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"query": {
				Type:        core.ToolSchemaTypeString,
				Description: `Node name (exact) or ILIKE pattern containing % (e.g. "llm-server", "redis%"). Empty string to list all matching the other filters.`,
			},
			"node_types": {
				Type:        core.ToolSchemaTypeArray,
				Description: `Filter by node types. Examples: ["Workload"], ["Database"], ["LoadBalancer"].`,
				Items:       map[string]any{"type": "string"},
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Kubernetes namespace filter.",
			},
			"source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Data source: k8s, aws, gcp, azure.",
			},
			"labels": {
				Type:        core.ToolSchemaTypeString,
				Description: `JSON label filter, e.g. '{"app":"kibana"}'.`,
			},
			"account_ids": {
				Type:        core.ToolSchemaTypeArray,
				Description: `Filter by cloud account IDs (e.g. AWS account numbers). Example: ["123456789012"].`,
				Items:       map[string]any{"type": "string"},
			},
		},
		Required: []string{"query"},
	}
}

type kgSearchInput struct {
	Query      string   `json:"query"`
	NodeTypes  []string `json:"node_types"`
	Namespace  string   `json:"namespace"`
	Source     string   `json:"source"`
	Labels     string   `json:"labels"`
	AccountIDs []string `json:"account_ids"`
}

func (t KGSearchNodesTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbCtx.Ctx.GetLogger().Info("kg_search_nodes: executing", "input", slog.AnyValue(input))

	parsed, err := parseKGSearchInput(input)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	apiParams := map[string]any{}
	// Translate `query` to name (exact) or name_pattern (if it contains %).
	if parsed.Query != "" {
		if strings.Contains(parsed.Query, "%") {
			apiParams["name_pattern"] = parsed.Query
		} else {
			apiParams["name"] = parsed.Query
		}
	}
	if len(parsed.NodeTypes) > 0 {
		apiParams["node_types"] = parsed.NodeTypes
	}
	if parsed.Namespace != "" {
		apiParams["namespace"] = parsed.Namespace
	}
	if parsed.Source != "" {
		apiParams["source"] = parsed.Source
	}
	if parsed.Labels != "" {
		var labels map[string]string
		if lerr := common.UnmarshalJson([]byte(parsed.Labels), &labels); lerr != nil {
			return core.NBToolResponse{}, errors.New("kg_search_nodes: invalid labels JSON — expected map[string]string, got: " + parsed.Labels)
		}
		apiParams["labels"] = labels
	}
	if len(parsed.AccountIDs) > 0 {
		apiParams["account_ids"] = parsed.AccountIDs
	}

	data, err := doKGActionRequest(*nbCtx.Ctx, "kg_search_nodes", nbCtx.AccountId, apiParams)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	formatted := formatKGSearchResponse(data)

	return core.NBToolResponse{
		Data:              formatted,
		Type:              core.NBToolResponseTypeJson,
		Status:            core.NBToolResponseStatusSuccess,
		AdditionalDetails: data,
	}, nil
}

func parseKGSearchInput(input core.NBToolCallRequest) (kgSearchInput, error) {
	out := kgSearchInput{}

	// Support JSON in Command (preferred ReAct shape) and flat args.
	if strings.HasPrefix(strings.TrimSpace(input.Command), "{") {
		if err := common.UnmarshalJson([]byte(input.Command), &out); err != nil {
			return out, errors.New("kg_search_nodes: invalid command JSON: " + err.Error())
		}
	} else {
		out.Query = input.Command
	}

	if ns, ok := input.Arguments["namespace"].(string); ok && out.Namespace == "" {
		out.Namespace = ns
	}
	if src, ok := input.Arguments["source"].(string); ok && out.Source == "" {
		out.Source = src
	}
	if lbl, ok := input.Arguments["labels"].(string); ok && out.Labels == "" {
		out.Labels = lbl
	}
	if q, ok := input.Arguments["query"].(string); ok && out.Query == "" {
		out.Query = q
	}
	if nts, ok := input.Arguments["node_types"].([]any); ok && len(out.NodeTypes) == 0 {
		for _, v := range nts {
			if s, ok := v.(string); ok {
				out.NodeTypes = append(out.NodeTypes, s)
			}
		}
	}
	if aids, ok := input.Arguments["account_ids"].([]any); ok && len(out.AccountIDs) == 0 {
		for _, v := range aids {
			if s, ok := v.(string); ok && s != "" {
				out.AccountIDs = append(out.AccountIDs, s)
			}
		}
	}

	return out, nil
}
