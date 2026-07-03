package tools

import (
	"errors"
	"fmt"
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

// KGSearchNodesTool exposes the Knowledge Graph `kg_search_nodes` RPC action
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
		`Pods are read directly from the live k8s_pods table at search/traversal time (seconds-fresh), and also surface as neighbors when you traverse from a K8s Node or Workload (RUNS_ON / MANAGES). ` +
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
				Description: `Node name (exact) or ILIKE pattern containing % (e.g. "llm-server", "redis%"). May be empty when at least one other filter (node_types / namespace / source / labels / account_ids) is supplied — that filter set alone will match.`,
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
			"limit": {
				Type:        core.ToolSchemaTypeInteger,
				Description: `OPTIONAL. Max rows to return (default 20, max 100). When the header says "Found N (showing M)" with N>M the result was capped — raise limit, add filters, or summarize by category; do NOT present the page as complete.`,
			},
		},
		// query is intentionally NOT required at the schema level. The
		// framework's required-field validator treats "field present but
		// empty" as MISSING (see executor_planner.go:3049), which contradicts
		// this tool's contract that an empty query is valid when filters are
		// supplied. Semantic validation (must supply query OR at least one
		// filter) lives inline in Call() below — 2 of 3 kg_search_nodes
		// errors in the 2026-07-02 sweep were the model correctly following
		// the description ("Empty string to list all matching the other
		// filters") and getting rejected here.
		Required: []string{},
	}
}

type kgSearchInput struct {
	Query      string   `json:"query"`
	NodeTypes  []string `json:"node_types"`
	Namespace  string   `json:"namespace"`
	Source     string   `json:"source"`
	Labels     string   `json:"labels"`
	AccountIDs []string `json:"account_ids"`
	Limit      int      `json:"limit"`
}

func (t KGSearchNodesTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbCtx.Ctx.GetLogger().Info("kg_search_nodes: executing", "input", slog.AnyValue(input))

	parsed, err := parseKGSearchInput(input)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	if reject := validateKGSearchInput(parsed); reject != nil {
		return *reject, nil
	}

	// Fetch the tenant's cloud_account map exactly once. We need it for two
	// things and previously hit the DB twice: (a) resolving friendly account
	// names in `account_ids` to UUIDs before the backend sees them, and (b)
	// rendering the Account column with names instead of raw UUIDs. Failure is
	// non-fatal on its own — only fatal if the caller actually passed
	// account_ids (where the resolution would silently drop or mis-resolve).
	nameMap, nameErr := fetchTenantAccountMap(*nbCtx.Ctx, nbCtx.AccountId)
	if nameErr != nil {
		nbCtx.Ctx.GetLogger().Debug("kg_search_nodes: account-name map unavailable; falling back to UUIDs", "error", nameErr)
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
		// Accept friendly account names from the LLM and resolve to UUIDs before
		// the backend sees them. The backend column is uuid-typed, so passing
		// "aws-demo" directly errors with "pq: invalid input syntax". If the map
		// fetch failed, we can't safely resolve — surface the underlying error
		// so the LLM doesn't proceed against an unscoped tenant boundary.
		if nameErr != nil {
			return core.NBToolResponse{}, fmt.Errorf("kg_search_nodes: %w", nameErr)
		}
		resolved, rerr := resolveAccountIdentifiers(parsed.AccountIDs, nameMap)
		if rerr != nil {
			return core.NBToolResponse{}, fmt.Errorf("kg_search_nodes: %w", rerr)
		}
		apiParams["account_ids"] = resolved

	}
	if parsed.Limit > 0 {
		apiParams["limit"] = parsed.Limit
	}

	data, err := doKGActionRequest(*nbCtx.Ctx, "kg_search_nodes", nbCtx.AccountId, apiParams)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	// Reuse the map fetched above for column rendering. nil is fine —
	// accountDisplay falls back to UUIDs when the map is missing or partial.
	formatted := formatKGSearchResponse(data, nameMap)

	return core.NBToolResponse{
		Data:              formatted,
		Type:              core.NBToolResponseTypeJson,
		Status:            core.NBToolResponseStatusSuccess,
		AdditionalDetails: data,
	}, nil
}

// validateKGSearchInput returns a rejection response when the parsed input is
// semantically incomplete — currently one rule: all-empty (no query AND no
// filters) is an unbounded "list every node in the graph" call that no
// legitimate use case wants. Returns nil to signal "input is fine, proceed".
// Kept as a pure function (no I/O) so unit tests don't need a live logger /
// security context — one of the failure modes the schema-required check
// previously masked from test coverage.
func validateKGSearchInput(parsed kgSearchInput) *core.NBToolResponse {
	if strings.TrimSpace(parsed.Query) == "" &&
		len(parsed.NodeTypes) == 0 &&
		strings.TrimSpace(parsed.Namespace) == "" &&
		strings.TrimSpace(parsed.Source) == "" &&
		strings.TrimSpace(parsed.Labels) == "" &&
		len(parsed.AccountIDs) == 0 {
		return &core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data: "kg_search_nodes: at least one of query, node_types, namespace, source, labels, or account_ids must be non-empty. " +
				"Empty query is only valid when another filter (e.g. node_types=[\"Database\"] + namespace=\"prod\") narrows the search.",
		}
	}
	return nil
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
	if l, ok := input.Arguments["limit"]; ok && out.Limit == 0 {
		out.Limit = intFromAny(l)
	}

	return out, nil
}
