package query

import (
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func fromStringColsToQueryCols(cols []string) []QueryColumn {
	var queryCols []QueryColumn
	for _, col := range cols {
		queryCols = append(queryCols, QueryColumn{
			Name: col,
		})
	}
	return queryCols
}

func TestQueryGeneration(t *testing.T) {
	t.Run("TestQueryGenerationGrouping", func(t *testing.T) {
		queryRequest := QueryRequest{
			Table: "dw_query_groupings_v2",
			Columns: fromStringColsToQueryCols([]string{
				"tenant_id",
				"account_id",
				"database_name",
				"avg_query_exec_duration_micro",
			}),
			Where: QueryWhereClause{
				Binary: map[string]map[BinaryWhereClauseType]any{
					"tenant_id": {
						"_eq": "tenant_1",
					},
				},
			},
			OrderBy: []QueryOrderBy{
				{
					Column: "avg_query_exec_duration_micro",
					Order:  "desc",
				},
			},
			Limit: 10,
		}
		query, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), uuid.NewString(), queryRequest, table_metadata["dw_query_groupings_v2"])
		assert.Nil(t, err)
		expectedQuery := "SELECT cast(tenant_id as TEXT) AS tenant_id,cast(account_id as TEXT) AS account_id,cast(database_name as TEXT) AS database_name,avg(query_exec_duration_micro) AS avg_query_exec_duration_micro FROM dw_queries WHERE (tenant_id = 'tenant_1') GROUP BY cast(tenant_id as TEXT),cast(account_id as TEXT),cast(database_name as TEXT) ORDER BY avg_query_exec_duration_micro  DESC  LIMIT 10"
		assert.NotEmpty(t, query)
		assert.Equal(t, expectedQuery, query)
	})
	t.Run("TestQueryGenerationNormal", func(t *testing.T) {
		queryRequest := QueryRequest{
			Table: "dw_queries_v2",
			Columns: fromStringColsToQueryCols([]string{
				"tenant_id",
				"account_id",
				"database_name",
			}),
			Where: QueryWhereClause{
				Binary: map[string]map[BinaryWhereClauseType]any{
					"tenant_id": {
						"_eq": "tenant_1",
					},
				},
			},
			OrderBy: []QueryOrderBy{
				{
					Column: "tenant_id",
					Order:  "desc",
				},
			},
			Limit: 10,
		}
		query, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), uuid.NewString(), queryRequest, table_metadata["dw_queries_v2"])
		assert.Nil(t, err)
		expectedQuery := "SELECT cast(tenant_id as TEXT) AS tenant_id,cast(account_id as TEXT) AS account_id,cast(database_name as TEXT) AS database_name FROM dw_queries WHERE (tenant_id = 'tenant_1') ORDER BY tenant_id  DESC  LIMIT 10"
		assert.NotEmpty(t, query)
		assert.Equal(t, expectedQuery, query)
	})
}

func TestQueryGenerationOnBuilder(t *testing.T) {
	t.Run("TestQueryGenerationGroupingBuilder", func(t *testing.T) {
		queryRequest := QueryRequest{
			Table: "resource_groupings_v2",
			Columns: fromStringColsToQueryCols([]string{
				"tenant_id",
				"account_id",
				"resource_service_name",
				"count_resource",
				"sum_spend_amount",
				"sum_recommendation_estimated_savings",
				"count_recommendation",
			}),
			Where: QueryWhereClause{
				Binary: map[string]map[BinaryWhereClauseType]any{
					"account_id": {
						"_eq": testenv.FakeAccountID,
					},
					"resource_status": {
						"_eq": "Active",
					},
					"recommendation_status": {
						"_eq": "Open",
					},
					"resource_service_name": {
						"_eq": "AmazonEC2",
					},
					"spend_date": {
						"_between": map[string]any{
							"_gte": "2025-01-01T00:00:00Z",
							"_lte": "2025-05-01T00:00:00Z",
						},
					},
				},
			},
			GroupBy: []string{
				"tenant_id",
				"account_id",
				"resource_service_name",
				"resource_id",
				"resource_name",
			},
			OrderBy: []QueryOrderBy{
				{
					Column: "count_recommendation",
					Order:  "desc",
				},
			},
			Limit: 10,
		}
		query, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), uuid.NewString(), queryRequest, table_metadata["resource_groupings_v2"])
		assert.Nil(t, err)
		expectedQuery := "SELECT cast(tenant_id as TEXT) AS tenant_id,cast(account_id as TEXT) AS account_id,cast(service_name as TEXT) AS resource_service_name,sum(resource_count) AS count_resource,sum(spend_amount) AS sum_spend_amount,sum(recommendation_estimated_savings) AS sum_recommendation_estimated_savings,sum(recommendation_count) AS count_recommendation FROM (\n\t\t\t\t\tselect cr.tenant as tenant_id, cr.account as account_id, cr.id, cr.name, cr.service_name, cr.status, cr.\"type\", cr.region, cr.arn, cr.tags\n\t\t\t\t\t\t, cr.resource_count::int\n\t\t\t\t\t\t, s.spend_amount::float\n\t\t\t\t\t\t, r.recommendation_count::int, r.recommendation_estimated_savings::float\n\t\t\t\t\tfrom (select tenant, account, id, name, service_name, status, \"type\", region, arn, tags, count(*) as resource_count from cloud_resourses cr1 where (service_name = 'AmazonEC2') AND (status = 'Active') AND account = '22222222-2222-2222-2222-222222222222' group by tenant, account, id, name, service_name, status, \"type\", region, arn, tags) cr\n\t\t\t\t\tleft join (select sum(spend_amount) as spend_amount, cloud_resource_id, cloud_account from (select amount as spend_amount, \"date\" as spend_date, cloud_resource_id, cloud_account from spends) spends1 where ((spend_date >= '2025-01-01T00:00:00Z' AND spend_date <= '2025-05-01T00:00:00Z')) group by cloud_resource_id, cloud_account) s on s.cloud_resource_id = cr.id and s.cloud_account = cr.account\n\t\t\t\t\tleft join (select count(*) as recommendation_count, sum(recommendation_estimated_savings) as recommendation_estimated_savings, resource_id, cloud_account_id from (select id as recommendation_id, rule_name as recommendation_rule_name, category as recommendation_category, status as recommendation_status, severity as recommendation_severity, estimated_savings as recommendation_estimated_savings, resource_id, cloud_account_id from recommendation ) r1 where (recommendation_status = 'Open') group by resource_id, cloud_account_id ) r on r.resource_id = cr.id and r.cloud_account_id = cr.account\n\t\t\t\t) as resource_group GROUP BY cast(tenant_id as TEXT),cast(account_id as TEXT),cast(service_name as TEXT),cast(id as TEXT),cast(name as TEXT) ORDER BY count_recommendation  DESC  LIMIT 10"
		assert.NotEmpty(t, query)
		assert.Equal(t, expectedQuery, query)
	})

	t.Run("TestQueryGenerationGroupingBuilder2", func(t *testing.T) {
		testenv.RequireMetastore(t)
		accountID := testenv.RequireEnv(t, testenv.Account)[testenv.Account]
		queryRequest := QueryRequest{
			Table: "resource_groupings_v2",
			Columns: fromStringColsToQueryCols([]string{
				"tenant_id",
				"account_id",
				"resource_service_name",
				"count_resource",
				"sum_spend_amount",
				"sum_recommendation_estimated_savings",
				"count_recommendation",
			}),
			Where: QueryWhereClause{
				Binary: map[string]map[BinaryWhereClauseType]any{
					"account_id": {
						"_eq": accountID,
					},
					"recommendation_status": {
						"_eq": "Open",
					},
					"spend_date": {
						"_between": map[string]any{
							"_gte": "2025-01-01T00:00:00Z",
							"_lte": "2025-05-01T00:00:00Z",
						},
					},
				},
			},
			GroupBy: []string{
				"tenant_id",
				"account_id",
				"resource_service_name",
			},
			OrderBy: []QueryOrderBy{
				{
					Column: "count_recommendation",
					Order:  "desc",
				},
			},
			Limit: 10,
		}
		query, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), uuid.NewString(), queryRequest, table_metadata["resource_groupings_v2"])
		assert.Nil(t, err)
		assert.NotEmpty(t, query)
		queryResponse, err := executeSqlQuery(database.Metastore, query, []any{}, queryRequest.Limit)
		assert.Nil(t, err)
		assert.NotEmpty(t, queryResponse)
	})
}

func TestWhereGeneration(t *testing.T) {
	t.Run("TestWhereGeneration", func(t *testing.T) {
		query, err := generateWhereClause(QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{
				"tenant_id": {
					"_in": []string{"tenant_1", "tenant_2"},
				},
			},
		}, table_metadata["dw_query_groupings_v2"])
		assert.Nil(t, err)
		expectedQuery := "(tenant_id IN ('tenant_1','tenant_2'))"
		assert.NotEmpty(t, query)
		assert.Equal(t, expectedQuery, query)
	})

	t.Run("TestWhereGenerationComplex", func(t *testing.T) {
		query, err := generateWhereClause(QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{
				"tenant_id": {
					"_in": []string{"tenant_1", "tenant_2"},
				},
			},
			And: []QueryWhereClause{
				{
					Binary: map[string]map[BinaryWhereClauseType]any{
						"account_id": {
							"_in": []string{"account_1", "account_2"},
						},
					},
				},
				{
					Binary: map[string]map[BinaryWhereClauseType]any{
						"resource_id": {
							"_eq": "resource_1",
						},
					},
				},
			},
		}, table_metadata["dw_query_groupings_v2"])
		assert.Nil(t, err)
		expectedQuery := "((account_id IN ('account_1','account_2')) AND (resource_id = 'resource_1')) AND (tenant_id IN ('tenant_1','tenant_2'))"
		assert.NotEmpty(t, query)
		assert.Equal(t, expectedQuery, query)
	})
}

func TestSerialization(t *testing.T) {

	t.Run("TestSerialization", func(t *testing.T) {
		whereCluase := QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{
				"tenant_id": {
					"_in": []string{"tenant_1", "tenant_2"},
				},
			},
			And: []QueryWhereClause{
				{
					Binary: map[string]map[BinaryWhereClauseType]any{
						"account_id": {
							"_in": []string{"account_1", "account_2"},
						},
					},
				},
				{
					Binary: map[string]map[BinaryWhereClauseType]any{
						"resource_id": {
							"_eq": "resource_1",
						},
					},
				},
			},
		}
		data, err := common.MarshalJson(whereCluase)
		assert.Nil(t, err)
		assert.NotEmpty(t, data)
		assert.Equal(t, `{"_binary":{"tenant_id":{"_in":["tenant_1","tenant_2"]}},"_and":[{"_binary":{"account_id":{"_in":["account_1","account_2"]}}},{"_binary":{"resource_id":{"_eq":"resource_1"}}}]}`, string(data))
	})

	t.Run("TestDeserialization", func(t *testing.T) {
		data := `{"_binary":{"tenant_id":{"_in":["tenant_1","tenant_2"]}},"_and":[{"_binary":{"account_id":{"_in":["account_1","account_2"]}}},{"_binary":{"resource_id":{"_eq":"resource_1"}}}]}`
		whereClause := QueryWhereClause{}
		err := common.UnmarshalJson([]byte(data), &whereClause)
		assert.Nil(t, err)
		assert.NotEmpty(t, whereClause)
		assert.NotEmpty(t, whereClause.Binary)
		assert.NotEmpty(t, whereClause.And)
		assert.Equal(t, 2, len(whereClause.And))
	})
}

func TestAgentWarehouseQueryGeneration(t *testing.T) {
	t.Run("TestQueryGenerationUsingGenerator", func(t *testing.T) {
		testenv.RequireWarehouse(t)
		accountId := testenv.RequireEnv(t, testenv.Account)[testenv.Account]
		queryRequest := QueryRequest{
			Table: "traces_v2",
			Columns: fromStringColsToQueryCols([]string{
				"trace_id",
				"span_id",
			}),
			Where: QueryWhereClause{
				Binary: map[string]map[BinaryWhereClauseType]any{
					"account_id": {
						"_eq": accountId,
					},
				},
			},
			OrderBy: []QueryOrderBy{
				{
					Column: "timestamp",
					Order:  "desc",
				},
			},
			Limit: 10,
		}
		query, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), accountId, queryRequest, table_metadata["traces_v2"])
		assert.Nil(t, err)
		expectedQuery := "SELECT cast(tenant_id as TEXT) AS tenant_id,cast(account_id as TEXT) AS account_id,cast(database_name as TEXT) AS database_name FROM dw_queries WHERE (tenant_id = 'tenant_1') ORDER BY tenant_id  DESC  LIMIT 10"
		assert.NotEmpty(t, query)
		assert.Equal(t, expectedQuery, query)
	})
}
