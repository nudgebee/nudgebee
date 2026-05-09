package tools

import (
	"nudgebee/llm/tools/core"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueryBuilderToLogglyQuery(t *testing.T) {
	tool := &NBLogTool{}

	testCases := []struct {
		name     string
		builder  core.QueryBuilder
		expected string
		hasError bool
	}{
		{
			name: "Simple Eq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"level": {core.Eq: "error"},
					},
				},
			},
			expected: `level:error`,
			hasError: false,
		},
		{
			name: "Simple Nq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"level": {core.Nq: "info"},
					},
				},
			},
			expected: `NOT level:info`,
			hasError: false,
		},
		{
			name: "Simple In",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"status": {core.In: []any{"500", "503"}},
					},
				},
			},
			expected: `status:(500 OR 503)`,
			hasError: false,
		},
		{
			name: "Simple NotIn",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"status": {core.NotIn: []any{"200", "201"}},
					},
				},
			},
			expected: `NOT status:(200 OR 201)`,
			hasError: false,
		},
		{
			name: "Simple Like",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"message": {core.Like: "database error"},
					},
				},
			},
			expected: `message:*database error*`,
			hasError: false,
		},
		{
			name: "Simple NLike",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"message": {core.NLike: "permission denied"},
					},
				},
			},
			expected: `NOT message:*permission denied*`,
			hasError: false,
		},
		{
			name: "Combined query",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"level":   {core.Eq: "error"},
						"service": {core.Eq: "backend"},
					},
				},
			},
			expected: `level:error AND service:backend`,
			hasError: false,
		},
		{
			name: "Body search",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Eq: "hello world"},
					},
				},
			},
			expected: `hello world`,
			hasError: false,
		},
		{
			name: "Body search with wildcard",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Like: "hello"},
					},
				},
			},
			expected: `*hello*`,
			hasError: false,
		},
		{
			name: "Regex search",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Like: "/error|warn/"},
					},
				},
			},
			expected: `/error|warn/`,
			hasError: false,
		},
		{
			name: "Field search with regex",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"message": {core.Like: "/error|warn/"},
					},
				},
			},
			expected: `message:/error|warn/`,
			hasError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := tool.queryBuilderToLogglyQuery(tc.builder)

			if tc.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				// The order of AND clauses is not guaranteed, so we need to compare them in a way that is order-independent.
				assert.ElementsMatch(t, strings.Split(tc.expected, " AND "), strings.Split(actual, " AND "))
			}
		})
	}
}

func TestQueryBuilderToLokiQuery(t *testing.T) {
	tool := &NBLogTool{}

	testCases := []struct {
		name     string
		builder  core.QueryBuilder
		expected string
		hasError bool
	}{

		{
			name: "Simple label selector with Eq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"app": {core.Eq: "myapp"},
					},
				},
			},
			expected: `{app="myapp"}`,
			hasError: false,
		},
		{
			name: "Simple label selector with Nq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"app": {core.Nq: "myapp"},
					},
				},
			},
			expected: `{app!="myapp"}`,
			hasError: false,
		},
		{
			name: "Simple label selector with Like (regex)",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"app": {core.Like: "my.*"},
					},
				},
			},
			expected: `{app=~"my.*"}`,
			hasError: false,
		},
		{
			name: "Simple label selector with NLike (regex)",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"app": {core.NLike: "my.*"},
					},
				},
			},
			expected: `{app!~"my.*"}`,
			hasError: false,
		},
		{
			name: "Simple label selector with In",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"app": {core.In: []any{"myapp", "otherapp"}},
					},
				},
			},
			expected: `{app=~"myapp|otherapp"}`,
			hasError: false,
		},
		{
			name: "Simple label selector with NotIn",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"app": {core.NotIn: []any{"myapp", "otherapp"}},
					},
				},
			},
			expected: `{app!~"myapp|otherapp"}`,
			hasError: false,
		}, {
			name: "Simple line filter with Eq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Eq: "error"},
					},
				},
			},
			expected: `{stream="stdout"} |= "error"`,
			hasError: false,
		}, {
			name: "Simple line filter with Nq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Nq: "error"},
					},
				},
			},
			expected: `{stream="stdout"} != "error"`,
			hasError: false,
		}, {
			name: "Simple line filter with Like (regex)",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Like: "error|warn"},
					},
				},
			},
			expected: `{stream="stdout"} |~ "error|warn"`,
			hasError: false,
		}, {
			name: "Simple line filter with NLike (regex)",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.NLike: "error|warn"},
					},
				},
			},
			expected: `{stream="stdout"} !~ "error|warn"`,
			hasError: false,
		}, {
			name: "Combined query",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"app":   {core.Eq: "myapp"},
						"_body": {core.Like: "error"},
					},
				},
			},
			expected: `{app="myapp"} |~ "error"`,
			hasError: false,
		}, {
			name: "And clause",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"app": {core.Eq: "myapp"},
							},
						},
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"level": {core.Eq: "info"},
							},
						},
					},
				},
			},
			expected: `{app="myapp",level="info"}`,
			hasError: false,
		}, {
			name: "OR clause for _body with eq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Or: []core.QueryWhereClause{
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"_body": {core.Eq: "error"},
							},
						},
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"_body": {core.Eq: "warning"},
							},
						},
					},
				},
			},
			expected: `{stream="stdout"} |~ "error|warning"`,
			hasError: false,
		}, {
			name: "OR clause for _body with eq and like",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Or: []core.QueryWhereClause{
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"_body": {core.Eq: "error"},
							},
						},
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"_body": {core.Like: "fail%"},
							},
						},
					},
				},
			},
			expected: `{stream="stdout"} |~ "error|fail.*"`,
			hasError: false,
		}, {
			name: "OR clause with _body and other field",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Or: []core.QueryWhereClause{
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"_body": {core.Eq: "error"},
							},
						},
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"app": {core.Eq: "otherapp"},
							},
						},
					},
				},
			},
			expected: `{stream="stdout"} |~ "error|(app=\"otherapp\"|\"app\":\"otherapp\")"`,
			hasError: false,
		}, {
			name: "OR clause for label with Eq",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Or: []core.QueryWhereClause{
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"app": {core.Eq: "myapp"},
							},
						},
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"app": {core.Eq: "otherapp"},
							},
						},
					},
				},
			},
			expected: `{app=~"myapp|otherapp"}`,
			hasError: false,
		}, {
			name: "OR on label with AND on another label",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{
							Or: []core.QueryWhereClause{
								{
									Binary: map[string]map[core.BinaryWhereClauseType]any{

										"app": {core.Eq: "myapp"},
									},
								},
								{
									Binary: map[string]map[core.BinaryWhereClauseType]any{
										"app": {core.Eq: "otherapp"},
									},
								},
							},
						},
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"level": {core.Eq: "error"},
							},
						},
					},
				},
			},
			expected: `{app=~"myapp|otherapp",level="error"}`,
			hasError: false,
		}, {
			name: "Conflicting OR and AND on same label",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"app": {core.Eq: "anotherapp"},
							},
						},
						{
							Or: []core.QueryWhereClause{
								{
									Binary: map[string]map[core.BinaryWhereClauseType]any{
										"app": {core.Eq: "myapp"},
									},
								},
								{
									Binary: map[string]map[core.BinaryWhereClauseType]any{
										"app": {core.Eq: "otherapp"},
									},
								},
							},
						},
					},
				},
			},

			expected: "",

			hasError: true,
		}, {
			name: "OR on different labels",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Or: []core.QueryWhereClause{
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"app": {core.Eq: "myapp"},
							},
						},
						{
							Binary: map[string]map[core.BinaryWhereClauseType]any{
								"level": {core.Eq: "error"},
							},
						},
					},
				},
			},
			expected: `{stream="stdout"} |~ "(app=\"myapp\"|\"app\":\"myapp\")|(level=\"error\"|\"level\":\"error\")"`,
			hasError: false,
		}, {
			name: "Not clause",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Not: &core.QueryWhereClause{
						Binary: map[string]map[core.BinaryWhereClauseType]any{
							"app": {core.Eq: "myapp"},
						},
					},
				},
			},
			expected: "",
			hasError: true,
		}, {
			name: "_body In operator",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.In: []any{"error", "warn"}},
					},
				},
			},
			expected: `{stream="stdout"} |~ "error|warn"`,
			hasError: false,
		}, {

			name: "_body NotIn operator",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.NotIn: []any{"debug", "trace"}},
					},
				},
			},
			expected: `{stream="stdout"} !~ "debug|trace"`,
			hasError: false,
		}, {
			name: "Value escaping",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"message": {core.Eq: `hello "world"`},
					},
				},
			},
			expected: `{message="hello \"world\""}`,
			hasError: false,
		}, {
			name: "Wildcard to regex conversion for _body Like",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.Like: `foo.*bar`},
					},
				},
			},
			expected: `{stream="stdout"} |~ "foo.*bar"`,
			hasError: false,
		}, {
			name: "Wildcard to regex conversion for _body NLike",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: map[string]map[core.BinaryWhereClauseType]any{
						"_body": {core.NLike: `foo?bar`},
					},
				},
			},
			expected: `{stream="stdout"} !~ "foo?bar"`,
			hasError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := tool.queryBuilderToLokiQuery(tc.builder)
			if tc.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, actual)
			}
		})
	}
}

func TestQueryBuilderToObservQuery(t *testing.T) {

	tool := &NBLogTool{}

	testCases := []struct {
		name string

		builder core.QueryBuilder

		expected string

		hasError bool
	}{

		{

			name: "Simple Eq",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"level": {core.Eq: "error"},
					},
				},
			},

			expected: `filter level = "error"`,

			hasError: false,
		},

		{

			name: "Simple Nq",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"level": {core.Nq: "info"},
					},
				},
			},

			expected: `filter level != "info"`,

			hasError: false,
		},

		{

			name: "Simple In",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"status": {core.In: []any{500, 503}},
					},
				},
			},

			expected: `filter status in [500, 503]`,

			hasError: false,
		},

		{

			name: "Simple NotIn",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"status": {core.NotIn: []any{"200", "201"}},
					},
				},
			},

			expected: `filter not (status in ["200", "201"])`,

			hasError: false,
		},

		{

			name: "Simple Like (contains)",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"message": {core.Like: "database error"},
					},
				},
			},

			expected: `filter message ~ "database error"`,

			hasError: false,
		},

		{

			name: "Simple NLike (contains)",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"message": {core.NLike: "permission denied"},
					},
				},
			},

			expected: `filter message !~ "permission denied"`,

			hasError: false,
		},

		{

			name: "Like with regex",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"message": {core.Like: "/error|warn/"},
					},
				},
			},

			expected: `filter message ~ /error|warn/`,

			hasError: false,
		},

		{

			name: "NLike with regex",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"message": {core.NLike: "/error|warn/"},
					},
				},
			},

			expected: `filter message !~ /error|warn/`,

			hasError: false,
		},

		{

			name: "ILike with wildcard",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"message": {core.ILike: "database%error"},
					},
				},
			},

			expected: `filter message ~ "database*error"`,

			hasError: false,
		},

		{

			name: "Combined query (AND of binary)",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"level": {core.Eq: "error"},

						"service": {core.Eq: "backend"},
					},
				},
			},

			expected: `filter level = "error" and service = "backend"`,

			hasError: false,
		},

		{

			name: "Body search",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"_body": {core.Like: "hello world"},
					},
				},
			},

			expected: `filter _body ~ "hello world"`,

			hasError: false,
		},

		{

			name: "Query with limit",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"level": {core.Eq: "error"},
					},
				},

				Limit: 50,
			},

			expected: `filter level = "error" | limit 50`,

			hasError: false,
		},

		{

			name: "Query with limit only",

			builder: core.QueryBuilder{

				Limit: 100,
			},

			expected: `limit 100`,

			hasError: false,
		},

		{

			name: "Empty query",

			builder: core.QueryBuilder{},

			expected: "",

			hasError: false,
		},

		{

			name: "Value with quotes",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Binary: map[string]map[core.BinaryWhereClauseType]any{

						"message": {core.Eq: `hello "world"`},
					},
				},
			},

			expected: `filter message = "hello \"world\""`,

			hasError: false,
		},

		{

			name: "AND clause",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					And: []core.QueryWhereClause{

						{

							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"app": {core.Eq: "myapp"},
							},
						},

						{

							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"level": {core.Eq: "info"},
							},
						},
					},
				},
			},

			expected: `filter app = "myapp" and level = "info"`,

			hasError: false,
		},

		{

			name: "OR clause",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Or: []core.QueryWhereClause{

						{

							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"app": {core.Eq: "myapp"},
							},
						},

						{

							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"app": {core.Eq: "otherapp"},
							},
						},
					},
				},
			},

			expected: `filter (app = "myapp" or app = "otherapp")`,

			hasError: false,
		},

		{

			name: "NOT clause",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Not: &core.QueryWhereClause{

						Binary: map[string]map[core.BinaryWhereClauseType]any{

							"app": {core.Eq: "myapp"},
						},
					},
				},
			},

			expected: `filter not (app = "myapp")`,

			hasError: false,
		},

		{

			name: "AND with OR subclause",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					And: []core.QueryWhereClause{

						{

							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"level": {core.Eq: "error"},
							},
						},

						{

							Or: []core.QueryWhereClause{

								{

									Binary: map[string]map[core.BinaryWhereClauseType]any{

										"app": {core.Eq: "app1"},
									},
								},

								{

									Binary: map[string]map[core.BinaryWhereClauseType]any{

										"app": {core.Eq: "app2"},
									},
								},
							},
						},
					},
				},
			},

			expected: `filter level = "error" and (app = "app1" or app = "app2")`,

			hasError: false,
		},

		{

			name: "OR with NOT subclause",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					Or: []core.QueryWhereClause{

						{

							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"level": {core.Eq: "warn"},
							},
						},

						{

							Not: &core.QueryWhereClause{

								Binary: map[string]map[core.BinaryWhereClauseType]any{

									"app": {core.Eq: "app3"},
								},
							},
						},
					},
				},
			},

			expected: `filter (level = "warn" or not (app = "app3"))`,

			hasError: false,
		},

		{

			name: "Complex nested query",

			builder: core.QueryBuilder{

				Where: core.QueryWhereClause{

					And: []core.QueryWhereClause{

						{

							Binary: map[string]map[core.BinaryWhereClauseType]any{

								"env": {core.Eq: "prod"},
							},
						},

						{

							Or: []core.QueryWhereClause{

								{

									Binary: map[string]map[core.BinaryWhereClauseType]any{

										"service": {core.Eq: "auth"},
									},
								},

								{

									And: []core.QueryWhereClause{

										{

											Binary: map[string]map[core.BinaryWhereClauseType]any{

												"service": {core.Eq: "user"},
											},
										},

										{

											Not: &core.QueryWhereClause{

												Binary: map[string]map[core.BinaryWhereClauseType]any{

													"status": {core.Eq: "200"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},

			expected: `filter env = "prod" and (service = "auth" or service = "user" and not (status = "200"))`,

			hasError: false,
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {

			actual, err := tool.queryBuilderToObservQuery(tc.builder)

			if tc.hasError {

				assert.Error(t, err)

			} else {

				assert.NoError(t, err)

				actualQuery, actualLimit, _ := strings.Cut(actual, " | ")

				expectedQuery, expectedLimit, _ := strings.Cut(tc.expected, " | ")

				assert.Equal(t, expectedLimit, actualLimit)

				if strings.Contains(actualQuery, " and ") || strings.Contains(actualQuery, " or ") || strings.Contains(actualQuery, "not (") {

					// For complex queries with AND/OR/NOT, we can't simply split by " and " or " or "

					// and use ElementsMatch because of nested parentheses. A direct string comparison

					// is more appropriate here, assuming the expected string is carefully constructed

					// to match the output.

					assert.Equal(t, expectedQuery, actualQuery)

				} else {

					assert.Equal(t, expectedQuery, actualQuery)

				}

			}

		})

	}

}
