package observability

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpenObserveLogSource implements LogSource interface for OpenObserve
type OpenObserveLogSource struct{}

// openObserveLogLabelMapping maps canonical field names to OpenObserve column names.
//
// OpenObserve has no fixed log schema — it flattens whatever the shipper sends, joining
// nested keys with '_'. These are the OpenTelemetry collector's names (resource attributes
// k8s.namespace.name → k8s_namespace_name), which is the dominant OpenObserve ingestion
// path. A Fluent Bit pipeline writes kubernetes_namespace_name instead; those deployments
// can remap per-account via the label-mapping override rather than editing this table.
var openObserveLogLabelMapping = map[string]string{
	"timestamp": "_timestamp",
	"body":      "body",
	"message":   "body",
	"namespace": "k8s_namespace_name",
	"pod":       "k8s_pod_name",
	"container": "k8s_container_name",
	"node":      "k8s_node_name",
	"workload":  "k8s_deployment_name",
	"app":       "k8s_deployment_name",
	"cluster":   "k8s_cluster",
	"host":      "host_name",
	"hostname":  "host_name",
	"service":   "service_name",
	// severity is the OTel severity number (Int64) on collector-fed streams, not a
	// level string — see the log-group column resolution for why that distinction matters.
	"severity": "severity",
	"level":    "severity",
}

// Kept identical to OpenObserveTraceSource.GetSupportedOperators — both render through
// buildOpenObserveBinaryClause. Inline literals, not a shared var: the operator-catalog
// drift test reads string literals out of each GetSupportedOperators body.
func (s *OpenObserveLogSource) GetSupportedOperators() []string {
	return []string{
		"_eq", "_neq", "_in", "_not_in", "_gt", "_gte", "_lt", "_lte",
		"_contains", "_like", "_nlike", "_ilike", "_is_null",
	}
}

func (s *OpenObserveLogSource) GetLabelMapping() map[string]string {
	return openObserveLogLabelMapping
}

func (s *OpenObserveLogSource) GetIgnoredQueryRequestKeys() []string {
	return []string{} // No keys ignored by default
}

type openObserveSearchRequest struct {
	Query struct {
		SQL       string `json:"sql"`
		StartTime int64  `json:"start_time"`
		EndTime   int64  `json:"end_time"`
		Size      int    `json:"size,omitempty"`
	} `json:"query"`
}

type openObserveSearchResponse struct {
	Hits []map[string]any `json:"hits"`
}

func escapeOpenObserveString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// errOpenObserveStreamNotFound reports that a stream does not exist. Callers disagree on
// whether that is a failure: a missing *log* stream is a misconfiguration worth surfacing,
// while a missing *metric* stream is routine — the __name__ list contains families (a
// summary's base name, say) that never received samples and so have no stream of their own.
var errOpenObserveStreamNotFound = errors.New("openobserve stream not found")

// decodeOpenObserveSearchResponse decodes a /_search response, preserving numbers as their
// literal text rather than converting to float64.
//
// This matters for 64-bit values: a span's start_time is epoch nanoseconds (~1.79e18),
// roughly 200x beyond float64's exact-integer limit of 2^53, so a plain decode silently
// discards the low digits before any formatting runs. Trace waterfalls built from those
// values collapse to identical timestamps. json.Number keeps the original digits, and
// openObserveNumber / openObserveInt64 read it back.
func decodeOpenObserveSearchResponse(r io.Reader) (openObserveSearchResponse, error) {
	var searchResp openObserveSearchResponse
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&searchResp); err != nil {
		return openObserveSearchResponse{}, err
	}
	return searchResp, nil
}

// openObserveInt64 extracts an exact integer from a decoded JSON cell. json.Number keeps
// full 64-bit precision; float64 is accepted for callers that decoded without UseNumber.
func openObserveInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

// formatOpenObserveLabelValue renders a decoded JSON cell as the literal string a filter
// will be built from.
//
// fmt.Sprintf("%v") is wrong here: JSON numbers decode to float64, and %v prints large
// ones in scientific notation — an Int64 timestamp of 1786612792471605 becomes
// "1.786612792471605e+15", which is then round-tripped into `col = '1.78...e+15'` and
// matches nothing. Formatting with 'f' keeps integers intact.
func formatOpenObserveLabelValue(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case json.Number:
		// Already the literal text OpenObserve sent — no precision to lose.
		return n.String()
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(n), 'f', -1, 32)
	case bool:
		return strconv.FormatBool(n)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// openObserveAuthHeader builds the Basic auth header every OpenObserve API call needs.
func openObserveAuthHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func isSafeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func buildOpenObserveBinaryClause(binary query.BinaryWhereClause, mapping map[string]string) (string, error) {
	var parts []string
	for field, ops := range binary {
		col := field
		if mapped, ok := mapping[col]; ok {
			col = mapped
		}
		if !isSafeIdentifier(col) {
			return "", fmt.Errorf("invalid or unsafe column name: %q", col)
		}
		for op, val := range ops {
			strVal := fmt.Sprintf("%v", val)
			switch op {
			case query.Eq:
				parts = append(parts, fmt.Sprintf("%s = '%s'", col, escapeOpenObserveString(strVal)))
			case query.Nq:
				parts = append(parts, fmt.Sprintf("%s != '%s'", col, escapeOpenObserveString(strVal)))
			case query.In, query.NotIn:
				parts = append(parts, openObserveInClause(col, op == query.NotIn, val))
			case query.Gt, query.Gte, query.Lt, query.Lte:
				parts = append(parts, fmt.Sprintf("%s %s %s", col, openObserveComparison[op], openObserveOrderedLiteral(col, val)))
			case query.Contains:
				parts = append(parts, fmt.Sprintf("str_match(%s, '%s')", col, escapeOpenObserveString(strVal)))
			case query.ILike:
				// A LIKE pattern, like every other provider's _ilike (Loki and ES anchor it
				// too). This used to be str_match_ignore_case, a substring match that took
				// the % wildcards literally — so the pattern the UI's "ILIKE" chip and the
				// logs agent both send, "%error%", matched nothing and read as "no logs".
				parts = append(parts, fmt.Sprintf("%s ILIKE '%s'", col, escapeOpenObserveString(strVal)))
			case query.IsNull:
				// true = the field is absent, false = present (the builder's exists chips).
				// The value-less "is null" chip arrives as "" and means absent. Any other
				// value is refused rather than guessed.
				switch strings.ToLower(strVal) {
				case "true", "":
					parts = append(parts, fmt.Sprintf("%s IS NULL", col))
				case "false":
					parts = append(parts, fmt.Sprintf("%s IS NOT NULL", col))
				default:
					return "", fmt.Errorf("openobserve: _is_null takes true or false, not %q", strVal)
				}
			case query.NLike:
				parts = append(parts, fmt.Sprintf("%s NOT LIKE '%s'", col, escapeOpenObserveString(strVal)))
			case query.Like:
				// SQL LIKE, so the caller's % / _ stay wildcards — that is the whole point
				// of the operator (callers pass patterns like "5%" to select 5xx statuses).
				// Only the quote is escaped, which is what would otherwise break out of the
				// literal. Without this case the builder rejected the operator outright and
				// took the entire query down, so a workload trace search that filtered on an
				// HTTP status class returned nothing at all.
				parts = append(parts, fmt.Sprintf("%s LIKE '%s'", col, escapeOpenObserveString(strVal)))
			default:
				return "", fmt.Errorf("unsupported binary operator for OpenObserve: %s", op)
			}
		}
	}
	return strings.Join(parts, " AND "), nil
}

var openObserveComparison = map[query.BinaryWhereClauseType]string{
	query.Gt: ">", query.Gte: ">=", query.Lt: "<", query.Lte: "<=",
}

// openObserveInClause renders _in / _not_in. An empty list is answered rather than
// rendered, because "IN ()" is a syntax error: nothing is IN an empty set, and
// everything is NOT IN it.
func openObserveInClause(col string, negate bool, val any) string {
	var items []any
	switch list := val.(type) {
	case nil:
		// `"_in": null` is the empty set, not the literal string "<nil>".
	case []any:
		items = list
	case []string:
		for _, s := range list {
			items = append(items, s)
		}
	default:
		items = []any{val}
	}
	if len(items) == 0 {
		if negate {
			return "TRUE"
		}
		return "FALSE"
	}
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, "'"+escapeOpenObserveString(fmt.Sprintf("%v", item))+"'")
	}
	keyword := "IN"
	if negate {
		keyword = "NOT IN"
	}
	return fmt.Sprintf("%s %s (%s)", col, keyword, strings.Join(quoted, ", "))
}

// openObserveOrderedLiteral renders the right-hand side of a range comparison. A numeric
// value goes out bare even when it arrives as a string: OpenObserve compares a quoted
// literal as text, so `duration >= '3000'` also matches 400 and 50000 — measurably
// different rows from `duration >= 3000`. Equality and IN coerce correctly and stay quoted.
//
// _timestamp is epoch microseconds, and the logs agent bounds it with ISO strings
// ("timestamp >= 2026-09-25T18:18:00Z"). Quoted, that compares as text and matches
// nothing — a silent zero — so a parseable time is converted to microseconds.
func openObserveOrderedLiteral(col string, val any) string {
	s := fmt.Sprintf("%v", val)
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if col == "_timestamp" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return strconv.FormatInt(t.UnixMicro(), 10)
		}
	}
	return "'" + escapeOpenObserveString(s) + "'"
}

// buildOpenObserveWhere renders a where clause, handing each _binary group to
// binaryClause. Every key present on one clause is ANDed: callers routinely send
// _binary and _or side by side (the workload traces tab does), and returning after the
// first key silently dropped the rest — an unapplied filter, so a wider result set.
func buildOpenObserveWhere(where query.QueryWhereClause, binaryClause func(query.BinaryWhereClause) (string, error)) (string, error) {
	var parts []string

	if len(where.Binary) > 0 {
		part, err := binaryClause(where.Binary)
		if err != nil {
			return "", err
		}
		if part != "" {
			parts = append(parts, part)
		}
	}

	for _, group := range []struct {
		clauses []query.QueryWhereClause
		joiner  string
	}{{where.And, " AND "}, {where.Or, " OR "}} {
		var sub []string
		for _, c := range group.clauses {
			part, err := buildOpenObserveWhere(c, binaryClause)
			if err != nil {
				return "", err
			}
			if part != "" {
				sub = append(sub, part)
			}
		}
		switch len(sub) {
		case 0:
		case 1:
			parts = append(parts, sub[0])
		default:
			parts = append(parts, "("+strings.Join(sub, group.joiner)+")")
		}
	}

	if where.Not != nil {
		notPart, err := buildOpenObserveWhere(*where.Not, binaryClause)
		if err != nil {
			return "", err
		}
		if notPart != "" {
			parts = append(parts, fmt.Sprintf("NOT (%s)", notPart))
		}
	}

	switch len(parts) {
	case 0:
		return "", nil
	case 1:
		return parts[0], nil
	default:
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}
}

func buildOpenObserveSQLWhereClause(where query.QueryWhereClause) (string, error) {
	return buildOpenObserveWhere(where, func(b query.BinaryWhereClause) (string, error) {
		return buildOpenObserveBinaryClause(b, openObserveLogLabelMapping)
	})
}

func (s *OpenObserveLogSource) buildSQL(req FetchLogRequest, stream string) (string, error) {
	// Callers pass a config-resolved stream, which GetOpenObserveConfigs has already
	// validated. Re-checked here so the function is safe for any caller, not just that one.
	if !integrations.IsSafeOpenObserveStreamName(stream) {
		return "", fmt.Errorf("invalid or unsafe stream name: %q", stream)
	}

	whereClause, err := buildOpenObserveSQLWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Query) != "" {
		limit := req.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 10000 {
			limit = 10000
		}
		return buildOpenObserveRawSQL(req.Query, stream, whereClause, "_timestamp DESC", limit)
	}

	limit := req.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}

	sql := fmt.Sprintf(`SELECT * FROM "%s"`, stream)
	if whereClause != "" {
		sql += " WHERE " + whereClause
	}

	sql += fmt.Sprintf(" ORDER BY _timestamp DESC LIMIT %d", limit)

	return sql, nil
}

func (s *OpenObserveLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}
	return s.buildSQL(s.withSchemaFallbacks(ctx, req, cfg), cfg.LogStream)
}

// withSchemaFallbacks applies every filter rewrite that depends on which columns the
// stream actually carries. The where clause arrives already rewritten through the
// account's effective label mapping, so the workload and pod columns are read from that
// same mapping — an account that remaps them (a Fluent Bit shipper's kubernetes_* names)
// would otherwise never get the fallback. Resolving it needs the tenant, so a caller
// without a security context gets the provider defaults.
func (s *OpenObserveLogSource) withSchemaFallbacks(ctx *security.RequestContext, req FetchLogRequest, cfg integrations.OpenObserveConfig) FetchLogRequest {
	req = withOpenObserveCorrelationIDs(req, cfg)
	mapping := openObserveLogLabelMapping
	if ctx != nil && ctx.GetSecurityContext() != nil {
		mapping = getMergedLabelMapping(ctx, req.AccountId, s)
	}
	return withOpenObserveWorkloadPodFallback(req, cfg, mapping["app"], mapping["pod"])
}

// openObserveCorrelationFields are the trace-correlation fields the traces screen's Logs tab
// and the agents filter logs on. A collector that tails container stdout has no such column —
// the id sits inside the message — so a filter on it matched nothing: the tab showed no logs
// for a trace whose id was in the log body.
var openObserveCorrelationFields = map[string]bool{"trace_id": true, "span_id": true}

// withOpenObserveCorrelationIDs rewrites trace_id / span_id equality and set filters into a
// match on the message body, but only when the stream has no column of that name. A stream
// fed by OTLP logs with trace context keeps its real column. The schema is fetched only when
// such a filter is present; if it cannot be read the request is left as it was.
func withOpenObserveCorrelationIDs(req FetchLogRequest, cfg integrations.OpenObserveConfig) FetchLogRequest {
	if !whereMentionsField(req.QueryRequest.Where, openObserveCorrelationFields) {
		return req
	}
	fields, err := fetchOpenObserveStreamFields(cfg.URL, cfg.OrgID, cfg.Username, cfg.Password, cfg.LogStream, "logs")
	if err != nil {
		return req
	}
	missing := map[string]bool{}
	for f := range openObserveCorrelationFields {
		if _, ok := fields[f]; !ok {
			missing[f] = true
		}
	}
	if len(missing) == 0 {
		return req
	}
	req.QueryRequest.Where = rewriteCorrelationToBody(req.QueryRequest.Where, missing)
	return req
}

func whereMentionsField(w query.QueryWhereClause, fields map[string]bool) bool {
	for f := range w.Binary {
		if fields[f] {
			return true
		}
	}
	for _, c := range append(append([]query.QueryWhereClause{}, w.And...), w.Or...) {
		if whereMentionsField(c, fields) {
			return true
		}
	}
	return w.Not != nil && whereMentionsField(*w.Not, fields)
}

// rewriteCorrelationToBody returns a copy of w where each _eq / _in on a missing correlation
// field becomes a body substring match (ids are unique hex strings, so a substring is exact
// enough). Other operators on those fields are left alone.
func rewriteCorrelationToBody(w query.QueryWhereClause, missing map[string]bool) query.QueryWhereClause {
	out := query.QueryWhereClause{}
	var extra []query.QueryWhereClause
	if len(w.Binary) > 0 {
		out.Binary = query.BinaryWhereClause{}
		for field, ops := range w.Binary {
			if !missing[field] {
				out.Binary[field] = ops
				continue
			}
			kept := map[query.BinaryWhereClauseType]any{}
			for op, v := range ops {
				switch op {
				case query.Eq:
					extra = append(extra, query.QueryWhereClause{Binary: query.BinaryWhereClause{"body": {query.Contains: v}}})
				case query.In:
					var alts []query.QueryWhereClause
					for _, id := range splitOpenObserveList(v) {
						alts = append(alts, query.QueryWhereClause{Binary: query.BinaryWhereClause{"body": {query.Contains: id}}})
					}
					if len(alts) == 0 {
						kept[op] = v // empty set: let the builder answer FALSE
						continue
					}
					extra = append(extra, query.QueryWhereClause{Or: alts})
				default:
					kept[op] = v
				}
			}
			if len(kept) > 0 {
				out.Binary[field] = kept
			}
		}
		if len(out.Binary) == 0 {
			out.Binary = nil
		}
	}
	for _, c := range w.And {
		out.And = append(out.And, rewriteCorrelationToBody(c, missing))
	}
	out.And = append(out.And, extra...)
	for _, c := range w.Or {
		out.Or = append(out.Or, rewriteCorrelationToBody(c, missing))
	}
	if w.Not != nil {
		n := rewriteCorrelationToBody(*w.Not, missing)
		out.Not = &n
	}
	return out
}

// withOpenObserveWorkloadPodFallback lets a workload filter match the pods of workloads the
// shipper records no name for. The canonical app / workload labels map to the deployment
// column, which the OTel collector fills only for a Deployment's pods — so filtering by a
// Job, CronJob, StatefulSet or DaemonSet matched nothing, and the Log Groups drill-down
// (which filters by the workload the group reports) showed no logs for them. The schema is
// fetched only when such a filter is present; if it cannot be read, or the stream has no
// pod column to fall back on, the request is left as it was.
func withOpenObserveWorkloadPodFallback(req FetchLogRequest, cfg integrations.OpenObserveConfig, workloadCol, podCol string) FetchLogRequest {
	if workloadCol == "" || podCol == "" || workloadCol == podCol ||
		!whereMentionsField(req.QueryRequest.Where, map[string]bool{workloadCol: true}) {
		return req
	}
	fields, err := fetchOpenObserveStreamFields(cfg.URL, cfg.OrgID, cfg.Username, cfg.Password, cfg.LogStream, "logs")
	if err != nil {
		return req
	}
	if _, ok := fields[podCol]; !ok {
		return req
	}
	_, hasWorkloadCol := fields[workloadCol]
	req.QueryRequest.Where = rewriteWorkloadWithPodFallback(req.QueryRequest.Where, workloadCol, podCol, hasWorkloadCol)
	return req
}

// rewriteWorkloadWithPodFallback returns a copy of w where each _eq / _in on workloadCol
// also accepts records that carry no workload name but whose pod belongs to that workload:
// the pod is named <workload>-<suffix>, or is the workload itself (a bare pod). A record
// that does carry a workload name still has to match it exactly. When the stream has no
// workload column at all, the pod match is the whole filter — referencing the missing
// column would make OpenObserve reject the query. Other operators are left alone.
func rewriteWorkloadWithPodFallback(w query.QueryWhereClause, workloadCol, podCol string, hasWorkloadCol bool) query.QueryWhereClause {
	matchOne := func(v any) query.QueryWhereClause {
		name := fmt.Sprintf("%v", v)
		podMatch := query.QueryWhereClause{Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{podCol: {query.Eq: name}}},
			{Binary: query.BinaryWhereClause{podCol: {query.Like: name + "-%"}}},
		}}
		if !hasWorkloadCol {
			return podMatch
		}
		unrecorded := query.QueryWhereClause{Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{workloadCol: {query.IsNull: true}}},
			{Binary: query.BinaryWhereClause{workloadCol: {query.Eq: ""}}},
		}}
		return query.QueryWhereClause{Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{workloadCol: {query.Eq: name}}},
			{And: []query.QueryWhereClause{unrecorded, podMatch}},
		}}
	}

	out := query.QueryWhereClause{}
	var extra []query.QueryWhereClause
	if len(w.Binary) > 0 {
		out.Binary = query.BinaryWhereClause{}
		for field, ops := range w.Binary {
			if field != workloadCol {
				out.Binary[field] = ops
				continue
			}
			kept := map[query.BinaryWhereClauseType]any{}
			for op, v := range ops {
				switch op {
				case query.Eq:
					extra = append(extra, matchOne(v))
				case query.In:
					var alts []query.QueryWhereClause
					for _, name := range splitOpenObserveList(v) {
						alts = append(alts, matchOne(name))
					}
					if len(alts) == 0 {
						kept[op] = v // empty set: let the builder answer FALSE
						continue
					}
					extra = append(extra, query.QueryWhereClause{Or: alts})
				default:
					kept[op] = v
				}
			}
			if len(kept) > 0 {
				out.Binary[field] = kept
			}
		}
		if len(out.Binary) == 0 {
			out.Binary = nil
		}
	}
	for _, c := range w.And {
		out.And = append(out.And, rewriteWorkloadWithPodFallback(c, workloadCol, podCol, hasWorkloadCol))
	}
	out.And = append(out.And, extra...)
	for _, c := range w.Or {
		out.Or = append(out.Or, rewriteWorkloadWithPodFallback(c, workloadCol, podCol, hasWorkloadCol))
	}
	if w.Not != nil {
		n := rewriteWorkloadWithPodFallback(*w.Not, workloadCol, podCol, hasWorkloadCol)
		out.Not = &n
	}
	return out
}

// splitOpenObserveList normalises an _in value to its elements.
func splitOpenObserveList(v any) []any {
	switch list := v.(type) {
	case []any:
		return list
	case []string:
		out := make([]any, len(list))
		for i, s := range list {
			out[i] = s
		}
		return out
	case nil:
		return nil
	default:
		return []any{v}
	}
}

func (s *OpenObserveLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	logs, _, err := s.QueryLogsDetailed(ctx, req)
	return logs, err
}

func (s *OpenObserveLogSource) QueryLogsDetailed(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, LogQueryDetails, error) {
	details := LogQueryDetails{}
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, details, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	sql, err := s.buildSQL(s.withSchemaFallbacks(ctx, req, cfg), cfg.LogStream)
	if err != nil {
		return nil, details, err
	}
	details.ExecutedQuery = sql

	// OpenObserve requires microseconds
	startTimeMicros := req.StartTime * 1000
	endTimeMicros := req.EndTime * 1000

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, details, fmt.Errorf("failed to marshal search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", cfg.URL, cfg.OrgID)
	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return nil, details, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		text := strings.TrimSpace(string(bodyBytes))

		// OpenObserve answers a window it holds no data for with "stream not found",
		// the same body it returns for a stream that genuinely does not exist. That is
		// an empty result, not a failure: surfacing it as an error makes an idle
		// fifteen minutes read as a broken integration, and the assistant then reports
		// the log backend as disconnected. The metric search path already collapses
		// these — see isOpenObserveEmptyResultError.
		if isOpenObserveEmptyResultError(text) {
			return []OutputLog{}, details, nil
		}

		return nil, details, fmt.Errorf("OpenObserve query failed with status %d: %s", resp.StatusCode, text)
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return nil, details, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	var outputs []OutputLog
	for _, hit := range searchResp.Hits {
		out := OutputLog{
			Labels: make(map[string]any),
		}

		for k, v := range hit {
			switch k {
			case "_timestamp":
				if micros, ok := openObserveInt64(v); ok {
					out.Timestamp = time.UnixMicro(micros).UTC().Format(time.RFC3339Nano)
				} else if tStr, ok := v.(string); ok {
					out.Timestamp = tStr
				}
				out.Labels[k] = v
			case "message", "log", "body":
				if str, ok := v.(string); ok {
					out.Message = str
				}
				out.Labels[k] = v
			case "severity", "level":
				if str, ok := v.(string); ok {
					out.Severity = str
				}
				out.Labels[k] = v
			default:
				out.Labels[k] = v
			}
		}

		// The log-row detail renders labels.timestamp as epoch nanoseconds. Set it
		// explicitly, and after the loop: otherwise normalizeOutputLogLabels aliases the
		// raw microsecond _timestamp into it (every row read January 1970), and a shipper
		// field that is itself named "timestamp" would win or lose on map order.
		if micros, ok := openObserveInt64(hit["_timestamp"]); ok {
			out.Labels["timestamp"] = micros * 1000
		}

		outputs = append(outputs, out)
	}

	return outputs, details, nil
}

func buildOpenObserveLabelSampleRequest(req FetchLogLabelRequest, now time.Time) FetchLogRequest {
	startTime, endTime := req.StartTime, req.EndTime
	if startTime == 0 && endTime == 0 {
		endTime = now.UnixMilli()
		startTime = now.Add(-time.Hour).UnixMilli()
	}

	return FetchLogRequest{
		AccountId:         req.AccountId,
		LogProvider:       req.LogProvider,
		LogProviderSource: req.LogProviderSource,
		StartTime:         startTime,
		EndTime:           endTime,
		Limit:             100,
	}
}

func collectOpenObserveLogLabels(logs []OutputLog) []OutputLogLabel {
	labelSet := make(map[string]struct{})
	for _, log := range logs {
		for name := range log.Labels {
			labelSet[name] = struct{}{}
		}
	}

	labelNames := make([]string, 0, len(labelSet))
	for name := range labelSet {
		labelNames = append(labelNames, name)
	}
	sort.Strings(labelNames)

	labels := make([]OutputLogLabel, 0, len(labelNames))
	for _, name := range labelNames {
		labels = append(labels, OutputLogLabel{Label: name, Attributes: make(map[string]any)})
	}
	return labels
}

func (s *OpenObserveLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	fetchReq := buildOpenObserveLabelSampleRequest(req, time.Now())

	logs, err := s.QueryLogs(ctx, fetchReq)
	if err != nil {
		return nil, err
	}

	return collectOpenObserveLogLabels(logs), nil
}

func (s *OpenObserveLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	// Query unique values for the requested label using SQL GROUP BY
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	col := req.LabelName
	if mapped, ok := openObserveLogLabelMapping[col]; ok {
		col = mapped
	}
	if !isSafeIdentifier(col) {
		return nil, fmt.Errorf("invalid or unsafe label name: %q", col)
	}

	// Skip nulls and blanks: they render as empty rows in the value dropdown, and the
	// dropdown is the only consumer of this call.
	sql := fmt.Sprintf(`SELECT %s FROM "%s" WHERE %s IS NOT NULL GROUP BY %s ORDER BY %s LIMIT %d`,
		col, cfg.LogStream, col, col, col, labelValuesPageSize)

	// The label-value dropdown fires before the user has committed a filter, and several
	// callers omit the window entirely. Without a default the range is 0→0 and OpenObserve
	// correctly returns nothing, which surfaces as "no suggestions".
	startTimeMicros, endTimeMicros := openObserveTimeRangeMicros(req.StartTime, req.EndTime, time.Now())

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", cfg.URL, cfg.OrgID)
	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		text := strings.TrimSpace(string(bodyBytes))
		// A label absent from this stream has no values; that is an empty list, not an error.
		if isOpenObserveEmptyResultError(text) {
			return []OutputLogLabelValue{}, nil
		}
		return nil, fmt.Errorf("OpenObserve query failed with status %d: %s", resp.StatusCode, text)
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	values := make([]OutputLogLabelValue, 0, len(searchResp.Hits))
	for _, hit := range searchResp.Hits {
		val, ok := hit[col]
		if !ok || val == nil {
			continue
		}
		strVal := formatOpenObserveLabelValue(val)
		if strVal == "" {
			continue
		}
		values = append(values, OutputLogLabelValue{
			Value:      strVal,
			Attributes: make(map[string]any),
		})
	}

	return values, nil
}

// openObserveLogGroupLimit caps how many aggregated error patterns are returned,
// matching the other log-group providers.
const openObserveLogGroupLimit = 100

// openObserveErrorSeverities are the lower-cased severity values treated as errors.
var openObserveErrorSeverities = []string{"error", "critical", "fatal", "err", "crit"}

// openObserveErrorMessagePatterns are matched against the message body when a record
// carries no usable severity. Deliberately upper-case: a case-insensitive match on
// "error" hits ordinary prose ("no errors found") far too often.
var openObserveErrorMessagePatterns = []string{"ERROR", "FATAL", "CRITICAL"}

// openObserveExcludedContainers are infrastructure containers whose logs are noise in
// the error-pattern view.
var openObserveExcludedContainers = []string{"prometheus", "grafana", "nudgebee-agent"}

// openObserveLogGroupCandidates lists the accepted OpenObserve column names per logical
// role, in priority order. OpenObserve flattens nested ingest payloads with '_', so the
// same logical field lands under a different name depending on the shipper (OTel
// collector → k8s_namespace_name, Fluent Bit → kubernetes_namespace_name, and so on).
// The stream schema is read at runtime and the first candidate present wins, so a role
// with no match simply drops out of the GROUP BY instead of producing SQL that
// references a non-existent column — which OpenObserve rejects outright.
var openObserveLogGroupCandidates = struct {
	Message        []string
	Severity       []string
	SeverityNumber []string
	Namespace      []string
	Pod            []string
	Container      []string
	Workload       []string
}{
	Message:  []string{"body", "message", "log"},
	Severity: []string{"severity_text", "severity", "level", "log_level"},
	// Only the OTel-specified name is accepted for numeric severity. Other numeric
	// "level"/"severity" columns are ambiguous — syslog counts down (0 = emerg, 3 = err)
	// while OTel counts up (17+ = error), and guessing wrong inverts the filter so INFO
	// records would be reported as errors.
	SeverityNumber: []string{"severity_number"},
	Namespace:      []string{"k8s_namespace_name", "kubernetes_namespace_name", "namespace_name", "namespace"},
	Pod:            []string{"k8s_pod_name", "kubernetes_pod_name", "pod_name", "pod"},
	Container:      []string{"k8s_container_name", "kubernetes_container_name", "container_name", "container"},
	// The real workload name, when the shipper records one. Preferred over deriving it
	// from the pod name: the derived value is a guess, and the drill-down turns whatever
	// we report here into an `app` filter — which maps to the deployment column. If the
	// two disagree, clicking a group silently returns no logs.
	Workload: []string{"k8s_deployment_name", "kubernetes_deployment_name", "deployment"},
}

// openObserveOtelErrorSeverityNumber is the lowest OTel severity number meaning ERROR.
// The OTel spec assigns TRACE 1-4, DEBUG 5-8, INFO 9-12, WARN 13-16, ERROR 17-20, FATAL 21-24.
const openObserveOtelErrorSeverityNumber = 17

// openObserveOtelFatalSeverityNumber is the lowest OTel severity number meaning FATAL.
const openObserveOtelFatalSeverityNumber = 21

// openObserveLogGroupCols holds the stream column actually backing each logical role.
// Every field except Message may be empty when the stream has no matching column.
type openObserveLogGroupCols struct {
	Message string
	// Severity is a string-typed severity column (e.g. severity_text = "ERROR").
	Severity string
	// SeverityNumber is a numeric OTel severity column (severity_number = 17).
	// A stream can carry both; string severity is preferred for filtering because
	// it is what the record actually reads as.
	SeverityNumber string
	Namespace      string
	Pod            string
	// Workload is the shipper-recorded workload name. Empty on streams that don't
	// carry one, in which case it is derived from the pod name per row.
	Workload  string
	Container string
}

// openObserveStreamSchema is the subset of GET /api/{org}/streams/{stream}/schema we read.
type openObserveStreamSchema struct {
	Name   string `json:"name"`
	Schema []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"schema"`
}

// openObserveNonStringTypes are the Arrow type names OpenObserve reports for columns that
// cannot be used in a string context. Anything not listed — including an empty or
// unrecognised type name — is treated as usable: the schema endpoint is the only type
// source available, and dropping a column because its type name is unfamiliar would break
// working streams for no gain.
var openObserveNonStringTypes = map[string]struct{}{
	"int8": {}, "int16": {}, "int32": {}, "int64": {},
	"uint8": {}, "uint16": {}, "uint32": {}, "uint64": {},
	"float16": {}, "float32": {}, "float64": {},
	"boolean": {}, "bool": {}, "binary": {}, "largebinary": {},
	"date32": {}, "date64": {}, "null": {},
}

// isOpenObserveStringColumn reports whether a column can be used with LOWER(), LIKE, and
// string-literal comparison. Applying LOWER() to an Int64 column makes DataFusion reject
// the entire query with a type_coercion planning error, so every role that lands in a
// string context must be type-checked before it reaches the SQL.
func isOpenObserveStringColumn(fieldType string) bool {
	_, nonString := openObserveNonStringTypes[strings.ToLower(strings.TrimSpace(fieldType))]
	return !nonString
}

// isOpenObserveIntegerColumn reports whether a column holds an integer, so it can be
// compared numerically rather than as a string.
func isOpenObserveIntegerColumn(fieldType string) bool {
	switch strings.ToLower(strings.TrimSpace(fieldType)) {
	case "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

// fetchOpenObserveStreamFields returns the columns defined on a log stream, keyed by name
// with the Arrow type name as the value. Types matter as much as names: the same logical
// field can arrive as Utf8 from one shipper and Int64 from another (severity_text vs a
// numeric severity), and using the wrong one in a string context fails the whole query.
func fetchOpenObserveStreamFields(url, orgID, username, password, stream, streamType string) (map[string]string, error) {
	endpoint := fmt.Sprintf("%s/api/%s/streams/%s/schema?type=%s",
		url, neturl.PathEscape(orgID), neturl.PathEscape(stream), neturl.QueryEscape(streamType))

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": openObserveAuthHeader(username, password),
			"Content-Type":  "application/json",
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve schema request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		body := strings.TrimSpace(string(bodyBytes))

		// Wrapped so callers can distinguish "no such stream" from a real failure without
		// matching on message text.
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: stream %q (status %d: %s)",
				errOpenObserveStreamNotFound, stream, resp.StatusCode, body)
		}

		return nil, fmt.Errorf("OpenObserve schema request for stream %q failed with status %d: %s",
			stream, resp.StatusCode, body)
	}

	var schema openObserveStreamSchema
	if err := json.NewDecoder(resp.Body).Decode(&schema); err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve schema response: %w", err)
	}

	fields := make(map[string]string, len(schema.Schema))
	for _, f := range schema.Schema {
		if f.Name != "" {
			fields[f.Name] = f.Type
		}
	}
	return fields, nil
}

// pickOpenObserveColumn returns the first candidate present in the stream schema whose
// type can be used in a string context. A candidate that exists but is numeric is skipped
// rather than accepted, so resolution falls through to the next spelling.
func pickOpenObserveColumn(fields map[string]string, candidates []string) string {
	for _, c := range candidates {
		if fieldType, ok := fields[c]; ok && isOpenObserveStringColumn(fieldType) {
			return c
		}
	}
	return ""
}

// pickOpenObserveIntegerColumn returns the first candidate present with an integer type.
func pickOpenObserveIntegerColumn(fields map[string]string, candidates []string) string {
	for _, c := range candidates {
		if fieldType, ok := fields[c]; ok && isOpenObserveIntegerColumn(fieldType) {
			return c
		}
	}
	return ""
}

// resolveOpenObserveLogGroupCols maps each logical role onto a real stream column.
func resolveOpenObserveLogGroupCols(fields map[string]string) openObserveLogGroupCols {
	return openObserveLogGroupCols{
		Message:        pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Message),
		Severity:       pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Severity),
		SeverityNumber: pickOpenObserveIntegerColumn(fields, openObserveLogGroupCandidates.SeverityNumber),
		Namespace:      pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Namespace),
		Pod:            pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Pod),
		Container:      pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Container),
		Workload:       pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Workload),
	}
}

// openObserveQuoteLiteralList renders a SQL list of escaped string literals.
func openObserveQuoteLiteralList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + escapeOpenObserveString(v) + "'"
	}
	return strings.Join(quoted, ", ")
}

// buildOpenObserveLogGroupSQL emits a GROUP BY query that pushes the aggregation down to
// OpenObserve. Grouping on the exact message matches the other providers: generatePatternHash
// is a SHA1 of the raw message bytes, so client-side grouping would have no fidelity
// advantage while costing a full raw-row fetch. MAX(_timestamp) gives the UI a per-group
// "Last Time"; without it every row renders the same timestamp.
func buildOpenObserveLogGroupSQL(stream string, cols openObserveLogGroupCols, selectedNamespace, selectedWorkload string, limit int) string {
	if limit <= 0 {
		limit = openObserveLogGroupLimit
	}

	// Aliases are prefixed so they can never collide with a SQL reserved word.
	selectCols := []string{fmt.Sprintf("%s AS group_sample", cols.Message)}
	groupCols := []string{cols.Message}

	appendCol := func(col, alias string) {
		if col == "" {
			return
		}
		selectCols = append(selectCols, fmt.Sprintf("%s AS %s", col, alias))
		groupCols = append(groupCols, col)
	}
	appendCol(cols.Namespace, "group_namespace")
	appendCol(cols.Pod, "group_pod")
	appendCol(cols.Workload, "group_workload")
	appendCol(cols.Container, "group_container")
	appendCol(cols.Severity, "group_level")
	appendCol(cols.SeverityNumber, "group_level_num")

	selectCols = append(selectCols,
		"count(*) AS group_count",
		"max(_timestamp) AS group_last_ts",
	)

	// Message-content fallback for records that carry no usable severity at all.
	messageMatches := make([]string, 0, len(openObserveErrorMessagePatterns))
	for _, p := range openObserveErrorMessagePatterns {
		messageMatches = append(messageMatches,
			fmt.Sprintf("%s LIKE '%%%s%%'", cols.Message, escapeOpenObserveString(p)))
	}
	messageErrorFilter := "(" + strings.Join(messageMatches, " OR ") + ")"

	// What identifies an error when the string severity is absent or blank: a numeric OTel
	// severity if the stream has one, otherwise message content.
	fallbackErrorFilter := messageErrorFilter
	if cols.SeverityNumber != "" {
		fallbackErrorFilter = fmt.Sprintf("%s >= %d", cols.SeverityNumber, openObserveOtelErrorSeverityNumber)
	}

	conditions := []string{
		fmt.Sprintf("%s IS NOT NULL AND %s != ''", cols.Message, cols.Message),
	}

	if cols.Severity != "" {
		// LOWER() is only ever applied to a column the schema reported as string-typed —
		// DataFusion rejects the whole query with a type_coercion error otherwise.
		conditions = append(conditions, fmt.Sprintf("(LOWER(%s) IN (%s) OR ((%s IS NULL OR %s = '') AND %s))",
			cols.Severity, openObserveQuoteLiteralList(openObserveErrorSeverities),
			cols.Severity, cols.Severity, fallbackErrorFilter))
	} else {
		conditions = append(conditions, fallbackErrorFilter)
	}

	if cols.Container != "" {
		conditions = append(conditions, fmt.Sprintf("%s NOT IN (%s)",
			cols.Container, openObserveQuoteLiteralList(openObserveExcludedContainers)))
	}

	if selectedNamespace != "" && cols.Namespace != "" {
		conditions = append(conditions, fmt.Sprintf("%s = '%s'",
			cols.Namespace, escapeOpenObserveString(selectedNamespace)))
	}

	if selectedWorkload != "" {
		// Pods are named {workload}-{suffix}; fall back to the container name when the
		// stream carries no pod column.
		escaped := escapeOpenObserveString(selectedWorkload)
		switch {
		case cols.Pod != "":
			conditions = append(conditions, fmt.Sprintf("%s LIKE '%s-%%'", cols.Pod, escaped))
		case cols.Container != "":
			conditions = append(conditions, fmt.Sprintf("%s LIKE '%s%%'", cols.Container, escaped))
		}
	}

	return fmt.Sprintf(
		`SELECT %s FROM "%s" WHERE %s GROUP BY %s ORDER BY group_count DESC LIMIT %d`,
		strings.Join(selectCols, ", "),
		stream,
		strings.Join(conditions, " AND "),
		strings.Join(groupCols, ", "),
		limit,
	)
}

// openObserveTimeRangeMicros normalizes the request window (milliseconds) to the
// microseconds OpenObserve's search API expects, defaulting to the last hour.
func openObserveTimeRangeMicros(startMs, endMs int64, now time.Time) (int64, int64) {
	if endMs <= 0 {
		endMs = now.UnixMilli()
	}
	if startMs <= 0 {
		startMs = now.Add(-time.Hour).UnixMilli()
	}
	return startMs * 1000, endMs * 1000
}

// openObserveNumber coerces a decoded JSON cell to float64.
func openObserveNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

// openObserveLevelFromSeverityNumber maps an OTel severity number onto a level name.
// Rows are already filtered to errors, so anything unrecognised reads as "error".
func openObserveLevelFromSeverityNumber(v any) string {
	if n, ok := openObserveNumber(v); ok && n >= openObserveOtelFatalSeverityNumber {
		return "fatal"
	}
	return "error"
}

// openObserveK8sNameAlphabet is the alphabet Kubernetes draws generated name parts from
// (k8s.io/apimachinery/pkg/util/rand): no vowels and no 0, 1 or 3, so a generated part
// never spells a word. Both the pod-template hash and the random suffix use it, and
// requiring it is what keeps a real name segment — the "nightly" of a Job named
// db-backup-nightly — from being stripped as if it were generated.
const openObserveK8sNameAlphabet = "bcdfghjklmnpqrstvwxz2456789"

// openObserveWorkloadFromPod names the workload that owns a pod the shipper recorded no
// workload for. On an OTel collector stream that is every pod not owned by a Deployment:
// the Job pod runbook-script-<uuid>-x9gvn used to be reported under its full pod name,
// making every run its own "application". Recognised shapes:
//
//	Deployment   <name>-<pod-template-hash>-<suffix>
//	CronJob      <name>-<scheduled minute>-<suffix>
//	Job          <name>-<suffix>   (DaemonSet pods look the same)
//	StatefulSet  <name>-<ordinal>
//
// Anything else, a bare pod included, is its own workload. When a controller's name ends
// in a UUID, that UUID is dropped: it is a per-run id, and leaving it in splits one
// application into a row per run. A bare pod keeps its UUID — it is not per-run:
// llm-server names each account's long-lived code-agent pod workspace-<account id>, and
// stripping would merge every account's workspace into one row.
func openObserveWorkloadFromPod(pod string) string {
	owner := openObserveOwnerFromPod(pod)
	if owner == pod {
		return pod
	}
	return stripOpenObserveRunID(owner)
}

func openObserveOwnerFromPod(pod string) string {
	parts := strings.Split(pod, "-")
	n := len(parts)
	switch {
	case n >= 3 && isGeneratedK8sName(parts[n-1], 5, 5) &&
		(isGeneratedK8sName(parts[n-2], 5, 10) || isCronJobScheduledMinute(parts[n-2])):
		return strings.Join(parts[:n-2], "-")
	case n >= 2 && (isGeneratedK8sName(parts[n-1], 5, 5) || isStatefulSetOrdinal(parts[n-1])):
		return strings.Join(parts[:n-1], "-")
	}
	return pod
}

// openObserveRunIDSuffix matches a trailing -<uuid>. Workloads created once per run name
// themselves after the run — the runbook runner creates a Job runbook-script-<execution id>
// for every execution, with no controller above it — and OpenObserve records nothing else
// (no job name, no labels) that would group those runs. The drill-down still finds each
// run's pods: the workload filter falls back to a <name>-% match on the pod name.
var openObserveRunIDSuffix = regexp.MustCompile(`-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// stripOpenObserveRunID drops a trailing run id, unless the name is nothing but one.
func stripOpenObserveRunID(name string) string {
	if loc := openObserveRunIDSuffix.FindStringIndex(name); loc != nil && loc[0] > 0 {
		return name[:loc[0]]
	}
	return name
}

// isGeneratedK8sName reports whether s is a generated name part of the given length range.
func isGeneratedK8sName(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(openObserveK8sNameAlphabet, r) {
			return false
		}
	}
	return true
}

// isCronJobScheduledMinute reports whether s is the scheduled time, in minutes since the
// epoch, that the CronJob controller puts in a Job's name — eight digits until 2160.
func isCronJobScheduledMinute(s string) bool {
	return len(s) >= 8 && len(s) <= 10 && isAllDigits(s)
}

// isStatefulSetOrdinal reports whether s is a StatefulSet pod ordinal. Capped at five
// digits so an all-digit tail on some other name is not taken for one.
func isStatefulSetOrdinal(s string) bool {
	return s != "" && len(s) <= 5 && isAllDigits(s)
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// convertOpenObserveLogGroups maps aggregated rows onto the LogGroup contract shared by
// every provider. Timestamps are emitted in epoch seconds — the frontend multiplies by 1000.
func convertOpenObserveLogGroups(hits []map[string]any, fallbackTimestampSec int64) LogGroupOutput {
	groups := make([]LogGroup, 0, len(hits))

	for _, hit := range hits {
		count, ok := openObserveNumber(hit["group_count"])
		if !ok {
			continue
		}

		sample, _ := hit["group_sample"].(string)
		if sample == "" {
			continue
		}

		namespace, _ := hit["group_namespace"].(string)
		pod, _ := hit["group_pod"].(string)
		container, _ := hit["group_container"].(string)
		level, _ := hit["group_level"].(string)
		if level == "" {
			// Fall back to the numeric OTel severity when the stream has no string one,
			// so FATAL is not flattened into ERROR.
			level = openObserveLevelFromSeverityNumber(hit["group_level_num"])
		}

		// _timestamp is microseconds; the LogGroup contract is epoch seconds.
		timestampSec := fallbackTimestampSec
		if lastTs, ok := openObserveNumber(hit["group_last_ts"]); ok && lastTs > 0 {
			timestampSec = int64(lastTs) / 1_000_000
		}

		// Prefer the workload the shipper recorded; fall back to deriving it from the pod
		// name for streams (or pods) that carry no workload column — e.g. a StatefulSet on
		// a stream that only records k8s_deployment_name.
		workload, _ := hit["group_workload"].(string)
		if workload == "" {
			workload = openObserveWorkloadFromPod(pod)
		}

		group := LogGroup{
			Sample:      sample,
			Namespace:   namespace,
			Workload:    workload,
			Container:   container,
			Level:       level,
			Count:       int64(count),
			Timestamps:  []int64{timestampSec},
			Values:      []float64{count},
			PatternHash: generatePatternHash(sample),
		}

		// /k8s/<namespace>/<workload>, the shape Loki, Elasticsearch, Datadog and the other
		// log-group providers emit. Not Prometheus's /k8s/<namespace>/<pod>/<container>: the
		// UI reads a four-segment id as that format and trims the pod suffix off the third
		// segment, which here is already the workload — so "nudgebee-doc-docs" displayed,
		// and was drilled down into, as "nudgebee-doc".
		if group.Namespace != "" && group.Workload != "" {
			group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
		}

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}
}

// QueryLogGroup fetches aggregated error-log patterns from OpenObserve, making
// OpenObserveLogSource satisfy LogGroupSource so log grouping is routed through the log
func (s *OpenObserveLogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("OpenObserveLogSource.QueryLogGroup: failed to get configs", "error", err)
		return LogGroupOutput{}, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	// The stream schema decides which columns the aggregation can reference; guessing
	// would make OpenObserve reject the whole query with a column-not-found error.
	fields, err := fetchOpenObserveStreamFields(cfg.URL, cfg.OrgID, cfg.Username, cfg.Password, cfg.LogStream, "logs")
	if err != nil {
		// A stream OpenObserve has not materialised yet reports no schema. That means
		// there is nothing to group, not that grouping is broken — and it happens
		// routinely on an account whose log stream has simply been idle.
		if errors.Is(err, errOpenObserveStreamNotFound) || isOpenObserveEmptyResultError(err.Error()) {
			return LogGroupOutput{}, nil
		}
		ctx.GetLogger().Error("OpenObserveLogSource.QueryLogGroup: failed to read stream schema", "error", err)
		return LogGroupOutput{}, err
	}

	cols := resolveOpenObserveLogGroupCols(fields)
	if cols.Message == "" {
		return LogGroupOutput{}, fmt.Errorf(
			"OpenObserve stream %q has no log message column — expected one of: %s",
			cfg.LogStream, strings.Join(openObserveLogGroupCandidates.Message, ", "))
	}

	sql := buildOpenObserveLogGroupSQL(
		cfg.LogStream,
		cols,
		common.GetString(req.Request, "selectedNamespace"),
		common.GetString(req.Request, "selectedWorkload"),
		openObserveLogGroupLimit,
	)

	startTimeMicros, endTimeMicros := openObserveTimeRangeMicros(req.StartTime, req.EndTime, time.Now())

	ctx.GetLogger().Info("OpenObserve Log Group Query", "query", sql)

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to marshal log group request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", cfg.URL, cfg.OrgID)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": openObserveAuthHeader(cfg.Username, cfg.Password),
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(60*time.Second),
	)
	if err != nil {
		ctx.GetLogger().Error("OpenObserveLogSource.QueryLogGroup: search request failed", "query", sql, "error", err)
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return LogGroupOutput{}, fmt.Errorf(
				"log group query timed out — the selected time range contains too many logs. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return LogGroupOutput{}, fmt.Errorf("OpenObserve log group request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		text := strings.TrimSpace(string(bodyBytes))
		// Same empty-window case as QueryLogs: no groups, rather than a failure.
		if isOpenObserveEmptyResultError(text) {
			return LogGroupOutput{}, nil
		}
		return LogGroupOutput{}, fmt.Errorf("OpenObserve log group query failed with status %d: %s",
			resp.StatusCode, text)
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	// Groups with no MAX(_timestamp) fall back to the end of the query window.
	return convertOpenObserveLogGroups(searchResp.Hits, endTimeMicros/1_000_000), nil
}
