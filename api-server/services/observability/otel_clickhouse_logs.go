package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/observability/logql2ch"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

// OtelClickhouseLogSource reads logs from the logs table in the agent's ClickHouse
// (the one the agent reports checking, otel_logs by default), through the relay's
// query_data action (as the trace source does).
//
// LogQL — Code mode, the workflow builder — is translated by logql2ch. A
// canonical where-clause — Builder mode, nubi, event rules — is compiled by
// logql2ch.WhereCond directly, never rendered as LogQL first, so `_in`, a
// cross-field `_or` and `_not` keep their meaning. When a request carries both
// (FetchLogs ANDs the account's default filters into the where-clause), both
// apply.
type OtelClickhouseLogSource struct{}

const (
	otelLogsDefaultTable  = "otel_logs"
	otelLogsDefaultWindow = time.Hour
	otelLogsMaxWindow     = 30 * 24 * time.Hour
	otelLogsGroupLimit    = 1000
	// otelLogsTimeout bounds a query whose caller set no timeout or deadline
	// (label values, log groups); otelLogsProbeTimeout bounds the schema probe.
	otelLogsTimeout      = 30 * time.Second
	otelLogsProbeTimeout = 5 * time.Second
)

// otelClickhouseLogLabelMapping is Loki's (canonical names are Loki names,
// content/message become the log line) plus the canonical names whose Loki
// spelling differs from the translator's labels.
var otelClickhouseLogLabelMapping = func() map[string]string {
	m := maps.Clone(LogsFilterMap)
	m["node"] = "node_name"
	m["service"] = "service_name"
	m["severity"] = "level"
	return m
}()

func (s *OtelClickhouseLogSource) ProviderRef() providerRef {
	return providerRef{Provider: "otel_clickhouse", Source: "agent"}
}

func (s *OtelClickhouseLogSource) GetLabelMapping() map[string]string {
	return maps.Clone(otelClickhouseLogLabelMapping)
}

// RefusedLogFields withholds the canonical fields the translator refuses, so no
// client is told it can filter on them. otel_logs has no cluster label: the
// table is per cluster.
func (s *OtelClickhouseLogSource) RefusedLogFields() []string { return []string{"cluster"} }

func (s *OtelClickhouseLogSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_contains", "_like", "_ilike", "_nlike", "_icontains", "_nicontains", "_regex", "_nregex"}
}

// GetQuery returns the where-clause as LogQL for display (Builder → Code), but
// only when that LogQL means exactly what QueryLogs runs. Otherwise it returns
// "", so FetchLogs reports the where-clause JSON as the executed query instead
// of a LogQL string that would select different lines.
//
// QueryLogs recognises its own display by string equality and then runs the
// where-clause alone, so the display must be the same on every call.
func (s *OtelClickhouseLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	if !lokiRendersExactly(req.QueryRequest.Where) {
		return "", nil
	}
	// A where-clause BuildLokiQuery cannot render (a regex it judges unsafe)
	// still runs, compiled by WhereCond; it just has no LogQL form.
	q, err := (&LokiSource{}).BuildLokiQuery(req.QueryRequest)
	if err != nil {
		return "", nil
	}
	q = strings.TrimSpace(q)
	// The values hold no backslash (lokiRendersExactly), so one here is
	// BuildLokiQuery's regex escaping (\.) or a regex written into "..."
	// unescaped: a bad string to LogQL, or a different one (\t is a tab).
	if strings.Contains(q, `\`) {
		return "", nil
	}
	// The display is also what Code mode runs when the user switches to it.
	if _, err := logql2ch.Translate(q, logql2ch.Options{Start: time.Unix(0, 0), End: time.Unix(1, 0)}); err != nil {
		return "", nil
	}
	return q, nil
}

// lokiRendersExactly reports whether BuildLokiQuery renders w as LogQL with
// the same meaning, and the same LogQL each time: label equality, sets and
// regexes with at least one positive matcher, line filters, and ANDs. It
// renders _or across fields as AND, _not as invalid LogQL, trace_id as a line
// filter (otel logs carry it in TraceId), and prepends namespace=~".+" when no
// label is positive.
func lokiRendersExactly(w query.QueryWhereClause) bool {
	positive := map[string]int{}
	var walk func(query.QueryWhereClause) bool
	walk = func(c query.QueryWhereClause) bool {
		if len(c.Or) > 0 || c.Not != nil {
			return false
		}
		for field, ops := range c.Binary {
			for _, v := range ops {
				if !plainValue(v) {
					return false
				}
			}
			if field == "log" {
				// BuildLokiQuery walks a field's operators in map order, so two
				// line filters in one clause come out in either order.
				if len(ops) > 1 {
					return false
				}
				for op := range ops {
					switch op {
					case query.Eq, query.Contains, query.Nq, query.Regex, query.NRegex, query.IContains, query.NIContains:
					default:
						return false
					}
				}
				continue
			}
			// Any other field becomes a stream label, which is right only for
			// the translator's labels: not trace_id, timestamp, or the body's
			// other names (body, line), which WhereCond reads as the body.
			if _, ok := logql2ch.LabelExpr(field, logql2ch.SchemaColumns); !ok {
				return false
			}
			// _not_in and _nregex both render as !~ and merge in map order.
			_, notIn := ops[query.NotIn]
			_, nRegex := ops[query.NRegex]
			if notIn && nRegex {
				return false
			}
			for op, v := range ops {
				switch op {
				case query.Eq, query.In, query.Regex:
					positive[field]++
				case query.Nq, query.NotIn, query.NRegex:
				default:
					return false
				}
				// An empty set renders as =~"", which matches an empty label.
				if (op == query.In || op == query.NotIn) && emptyList(v) {
					return false
				}
			}
		}
		for _, sub := range c.And {
			if !walk(sub) {
				return false
			}
		}
		return true
	}
	if !walk(w) || len(positive) == 0 {
		return false
	}
	// Two positive matchers on one label merge into one alternation: OR, where
	// the where-clause ANDs them. Negative ones merge into a negated
	// alternation, which is still AND.
	for _, n := range positive {
		if n > 1 {
			return false
		}
	}
	return true
}

func emptyList(v any) bool {
	switch x := v.(type) {
	case []string:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// plainValue reports whether a where-clause value holds no quote or backslash.
// BuildLokiQuery does not escape them the same way for every operator: a quote
// in an _in value ends the string, and what follows reads as more matchers.
func plainValue(v any) bool {
	var vals []string
	switch x := v.(type) {
	case []string:
		vals = x
	case []any:
		for _, e := range x {
			vals = append(vals, fmt.Sprint(e))
		}
	default:
		vals = []string{fmt.Sprint(v)}
	}
	for _, s := range vals {
		if strings.ContainsAny(s, `"\`) {
			return false
		}
	}
	return true
}

func (s *OtelClickhouseLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	return s.queryLogs(ctx, req)
}

// queryLogs is QueryLogs with extra conditions ANDed into the query, for callers that
// need SQL the where-clause cannot say.
func (s *OtelClickhouseLogSource) queryLogs(ctx *security.RequestContext, req FetchLogRequest, extra ...logql2ch.Cond) ([]OutputLog, error) {
	if req.AccountId == "" {
		return nil, fmt.Errorf("otel_clickhouse: account ID is required")
	}
	logql, params := splitLokiParams(req.Query)
	start, end, err := otelLogsWindow(req.StartTime, req.EndTime, params)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if v := params.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil {
			return nil, fmt.Errorf("otel_clickhouse: bad limit %q", v)
		}
	}
	step := time.Duration(req.StepInterval) * time.Second
	if v := params.Get("step"); v != "" {
		if step, err = parseLokiStep(v); err != nil {
			return nil, err
		}
	}
	table := otelLogsTableFor(req.AccountId)
	schema := otelLogsSchema(ctx, req.AccountId, table)
	opts := logql2ch.Options{
		Table: table, Schema: schema, Start: start, End: end,
		Limit: limit, Forward: params.Get("direction") == "forward", Step: step,
	}
	displayed := ""
	if hasWhereData(req.QueryRequest.Where) {
		cond, err := logql2ch.WhereCond(req.QueryRequest.Where, schema)
		if err != nil {
			return nil, fmt.Errorf("otel_clickhouse: %w", err)
		}
		opts.Filters = []logql2ch.Cond{cond}
		displayed, _ = s.GetQuery(ctx, req)
	}
	opts.Filters = append(opts.Filters, extra...)

	var q logql2ch.Query
	switch {
	case strings.TrimSpace(logql) == "" || logql == displayed:
		// Builder mode: the LogQL, if any, is only GetQuery's display form of
		// the where-clause.
		if len(opts.Filters) == 0 {
			return nil, fmt.Errorf("otel_clickhouse: a log query needs LogQL or a filter")
		}
		q, err = logql2ch.Logs(opts)
	default:
		q, err = logql2ch.Translate(logql, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("otel_clickhouse: %w", err)
	}

	raw, err := s.run(ctx, req.AccountId, q.SQL, req.Timeout)
	if err != nil {
		return nil, err
	}
	if q.Kind == logql2ch.KindMetric {
		// The SQL asks for one row past the cap, so more rows means it was cut.
		if len(raw.Rows) > logql2ch.MaxMetricRows {
			return nil, fmt.Errorf("otel_clickhouse: the query returns more than %d points; use fewer series (a narrower sum by) or a larger range", logql2ch.MaxMetricRows)
		}
		return otelLogsMetricRows(raw, q.Labels)
	}
	return otelLogsRows(raw)
}

func (s *OtelClickhouseLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	labels := logql2ch.Labels()
	out := make([]OutputLogLabel, len(labels))
	for i, l := range labels {
		out[i] = OutputLogLabel{Label: l, Attributes: map[string]any{}}
	}
	return out, nil
}

func (s *OtelClickhouseLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	if req.AccountId == "" {
		return nil, fmt.Errorf("otel_clickhouse: account ID is required")
	}
	var params url.Values
	if q, ok := req.Request["query"].(string); ok {
		params, _ = url.ParseQuery(q)
	}
	start, end, err := otelLogsWindow(req.StartTime, req.EndTime, params)
	if err != nil {
		return nil, err
	}
	table := otelLogsTableFor(req.AccountId)
	schema := otelLogsSchema(ctx, req.AccountId, table)
	opts := logql2ch.Options{Table: table, Schema: schema, Start: start, End: end}
	// A picker narrowed by the chips already set; a chip on a field the
	// translator does not know only widens the list, so it is skipped.
	for label, value := range req.CurrentFilters {
		w := query.QueryWhereClause{Binary: query.BinaryWhereClause{s.labelName(label): {query.Eq: value}}}
		if cond, err := logql2ch.WhereCond(w, schema); err == nil {
			opts.Filters = append(opts.Filters, cond)
		}
	}
	sql, err := logql2ch.LabelValuesSQL(s.labelName(req.LabelName), opts)
	if err != nil {
		return nil, fmt.Errorf("otel_clickhouse: %w", err)
	}
	raw, err := s.run(ctx, req.AccountId, sql, 0)
	if err != nil {
		return nil, err
	}
	out := make([]OutputLogLabelValue, 0, len(raw.Rows))
	for _, r := range raw.Rows {
		if len(r) > 0 {
			out = append(out, OutputLogLabelValue{Value: fmt.Sprint(r[0]), Attributes: map[string]any{}})
		}
	}
	return out, nil
}

// labelName maps a canonical name (node, service, severity) onto the
// translator's label; other names pass through.
func (s *OtelClickhouseLogSource) labelName(name string) string {
	if mapped, ok := otelClickhouseLogLabelMapping[name]; ok && mapped != "log" {
		return mapped
	}
	return name
}

// QueryLogGroup fetches the window's likely error lines and groups them by
// pattern, as LokiSource does. A line qualifies by the record's severity, or, in a
// line without one, by whole error words outside fields that report no error
// (logql2ch.ErrorLines). A looser text match caught info records ("Preferred") and
// stdout lines carrying err=<nil>, which filled the fetch limit and left no room for
// the errors that grouping keeps.
func (s *OtelClickhouseLogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	where := query.QueryWhereClause{Binary: query.BinaryWhereClause{}}
	if ns := common.GetString(req.Request, "selectedNamespace"); ns != "" {
		where.Binary["namespace"] = map[query.BinaryWhereClauseType]any{query.Eq: ns}
	}
	if wl := common.GetString(req.Request, "selectedWorkload"); wl != "" {
		where.Binary["app"] = map[query.BinaryWhereClauseType]any{query.Eq: wl}
	}
	logs, err := s.queryLogs(ctx, FetchLogRequest{
		AccountId: req.AccountId, StartTime: req.StartTime, EndTime: req.EndTime,
		Limit: otelLogsGroupLimit, QueryRequest: LogsQueryBuilderRequest{Where: where},
	}, logql2ch.ErrorLines())
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("otel_clickhouse.QueryLogGroup: %w", err)
	}
	return (&LokiSource{}).groupLogsByPattern(logs, req.EndTime), nil
}

// withLineFilter narrows a LogQL log query to lines matching pattern (event
// cards ask for the error lines). The where-clause JSON FetchLogs reports for
// a Builder query is not LogQL, so it declines that.
func (s *OtelClickhouseLogSource) withLineFilter(q, pattern string) (string, bool) {
	q = strings.TrimSpace(q)
	if !strings.HasPrefix(q, "{") || json.Valid([]byte(q)) || strings.Contains(pattern, "`") {
		return "", false
	}
	return q + " |~ `" + pattern + "`", true
}

func (s *OtelClickhouseLogSource) run(ctx *security.RequestContext, accountId, sql string, timeout time.Duration) (RawTraceResult, error) {
	runCtx := ctx.GetContext()
	if runCtx == nil {
		runCtx = context.Background()
	}
	if _, hasDeadline := runCtx.Deadline(); !hasDeadline && timeout <= 0 {
		timeout = otelLogsTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, timeout)
		defer cancel()
	}
	raw, err := execWithLimit(runCtx, sql, accountId)
	if err != nil {
		return RawTraceResult{}, otelLogsError(err, otelLogsTableFor(accountId))
	}
	return raw, nil
}

// execWithLimit runs sql with withExecutionLimit's clauses. A readonly=1 ClickHouse user
// may not change settings, so ClickHouse refuses the SETTINGS clause (Code: 164,
// READONLY); the statement then runs once more without it and, like trace queries,
// stops only when our wait does.
func execWithLimit(ctx context.Context, sql, accountId string) (RawTraceResult, error) {
	raw, err := otelLogsExec(ctx, withExecutionLimit(ctx, sql), accountId)
	if err != nil && readonlySettings.MatchString(err.Error()) {
		return otelLogsExec(ctx, sql+otelLogsFormat, accountId)
	}
	return raw, err
}

// readonlySettings is ClickHouse's refusal of a setting to a readonly=1 user.
var readonlySettings = regexp.MustCompile(`\(READONLY\)|(?i:\bcode: 164\b)`)

// otelLogsFormat is the output format the agent decodes. The agent appends it only to a
// statement with no "FORMAT " anywhere in it, so a line filter or label value carrying
// that word (|= "invalid format string") would come back as TabSeparated and fail to
// decode; every statement names it itself. Only agents that report clickhouseLogsCollected
// get here, which the Go agent alone sends: the Python agent's client appends its own
// FORMAT, and a second one is a syntax error.
const otelLogsFormat = " FORMAT JSONCompact"

// withExecutionLimit ends sql with a SETTINGS clause that has ClickHouse stop the query
// itself at ctx's deadline, then the output format. The deadline only ends our wait:
// neither the relay nor the agent cancels the query in ClickHouse, so without the limit
// a costly scan (an LLM's `|~` over 30 days) keeps running in the customer's cluster
// after we gave up on it.
func withExecutionLimit(ctx context.Context, sql string) string {
	secs := int(otelLogsTimeout / time.Second)
	if deadline, ok := ctx.Deadline(); ok {
		secs = int(math.Ceil(time.Until(deadline).Seconds()))
	}
	return fmt.Sprintf("%s SETTINGS max_execution_time = %d%s", sql, max(secs, 1), otelLogsFormat)
}

// otelLogsExec runs one statement on the agent's ClickHouse; a var so tests
// can answer without a relay.
var otelLogsExec = func(ctx context.Context, sql, accountId string) (RawTraceResult, error) {
	return OtelClickhouseTraceSource{}.executeClickhouseQueryRaw(ctx, sql, accountId)
}

// otelLogsAgentTable is the logs table the account's agent reports checking
// (clickhouseLogsTable, "database.table"), or "" when it reports none; a var so
// tests can answer without an agent row.
var otelLogsAgentTable = func(accountId string) string {
	details, err := account.GetAgentConnectionDetails(accountId)
	if err != nil || details.Features.ClickhouseLogsTable == nil {
		return ""
	}
	return *details.Features.ClickhouseLogsTable
}

// otelLogsTableFor is the table to read the account's logs from: the agent's, when
// it is a plain [database.]table name, otherwise otel_logs. The name goes into SQL
// unquoted, so anything else is refused here.
func otelLogsTableFor(accountId string) string {
	if t := otelLogsAgentTable(accountId); logql2ch.ValidTableName(t) {
		return t
	}
	return otelLogsDefaultTable
}

// unknownTable is ClickHouse's missing-table error, as the HTTP interface
// (Code: 60. DB::Exception ... (UNKNOWN_TABLE)) or clickhouse-go (code: 60,
// message: ...) prints it.
var unknownTable = regexp.MustCompile(`UNKNOWN_TABLE|(?i:\bcode: 60\b)`)

// otelLogsError turns ClickHouse's missing-table error into the reason: the
// account resolves to ClickHouse logs, but the cluster does not collect them.
func otelLogsError(err error, table string) error {
	if unknownTable.MatchString(err.Error()) {
		return fmt.Errorf("otel_clickhouse: logs collection is not enabled on this cluster (its ClickHouse has no %s table): %w", table, err)
	}
	return fmt.Errorf("otel_clickhouse: %w", err)
}

// splitLokiParams accepts what LokiSource accepts: raw LogQL, or Loki API
// parameters (query=…&start=…&end=…) as the UI's Code mode may send.
func splitLokiParams(q string) (string, url.Values) {
	q = strings.TrimSpace(q)
	if q == "" || strings.HasPrefix(q, "{") {
		return q, url.Values{}
	}
	params, err := url.ParseQuery(q)
	if err != nil || params.Get("query") == "" {
		return q, url.Values{}
	}
	return strings.TrimSpace(params.Get("query")), params
}

// otelLogsWindow resolves the window: Loki-style start/end parameters win,
// then the request's (unix milliseconds); the default is the last hour, and a
// window longer than 30 days is refused.
func otelLogsWindow(startMs, endMs int64, params url.Values) (time.Time, time.Time, error) {
	end := time.Now()
	if endMs > 0 {
		end = time.UnixMilli(endMs)
	}
	start := end.Add(-otelLogsDefaultWindow)
	if startMs > 0 {
		start = time.UnixMilli(startMs)
	}
	for key, t := range map[string]*time.Time{"start": &start, "end": &end} {
		if v := params.Get(key); v != "" {
			parsed, err := parseLokiTime(v)
			if err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("otel_clickhouse: bad %s %q", key, v)
			}
			*t = parsed
		}
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("otel_clickhouse: the window must start before it ends")
	}
	if end.Sub(start) > otelLogsMaxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("otel_clickhouse: the window is longer than %d days", int(otelLogsMaxWindow.Hours()/24))
	}
	return start, end, nil
}

// parseLokiTime reads a time as Loki's query_range does: float seconds (with a
// dot), integer seconds (10 digits or fewer) or nanoseconds, or RFC3339. Float
// seconds are rounded to the millisecond, as Loki's parser rounds them, so the
// window matches the one Loki would use.
func parseLokiTime(v string) (time.Time, error) {
	if strings.Contains(v, ".") {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			sec, frac := math.Modf(f)
			return time.Unix(int64(sec), int64(math.Round(frac*1000)/1000*float64(time.Second))), nil
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Parse(time.RFC3339Nano, v)
	}
	if len(v) <= 10 {
		return time.Unix(n, 0), nil
	}
	return time.Unix(0, n), nil
}

// parseLokiStep reads Loki's step parameter: seconds, or a duration (5m).
func parseLokiStep(v string) (time.Duration, error) {
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("otel_clickhouse: bad step %q", v)
	}
	return d, nil
}

// otelLogsSchema says whether the account's logs table has the runner's label
// columns. The check is one system.columns read, cached per account and table;
// when it fails the expression schema is used, which reads the same values from
// the attribute maps on any otel_logs-shaped table, only more slowly.
func otelLogsSchema(ctx *security.RequestContext, accountId, table string) logql2ch.Schema {
	key := accountId + "|" + table
	otelLogsSchemas.Lock()
	e, ok := otelLogsSchemas.m[key]
	otelLogsSchemas.Unlock()
	if ok && time.Now().Before(e.until) {
		return e.schema
	}
	schema, ttl := logql2ch.SchemaExpressions, time.Minute
	runCtx := ctx.GetContext()
	if runCtx == nil {
		runCtx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(runCtx, otelLogsProbeTimeout)
	defer cancel()
	if raw, err := execWithLimit(probeCtx, logql2ch.SchemaProbeSQL(table), accountId); err != nil {
		ctx.GetLogger().Warn("otel_clickhouse: cannot read the logs table's columns; reading labels from the attribute maps",
			"account_id", accountId, "table", table, "error", err)
	} else {
		names := make([]string, 0, len(raw.Rows))
		for _, r := range raw.Rows {
			if len(r) > 0 {
				names = append(names, fmt.Sprint(r[0]))
			}
		}
		schema, ttl = logql2ch.SchemaFromColumns(names), 10*time.Minute
	}
	otelLogsSchemas.Lock()
	otelLogsSchemas.m[key] = otelLogsSchemaEntry{schema: schema, until: time.Now().Add(ttl)}
	otelLogsSchemas.Unlock()
	return schema
}

type otelLogsSchemaEntry struct {
	schema logql2ch.Schema
	until  time.Time
}

var otelLogsSchemas = struct {
	sync.Mutex
	m map[string]otelLogsSchemaEntry
}{m: map[string]otelLogsSchemaEntry{}}

// otelLogsRows maps a log query's rows (ts, Body, l_<label>…, trace_id,
// span_id, severity_number) onto OutputLog.
func otelLogsRows(raw RawTraceResult) ([]OutputLog, error) {
	col := map[string]int{}
	for i, c := range raw.Columns {
		col[c] = i
	}
	tsIdx, okTs := col["ts"]
	bodyIdx, okBody := col["Body"]
	if !okTs || !okBody {
		return nil, fmt.Errorf("otel_clickhouse: unexpected columns %v", raw.Columns)
	}
	sevIdx, hasSev := col["severity_number"]
	out := make([]OutputLog, 0, len(raw.Rows))
	for _, r := range raw.Rows {
		ns, err := strconv.ParseInt(cellString(r, tsIdx), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("otel_clickhouse: bad timestamp %v", cellString(r, tsIdx))
		}
		body := cellString(r, bodyIdx)
		labels := map[string]any{}
		for name, i := range col {
			label, isLabel := strings.CutPrefix(name, "l_")
			if name == "trace_id" || name == "span_id" {
				label, isLabel = name, true
			}
			if v := cellString(r, i); isLabel && v != "" {
				labels[label] = v
			}
		}
		// Without a record severity (0) the level label is the store's guess
		// from error words, which reads err=<nil> as an error; the line is
		// classified as Loki's are instead.
		level := fmt.Sprint(labels["level"])
		if hasSev && cellString(r, sevIdx) == "0" {
			level = ""
		}
		out = append(out, OutputLog{
			Timestamp: time.Unix(0, ns).UTC().Format(time.RFC3339Nano),
			Message:   body,
			Labels:    labels,
			Severity:  otelLogSeverity(level, body),
		})
	}
	return out, nil
}

// otelLogSeverity maps a record's level onto OutputLog's vocabulary; a line
// with no severity is classified from its text, as Loki's lines are.
func otelLogSeverity(level, body string) string {
	switch level {
	case "critical", "error", "warning", "info", "debug":
		return level
	}
	return (&LokiSource{}).extractSeverity(body)
}

// otelLogsMetricRows maps a metric query's rows (series, t, v, lv) onto one
// OutputLog per point, as convertLokiResponse does for a Loki matrix.
func otelLogsMetricRows(raw RawTraceResult, labelNames []string) ([]OutputLog, error) {
	col := map[string]int{}
	for i, c := range raw.Columns {
		col[c] = i
	}
	tIdx, okT := col["t"]
	vIdx, okV := col["v"]
	if !okT || !okV {
		return nil, fmt.Errorf("otel_clickhouse: unexpected columns %v", raw.Columns)
	}
	lvIdx, hasLV := col["lv"]
	out := make([]OutputLog, 0, len(raw.Rows))
	for _, r := range raw.Rows {
		sec, err := strconv.ParseFloat(cellString(r, tIdx), 64)
		if err != nil {
			return nil, fmt.Errorf("otel_clickhouse: bad point time %v", cellString(r, tIdx))
		}
		v, err := strconv.ParseFloat(cellString(r, vIdx), 64)
		if err != nil {
			return nil, fmt.Errorf("otel_clickhouse: bad point value %v", cellString(r, vIdx))
		}
		labels := map[string]any{}
		if hasLV && lvIdx < len(r) {
			if values, ok := r[lvIdx].([]any); ok {
				for i, name := range labelNames {
					// Loki leaves an empty label out of the series.
					if v := cellString(values, i); v != "" {
						labels[name] = v
					}
				}
			}
		}
		// t is always whole seconds: the step is, and the SQL builds t from it.
		out = append(out, OutputLog{
			Timestamp: time.Unix(int64(sec), 0).UTC().Format(time.RFC3339Nano),
			Message:   strconv.FormatFloat(v, 'f', -1, 64),
			Labels:    labels,
			Severity:  "unknown",
		})
	}
	return out, nil
}

func cellString(row []any, i int) string {
	if i >= len(row) || row[i] == nil {
		return ""
	}
	switch v := row[i].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	}
	return fmt.Sprint(row[i])
}
