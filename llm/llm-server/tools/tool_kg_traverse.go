package tools

import (
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolKGTraverse = "kg_traverse"

// LoadBalancer-default exclusions — documented & overridable by the LLM.
// Used only when direction="both" AND node_types includes LoadBalancer AND
// the caller omits exclude_node_types entirely.
var kgTraverseLoadBalancerAutoExclude = []string{"SecurityGroup", "NetworkInterface", "Subnet"}

const (
	kgTraverseDefaultResultLimit = 50
	kgTraverseMaxResultLimit     = 200
	kgTraverseDefaultMaxDepth    = 1
	kgTraverseMaxMaxDepth        = 3
)

func init() {
	core.RegisterNBToolFactory(ToolKGTraverse, func(accountId string) (core.NBTool, error) {
		return KGTraverseTool{accountId: accountId}, nil
	})
}

// KGTraverseTool exposes the Knowledge Graph `kg_traverse` Hasura action as an
// LLM tool for directional multi-hop infrastructure topology exploration.
type KGTraverseTool struct {
	accountId string
}

func (t KGTraverseTool) Name() string             { return ToolKGTraverse }
func (t KGTraverseTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t KGTraverseTool) Description() string {
	return `Traverse the infrastructure knowledge graph to explore dependencies, hosting, connectivity, and service call chains. ` +
		`This is the PRIMARY tool for dependency and topology questions. ` +
		`downstream = "what does X depend on / call / connect to"; upstream = "what depends on / calls / connects to X"; both = bidirectional. ` +
		`The KG includes CALLS edges (service-to-service call relationships) in addition to infrastructure topology. ` +
		`Use service_dependency_graph ONLY when you need runtime METRICS (latency, error rates, traffic volume) — the KG does not carry metrics. ` +
		`Seeding: provide either (a) query (name or %-pattern) for inline search, or (b) node_id / node_ids returned by kg_search_nodes to skip re-resolution. node_id wins if both are provided. ` +
		`Relationship types (exact spelling): ` +
		`Calls: CALLS, PUBLISHES_TO, SUBSCRIBES_TO. ` +
		`Dependency: RUNS_ON, PULLS_FROM, IS_CONFIGURED_BY, MOUNTS, IS_BOUND_TO, RUNS_AS, ASSUMES, IS_ENCRYPTED_BY. ` +
		`Provision: EXPOSES, MANAGES, PROVIDES_STORAGE. ` +
		`Hosting: HOSTED_ON, BELONGS_TO, RUNS_IN. ` +
		`Routing: ROUTES_TO, ROUTES_THROUGH, ROUTES_TO_SERVICE, ROUTES_TO_BACKEND. ` +
		`Lifecycle: BUILT_FROM, ASSOCIATED_WITH. ` +
		`TIP: omit relationship_types to return ALL edges. ` +
		`For LoadBalancer direction=both queries SecurityGroup/NetworkInterface/Subnet are auto-excluded; pass exclude_node_types=[] to see the full subgraph when debugging security-groups / subnets / NICs. ` +
		`Edges returned are the BFS traversal edges (seed -> neighbor at each hop) — sibling-to-sibling edges between two depth-1 neighbors are excluded so dependency listings stay focused on what was actually walked from the seed. ` +
		`If the response contains truncated:true, refine the query (add namespace/node_types, reduce max_depth) before acting.`
}

func (t KGTraverseTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"query": {
				Type:        core.ToolSchemaTypeString,
				Description: `Starting point: node name (exact) or ILIKE pattern containing %. Example: "llm-server", "prod-lb%". Provide this OR node_id/node_ids.`,
			},
			"node_id": {
				Type:        core.ToolSchemaTypeString,
				Description: `Starting point: a single node ID returned by kg_search_nodes. Use this to skip re-resolution by name. Provide this OR query.`,
			},
			"node_ids": {
				Type:        core.ToolSchemaTypeArray,
				Description: `Starting points: multiple node IDs returned by kg_search_nodes for fan-out traversal. Provide this OR query.`,
				Items:       map[string]any{"type": "string"},
			},
			"direction": {
				Type:        core.ToolSchemaTypeString,
				Description: `Traversal direction. downstream = follow outgoing edges; upstream = follow incoming edges; both = bidirectional.`,
				Enum:        []any{"downstream", "upstream", "both"},
			},
			"node_types": {
				Type:        core.ToolSchemaTypeArray,
				Description: `Filter the starting node type, e.g. ["Workload"], ["Namespace"], ["LoadBalancer"].`,
				Items:       map[string]any{"type": "string"},
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Filter starting nodes by namespace.",
			},
			"max_depth": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Traversal depth, 1-3. Default 1.",
			},
			"relationship_types": {
				Type:        core.ToolSchemaTypeArray,
				Description: `OPTIONAL. Filter edges by relationship type (exact spelling). Omit to return all edges.`,
				Items:       map[string]any{"type": "string"},
			},
			"exclude_node_types": {
				Type: core.ToolSchemaTypeArray,
				Description: `OPTIONAL. Exclude nodes of these types. When direction=both AND node_types includes LoadBalancer ` +
					`the default is ["SecurityGroup","NetworkInterface","Subnet"]. Pass [] to disable the default ` +
					`(required for SG / subnet / NIC investigations).`,
				Items: map[string]any{"type": "string"},
			},
			"result_limit": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "OPTIONAL. Cap on returned nodes. Default 50, max 200. truncated:true in the response means you hit the cap — refine before acting.",
			},
		},
		Required: []string{"direction"},
	}
}

type kgTraverseInput struct {
	Query             string   `json:"query"`
	NodeID            string   `json:"node_id"`
	NodeIDs           []string `json:"node_ids"`
	Direction         string   `json:"direction"`
	NodeTypes         []string `json:"node_types"`
	Namespace         string   `json:"namespace"`
	MaxDepth          int      `json:"max_depth"`
	RelationshipTypes []string `json:"relationship_types"`
	// ExcludeNodeTypes is a *[]string so we can distinguish "omitted" from "explicit empty".
	ExcludeNodeTypes       *[]string `json:"exclude_node_types,omitempty"`
	ExcludeNodeTypesRaw    any       `json:"-"`
	excludeNodeTypesPassed bool      `json:"-"`
	ResultLimit            int       `json:"result_limit,omitempty"`
}

func (t KGTraverseTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbCtx.Ctx.GetLogger().Info("kg_traverse: executing", "input", slog.AnyValue(input))

	parsed, err := parseKGTraverseInput(input)
	if err != nil {
		return core.NBToolResponse{}, err
	}
	seedIDs := resolveSeedIDs(parsed)
	if parsed.Query == "" && len(seedIDs) == 0 {
		return core.NBToolResponse{}, errors.New("kg_traverse: provide either query or node_id/node_ids")
	}
	if parsed.Direction == "" {
		return core.NBToolResponse{}, errors.New(`kg_traverse: direction is required ("downstream", "upstream", or "both")`)
	}
	if parsed.Direction != "downstream" && parsed.Direction != "upstream" && parsed.Direction != "both" {
		return core.NBToolResponse{}, errors.New(`kg_traverse: invalid direction — must be "downstream", "upstream", or "both"`)
	}

	// Resolve max_depth.
	depth := parsed.MaxDepth
	if depth <= 0 {
		depth = kgTraverseDefaultMaxDepth
	}
	if depth > kgTraverseMaxMaxDepth {
		depth = kgTraverseMaxMaxDepth
	}

	// Resolve result_limit (clamp to [1, max]).
	limit := parsed.ResultLimit
	if limit <= 0 {
		limit = kgTraverseDefaultResultLimit
	}
	if limit > kgTraverseMaxResultLimit {
		limit = kgTraverseMaxResultLimit
	}

	// Resolve exclude_node_types: caller-provided value wins (including empty).
	// Default auto-exclude applies ONLY when omitted AND direction=both AND node_types includes LoadBalancer.
	var excludes []string
	if parsed.excludeNodeTypesPassed {
		if parsed.ExcludeNodeTypes != nil {
			excludes = *parsed.ExcludeNodeTypes
		} else {
			excludes = []string{} // explicit null → empty
		}
	} else if parsed.Direction == "both" && containsFold(parsed.NodeTypes, "LoadBalancer") {
		excludes = append(excludes, kgTraverseLoadBalancerAutoExclude...)
	}

	apiParams := map[string]any{
		"direction": parsed.Direction,
		"max_depth": depth,
		"max_nodes": limit,
	}
	// Seeding: node_ids (Mode 1) wins over query (Mode 2) when both present —
	// IDs are unambiguous, names need a search round-trip.
	if len(seedIDs) > 0 {
		apiParams["node_ids"] = seedIDs
	} else if strings.Contains(parsed.Query, "%") {
		apiParams["name_pattern"] = parsed.Query
	} else {
		apiParams["name"] = parsed.Query
	}
	if len(parsed.NodeTypes) > 0 {
		apiParams["search_node_types"] = parsed.NodeTypes
	}
	if parsed.Namespace != "" {
		apiParams["namespace"] = parsed.Namespace
	}
	if len(parsed.RelationshipTypes) > 0 {
		apiParams["relationship_types"] = parsed.RelationshipTypes
	}
	if len(excludes) > 0 {
		apiParams["exclude_node_types"] = excludes
	}

	data, err := doKGActionRequest(*nbCtx.Ctx, "kg_traverse", nbCtx.AccountId, apiParams)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	formatted := formatKGTraverseResponse(data, excludes, limit)

	return core.NBToolResponse{
		Data:              formatted,
		Type:              core.NBToolResponseTypeJson,
		Status:            core.NBToolResponseStatusSuccess,
		AdditionalDetails: data,
	}, nil
}

func parseKGTraverseInput(input core.NBToolCallRequest) (kgTraverseInput, error) {
	out := kgTraverseInput{}

	// Support JSON in Command and flat args.
	if strings.HasPrefix(strings.TrimSpace(input.Command), "{") {
		// First unmarshal into a flexible map so we can detect whether
		// exclude_node_types was provided (distinguishing omitted from explicit []).
		var raw map[string]any
		if err := common.UnmarshalJson([]byte(input.Command), &raw); err != nil {
			return out, errors.New("kg_traverse: invalid command JSON: " + err.Error())
		}
		if err := common.UnmarshalJson([]byte(input.Command), &out); err != nil {
			return out, errors.New("kg_traverse: invalid command JSON: " + err.Error())
		}
		if v, present := raw["exclude_node_types"]; present {
			out.excludeNodeTypesPassed = true
			out.ExcludeNodeTypesRaw = v
			if arr, ok := v.([]any); ok {
				s := make([]string, 0, len(arr))
				for _, e := range arr {
					if str, ok := e.(string); ok {
						s = append(s, str)
					}
				}
				out.ExcludeNodeTypes = &s
			} else if v == nil {
				empty := []string{}
				out.ExcludeNodeTypes = &empty
			}
		}
	} else {
		out.Query = input.Command
	}

	// Fill from flat Arguments if not already set via Command JSON.
	if q, ok := input.Arguments["query"].(string); ok && out.Query == "" {
		out.Query = q
	}
	if d, ok := input.Arguments["direction"].(string); ok && out.Direction == "" {
		out.Direction = d
	}
	if id, ok := input.Arguments["node_id"].(string); ok && out.NodeID == "" {
		out.NodeID = id
	}
	if ids, ok := input.Arguments["node_ids"].([]any); ok && len(out.NodeIDs) == 0 {
		for _, v := range ids {
			if s, ok := v.(string); ok && s != "" {
				out.NodeIDs = append(out.NodeIDs, s)
			}
		}
	}
	if ns, ok := input.Arguments["namespace"].(string); ok && out.Namespace == "" {
		out.Namespace = ns
	}
	if md, ok := input.Arguments["max_depth"]; ok && out.MaxDepth == 0 {
		out.MaxDepth = intFromAny(md)
	}
	if rl, ok := input.Arguments["result_limit"]; ok && out.ResultLimit == 0 {
		out.ResultLimit = intFromAny(rl)
	}
	if nts, ok := input.Arguments["node_types"].([]any); ok && len(out.NodeTypes) == 0 {
		for _, v := range nts {
			if s, ok := v.(string); ok {
				out.NodeTypes = append(out.NodeTypes, s)
			}
		}
	}
	if rts, ok := input.Arguments["relationship_types"].([]any); ok && len(out.RelationshipTypes) == 0 {
		for _, v := range rts {
			if s, ok := v.(string); ok {
				out.RelationshipTypes = append(out.RelationshipTypes, s)
			}
		}
	}
	if v, present := input.Arguments["exclude_node_types"]; present && !out.excludeNodeTypesPassed {
		out.excludeNodeTypesPassed = true
		out.ExcludeNodeTypesRaw = v
		if arr, ok := v.([]any); ok {
			s := make([]string, 0, len(arr))
			for _, e := range arr {
				if str, ok := e.(string); ok {
					s = append(s, str)
				}
			}
			out.ExcludeNodeTypes = &s
		} else if v == nil {
			empty := []string{}
			out.ExcludeNodeTypes = &empty
		}
	}

	return out, nil
}

// resolveSeedIDs collapses node_id (singular) and node_ids (array) into a
// single deduplicated slice. node_id is appended last so an explicit array
// takes priority on ordering when both are provided.
func resolveSeedIDs(in kgTraverseInput) []string {
	seen := make(map[string]struct{}, len(in.NodeIDs)+1)
	out := make([]string, 0, len(in.NodeIDs)+1)
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range in.NodeIDs {
		add(id)
	}
	add(in.NodeID)
	return out
}

func containsFold(ss []string, target string) bool {
	for _, s := range ss {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}
