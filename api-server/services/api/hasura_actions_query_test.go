package api

import (
	"nudgebee/services/query"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

func TestGQLFieldParsing(t *testing.T) {
	t.Run("TestGQLFieldParsing", func(t *testing.T) {
		cols, err := parseHasuraSelectColumns(&HasuraActionRequest{
			RequestQuery: `query MyQuery{ dw_query_groupings_v2(where:{a:{_eq:1},b:{_eq:2},c:{_eq:3}}){a b c}}`,
		})

		assert.Nil(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, lo.Map(cols, func(item query.QueryColumn, index int) string { return item.Name }))
	})

	t.Run("TestGQLFieldParsingNested", func(t *testing.T) {
		cols, err := parseHasuraSelectColumns(&HasuraActionRequest{
			RequestQuery: `query MyQuery{ dw_query_groupings_v2(where:{a:{_eq:1},b:{_eq:2},c:{_eq:3}}){ rows{ a b c}}}`,
		})

		assert.Nil(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, lo.Map(cols, func(item query.QueryColumn, index int) string { return item.Name }))
	})

	t.Run("TestGQLFieldParsingWithExpr", func(t *testing.T) {
		cols, err := parseHasuraSelectColumns(&HasuraActionRequest{
			RequestQuery: `query MyQuery{ dw_query_groupings_v2(where:{a:{_eq:1},b:{_eq:2},c:{_eq:3}}){k:a(x: "y") b c}}`,
		})

		assert.Nil(t, err)
		assert.Equal(t, []query.QueryColumn{{Name: "a", Expr: "x", Args: []string{"y"}}, {Name: "b", Args: []string{}}, {Name: "c", Args: []string{}}}, cols)
	})

	t.Run("TestGQLFieldParsingWithMultiQuery", func(t *testing.T) {
		cols, err := parseHasuraSelectColumns(&HasuraActionRequest{
			RequestQuery: "\nquery ListWarehouses($limit: Int, $offset: Int, $startDate: timestamp, $endDate: timestamp) {\n  cloud_resourses_aggregate(where: {id:{_eq:\"86a51daa-faf0-40c5-a276-7d91b56c6380\"},type:{_eq:\"Compute\"},cloud_account:{cloud_provider:{_eq:Snowflake}}}) {\n    aggregate {\n      count\n    }\n  }\n  cloud_resourses(where: {id:{_eq:\"86a51daa-faf0-40c5-a276-7d91b56c6380\"},type:{_eq:\"Compute\"},cloud_account:{cloud_provider:{_eq:Snowflake}}}, limit: $limit, offset: $offset, order_by:{cloud_resource_metrics_aggregate:{sum:{value:desc_nulls_last}}}) {\n    name\n    id\n    status\n    is_active\n    cloud_account {\n      id\n      account_name\n    }\n    compute_credit: cloud_resource_metrics_aggregate(where: {metric: {_eq: \"compute_credit\"}}) {\n      aggregate {\n        sum {\n          value\n        }\n      }\n    }\n    spends_aggregate(where:{_and:[{date:{_gte: $startDate}}, {date:{_lte: $endDate}}]}){\n      aggregate{\n        sum{\n          amount\n        }\n      }\n    }\n    recommendations_aggregate(where:{status:{_in:[Open, Assigned]}}){\n      aggregate{\n        count\n        sum{\n          estimated_savings\n        }\n      }\n    }\n  }\n  dw_query_groupings: dw_query_groupings_v2(where:{resource_id:{_eq:\"86a51daa-faf0-40c5-a276-7d91b56c6380\"}}){\n    rows{\n      tenant_id\n      account_id\n      warehouse_name\n      query_count\n      sum_query_exec_duration_micro\n      sum_bill\n    }\n  }\n}",
			Action: HasuraActionRequestAction{
				Name: "dw_query_groupings_v2",
			},
		})
		assert.Nil(t, err)
		assert.Equal(t, []string{"tenant_id", "account_id", "warehouse_name", "query_count", "sum_query_exec_duration_micro", "sum_bill"}, lo.Map(cols, func(item query.QueryColumn, index int) string { return item.Name }))
	})
}
