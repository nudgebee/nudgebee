package tools

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// This file backs the events_v2 agent (agents/agent_events_v2.go): three
// deterministic, parameter-typed tools that cover the query shapes real
// events_execute traffic actually uses (single-ID lookup, filtered lists,
// aggregation/count — see docs/architecture-decisions.md for the traffic
// breakdown this design is based on). Raw SQL via events_execute/anomaly_execute
// remains available on the events_v2 agent as a fallback for the long tail
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

func eventsV2ErrorResponse(err error) core.NBToolResponse {
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

func eventReferences(nbCtx core.NbToolContext, data []map[string]any) []core.NBToolResponseReference {
	references := []core.NBToolResponseReference{}
	for _, e := range data {
		if idStr, ok := e["id"].(string); ok {
			references = append(references, core.GetNudgebeeUIReference(nbCtx, "investigate", "Investigate - "+idStr, map[string]string{
				"id":        idStr,
				"accountId": nbCtx.AccountId,
			}, ""))
		}
	}
	references = append(references, core.GetNudgebeeUIReferenceForClusterDetails(nbCtx, []string{"events", "summary"}, "Event Details", nil, ""))
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
		"'why was this event triaged this way' questions (combine with get_triage_explanation). " +
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
		return eventsV2ErrorResponse(errors.New("event_id is required")), nil
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return eventsV2ErrorResponse(fmt.Errorf("event_id must be a valid UUID: %w", err)), nil
	}

	query := fmt.Sprintf(
		"SELECT id, title, description, starts_at, updated_at, ends_at, status, priority, source, "+
			"aggregation_key, finding_type, subject_type, subject_name, subject_namespace, subject_node, "+
			"fingerprint, evidences, cloud_account_id, nb_status, urgency, computed_priority, computed_score, "+
			"score_factors, score_confidence, cluster, service_key, subject_owner, labels, event_count "+
			"FROM events WHERE id = %s",
		pq.QuoteLiteral(eventID),
	)

	view := resolveEventsViewPlaceholders(eventsViewWithId, nbCtx.AccountId, true)
	resp, data, err := sqlToolCall(nbCtx, query, "events", view, 1, nil)
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

	// capInvestigateDataEvidence bounds the per-field-typed insights first (its
	// own ~50KB aggregate budget), then the whole (already-capped) struct is
	// measured again here — this second check covers the struct's non-Insight
	// fields (LogData, ErrorLogData, LogSummary) that capInvestigateDataEvidence
	// can't see, and offloads to a workspace file past
	// LlmServerEventEvidenceOverflowThreshold, the same overflow convention
	// get_event_evidence already uses (tool_event_evidence.go's
	// saveEvidenceToWorkspaceIfLarge). A workspace-save failure falls back to
	// the inline, already-per-field-capped struct rather than losing data.
	var overflowRefs []core.NBToolResponseReference
	for _, row := range data {
		investigateData, ok := row["evidences"].(events.InvestigateData)
		if !ok {
			continue
		}
		capInvestigateDataEvidence(&investigateData)

		if evidenceBytes, marshalErr := common.MarshalJson(investigateData); marshalErr == nil {
			preview, refs := saveEvidenceToWorkspaceIfLarge(nbCtx.Ctx, wm, nbCtx.AccountId, nbCtx.ConversationId,
				fmt.Sprintf("event_%s_evidence", eventID), string(evidenceBytes))
			if refs != nil {
				row["evidences"] = preview
				overflowRefs = append(overflowRefs, refs...)
				continue
			}
		}
		row["evidences"] = investigateData
	}

	bytesData, marshalErr := common.MarshalJson(data)
	if marshalErr != nil {
		return core.NBToolResponse{}, marshalErr
	}
	resp.Data = string(bytesData)
	resp.References = append(eventReferences(nbCtx, data), overflowRefs...)
	return resp, nil
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
	id.AlertLabels = capInsight(id.AlertLabels)
	id.JobInformation = capInsight(id.JobInformation)
	id.JobEvents = capInsight(id.JobEvents)
	id.JobPodEvents = capInsight(id.JobPodEvents)
	id.RelatedEvents = capInsight(id.RelatedEvents)
	id.ContainerMetrics = capInsight(id.ContainerMetrics)
	id.Traces = capInsight(id.Traces)
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
// them from dropping costs little.
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

	total := insightSize(id.Traces) + insightSize(id.AlertLabels)
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
	return "List/search events with typed filters: subject_name, subject_namespace, subject_type, " +
		"finding_type (issue/configuration_change/SLO/Anomaly), aggregation_key, priority, and a time " +
		"range (start_time/end_time ISO8601, or relative_range like '24h'/'7d'). Returns a compact " +
		"evidence manifest per event (available evidence types + key insights), not full raw evidence — " +
		"use get_event_by_id or get_event_evidence to drill into a specific event afterward. Use this for " +
		"any 'show me recent/list events matching X' question instead of writing raw SQL."
}

func (t ListEventsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"subject_name": {Type: core.ToolSchemaTypeString, Description: "ILIKE (substring) match on subject_name."},
			"subject_namespace": {
				Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
				Description: "One or more exact namespace values, e.g. a question spanning several namespaces at once.",
			},
			"subject_type": {Type: core.ToolSchemaTypeString, Description: "ILIKE match on subject_type (pod, deployment, node, etc. — not a fixed set)."},
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
			"start_time":     {Type: core.ToolSchemaTypeString, Description: "ISO8601. Alternative to relative_range."},
			"end_time":       {Type: core.ToolSchemaTypeString, Description: "ISO8601. Defaults to now."},
			"relative_range": {Type: core.ToolSchemaTypeString, Description: "Shorthand duration, e.g. '24h', '7d'. Alternative to start_time."},
			"limit":          {Type: core.ToolSchemaTypeInteger, Default: 10, Description: "Max rows, default 10."},
		},
	}
}

func (t ListEventsTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t ListEventsTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	var whereClauses []string

	if v := stringArg(input, "subject_name"); v != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("subject_name ILIKE %s", pq.QuoteLiteral("%"+v+"%")))
	}
	if vs := stringArrayArg(input, "subject_namespace"); len(vs) > 0 {
		whereClauses = append(whereClauses, "subject_namespace IN ("+quotedList(vs)+")")
	}
	if v := stringArg(input, "subject_type"); v != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("subject_type ILIKE %s", pq.QuoteLiteral(v)))
	}
	if vs := stringArrayArg(input, "finding_type"); len(vs) > 0 {
		whereClauses = append(whereClauses, "finding_type IN ("+quotedList(vs)+")")
	}
	if vs := stringArrayArg(input, "aggregation_key"); len(vs) > 0 {
		whereClauses = append(whereClauses, "aggregation_key IN ("+quotedList(vs)+")")
	}
	if vs := stringArrayArg(input, "priority"); len(vs) > 0 {
		whereClauses = append(whereClauses, "priority IN ("+quotedList(vs)+")")
	}

	startTime, endTime, err := resolveTimeRange(input)
	if err != nil {
		return eventsV2ErrorResponse(err), nil
	}
	if startTime != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("starts_at >= %s", pq.QuoteLiteral(startTime)))
	}
	if endTime != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("starts_at <= %s", pq.QuoteLiteral(endTime)))
	}

	limit := intArg(input, "limit", 10)

	query := "SELECT id, title, starts_at, priority, source, aggregation_key, finding_type, subject_type, " +
		"subject_name, subject_namespace, evidences, nb_status, computed_priority, computed_score, event_count FROM events"
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += " ORDER BY starts_at DESC"

	view := resolveEventsViewPlaceholders(eventsViewWithRanking, nbCtx.AccountId, false)
	resp, data, err := sqlToolCall(nbCtx, query, "events", view, limit, nil)
	if err != nil {
		return resp, err
	}

	buildManifestOnlyEvidence(data)

	bytesData, marshalErr := common.MarshalJson(data)
	if marshalErr != nil {
		return core.NBToolResponse{}, marshalErr
	}
	resp.Data = string(bytesData)
	resp.References = eventReferences(nbCtx, data)
	return resp, nil
}

// ---------------------------------------------------------------------------
// aggregate_events — covers GROUP BY/COUNT traffic (~4-5% of real
// events_execute traffic, but the natural shape for "how many X" and
// alert-noise questions).
// ---------------------------------------------------------------------------

var allowedGroupByColumns = []string{
	"aggregation_key", "subject_namespace", "subject_type", "finding_type", "priority", "source", "nb_status",
}

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
	return "Count or group events by a single column (aggregation_key, subject_namespace, subject_type, " +
		"finding_type, priority, source, or nb_status), optionally scoped by subject_namespace/finding_type/" +
		"time range. Set count_distinct_fingerprint=true to count unique event patterns (dedup) instead of " +
		"raw occurrences. Use this for 'how many X' / 'top N by Y' / alert-noise questions instead of " +
		"writing raw SQL with GROUP BY/COUNT."
}

func (t AggregateEventsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"group_by": {
				Type:        core.ToolSchemaTypeString,
				Enum:        []any{"aggregation_key", "subject_namespace", "subject_type", "finding_type", "priority", "source", "nb_status"},
				Description: "Column to group by.",
			},
			"count_distinct_fingerprint": {Type: core.ToolSchemaTypeBoolean, Description: "Count unique event patterns (dedup) instead of raw occurrences."},
			"subject_namespace": {
				Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
				Description: "Optional: scope to one or more namespaces.",
			},
			"finding_type": {
				Type: core.ToolSchemaTypeArray, Items: map[string]any{"type": "string"},
				Description: "Optional: one or more of issue, configuration_change, SLO, Anomaly.",
			},
			"start_time":     {Type: core.ToolSchemaTypeString, Description: "ISO8601. Alternative to relative_range."},
			"end_time":       {Type: core.ToolSchemaTypeString, Description: "ISO8601. Defaults to now."},
			"relative_range": {Type: core.ToolSchemaTypeString, Description: "Shorthand duration, e.g. '24h', '7d'. Alternative to start_time."},
		},
		Required: []string{"group_by"},
	}
}

func (t AggregateEventsTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t AggregateEventsTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	groupBy := stringArg(input, "group_by")
	if !isAllowedGroupByColumn(groupBy) {
		return eventsV2ErrorResponse(fmt.Errorf("group_by must be one of: %s", strings.Join(allowedGroupByColumns, ", "))), nil
	}

	countExpr := "count(*)"
	if boolArg(input, "count_distinct_fingerprint") {
		countExpr = "count(DISTINCT fingerprint)"
	}

	var whereClauses []string
	if vs := stringArrayArg(input, "subject_namespace"); len(vs) > 0 {
		whereClauses = append(whereClauses, "subject_namespace IN ("+quotedList(vs)+")")
	}
	if vs := stringArrayArg(input, "finding_type"); len(vs) > 0 {
		whereClauses = append(whereClauses, "finding_type IN ("+quotedList(vs)+")")
	}
	startTime, endTime, err := resolveTimeRange(input)
	if err != nil {
		return eventsV2ErrorResponse(err), nil
	}
	if startTime != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("starts_at >= %s", pq.QuoteLiteral(startTime)))
	}
	if endTime != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("starts_at <= %s", pq.QuoteLiteral(endTime)))
	}

	// group_by is enum-validated above against a fixed allowlist, so it's safe
	// to interpolate directly — this is the one column name events_v2 lets
	// caller input drive, and it can only ever be one of those seven values.
	query := fmt.Sprintf("SELECT %s, %s AS count FROM events", groupBy, countExpr)
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += fmt.Sprintf(" GROUP BY %s ORDER BY count DESC", groupBy)

	view := resolveEventsViewPlaceholders(eventsViewWithId, nbCtx.AccountId, false)
	resp, _, err := sqlToolCall(nbCtx, query, "events", view, 0, nil)
	if err != nil {
		return resp, err
	}
	return resp, nil
}
