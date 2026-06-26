package api

import (
	"nudgebee/services/query"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

func TestGQLFieldParsing(t *testing.T) {
	t.Run("TestGQLFieldParsing", func(t *testing.T) {
		cols, err := parseSelectColumns(&ActionRequest{
			RequestQuery: `query MyQuery{ dw_query_groupings_v2(where:{a:{_eq:1},b:{_eq:2},c:{_eq:3}}){a b c}}`,
		})

		assert.Nil(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, lo.Map(cols, func(item query.QueryColumn, index int) string { return item.Name }))
	})

	t.Run("TestGQLFieldParsingNested", func(t *testing.T) {
		cols, err := parseSelectColumns(&ActionRequest{
			RequestQuery: `query MyQuery{ dw_query_groupings_v2(where:{a:{_eq:1},b:{_eq:2},c:{_eq:3}}){ rows{ a b c}}}`,
		})

		assert.Nil(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, lo.Map(cols, func(item query.QueryColumn, index int) string { return item.Name }))
	})

	t.Run("TestGQLFieldParsingWithExpr", func(t *testing.T) {
		cols, err := parseSelectColumns(&ActionRequest{
			RequestQuery: `query MyQuery{ dw_query_groupings_v2(where:{a:{_eq:1},b:{_eq:2},c:{_eq:3}}){k:a(x: "y") b c}}`,
		})

		assert.Nil(t, err)
		assert.Equal(t, []query.QueryColumn{{Name: "a", Expr: "x", Args: []string{"y"}}, {Name: "b", Args: []string{}}, {Name: "c", Args: []string{}}}, cols)
	})

	t.Run("TestGQLFieldParsingWithMultiQuery", func(t *testing.T) {
		cols, err := parseSelectColumns(&ActionRequest{
			RequestQuery: "\nquery ListWarehouses($limit: Int, $offset: Int, $startDate: timestamp, $endDate: timestamp) {\n  cloud_resourses_aggregate(where: {id:{_eq:\"86a51daa-faf0-40c5-a276-7d91b56c6380\"},type:{_eq:\"Compute\"},cloud_account:{cloud_provider:{_eq:Snowflake}}}) {\n    aggregate {\n      count\n    }\n  }\n  cloud_resourses(where: {id:{_eq:\"86a51daa-faf0-40c5-a276-7d91b56c6380\"},type:{_eq:\"Compute\"},cloud_account:{cloud_provider:{_eq:Snowflake}}}, limit: $limit, offset: $offset, order_by:{cloud_resource_metrics_aggregate:{sum:{value:desc_nulls_last}}}) {\n    name\n    id\n    status\n    is_active\n    cloud_account {\n      id\n      account_name\n    }\n    compute_credit: cloud_resource_metrics_aggregate(where: {metric: {_eq: \"compute_credit\"}}) {\n      aggregate {\n        sum {\n          value\n        }\n      }\n    }\n    spends_aggregate(where:{_and:[{date:{_gte: $startDate}}, {date:{_lte: $endDate}}]}){\n      aggregate{\n        sum{\n          amount\n        }\n      }\n    }\n    recommendations_aggregate(where:{status:{_in:[Open, Assigned]}}){\n      aggregate{\n        count\n        sum{\n          estimated_savings\n        }\n      }\n    }\n  }\n  dw_query_groupings: dw_query_groupings_v2(where:{resource_id:{_eq:\"86a51daa-faf0-40c5-a276-7d91b56c6380\"}}){\n    rows{\n      tenant_id\n      account_id\n      warehouse_name\n      query_count\n      sum_query_exec_duration_micro\n      sum_bill\n    }\n  }\n}",
			Action: ActionRequestAction{
				Name: "dw_query_groupings_v2",
			},
		})
		assert.Nil(t, err)
		assert.Equal(t, []string{"tenant_id", "account_id", "warehouse_name", "query_count", "sum_query_exec_duration_micro", "sum_bill"}, lo.Map(cols, func(item query.QueryColumn, index int) string { return item.Name }))
	})
}

// TestParseSelectColumnsMalformed covers GraphQL shapes that previously triggered
// unchecked type assertions / index access and panicked the request goroutine.
// Each must now return cleanly (an error or a result), never panic.
func TestParseSelectColumnsMalformed(t *testing.T) {
	t.Run("FragmentOnlyDefinition", func(t *testing.T) {
		// First definition is a FragmentDefinition, not an OperationDefinition.
		assert.NotPanics(t, func() {
			_, err := parseSelectColumns(&ActionRequest{
				RequestQuery: `fragment F on T { a b }`,
			})
			assert.Error(t, err)
		})
	})

	t.Run("InlineFragmentSelection", func(t *testing.T) {
		// Top-level selection is an inline fragment, not a *ast.Field.
		assert.NotPanics(t, func() {
			_, err := parseSelectColumns(&ActionRequest{
				RequestQuery: `query MyQuery { ... on Foo { a b } }`,
				Action:       ActionRequestAction{Name: "foo"},
			})
			assert.Error(t, err)
		})
	})

	t.Run("RootFieldNoSubSelections", func(t *testing.T) {
		// Root field has no selection set; previously a nil-deref panic.
		cols, err := parseSelectColumns(&ActionRequest{
			RequestQuery: `query MyQuery { foo }`,
			Action:       ActionRequestAction{Name: "foo"},
		})
		assert.Nil(t, err)
		assert.Empty(t, cols)
	})

	t.Run("NonStringFieldArgument", func(t *testing.T) {
		// A non-string argument literal (boolean) previously panicked on the
		// unchecked .(string) assertion; it must now be stringified instead.
		cols, err := parseSelectColumns(&ActionRequest{
			RequestQuery: `query MyQuery { foo { k:a(x: true) b } }`,
			Action:       ActionRequestAction{Name: "foo"},
		})
		assert.Nil(t, err)
		assert.Equal(t, []string{"a", "b"}, lo.Map(cols, func(item query.QueryColumn, index int) string { return item.Name }))
		assert.Equal(t, []string{"true"}, cols[0].Args)
	})

	t.Run("MultiSelectionActionNotFound", func(t *testing.T) {
		// Multiple top-level selections but the action name matches none: must
		// error rather than silently projecting the first field's columns.
		_, err := parseSelectColumns(&ActionRequest{
			RequestQuery: `query MyQuery { foo { a } bar { b } }`,
			Action:       ActionRequestAction{Name: "baz"},
		})
		assert.Error(t, err)
	})
}
