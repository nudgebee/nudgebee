package tools

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// This file backs the events agent's structured tools (agents/agent_events.go):
// three deterministic, parameter-typed tools that cover the query shapes real
// events_execute traffic actually uses (single-ID lookup, filtered lists,
// aggregation/count — see docs/architecture-decisions.md for the traffic
// breakdown this design is based on). Raw SQL via events_execute/anomaly_execute
// remains available on the events agent as a fallback for the long tail
// these tools don't cover.
//
// These tools build SQL themselves from typed, server-validated arguments —
// they never interpolate LLM-authored SQL text. String values go through
// pq.QuoteLiteral; the one column name driven by LLM input (aggregate_events'
// group_by) is checked against a fixed allowlist before use.

const (
	ToolGetEventById    = "get_event_by_id"
	ToolListEvents      = "list_events"
	ToolAggregateEvents = "aggregate_events"
)

func init() {
	core.RegisterNBToolFactory(ToolGetEventById, func(accountId string) (core.NBTool, error) {
		return GetEventByIdTool{}, nil
	})
	core.RegisterNBToolFactory(ToolListEvents, func(accountId string) (core.NBTool, error) {
		return ListEventsTool{}, nil
	})
	core.RegisterNBToolFactory(ToolAggregateEvents, func(accountId string) (core.NBTool, error) {
		return AggregateEventsTool{}, nil
	})
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

func structuredEventsErrorResponse(err error) core.NBToolResponse {
	return core.NBToolResponse{
		Data:   fmt.Sprintf("Error: %s", err.Error()),
		Status: core.NBToolResponseStatusError,
	}
}

// stringArrayArg reads an array-typed argument, tolerating a bare string
// (some tool calls pass a single value un-wrapped, e.g. "subject_namespace":
// "nudgebee" instead of ["nudgebee"]) so the filter still applies instead of
// being silently dropped.
func stringArrayArg(input core.NBToolCallRequest, key string) []string {
	if s, ok := input.Arguments[key].(string); ok {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	raw, ok := input.Arguments[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func intArg(input core.NBToolCallRequest, key string, def int) int {
	if v, ok := input.Arguments[key].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

func boolArg(input core.NBToolCallRequest, key string) bool {
	v, _ := input.Arguments[key].(bool)
	return v
}

func quotedList(vs []string) string {
	quoted := make([]string, len(vs))
	for i, v := range vs {
		quoted[i] = pq.QuoteLiteral(v)
	}
	return strings.Join(quoted, ", ")
}

var relativeRangeRe = regexp.MustCompile(`^(\d+)([dhm])$`)

// parseRelativeRange parses shorthand durations like "24h", "7d", "30m".
// time.ParseDuration doesn't support day units, and real queries commonly
// need them (e.g. "last 7 days"), hence the small custom parser.
func parseRelativeRange(s string) (time.Duration, error) {
	m := relativeRangeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("relative_range must look like '24h', '7d', or '30m', got %q", s)
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "m":
		return time.Duration(n) * time.Minute, nil
	}
	return 0, fmt.Errorf("unsupported relative_range unit in %q", s)
}

// resolveTimeRange turns start_time/end_time (ISO8601) or relative_range
// (e.g. "24h") tool arguments into ISO8601 start/end strings for the WHERE
// clause. Returns empty strings for whichever bound wasn't supplied.
func resolveTimeRange(input core.NBToolCallRequest) (startISO, endISO string, err error) {
	start := stringArg(input, "start_time")
	end := stringArg(input, "end_time")
	rel := stringArg(input, "relative_range")

	if start != "" {
		if _, perr := time.Parse(time.RFC3339, start); perr != nil {
			return "", "", fmt.Errorf("start_time must be ISO8601, got %q: %w", start, perr)
		}
	}
	if end != "" {
		if _, perr := time.Parse(time.RFC3339, end); perr != nil {
			return "", "", fmt.Errorf("end_time must be ISO8601, got %q: %w", end, perr)
		}
	}
	if rel != "" {
		if start != "" || end != "" {
			return "", "", errors.New("provide either relative_range or start_time/end_time, not both")
		}
		d, perr := parseRelativeRange(rel)
		if perr != nil {
			return "", "", perr
		}
		start = time.Now().Add(-d).UTC().Format(time.RFC3339)
	}
	return start, end, nil
}

func appendDefaultEventStart(whereClauses []string, startTime string, now time.Time) []string {
	if startTime != "" {
		return whereClauses
	}
	defaultStart := now.AddDate(0, 0, -30).UTC().Format(time.RFC3339)
	return append(whereClauses, fmt.Sprintf("starts_at >= %s", pq.QuoteLiteral(defaultStart)))
}

// buildManifestOnlyEvidence replaces each row's raw evidences JSON with a
// lightweight manifest (available evidence types + key insights), regardless
// of row count. list_events is a discovery tool, not a deep-dive tool — full
// per-row evidence (log dumps, HTTP bodies, trace payloads) stays exclusive to
// get_event_by_id and get_event_evidence. Reuses EventsExecuteTool's existing
// buildEvidenceManifest (the same logic events_execute uses above its
// row-count threshold) rather than a second, drifting implementation.
func buildManifestOnlyEvidence(data []map[string]any) {
	et := EventsExecuteTool{}
	for _, row := range data {
		if row["evidences"] == nil {
			continue
		}
		evidenceStr, ok := row["evidences"].(string)
		if !ok {
			continue
		}
		evs := []events.Evidence{}
		if err := common.UnmarshalJson([]byte(evidenceStr), &evs); err != nil || len(evs) == 0 {
			delete(row, "evidences")
			continue
		}
		et.buildEvidenceManifest(row, evs)
	}
}

func eventReferences(nbCtx core.NbToolContext, dataAccountID string, data []map[string]any) []core.NBToolResponseReference {
	references := []core.NBToolResponseReference{}
	for _, e := range data {
		if idStr, ok := e["id"].(string); ok {
			references = append(references, core.GetNudgebeeUIReference(nbCtx, "investigate", "Investigate - "+idStr, map[string]string{
				"id":        idStr,
				"accountId": dataAccountID,
			}, ""))
		}
	}
	detailsCtx := nbCtx
	detailsCtx.AccountId = dataAccountID // the events page of the account queried
	references = append(references, core.GetNudgebeeUIReferenceForClusterDetails(detailsCtx, []string{"events", "summary"}, "Event Details", nil, ""))
	return references
}

// ---------------------------------------------------------------------------
// get_event_by_id — covers pure `WHERE id = ...` lookups (~26% of real
// events_execute traffic, and the shape the auto-triage flow's "explain this
// event" question always uses).
// ---------------------------------------------------------------------------

type GetEventByIdTool struct{}

func (t GetEventByIdTool) Name() string             { return ToolGetEventById }
func (t GetEventByIdTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t GetEventByIdTool) Description() string {
	return "Fetch full details (all columns, full evidence — logs, metrics, traces, deployment diffs) " +
		"for a single event by its UUID. Use for deep investigation of one specific event, including " +
		"'why was this event triaged this way' questions (combine with get_event_triage_explanation). " +
		"Input: event_id."
}

func (t GetEventByIdTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"event_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "The event's UUID (events.id).",
			},
		},
		Required: []string{"event_id"},
	}
}

func (t GetEventByIdTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t GetEventByIdTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	eventID := stringArg(input, "event_id")
	if eventID == "" {
		return structuredEventsErrorResponse(errors.New("event_id is required")), nil
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return structuredEventsErrorResponse(fmt.Errorf("event_id must be a valid UUID: %w", err)), nil
	}
	return onEventAccount(nbCtx, eventID, func(c core.NbToolContext) (core.NBToolResponse, error) {
		return t.byID(c, eventID)
	})
}

// byID reads one event, on the account onEventAccount picked.
func (t GetEventByIdTool) byID(nbCtx core.NbToolContext, eventID string) (core.NBToolResponse, error) {
	query := fmt.Sprintf(
		"SELECT id, title, description, starts_at, updated_at, ends_at, status, priority, source, "+
			"aggregation_key, finding_type, subject_type, subject_name, subject_namespace, subject_node, "+
			"fingerprint, evidences, cloud_account_id, nb_status, urgency, computed_priority, computed_score, "+
			"score_factors, score_confidence, cluster, service_key, subject_owner, labels, event_count "+
			"FROM events WHERE id = %s",
		pq.QuoteLiteral(eventID),
	)

	// dataCtx reads the delegated target's account; nbCtx keeps the
	// conversation's workspace.
	dataCtx := nbCtx.ForTarget()
	dataAccountID := dataCtx.AccountId
	view := byIDView(dataAccountID, query)
	resp, data, err := sqlToolCall(dataCtx, query, "events", view, 1, nil)
	if err != nil {
		return resp, err
	}
	if len(data) == 0 {
		resp.Data = "[]"
		resp.Status = core.NBToolResponseStatusSuccess
		return resp, nil
	}

	et := EventsExecuteTool{}
	et.enrichData(data) // single row → full-evidence path

	var overflowRefs []core.NBToolResponseReference
	for _, row := range data {
		investigateData, ok := row["evidences"].(events.InvestigateData)
		if !ok {
			continue
		}
		inline, refs := offloadEventEvidence(nbCtx.Ctx, wm, nbCtx.AccountId, nbCtx.ConversationId, eventID, investigateData)
		row["evidences"] = inline
		overflowRefs = append(overflowRefs, refs...)
	}

	bytesData, marshalErr := common.MarshalJson(data)
	if marshalErr != nil {
		return core.NBToolResponse{}, marshalErr
	}
	resp.Data = string(bytesData)
	resp.References = append(eventReferences(nbCtx, dataAccountID, data), overflowRefs...)
	return resp, nil
}

// byIDView is the events view with the id filter inside it, on the uuid
// column. The view returns id::text, so the outer "WHERE id = '<uuid>'"
// compares text and cannot use the primary key: on the largest dev account it
// read 52k buffers (89k rows) per lookup instead of 4, and calls took up to
// 10s when those pages were not cached.
func byIDView(accountID, query string) string {
	return injectIdPredicate(resolveEventsViewPlaceholders(eventsViewWithId, accountID, true), query)
}

// EventEvidenceFileName is the workspace file holding one event's structured
// evidence (the InvestigateData object). get_event_by_id and the event analyzer
// both write it, with the same shape, so an investigation finds one file under
// one name whichever stage produced it.
func EventEvidenceFileName(eventID string) string {
	return fmt.Sprintf("event_%s_evidence.json", eventID)
}

// offloadEventEvidence returns the inline form of one event's evidence plus the
// workspace-file reference, if any.
//
// The workspace file must hold the UNCAPPED evidence. capInsight cuts each
// field's Data at maxEvidenceInsightDataChars, and that Data is usually a JSON
// document stored as a string (pod_metrics series, pod_data, others) — cutting
// it mid-document leaves invalid JSON. Agents told the file is the "full
// evidence" then json.load it and fail at char ~3000, losing exactly the metric
// series and pod status they needed (dev events 2271fe6b, f1580e90, f6d8fa9e,
// 509532e3). So: marshal the full struct first, cap afterwards for the inline
// preview only.
//
// Marshal before capping is load-bearing: capInsightSlice rewrites slice
// elements in place, and a struct copy shares those backing arrays.
//
// Evidence under LlmServerEventEvidenceOverflowThreshold is returned whole
// (uncapped). A marshal or workspace-save failure falls back to the inline
// capped struct rather than losing the response.
func offloadEventEvidence(ctx *security.RequestContext, wm workspace.WorkspaceManager, accountId, conversationId, eventID string, investigateData events.InvestigateData) (any, []core.NBToolResponseReference) {
	fullBytes, fullErr := common.MarshalJson(investigateData)
	if fullErr != nil {
		ctx.GetLogger().Error("get_event_by_id: failed to marshal full evidence, returning capped evidence inline", "error", fullErr, "event_id", eventID)
		capInvestigateDataEvidence(&investigateData)
		return investigateData, nil
	}
	if len(fullBytes) <= config.Config.LlmServerEventEvidenceOverflowThreshold {
		// Small enough to return whole: nothing to cap, nothing to save.
		return investigateData, nil
	}
	// Built before capping: capInvestigateDataEvidence rewrites slices in
	// place, and the section sizes in the preview must be the file's sizes.
	preview := buildEventEvidencePreview(investigateData)
	capInvestigateDataEvidence(&investigateData)

	outputFile := EventEvidenceFileName(eventID)
	if err := wm.SaveFile(ctx, accountId, conversationId, outputFile, string(fullBytes)); err != nil {
		ctx.GetLogger().Error("get_event_by_id: failed to save evidence to workspace", "error", err, "path", outputFile)
		return investigateData, nil
	}

	inline := fmt.Sprintf("Output large (%d bytes). Evidence saved to %s with every section complete — use the key facts below. "+
		"To fetch another section call get_event_evidence with evidence_type set to the section name, or to the name in [evidence_type=...] when one is shown; "+
		"sections marked [file only] cannot be fetched. query_mode=summary only shrinks metrics, traces and logs (other types come back in full); "+
		"to page a list use query_mode=filter with offset and limit. "+
		"Logs are reduced in the file: error lines are in error_log_data, and log_data is empty or a short tail; use get_event_evidence with evidence_type=logs for the full log body.\n%s",
		len(fullBytes), outputFile, preview)
	// The description carries only the shape's first line: it is repeated for
	// every saved file in the evidence index on later turns, so the full shape
	// stays in the preview above.
	return inline, []core.NBToolResponseReference{{
		Text:        "Full event evidence saved to workspace",
		Url:         outputFile,
		Type:        "file",
		Description: "Full event evidence collected by system. " + strings.SplitN(EventEvidenceShape(investigateData), "\n", 2)[0],
	}}
}

// get_event_by_id always selects the `evidences` column (unlike events_execute,
// where the LLM's own SELECT list usually omits it), so it always takes
// processRowWithMessages' full-evidence path. Most InvestigateData fields have
// no size cap there — only LogData/ErrorLogData do — so a single heavy-evidence
// event (e.g. a crash loop with a flood of pod/node events) can serialize to
// ~1MB, which then dominates every later turn's input tokens for the rest of
// the conversation. capInvestigateDataEvidence bounds those fields the same
// way agent_events.go's reduceEventData already bounds Markdowns/Others
// (stringify + truncate at maxEvidenceInsightDataChars), plus caps slice
// length at maxEvidenceInsightEntries. Scoped to this tool only — enrichData/
// processRowWithMessages (shared with events_execute) are untouched, so the
// events agent's behavior and cost are unaffected.
const (
	maxEvidenceInsightEntries   = 15
	maxEvidenceInsightDataChars = 3000
)

// truncateAtRuneBoundary caps s at maxBytes, walking back to the nearest
// UTF-8 rune boundary so a multi-byte character never gets split (mirrors
// agents/core/scratchpad.go's TruncateHead — not reused directly since
// tools can't import agents/core without a cycle).
func truncateAtRuneBoundary(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func capInsight(insight events.InvestigateDataInsight) events.InvestigateDataInsight {
	if insight.Data != nil {
		// Evidence data is almost always a string (raw JSON already stringified
		// upstream); the type switch skips the JSON-marshal path for that
		// common case, which matters here since insight.Data can be up to
		// ~1MB before capping. For anything else (a native Go struct/map —
		// e.g. a Prometheus query result), JSON-marshal rather than
		// fmt.Sprintf("%v", ...): %v produces Go's debug syntax
		// (map[k:v ...], scientific-notation floats), which is both more
		// verbose than the equivalent JSON for the same data and not valid
		// JSON itself — a model asked to reproduce it ends up re-encoding
		// malformed pseudo-JSON instead of parsing a clean structure.
		var dataStr string
		switch v := insight.Data.(type) {
		case string:
			dataStr = v
		default:
			if jsonBytes, err := common.MarshalJson(v); err == nil {
				dataStr = string(jsonBytes)
			} else {
				dataStr = fmt.Sprintf("%v", v)
			}
		}
		if len(dataStr) > maxEvidenceInsightDataChars {
			insight.Data = truncateAtRuneBoundary(dataStr, maxEvidenceInsightDataChars) + "\n... (truncated)"
		}
	}
	return insight
}

func capInsightSlice(insights []events.InvestigateDataInsight) []events.InvestigateDataInsight {
	if len(insights) > maxEvidenceInsightEntries {
		insights = insights[:maxEvidenceInsightEntries]
	}
	for i, insight := range insights {
		insights[i] = capInsight(insight)
	}
	return insights
}

// capTracesInsight bounds Traces.Data's size by capping the inner trace-span
// array to maxEvidenceInsightEntries, instead of collapsing the whole map to a
// truncated string the way the generic capInsight does. agent_events.go's
// reduceEventData reads Traces.Data as map[string]any{"data": []any{...}} —
// stringifying it here would silently corrupt that shape (a Go type-assertion
// panic downstream, since capInsight's %v dump isn't valid JSON either). Falls
// back to capInsight for any other shape (e.g. Data already arrived as a
// string).
func capTracesInsight(insight events.InvestigateDataInsight) events.InvestigateDataInsight {
	tracesMap, ok := insight.Data.(map[string]any)
	if !ok {
		return capInsight(insight)
	}
	spans, ok := tracesMap["data"].([]any)
	if !ok || len(spans) <= maxEvidenceInsightEntries {
		return insight
	}
	capped := make(map[string]any, len(tracesMap))
	for k, v := range tracesMap {
		capped[k] = v
	}
	capped["data"] = spans[:maxEvidenceInsightEntries]
	insight.Data = capped
	return insight
}

// capAlertLabelsInsight bounds AlertLabels.Data's size by capping the label
// array's length, instead of collapsing it to a truncated string. reduceEventData
// iterates AlertLabels.Data as a []any directly — stringifying it here would
// corrupt that shape the same way capTracesInsight's comment describes. Falls
// back to capInsight for any other shape.
func capAlertLabelsInsight(insight events.InvestigateDataInsight) events.InvestigateDataInsight {
	labels, ok := insight.Data.([]any)
	if !ok {
		return capInsight(insight)
	}
	if len(labels) > maxEvidenceInsightEntries {
		insight.Data = labels[:maxEvidenceInsightEntries]
	}
	return insight
}

func capInvestigateDataEvidence(id *events.InvestigateData) {
	id.PodMetrics = capInsightSlice(id.PodMetrics)
	id.NodeMetrics = capInsightSlice(id.NodeMetrics)
	id.NoisyNeighbours = capInsightSlice(id.NoisyNeighbours)
	id.ApiFailures = capInsightSlice(id.ApiFailures)
	id.PodEvents = capInsightSlice(id.PodEvents)
	id.NodeEvents = capInsightSlice(id.NodeEvents)
	id.UserActions = capInsightSlice(id.UserActions)
	id.RDBMSQueryData = capInsightSlice(id.RDBMSQueryData)
	id.MetricsData = capInsightSlice(id.MetricsData)
	id.Markdowns = capInsightSlice(id.Markdowns)
	id.Others = capInsightSlice(id.Others)

	id.PodData = capInsight(id.PodData)
	id.NodeData = capInsight(id.NodeData)
	id.Deployment = capInsight(id.Deployment)
	id.AlertLabels = capAlertLabelsInsight(id.AlertLabels)
	id.JobInformation = capInsight(id.JobInformation)
	id.JobEvents = capInsight(id.JobEvents)
	id.JobPodEvents = capInsight(id.JobPodEvents)
	id.RelatedEvents = capInsight(id.RelatedEvents)
	id.ContainerMetrics = capInsight(id.ContainerMetrics)
	id.Traces = capTracesInsight(id.Traces)
	id.AlertData = capInsight(id.AlertData)
	id.ServiceMap = capInsight(id.ServiceMap)

	capTotalEvidenceSize(id)
}

// maxTotalEvidenceChars bounds the SUM of every insight field's Data across
// the whole InvestigateData object, once each field is already capped
// individually above. Per-field capping alone doesn't bound the total: 11
// slice fields x maxEvidenceInsightEntries x maxEvidenceInsightDataChars,
// plus 12 single fields x maxEvidenceInsightDataChars, comes to ~531KB in
// the worst case even with every per-field cap already applied (an event
// with many moderately-sized fields, each individually under its own cap,
// still sums to something unbounded). Once the combined size crosses this
// budget, the largest remaining fields are replaced with a pointer to
// get_event_evidence — which has its own workspace offload for full detail
// — until the total is back under budget.
const maxTotalEvidenceChars = 50000

// capTotalEvidenceSize re-checks the sum of every already-per-field-capped
// insight's Data size and, if over maxTotalEvidenceChars, drops the largest
// remaining fields (replacing their Data with a short pointer) until the
// total fits. Traces and AlertLabels are counted toward the total but never
// dropped here: capTracesInsight/capAlertLabelsInsight's doc comments note
// downstream consumers read their Data as a map/[]any respectively, so
// replacing it with a plain string would corrupt that shape. Both are
// already small (bounded to maxEvidenceInsightEntries items), so excluding
// them from dropping costs little. LogPatternBreakdown is treated the same
// way: the investigation prompt decodes its Data as a structure, and its
// producer bounds it to a handful of patterns with length-capped examples.
func capTotalEvidenceSize(id *events.InvestigateData) {
	type field struct {
		size int
		drop func()
	}

	fields := []field{
		{insightSliceSize(id.MetricsData), func() { id.MetricsData = []events.InvestigateDataInsight{evidenceOmittedInsight("metrics_data")} }},
		{insightSliceSize(id.PodMetrics), func() { id.PodMetrics = []events.InvestigateDataInsight{evidenceOmittedInsight("pod_metrics")} }},
		{insightSliceSize(id.NodeMetrics), func() { id.NodeMetrics = []events.InvestigateDataInsight{evidenceOmittedInsight("node_metrics")} }},
		{insightSliceSize(id.PodEvents), func() { id.PodEvents = []events.InvestigateDataInsight{evidenceOmittedInsight("pod_events")} }},
		{insightSliceSize(id.NodeEvents), func() { id.NodeEvents = []events.InvestigateDataInsight{evidenceOmittedInsight("node_events")} }},
		{insightSliceSize(id.ApiFailures), func() { id.ApiFailures = []events.InvestigateDataInsight{evidenceOmittedInsight("api_failures")} }},
		{insightSliceSize(id.NoisyNeighbours), func() {
			id.NoisyNeighbours = []events.InvestigateDataInsight{evidenceOmittedInsight("noisy_neighbours")}
		}},
		{insightSliceSize(id.UserActions), func() { id.UserActions = []events.InvestigateDataInsight{evidenceOmittedInsight("user_actions")} }},
		{insightSliceSize(id.RDBMSQueryData), func() {
			id.RDBMSQueryData = []events.InvestigateDataInsight{evidenceOmittedInsight("rdbms_query_response")}
		}},
		{insightSliceSize(id.Markdowns), func() { id.Markdowns = []events.InvestigateDataInsight{evidenceOmittedInsight("markdowns")} }},
		{insightSliceSize(id.Others), func() { id.Others = []events.InvestigateDataInsight{evidenceOmittedInsight("all")} }},
		{insightSize(id.Deployment), func() { id.Deployment = evidenceOmittedInsight("deployment") }},
		{insightSize(id.PodData), func() { id.PodData = evidenceOmittedInsight("pod_data") }},
		{insightSize(id.NodeData), func() { id.NodeData = evidenceOmittedInsight("all") }},
		{insightSize(id.RelatedEvents), func() { id.RelatedEvents = evidenceOmittedInsight("related_events") }},
		{insightSize(id.JobInformation), func() { id.JobInformation = evidenceOmittedInsight("job_information") }},
		{insightSize(id.JobEvents), func() { id.JobEvents = evidenceOmittedInsight("job_events") }},
		{insightSize(id.JobPodEvents), func() { id.JobPodEvents = evidenceOmittedInsight("all") }},
		{insightSize(id.ContainerMetrics), func() { id.ContainerMetrics = evidenceOmittedInsight("container_metrics") }},
		{insightSize(id.AlertData), func() { id.AlertData = evidenceOmittedInsight("alert_data") }},
		{insightSize(id.ServiceMap), func() { id.ServiceMap = evidenceOmittedInsight("all") }},
	}

	total := insightSize(id.Traces) + insightSize(id.AlertLabels) + insightSize(id.LogPatternBreakdown)
	for _, f := range fields {
		total += f.size
	}
	if total <= maxTotalEvidenceChars {
		return
	}

	// Largest first: the biggest contributors to the overage, and — being
	// metric/event streams rather than a single fact — the least costly to
	// defer to an explicit get_event_evidence follow-up.
	sort.Slice(fields, func(i, j int) bool { return fields[i].size > fields[j].size })
	for _, f := range fields {
		if total <= maxTotalEvidenceChars {
			break
		}
		total -= f.size
		f.drop()
	}
}

// evidenceOmittedInsight replaces a field dropped by capTotalEvidenceSize.
// evidenceType is the get_event_evidence argument that recovers the full
// data ("all" when the field has no dedicated evidence_type).
func evidenceOmittedInsight(evidenceType string) events.InvestigateDataInsight {
	return events.InvestigateDataInsight{
		Data: fmt.Sprintf("omitted — event evidence exceeded the %d-char total budget. Call get_event_evidence with evidence_type=%q for this data.", maxTotalEvidenceChars, evidenceType),
	}
}

// insightSize measures how large an insight's Data will actually serialize
// to — string Data measured directly, anything else JSON-marshaled first
// (mirrors capInsight's own handling).
func insightSize(insight events.InvestigateDataInsight) int {
	if insight.Data == nil {
		return 0
	}
	if s, ok := insight.Data.(string); ok {
		return len(s)
	}
	if b, err := common.MarshalJson(insight.Data); err == nil {
		return len(b)
	}
	return len(fmt.Sprintf("%v", insight.Data))
}

func insightSliceSize(insights []events.InvestigateDataInsight) int {
	total := 0
	for _, insight := range insights {
		total += insightSize(insight)
	}
	return total
}

// ---------------------------------------------------------------------------
// list_events — covers subject/namespace/type + finding_type/aggregation_key
// + time-range filtered lists (~65% of real events_execute traffic).
// ---------------------------------------------------------------------------

type ListEventsTool struct{}

func (t ListEventsTool) Name() string             { return ToolListEvents }
func (t ListEventsTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t ListEventsTool) Description() string {
	return "List/search events with typed filters: subject_name, subject_namespace, subject_type, subject_owner, " +
		"subject_node, cluster, finding_type (issue/configuration_change/SLO/Anomaly), aggregation_key, priority, " +
		"source (alert source, e.g. datadog, pagerduty, prometheus), status (FIRING/RESOLVED/CLOSED), nb_status " +
		"(OPEN/RESOLVED/SUPPRESSED/DUPLICATE), fingerprint, text (search in title and description), and a time " +
		"range (start_time/end_time ISO8601, or relative_range like '24h'/'7d'). Returns a compact " +
		"evidence manifest per event (available evidence types + key insights), not full raw evidence — " +
		"use get_event_by_id or get_event_evidence to drill into a specific event afterward. Use this for " +
		"any 'show me recent/list events matching X' question instead of writing raw SQL."
}

func (t ListEventsTool) InputSchema() core.ToolSchema {
	props := eventFilterSchema()
	props["limit"] = core.ToolSchemaProperty{Type: core.ToolSchemaTypeInteger, Default: 10, Description: "Max rows, default 10."}
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: props}
}

// eventFilterSchema is the filter set list_events and aggregate_events share,
// so a filter added for one is there for the other.
func eventFilterSchema() map[string]core.ToolSchemaProperty {
	return map[string]core.ToolSchemaProperty{
		"subject_name": {Type: core.ToolSchemaTypeString, Description: "ILIKE (substring) match on subject_name."},
		"subject_namespace": {
			Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
			Description: "One or more exact namespace values, e.g. a question spanning several namespaces at once.",
		},
		"subject_type":  {Type: core.ToolSchemaTypeString, Description: "ILIKE match on subject_type (pod, deployment, node, etc. — not a fixed set)."},
		"subject_owner": {Type: core.ToolSchemaTypeString, Description: "ILIKE (substring) match on subject_owner, the owning workload (e.g. a Deployment's name)."},
		"subject_node":  {Type: core.ToolSchemaTypeString, Description: "ILIKE (substring) match on subject_node, the node the subject ran on."},
		"cluster":       {Type: core.ToolSchemaTypeString, Description: "ILIKE (substring) match on cluster."},
		"finding_type": {
			Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
			Description: "One or more of: issue, configuration_change, SLO, Anomaly.",
		},
		"aggregation_key": {
			Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
			Description: "One or more exact aggregation_key values — e.g. an 'OOM or restart' question needs both pod_oom_killer_enricher and report_crash_loop. Values drift as new alert types are added, don't assume a fixed set.",
		},
		"priority": {
			Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
			Description: "One or more of: DEBUG, INFO, LOW, MEDIUM, HIGH.",
		},
		"source": {
			Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
			Description: "One or more alert sources, each a case-insensitive substring of the stored source (e.g. datadog, pagerduty, prometheus, elasticsearch, kubernetes_api_server, AWS, GCP, Azure, anomaly, slo, automation).",
		},
		"status": {
			Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
			Description: "Alert state from the source, one or more of: FIRING, RESOLVED, CLOSED.",
		},
		"nb_status": {
			Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
			Description: "Nudgebee triage state, one or more of: OPEN, RESOLVED, SUPPRESSED, DUPLICATE.",
		},
		"fingerprint": {Type: core.ToolSchemaTypeString, Description: "Exact fingerprint: every occurrence of one recurring alert."},
		"text": {
			Type:        core.ToolSchemaTypeString,
			Description: fmt.Sprintf("Case-insensitive substring searched in title and description (max %d chars), e.g. 'timeout'.", maxEventTextFilterChars),
		},
		"start_time":     {Type: core.ToolSchemaTypeString, Description: "ISO8601. Alternative to relative_range."},
		"end_time":       {Type: core.ToolSchemaTypeString, Description: "ISO8601. Defaults to now."},
		"relative_range": {Type: core.ToolSchemaTypeString, Description: "Shorthand duration, e.g. '24h', '7d'. Alternative to start_time."},
	}
}

// maxEventTextFilterChars bounds the text filter: it is an unindexed ILIKE
// over title and description, so it stays a short search term.
const maxEventTextFilterChars = 200

// eventFilterClauses turns the shared filter arguments into WHERE clauses,
// including the time range (and the 30-day default when no start is given).
// Every value is quoted with pq.QuoteLiteral; no argument names a column.
func eventFilterClauses(input core.NBToolCallRequest, now time.Time) ([]string, error) {
	var where []string
	ilike := func(column, v string) string {
		return fmt.Sprintf("%s ILIKE %s", column, pq.QuoteLiteral("%"+v+"%"))
	}
	for _, f := range []struct{ arg, column string }{
		{"subject_name", "subject_name"},
		{"subject_owner", "subject_owner"},
		{"subject_node", "subject_node"},
		{"cluster", "cluster"},
	} {
		if v := stringArg(input, f.arg); v != "" {
			where = append(where, ilike(f.column, v))
		}
	}
	if v := stringArg(input, "subject_type"); v != "" {
		where = append(where, fmt.Sprintf("subject_type ILIKE %s", pq.QuoteLiteral(v)))
	}
	for _, f := range []struct {
		arg   string
		upper bool
	}{
		{"subject_namespace", false},
		{"finding_type", false},
		{"aggregation_key", false},
		{"priority", false},
		// Stored upper-case; the model often writes them lower-case.
		{"status", true},
		{"nb_status", true},
	} {
		vs := stringArrayArg(input, f.arg)
		if len(vs) == 0 {
			continue
		}
		if f.upper {
			for i := range vs {
				vs[i] = strings.ToUpper(vs[i])
			}
		}
		where = append(where, f.arg+" IN ("+quotedList(vs)+")")
	}
	// Stored sources mix case and suffixes (datadog_webhook, AWS_EventBridge),
	// so each value is a case-insensitive substring.
	if vs := stringArrayArg(input, "source"); len(vs) > 0 {
		ors := make([]string, len(vs))
		for i, v := range vs {
			ors[i] = ilike("source", v)
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if v := stringArg(input, "fingerprint"); v != "" {
		where = append(where, "fingerprint = "+pq.QuoteLiteral(v))
	}
	if v := strings.TrimSpace(stringArg(input, "text")); v != "" {
		if utf8.RuneCountInString(v) > maxEventTextFilterChars {
			return nil, fmt.Errorf("text must be at most %d characters; search for a shorter phrase", maxEventTextFilterChars)
		}
		where = append(where, "("+ilike("title", v)+" OR "+ilike("description", v)+")")
	}

	startTime, endTime, err := resolveTimeRange(input)
	if err != nil {
		return nil, err
	}
	if startTime != "" {
		where = append(where, fmt.Sprintf("starts_at >= %s", pq.QuoteLiteral(startTime)))
	}
	if endTime != "" {
		where = append(where, fmt.Sprintf("starts_at <= %s", pq.QuoteLiteral(endTime)))
	}
	return appendDefaultEventStart(where, startTime, now), nil
}

func (t ListEventsTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t ListEventsTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	whereClauses, err := eventFilterClauses(input, time.Now())
	if err != nil {
		return structuredEventsErrorResponse(err), nil
	}

	limit := intArg(input, "limit", 10)

	// dataCtx reads the delegated target's account; nbCtx keeps the
	// conversation's workspace.
	dataCtx := nbCtx.ForTarget()
	dataAccountID := dataCtx.AccountId
	view := buildListEventsView(dataAccountID, whereClauses, limit)
	query := "SELECT id, title, starts_at, priority, source, aggregation_key, finding_type, subject_type, " +
		"subject_name, subject_namespace, nb_status, computed_priority, computed_score, event_count, evidences " +
		"FROM events ORDER BY starts_at DESC"
	resp, data, err := sqlToolCall(dataCtx, query, "events", view, limit, nil)
	if err != nil {
		return resp, err
	}
	buildManifestOnlyEvidence(data)

	bytesData, marshalErr := common.MarshalJson(data)
	if marshalErr != nil {
		return core.NBToolResponse{}, marshalErr
	}
	resp.Data = string(bytesData)
	resp.References = eventReferences(nbCtx, dataAccountID, data)
	return resp, nil
}

// buildListEventsView returns the newest event of each recurring alert
// (same aggregation_key, subject and fingerprint) with how many times it
// occurred, newest first, at most limit of them.
//
// The groups are found with a hash aggregate over the matching rows, and
// only the limit rows returned are then read in full. Ranking every row with
// a window instead sorted all of them first: 0.73s against 0.19s for the
// largest dev account's 68k events in 30 days.
//
// evidences is cut down in SQL to the fields buildEvidenceManifest reads.
// Evidence is most of the table's size (median 29 KB, p99 2 MB per event),
// and loading it whole for a 10-row list moved up to 28 MB per call. Items
// that are not objects are dropped: Go cannot decode them as an Evidence, and
// one used to lose the whole event's manifest.
func buildListEventsView(accountID string, whereClauses []string, limit int) string {
	where := strings.Join(append([]string{
		fmt.Sprintf("cloud_account_id = %s::uuid", pq.QuoteLiteral(accountID)),
	}, whereClauses...), " AND ")
	groupKey := []string{"aggregation_key", "subject_name", "subject_type", "subject_namespace", "fingerprint"}
	sameGroup := make([]string, len(groupKey))
	for i, k := range groupKey {
		sameGroup[i] = fmt.Sprintf("e.%[1]s IS NOT DISTINCT FROM g.%[1]s", k)
	}
	keys := strings.Join(groupKey, ", ")

	// The filters are repeated in the LATERAL so the row it picks matches
	// them too; their unqualified columns resolve to e, the nearest scope.
	return `WITH g AS (` +
		`SELECT ` + keys + `, max(starts_at) AS last_starts_at, count(*) AS event_count ` +
		`FROM events WHERE ` + where + ` GROUP BY ` + keys + ` ORDER BY last_starts_at DESC LIMIT ` + strconv.Itoa(limit) +
		`) SELECT r.id, r.title, r.starts_at, r.priority, r.source, r.aggregation_key, r.finding_type, ` +
		`r.subject_type, r.subject_name, r.subject_namespace, r.nb_status, r.computed_priority, r.computed_score, ` +
		`g.event_count, r.evidences FROM g CROSS JOIN LATERAL (` +
		`SELECT e.id::text AS id, e.title, e.starts_at, e.priority, e.source, e.aggregation_key, e.finding_type, ` +
		`e.subject_type, e.subject_name, e.subject_namespace, e.nb_status, e.computed_priority, e.computed_score, ` +
		manifestEvidenceExpr + ` AS evidences FROM events e WHERE ` + where +
		` AND e.starts_at = g.last_starts_at AND ` + strings.Join(sameGroup, " AND ") +
		` LIMIT 1) r`
}

// manifestEvidenceExpr projects e.evidences down to what
// buildEvidenceManifest reads: type, insight, filename, additional_info's
// action_name and title, a table's table_name, and the data of a log-pattern
// breakdown card (isLogPatternBreakdownCard decodes it).
var manifestEvidenceExpr = `COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(` +
	`'type', ev->'type', 'insight', ev->'insight', 'filename', ev->'filename', ` +
	`'additional_info', CASE WHEN jsonb_typeof(ev->'additional_info') = 'object' THEN ` +
	`jsonb_build_object('action_name', ev->'additional_info'->'action_name', 'title', ev->'additional_info'->'title') END, ` +
	`'data', CASE WHEN ev->>'type' = 'table' AND jsonb_typeof(ev->'data') = 'object' THEN jsonb_build_object('table_name', ev->'data'->'table_name') ` +
	`WHEN ev->'additional_info'->>'action_name' = ` + pq.QuoteLiteral(events.LogPatternBreakdownAction) + ` THEN ev->'data' END))) ` +
	`FROM jsonb_array_elements(CASE WHEN jsonb_typeof(e.evidences) = 'array' THEN e.evidences ELSE '[]'::jsonb END) ev ` +
	`WHERE jsonb_typeof(ev) = 'object'), '[]'::jsonb)::text`

// ---------------------------------------------------------------------------
// aggregate_events — covers GROUP BY/COUNT traffic (~4-5% of real
// events_execute traffic, but the natural shape for "how many X" and
// alert-noise questions).
// ---------------------------------------------------------------------------

var allowedGroupByColumns = []string{
	"aggregation_key", "subject_namespace", "subject_type", "finding_type", "priority", "source", "nb_status",
	"status", "title", "subject_name", "subject_owner", "cluster",
}

// maxAggregateGroupByColumns bounds group_by: real queries group by up to
// four columns, and more than three makes one row per event.
const maxAggregateGroupByColumns = 3

func isAllowedGroupByColumn(col string) bool {
	for _, c := range allowedGroupByColumns {
		if c == col {
			return true
		}
	}
	return false
}

type AggregateEventsTool struct{}

func (t AggregateEventsTool) Name() string             { return ToolAggregateEvents }
func (t AggregateEventsTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t AggregateEventsTool) Description() string {
	return "Count or group events by up to three columns (aggregation_key, subject_namespace, subject_type, " +
		"finding_type, priority, source, nb_status, status, title, subject_name, subject_owner, cluster), with the same " +
		"filters as list_events. Each row also carries first_seen, last_seen and total_event_groups (the number of groups before " +
		"limit; when it exceeds the rows returned, say 'top N of total_event_groups'). Set count_distinct_fingerprint=true " +
		"to count unique event patterns (dedup) instead of raw occurrences. Use this for 'how many X' / 'top N by Y' / " +
		"'when did X start' / alert-noise questions instead of writing raw SQL with GROUP BY/COUNT."
}

func (t AggregateEventsTool) InputSchema() core.ToolSchema {
	props := eventFilterSchema()
	groupBy := make([]any, len(allowedGroupByColumns))
	for i, c := range allowedGroupByColumns {
		groupBy[i] = c
	}
	props["group_by"] = core.ToolSchemaProperty{
		Type:        core.ToolSchemaTypeArray,
		Items:       map[string]any{"type": "string", "enum": groupBy},
		Description: fmt.Sprintf("One to %d columns to group by, e.g. [\"aggregation_key\"] or [\"subject_namespace\", \"priority\"].", maxAggregateGroupByColumns),
	}
	props["count_distinct_fingerprint"] = core.ToolSchemaProperty{Type: core.ToolSchemaTypeBoolean, Description: "Count unique event patterns (dedup) instead of raw occurrences."}
	props["limit"] = core.ToolSchemaProperty{Type: core.ToolSchemaTypeInteger, Default: 50, Description: fmt.Sprintf("Max groups, largest first; default 50, at most %d.", maxAggregateEventsRows)}
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: props, Required: []string{"group_by"}}
}

// maxAggregateEventsRows caps the groups returned: grouping by title or
// subject_name can make thousands.
const maxAggregateEventsRows = 200

func (t AggregateEventsTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t AggregateEventsTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// stringArrayArg also takes a bare string, the shape group_by had before
	// it became a list.
	groupBy := stringArrayArg(input, "group_by")
	whereClauses, err := eventFilterClauses(input, time.Now())
	if err != nil {
		return structuredEventsErrorResponse(err), nil
	}
	limit := min(intArg(input, "limit", 50), maxAggregateEventsRows)
	query, err := buildAggregateEventsQuery(groupBy, boolArg(input, "count_distinct_fingerprint"), whereClauses, limit)
	if err != nil {
		return structuredEventsErrorResponse(err), nil
	}

	// The typed time predicates above are authoritative. Do not reapply the
	// legacy view's fixed 30-day lower bound: explicit older ranges must remain
	// queryable, while calls without a start bound already received the default.
	// dataCtx reads the delegated target's account; nbCtx keeps the
	// conversation's workspace.
	dataCtx := nbCtx.ForTarget()
	dataAccountID := dataCtx.AccountId
	view := resolveEventsViewPlaceholders(eventsViewWithId, dataAccountID, true)
	resp, _, err := sqlToolCall(dataCtx, query, "events", view, limit, nil)
	if err != nil {
		return resp, err
	}
	return resp, nil
}

// buildAggregateEventsQuery builds the GROUP BY query. Column names come only
// from allowedGroupByColumns; filter values are already quoted. The LIMIT is
// written here: buildLimitedQuery skips its own when it sees the word LIMIT
// anywhere, a filter value like 'rate limit' included.
func buildAggregateEventsQuery(groupBy []string, distinctFingerprint bool, whereClauses []string, limit int) (string, error) {
	if len(groupBy) == 0 || len(groupBy) > maxAggregateGroupByColumns {
		return "", fmt.Errorf("group_by must list 1 to %d of: %s", maxAggregateGroupByColumns, strings.Join(allowedGroupByColumns, ", "))
	}
	seen := map[string]bool{}
	for _, col := range groupBy {
		if !isAllowedGroupByColumn(col) {
			return "", fmt.Errorf("group_by must list 1 to %d of: %s", maxAggregateGroupByColumns, strings.Join(allowedGroupByColumns, ", "))
		}
		if seen[col] {
			return "", fmt.Errorf("group_by lists %s twice", col)
		}
		seen[col] = true
	}
	cols := strings.Join(groupBy, ", ")

	countExpr := "count(*)"
	if distinctFingerprint {
		countExpr = "count(DISTINCT fingerprint)"
	}
	// total_event_groups is counted before the LIMIT, so a cut list says how many
	// groups there were.
	query := fmt.Sprintf("SELECT %s, %s AS count, min(starts_at) AS first_seen, max(starts_at) AS last_seen, count(*) OVER () AS total_event_groups FROM events", cols, countExpr)
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	return query + fmt.Sprintf(" GROUP BY %s ORDER BY count DESC LIMIT %d", cols, limit), nil
}

const (
	// eventEvidencePreviewBytes bounds the inline summary get_event_by_id
	// returns once evidence is offloaded to the workspace file. The file is
	// the source of truth; this only orients the model. It is deliberately not
	// evidencePreviewBytes, which is sized to the whole scratchpad observation
	// and let one event's log lines fill it.
	eventEvidencePreviewBytes = 7500
	previewErrorLines         = 10
	previewErrorLineChars     = 300
	previewFieldChars         = 1200
	// previewRootCauseChars caps the pod_events / pod_data slices: for crash
	// loops and OOMs they hold the restart reason and last state.
	previewRootCauseChars = 1000
	// previewInsightBytes / previewInsightChars bound the one-line insight
	// messages inlined from every section (deployment correlation, trace
	// errors, ...). They are already written as summaries, so they carry the
	// lead even when the section's raw data stays in the file.
	previewInsightBytes = 1500
	previewInsightChars = 240
)

// buildEventEvidencePreview summarises offloaded evidence: log summary, the
// first error lines, alert labels/data, then every other non-empty section
// with its size so the model knows what to read from the file.
func buildEventEvidencePreview(id events.InvestigateData) string {
	sections := evidenceSections(id)

	var b strings.Builder
	b.WriteString("Key facts:\n")

	if id.AnomalyDetection != nil {
		b.WriteString("anomaly_detection:\n")
		for line := range strings.SplitSeq(strings.TrimSpace(id.AnomalyDetection.Render()), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	if id.LogSummary != nil {
		if raw, err := common.MarshalJson(id.LogSummary); err == nil {
			fmt.Fprintf(&b, "log_summary: %s\n", truncateUTF8Safe(string(raw), previewFieldChars))
		}
	}
	if h := insightHighlights(sections, previewInsightBytes); h != "" {
		b.WriteString("insights (from sections whose data stays in the file):\n" + h)
	}
	if n := len(id.ErrorLogData); n > 0 {
		distinct, patterns := distinctErrorLines(id.ErrorLogData, previewErrorLines)
		fmt.Fprintf(&b, "error_log_data (%d lines, %d distinct patterns, first %d of them):\n", n, patterns, len(distinct))
		for _, line := range distinct {
			b.WriteString("  " + truncateUTF8Safe(line, previewErrorLineChars) + "\n")
		}
	}
	if len(id.PodEvents) > 0 {
		fmt.Fprintf(&b, "pod_events: %s\n", rootCauseSlice(id.PodEvents, previewRootCauseChars))
	}
	if id.PodData.Data != nil || len(id.PodData.Insight) > 0 {
		fmt.Fprintf(&b, "pod_data: %s\n", rootCauseSlice([]events.InvestigateDataInsight{id.PodData}, previewRootCauseChars))
	}
	for _, f := range []struct {
		name    string
		insight events.InvestigateDataInsight
	}{{"alert_labels", id.AlertLabels}, {"alert_data", id.AlertData}} {
		if f.insight.Data == nil {
			continue
		}
		if raw, err := common.MarshalJson(f.insight.Data); err == nil {
			fmt.Fprintf(&b, "%s: %s\n", f.name, truncateUTF8Safe(string(raw), previewFieldChars))
		}
	}
	b.WriteString(previewMetricCards(id.MetricsData))
	b.WriteString(previewTraces(id.Traces))

	sort.Slice(sections, func(i, j int) bool { return sections[i].size > sections[j].size })
	var tail strings.Builder
	tail.WriteString("Sections in the file (bytes):")
	for _, s := range sections {
		if s.size <= 0 {
			continue
		}
		switch s.evType {
		case "":
			fmt.Fprintf(&tail, " %s[file only]=%d", s.name, s.size)
		case s.name:
			fmt.Fprintf(&tail, " %s=%d", s.name, s.size)
		default:
			fmt.Fprintf(&tail, " %s[evidence_type=%s]=%d", s.name, s.evType, s.size)
		}
	}
	tail.WriteString("\n")

	// The section list is what tells the model what else it can fetch, so
	// it is reserved first and only the facts above it are trimmed.
	return truncateUTF8Safe(b.String(), max(0, eventEvidencePreviewBytes-tail.Len())) + tail.String()
}

// previewSection is one evidence section as the preview sees it. items is nil
// for log_data, whose size is its string length.
type previewSection struct {
	name   string
	evType string
	items  []events.InvestigateDataInsight
	size   int
}

// evidenceSections lists the evidence file's sections with their sizes. evType is
// the get_event_evidence evidence_type for the section; "" means the tool cannot
// return it and it is only in the file.
func evidenceSections(id events.InvestigateData) []previewSection {
	sections := []previewSection{
		{"log_data", "logs", nil, len(id.LogData)},
		{"pod_metrics", "pod_metrics", id.PodMetrics, 0},
		{"node_metrics", "node_metrics", id.NodeMetrics, 0},
		{"metrics_queries_data", "metrics_data", id.MetricsData, 0},
		{"pod_events", "pod_events", id.PodEvents, 0},
		{"node_events", "node_events", id.NodeEvents, 0},
		{"noisy_neighbours", "noisy_neighbours", id.NoisyNeighbours, 0},
		{"api_failures", "api_failures", id.ApiFailures, 0},
		{"user_actions", "user_actions", id.UserActions, 0},
		{"rdbms_query_data", "rdbms_query_response", id.RDBMSQueryData, 0},
		{"markdown_data", "markdowns", id.Markdowns, 0},
		{"others", "", id.Others, 0},
		{"pod_data", "pod_data", []events.InvestigateDataInsight{id.PodData}, 0},
		{"node_data", "", []events.InvestigateDataInsight{id.NodeData}, 0},
		{"deployment", "deployment", []events.InvestigateDataInsight{id.Deployment}, 0},
		{"job_information", "job_information", []events.InvestigateDataInsight{id.JobInformation}, 0},
		{"job_events", "job_events", []events.InvestigateDataInsight{id.JobEvents}, 0},
		{"job_pod_events", "", []events.InvestigateDataInsight{id.JobPodEvents}, 0},
		{"related_events", "related_events", []events.InvestigateDataInsight{id.RelatedEvents}, 0},
		{"container_metrics", "container_metrics", []events.InvestigateDataInsight{id.ContainerMetrics}, 0},
		{"traces", "traces", []events.InvestigateDataInsight{id.Traces}, 0},
		{"service_map_data", "", []events.InvestigateDataInsight{id.ServiceMap}, 0},
		{"alert_labels", "alert_labels", []events.InvestigateDataInsight{id.AlertLabels}, 0},
		{"alert_data", "alert_data", []events.InvestigateDataInsight{id.AlertData}, 0},
	}
	for i := range sections {
		if sections[i].items != nil {
			sections[i].size = insightSliceSize(sections[i].items)
		}
	}
	return sections
}

// EventEvidenceShape describes the event evidence file so it can be read without
// first exploring it: which top-level keys hold data and in what form, the metric
// cards by name, and the traces' unit. Agents told only "parse it (jq 'keys')"
// spent their first turns listing keys, printing sections that turned out empty
// and working out which "prometheus" card was which.
func EventEvidenceShape(id events.InvestigateData) string {
	var b strings.Builder
	b.WriteString("Top-level keys with data: ")
	var withData, empty []string
	for _, s := range evidenceSections(id) {
		if s.size <= 0 {
			empty = append(empty, s.name)
			continue
		}
		desc := fmt.Sprintf("%s (%d bytes", s.name, s.size)
		switch {
		case s.items == nil:
			desc += ", text"
		case len(s.items) > 1:
			desc += fmt.Sprintf(", list of %d entries", len(s.items))
		}
		if sectionDataIsString(s.items) {
			desc += ", each entry's .data is a JSON document stored as a string: decode it a second time"
		}
		withData = append(withData, desc+")")
	}
	if id.AnomalyDetection != nil {
		withData = append([]string{"anomaly_detection (the anomaly detector's result, already decoded)"}, withData...)
	}
	if len(withData) == 0 {
		b.WriteString("none")
	} else {
		b.WriteString(strings.Join(withData, "; "))
	}
	b.WriteString(".\n")
	if len(empty) > 0 {
		b.WriteString("Empty, do not read: " + strings.Join(empty, ", ") + ".\n")
	}
	b.WriteString(previewOthers(id.Others))
	b.WriteString(previewMetricCards(id.MetricsData))
	b.WriteString(previewTraces(id.Traces))
	return b.String()
}

const (
	previewOthersBytes     = 1500
	previewOthersDescChars = 90
	previewOthersHeaders   = 6
)

// previewOthers names each entry of others. Every entry's own title is just its
// format ("json", "table"), so agents opened each one to learn what it held:
// a Kubernetes change object, a table of pod events, the pods on a node.
func previewOthers(items []events.InvestigateDataInsight) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("others entries (.others[i].data):\n")
	for i, it := range items {
		name, _ := it.AdditionalInfo["title"].(string)
		var detail string
		m := (GetEventEvidenceTool{}).toMap(it.Data)
		switch {
		case m["rows"] != nil || m["headers"] != nil:
			if name == "" {
				name, _ = m["table_name"].(string)
			}
			rows, _ := m["rows"].([]any)
			headers, _ := m["headers"].([]any)
			cols := make([]string, 0, previewOthersHeaders)
			for _, h := range headers {
				if len(cols) == previewOthersHeaders {
					cols = append(cols, "…")
					break
				}
				if hs, ok := h.(string); ok {
					cols = append(cols, hs)
				}
			}
			detail = fmt.Sprintf("table, %d rows, columns %s", len(rows), strings.Join(cols, ", "))
		case m != nil:
			kind, _ := m["kind"].(string)
			desc, _ := m["description"].(string)
			if kind != "" || desc != "" {
				detail = strings.TrimSpace(kind + " change: " + truncateUTF8Safe(strings.Join(strings.Fields(desc), " "), previewOthersDescChars))
			} else {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				detail = "object with keys " + strings.Join(keys, ", ")
			}
		default:
			detail = it.Title
		}
		line := fmt.Sprintf("  [%d] ", i)
		if name != "" {
			line += name + ": "
		}
		line += detail + "\n"
		if b.Len()+len(line) > previewOthersBytes {
			fmt.Fprintf(&b, "  … %d more entries\n", len(items)-i)
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// sectionDataIsString reports whether every entry of a section that has data
// carries it as a string rather than a decoded object.
func sectionDataIsString(items []events.InvestigateDataInsight) bool {
	seen := false
	for _, it := range items {
		if it.Data == nil {
			continue
		}
		if _, ok := it.Data.(string); !ok {
			return false
		}
		seen = true
	}
	return seen
}

const (
	previewQueryChars   = 160
	previewSlowestSpans = 3
	previewSpanChars    = 100
)

// previewMetricCards lists the metric cards by the name the file keys them on,
// with how many series hold data. Every card's own title is just "prometheus",
// so without this the model reads each card to find which one is which, and
// cannot tell an empty card (all NaN) from a quiet one.
func previewMetricCards(cards []events.InvestigateDataInsight) string {
	if len(cards) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("metrics_queries_data cards (name in .additional_info.title, series in .data.series_list_result[].values):\n")
	for i, card := range cards {
		name, _ := card.AdditionalInfo["title"].(string)
		if name == "" {
			name = card.Title
		}
		withData, total := 0, 0
		if m := (GetEventEvidenceTool{}).toMap(card.Data); m != nil {
			series, _ := m["series_list_result"].([]any)
			for _, s := range series {
				sm, _ := s.(map[string]any)
				values, _ := sm["values"].([]any)
				total++
				for _, v := range values {
					if !math.IsNaN(toFloat64(v)) {
						withData++
						break
					}
				}
			}
		}
		fmt.Fprintf(&b, "  [%d] %s: ", i, name)
		if withData == 0 {
			b.WriteString("no data")
		} else {
			fmt.Fprintf(&b, "%d of %d series with data", withData, total)
		}
		if card.Unit != "" {
			fmt.Fprintf(&b, ", unit %s", card.Unit)
		}
		if card.Query != "" {
			fmt.Fprintf(&b, ", query %s", truncateUTF8Safe(card.Query, previewQueryChars))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// previewTraces states the span count and Duration's unit, and names the slowest
// spans. Durations are nanoseconds; read as microseconds, a 0.4 ms query looks
// like 0.4 s.
func previewTraces(traces events.InvestigateDataInsight) string {
	m, _ := traces.Data.(map[string]any)
	spans, _ := m["data"].([]any)
	if len(spans) == 0 {
		return ""
	}
	type span struct {
		ns   float64
		desc string
	}
	var all []span
	for _, s := range spans {
		sm, _ := s.(map[string]any)
		ns := toFloat64(sm["Duration"])
		if math.IsNaN(ns) {
			continue
		}
		attrs, _ := sm["SpanAttributes"].(map[string]any)
		str := func(k string) string { v, _ := attrs[k].(string); return strings.TrimSpace(v) }
		parts := []string{}
		if name, _ := sm["SpanName"].(string); name != "" && name != str("http.method") {
			parts = append(parts, name)
		}
		if dest := str("destination.workload_name"); dest != "" {
			parts = append(parts, "to "+dest)
		}
		if method, path := str("http.method"), str("http.path"); method != "" || path != "" {
			parts = append(parts, strings.TrimSpace(method+" "+path))
		} else if stmt := strings.Join(strings.Fields(str("db.statement")), " "); stmt != "" {
			parts = append(parts, stmt)
		}
		all = append(all, span{ns, truncateUTF8Safe(strings.Join(parts, " "), previewSpanChars)})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ns > all[j].ns })

	var b strings.Builder
	fmt.Fprintf(&b, "traces: %d spans in .traces.data.data[]; Duration is in nanoseconds", len(spans))
	if len(all) > 0 {
		b.WriteString("; slowest:")
		for i, s := range all {
			if i == previewSlowestSpans {
				break
			}
			fmt.Fprintf(&b, " %.1f ms %s;", s.ns/1e6, s.desc)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// errTemplateNoise matches what differs between otherwise identical log
// lines: UUIDs, long hex ids (trace/span ids) and numbers.
var errTemplateNoise = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{16,}|\d+`)

// distinctErrorLines returns up to limit error lines, one per distinct
// pattern in first-seen order, and how many distinct patterns there are. The
// first N raw lines are usually one message repeated, which hides that other
// collections, hosts or pods are failing too.
func distinctErrorLines(lines []string, limit int) ([]string, int) {
	seen := make(map[string]struct{}, len(lines))
	var out []string
	for _, line := range lines {
		key := errTemplateNoise.ReplaceAllString(line, "#")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if len(out) < limit {
			out = append(out, line)
		}
	}
	return out, len(seen)
}

// insightHighlights inlines the one-line insight messages of every section
// except pod_events/pod_data (rootCauseSlice already carries theirs) and the
// alert sections (printed whole in the key facts). Critical
// and warning messages come first, duplicates are dropped, and the result
// stays within maxBytes.
func insightHighlights(sections []previewSection, maxBytes int) string {
	type entry struct {
		rank int
		line string
	}
	rank := func(sev string) int {
		switch strings.ToLower(sev) {
		case "critical", "error":
			return 0
		case "warning", "warn":
			return 1
		}
		return 2
	}
	seen := map[string]struct{}{}
	var entries []entry
	for _, sec := range sections {
		if sec.name == "pod_events" || sec.name == "pod_data" || sec.name == "alert_labels" || sec.name == "alert_data" {
			continue
		}
		for _, item := range sec.items {
			for _, in := range item.Insight {
				msg := strings.TrimSpace(in.Message)
				if msg == "" {
					continue
				}
				if _, dup := seen[msg]; dup {
					continue
				}
				seen[msg] = struct{}{}
				entries = append(entries, entry{rank(in.Severity), "  [" + sec.name + "] " + truncateUTF8Safe(msg, previewInsightChars) + "\n"})
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].rank < entries[j].rank })

	var b strings.Builder
	for _, e := range entries {
		if b.Len()+len(e.line) > maxBytes {
			break
		}
		b.WriteString(e.line)
	}
	return b.String()
}

// oomKilledTitle is the block tool_event.go appends to PodEvents for OOM
// events; it is appended last, so a cut from the front would drop it.
const oomKilledTitle = "*Pod and Node OOMKilled data*"

// rootCauseSlice fits the part of pod_events / pod_data that explains a crash
// into maxBytes. Each block's title and insight come first (the OOMKilled
// block before the others), because the summary is what names the cause; raw
// table rows only fill whatever budget is left. InvestigateDataInsight
// marshals data first, so cutting its JSON from the front keeps the rows and
// drops the summary.
func rootCauseSlice(insights []events.InvestigateDataInsight, maxBytes int) string {
	ordered := make([]events.InvestigateDataInsight, 0, len(insights))
	for _, in := range insights {
		if in.Title == oomKilledTitle {
			ordered = append(ordered, in)
		}
	}
	for _, in := range insights {
		if in.Title != oomKilledTitle {
			ordered = append(ordered, in)
		}
	}

	var b strings.Builder
	for _, in := range ordered {
		if in.Title != "" {
			b.WriteString(in.Title + " ")
		}
		if len(in.Insight) > 0 {
			if raw, err := common.MarshalJson(in.Insight); err == nil {
				b.WriteString(string(raw))
			}
		}
		b.WriteString("; ")
	}
	out := truncateUTF8Safe(b.String(), maxBytes)
	for _, in := range ordered {
		left := maxBytes - len(out)
		if left <= len("data:  ") || in.Data == nil {
			continue
		}
		if raw, err := common.MarshalJson(in.Data); err == nil {
			out += "data: " + truncateUTF8Safe(string(raw), left-len("data:  ")) + " "
		}
	}
	return strings.TrimSpace(out)
}
