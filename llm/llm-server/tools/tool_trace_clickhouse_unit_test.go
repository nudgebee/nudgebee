package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTracesViewExpansion guards the regression where the `traces_view` -> otel_traces
// substitution only matched when the view name was surrounded by literal spaces. Queries
// like `SELECT count(*) FROM traces_view` (no trailing space) were sent verbatim with the
// non-existent `traces_view` table, errored in ClickHouse, and were surfaced as empty.
func TestTracesViewExpansion(t *testing.T) {
	expansion := fmt.Sprintf(tracesViewClickhouseFormat, "otel_traces")

	cases := []string{
		"SELECT count(*) FROM traces_view",                    // no trailing space (the original bug)
		"SELECT * FROM traces_view WHERE workload_name = 'x'", // space-padded
		"SELECT * FROM traces_view\nORDER BY timestamp DESC",  // newline-terminated
		"SELECT * FROM (SELECT * FROM traces_view) t",         // followed by ')'
		"SELECT * FROM default.traces_view LIMIT 5",           // schema-qualified
		"SELECT * FROM TRACES_VIEW",                           // case-insensitive
	}

	for _, q := range cases {
		got := tracesViewPattern.ReplaceAllString(q, " "+expansion+" ")
		assert.NotEqual(t, q, got, "query should have been rewritten: %q", q)
		assert.Contains(t, got, "otel_traces", "expansion should reference otel_traces for: %q", q)
		assert.NotRegexp(t, `(?i)\b(?:default\.)?traces_view\b`, got, "no literal traces_view should remain for: %q", q)
	}
}

// TestTracesViewSchemaColumns guards that the projection exposes exactly the columns the
// traces agent's prompt/examples reference. Missing `endpoint` / `http_status_code` caused
// every agent query that selected them to fail with UNKNOWN_IDENTIFIER.
func TestTracesViewSchemaColumns(t *testing.T) {
	for _, col := range []string{
		"as endpoint",
		"as http_status_code",
		"as resource",
		"as status_code",
		"as workload_name",
		"as duration_ns",
	} {
		assert.True(t, strings.Contains(tracesViewClickhouseFormat, col),
			"traces_view projection must expose column %q (agent prompt references it)", col)
	}
}
