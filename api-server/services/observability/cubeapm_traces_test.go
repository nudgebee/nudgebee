package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
)

// ---------------------------------------------------------------------------
// LogsQL span queries
// ---------------------------------------------------------------------------

func eq(field, value string) query.QueryWhereClause {
	return query.QueryWhereClause{Binary: query.BinaryWhereClause{field: {query.Eq: value}}}
}

func mustTraceBase(t *testing.T, where query.QueryWhereClause, env string) string {
	t.Helper()
	got, err := cubeAPMTraceBaseQuery("", where, env)
	if err != nil {
		t.Fatalf("cubeAPMTraceBaseQuery: %v", err)
	}
	return got
}

func TestCubeAPMTraceBaseQuerySelector(t *testing.T) {
	// Every span query is scoped to span records; span_event rows (exceptions)
	// live in the same store and would otherwise be listed and counted as spans.
	if got := mustTraceBase(t, query.QueryWhereClause{}, ""); got != `{event.domain="span"}` {
		t.Errorf("no filters = %q", got)
	}
	if got := mustTraceBase(t, query.QueryWhereClause{}, "prod"); got != `{env="prod", event.domain="span"}` {
		t.Errorf("with env = %q", got)
	}
	// An empty _or (which the Traces page sends) must not render as `()`.
	if got := mustTraceBase(t, query.QueryWhereClause{Or: []query.QueryWhereClause{}}, ""); got != `{event.domain="span"}` {
		t.Errorf("empty _or = %q", got)
	}
}

// The regression this change fixes: a service filter must reach CubeAPM whether it
// is top-level or nested in _and. The per-service search only recognised the
// top-level form, so a nested filter fell back to a capped fan-out that never
// searched the service at all.
func TestCubeAPMTraceServiceFilterTopLevelAndNested(t *testing.T) {
	want := `service:="services-server"`

	top := mustTraceBase(t, eq("workload_name", "services-server"), "")
	nested := mustTraceBase(t, query.QueryWhereClause{And: []query.QueryWhereClause{eq("workload_name", "services-server")}}, "")

	for name, got := range map[string]string{"top-level": top, "nested in _and": nested} {
		if !strings.Contains(got, want) {
			t.Errorf("%s: %q does not filter on %s", name, got, want)
		}
	}
}

func TestCubeAPMTraceWhereRendering(t *testing.T) {
	tests := []struct {
		name  string
		where query.QueryWhereClause
		want  []string
		deny  []string
	}{
		{
			name:  "canonical label maps to the LogsQL field",
			where: eq("span_name", "GET /health"),
			want:  []string{`span_name:="GET /health"`},
		},
		{
			name:  "unmapped raw field is used verbatim",
			where: eq("_resource.k8s.pod.name", "api-0"),
			want:  []string{`_resource.k8s.pod.name:="api-0"`},
		},
		{
			name:  "alias label ORs every backing field",
			where: eq("workload_namespace", "payments"),
			want:  []string{`((_resource.k8s.namespace.name:="payments") OR (_resource.service.namespace:="payments"))`},
		},
		{
			// NOT (a OR b), never NOT a OR NOT b — the latter is true for every span
			// whose two alias fields differ.
			name:  "negation wraps the whole alias OR",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"workload_namespace": {query.Nq: "payments"}}},
			want:  []string{`NOT (((_resource.k8s.namespace.name:="payments") OR (_resource.service.namespace:="payments")))`},
			deny:  []string{`NOT (_resource.k8s.namespace.name:="payments") OR`},
		},
		{
			name:  "_in expands to OR-ed equalities",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"workload_name": {query.In: []any{"a", "b"}}}},
			want:  []string{`((service:="a") OR (service:="b"))`},
		},
		{
			name:  "_not_in negates the OR",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"workload_name": {query.NotIn: []any{"a", "b"}}}},
			want:  []string{`NOT (((service:="a") OR (service:="b")))`},
		},
		{
			// The Traces page sends the ClickHouse spelling; CubeAPM stores ERROR and
			// LogsQL equality is case-sensitive.
			name:  "status value is normalised to CubeAPM's spelling",
			where: eq("status_code", "STATUS_CODE_ERROR"),
			want:  []string{`status_code:="ERROR"`},
		},
		{
			// Not field:"*value*": in LogsQL a quoted phrase takes the asterisks
			// literally and matches nothing.
			name:  "contains renders an escaped case-insensitive regex",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"span_name": {query.Contains: "GET /a.b"}}},
			want:  []string{`span_name:~"(?i)GET /a\\.b"`},
			deny:  []string{`*GET`},
		},
		{
			name: "two labels on the same field do not overwrite each other",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"workload_name": {query.Eq: "a"},
				"service_name":  {query.Eq: "b"},
			}},
			want: []string{`service:="a"`, `service:="b"`},
		},
		{
			name: "or and not subtrees are kept",
			where: query.QueryWhereClause{
				Or:  []query.QueryWhereClause{eq("workload_name", "a"), eq("workload_name", "b")},
				Not: &query.QueryWhereClause{Binary: query.BinaryWhereClause{"span_name": {query.Eq: "GET /health"}}},
			},
			want: []string{` OR `, `NOT (`, `span_name:="GET /health"`},
		},
		{
			// A value is always quoted, so it cannot close the filter and append a pipe.
			name:  "hostile value stays inside the quotes",
			where: eq("workload_name", `x" | delete`),
			want:  []string{`service:="x\" | delete"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustTraceBase(t, tt.where, "")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("query %q is missing %q", got, w)
				}
			}
			for _, d := range tt.deny {
				if strings.Contains(got, d) {
					t.Errorf("query %q must not contain %q", got, d)
				}
			}
		})
	}
}

func TestCubeAPMTraceWhereRejectsUnsupportedOperator(t *testing.T) {
	_, err := cubeAPMTraceBaseQuery("", query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"duration_ns": {query.Gt: "10"}},
	}, "")
	if err == nil {
		t.Fatal("an operator the builder cannot render must fail, not be dropped silently")
	}
}

func TestCubeAPMTraceWhereRejectsUnsafeField(t *testing.T) {
	_, err := cubeAPMTraceBaseQuery("", eq("x | delete", "y"), "")
	if err == nil {
		t.Fatal("a field name carrying query syntax must be rejected")
	}
}

// Every operator advertised for traces must render, or the builder offers a filter
// that errors at query time.
func TestCubeAPMTraceSupportedOperatorsRender(t *testing.T) {
	s := &CubeAPMTraceSource{}
	for _, op := range s.GetSupportedOperators() {
		t.Run(op, func(t *testing.T) {
			_, err := cubeAPMTraceBaseQuery("", query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"span_name": {query.BinaryWhereClauseType(op): "POST /pay"}},
			}, "")
			if err != nil {
				t.Errorf("advertised operator %q does not render: %v", op, err)
			}
		})
	}
}

func TestBuildCubeAPMTraceListQuery(t *testing.T) {
	tests := []struct {
		name string
		req  TracesV3Request
		want string
	}{
		{
			name: "defaults to newest first, default page",
			req:  TracesV3Request{},
			want: `{event.domain="span"} | sort by (_time desc) | limit 100`,
		},
		{
			name: "duration ascending with offset",
			req: TracesV3Request{QueryRequest: TracesQueryBuilderRequest{
				Limit: 20, Offset: 40,
				OrderBy: []query.QueryOrderBy{{Column: "duration_ns", Order: query.Asc}},
			}},
			want: `{event.domain="span"} | sort by (duration asc) | offset 40 | limit 20`,
		},
		{
			name: "limit is clamped",
			req:  TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 50000}},
			want: `{event.domain="span"} | sort by (_time desc) | limit 1000`,
		},
		{
			name: "unknown sort column falls back to time",
			req: TracesV3Request{QueryRequest: TracesQueryBuilderRequest{
				OrderBy: []query.QueryOrderBy{{Column: "span_name", Order: query.Asc}},
			}},
			want: `{event.domain="span"} | sort by (_time asc) | limit 100`,
		},
		{
			// A Code-mode query is the user's; it is sent exactly as typed.
			name: "raw query passes through untouched",
			req:  TracesV3Request{Query: " service:=x | limit 3 "},
			want: `service:=x | limit 3`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildCubeAPMTraceListQuery(tt.req, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestBuildCubeAPMTraceGroupQuery(t *testing.T) {
	got, err := buildCubeAPMTraceGroupQuery(TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{Where: eq("workload_name", "checkout"), Limit: 10},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		`service:="checkout"`,
		`| stats by (service, span_name, _resource.k8s.namespace.name) count() count, `,
		`count() if (status_code:=ERROR) error_count`,
		`quantile(0.95, duration) p95`,
		`quantile(0.99, duration) p99`,
		`| sort by (count desc) | limit 10`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("group query %q is missing %q", got, want)
		}
	}
}

// cubeAPMSampleTraceRow is one span record as the LogsQL trace store returns it,
// trimmed from a live instance: flat fields, hex ids, a numeric-string nanosecond
// duration, and resource attributes under `_resource.`.
const cubeAPMSampleTraceRow = `{"_time":"2026-09-16T10:25:43.824673071Z","_stream_id":"0000000100","_stream":"{env=\"UNSET\",event.domain=\"span\",service=\"checkout\"}","_msg":"UNSET","_resource.k8s.namespace.name":"payments","_resource.k8s.pod.name":"checkout-7d9f-abcde","_resource.telemetry.sdk.language":"go","duration":"681975630","env":"UNSET","error":"true","event.domain":"span","http.request.method":"GET","http.response.status_code":"404","server.address":"api.example.com","service":"checkout","span_id":"194f0803a0b7b7a8","parent_id":"d511c08047db2e19","span_kind":"client","span_name":"HTTP GET","status_code":"ERROR","trace_id":"a6ae1df77ad4cf48711a3763d7c2317d","url.full":"https://api.example.com/v1/items"}`

func decodeTraceRows(t *testing.T, ndjson string) []map[string]any {
	t.Helper()
	rows, err := decodeCubeAPMNDJSON(strings.NewReader(ndjson))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rows
}

// service.go rewrites a where clause through GetLabelMapping() before calling the
// source, so alias labels arrive already mapped. The OR across alias fields must
// survive that step, not only the canonical spelling the other tests use.
func TestCubeAPMTraceAliasesSurviveUpstreamMapping(t *testing.T) {
	src := &CubeAPMTraceSource{}
	tests := []struct {
		label string
		want  []string
	}{
		{"workload_namespace", []string{`_resource.k8s.namespace.name:="payments"`, `_resource.service.namespace:="payments"`}},
		{"http_status_code", []string{`http.response.status_code:="payments"`, `http.status_code:="payments"`}},
		{"destination_workload_name", []string{`peer.service:="payments"`, `server.address:="payments"`}},
		{"resource", []string{`http.route:="payments"`, `url.path:="payments"`}},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			mapped := convertWhereClauseWithMApping(eq(tt.label, "payments"), src.GetLabelMapping())
			got := mustTraceBase(t, mapped, "")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("after upstream mapping, %q is missing %q", got, w)
				}
			}
		})
	}
}

// A Code-mode query that is already a pipeline (e.g. ends in its own `| limit`)
// cannot have stats appended: the count would be of that page, not the total.
func TestCubeAPMTraceRawPipelineCountsAndGroups(t *testing.T) {
	fake := newFakeCubeAPM(t, map[string]func(map[string]string) (int, string){
		cubeAPMTracesQueryPath: func(map[string]string) (int, string) { return http.StatusOK, `{"count":"3"}` + "\n" },
	})

	n, err := cubeAPMTraceCount(fake.cfg(), TracesV3Request{Query: "service:=x | limit 3"}, "count()")
	if err != nil || n != -1 {
		t.Errorf("pipeline count = %d, %v; want the -1 estimate", n, err)
	}
	if len(fake.forms) != 0 {
		t.Errorf("a pipeline count must not query CubeAPM, sent %v", fake.forms)
	}

	n, err = cubeAPMTraceCount(fake.cfg(), TracesV3Request{Query: "service:=x"}, "count()")
	if err != nil || n != 3 {
		t.Errorf("bare filter count = %d, %v; want 3", n, err)
	}
	if got := fake.forms[0]["query"]; got != "service:=x | stats count() count" {
		t.Errorf("bare filter query = %q", got)
	}

	if _, err := buildCubeAPMTraceGroupQuery(TracesV3Request{Query: "service:=x | limit 3"}, ""); err == nil {
		t.Error("grouping a pipeline query must fail rather than aggregate one page")
	}
	if _, err := buildCubeAPMTraceGroupQuery(TracesV3Request{Query: "service:=x"}, ""); err != nil {
		t.Errorf("grouping a bare filter: %v", err)
	}
}

func TestCubeAPMTraceRowToSpan(t *testing.T) {
	span := cubeAPMTraceRowToSpan(decodeTraceRows(t, cubeAPMSampleTraceRow)[0])

	checks := map[string][2]string{
		"TraceID":           {span.TraceID, "a6ae1df77ad4cf48711a3763d7c2317d"},
		"SpanID":            {span.SpanID, "194f0803a0b7b7a8"},
		"ParentSpanID":      {span.ParentSpanID, "d511c08047db2e19"},
		"SpanName":          {span.SpanName, "HTTP GET"},
		"SpanKind":          {span.SpanKind, "client"},
		"ServiceName":       {span.ServiceName, "checkout"},
		"WorkloadName":      {span.WorkloadName, "checkout"},
		"WorkloadNamespace": {span.WorkloadNamespace, "payments"},
		"HTTPStatusCode":    {span.HTTPStatusCode, "404"},
		"DestinationName":   {span.DestinationName, "api.example.com"},
		"Resource":          {span.Resource, "https://api.example.com/v1/items"},
		"StatusCode":        {span.StatusCode, "ERROR"},
		"Timestamp":         {span.Timestamp, "2026-09-16T10:25:43.824673071Z"},
		"TraceSource":       {span.TraceSource, "cubeapm"},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}

	if span.DurationNs != 681975630 {
		t.Errorf("DurationNs = %d, want 681975630 (the field is nanoseconds)", span.DurationNs)
	}
	if span.EndTime == "" {
		t.Error("EndTime should be derived from _time + duration")
	}

	// Resource attributes lose their prefix and gain service.name.
	if span.ResourceAttributes["k8s.pod.name"] != "checkout-7d9f-abcde" {
		t.Errorf("ResourceAttributes = %v", span.ResourceAttributes)
	}
	if span.ResourceAttributes["service.name"] != "checkout" {
		t.Error("service.name should be present in ResourceAttributes")
	}

	// Span attributes carry the free-form fields, not engine metadata or the
	// fields already promoted to columns.
	if span.SpanAttributes["http.request.method"] != "GET" {
		t.Errorf("SpanAttributes = %v", span.SpanAttributes)
	}
	for _, hidden := range []string{"_time", "_msg", "_stream", "trace_id", "service", "duration", "event.domain", "_resource.k8s.pod.name"} {
		if _, ok := span.SpanAttributes[hidden]; ok {
			t.Errorf("SpanAttributes must not carry %q", hidden)
		}
	}
}

func TestCubeAPMTraceRowStatusFallsBackToErrorFlag(t *testing.T) {
	span := cubeAPMTraceRowToSpan(map[string]any{"error": "true"})
	if span.StatusCode != "ERROR" {
		t.Errorf("StatusCode = %q, want ERROR from error=true", span.StatusCode)
	}
}

func TestCubeAPMTraceGroupRowToValues(t *testing.T) {
	rows := decodeTraceRows(t, `{"count":"1538","service":"checkout","span_name":"sql.query","_resource.k8s.namespace.name":"payments","error_count":"3","p95":"1768549","p99":"2500000.5","max_duration":"10392193","total_duration":"1185111982"}`)
	g := cubeAPMTraceGroupRowToValues(rows[0])

	if g.Count != 1538 || g.ErrorCount != 3 {
		t.Errorf("Count/ErrorCount = %d/%d", g.Count, g.ErrorCount)
	}
	if g.P95Latency != 1768549 || g.P99Latency != 2500000 || g.MaxLatency != 10392193 {
		t.Errorf("latencies = %d/%d/%d", g.P95Latency, g.P99Latency, g.MaxLatency)
	}
	if g.WorkloadName != "checkout" || g.WorkloadNamespace != "payments" || g.SpanName != "sql.query" {
		t.Errorf("dimensions = %q/%q/%q", g.WorkloadName, g.WorkloadNamespace, g.SpanName)
	}
	if g.DurationNS != 1185111982 {
		t.Errorf("DurationNS = %d", g.DurationNS)
	}
}

// fakeCubeAPM serves the LogsQL trace endpoints from a handler per path and records
// every request form it saw.
type fakeCubeAPM struct {
	server *httptest.Server
	forms  []map[string]string
}

func newFakeCubeAPM(t *testing.T, handlers map[string]func(form map[string]string) (int, string)) *fakeCubeAPM {
	t.Helper()
	f := &fakeCubeAPM{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		form := map[string]string{"path": r.URL.Path}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		f.forms = append(f.forms, form)

		handler, ok := handlers[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "unsupported path requested: %q", r.URL.Path)
			return
		}
		status, body := handler(form)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCubeAPM) cfg() integrations.CubeAPMConfig {
	return integrations.CubeAPMConfig{URL: f.server.URL}
}

func TestQueryCubeAPMTracesAgainstServer(t *testing.T) {
	fake := newFakeCubeAPM(t, map[string]func(map[string]string) (int, string){
		cubeAPMTracesQueryPath: func(map[string]string) (int, string) {
			return http.StatusOK, cubeAPMSampleTraceRow + "\n" + strings.Replace(cubeAPMSampleTraceRow, `"span_id":"194f0803a0b7b7a8"`, `"span_id":"0000000000000002"`, 1) + "\n"
		},
	})

	req := TracesV3Request{StartTime: 1_789_552_581_856, EndTime: 1_789_553_481_856,
		QueryRequest: TracesQueryBuilderRequest{Limit: 5}}
	logsQL, err := buildCubeAPMTraceListQuery(req, "")
	if err != nil {
		t.Fatal(err)
	}

	spans, err := queryCubeAPMTraces(fake.cfg(), logsQL, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spans) != 2 || spans[1].SpanID != "0000000000000002" {
		t.Fatalf("got %d spans: %+v", len(spans), spans)
	}

	form := fake.forms[0]
	if form["query"] != logsQL {
		t.Errorf("sent query %q, want %q", form["query"], logsQL)
	}
	// start/end are sent in seconds.
	if form["start"] != "1789552581" || form["end"] != "1789553481" {
		t.Errorf("window = %s..%s", form["start"], form["end"])
	}
	if form["limit"] != "5" {
		t.Errorf("limit = %q", form["limit"])
	}
}

func TestCubeAPMTraceCountAgainstServer(t *testing.T) {
	fake := newFakeCubeAPM(t, map[string]func(map[string]string) (int, string){
		cubeAPMTracesQueryPath: func(map[string]string) (int, string) {
			return http.StatusOK, `{"count":"5071"}` + "\n"
		},
	})

	n, err := cubeAPMTraceCount(fake.cfg(), TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{Where: eq("workload_name", "checkout")},
	}, "count_uniq(trace_id)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5071 {
		t.Errorf("count = %d, want 5071", n)
	}
	want := `{event.domain="span"} (service:="checkout") | stats count_uniq(trace_id) count`
	if got := fake.forms[0]["query"]; got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
}

func TestCubeAPMTraceLabelValuesUnionsAliasFields(t *testing.T) {
	fake := newFakeCubeAPM(t, map[string]func(map[string]string) (int, string){
		cubeAPMTracesFieldValuesPath: func(form map[string]string) (int, string) {
			switch form["field"] {
			case "_resource.k8s.namespace.name":
				return http.StatusOK, `{"values":[{"value":"payments","hits":9},{"value":"demo","hits":3}]}`
			case "_resource.service.namespace":
				return http.StatusOK, `{"values":[{"value":"demo","hits":1},{"value":"","hits":1},{"value":"shop","hits":1}]}`
			}
			return http.StatusOK, `{"values":[]}`
		},
	})

	values, err := queryCubeAPMTraceLabelValues(fake.cfg(), TracesV3LabelValuesRequest{Label: "workload_namespace"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(values, ",") != "demo,payments,shop" {
		t.Errorf("values = %v, want the sorted, de-duplicated union without blanks", values)
	}
	if len(fake.forms) != 2 {
		t.Errorf("made %d requests, want one per backing field", len(fake.forms))
	}
}

func TestCubeAPMTraceLabelsHideEngineFields(t *testing.T) {
	fake := newFakeCubeAPM(t, map[string]func(map[string]string) (int, string){
		cubeAPMTracesFieldNamesPath: func(map[string]string) (int, string) {
			return http.StatusOK, `{"values":[{"value":"_time"},{"value":"service"},{"value":"_stream"},{"value":"_resource.k8s.pod.name"},{"value":"event.domain"}]}`
		},
	})

	labels, err := queryCubeAPMTraceLabels(fake.cfg(), FetchTraceLabelRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var names []string
	for _, l := range labels {
		names = append(names, l.Label)
	}
	if strings.Join(names, ",") != "_resource.k8s.pod.name,service" {
		t.Errorf("labels = %v", names)
	}
}

// An instance without the LogsQL trace API must fail loudly. The alternative — an
// empty result — reads as "this service has no traces".
func TestCubeAPMTraceMissingEndpointIsReported(t *testing.T) {
	fake := newFakeCubeAPM(t, nil)

	_, err := queryCubeAPMTraces(fake.cfg(), `{event.domain="span"}`, TracesV3Request{})
	if err == nil {
		t.Fatal("expected an error when the endpoint is missing")
	}
	if !strings.Contains(err.Error(), "does not serve the trace query API") {
		t.Errorf("error = %v, want it to name the missing API", err)
	}
}

func TestIsCubeAPMMissingTraceEndpoint(t *testing.T) {
	if !isCubeAPMMissingTraceEndpoint(fmt.Errorf(`CubeAPM returned HTTP 400: unsupported path requested: "/select/logsql/query"`)) {
		t.Error("CubeAPM's unknown-path answer should be recognised")
	}
	if !isCubeAPMMissingTraceEndpoint(fmt.Errorf("CubeAPM returned HTTP 404: not found")) {
		t.Error("a proxy 404 should be recognised")
	}
	if isCubeAPMMissingTraceEndpoint(fmt.Errorf("CubeAPM returned HTTP 400: cannot parse query")) {
		t.Error("a query parse error is not a missing endpoint")
	}
	if isCubeAPMMissingTraceEndpoint(nil) {
		t.Error("nil is not a missing endpoint")
	}
}

// ---------------------------------------------------------------------------
// Trace waterfall: by-id endpoint, Jaeger protobuf-JSON
// ---------------------------------------------------------------------------

// cubeAPMSampleTraceFetch is the by-id trace response from CubeAPM's HTTP API
// reference: Jaeger protobuf-JSON, with base64 ids, typed tag values and a bare
// nanosecond duration.
const cubeAPMSampleTraceFetch = `{
  "spans": [
    {
      "trace_id": "V5OPQPPw/3hScwih8m9RBg==",
      "span_id": "2d4bjpT7FaA=",
      "operation_name": "POST /v1/payment",
      "references": [
        {"trace_id": "V5OPQPPw/3hScwih8m9RBg==", "span_id": "901CCWrDT6M="}
      ],
      "start_time": "2025-10-29T03:55:04.90625352Z",
      "duration": 40965161,
      "tags": [
        {"key": "http.status_code", "v_type": 2, "v_int64": 200},
        {"key": "http.route", "v_str": "/v1/payment"},
        {"key": "span.kind", "v_str": "server"},
        {"key": "http.method", "v_str": "POST"}
      ],
      "logs": null,
      "process": {
        "service_name": "notify-service",
        "tags": [
          {"key": "telemetry.sdk.language", "v_str": "java"},
          {"key": "k8s.namespace.name", "v_str": "payments"}
        ]
      }
    }
  ]
}`

func decodeSampleSpans(t *testing.T) []common.OpenTelemetryTrace {
	t.Helper()
	var fetched cubeAPMTraceFetch
	dec := json.NewDecoder(strings.NewReader(cubeAPMSampleTraceFetch))
	dec.UseNumber()
	if err := dec.Decode(&fetched); err != nil {
		t.Fatalf("failed to decode sample response: %v", err)
	}

	var spans []common.OpenTelemetryTrace
	for _, s := range fetched.Spans {
		spans = append(spans, cubeAPMSpanToTrace(s))
	}
	return spans
}

func TestCubeAPMSpanToTrace(t *testing.T) {
	spans := decodeSampleSpans(t)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	span := spans[0]

	// Ids arrive base64-encoded; every other provider here reports lowercase hex.
	// Expected values are the base64 payloads decoded independently:
	//   V5OPQPPw/3hScwih8m9RBg== -> 57938f40f3f0ff78527308a1f26f5106 (16 bytes)
	//   2d4bjpT7FaA=             -> d9de1b8e94fb15a0                 (8 bytes)
	//   901CCWrDT6M=             -> f74d42096ac34fa3                 (8 bytes)
	if span.TraceID != "57938f40f3f0ff78527308a1f26f5106" {
		t.Errorf("TraceID = %q, want the base64 id decoded to hex", span.TraceID)
	}
	if span.SpanID != "d9de1b8e94fb15a0" {
		t.Errorf("SpanID = %q", span.SpanID)
	}
	// The parent comes from references[0], which is what makes the waterfall nest.
	if span.ParentSpanID != "f74d42096ac34fa3" {
		t.Errorf("ParentSpanID = %q", span.ParentSpanID)
	}

	if span.SpanName != "POST /v1/payment" {
		t.Errorf("SpanName = %q", span.SpanName)
	}
	if span.ServiceName != "notify-service" {
		t.Errorf("ServiceName = %q", span.ServiceName)
	}
	if span.DurationNs != 40965161 {
		t.Errorf("DurationNs = %d, want 40965161 (the field is nanoseconds)", span.DurationNs)
	}
	if span.SpanKind != "server" {
		t.Errorf("SpanKind = %q", span.SpanKind)
	}
	// A typed int tag must render as its number, not as an empty string.
	if span.HTTPStatusCode != "200" {
		t.Errorf("HTTPStatusCode = %q, want 200", span.HTTPStatusCode)
	}
	if span.Resource != "/v1/payment" {
		t.Errorf("Resource = %q, want the http.route", span.Resource)
	}
	// Process tags are the span's resource attributes.
	if span.ResourceAttributes["k8s.namespace.name"] != "payments" {
		t.Errorf("ResourceAttributes = %v", span.ResourceAttributes)
	}
	if span.WorkloadNamespace != "payments" {
		t.Errorf("WorkloadNamespace = %q", span.WorkloadNamespace)
	}
	if span.ResourceAttributes["service.name"] != "notify-service" {
		t.Error("service.name should be present in ResourceAttributes")
	}
	if span.EndTime == "" {
		t.Error("EndTime should be derived from start_time + duration")
	}
}

func TestCubeAPMDecodeID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"base64 span id", "2d4bjpT7FaA=", "d9de1b8e94fb15a0"},
		{"already hex passes through", "d9de1b8e94fb15a0", "d9de1b8e94fb15a0"},
		{"uppercase hex is lowercased", "D9DE1B8E94FB15A0", "d9de1b8e94fb15a0"},
		{"empty", "", ""},
		// An undecodable value is returned as-is rather than becoming an empty id,
		// so a trace stays addressable even if the encoding changes.
		{"garbage passes through", "!!!not-base64!!!", "!!!not-base64!!!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMDecodeID(tt.in); got != tt.want {
				t.Errorf("cubeAPMDecodeID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsHexID(t *testing.T) {
	if !isHexID("d9de1b8e94fb15a0") {
		t.Error("16-char hex should be recognized")
	}
	if !isHexID(strings.Repeat("a", 32)) {
		t.Error("32-char hex should be recognized")
	}
	// "2d4bjpT7FaA=" is 12 chars — not an id length — and contains non-hex bytes.
	if isHexID("2d4bjpT7FaA=") {
		t.Error("base64 must not be mistaken for hex")
	}
	if isHexID("zzzzzzzzzzzzzzzz") {
		t.Error("non-hex characters should be rejected")
	}
}

func TestCubeAPMDurationNanos(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"bare nanoseconds", `40965161`, 40965161},
		{"float", `40965161.7`, 40965161},
		// protobuf's canonical JSON encoding for a Duration is a suffixed string.
		{"protobuf seconds string", `"0.040965161s"`, 40965161},
		{"milliseconds string", `"40ms"`, 40000000},
		{"microseconds string", `"40us"`, 40000},
		{"nanoseconds string", `"40ns"`, 40},
		{"numeric string", `"40965161"`, 40965161},
		{"empty string", `""`, 0},
		{"null", `null`, 0},
		{"garbage", `"abc"`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMDurationNanos(json.RawMessage(tt.in)); got != tt.want {
				t.Errorf("cubeAPMDurationNanos(%s) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}

	t.Run("absent field", func(t *testing.T) {
		if got := cubeAPMDurationNanos(nil); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

func TestCubeAPMTagString(t *testing.T) {
	tests := []struct {
		name string
		tag  cubeAPMTag
		want string
	}{
		{"string", cubeAPMTag{VStr: "server"}, "server"},
		{"int64", cubeAPMTag{VType: 2, VInt64: json.Number("200")}, "200"},
		{"float", cubeAPMTag{VType: 3, VFloat: json.Number("1.5")}, "1.5"},
		{"bool true", cubeAPMTag{VType: 1, VBool: true}, "true"},
		// protobuf-JSON omits zero values, so a false bool is identified only by
		// its type tag — without this it would render as an absent tag.
		{"bool false", cubeAPMTag{VType: 1}, "false"},
		{"empty string tag", cubeAPMTag{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tag.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCubeAPMStatusCode(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]string
		want  string
	}{
		{"otel status", map[string]string{"otel.status_code": "error"}, "ERROR"},
		// error=true is the Jaeger convention and outlives the OTel status on
		// spans exported through a Jaeger-compatible path.
		{"jaeger error flag", map[string]string{"error": "true"}, "ERROR"},
		{"status.code fallback", map[string]string{"status.code": "ok"}, "OK"},
		{"none", map[string]string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMStatusCode(tt.attrs); got != tt.want {
				t.Errorf("cubeAPMStatusCode(%v) = %q, want %q", tt.attrs, got, tt.want)
			}
		})
	}
}

func TestCubeAPMEndTime(t *testing.T) {
	got := cubeAPMEndTime("2025-10-29T03:55:04.000000000Z", int64(time.Second))
	if !strings.HasPrefix(got, "2025-10-29T03:55:05") {
		t.Errorf("EndTime = %q, want start + 1s", got)
	}

	// An unparseable start must leave the field empty rather than invent an
	// epoch-anchored end.
	if got := cubeAPMEndTime("not-a-time", 100); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := cubeAPMEndTime("2025-10-29T03:55:04Z", 0); got != "" {
		t.Errorf("got %q, want empty for a zero duration", got)
	}
}

func TestSortCubeAPMSpans(t *testing.T) {
	newSpans := func() []common.OpenTelemetryTrace {
		return []common.OpenTelemetryTrace{
			{SpanName: "a", Timestamp: "2026-09-04T01:00:01Z", DurationNs: 100},
			{SpanName: "b", Timestamp: "2026-09-04T01:00:03Z", DurationNs: 300},
			{SpanName: "c", Timestamp: "2026-09-04T01:00:02Z", DurationNs: 200},
		}
	}

	t.Run("defaults to newest first", func(t *testing.T) {
		spans := newSpans()
		sortCubeAPMSpans(spans, nil)
		if spans[0].SpanName != "b" || spans[2].SpanName != "a" {
			t.Errorf("order = %s %s %s, want b c a", spans[0].SpanName, spans[1].SpanName, spans[2].SpanName)
		}
	})

	t.Run("timestamp ascending", func(t *testing.T) {
		spans := newSpans()
		sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "timestamp", Order: query.Asc}})
		if spans[0].SpanName != "a" || spans[2].SpanName != "b" {
			t.Errorf("order = %s %s %s, want a c b", spans[0].SpanName, spans[1].SpanName, spans[2].SpanName)
		}
	})

	t.Run("duration descending", func(t *testing.T) {
		spans := newSpans()
		sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "duration_ns", Order: query.Desc}})
		if spans[0].DurationNs != 300 || spans[2].DurationNs != 100 {
			t.Errorf("durations = %d %d %d", spans[0].DurationNs, spans[1].DurationNs, spans[2].DurationNs)
		}
	})
}

func TestCubeAPMHeatmapWindowSeconds(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)

	t.Run("pads a supplied window", func(t *testing.T) {
		start, end := cubeAPMHeatmapWindowSeconds(1_700_000_000_000, 1_700_000_060_000, now)
		// A trace's earliest span can begin before the row's timestamp and its
		// latest can end after; clipping either truncates the waterfall.
		if start != 1_700_000_000-300 {
			t.Errorf("start = %d, want the window start minus 5m", start)
		}
		if end != 1_700_000_060+300 {
			t.Errorf("end = %d, want the window end plus 5m", end)
		}
	})

	// The trace-detail view asks by trace_id alone; an hour-long default silently
	// returns nothing for any older trace.
	t.Run("falls back to a 30-day lookback", func(t *testing.T) {
		start, end := cubeAPMHeatmapWindowSeconds(0, 0, now)
		if end != now.Unix() {
			t.Errorf("end = %d, want now", end)
		}
		if end-start != int64((30 * 24 * time.Hour).Seconds()) {
			t.Errorf("lookback = %ds, want 30 days", end-start)
		}
	})
}

func TestCubeAPMTracesToHeatmap(t *testing.T) {
	spans := decodeSampleSpans(t)
	heatmap := cubeAPMTracesToHeatmap(spans)

	if len(heatmap) != 1 {
		t.Fatalf("got %d entries, want 1", len(heatmap))
	}
	h := heatmap[0]
	if h.DurationNs != 40965161 {
		t.Errorf("DurationNs = %d", h.DurationNs)
	}
	if h.ServiceName != "notify-service" {
		t.Errorf("ServiceName = %q", h.ServiceName)
	}
	if h.SpanName != "POST /v1/payment" {
		t.Errorf("SpanName = %q", h.SpanName)
	}
	if h.TraceID == "" || h.SpanID == "" {
		t.Error("heatmap entries must carry ids so the waterfall can link spans")
	}
}

func TestCubeAPMTraceSourceContract(t *testing.T) {
	var s any = &CubeAPMTraceSource{}
	if _, ok := s.(TraceSource); !ok {
		t.Error("CubeAPMTraceSource must implement TraceSource")
	}
}

func TestCubeAPMTraceSourceRoutedFromDispatcher(t *testing.T) {
	src, err := getTraceSource("cubeapm", "user")
	if err != nil {
		t.Fatalf("getTraceSource(cubeapm, user) failed: %v", err)
	}
	if _, ok := src.(*CubeAPMTraceSource); !ok {
		t.Errorf("got %T, want *CubeAPMTraceSource", src)
	}
}

func TestSortCubeAPMSpansHandlesFractionalSecondWidths(t *testing.T) {
	spans := []common.OpenTelemetryTrace{
		{SpanName: "later", Timestamp: "2025-10-29T03:55:04.12Z"},
		{SpanName: "earlier", Timestamp: "2025-10-29T03:55:04.1Z"},
	}

	// Guard the premise: a raw string compare really does get this wrong, i.e.
	// the EARLIER span's timestamp sorts as greater than the later one's.
	if spans[1].Timestamp <= spans[0].Timestamp {
		t.Fatal("premise no longer holds; the fixture no longer exercises the bug")
	}

	sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "timestamp", Order: query.Asc}})
	if spans[0].SpanName != "earlier" {
		t.Errorf("ascending order = %s then %s, want earlier first",
			spans[0].SpanName, spans[1].SpanName)
	}

	sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "timestamp", Order: query.Desc}})
	if spans[0].SpanName != "later" {
		t.Errorf("descending order = %s then %s, want later first",
			spans[0].SpanName, spans[1].SpanName)
	}
}

func TestCubeAPMSpanStartNanos(t *testing.T) {
	got := cubeAPMSpanStartNanos(common.OpenTelemetryTrace{Timestamp: "2025-10-29T03:55:04.1Z"})
	want := cubeAPMSpanStartNanos(common.OpenTelemetryTrace{Timestamp: "2025-10-29T03:55:04.100Z"})
	if got != want {
		t.Errorf(".1Z and .100Z are the same instant but parsed to %d and %d", got, want)
	}

	// Falls back to StartTime when Timestamp is absent.
	if cubeAPMSpanStartNanos(common.OpenTelemetryTrace{StartTime: "2025-10-29T03:55:04Z"}) == 0 {
		t.Error("StartTime fallback did not parse")
	}

	// A malformed start sorts as 0 rather than failing the whole query.
	if cubeAPMSpanStartNanos(common.OpenTelemetryTrace{Timestamp: "not-a-time"}) != 0 {
		t.Error("an unparseable timestamp should sort as 0")
	}
}

// The search API answers 400 {"error":"invalid limit"} above 100 — verified by
// bisection against a live instance (100 -> 200, 101 -> 400). This is a server
// constraint, not a policy choice, and exceeding it fails the request outright
// rather than degrading it, so it needs a guard CI can see: the live test that

func TestCubeAPMCount(t *testing.T) {
	tests := []struct {
		in   any
		want int
	}{
		{"5071", 5071},
		{"-3", 0},
		{"99999999999", math.MaxInt32},
		{"12.9", 12},
		{nil, 0},
	}
	for _, tt := range tests {
		if got := cubeAPMCount(tt.in); got != tt.want {
			t.Errorf("cubeAPMCount(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
