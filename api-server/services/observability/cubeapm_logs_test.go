package observability

import (
	"strings"
	"testing"
	"time"

	"nudgebee/services/query"
)

func TestCubeAPMQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "prod", `"prod"`},
		{"spaces", "my value", `"my value"`},
		{"embedded quote", `say "hi"`, `"say \"hi\""`},
		{"backslash", `a\b`, `"a\\b"`},
		{"newline", "a\nb", `"a\nb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"empty", "", `""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMQuote(tt.in); got != tt.want {
				t.Errorf("cubeAPMQuote(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

// A filter value is the untrusted half of a query. Without quoting, a value
// carrying a pipe would be parsed as further LogsQL and could append its own
// pipeline stage to the query.
func TestCubeAPMQuoteContainsInjectionAttempt(t *testing.T) {
	quoted := cubeAPMQuote(`x" | drop _msg | limit 1 "`)
	if strings.Count(quoted, `"`) != 2+strings.Count(quoted, `\"`) {
		t.Errorf("unescaped quote escaped the literal: %s", quoted)
	}
	if !strings.HasPrefix(quoted, `"`) || !strings.HasSuffix(quoted, `"`) {
		t.Errorf("value is not fully enclosed: %s", quoted)
	}
}

func TestIsSafeCubeAPMField(t *testing.T) {
	valid := []string{"_msg", "_time", "log.level", "k8s.namespace.name", "service_name", "a-b", "http:status", "a/b"}
	for _, f := range valid {
		if !isSafeCubeAPMField(f) {
			t.Errorf("isSafeCubeAPMField(%q) = false, want true", f)
		}
	}

	invalid := []string{"", "a b", `a"b`, "a|b", "a{b", "a*b", "a,b", "a(b", strings.Repeat("a", 256)}
	for _, f := range invalid {
		if isSafeCubeAPMField(f) {
			t.Errorf("isSafeCubeAPMField(%q) = true, want false", f)
		}
	}
}

func TestBuildCubeAPMConditions(t *testing.T) {
	tests := []struct {
		name  string
		where query.QueryWhereClause
		want  string
	}{
		{
			name:  "empty",
			where: query.QueryWhereClause{},
			want:  "",
		},
		{
			name: "eq is mapped to the CubeAPM field",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"namespace": {query.Eq: "payments"},
			}},
			want: `k8s.namespace.name:="payments"`,
		},
		{
			name: "neq negates",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"level": {query.Nq: "debug"},
			}},
			want: `NOT log.level:="debug"`,
		},
		{
			name: "contains becomes a substring match",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"message": {query.Contains: "timeout"},
			}},
			want: `_msg:"*timeout*"`,
		},
		{
			name: "regex",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"level": {query.Regex: "error|warn"},
			}},
			want: `log.level:~"error|warn"`,
		},
		{
			name: "unmapped field passes through verbatim",
			where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"trace.id": {query.Eq: "abc"},
			}},
			want: `trace.id:="abc"`,
		},
		{
			name: "or group",
			where: query.QueryWhereClause{Or: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"level": {query.Eq: "error"}}},
				{Binary: query.BinaryWhereClause{"level": {query.Eq: "fatal"}}},
			}},
			want: `((log.level:="error") OR (log.level:="fatal"))`,
		},
		{
			name: "not wraps",
			where: query.QueryWhereClause{Not: &query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"namespace": {query.Eq: "kube-system"}},
			}},
			want: `NOT (k8s.namespace.name:="kube-system")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildCubeAPMConditions(tt.where, cubeAPMLogLabelMapping)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

// Multiple filters on the same clause are rendered in a stable order so the
// generated query can be compared, cached and read in a log line.
func TestBuildCubeAPMConditionsIsDeterministic(t *testing.T) {
	where := query.QueryWhereClause{Binary: query.BinaryWhereClause{
		"namespace": {query.Eq: "payments"},
		"pod":       {query.Eq: "checkout-1"},
		"container": {query.Eq: "app"},
	}}

	first, err := buildCubeAPMConditions(where, cubeAPMLogLabelMapping)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := buildCubeAPMConditions(where, cubeAPMLogLabelMapping)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != first {
			t.Fatalf("condition order is not stable:\n  %s\n  %s", first, got)
		}
	}
}

func TestBuildCubeAPMConditionsRejectsUnsafeField(t *testing.T) {
	_, err := buildCubeAPMConditions(query.QueryWhereClause{Binary: query.BinaryWhereClause{
		`evil" | drop _msg | x "`: {query.Eq: "1"},
	}}, cubeAPMLogLabelMapping)
	if err == nil {
		t.Fatal("expected an error for an unsafe field name")
	}
	if !strings.Contains(err.Error(), "unsafe field name") {
		t.Errorf("error = %v", err)
	}
}

func TestBuildCubeAPMConditionsRejectsUnsupportedOperator(t *testing.T) {
	_, err := buildCubeAPMConditions(query.QueryWhereClause{Binary: query.BinaryWhereClause{
		"namespace": {query.Between: "a"},
	}}, cubeAPMLogLabelMapping)
	if err == nil {
		t.Fatal("expected an error for an unsupported operator")
	}
}

func TestCubeAPMBaseQuery(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		conditions string
		want       string
	}{
		{"neither", "", "", "*"},
		{"env only", "prod", "", `{env="prod"}`},
		{"conditions only", "", `log.level:="error"`, `log.level:="error"`},
		{"both", "prod", `log.level:="error"`, `{env="prod"} log.level:="error"`},
		{"env value is quoted", `a"b`, "", `{env="a\"b"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMBaseQuery(tt.env, tt.conditions); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestCubeAPMLogLimit(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, cubeAPMDefaultLogLimit},
		{-5, cubeAPMDefaultLogLimit},
		{50, 50},
		{cubeAPMMaxLogLimit + 1, cubeAPMMaxLogLimit},
	}
	for _, tt := range tests {
		if got := cubeAPMLogLimit(tt.in); got != tt.want {
			t.Errorf("cubeAPMLogLimit(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestCubeAPMBuildLogsQL(t *testing.T) {
	s := &CubeAPMLogSource{}

	t.Run("builder mode", func(t *testing.T) {
		got, err := s.buildLogsQL(FetchLogRequest{
			Limit: 25,
			QueryRequest: LogsQueryBuilderRequest{Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"namespace": {query.Eq: "payments"}},
			}},
		}, "prod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `{env="prod"} k8s.namespace.name:="payments" | sort ("_time" desc) | limit 25`
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})

	// Code mode hands the user's own LogsQL to the server untouched; rewriting it
	// would fight the user, who may already have written their own env selector.
	t.Run("raw query passes through", func(t *testing.T) {
		raw := `{env="staging"} error | stats count() as c`
		got, err := s.buildLogsQL(FetchLogRequest{Query: "  " + raw + "  ", Limit: 10}, "prod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != raw {
			t.Errorf("got %s, want the raw query unchanged (%s)", got, raw)
		}
	})

	t.Run("no filters and no env", func(t *testing.T) {
		got, err := s.buildLogsQL(FetchLogRequest{}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `* | sort ("_time" desc) | limit 100`
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
}

func TestDecodeCubeAPMNDJSON(t *testing.T) {
	t.Run("parses each line", func(t *testing.T) {
		body := `{"_time":"2026-09-04T01:00:00Z","_msg":"first"}
{"_time":"2026-09-04T01:00:01Z","_msg":"second"}
`
		rows, err := decodeCubeAPMNDJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2", len(rows))
		}
		if rows[1]["_msg"] != "second" {
			t.Errorf("row[1]._msg = %v", rows[1]["_msg"])
		}
	})

	// A single unparseable record should not blank out an otherwise good page.
	t.Run("skips malformed lines", func(t *testing.T) {
		body := "{\"_msg\":\"ok\"}\nnot json\n\n{\"_msg\":\"also ok\"}\n"
		rows, err := decodeCubeAPMNDJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2", len(rows))
		}
	})

	// A log line carrying a stack trace routinely exceeds bufio's default 64KB
	// token cap, which would silently truncate the response mid-stream.
	t.Run("handles lines above the default scanner cap", func(t *testing.T) {
		big := strings.Repeat("x", 200_000)
		body := `{"_msg":"` + big + `"}` + "\n"
		rows, err := decodeCubeAPMNDJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if len(rows[0]["_msg"].(string)) != len(big) {
			t.Errorf("message was truncated to %d bytes", len(rows[0]["_msg"].(string)))
		}
	})

	t.Run("empty body", func(t *testing.T) {
		rows, err := decodeCubeAPMNDJSON(strings.NewReader(""))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})
}

// Large integers must survive decoding intact; a float64 round-trip silently
// discards the low digits of a nanosecond timestamp.
func TestDecodeCubeAPMNDJSONPreservesLargeIntegers(t *testing.T) {
	rows, err := decodeCubeAPMNDJSON(strings.NewReader(`{"ts":1786612792471605123}` + "\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cubeAPMString(rows[0]["ts"]); got != "1786612792471605123" {
		t.Errorf("ts = %s, want 1786612792471605123", got)
	}
}

func TestParseCubeAPMStreamLabels(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "simple",
			in:   `{env="prod",service.name="checkout"}`,
			want: map[string]string{"env": "prod", "service.name": "checkout"},
		},
		{
			name: "value containing a comma",
			in:   `{tags="a,b",env="prod"}`,
			want: map[string]string{"tags": "a,b", "env": "prod"},
		},
		{
			name: "escaped quote in value",
			in:   `{msg="say \"hi\"",env="prod"}`,
			want: map[string]string{"msg": `say "hi"`, "env": "prod"},
		},
		{
			name: "empty",
			in:   "",
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCubeAPMStreamLabels(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("labels[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestCubeAPMRowToOutputLog(t *testing.T) {
	row := map[string]any{
		"_time":              "2026-09-04T01:00:00.123456Z",
		"_msg":               "upstream timeout",
		"log.level":          "error",
		"k8s.namespace.name": "payments",
		"_stream":            `{env="prod",service.name="checkout"}`,
	}

	out := cubeAPMRowToOutputLog(row)

	if out.Timestamp != "2026-09-04T01:00:00.123456Z" {
		t.Errorf("Timestamp = %q; CubeAPM's _time is already ISO 8601 and should pass through", out.Timestamp)
	}
	if out.Message != "upstream timeout" {
		t.Errorf("Message = %q", out.Message)
	}
	if out.Severity != "error" {
		t.Errorf("Severity = %q", out.Severity)
	}
	// Promoted fields stay in Labels too — the log table lets users column on any
	// of them, and dropping one here makes it unselectable there.
	if out.Labels["_msg"] != "upstream timeout" {
		t.Error("_msg was dropped from Labels")
	}
	// The stream selector is expanded so its fields are individually filterable.
	if out.Labels["env"] != "prod" || out.Labels["service.name"] != "checkout" {
		t.Errorf("stream labels not expanded: %v", out.Labels)
	}
}

func TestCubeAPMRowToOutputLogFallbackFields(t *testing.T) {
	// An ingestion pipeline configured with a different _msg_field leaves the
	// original key in place; the message must still resolve.
	out := cubeAPMRowToOutputLog(map[string]any{
		"body":     "fallback message",
		"severity": "WARN",
	})
	if out.Message != "fallback message" {
		t.Errorf("Message = %q, want the body fallback", out.Message)
	}
	if out.Severity != "WARN" {
		t.Errorf("Severity = %q, want the severity fallback", out.Severity)
	}
}

func TestCubeAPMTimeRangeMillis(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)

	t.Run("passes through a supplied window", func(t *testing.T) {
		start, end := cubeAPMTimeRangeMillis(1000, 2000, now)
		if start != 1000 || end != 2000 {
			t.Errorf("got (%d, %d), want (1000, 2000)", start, end)
		}
	})

	t.Run("defaults to the last hour", func(t *testing.T) {
		start, end := cubeAPMTimeRangeMillis(0, 0, now)
		if end != now.UnixMilli() {
			t.Errorf("end = %d, want %d", end, now.UnixMilli())
		}
		if end-start != time.Hour.Milliseconds() {
			t.Errorf("window = %dms, want 1h", end-start)
		}
	})
}

func TestBuildCubeAPMLogGroupQuery(t *testing.T) {
	t.Run("includes severity, exclusions and the pipeline", func(t *testing.T) {
		got := buildCubeAPMLogGroupQuery("prod", "", "", 50)

		for _, want := range []string{
			`{env="prod"}`,
			`_msg:*`,
			`log.level:~"(?i)^(error|err|critical|crit|fatal|emergency|alert|panic|severe)$"`,
			`NOT k8s.container.name:="istio-proxy"`,
			`| stats by (_msg, k8s.namespace.name, k8s.pod.name, k8s.deployment.name, k8s.container.name, log.level) count() as cube_count`,
			`| sort ("cube_count" desc)`,
			`| limit 50`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("query missing %q\ngot: %s", want, got)
			}
		}
	})

	// Pods are named {workload}-{replica-suffix}, so a workload filter has to be a
	// pod prefix — an equality on a deployment field misses StatefulSets and Jobs.
	t.Run("workload filter is a pod prefix", func(t *testing.T) {
		got := buildCubeAPMLogGroupQuery("", "payments", "checkout-api", 0)
		if !strings.Contains(got, `k8s.pod.name:"checkout-api-*"`) {
			t.Errorf("query missing pod prefix filter\ngot: %s", got)
		}
		if !strings.Contains(got, `k8s.namespace.name:="payments"`) {
			t.Errorf("query missing namespace filter\ngot: %s", got)
		}
	})

	t.Run("zero limit falls back to the default", func(t *testing.T) {
		if !strings.Contains(buildCubeAPMLogGroupQuery("", "", "", 0), "| limit 100") {
			t.Error("expected the default log-group limit")
		}
	})
}

func TestConvertCubeAPMLogGroups(t *testing.T) {
	rows := []map[string]any{
		{
			"_msg":                "upstream timeout",
			"k8s.namespace.name":  "payments",
			"k8s.pod.name":        "checkout-api-7d9f-abc",
			"k8s.deployment.name": "checkout-api",
			"k8s.container.name":  "app",
			"log.level":           "error",
			// stats counts arrive as strings in the NDJSON body.
			"cube_count": "42",
		},
		// No message: nothing to group on or display.
		{"cube_count": "5"},
		// Unparseable count.
		{"_msg": "x", "cube_count": "not-a-number"},
		// Zero count.
		{"_msg": "y", "cube_count": "0"},
	}

	out := convertCubeAPMLogGroups(rows, 1788489360)

	if len(out.Groups) != 1 {
		t.Fatalf("got %d groups, want 1 (rows without a message or a usable count are dropped)", len(out.Groups))
	}
	g := out.Groups[0]

	if g.Count != 42 {
		t.Errorf("Count = %d, want 42", g.Count)
	}
	if g.Sample != "upstream timeout" {
		t.Errorf("Sample = %q", g.Sample)
	}
	if g.Workload != "checkout-api" {
		t.Errorf("Workload = %q", g.Workload)
	}
	if g.ContainerID != "/k8s/payments/checkout-api/app" {
		t.Errorf("ContainerID = %q; the UI parses namespace and workload back out of this field", g.ContainerID)
	}
	if g.PatternHash == "" {
		t.Error("PatternHash must be set so groups link to tickets")
	}
	if len(g.Timestamps) != 1 || g.Timestamps[0] != 1788489360 {
		t.Errorf("Timestamps = %v, want the window end in epoch seconds", g.Timestamps)
	}
	if len(g.Values) != 1 || g.Values[0] != 42 {
		t.Errorf("Values = %v", g.Values)
	}
}

// A pod with no deployment field (StatefulSet, Job) still has to resolve a
// workload, which is derived from the pod name.
func TestConvertCubeAPMLogGroupsDerivesWorkloadFromPod(t *testing.T) {
	out := convertCubeAPMLogGroups([]map[string]any{{
		"_msg":               "boom",
		"k8s.namespace.name": "payments",
		"k8s.pod.name":       "ledger-0",
		"cube_count":         "3",
	}}, 1788489360)

	if len(out.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(out.Groups))
	}
	if out.Groups[0].Workload == "" {
		t.Error("Workload should be derived from the pod name when no deployment field is present")
	}
}

func TestCubeAPMLogSourceContract(t *testing.T) {
	var s any = &CubeAPMLogSource{}

	if _, ok := s.(LogSource); !ok {
		t.Error("CubeAPMLogSource must implement LogSource")
	}
	// Without LogGroupSource the Log Groups view errors with an unsupported
	// provider/source combination.
	if _, ok := s.(LogGroupSource); !ok {
		t.Error("CubeAPMLogSource must implement LogGroupSource")
	}
	if _, ok := s.(QueryRequestKeyFilter); !ok {
		t.Error("CubeAPMLogSource must implement QueryRequestKeyFilter")
	}
}

// Every operator advertised by GetSupportedOperators must actually render, or
// the builder offers a filter that fails at query time.
func TestCubeAPMLogSupportedOperatorsAllRender(t *testing.T) {
	s := &CubeAPMLogSource{}
	for _, op := range s.GetSupportedOperators() {
		t.Run(op, func(t *testing.T) {
			_, err := buildCubeAPMConditions(query.QueryWhereClause{Binary: query.BinaryWhereClause{
				"namespace": {query.BinaryWhereClauseType(op): "payments"},
			}}, cubeAPMLogLabelMapping)
			if err != nil {
				t.Errorf("advertised operator %q does not render: %v", op, err)
			}
		})
	}
}

func TestCubeAPMLogSourceRoutedFromDispatcher(t *testing.T) {
	src, err := getLogSource("cubeapm", "user")
	if err != nil {
		t.Fatalf("getLogSource(cubeapm, user) failed: %v", err)
	}
	if _, ok := src.(*CubeAPMLogSource); !ok {
		t.Errorf("got %T, want *CubeAPMLogSource", src)
	}
}

// The label-values query folds the "field is present" condition into the base
// query rather than juxtaposing it with a bare `*`, so the emitted LogsQL is the
// same shape whether or not an environment is configured.
func TestCubeAPMBaseQueryFoldsFieldExistsCondition(t *testing.T) {
	if got := cubeAPMBaseQuery("", "log.level:*"); got != "log.level:*" {
		t.Errorf("got %q, want the bare condition with no leading wildcard", got)
	}
	if got := cubeAPMBaseQuery("prod", "log.level:*"); got != `{env="prod"} log.level:*` {
		t.Errorf("got %q", got)
	}
}
