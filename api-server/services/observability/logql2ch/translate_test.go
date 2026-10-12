package logql2ch

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"nudgebee/services/query"
)

var (
	testStart = time.Unix(1_700_000_000, 0)
	testEnd   = testStart.Add(30 * time.Minute)
)

func testOpts() Options {
	return Options{Schema: SchemaColumns, Start: testStart, End: testEnd}
}

// entryOptions is how the corpus test calls the translator for an entry.
// Unsupported entries get a 5m step, which range-ne-step quotes in its error.
func entryOptions(e corpusEntry, db string, start, end time.Time) Options {
	o := Options{Table: db + ".otel_logs", Schema: SchemaColumns, Start: start, End: end, Step: 5 * time.Minute}
	if e.Kind == "logs" {
		o.Limit, o.Forward, o.Step = e.limit(), e.Direction == "forward", 0
	}
	if e.Step != "" {
		o.Step, _ = time.ParseDuration(e.Step)
	}
	return o
}

var spaces = regexp.MustCompile(`\s+`)

func norm(s string) string { return strings.TrimSpace(spaces.ReplaceAllString(s, " ")) }

// TestTranslate_Corpus is the contract: every v1 entry translates to exactly
// its hand-checked conditions (and series), and every unsupported entry fails
// with exactly its error.
func TestTranslate_Corpus(t *testing.T) {
	for _, e := range loadCorpus(t) {
		t.Run(e.ID, func(t *testing.T) {
			q, err := Translate(e.LogQL, entryOptions(e, "db", testStart, testEnd))
			if e.Status == "unsupported" {
				if err == nil {
					t.Fatalf("translated %s; want error %q\n%s", e.LogQL, e.Error, q.SQL)
				}
				if slices.Contains(e.Features, "invalid-syntax") {
					if !strings.HasPrefix(err.Error(), e.Error) {
						t.Errorf("error %q, want prefix %q", err, e.Error)
					}
					return
				}
				if err.Error() != e.Error {
					t.Errorf("error\n %q\nwant\n %q", err, e.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("translate %s: %v", e.LogQL, err)
			}
			if got, want := norm(q.Where), norm(e.Where); got != want {
				t.Errorf("where\n got: %s\nwant: %s", got, want)
			}
			wantKind := KindLogs
			if e.Kind == "metric" {
				wantKind = KindMetric
				if got, want := norm(q.Series), norm(e.Series); got != want {
					t.Errorf("series\n got: %s\nwant: %s", got, want)
				}
			}
			if q.Kind != wantKind {
				t.Errorf("kind %v, want %v", q.Kind, wantKind)
			}
		})
	}
}

func TestLabels_MatchAliasesYAML(t *testing.T) {
	aliases := loadAliases(t)
	want := make([]string, 0, len(aliases))
	for k := range aliases {
		want = append(want, k)
	}
	slices.Sort(want)
	if got := Labels(); !slices.Equal(got, want) {
		t.Fatalf("Labels() = %v, want %v", got, want)
	}
	for label, sql := range aliases {
		if got, _ := LabelExpr(label, SchemaColumns); norm(got) != norm(sql) {
			t.Errorf("%s: %s, want %s", label, got, sql)
		}
	}
}

func TestTranslate_ExpressionSchema(t *testing.T) {
	o := testOpts()
	o.Schema = SchemaExpressions
	q, err := Translate(`{namespace="shop", app="web"} |= "x"`, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ResourceAttributes['k8s.namespace.name'] = 'shop'",
		"multiIf(ResourceAttributes['k8s.deployment.name'] != ''",
		"position(Body, 'x') > 0",
	} {
		if !strings.Contains(q.Where, want) {
			t.Errorf("missing %q in %s", want, q.Where)
		}
	}
	for _, col := range []string{" namespace ", "workload ="} {
		if strings.Contains(q.Where, col) {
			t.Errorf("expression schema still reads a label column (%q): %s", col, q.Where)
		}
	}
	// job inlines both expressions.
	q, err = Translate(`{job="shop/web"}`, o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(q.Where, "concat(ResourceAttributes['k8s.namespace.name'], '/', multiIf(") {
		t.Errorf("job: %s", q.Where)
	}
}

func TestTranslate_FullSQL(t *testing.T) {
	q, err := Translate(`{namespace="shop"}`, Options{Table: "db.otel_logs", Schema: SchemaColumns, Start: testStart, End: testEnd})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SELECT toString(toUnixTimestamp64Nano(Timestamp)) AS ts, Body, namespace AS l_namespace, pod AS l_pod, container AS l_container, workload AS l_app",
		"TraceId AS trace_id, SpanId AS span_id, SeverityNumber AS severity_number",
		"FROM db.otel_logs",
		"Timestamp >= fromUnixTimestamp64Nano(toInt64(1700000000000000000)) AND Timestamp < fromUnixTimestamp64Nano(toInt64(1700001800000000000))",
		"AND (namespace = 'shop')", "ORDER BY Timestamp DESC", "LIMIT 5000",
	} {
		if !strings.Contains(q.SQL, want) {
			t.Errorf("missing %q in\n%s", want, q.SQL)
		}
	}
	if strings.Contains(q.SQL, "SETTINGS") {
		t.Errorf("a readonly=1 user rejects a SETTINGS clause:\n%s", q.SQL)
	}

	o := testOpts()
	o.Limit, o.Forward = 200, true
	q, _ = Translate(`{namespace="shop"}`, o)
	if !strings.Contains(q.SQL, "ORDER BY Timestamp ASC\nLIMIT 200") || !strings.Contains(q.SQL, "FROM otel_logs") {
		t.Errorf("forward / default table:\n%s", q.SQL)
	}

	// A window already on the 5m grid, so nothing snaps.
	o = testOpts()
	o.Start = time.Unix(1_700_000_100, 0)
	o.End = o.Start.Add(30 * time.Minute)
	q, err = Translate(`sum by (pod) (rate({namespace="shop"}[5m]))`, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`concat('pod="', pod, '"') AS series`,
		"1700000100 + intDiv(toUnixTimestamp64Nano(Timestamp) - 1700000100000000000 + 300000000000 - 1, 300000000000) * 300 AS t",
		"toFloat64(count()) / 300 AS v, any([toString(pod)]) AS lv",
		"Timestamp > fromUnixTimestamp64Nano(toInt64(1699999800000000000)) AND Timestamp <= fromUnixTimestamp64Nano(toInt64(1700001900000000000))",
		"GROUP BY series, t", "ORDER BY series, t",
	} {
		if !strings.Contains(q.SQL, want) {
			t.Errorf("missing %q in\n%s", want, q.SQL)
		}
	}
	if !slices.Equal(q.Labels, []string{"pod"}) || q.Step != 5*time.Minute {
		t.Errorf("labels %v step %v", q.Labels, q.Step)
	}

	q, _ = Translate(`sum(count_over_time({namespace="shop"}[5m]))`, testOpts())
	if !strings.Contains(q.SQL, "'' AS series") || !strings.Contains(q.SQL, "toFloat64(count()) AS v\n") || strings.Contains(q.SQL, " lv") {
		t.Errorf("sum without labels:\n%s", q.SQL)
	}
}

// A metric window snaps to the step grid like Loki's query_range: start down,
// end up.
func TestTranslate_MetricWindowSnaps(t *testing.T) {
	o := testOpts()
	o.Start, o.End = time.Unix(1_700_000_123, 0), time.Unix(1_700_000_900, 500)
	q, err := Translate(`sum(count_over_time({namespace="a"}[5m]))`, o)
	if err != nil {
		t.Fatal(err)
	}
	if q.Start.Unix() != 1_700_000_100 || q.End.Unix() != 1_700_001_000 {
		t.Errorf("window %d..%d, want 1700000100..1700001000", q.Start.Unix(), q.End.Unix())
	}
	o.Start, o.End = testStart, testStart.Add(30*24*time.Hour)
	if _, err := Translate(`sum(count_over_time({namespace="a"}[1m]))`, o); kindOf(err) != KindBadRequest {
		t.Errorf("43201 points: err %v, want a bad request", err)
	}
	o.End = testEnd
	if _, err := Translate(`sum(count_over_time({namespace="a"}[1500ms]))`, o); kindOf(err) != KindBadRequest {
		t.Errorf("sub-second range: err %v", err)
	}
}

func TestTranslate_Cases(t *testing.T) {
	cases := []struct {
		logql, where string
	}{
		// Line filters chained with or: positive ones OR, negative ones AND.
		{`{namespace="a"} |= "x" or "y"`, `namespace = 'a' AND (position(Body, 'x') > 0 OR position(Body, 'y') > 0)`},
		{`{namespace="a"} != "x" or "y"`, `namespace = 'a' AND position(Body, 'x') = 0 AND position(Body, 'y') = 0`},
		{`{namespace="a"} |~ "x" or "y"`, `namespace = 'a' AND (match(Body, '(?-s)x') OR match(Body, '(?-s)y'))`},
		// Label filters: and / comma / or / parentheses; stream labels need no parser.
		{`{namespace="a"} | pod="p" and container!="c"`, `namespace = 'a' AND pod = 'p' AND container != 'c'`},
		{`{namespace="a"} | pod="p", container="c"`, `namespace = 'a' AND pod = 'p' AND container = 'c'`},
		{`{namespace="a"} | pod="p" or container="c"`, `namespace = 'a' AND (pod = 'p' OR container = 'c')`},
		{`{namespace="a"} | (pod="p" or pod="q") and stream="stderr"`, `namespace = 'a' AND (pod = 'p' OR pod = 'q') AND stream = 'stderr'`},
		{`{namespace="a"} | pod=~"web-.*"`, `namespace = 'a' AND match(pod, '^(?:web-.*)$')`},
		// level: values map to the column's spelling; a plain alternation is IN.
		{`{level="warn"}`, `level = 'warning'`},
		{`{level=~"fatal|error"}`, `level IN ('critical', 'error')`},
		{`{namespace="a", level!~"debug|info"}`, `namespace = 'a' AND level NOT IN ('debug', 'info')`},
		{`{level=~"(warn|error)"}`, `level IN ('warning', 'error')`},
		{`{level=~"(?:WARN|Error)"}`, `level IN ('warning', 'error')`},
		{`{level="ERROR"}`, `level = 'error'`},
		// A level regex that names no value means the same on either spelling;
		// BuildLokiQuery writes level=~".+" before a selector of negations.
		{`{level!="debug", level=~".+"}`, `level != 'debug' AND match(level, '^(?:.+)$')`},
		{`{namespace="a"} | level=~".*"`, `namespace = 'a' AND match(level, '^(?:.*)$')`},
		// A chain of one operator is one group, however it is parenthesized.
		{`{namespace="a"} | pod="p" or pod="q" or pod="r" or pod="s"`, `namespace = 'a' AND (pod = 'p' OR pod = 'q' OR pod = 'r' OR pod = 's')`},
		{`{namespace="a"} | pod="p" and (stream="x" or (container="c" or container="d"))`, `namespace = 'a' AND pod = 'p' AND (stream = 'x' OR container = 'c' OR container = 'd')`},
		{`{namespace="a"} | pod="p" or (stream="x" and container="c" and container="d")`, `namespace = 'a' AND (pod = 'p' OR (stream = 'x' AND container = 'c' AND container = 'd'))`},
		{`{namespace="a"} | level="warn"`, `namespace = 'a' AND level = 'warning'`},
		// Parsers: numeric comparisons, negative numbers, logfmt strings, __error__.
		// A name with an underscore is also looked up as the nested path Loki
		// flattens to it, the top-level key first.
		{`{namespace="a"} | json | latency_ms > -1.5`, `namespace = 'a' AND toFloat64OrNull(multiIf(JSONType(Body, 'latency_ms') = 'String', JSONExtractString(Body, 'latency_ms'), JSONType(Body, 'latency_ms') IN ('Int64', 'UInt64', 'Double', 'Bool'), JSONExtractRaw(Body, 'latency_ms'), JSONType(Body, 'latency', 'ms') = 'String', JSONExtractString(Body, 'latency', 'ms'), JSONType(Body, 'latency', 'ms') IN ('Int64', 'UInt64', 'Double', 'Bool'), JSONExtractRaw(Body, 'latency', 'ms'), '')) > -1.5`},
		{`{namespace="a"} | json | x >= 1e3`, `namespace = 'a' AND toFloat64OrNull(multiIf(JSONType(Body, 'x') = 'String', JSONExtractString(Body, 'x'), JSONType(Body, 'x') IN ('Int64', 'UInt64', 'Double', 'Bool'), JSONExtractRaw(Body, 'x'), '')) >= 1000`},
		{`{namespace="a"} | json | code == 5`, `namespace = 'a' AND toFloat64OrNull(multiIf(JSONType(Body, 'code') = 'String', JSONExtractString(Body, 'code'), JSONType(Body, 'code') IN ('Int64', 'UInt64', 'Double', 'Bool'), JSONExtractRaw(Body, 'code'), '')) = 5`},
		{`{namespace="a"} | logfmt | method="GET"`, `namespace = 'a' AND extractKeyValuePairs(Body, '=', ' ', '"')['method'] = 'GET'`},
		{`{namespace="a"} | json | __error__=""`, `namespace = 'a' AND isValidJSON(Body) AND JSONType(Body) = 'Object'`},
		{`{namespace="a"} | json | __error__!=""`, `namespace = 'a' AND NOT (isValidJSON(Body) AND JSONType(Body) = 'Object')`},
		// After a parser a stream label keeps its meaning.
		{`{namespace="a"} | json | pod="p"`, `namespace = 'a' AND pod = 'p'`},
		// Comments and the pipeline after the range.
		{"{namespace=\"a\"} # all of a\n|= \"x\"", `namespace = 'a' AND position(Body, 'x') > 0`},
		// Empty-value line filters keep Loki's meaning (|= "" keeps all, != "" drops all).
		{`{namespace="a"} |= ""`, `namespace = 'a' AND position(Body, '') > 0`},
	}
	for _, c := range cases {
		q, err := Translate(c.logql, testOpts())
		if err != nil {
			t.Errorf("%s: %v", c.logql, err)
			continue
		}
		if q.Where != c.where {
			t.Errorf("%s\n got: %s\nwant: %s", c.logql, q.Where, c.where)
		}
	}
	q, err := Translate(`sum by (namespace) (count_over_time({namespace="a"} |= "x" [5m]))`, testOpts())
	if err != nil || q.Where != `namespace = 'a' AND position(Body, 'x') > 0` {
		t.Errorf("pipeline inside the range: %v %s", err, q.Where)
	}
	q, err = Translate(`count_over_time({namespace="a"}[5m] |= "x")`, testOpts())
	if err != nil || q.Where != `namespace = 'a' AND position(Body, 'x') > 0` {
		t.Errorf("pipeline after the range: %v %s", err, q.Where)
	}
	q, err = Translate(`sum by (status) (count_over_time({namespace="a"} | json | __error__="" [5m]))`, testOpts())
	if err != nil || !strings.Contains(q.Series, "JSONExtractString(Body, 'status')") {
		t.Errorf("group by an extracted field: %v %s", err, q.Series)
	}
}

func TestTranslate_Rejects(t *testing.T) {
	cases := []struct {
		logql string
		kind  ErrorKind
		msg   string // substring
	}{
		{`{}`, KindInvalid, "expected a label name"},
		{`{namespace=~".*"}`, KindInvalid, "does not match the empty string"},
		{`{namespace!="a"}`, KindInvalid, "does not match the empty string"},
		{`{namespace="a"`, KindInvalid, "expected"},
		{`{namespace='a'}`, KindInvalid, "unexpected character"},
		{`{namespace="a"} |~ "("`, KindInvalid, "bad regex"},
		{`{namespace=~"("}`, KindInvalid, "bad regex"},
		{`{namespace="a"} |= "unterminated`, KindInvalid, "unterminated"},
		{`{namespace="a"} | latency > 250ms`, KindUnsupported, "duration and bytes"},
		{`{namespace="a"} | json foo="bar"`, KindUnsupported, "json with parameters"},
		{`{namespace="a"} | logfmt --strict`, KindUnsupported, "logfmt with parameters"},
		{`{namespace="a"} | json | logfmt`, KindUnsupported, "more than one parser"},
		{`{namespace="a"} |> "<_> error"`, KindUnsupported, "pattern line filter"},
		{`{namespace="a"} |= ip("10.0.0.1")`, KindUnsupported, "ip line filter"},
		{`{namespace="a"} | logfmt | __error__=""`, KindUnsupported, "__error__"},
		{`{namespace="a"} | foo="x"`, KindUnknownLabel, `unknown label "foo"`},
		{`avg(count_over_time({namespace="a"}[5m]))`, KindUnsupported, "avg"},
		{`sum without (pod) (count_over_time({namespace="a"}[5m]))`, KindUnsupported, "without"},
		{`sum(sum(count_over_time({namespace="a"}[5m])))`, KindUnsupported, "nested"},
		{`count_over_time({namespace="a"} | json | __error__="" [5m])`, KindUnsupported, "needs sum or sum by"},
		{`sum(count_over_time({namespace="a"} | json [5m]))`, KindUnsupported, `without | __error__=""`},
		{`sum(count_over_time({namespace="a"} | json | __error__="" or x="1" [5m]))`, KindUnsupported, `without | __error__=""`},
		{`sum(count_over_time({namespace="a"} | logfmt [5m]))`, KindUnsupported, "over | logfmt"},
		{`{level=~"err.*"}`, KindUnsupported, "a level regex other than a list of values"},
		{`{namespace="a"} | level=~"(warn|error"`, KindInvalid, "bad regex"},
		{`sum(count_over_time({namespace="a"}[36028797018964968s]))`, KindInvalid, "too long"},
		{`sum(count_over_time({namespace="a"}[1h9223372036854775807s]))`, KindInvalid, "too long"},
		{`sum(count_over_time({namespace="a"}[31d]))`, KindBadRequest, "longer than 30 days"},
		{`{level="trace"}`, KindBadRequest, `level "trace" is not one of`},
		{`{namespace="a", level!="err"}`, KindBadRequest, `level "err" is not one of`},
		{`{level=~"warn|trace"}`, KindBadRequest, `level "trace" is not one of`},
		{strings.Repeat("(", 65) + `{namespace="a"}` + strings.Repeat(")", 65), KindInvalid, "nesting deeper than 64"},
		{`{namespace="a"} | ` + strings.Repeat("(", 65) + `pod="p"` + strings.Repeat(")", 65), KindInvalid, "nesting deeper than 64"},
		{`{namespace="a"} |= "` + strings.Repeat("x", 64<<10) + `"`, KindBadRequest, "longer than 64 KiB"},
		{`sum(count_over_time({namespace="a"}[5m])) / 2`, KindUnsupported, "binary operations"},
		{`count_over_time({namespace="a"}[5m]) or count_over_time({namespace="b"}[5m])`, KindUnsupported, "binary operations"},
		{`sum(count_over_time({namespace="a"}))`, KindInvalid, "range"},
		{`sum({namespace="a"})`, KindInvalid, "range aggregation"},
		{`rate_counter({namespace="a"}[5m])`, KindUnsupported, "rate_counter"},
	}
	for _, c := range cases {
		_, err := Translate(c.logql, testOpts())
		if err == nil {
			t.Errorf("%s: translated; want %v error", c.logql, c.kind)
			continue
		}
		if kindOf(err) != c.kind || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v (kind %v), want kind %v containing %q", c.logql, err, kindOf(err), c.kind, c.msg)
		}
	}
}

func TestTranslate_Limits(t *testing.T) {
	// 64 levels of nesting is fine; 65 is refused (TestTranslate_Rejects).
	if _, err := Translate(strings.Repeat("(", 64)+`{namespace="a"}`+strings.Repeat(")", 64), testOpts()); err != nil {
		t.Errorf("64 levels: %v", err)
	}
	// Loki's point cap is on the requested window: exactly 11000 steps is allowed.
	o := testOpts()
	o.End = o.Start.Add(11000 * time.Second)
	if _, err := Translate(`sum(count_over_time({namespace="a"}[1s]))`, o); err != nil {
		t.Errorf("11000 points: %v", err)
	}
	o.End = o.End.Add(time.Second)
	if _, err := Translate(`sum(count_over_time({namespace="a"}[1s]))`, o); kindOf(err) != KindBadRequest {
		t.Errorf("11001 points: %v", err)
	}

	// A long chain of one operator renders flat: ClickHouse refuses SQL nested
	// past max_parser_depth (1000).
	q, err := Translate(`{namespace="a"} | pod="p"`+strings.Repeat(` or pod="p"`, 1500), testOpts())
	if err != nil {
		t.Fatal(err)
	}
	if d := parenDepth(q.Where); d != 1 {
		t.Errorf("an or chain of 1501 nests %d deep", d)
	}
	// A short query can render past ClickHouse's max_query_size (256 KiB): each
	// json field is a multiIf over every nested path its name could stand for.
	_, err = Translate(`{namespace="a"} | json`+strings.Repeat(` | a_b_c_d="1"`, 200), testOpts())
	if kindOf(err) != KindBadRequest || !strings.Contains(err.Error(), "KiB of SQL") {
		t.Errorf("200 json filters: %v", err)
	}
	if _, err := Translate(`{namespace="a"} | json`+strings.Repeat(` | a_b_c_d="1"`, 20), testOpts()); err != nil {
		t.Errorf("20 json filters: %v", err)
	}
}

// parenDepth is the deepest parenthesis nesting in s.
func parenDepth(s string) int {
	depth, deepest := 0, 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
			deepest = max(deepest, depth)
		case ')':
			depth--
		}
	}
	return deepest
}

func TestJSONPaths(t *testing.T) {
	cases := map[string][][]string{
		"status":      {{"status"}},
		"a_b":         {{"a_b"}, {"a", "b"}},
		"a_b_c":       {{"a_b_c"}, {"a", "b_c"}, {"a_b", "c"}, {"a", "b", "c"}},
		"a__b":        {{"a__b"}},
		"_a":          {{"_a"}},
		"a_b_c_d_e":   nil, // 16 paths
		"a_b_c_d_e_f": {{"a_b_c_d_e_f"}, {"a", "b", "c", "d", "e", "f"}},
	}
	for name, want := range cases {
		got := jsonPaths(name)
		if want == nil {
			if len(got) != 16 || !slices.Equal(got[0], []string{name}) {
				t.Errorf("%s: %d paths, first %v", name, len(got), got[0])
			}
			continue
		}
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", name, got, want)
			continue
		}
		for i := range want {
			if !slices.Equal(got[i], want[i]) {
				t.Errorf("%s: %v, want %v", name, got, want)
			}
		}
	}
}

func TestOptions_Validated(t *testing.T) {
	bad := []Options{
		{Start: testStart},                                             // no end
		{Start: testEnd, End: testStart},                               // reversed
		{Start: testStart, End: testEnd, Limit: 5001},                  // over the cap
		{Start: testStart, End: testEnd, Limit: -1},                    // negative
		{Start: testStart, End: testEnd, Table: "t; x"},                // not a table name
		{Start: testStart, End: testStart.Add(maxRange + time.Second)}, // window over 30 days
	}
	for _, o := range bad {
		if _, err := Translate(`{namespace="a"}`, o); kindOf(err) != KindBadRequest {
			t.Errorf("%+v: err %v, want a bad request", o, err)
		}
	}
	// The window cap holds on every entry point, and exactly 30 days is allowed.
	long := Options{Start: testStart, End: testStart.Add(maxRange + time.Second)}
	if _, err := Logs(long); kindOf(err) != KindBadRequest {
		t.Errorf("Logs over 30 days: %v", err)
	}
	if _, err := LabelValuesSQL("pod", long); kindOf(err) != KindBadRequest {
		t.Errorf("LabelValuesSQL over 30 days: %v", err)
	}
	if _, err := Translate(`sum(count_over_time({namespace="a"}[1h]))`, long); kindOf(err) != KindBadRequest {
		t.Errorf("metric over 30 days: %v", err)
	}
	long.End = testStart.Add(maxRange)
	if _, err := Translate(`{namespace="a"}`, long); err != nil {
		t.Errorf("exactly 30 days: %v", err)
	}
}

// A metric query asks for one row past MaxMetricRows, so the caller can tell
// a cut answer from a complete one.
func TestTranslate_MetricRowLimit(t *testing.T) {
	q, err := Translate(`sum by (pod) (count_over_time({namespace="a"}[5m]))`, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("LIMIT %d", MaxMetricRows+1); !strings.HasSuffix(q.SQL, want) {
		t.Errorf("metric SQL does not end with %q:\n%s", want, q.SQL)
	}
}

// Every value reaches the SQL as one escaped literal: an attempt to close the
// string stays inside it.
func TestTranslate_ValuesStayLiterals(t *testing.T) {
	q, err := Translate(`{namespace="x' OR 1=1 --"} |= "a\\' ) ; DROP TABLE otel_logs --"`, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	want := `namespace = 'x\' OR 1=1 --' AND position(Body, 'a\\\' ) ; DROP TABLE otel_logs --') > 0`
	if q.Where != want {
		t.Errorf("\n got: %s\nwant: %s", q.Where, want)
	}
	q, err = Translate(`{namespace="a"} | json | `+"`x') OR 1`"+`="v"`, testOpts())
	if err == nil {
		t.Errorf("a backtick label name must not parse: %s", q.Where)
	}
}

func TestWhereCond(t *testing.T) {
	type bw = query.BinaryWhereClause
	type ops = map[query.BinaryWhereClauseType]any
	cases := []struct {
		name  string
		where query.QueryWhereClause
		want  string
	}{
		{"labels and in", query.QueryWhereClause{Binary: bw{
			"app": ops{query.Eq: "web"}, "namespace": ops{query.In: []any{"shop", "pay"}}}},
			`workload = 'web' AND namespace IN ('shop', 'pay')`},
		{"not in, empty in", query.QueryWhereClause{Binary: bw{
			"pod": ops{query.NotIn: []string{"a"}}, "container": ops{query.In: []any{}}}},
			`0 AND pod NOT IN ('a')`},
		{"cross-field or keeps OR", query.QueryWhereClause{Or: []query.QueryWhereClause{
			{Binary: bw{"namespace": ops{query.Eq: "a"}}}, {Binary: bw{"pod": ops{query.Eq: "b"}}}}},
			`(namespace = 'a' OR pod = 'b')`},
		{"not over two labels is one NOT", query.QueryWhereClause{Not: &query.QueryWhereClause{Binary: bw{
			"namespace": ops{query.Eq: "a"}, "pod": ops{query.Eq: "b"}}}},
			`NOT (namespace = 'a' AND pod = 'b')`},
		{"log field is the line", query.QueryWhereClause{Binary: bw{"log": ops{
			query.Eq: "x", query.ILike: "%err%", query.IContains: "", query.Regex: "a.b"}}},
			`position(Body, 'x') > 0 AND Body ILIKE '%err%' AND match(Body, '(?-s)a.b')`},
		{"trace id checks the column and the line", query.QueryWhereClause{Binary: bw{"trace_id": ops{query.Eq: "4bf9"}}},
			`(TraceId = '4bf9' OR position(Body, '4bf9') > 0)`},
		{"level mapped", query.QueryWhereClause{Binary: bw{"level": ops{query.In: []any{"warn", "error"}}}},
			`level IN ('warning', 'error')`},
		{"label regex anchored", query.QueryWhereClause{Binary: bw{"pod": ops{query.Regex: "web-.*"}}},
			`match(pod, '^(?:web-.*)$')`},
		{"timestamp", query.QueryWhereClause{Binary: bw{"timestamp": ops{query.Gte: "2026-10-12T00:00:00Z"}}},
			`Timestamp >= parseDateTime64BestEffort('2026-10-12T00:00:00Z', 9)`},
		{"nested and", query.QueryWhereClause{And: []query.QueryWhereClause{
			{Binary: bw{"namespace": ops{query.Eq: "a"}}},
			{Or: []query.QueryWhereClause{{Binary: bw{"pod": ops{query.Eq: "p"}}}, {Binary: bw{"log": ops{query.Contains: "x"}}}}}}},
			`namespace = 'a' AND (pod = 'p' OR position(Body, 'x') > 0)`},
	}
	for _, c := range cases {
		cond, err := WhereCond(c.where, SchemaColumns)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if cond.sql != c.want {
			t.Errorf("%s\n got: %s\nwant: %s", c.name, cond.sql, c.want)
		}
	}

	if _, err := WhereCond(query.QueryWhereClause{Binary: query.BinaryWhereClause{"cluster": {query.Eq: "x"}}}, SchemaColumns); kindOf(err) != KindUnknownLabel {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := WhereCond(query.QueryWhereClause{Binary: query.BinaryWhereClause{"pod": {query.Gt: 1}}}, SchemaColumns); kindOf(err) != KindBadRequest {
		t.Errorf("unsupported operator: %v", err)
	}
	if cond, err := WhereCond(query.QueryWhereClause{}, SchemaColumns); err != nil || cond.sql != "" {
		t.Errorf("empty where: %q %v", cond.sql, err)
	}
	// A level the column never holds is refused, not silently empty.
	for _, w := range []query.BinaryWhereClause{
		{"level": {query.Eq: "trace"}},
		{"level": {query.Nq: "err"}},
		{"level": {query.In: []any{"error", "trace"}}},
	} {
		if _, err := WhereCond(query.QueryWhereClause{Binary: w}, SchemaColumns); kindOf(err) != KindBadRequest || !strings.Contains(fmt.Sprint(err), "is not one of") {
			t.Errorf("%v: %v", w, err)
		}
	}

	// level: values map, regexes must be a list of values, patterns are refused.
	for _, c := range []struct {
		where query.QueryWhereClause
		want  string
	}{
		{query.QueryWhereClause{Binary: bw{"level": ops{query.Regex: "warn|error"}}}, `level IN ('warning', 'error')`},
		{query.QueryWhereClause{Binary: bw{"level": ops{query.Regex: ".+"}}}, `match(level, '^(?:.+)$')`},
		{query.QueryWhereClause{Binary: bw{"level": ops{query.NRegex: "WARN|fatal"}}}, `level NOT IN ('warning', 'critical')`},
		{query.QueryWhereClause{Binary: bw{"level": ops{query.Eq: "ERROR"}}}, `level = 'error'`},
		{query.QueryWhereClause{Binary: bw{"log": ops{query.In: []any{}}}}, `0`},
		{query.QueryWhereClause{Binary: bw{"log": ops{query.NotIn: []string{}}}}, ``}, // filters nothing
		{query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "a"}}, Not: &query.QueryWhereClause{}}, `namespace = 'a'`},
		{query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "a"}}, Not: &query.QueryWhereClause{Binary: bw{"log": ops{query.IContains: ""}}}}, `namespace = 'a'`},
	} {
		cond, err := WhereCond(c.where, SchemaColumns)
		if err != nil || cond.sql != c.want {
			t.Errorf("%+v: %q %v, want %q", c.where, cond.sql, err, c.want)
		}
	}
	for _, w := range []query.QueryWhereClause{
		{Binary: bw{"level": ops{query.Like: "fatal%"}}},
		{Binary: bw{"level": ops{query.IContains: "warn"}}},
		{Binary: bw{"pod": ops{query.Regex: "("}}},
	} {
		if _, err := WhereCond(w, SchemaColumns); kindOf(err) != KindBadRequest {
			t.Errorf("%+v: %v, want a bad request", w, err)
		}
	}
	if _, err := WhereCond(query.QueryWhereClause{Binary: bw{"level": ops{query.Regex: "err.*"}}}, SchemaColumns); kindOf(err) != KindUnsupported {
		t.Errorf("level regex that is not a list: %v", err)
	}

	// The caller's values are not rewritten in place.
	in := []string{"warn", "fatal"}
	if _, err := WhereCond(query.QueryWhereClause{Binary: bw{"level": ops{query.In: in}}}, SchemaColumns); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(in, []string{"warn", "fatal"}) {
		t.Errorf("WhereCond rewrote the caller's slice: %v", in)
	}

	// A Builder query and a LogQL query share one renderer, and filters AND in.
	cond, _ := WhereCond(query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "a"}}}, SchemaColumns)
	o := testOpts()
	o.Filters = []Cond{cond}
	q, err := Logs(o)
	if err != nil || !strings.Contains(q.SQL, "AND (1)\n  AND (namespace = 'a')") {
		t.Errorf("Logs: %v\n%s", err, q.SQL)
	}
	q, err = Translate(`{pod="p"} |= "x"`, o)
	if err != nil || !strings.Contains(q.SQL, "AND (pod = 'p' AND position(Body, 'x') > 0)\n  AND (namespace = 'a')") {
		t.Errorf("Translate with a filter: %v\n%s", err, q.SQL)
	}
}

func TestLabelValuesSQL(t *testing.T) {
	cond, _ := WhereCond(query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "a"}}}, SchemaColumns)
	o := testOpts()
	o.Filters = []Cond{cond}
	sql, err := LabelValuesSQL("app", o)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT DISTINCT workload AS v FROM otel_logs WHERE Timestamp >= fromUnixTimestamp64Nano(toInt64(1700000000000000000)) AND Timestamp < fromUnixTimestamp64Nano(toInt64(1700001800000000000)) AND workload != '' AND (namespace = 'a') ORDER BY v LIMIT 1000"
	if sql != want {
		t.Errorf("\n got: %s\nwant: %s", sql, want)
	}
	if _, err := LabelValuesSQL("instance", o); kindOf(err) != KindUnknownLabel {
		t.Errorf("unknown label: %v", err)
	}
}

func TestSchemaProbe(t *testing.T) {
	if got := SchemaProbeSQL("otel_logs"); got != "SELECT name FROM system.columns WHERE database = currentDatabase() AND table = 'otel_logs' AND name IN ('container', 'level', 'namespace', 'node', 'pod', 'stream', 'workload')" {
		t.Errorf("probe: %s", got)
	}
	if got := SchemaProbeSQL("db.otel_logs"); !strings.Contains(got, "database = 'db' AND table = 'otel_logs'") {
		t.Errorf("qualified probe: %s", got)
	}
	all := []string{"container", "level", "namespace", "node", "pod", "stream", "workload"}
	if SchemaFromColumns(all) != SchemaColumns || SchemaFromColumns(all[1:]) != SchemaExpressions || SchemaFromColumns(nil) != SchemaExpressions {
		t.Error("SchemaFromColumns needs every label column")
	}
}

func FuzzTranslate(f *testing.F) {
	for _, e := range loadCorpus(f) {
		f.Add(e.LogQL)
	}
	f.Add(`{a="b"} | json | x > 1 or (y =~ "z" and w != "")`)
	f.Fuzz(func(t *testing.T, s string) {
		q, err := Translate(s, testOpts())
		if err != nil {
			var te *Error
			if !errors.As(err, &te) {
				t.Fatalf("%q: error is not a *logql2ch.Error: %v", s, err)
			}
			return
		}
		if q.SQL == "" || q.Where == "" {
			t.Fatalf("%q: empty SQL", s)
		}
	})
}

func kindOf(err error) ErrorKind {
	var te *Error
	if errors.As(err, &te) {
		return te.Kind
	}
	return -1
}
