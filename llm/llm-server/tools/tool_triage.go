package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// Tool name constants for the read-only triage tools. These expose Nudgebee's
// auto-triage heuristics (dedup chains, correlations, scoring, triage rules and
// threshold suggestions) to the events agent so it can explain *why* an event
// was triaged the way it was — not just report the resulting nb_status.
const (
	ToolTriageExplanation    = "get_triage_explanation"
	ToolTriageRules          = "get_triage_rules"
	ToolThresholdSuggestions = "list_threshold_suggestions"
	ToolTriageDryRun         = "dryrun_triage_rule"
	ToolEventClassification  = "get_event_classification"
	ToolTriageRuleEvents     = "get_triage_rule_events"
)

// Bounds on the triage explanation payload. A noisy event can have a very long
// duplicate chain and dozens of correlations; the scratchpad caps a single tool
// observation (~4KB by default), which would truncate the raw payload mid-JSON.
// We keep the most recent occurrences and the strongest correlations, slim each
// entry to the fields needed to reason about causality, and rely on the
// backend-provided total counts to tell the LLM how much was elided.
const (
	maxDuplicateChainEntries = 5
	maxCorrelations          = 8
)

func init() {
	core.RegisterNBToolFactory(ToolTriageExplanation, func(accountId string) (core.NBTool, error) {
		return TriageExplanationTool{}, nil
	})
	core.RegisterNBToolFactory(ToolTriageRules, func(accountId string) (core.NBTool, error) {
		return TriageRulesTool{}, nil
	})
	core.RegisterNBToolFactory(ToolThresholdSuggestions, func(accountId string) (core.NBTool, error) {
		return ThresholdSuggestionsTool{}, nil
	})
	core.RegisterNBToolFactory(ToolTriageDryRun, func(accountId string) (core.NBTool, error) {
		return TriageDryRunTool{}, nil
	})
	core.RegisterNBToolFactory(ToolEventClassification, func(accountId string) (core.NBTool, error) {
		return EventClassificationTool{}, nil
	})
	core.RegisterNBToolFactory(ToolTriageRuleEvents, func(accountId string) (core.NBTool, error) {
		return TriageRuleEventsTool{}, nil
	})
}

// doTriageActionRequest calls an api-server /rpc/triage action and returns the
// raw JSON response body. The triage handlers respond with a plain JSON object
// (no {"data": ...} envelope), so callers can pass the body straight back to the
// LLM. Tenant isolation is enforced server-side from the x-tenant-id header.
//
// Mirrors the api-server RPC-call pattern used by the knowledge-graph tool
// (doKGActionRequest) and services_server/service.go.
func doTriageActionRequest(nbCtx core.NbToolContext, actionName string, input map[string]any) (string, error) {
	tenant := ""
	if nbCtx.Ctx != nil {
		if sc := nbCtx.Ctx.GetSecurityContext(); sc != nil {
			tenant = sc.GetTenantId()
		}
	}
	if tenant == "" {
		// Fail fast on an empty account rather than issuing a pointless
		// (and tenant-ambiguous) DB lookup with id = ''.
		if nbCtx.AccountId == "" {
			return "", errors.New("triage: account id is empty and tenant id is missing")
		}
		t, err := security.GetTenantIdFromAccountId(nbCtx.AccountId)
		if err != nil {
			return "", fmt.Errorf("triage: resolving tenant from account %s: %w", nbCtx.AccountId, err)
		}
		tenant = t
	}
	if tenant == "" {
		return "", errors.New("triage: tenant id is empty")
	}

	userId := ""
	if nbCtx.Ctx != nil {
		if sc := nbCtx.Ctx.GetSecurityContext(); sc != nil {
			userId = sc.GetUserId()
		}
	}

	payload := map[string]any{
		"action": map[string]any{"name": actionName},
		"input":  input,
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/rpc/triage", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   "application/json",
			"Accept":         "application/json",
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    tenant,
			"x-user-id":      userId,
		}),
		common.HttpWithJsonBody(payload),
	)
	if err != nil {
		return "", fmt.Errorf("triage: %s, unable to process request: %w", actionName, err)
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				slog.Info("triage: failed to close response body", "error", cerr)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("triage: %s, reading response body: %w", actionName, err)
	}

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("triage: %s unauthorized: %s", actionName, string(jsonBody))
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("triage: %s failed (status %d): %s", actionName, resp.StatusCode, string(jsonBody))
	}

	trimmed := bytes.TrimLeft(jsonBody, " \t\r\n")
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", fmt.Errorf("triage: %s unexpected response shape: %s", actionName, string(jsonBody))
	}

	return string(jsonBody), nil
}

// triageResponse wraps a successful raw-JSON body into an NBToolResponse.
func triageResponse(data string) core.NBToolResponse {
	return core.NBToolResponse{
		Data:   data,
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}
}

// triageErrorResponse surfaces an error to the LLM as an actionable tool error
// (returned with a nil Go error so the ReAct loop can reflect and recover).
func triageErrorResponse(err error) core.NBToolResponse {
	return core.NBToolResponse{
		Data:   fmt.Sprintf("Error: %s", err.Error()),
		Status: core.NBToolResponseStatusError,
	}
}

// stringArg reads an optional string argument from a tool call.
func stringArg(input core.NBToolCallRequest, key string) string {
	if v, ok := input.Arguments[key].(string); ok {
		return v
	}
	return ""
}

// ---------------------------------------------------------------------------
// get_triage_explanation — why was this event triaged the way it was?
// ---------------------------------------------------------------------------

// TriageExplanationTool returns the combined triage picture for a single event:
// duplicate chain (occurrence number, time-since-first/previous), correlated
// events (root-cause vs downstream), historical stats and the hourly trend.
// Combined with the event's own score_factors column, this answers
// "why is this event P0 / SUPPRESSED / DUPLICATE?".
type TriageExplanationTool struct{}

func (t TriageExplanationTool) Name() string             { return ToolTriageExplanation }
func (t TriageExplanationTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t TriageExplanationTool) Description() string {
	return "Explains how a single event was triaged. Given an event_id, returns its duplicate chain " +
		"(occurrence number, total occurrences, time since first/previous), correlated events " +
		"(likely_root_cause / downstream_impact / upstream_dependency / same_service), historical " +
		"firing stats and the hourly trend. Use this together with the event's score_factors column " +
		"to explain why an event is DUPLICATE/SUPPRESSED or has a given computed_priority."
}

func (t TriageExplanationTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"event_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the event to explain (events.id).",
			},
		},
		Required: []string{"event_id"},
	}
}

func (t TriageExplanationTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t TriageExplanationTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	eventID := stringArg(input, "event_id")
	if eventID == "" {
		return triageErrorResponse(errors.New("event_id is required")), nil
	}
	data, err := doTriageActionRequest(nbCtx, "event_get_triage", map[string]any{"event_id": eventID})
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("triage: get_triage_explanation failed", "error", err, "event_id", eventID)
		return triageErrorResponse(err), nil
	}
	return triageResponse(boundTriageExplanation(data)), nil
}

// boundTriageExplanation trims the duplicate chain and correlation list so the
// payload fits within the scratchpad's per-observation cap. It keeps the most
// recent occurrences and the strongest correlations, leaving the backend's
// total_occurrences / correlation_count intact so the LLM knows the full size.
// On any parse failure it returns the original data unchanged.
func boundTriageExplanation(data string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return data
	}

	if di, ok := m["duplicate_info"].(map[string]any); ok {
		if chain, ok := di["duplicate_chain"].([]any); ok {
			full := len(chain)
			if full > maxDuplicateChainEntries {
				chain = chain[full-maxDuplicateChainEntries:] // most recent occurrences
				di["duplicate_chain_truncated"] = true
				di["duplicate_chain_shown"] = len(chain)
			}
			slim := make([]any, 0, len(chain))
			for _, e := range chain {
				slim = append(slim, pick(e, "occurrence_number", "event_starts_at", "event_state"))
			}
			di["duplicate_chain"] = slim
		}
	}

	if corr, ok := m["correlated_events"].([]any); ok {
		sort.SliceStable(corr, func(i, j int) bool {
			return correlationScore(corr[i]) > correlationScore(corr[j])
		})
		if len(corr) > maxCorrelations {
			corr = corr[:maxCorrelations]
			m["correlated_events_truncated"] = true
			m["correlated_events_shown"] = len(corr)
		}
		slim := make([]any, 0, len(corr))
		for _, e := range corr {
			slim = append(slim, pick(e, "correlated_title", "correlation_type", "correlation_score",
				"correlation_reason", "subject_name", "time_offset_minutes"))
		}
		m["correlated_events"] = slim
	}

	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return string(out)
}

// pick returns a new map containing only the named keys present in v.
func pick(v any, keys ...string) any {
	src, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if val, ok := src[k]; ok {
			out[k] = val
		}
	}
	return out
}

// correlationScore reads correlation_score from a correlated-event map (0 if absent).
func correlationScore(v any) float64 {
	if c, ok := v.(map[string]any); ok {
		if s, ok := c["correlation_score"].(float64); ok {
			return s
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// get_triage_rules — what auto-triage rules are configured?
// ---------------------------------------------------------------------------

// TriageRulesTool lists the triage rules (suppression / scoring / classification)
// active for the current account/tenant — system defaults plus account/tenant
// custom rules. Use it to assess rule coverage gaps and to avoid proposing a
// rule that already exists.
type TriageRulesTool struct{}

func (t TriageRulesTool) Name() string             { return ToolTriageRules }
func (t TriageRulesTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t TriageRulesTool) Description() string {
	return "Lists the configured triage rules for the current account/tenant (system defaults + " +
		"custom rules). Rule types: 'suppression' (suppress/drop noisy events), 'scoring' " +
		"(adjust computed_score, e.g. service-tier bonuses), 'classification' (auto-classify " +
		"duplicates/false-positives). Optional filters: rule_type, enabled. Use this to find " +
		"coverage gaps and to check whether a rule already exists before proposing a new one."
}

func (t TriageRulesTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"rule_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional filter by rule type.",
				Enum:        []any{"suppression", "scoring", "classification"},
			},
			"enabled": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Optional filter: only enabled (true) or only disabled (false) rules.",
			},
		},
		Required: []string{},
	}
}

func (t TriageRulesTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t TriageRulesTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	in := map[string]any{}
	if nbCtx.AccountId != "" {
		in["cloud_account_id"] = nbCtx.AccountId
	}
	if rt := stringArg(input, "rule_type"); rt != "" {
		in["rule_type"] = rt
	}
	if v, ok := input.Arguments["enabled"].(bool); ok {
		in["enabled"] = v
	}
	data, err := doTriageActionRequest(nbCtx, "event_get_triage_rules", in)
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("triage: get_triage_rules failed", "error", err)
		return triageErrorResponse(err), nil
	}
	return triageResponse(data), nil
}

// ---------------------------------------------------------------------------
// list_threshold_suggestions — which noisy alerts can be tuned?
// ---------------------------------------------------------------------------

// ThresholdSuggestionsTool lists pre-computed threshold-tuning suggestions for
// noisy alerts (recommendation_type, confidence, estimated_reduction, the
// p50/p90/p95/p99/MAD metric stats and the reason). These are advisory — the
// threshold lives in the customer's external alerting system — so the tool is
// read-only and the agent should recommend, not apply.
type ThresholdSuggestionsTool struct{}

func (t ThresholdSuggestionsTool) Name() string             { return ToolThresholdSuggestions }
func (t ThresholdSuggestionsTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t ThresholdSuggestionsTool) Description() string {
	return "Lists pre-computed threshold-tuning suggestions for noisy alerts. Each suggestion has a " +
		"current vs suggested threshold, recommendation_type (tune_threshold/tune_both/" +
		"increase_duration/disable/review_alert), confidence (high/medium/low), estimated_reduction " +
		"(% of firings eliminated) and metric stats. Treat low-confidence flat-metric (MAD=0) rows " +
		"as weak signals. Optional filters: source, confidence, limit. These are advisory only — " +
		"recommend changes, do not claim to apply them."
}

func (t ThresholdSuggestionsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional filter by alert source (e.g. prometheus, pagerduty_webhook, GCP_Metric_Alert).",
			},
			"confidence": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional filter by confidence level.",
				Enum:        []any{"high", "medium", "low"},
			},
			"limit": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Maximum number of suggestions to return (default 20).",
			},
		},
		Required: []string{},
	}
}

func (t ThresholdSuggestionsTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t ThresholdSuggestionsTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	in := map[string]any{}
	if nbCtx.AccountId != "" {
		in["cloud_account_id"] = nbCtx.AccountId
	}
	if s := stringArg(input, "source"); s != "" {
		in["source"] = s
	}
	if cf := stringArg(input, "confidence"); cf != "" {
		in["confidence"] = cf
	}
	if v, ok := input.Arguments["limit"].(float64); ok && v > 0 {
		in["limit"] = v
	}
	data, err := doTriageActionRequest(nbCtx, "event_list_threshold_suggestions", in)
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("triage: list_threshold_suggestions failed", "error", err)
		return triageErrorResponse(err), nil
	}
	return triageResponse(data), nil
}

// ---------------------------------------------------------------------------
// dryrun_triage_rule — preview a rule's impact before recommending it
// ---------------------------------------------------------------------------

// TriageDryRunTool previews how many existing events a candidate triage rule
// would match, so the agent can quantify a proposed rule's volume reduction
// *before* recommending the user create it in the UI. It does not create the
// rule (read-only).
type TriageDryRunTool struct{}

func (t TriageDryRunTool) Name() string             { return ToolTriageDryRun }
func (t TriageDryRunTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t TriageDryRunTool) Description() string {
	return "Previews the impact of a candidate triage rule WITHOUT creating it — returns how many " +
		"existing events match the criteria (projected volume reduction). Required: rule_type " +
		"(suppression/scoring/classification) and action (e.g. suppress, drop, adjust_score). " +
		"Provide one or more match criteria: match_source, match_alertname (regex on aggregation_key), " +
		"match_namespace, match_service (regex on subject_owner), match_fingerprint, match_priority, " +
		"match_finding_type, match_labels. Use this to quantify a proposed rule before recommending " +
		"the user create it in the UI."
}

func (t TriageDryRunTool) InputSchema() core.ToolSchema {
	str := func(desc string) core.ToolSchemaProperty {
		return core.ToolSchemaProperty{Type: core.ToolSchemaTypeString, Description: desc}
	}
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"rule_type":          {Type: core.ToolSchemaTypeString, Description: "Rule type.", Enum: []any{"suppression", "scoring", "classification"}},
			"action":             str("Rule action, e.g. 'suppress', 'drop', 'adjust_score', 'auto_classify_duplicate', 'auto_classify_fp'."),
			"match_source":       str("Optional: exact match on event source."),
			"match_alertname":    str("Optional: regex match on aggregation_key."),
			"match_namespace":    str("Optional: exact match on subject_namespace."),
			"match_service":      str("Optional: regex match on subject_owner."),
			"match_fingerprint":  str("Optional: exact match on fingerprint."),
			"match_priority":     str("Optional: exact match on priority."),
			"match_finding_type": str("Optional: exact match on finding_type."),
			"match_labels":       str("Optional: JSON object string for label containment match."),
		},
		Required: []string{"rule_type", "action"},
	}
}

func (t TriageDryRunTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	// Read-only: previewing a rule does not create it.
	return core.ToolRequestTypeRead, nil
}

func (t TriageDryRunTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if nbCtx.AccountId == "" {
		return triageErrorResponse(errors.New("an account context is required to dry-run a triage rule")), nil
	}
	ruleType := stringArg(input, "rule_type")
	action := stringArg(input, "action")
	if ruleType == "" || action == "" {
		return triageErrorResponse(errors.New("rule_type and action are required")), nil
	}

	in := map[string]any{
		"cloud_account_id": nbCtx.AccountId,
		"rule_type":        ruleType,
		"action":           action,
	}
	for _, k := range []string{
		"match_source", "match_alertname", "match_namespace", "match_service",
		"match_fingerprint", "match_priority", "match_finding_type", "match_labels",
	} {
		if v := stringArg(input, k); v != "" {
			in[k] = v
		}
	}

	data, err := doTriageActionRequest(nbCtx, "events_dryrun_triage_rule", in)
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("triage: dryrun_triage_rule failed", "error", err)
		return triageErrorResponse(err), nil
	}
	return triageResponse(data), nil
}

// ---------------------------------------------------------------------------
// get_event_classification — how was this event classified, and why?
// ---------------------------------------------------------------------------

// EventClassificationTool returns the explicit classification verdict for an
// event (true_positive / false_positive / benign_positive / duplicate) with its
// reason_code, linked_event_id and the rule that produced it. This is distinct
// from get_triage_explanation (which gives the dedup chain / correlations /
// score) — it answers "what did we decide this event IS, and why".
type EventClassificationTool struct{}

func (t EventClassificationTool) Name() string             { return ToolEventClassification }
func (t EventClassificationTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t EventClassificationTool) Description() string {
	return "Returns the classification verdict recorded for an event — true_positive, " +
		"false_positive, benign_positive or duplicate — with its reason_code, linked_event_id " +
		"and the triage rule that produced it (if any). Use this to answer how an event was " +
		"classified and why. Many events have no explicit classification; the tool reports that " +
		"clearly. Input: event_id."
}

func (t EventClassificationTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"event_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the event (events.id).",
			},
		},
		Required: []string{"event_id"},
	}
}

func (t EventClassificationTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t EventClassificationTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	eventID := stringArg(input, "event_id")
	if eventID == "" {
		return triageErrorResponse(errors.New("event_id is required")), nil
	}
	data, err := doTriageActionRequest(nbCtx, "event_get_classification", map[string]any{"event_id": eventID})
	if err != nil {
		// A missing classification is the common, expected case (most events are
		// never explicitly classified) — surface it as a clean result, not an error.
		// Match the backend's specific message, not any 404, so a route-down or
		// event-not-found error is not silently reported as "not classified".
		if strings.Contains(err.Error(), "classification not found") {
			return triageResponse(`{"classified":false,"message":"No classification has been recorded for this event."}`), nil
		}
		nbCtx.Ctx.GetLogger().Error("triage: get_event_classification failed", "error", err, "event_id", eventID)
		return triageErrorResponse(err), nil
	}
	return triageResponse(data), nil
}

// ---------------------------------------------------------------------------
// get_triage_rule_events — what is a given rule actually catching?
// ---------------------------------------------------------------------------

// TriageRuleEventsTool lists the events a specific triage rule matched, so the
// agent can assess a rule's effectiveness ("what is this suppression rule
// actually catching, and is it still firing?"). Scoped to the agent's account
// for system rules.
type TriageRuleEventsTool struct{}

func (t TriageRuleEventsTool) Name() string             { return ToolTriageRuleEvents }
func (t TriageRuleEventsTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t TriageRuleEventsTool) Description() string {
	return "Lists the events that a specific triage rule matched — use it to assess a rule's " +
		"effectiveness (what a suppression/scoring/classification rule is actually catching and " +
		"whether it is still firing). Get rule_id from get_triage_rules. Optional limit (default 20)."
}

func (t TriageRuleEventsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"rule_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "ID of the triage rule (from get_triage_rules).",
			},
			"limit": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Maximum number of matched events to return (default 20).",
			},
		},
		Required: []string{"rule_id"},
	}
}

func (t TriageRuleEventsTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t TriageRuleEventsTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	ruleID := stringArg(input, "rule_id")
	if ruleID == "" {
		return triageErrorResponse(errors.New("rule_id is required")), nil
	}
	in := map[string]any{"rule_id": ruleID}
	if nbCtx.AccountId != "" {
		in["account_id"] = nbCtx.AccountId
	}
	if v, ok := input.Arguments["limit"].(float64); ok && v > 0 {
		in["limit"] = v
	}
	data, err := doTriageActionRequest(nbCtx, "event_get_triage_rule_events", in)
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("triage: get_triage_rule_events failed", "error", err)
		return triageErrorResponse(err), nil
	}
	return triageResponse(data), nil
}
