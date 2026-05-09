package kql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKQLParser(t *testing.T) {
	testCases := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{
			name:  "Simple where clause",
			query: `traces | where a == 1`,
		},
		{
			name:  "Simple where clause with negation",
			query: `traces | where a != 1`,
		},
		{
			name:  "Simple where clause with negation 2",
			query: `traces | where a !has "hello"`,
		},
		{
			name:  "Project operator",
			query: `traces | project a, b, c`,
		},
		{
			name:  "Order by operator",
			query: `traces | order by a asc, b desc`,
		},
		{
			name:  "Take operator",
			query: `traces | take 10`,
		},
		{
			name:  "Summarize operator",
			query: `traces | summarize count(a), avg(b) by c, d`,
		},
		{
			name:  "Extend operator",
			query: `traces | extend x = a + b, y = c * 2`,
		},
		{
			name:  "Project away operator",
			query: `traces | project_away a, b`,
		},
		{
			name:  "Project rename operator",
			query: `traces | project_rename new_a = a, new_b = b`,
		},
		{
			name:  "Count operator",
			query: `traces | count`,
		},
		{
			name:  "Distinct operator",
			query: `traces | distinct a, b`,
		},
		{
			name:  "Union operator",
			query: `traces | union (traces2), (traces3)`,
		},
		{
			name:  "Search operator",
			query: `traces | search in (a, b) "error"`,
		},
		{
			name:  "Parse operator",
			query: `traces | parse a with "*"  b "-" c`,
		},
		{
			name:  "Complex query",
			query: `traces | where a > 10 and b contains "error" | project c, d | order by c asc`,
		},
		{
			name:  "Subquery",
			query: `(traces | where a > 10) | project b`,
		},
		{
			name:  "Search as first operator",
			query: `search in (a, b) "error" | where a > 10`,
		},
		{
			name:  "Timespan literal",
			query: `traces | where a > "1d"`,
		},
		{
			name:    "Invalid query",
			query:   `traces | where a > | project c`,
			wantErr: true,
		},
		{
			name:  "Project with renaming and calculation",
			query: `traces | project EventTime = Timestamp, LatencyMs = Latency * 1000`,
		},
		{
			name:  "Extend with a case function",
			query: `traces | extend ErrorLevel = case(StatusCode >= 500, "Critical", StatusCode >= 400, "Warning", "OK")`,
		},
		{
			name:  "Complex boolean logic with parentheses",
			query: `traces | where (a == 1 and b == 2) or c == 3`,
		},
		{
			name:  "JSON property access with dots and brackets",
			query: `traces | where d.Response.StatusCode == 500 and d.Request.Headers[0] == "application/json"`,
		},
		{
			name:  "Standalone isnull/isnotnull functions",
			query: `traces | where isnotnull(user) and isnull(d.optionalField)`,
		},
		{
			name:  "The 'in' operator with a list of strings",
			query: `traces | where level in ("error", "critical")`,
		},
		{
			name:  "Summarize without a by clause",
			query: `traces | summarize TotalCount = count(), AverageLatency = avg(Latency)`,
		},
		{
			name:  "Summarize with a function in the by clause",
			query: `traces | summarize count() by bin(Timestamp, 1h)`,
		},
		// {
		// 	name:  "Parse with type specifier",
		// 	query: `traces | parse Message with "Duration=" duration:int "ms"`,
		// },
		{
			name:  "Query with mixed single and double quoted strings",
			query: `traces | where a == "hello" and b == 'world'`,
		},
		{
			name:  "Union with a subquery and a table",
			query: `(traces | where a > 10) | union exceptions`,
		},
		{
			name:  "Search with a complex expression",
			query: `search "error" and StatusCode in (500, 503)`,
		},
		{
			name:  "Simple Project operator",
			query: `dependencies | project name, resultCode`,
		},
		{
			name:  "SimpleTake",
			query: `ependencies | take 100`,
		},
		{
			name:  "Not Null",
			query: `dependencies | where isnotnull(resultCode)`,
		},
		{
			name:  "Function Call",
			query: `dependencies | where timestamp > ago(1d)`,
		},
		{
			name:  "Has Call",
			query: `dependencies | where type has "blob"`,
		},
		{
			name:  "Regex",
			query: `dependencies | where type matches regex "^Azure.*"`,
		},
		{
			name:  "Distinct",
			query: `dependencies | distinct name, type`,
		},
		{
			name:  "Distinct",
			query: `dependencies | distinct name, type`,
		},
		{
			name:  "Group Filter",
			query: `dependencies | summarize Count = count() by name | where Count > 3`,
		},
		{
			name:  "Union",
			query: `dependencies | where timestamp > ago(1d) | union (exceptions | where timestamp > ago(1d))`,
		},
		{
			name:  "Nested function",
			query: `traces | summarize count() by bin(todatetime(Timestamp), 1d)`,
		},
		{
			name:  "More Complex math",
			query: `traces | extend a = (b + c) / (d - 100.5)`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.query)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
