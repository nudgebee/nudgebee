package tools

import (
	"nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLokiExecuteTool_CleanupQuery(t *testing.T) {
	tool := LokiExecuteTool{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain query unchanged",
			input:    `{app="my-app"}`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip single backticks",
			input:    "`{app=\"my-app\"}`",
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip markdown code block with language tag",
			input:    "```logql\n{app=\"my-app\"}\n```",
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip markdown code block without language tag",
			input:    "```\n{app=\"my-app\"}\n```",
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip double quote wrapping",
			input:    `"{app=\"my-app\"}"`,
			expected: `{app=\"my-app\"}`,
		},
		{
			name:     "strip trailing pipe",
			input:    `{app="my-app"} |`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip trailing pipe tilde",
			input:    `{app="my-app"} |~`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip trailing pipe equals",
			input:    `{app="my-app"} |=`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip leading and trailing whitespace",
			input:    `   {app="my-app"}   `,
			expected: `{app="my-app"}`,
		},
		{
			name:     "preserve valid line filter",
			input:    `{app="my-app"} |~ "(?i)error"`,
			expected: `{app="my-app"} |~ "(?i)error"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.cleanupQuery(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLokiExecuteTool_FindInvalidLabels(t *testing.T) {
	tool := LokiExecuteTool{}

	tests := []struct {
		name        string
		query       string
		validLabels []string
		expected    []string
	}{
		{
			name:        "all labels valid",
			query:       `{app="my-app", namespace="prod"}`,
			validLabels: []string{"app", "namespace", "pod", "container"},
			expected:    nil,
		},
		{
			name:        "one invalid label",
			query:       `{service="my-app", namespace="prod"}`,
			validLabels: []string{"app", "namespace", "pod", "container"},
			expected:    []string{"service"},
		},
		{
			name:        "multiple invalid labels",
			query:       `{service="my-app", deployment="web"}`,
			validLabels: []string{"app", "namespace", "pod"},
			expected:    []string{"service", "deployment"},
		},
		{
			name:        "regex match operator",
			query:       `{app=~"my-app.*", namespace="prod"}`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
		{
			name:        "negation operator",
			query:       `{app!="my-app"}`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
		{
			name:        "no stream selector",
			query:       `|= "error"`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
		{
			name:        "empty valid labels treats all as invalid",
			query:       `{app="my-app"}`,
			validLabels: []string{},
			expected:    []string{"app"},
		},
		{
			name:        "query with line filter after selector",
			query:       `{app="my-app"} |~ "(?i)error"`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.findInvalidLabels(tt.query, tt.validLabels)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAppendLokiDirection(t *testing.T) {
	const baseQuery = `query={app="x"}&start=1&end=2&limit=10`

	tests := []struct {
		name           string
		query          string
		argDirection   any
		expected       string
		queryHasParam  string
		shouldNotMatch string
	}{
		{
			name:          "forward direction is appended",
			query:         baseQuery,
			argDirection:  "forward",
			queryHasParam: "&direction=forward",
		},
		{
			name:          "backward direction is appended",
			query:         baseQuery,
			argDirection:  "backward",
			queryHasParam: "&direction=backward",
		},
		{
			name:          "synonym 'asc' normalizes to forward",
			query:         baseQuery,
			argDirection:  "asc",
			queryHasParam: "&direction=forward",
		},
		{
			name:          "synonym 'oldest' normalizes to forward",
			query:         baseQuery,
			argDirection:  "oldest",
			queryHasParam: "&direction=forward",
		},
		{
			name:          "synonym 'desc' normalizes to backward",
			query:         baseQuery,
			argDirection:  "desc",
			queryHasParam: "&direction=backward",
		},
		{
			name:          "synonym 'newest' normalizes to backward",
			query:         baseQuery,
			argDirection:  "newest",
			queryHasParam: "&direction=backward",
		},
		{
			name:          "synonym 'backwards' (plural) normalizes to backward",
			query:         baseQuery,
			argDirection:  "backwards",
			queryHasParam: "&direction=backward",
		},
		{
			name:           "genuinely invalid direction is silently dropped",
			query:          baseQuery,
			argDirection:   "sideways",
			shouldNotMatch: "direction=",
		},
		{
			name:          "case-insensitive: FORWARD normalizes to lowercase",
			query:         baseQuery,
			argDirection:  "FORWARD",
			queryHasParam: "&direction=forward",
		},
		{
			name:          "leading/trailing whitespace is stripped",
			query:         baseQuery,
			argDirection:  "  forward  ",
			queryHasParam: "&direction=forward",
		},
		{
			name:           "non-string arg is ignored",
			query:          baseQuery,
			argDirection:   42,
			shouldNotMatch: "direction=",
		},
		{
			name:           "missing arg (nil) is ignored",
			query:          baseQuery,
			argDirection:   nil,
			shouldNotMatch: "direction=",
		},
		{
			name:         "existing direction= in query is preserved (no double-set)",
			query:        baseQuery + "&direction=forward",
			argDirection: "backward",
			expected:     baseQuery + "&direction=forward",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendLokiDirection(tt.query, tt.argDirection)
			if tt.expected != "" {
				assert.Equal(t, tt.expected, got)
			}
			if tt.queryHasParam != "" {
				assert.Contains(t, got, tt.queryHasParam)
			}
			if tt.shouldNotMatch != "" {
				assert.NotContains(t, got, tt.shouldNotMatch)
			}
		})
	}
}

func TestLokiExecuteTool_DirectionInputSchema(t *testing.T) {
	tool := LokiExecuteTool{}
	schema := tool.InputSchema()
	dir, ok := schema.Properties["direction"]
	assert.True(t, ok, "InputSchema must declare 'direction' property")
	assert.Equal(t, core.ToolSchemaTypeString, dir.Type)
	assert.Contains(t, dir.Description, "forward")
	assert.Contains(t, dir.Description, "backward")
}

// _ilike must produce a case-insensitive `(?i)` regex. Loki `|~`/`=~` are
// case-sensitive by default; SQL ILIKE is not.
func TestNBLogTool_BuildLokiQuery_ILikeCaseInsensitive(t *testing.T) {
	tool := &NBLogTool{}

	tests := []struct {
		name     string
		where    core.QueryWhereClause
		expected string
	}{
		{
			name: "ILike on _body emits case-insensitive line filter",
			where: core.QueryWhereClause{
				Binary: map[string]map[core.BinaryWhereClauseType]any{
					"app":   {core.Eq: "my-app-51"},
					"_body": {core.ILike: "%error%"},
				},
			},
			expected: `{app="my-app-51"} |~ "(?i).*error.*"`,
		},
		{
			name: "Like on _body stays case-sensitive",
			where: core.QueryWhereClause{
				Binary: map[string]map[core.BinaryWhereClauseType]any{
					"app":   {core.Eq: "my-app-51"},
					"_body": {core.Like: "%error%"},
				},
			},
			expected: `{app="my-app-51"} |~ ".*error.*"`,
		},
		{
			name: "ILike on label uses case-insensitive label matcher",
			where: core.QueryWhereClause{
				Binary: map[string]map[core.BinaryWhereClauseType]any{
					"namespace": {core.ILike: "kube-%"},
				},
			},
			expected: `{namespace=~"(?i)kube-.*"}`,
		},
		{
			name: "ILike inside OR scopes case-insensitivity per term",
			where: core.QueryWhereClause{
				Or: []core.QueryWhereClause{
					{Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.ILike: "%timeout%"},
					}},
					{Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Like: "%CRITICAL%"},
					}},
				},
			},
			expected: `{} |~ "(?i:.*timeout.*)|.*CRITICAL.*"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tool.buildLokiQueryFromWhereClause(&tt.where)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
