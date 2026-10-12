package logql2ch

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind is the shape of a translated query's result.
type Kind int

const (
	// KindLogs: one row per line — ts, Body, the l_<label> columns, trace_id,
	// span_id, severity_number.
	KindLogs Kind = iota
	// KindMetric: one row per series point — series, t (unix seconds), v, and,
	// when the series has labels, lv (their values in Query.Labels order).
	KindMetric
)

// MaxLimit is the most log lines one query returns (api-server's Loki cap).
const MaxLimit = 5000

// maxPoints is Loki's per-series point cap for a range query.
const maxPoints = 11000

// MaxMetricRows is the most series points one metric query returns. Its SQL
// asks for one more row, so a caller that gets more than MaxMetricRows knows
// the answer was cut and refuses it instead of returning a partial result.
const MaxMetricRows = 50000

// maxQueryLen and maxRange bound what one query may ask for (maxRange caps
// both the window and a metric range); maxSQLLen is ClickHouse's default
// max_query_size.
const (
	maxQueryLen = 64 << 10
	maxRange    = 30 * 24 * time.Hour
	maxSQLLen   = 256 << 10
)

// Options are the request-level inputs a LogQL string does not carry.
type Options struct {
	Table   string // default "otel_logs"
	Schema  Schema
	Start   time.Time // required
	End     time.Time // required, after Start
	Limit   int       // log queries: 0 means MaxLimit
	Forward bool      // log queries: oldest first
	// Step is a metric query's step. Zero means "the range", which is what our
	// callers mean; any other value must equal the range.
	Step time.Duration
	// Filters are ANDed into the query, e.g. an account's default log filters.
	Filters []Cond
}

// Cond is a SQL condition built by this package (see WhereCond). Callers can
// pass one along but cannot construct one, so no caller text reaches the SQL.
type Cond struct{ sql string }

// Query is a translated statement.
type Query struct {
	Kind Kind
	SQL  string
	// Where is the query's own conditions, ANDed after the time bounds ("1"
	// when there are none). Exposed so tests can compare it with the corpus.
	Where string
	// Series is a metric query's series expression; Labels names the labels it
	// renders, in the order of the lv column. Series renders an empty value as
	// k="" where Loki leaves the label out; the grouping is the same, and a
	// consumer building label sets should read lv and drop empty values.
	Series string
	Labels []string
	// Start, End and Step are the effective window: a metric query's window is
	// snapped to the step grid, as Loki does.
	Start, End time.Time
	Step       time.Duration
}

var tableName = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)?$`)

// Translate turns a LogQL query into one ClickHouse SELECT over otel_logs.
func Translate(logql string, opts Options) (Query, error) {
	if err := normalize(&opts); err != nil {
		return Query{}, err
	}
	if len(logql) > maxQueryLen {
		return Query{}, badRequestf("the query is longer than %d KiB", maxQueryLen>>10)
	}
	e, err := parse(logql)
	if err != nil {
		return Query{}, err
	}
	t := &translator{schema: opts.Schema}
	var q Query
	switch {
	case e.log != nil:
		conds, err := t.logConds(e.log)
		if err != nil {
			return Query{}, err
		}
		q = t.logsQuery(opts, conds)
	case e.rng != nil:
		q, err = t.metricQuery(opts, e.rng, nil, true)
	default:
		q, err = t.metricQuery(opts, e.agg.inner, e.agg.by, false)
	}
	if err != nil {
		return Query{}, err
	}
	return q, checkSQLLen(q.SQL)
}

// Logs builds a log query from Filters alone: the Builder path, where the
// filters come from a canonical where-clause.
func Logs(opts Options) (Query, error) {
	if err := normalize(&opts); err != nil {
		return Query{}, err
	}
	t := &translator{schema: opts.Schema}
	q := t.logsQuery(opts, nil)
	return q, checkSQLLen(q.SQL)
}

// checkSQLLen refuses SQL longer than ClickHouse's default max_query_size.
// A short query can still render long: each json field becomes a multiIf over
// every nested path its name could stand for.
func checkSQLLen(sql string) error {
	if len(sql) > maxSQLLen {
		return badRequestf("the query renders as %d KiB of SQL; the maximum is %d KiB, so use fewer filters", len(sql)>>10, maxSQLLen>>10)
	}
	return nil
}

// LabelValuesSQL lists a label's distinct non-empty values in the window,
// narrowed by opts.Filters, at most 1000 of them.
func LabelValuesSQL(label string, opts Options) (string, error) {
	if err := normalize(&opts); err != nil {
		return "", err
	}
	expr, ok := LabelExpr(label, opts.Schema)
	if !ok {
		return "", unknownLabelError(label)
	}
	where := append([]string{expr + " != ''"}, filterConds(opts.Filters)...)
	sql := fmt.Sprintf("SELECT DISTINCT %s AS v FROM %s WHERE %s AND %s ORDER BY v LIMIT 1000",
		expr, opts.Table, logWindow(opts.Start, opts.End), strings.Join(where, " AND "))
	return sql, checkSQLLen(sql)
}

func normalize(opts *Options) error {
	if opts.Table == "" {
		opts.Table = "otel_logs"
	}
	if !tableName.MatchString(opts.Table) {
		return badRequestf("bad table name %q", opts.Table)
	}
	if opts.Start.IsZero() || opts.End.IsZero() || !opts.End.After(opts.Start) {
		return badRequestf("a query needs a start before its end")
	}
	if opts.End.Sub(opts.Start) > maxRange {
		return badRequestf("the time window is longer than %d days", int(maxRange.Hours()/24))
	}
	switch {
	case opts.Limit == 0:
		opts.Limit = MaxLimit
	case opts.Limit < 0 || opts.Limit > MaxLimit:
		return badRequestf("limit %d is outside 1..%d", opts.Limit, MaxLimit)
	}
	return nil
}

type translator struct {
	schema Schema
	parser string // the parser stage seen so far: "", json or logfmt
	// noJSONErrors is set by a top-level `| __error__=""` after json, which
	// drops the lines json cannot parse.
	noJSONErrors bool
}

func (t *translator) logsQuery(opts Options, conds []string) Query {
	where := whereOf(conds)
	cols := []string{"toString(toUnixTimestamp64Nano(Timestamp)) AS ts", "Body"}
	for _, l := range outputLabels {
		expr, _ := LabelExpr(l, t.schema)
		cols = append(cols, expr+" AS l_"+l)
	}
	// severity_number says whether level came from the record's severity or, when
	// the record has none (0), from the level expression's guess at the text.
	cols = append(cols, "TraceId AS trace_id", "SpanId AS span_id", "SeverityNumber AS severity_number")
	order := "DESC"
	if opts.Forward {
		order = "ASC"
	}
	sql := fmt.Sprintf("SELECT %s\nFROM %s\nWHERE %s\n  AND (%s)%s\nORDER BY Timestamp %s\nLIMIT %d",
		strings.Join(cols, ", "), opts.Table, logWindow(opts.Start, opts.End), where,
		andFilters(opts.Filters), order, opts.Limit)
	return Query{Kind: KindLogs, SQL: sql, Where: where, Start: opts.Start, End: opts.End}
}

// logWindow is Loki's half-open window for log queries, [start, end).
func logWindow(start, end time.Time) string {
	return fmt.Sprintf("Timestamp >= fromUnixTimestamp64Nano(toInt64(%d)) AND Timestamp < fromUnixTimestamp64Nano(toInt64(%d))",
		start.UnixNano(), end.UnixNano())
}

// metricQuery answers count_over_time / rate, bare (bare=true) or under sum
// [by]. Each point t counts the lines in (t-step, t], evaluated at
// t = start + k*step with start and end snapped to the step grid, which is
// what Loki's query_range returns when range == step.
func (t *translator) metricQuery(opts Options, r *rangeAgg, by []string, bare bool) (Query, error) {
	conds, err := t.logConds(&r.log)
	if err != nil {
		return Query{}, err
	}
	step := time.Duration(r.rangeNs)
	if step > maxRange {
		return Query{}, badRequestf("the range [%s] is longer than %d days", r.rangeText, int(maxRange.Hours()/24))
	}
	// Loki fails a metric query whose pipeline errors on a line, rather than
	// counting it; json errors on every line that is not JSON unless
	// `| __error__=""` drops them. logfmt errors cannot be told apart in SQL.
	switch {
	case t.parser == "json" && !t.noJSONErrors:
		return Query{}, unsupported(`a metric query over | json without | __error__="" (Loki fails it on lines that are not JSON)`)
	case t.parser == "logfmt":
		return Query{}, unsupported("a metric query over | logfmt")
	}
	if opts.Step != 0 && opts.Step != step {
		return Query{}, unsupported(fmt.Sprintf("range [%s] must equal the query step (%s)", r.rangeText, formatDuration(opts.Step)))
	}
	if step < time.Second || step%time.Second != 0 {
		return Query{}, badRequestf("the range [%s] must be a whole number of seconds", r.rangeText)
	}
	stepS := int64(step / time.Second)
	startS := floorDiv(opts.Start.Unix(), stepS) * stepS
	endS := -floorDiv(-opts.End.Unix(), stepS) * stepS
	if opts.End.Nanosecond() != 0 && endS == opts.End.Unix() {
		endS += stepS
	}
	// Loki's check, on the requested window.
	if int64(opts.End.Sub(opts.Start)/step) > maxPoints {
		return Query{}, badRequestf("the query would return more than %d points per series, so use a larger range", maxPoints)
	}

	labels := by
	if bare {
		if t.parser != "" {
			return Query{}, unsupported("a metric query over a parser needs sum or sum by")
		}
		labels = streamLabels
	}
	labels = sortedUnique(labels)
	series, lv, err := t.series(labels)
	if err != nil {
		return Query{}, err
	}
	value := "toFloat64(count()) AS v"
	if r.fn == "rate" {
		value = fmt.Sprintf("toFloat64(count()) / %d AS v", stepS)
	}
	if lv != "" {
		value += ", " + lv
	}
	where := whereOf(conds)
	stepNs, startNs, endNs := step.Nanoseconds(), startS*1_000_000_000, endS*1_000_000_000
	sql := fmt.Sprintf(`SELECT %s AS series,
  %d + intDiv(toUnixTimestamp64Nano(Timestamp) - %d + %d - 1, %d) * %d AS t,
  %s
FROM %s
WHERE Timestamp > fromUnixTimestamp64Nano(toInt64(%d)) AND Timestamp <= fromUnixTimestamp64Nano(toInt64(%d))
  AND (%s)%s
GROUP BY series, t
ORDER BY series, t
LIMIT %d`,
		series, startS, startNs, stepNs, stepNs, stepS, value,
		opts.Table, startNs-stepNs, endNs, where, andFilters(opts.Filters), MaxMetricRows+1)
	return Query{Kind: KindMetric, SQL: sql, Where: where, Series: series, Labels: labels,
		Start: time.Unix(startS, 0), End: time.Unix(endS, 0), Step: step}, nil
}

// series renders the Loki label set of a metric series as sorted k="v" pairs,
// and an lv column holding the raw values.
func (t *translator) series(labels []string) (string, string, error) {
	if len(labels) == 0 {
		return "''", "", nil
	}
	var pieces, values []string
	for i, l := range labels {
		if l == "__error__" {
			return "", "", unsupported("grouping by __error__")
		}
		p, _, err := t.resolvePieces(l)
		if err != nil {
			return "", "", err
		}
		sep := `'",`
		if i == 0 {
			sep = "'"
		}
		pieces = append(pieces, sep+l+`="'`)
		pieces = append(pieces, p...)
		values = append(values, "toString("+joinPieces(p)+")")
	}
	pieces = append(pieces, `'"'`)
	return "concat(" + strings.Join(pieces, ", ") + ")", "any([" + strings.Join(values, ", ") + "]) AS lv", nil
}

// logConds translates a selector and its pipeline into SQL conditions.
func (t *translator) logConds(l *logExpr) ([]string, error) {
	if err := checkSelector(l.matchers); err != nil {
		return nil, err
	}
	var conds []string
	for _, m := range l.matchers {
		expr, isLevel, err := t.resolve(m.name)
		if err != nil {
			return nil, err
		}
		c, err := matchCond(expr, isLevel, m.op, m.value, m.pos)
		if err != nil {
			return nil, err
		}
		conds = append(conds, c)
	}
	for _, s := range l.stages {
		switch s := s.(type) {
		case lineFilter:
			cs, err := lineConds(s)
			if err != nil {
				return nil, err
			}
			conds = append(conds, cs...)
		case parserStage:
			if t.parser != "" {
				return nil, unsupported("more than one parser")
			}
			t.parser = s.kind
		case labelFilter:
			cs, err := t.labelConds(s.expr, true)
			if err != nil {
				return nil, err
			}
			conds = append(conds, cs...)
		}
	}
	return conds, nil
}

// checkSelector applies Loki's rule that a selector needs a matcher that does
// not match the empty string, so `{app=~".*"}` cannot scan everything.
func checkSelector(ms []matcher) error {
	for _, m := range ms {
		switch m.op {
		case "=":
			if m.value != "" {
				return nil
			}
		case "=~":
			re, err := regexp.Compile("^(?:" + m.value + ")$")
			if err != nil {
				return invalidf(m.pos, "bad regex %q: %v", m.value, err)
			}
			if !re.MatchString("") {
				return nil
			}
		}
	}
	return invalidf(ms[0].pos, "queries require at least one regexp or equality matcher that does not match the empty string")
}

// resolve maps a label name to SQL. Before a parser only the alias map is
// known; after one, stream labels keep their meaning and any other name is a
// field the parser extracts from Body.
func (t *translator) resolve(name string) (string, bool, error) {
	p, isLevel, err := t.resolvePieces(name)
	if err != nil {
		return "", false, err
	}
	return joinPieces(p), isLevel, nil
}

func (t *translator) resolvePieces(name string) ([]string, bool, error) {
	if t.parser != "" && !isStreamLabel(name) {
		return []string{t.extracted(name)}, false, nil
	}
	p, ok := labelPieces(name, t.schema)
	if !ok {
		return nil, false, unknownLabelError(name)
	}
	return p, name == "level", nil
}

// extracted is a field the json or logfmt parser would add as a label. JSON
// scalars become their text, as Loki renders them; objects, arrays and absent
// keys become the empty string.
//
// Loki names a nested JSON field by joining the path with `_`
// ({"http":{"status":500}} is http_status), so a name with underscores is also
// looked up as each nested path it could stand for, the top-level key first.
// Loki also replaces other characters in a key with `_` ("http.status" is
// http_status too); such keys are not reachable here.
func (t *translator) extracted(name string) string {
	if t.parser == "logfmt" {
		return "extractKeyValuePairs(Body, '=', ' ', '\"')[" + quote(name) + "]"
	}
	var arms []string
	for _, path := range jsonPaths(name) {
		p := quoteAll(path)
		arms = append(arms,
			fmt.Sprintf("JSONType(Body, %[1]s) = 'String', JSONExtractString(Body, %[1]s)", p),
			fmt.Sprintf("JSONType(Body, %[1]s) IN ('Int64', 'UInt64', 'Double', 'Bool'), JSONExtractRaw(Body, %[1]s)", p))
	}
	return "multiIf(" + strings.Join(arms, ", ") + ", '')"
}

// maxJSONSplits caps the nested paths tried for one name: a name with k
// underscores stands for 2^k paths.
const maxJSONSplits = 4

// jsonPaths lists the JSON paths a Loki json label name can come from: the
// key itself, then every way of splitting it at underscores into a nested
// path. Past maxJSONSplits underscores only the key and the full split are
// tried.
func jsonPaths(name string) [][]string {
	parts := strings.Split(name, "_")
	n := len(parts) - 1
	if n == 0 || slices.Contains(parts, "") {
		return [][]string{{name}}
	}
	if n > maxJSONSplits {
		return [][]string{{name}, parts}
	}
	var out [][]string
	for mask := 0; mask < 1<<n; mask++ {
		path := []string{parts[0]}
		for i := 1; i < len(parts); i++ {
			if mask&(1<<(i-1)) != 0 {
				path = append(path, parts[i])
			} else {
				path[len(path)-1] += "_" + parts[i]
			}
		}
		out = append(out, path)
	}
	return out
}

func joinPieces(p []string) string {
	if len(p) == 1 {
		return p[0]
	}
	return "concat(" + strings.Join(p, ", ") + ")"
}

// plainAlternation is a list of values, optionally in one group: warn|error,
// (warn|error) or (?:warn|error).
var plainAlternation = regexp.MustCompile(`^(?:[A-Za-z0-9_-]+(?:\|[A-Za-z0-9_-]+)*|\((?:\?:)?[A-Za-z0-9_-]+(?:\|[A-Za-z0-9_-]+)*\))$`)

// namesValue is a regex with a literal letter or digit in it, which could be a
// level spelled Loki's way. One without (.+, .*) means the same on either
// spelling; BuildLokiQuery adds level=~".+" to a selector of negations.
var namesValue = regexp.MustCompile(`[A-Za-z0-9]`)

// matchCond renders a label comparison. Loki anchors label regexes, so the
// SQL wraps them in ^(?:...)$. For level, values are mapped onto the column's
// spelling, and a plain alternation becomes IN.
func matchCond(expr string, isLevel bool, op, value string, pos int) (string, error) {
	switch op {
	case "=", "!=":
		if isLevel {
			var err error
			if value, err = levelValue(value); err != nil {
				return "", err
			}
		}
		return expr + " " + op + " " + quote(value), nil
	case "=~", "!~":
		if _, err := regexp.Compile("^(?:" + value + ")$"); err != nil {
			return "", invalidf(pos, "bad regex %q: %v", value, err)
		}
		if isLevel && namesValue.MatchString(value) {
			// level holds our spellings (warning, critical); a regex written in
			// Loki's (warn, fatal) can only be mapped when it is a list of values.
			if !plainAlternation.MatchString(value) {
				return "", unsupported(`a level regex other than a list of values, such as level=~"warn|error"`)
			}
			value = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(value, "(?:"), "("), ")")
			parts := strings.Split(value, "|")
			for i, v := range parts {
				lv, err := levelValue(v)
				if err != nil {
					return "", err
				}
				parts[i] = quote(lv)
			}
			in := " IN ("
			if op == "!~" {
				in = " NOT IN ("
			}
			return expr + in + strings.Join(parts, ", ") + ")", nil
		}
		c := "match(" + expr + ", " + quote("^(?:"+value+")$") + ")"
		if op == "!~" {
			c = "NOT " + c
		}
		return c, nil
	}
	return "", invalidf(pos, "unknown operator %q", op)
}

// levelValue maps a level written Loki's way onto the level column's
// lowercase spelling (WARN and warn are both warning). Any other value, such
// as trace or err, is refused: the column never holds it, so it would
// silently match nothing.
func levelValue(v string) (string, error) {
	v = strings.ToLower(v)
	if mapped, ok := levelValues[v]; ok {
		return mapped, nil
	}
	if !slices.Contains(levelColumnValues, v) {
		return "", badRequestf("level %q is not one of %s (warn and fatal also work)", v, strings.Join(levelColumnValues, ", "))
	}
	return v, nil
}

// lineConds renders a line filter. Line regexes are not anchored; (?-s) stops
// ClickHouse's `.` matching a newline, which Loki's Go RE2 does not.
func lineConds(f lineFilter) ([]string, error) {
	var parts []string
	for _, v := range f.values {
		switch f.op {
		case tPipeEq:
			parts = append(parts, "position(Body, "+quote(v)+") > 0")
		case tNeq:
			parts = append(parts, "position(Body, "+quote(v)+") = 0")
		case tPipeTilde, tNre:
			if _, err := regexp.Compile(v); err != nil {
				return nil, invalidf(f.pos, "bad regex %q: %v", v, err)
			}
			c := "match(Body, " + quote("(?-s)"+v) + ")"
			if f.op == tNre {
				c = "NOT " + c
			}
			parts = append(parts, c)
		}
	}
	// `|= "a" or "b"` keeps a line with either; `!= "a" or "b"` drops a line
	// with either, so negative chains are ANDed.
	if len(parts) > 1 && (f.op == tPipeEq || f.op == tPipeTilde) {
		return []string{"(" + strings.Join(parts, " OR ") + ")"}, nil
	}
	return parts, nil
}

// labelConds renders a label-filter stage; top-level ANDs become separate
// conditions.
func (t *translator) labelConds(e labelExpr, top bool) ([]string, error) {
	switch e := e.(type) {
	case labelBool:
		if top && e.and {
			l, err := t.labelConds(e.l, true)
			if err != nil {
				return nil, err
			}
			r, err := t.labelConds(e.r, true)
			if err != nil {
				return nil, err
			}
			return append(l, r...), nil
		}
		// A chain of one operator is one group, not one level of nesting per
		// term: ClickHouse refuses SQL nested past max_parser_depth.
		var parts []string
		for _, x := range sameOp(e, e.and) {
			c, err := t.labelConds(x, false)
			if err != nil {
				return nil, err
			}
			parts = append(parts, c...)
		}
		op := " OR "
		if e.and {
			op = " AND "
		}
		return []string{"(" + strings.Join(parts, op) + ")"}, nil
	case labelCmp:
		c, err := t.labelCmp(e)
		if err != nil {
			return nil, err
		}
		if top && e.name == "__error__" && e.op == tEq {
			t.noJSONErrors = true
		}
		return []string{c}, nil
	}
	return nil, fmt.Errorf("logql2ch: unexpected label expression %T", e)
}

// sameOp lists the operands of a chain of and (or of or), in order.
func sameOp(e labelExpr, and bool) []labelExpr {
	if b, ok := e.(labelBool); ok && b.and == and {
		return append(sameOp(b.l, and), sameOp(b.r, and)...)
	}
	return []labelExpr{e}
}

func (t *translator) labelCmp(c labelCmp) (string, error) {
	if c.name == "__error__" {
		if t.parser != "json" || c.numeric || c.str != "" || (c.op != tEq && c.op != tNeq) {
			return "", unsupported(`__error__ filters other than __error__="" or __error__!="" after json`)
		}
		// Loki's json parser needs an object: 42 and [1, 2] are parse errors too.
		if c.op == tEq {
			return jsonObject, nil
		}
		return "NOT (" + jsonObject + ")", nil
	}
	expr, isLevel, err := t.resolve(c.name)
	if err != nil {
		return "", err
	}
	if !c.numeric {
		return matchCond(expr, isLevel, tokOp(c.op), c.str, c.pos)
	}
	op := tokOp(c.op)
	if op == "==" {
		op = "="
	}
	// A value that is not a number fails the comparison; Loki would keep the
	// line tagged __error__=LabelFilterErr, which ClickHouse has no way to show.
	return "toFloat64OrNull(" + expr + ") " + op + " " + strconv.FormatFloat(c.num, 'f', -1, 64), nil
}

// jsonObject is a line Loki's json parser reads without error.
const jsonObject = "isValidJSON(Body) AND JSONType(Body) = 'Object'"

func tokOp(k tokKind) string {
	return map[tokKind]string{tEq: "=", tEqEq: "==", tNeq: "!=", tRe: "=~", tNre: "!~", tGt: ">", tGte: ">=", tLt: "<", tLte: "<="}[k]
}

func whereOf(conds []string) string {
	if len(conds) == 0 {
		return "1"
	}
	return strings.Join(conds, " AND ")
}

func filterConds(fs []Cond) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		if f.sql != "" {
			out = append(out, "("+f.sql+")")
		}
	}
	return out
}

func andFilters(fs []Cond) string {
	conds := filterConds(fs)
	if len(conds) == 0 {
		return ""
	}
	return "\n  AND " + strings.Join(conds, " AND ")
}

// quote renders s as a ClickHouse string literal, escaping the backslash
// first and then the quote (query.clickhouseDialect.QuoteLiteral's rule).
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func formatDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}
