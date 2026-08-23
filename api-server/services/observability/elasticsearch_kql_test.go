package observability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// kqlJSON translates a KQL string and returns the emitted DSL as compact JSON.
// json.Marshal sorts map keys, so the output is deterministic and can be compared
// exactly.
func kqlJSON(t *testing.T, kql string) string {
	t.Helper()
	dsl, err := kqlToDSL(kql)
	if err != nil {
		t.Fatalf("kqlToDSL(%q) unexpected error: %v", kql, err)
	}
	b, err := json.Marshal(dsl)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestKQLToDSL_LeafExpressions(t *testing.T) {
	cases := []struct{ kql, want string }{
		// field:value -> match
		{`status:200`, `{"match":{"status":{"query":"200"}}}`},
		{`service.name:auth`, `{"match":{"service.name":{"query":"auth"}}}`},
		// quoted phrase -> match_phrase
		{`service.name:"auth service"`, `{"match_phrase":{"service.name":"auth service"}}`},
		// field:* -> exists
		{`response:*`, `{"exists":{"field":"response"}}`},
		// value wildcard -> wildcard
		{`host:web*`, `{"wildcard":{"host":{"value":"web*"}}}`},
		{`host:*prod*`, `{"wildcard":{"host":{"value":"*prod*"}}}`},
		// field-name wildcard -> multi_match over the pattern
		{`kube.*:nginx`, `{"multi_match":{"fields":["kube.*"],"lenient":true,"query":"nginx"}}`},
		// bare term -> multi_match across all fields
		{`error`, `{"multi_match":{"fields":["*"],"lenient":true,"query":"error"}}`},
		// bare quoted phrase -> multi_match type phrase
		{`"disk full"`, `{"multi_match":{"fields":["*"],"lenient":true,"query":"disk full","type":"phrase"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.kql, func(t *testing.T) {
			assert.Equal(t, tc.want, kqlJSON(t, tc.kql))
		})
	}
}

func TestKQLToDSL_Ranges(t *testing.T) {
	cases := []struct{ kql, want string }{
		{`bytes > 1000`, `{"range":{"bytes":{"gt":1000}}}`},
		{`bytes >= 1000`, `{"range":{"bytes":{"gte":1000}}}`},
		{`bytes < 2.5`, `{"range":{"bytes":{"lt":2.5}}}`},
		{`bytes <= 100`, `{"range":{"bytes":{"lte":100}}}`},
		// no whitespace around the operator
		{`status>=400`, `{"range":{"status":{"gte":400}}}`},
		// date literal stays a string (ES parses it)
		{`@timestamp >= "2024-01-01T00:00:00Z"`, `{"range":{"@timestamp":{"gte":"2024-01-01T00:00:00Z"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.kql, func(t *testing.T) {
			assert.Equal(t, tc.want, kqlJSON(t, tc.kql))
		})
	}
}

func TestKQLToDSL_Booleans(t *testing.T) {
	cases := []struct{ kql, want string }{
		// and -> bool.must
		{`a:1 and b:2`, `{"bool":{"must":[{"match":{"a":{"query":"1"}}},{"match":{"b":{"query":"2"}}}]}}`},
		// or -> bool.should + minimum_should_match
		{`a:1 or b:2`, `{"bool":{"minimum_should_match":1,"should":[{"match":{"a":{"query":"1"}}},{"match":{"b":{"query":"2"}}}]}}`},
		// not -> bool.must_not
		{`not env:dev`, `{"bool":{"must_not":[{"match":{"env":{"query":"dev"}}}]}}`},
		// case-insensitive operators
		{`a:1 AND b:2`, `{"bool":{"must":[{"match":{"a":{"query":"1"}}},{"match":{"b":{"query":"2"}}}]}}`},
		{`a:1 Or b:2`, `{"bool":{"minimum_should_match":1,"should":[{"match":{"a":{"query":"1"}}},{"match":{"b":{"query":"2"}}}]}}`},
		// implicit AND (whitespace-separated clauses)
		{`a:1 b:2`, `{"bool":{"must":[{"match":{"a":{"query":"1"}}},{"match":{"b":{"query":"2"}}}]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.kql, func(t *testing.T) {
			assert.Equal(t, tc.want, kqlJSON(t, tc.kql))
		})
	}
}

func TestKQLToDSL_Precedence(t *testing.T) {
	// not > and > or:  "a:1 or b:2 and c:3" == a:1 OR (b:2 AND c:3)
	assert.Equal(t,
		`{"bool":{"minimum_should_match":1,"should":[{"match":{"a":{"query":"1"}}},{"bool":{"must":[{"match":{"b":{"query":"2"}}},{"match":{"c":{"query":"3"}}}]}}]}}`,
		kqlJSON(t, `a:1 or b:2 and c:3`),
	)
	// "not a:1 and b:2" == (NOT a:1) AND b:2
	assert.Equal(t,
		`{"bool":{"must":[{"bool":{"must_not":[{"match":{"a":{"query":"1"}}}]}},{"match":{"b":{"query":"2"}}}]}}`,
		kqlJSON(t, `not a:1 and b:2`),
	)
	// parentheses override: "(a:1 or b:2) and c:3"
	assert.Equal(t,
		`{"bool":{"must":[{"bool":{"minimum_should_match":1,"should":[{"match":{"a":{"query":"1"}}},{"match":{"b":{"query":"2"}}}]}},{"match":{"c":{"query":"3"}}}]}}`,
		kqlJSON(t, `(a:1 or b:2) and c:3`),
	)
}

func TestKQLToDSL_ValueLists(t *testing.T) {
	// field:(a or b) -> OR of matches on the same field
	assert.Equal(t,
		`{"bool":{"minimum_should_match":1,"should":[{"match":{"status":{"query":"200"}}},{"match":{"status":{"query":"201"}}}]}}`,
		kqlJSON(t, `status:(200 or 201)`),
	)
	// field:(a and b)
	assert.Equal(t,
		`{"bool":{"must":[{"match":{"tag":{"query":"a"}}},{"match":{"tag":{"query":"b"}}}]}}`,
		kqlJSON(t, `tag:(a and b)`),
	)
	// implicit OR inside a value list
	assert.Equal(t,
		`{"bool":{"minimum_should_match":1,"should":[{"match":{"status":{"query":"200"}}},{"match":{"status":{"query":"201"}}}]}}`,
		kqlJSON(t, `status:(200 201)`),
	)
	// single-element list collapses to a bare match
	assert.Equal(t, `{"match":{"status":{"query":"200"}}}`, kqlJSON(t, `status:(200)`))
}

func TestKQLToDSL_Nested(t *testing.T) {
	// parent:{ child:v and child2:v2 } -> nested with fields resolved to full path
	assert.Equal(t,
		`{"nested":{"path":"user","query":{"bool":{"must":[{"match":{"user.first":{"query":"john"}}},{"match":{"user.last":{"query":"doe"}}}]}},"score_mode":"none"}}`,
		kqlJSON(t, `user:{ first:john and last:doe }`),
	)
	// nested inside nested keeps prefixing the path
	assert.Equal(t,
		`{"nested":{"path":"a","query":{"nested":{"path":"a.b","query":{"match":{"a.b.c":{"query":"x"}}},"score_mode":"none"}},"score_mode":"none"}}`,
		kqlJSON(t, `a:{ b:{ c:x } }`),
	)
}

func TestKQLToDSL_RealisticLogQueries(t *testing.T) {
	// The example from the conversation.
	assert.Equal(t,
		`{"bool":{"must":[{"match":{"status":{"query":"200"}}},{"match_phrase":{"service.name":"auth"}}]}}`,
		kqlJSON(t, `status:200 and service.name:"auth"`),
	)
	// range + term + negation
	assert.Equal(t,
		`{"bool":{"must":[{"range":{"http.response.status_code":{"gte":400}}},{"bool":{"must_not":[{"match":{"env":{"query":"dev"}}}]}}]}}`,
		kqlJSON(t, `http.response.status_code >= 400 and not env:dev`),
	)
}

func TestKQLToDSL_Errors(t *testing.T) {
	bad := []string{
		`"unterminated`, // unterminated quote
		`(a:1`,          // unbalanced paren
		`a:1 and`,       // trailing operator
		`a:1 or`,        // trailing operator
		`not`,           // nothing after not
		`status >`,      // missing range value
		`user:{ }`,      // empty nested block
		`user:{ a:1`,    // unterminated nested block
		`status:(200`,   // unterminated value list
		`)`,             // stray close paren
	}
	for _, q := range bad {
		t.Run(q, func(t *testing.T) {
			_, err := kqlToDSL(q)
			assert.Error(t, err, "expected parse error for %q", q)
		})
	}
}

func TestKQLToDSL_EscapesAndSpecialChars(t *testing.T) {
	// Escaped space inside an unquoted value.
	assert.Equal(t, `{"match":{"name":{"query":"foo bar"}}}`, kqlJSON(t, `name:foo\ bar`))
	// Escaped colon in a value.
	assert.Equal(t, `{"match":{"url":{"query":"http://x"}}}`, kqlJSON(t, `url:http\://x`))
	// A literal '?' is not a wildcard in KQL -> stays a plain match.
	assert.Equal(t, `{"match":{"q":{"query":"a?b"}}}`, kqlJSON(t, `q:a?b`))
}

// ---------- buildESKQLQueryBody (the QueryLogs entry point) ----------

func TestBuildESKQLQueryBody_WrapsWithWindowSizeSort(t *testing.T) {
	body, err := buildESKQLQueryBody(`status:200 and level:error`, 1000, 2000, 25, 0, nil)
	assert.NoError(t, err)

	// Time range present -> translated query nests under bool.filter with the range.
	filter := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
	assert.Len(t, filter, 2)

	out, _ := json.Marshal(body)
	s := string(out)
	assert.Contains(t, s, `"match":{"status"`)
	assert.Contains(t, s, "epoch_millis")
	assert.Contains(t, s, `"@timestamp":{"order":"desc"}`) // default sort
	assert.Equal(t, 25, body["size"])                      // size from limit
}

func TestBuildESKQLQueryBody_NoWindowQueryIsTopLevel(t *testing.T) {
	body, err := buildESKQLQueryBody(`status:500`, 0, 0, 0, 0, nil)
	assert.NoError(t, err)
	// Without a window, the translated match is the query itself (no bool wrapper).
	m := body["query"].(map[string]any)["match"].(map[string]any)
	assert.Contains(t, m, "status")
	assert.Equal(t, defaultESLogQuerySize, body["size"])
}

func TestBuildESKQLQueryBody_EmptyDegradesToMatchAll(t *testing.T) {
	body, err := buildESKQLQueryBody("   ", 1000, 2000, 0, 0, nil)
	assert.NoError(t, err)
	out, _ := json.Marshal(body)
	s := string(out)
	assert.Contains(t, s, "match_all")
	assert.Contains(t, s, "epoch_millis") // still time-bounded
}

func TestBuildESKQLQueryBody_OffsetSetsFrom(t *testing.T) {
	body, err := buildESKQLQueryBody("level:warn", 0, 0, 10, 30, nil)
	assert.NoError(t, err)
	assert.Equal(t, 30, body["from"])
}

func TestBuildESKQLQueryBody_ParseErrorPropagates(t *testing.T) {
	_, err := buildESKQLQueryBody(`"unterminated`, 0, 0, 0, 0, nil)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "kql"), "error should come from the KQL parser: %v", err)
}
