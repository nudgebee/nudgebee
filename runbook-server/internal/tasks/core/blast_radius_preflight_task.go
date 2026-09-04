package core

import (
	"fmt"
	"io"
	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
)

const taskNameBlastRadiusPreflight = "core.blast-radius-preflight"

// risk levels (ordered low → critical).
const (
	riskLow      = "low"
	riskMedium   = "medium"
	riskHigh     = "high"
	riskCritical = "critical"
)

// riskOrder maps a risk level string to a comparable integer.
var riskOrder = map[string]int{
	riskLow:      0,
	riskMedium:   1,
	riskHigh:     2,
	riskCritical: 3,
}

// heavyKinds are node kinds that amplify blast radius — a downstream database
// or cache failing is far more severe than a downstream sidecar container.
var heavyKinds = map[string]bool{
	"Database": true, "Redis": true, "RabbitMQ": true,
	"ExternalService": true, "KafkaCluster": true, "MessageQueue": true,
	"S3Bucket": true, "ElasticSearch": true, "PostgreSQL": true, "MySQL": true,
	"ClickHouse": true, "Cassandra": true,
}

// BlastRadiusPreflightTask is a workflow node that queries the Knowledge Graph
// to enumerate downstream services reachable from a target resource, computes
// a weighted risk score, and optionally blocks workflow execution when the score
// meets or exceeds a configured threshold.
//
// Insert this node immediately before any destructive action (pod restart,
// scale-down, failover, config change) to prevent changes with unexpectedly
// large blast radii from executing unattended.
type BlastRadiusPreflightTask struct{}

func (t *BlastRadiusPreflightTask) GetName() string        { return taskNameBlastRadiusPreflight }
func (t *BlastRadiusPreflightTask) GetDisplayName() string { return "Blast Radius Pre-Flight Check" }
func (t *BlastRadiusPreflightTask) GetDescription() string {
	return "Assess the downstream blast radius of a planned action by traversing the Knowledge Graph. " +
		"Returns a risk score (0–100), lists all affected services, and optionally blocks execution when " +
		"the risk level meets or exceeds a configured threshold."
}

func (t *BlastRadiusPreflightTask) RuntimeNotes() []string {
	return []string{
		"Place this node BEFORE any destructive action (pod-restart, scale-down, failover) to gate it safely.",
		"Set on_high_risk=block and risk_threshold=high to auto-halt when 50+ downstream services are at risk.",
		"Set on_high_risk=warn to always proceed but surface the risk report in workflow output.",
		"Reference {{ Tasks['preflight'].output.affected_nodes }} in a downstream notification node to inform on-call.",
		"A risk_score of 0 means either no downstream services exist or the Knowledge Graph has not been built yet.",
	}
}

func (t *BlastRadiusPreflightTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"resource_name": {
				Type:        types.PropertyTypeString,
				Description: "Name of the resource that is about to be changed (e.g. 'checkout-service', 'redis-master').",
				Required:    true,
				Order:       1,
			},
			"namespace": {
				Type:        types.PropertyTypeString,
				Description: "Kubernetes namespace of the target resource. Leave empty to search across all namespaces.",
				Required:    false,
				Order:       2,
			},
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "Cloud account ID to scope the Knowledge Graph query. Defaults to the workflow's account.",
				Required:    false,
				Order:       3,
			},
			"action_description": {
				Type:        types.PropertyTypeString,
				Description: "Human-readable description of the planned action (e.g. 'scaling down to 1 replica'). Used in the LLM verdict.",
				Required:    false,
				SubType:     "textarea",
				Order:       4,
			},
			"risk_threshold": {
				Type:        types.PropertyTypeString,
				Description: "Minimum risk level that triggers the on_high_risk behaviour. One of: low, medium, high, critical.",
				Required:    false,
				Default:     riskHigh,
				Options:     []string{riskLow, riskMedium, riskHigh, riskCritical},
				Order:       5,
			},
			"on_high_risk": {
				Type:        types.PropertyTypeString,
				Description: "What to do when the computed risk level meets or exceeds risk_threshold. 'block' halts the workflow; 'warn' proceeds with a warning.",
				Required:    false,
				Default:     "warn",
				Options:     []string{"warn", "block"},
				Order:       6,
			},
			"include_llm_verdict": {
				Type:        types.PropertyTypeBoolean,
				Description: "When true, calls the LLM to generate a human-readable verdict and recommendation. Adds latency.",
				Required:    false,
				Default:     false,
				Order:       7,
			},
			"model": {
				Type:        types.PropertyTypeString,
				Description: "Optional LLM model override for the verdict (format: 'provider:model_name').",
				Required:    false,
				Order:       8,
			},
		},
	}
}

func (t *BlastRadiusPreflightTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"risk_score": {
				Type:        types.PropertyTypeNumber,
				Description: "Weighted risk score from 0 (no risk) to 100 (maximum risk).",
			},
			"risk_level": {
				Type:        types.PropertyTypeString,
				Description: "Human-readable risk level: low, medium, high, or critical.",
			},
			"affected_node_count": {
				Type:        types.PropertyTypeInteger,
				Description: "Total number of downstream nodes reachable from the target resource.",
			},
			"affected_nodes": {
				Type:        types.PropertyTypeArray,
				Description: "List of downstream nodes. Each entry has: id, name, kind, namespace, account_id.",
			},
			"is_blocked": {
				Type:        types.PropertyTypeBoolean,
				Description: "True when on_high_risk=block and risk_level >= risk_threshold.",
			},
			"verdict": {
				Type:        types.PropertyTypeString,
				Description: "LLM-generated assessment of the blast radius (empty when include_llm_verdict=false).",
			},
			"recommendation": {
				Type:        types.PropertyTypeString,
				Description: "LLM-generated recommendation for proceeding safely (empty when include_llm_verdict=false).",
			},
		},
	}
}

// Execute runs the blast radius assessment.
func (t *BlastRadiusPreflightTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	resourceName := strings.TrimSpace(stringParam(params, "resource_name"))
	if resourceName == "" {
		return nil, fmt.Errorf("core.blast-radius-preflight: resource_name is required")
	}

	namespace   := stringParam(params, "namespace")
	accountID   := stringParam(params, "account_id")
	if accountID == "" {
		accountID = taskCtx.GetAccountID()
	}
	actionDesc  := stringParam(params, "action_description")
	threshold   := stringParam(params, "risk_threshold")
	if threshold == "" {
		threshold = riskHigh
	}
	onHighRisk  := stringParam(params, "on_high_risk")
	if onHighRisk == "" {
		onHighRisk = "warn"
	}
	includeLLM  := boolParam(params, "include_llm_verdict")

	reqCtx  := taskCtx.GetNewRequestContextForAccount(accountID)
	endpoint := fmt.Sprintf("%s/rpc/knowledge-graph", config.Config.ServiceEndpoint)
	headers := map[string]string{
		"Content-Type":                      "application/json",
		"Accept":                            "application/json",
		"X-ACTION-TOKEN":                    config.Config.ServiceApiServerToken,
		"x-tenant-id":                       reqCtx.GetSecurityContext().GetTenantId(),
		"x-user-id":                         reqCtx.GetSecurityContext().GetUserId(),
	}

	// Step 1 — resolve resource name to Knowledge Graph node IDs.
	nodeIDs, err := kgSearchNodes(endpoint, headers, resourceName, namespace, accountID)
	if err != nil {
		taskCtx.GetLogger().Warn("blast-radius-preflight: kg_list_nodes failed, proceeding without graph data",
			"resource", resourceName, "error", err)
	}

	// Step 2 — traverse downstream up to 3 hops.
	affectedNodes := make([]map[string]any, 0)
	if len(nodeIDs) > 0 {
		affectedNodes, err = kgTraverseDownstream(endpoint, headers, nodeIDs)
		if err != nil {
			taskCtx.GetLogger().Warn("blast-radius-preflight: kg_list_path failed, proceeding without traversal",
				"seed_nodes", nodeIDs, "error", err)
		}
	}

	// Step 3 — compute weighted risk score.
	riskScore, riskLevel := computeBlastRadius(affectedNodes)

	// Step 4 — optional LLM verdict.
	verdict, recommendation := "", ""
	if includeLLM {
		verdict, recommendation = generateBlastRadiusVerdict(
			taskCtx, resourceName, namespace, actionDesc, affectedNodes, riskLevel,
			stringParam(params, "model"),
		)
	}

	// Step 5 — evaluate block threshold.
	isBlocked := onHighRisk == "block" && riskOrder[riskLevel] >= riskOrder[threshold]

	result := map[string]any{
		"risk_score":          riskScore,
		"risk_level":          riskLevel,
		"affected_node_count": len(affectedNodes),
		"affected_nodes":      affectedNodes,
		"is_blocked":          isBlocked,
		"verdict":             verdict,
		"recommendation":      recommendation,
	}

	taskCtx.GetLogger().Info("blast-radius-preflight: assessment complete",
		"resource", resourceName, "namespace", namespace,
		"risk_score", riskScore, "risk_level", riskLevel,
		"affected_nodes", len(affectedNodes), "is_blocked", isBlocked)

	if isBlocked {
		return result, fmt.Errorf(
			"blast-radius-preflight: execution blocked — risk level %q (score %d) meets or exceeds "+
				"threshold %q with %d downstream services affected; set on_high_risk=warn to proceed",
			riskLevel, riskScore, threshold, len(affectedNodes),
		)
	}

	return result, nil
}

// kgSearchNodes calls kg_list_nodes and returns the IDs of matching nodes.
func kgSearchNodes(endpoint string, headers map[string]string, name, namespace, accountID string) ([]string, error) {
	inputMap := map[string]any{
		"name":  name,
		"limit": 5,
	}
	if namespace != "" {
		inputMap["namespace"] = namespace
	}
	if accountID != "" {
		inputMap["account_ids"] = []string{accountID}
	}

	reqBody := map[string]any{
		"action": map[string]any{"name": "kg_list_nodes"},
		"input":  inputMap,
	}

	body, err := doKGRequest(endpoint, headers, reqBody)
	if err != nil {
		return nil, err
	}

	// Response: {"data": {"nodes": [...], "total_count": N}}
	var resp struct {
		Data struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := common.UnmarshalJson(body, &resp); err != nil {
		return nil, fmt.Errorf("kg_list_nodes: unmarshal failed: %w", err)
	}

	ids := make([]string, 0, len(resp.Data.Nodes))
	for _, n := range resp.Data.Nodes {
		if n.ID != "" {
			ids = append(ids, n.ID)
		}
	}
	return ids, nil
}

// kgTraverseDownstream calls kg_list_path and returns downstream nodes as plain maps.
func kgTraverseDownstream(endpoint string, headers map[string]string, seedNodeIDs []string) ([]map[string]any, error) {
	reqBody := map[string]any{
		"action": map[string]any{"name": "kg_list_path"},
		"input": map[string]any{
			"node_ids":  seedNodeIDs,
			"direction": "downstream",
			"max_depth": 3,
			"max_nodes": 200,
		},
	}

	body, err := doKGRequest(endpoint, headers, reqBody)
	if err != nil {
		return nil, err
	}

	// Response: {"data": {"data": {"nodes": [...KgNodeSlim...]}, "seed_node_ids": [...], ...}}
	var resp struct {
		Data struct {
			Graph struct {
				Nodes []struct {
					ID        string `json:"id"`
					Kind      string `json:"kind"`
					Name      string `json:"name"`
					Namespace string `json:"namespace,omitempty"`
					AccountID string `json:"account_id"`
				} `json:"nodes"`
			} `json:"data"`
			SeedNodeIDs []string `json:"seed_node_ids"`
		} `json:"data"`
	}
	if err := common.UnmarshalJson(body, &resp); err != nil {
		return nil, fmt.Errorf("kg_list_path: unmarshal failed: %w", err)
	}

	// Build a set of seed IDs to exclude the target resource itself.
	seedSet := make(map[string]bool, len(resp.Data.SeedNodeIDs))
	for _, id := range resp.Data.SeedNodeIDs {
		seedSet[id] = true
	}

	nodes := make([]map[string]any, 0, len(resp.Data.Graph.Nodes))
	for _, n := range resp.Data.Graph.Nodes {
		if seedSet[n.ID] {
			continue // skip the target resource itself
		}
		nodes = append(nodes, map[string]any{
			"id":         n.ID,
			"name":       n.Name,
			"kind":       n.Kind,
			"namespace":  n.Namespace,
			"account_id": n.AccountID,
		})
	}
	return nodes, nil
}

// doKGRequest posts a JSON body to the KG endpoint and returns the raw response bytes.
func doKGRequest(endpoint string, headers map[string]string, body map[string]any) ([]byte, error) {
	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(body),
	)
	if err != nil {
		return nil, fmt.Errorf("kg request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kg request: read body failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("kg request returned status %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

// computeBlastRadius scores the downstream node list and returns (score, level).
//
// Scoring rationale:
//   - Each downstream node contributes 2 points (capped at 50).
//   - Each "heavy" node kind (database, cache, queue) adds an extra 10 points (capped at 50).
//   - Total is capped at 100.
//
// Thresholds: 0–24 → low, 25–49 → medium, 50–74 → high, 75–100 → critical.
func computeBlastRadius(nodes []map[string]any) (int, string) {
	baseScore := len(nodes) * 2
	if baseScore > 50 {
		baseScore = 50
	}

	heavyBonus := 0
	for _, n := range nodes {
		kind, _ := n["kind"].(string)
		if heavyKinds[kind] {
			heavyBonus += 10
		}
	}
	if heavyBonus > 50 {
		heavyBonus = 50
	}

	score := baseScore + heavyBonus
	if score > 100 {
		score = 100
	}

	level := riskLow
	switch {
	case score >= 75:
		level = riskCritical
	case score >= 50:
		level = riskHigh
	case score >= 25:
		level = riskMedium
	}
	return score, level
}

// generateBlastRadiusVerdict calls the LLM to produce a human-readable assessment.
// Errors are swallowed — a missing verdict is non-fatal.
func generateBlastRadiusVerdict(
	taskCtx types.TaskContext,
	resourceName, namespace, actionDesc string,
	affectedNodes []map[string]any,
	riskLevel, modelParam string,
) (verdict, recommendation string) {
	affected := buildNodeSummary(affectedNodes)

	prompt := fmt.Sprintf(
		"You are an SRE reviewing a planned change. Provide a concise (3–5 sentence) blast-radius assessment.\n\n"+
			"Target resource: %s (namespace: %s)\n"+
			"Planned action: %s\n"+
			"Risk level: %s\n"+
			"Downstream services affected (%d total):\n%s\n\n"+
			"Respond in two clearly labelled sections:\n"+
			"VERDICT: <one-paragraph assessment of the risk>\n"+
			"RECOMMENDATION: <concrete next steps to mitigate the risk>",
		resourceName, namespace, actionDesc, riskLevel, len(affectedNodes), affected,
	)

	provider, model := parseModelParamString(modelParam)
	resp, err := llm.ProcessRequest(taskCtx.GetNewRequestContext(), llm.LLMRequest{
		Message:      prompt,
		AccountId:    taskCtx.GetAccountID(),
		SessionId:    taskCtx.GetWorkflowRunID(),
		LlmProvider:  provider,
		LlmModelName: model,
	})
	if err != nil {
		taskCtx.GetLogger().Warn("blast-radius-preflight: LLM verdict generation failed", "error", err)
		return "", ""
	}

	raw := resp.Message
	verdict, recommendation = splitVerdictAndRecommendation(raw)
	return verdict, recommendation
}

// buildNodeSummary formats the first 20 affected nodes as a bullet list.
func buildNodeSummary(nodes []map[string]any) string {
	if len(nodes) == 0 {
		return "  (none)"
	}
	limit := len(nodes)
	if limit > 20 {
		limit = 20
	}
	var sb strings.Builder
	for _, n := range nodes[:limit] {
		name, _ := n["name"].(string)
		kind, _ := n["kind"].(string)
		ns, _ := n["namespace"].(string)
		if ns != "" {
			fmt.Fprintf(&sb, "  - %s (%s, ns: %s)\n", name, kind, ns)
		} else {
			fmt.Fprintf(&sb, "  - %s (%s)\n", name, kind)
		}
	}
	if len(nodes) > 20 {
		fmt.Fprintf(&sb, "  ... and %d more\n", len(nodes)-20)
	}
	return sb.String()
}

// splitVerdictAndRecommendation extracts VERDICT and RECOMMENDATION sections from
// the LLM response. Falls back to using the full response as the verdict.
func splitVerdictAndRecommendation(raw string) (verdict, recommendation string) {
	upper := strings.ToUpper(raw)
	verdictIdx := strings.Index(upper, "VERDICT:")
	recIdx := strings.Index(upper, "RECOMMENDATION:")

	switch {
	case verdictIdx >= 0 && recIdx > verdictIdx:
		verdict = strings.TrimSpace(raw[verdictIdx+len("VERDICT:") : recIdx])
		recommendation = strings.TrimSpace(raw[recIdx+len("RECOMMENDATION:"):])
	case verdictIdx >= 0:
		verdict = strings.TrimSpace(raw[verdictIdx+len("VERDICT:"):])
	default:
		verdict = strings.TrimSpace(raw)
	}
	return
}

// parseModelParamString splits "provider:model" into (provider, model).
// Returns empty strings for both if the format is not recognised.
func parseModelParamString(param string) (provider, model string) {
	if param == "" {
		return "", ""
	}
	parts := strings.SplitN(param, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", param
}

// stringParam extracts a string from params, returning "" on type mismatch.
func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// boolParam extracts a bool from params, returning false on type mismatch.
func boolParam(params map[string]any, key string) bool {
	v, _ := params[key].(bool)
	return v
}
