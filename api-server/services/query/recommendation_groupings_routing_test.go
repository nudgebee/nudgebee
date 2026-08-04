package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/services/security"
)

// The recommendation_groupings_v2 DefGenerator picks one of three subquery
// shapes from the requested columns. Getting that choice wrong is silent at
// compile time and fails at runtime with `column "<x>" does not exist`, so the
// routing is pinned here: any column whose Def is only available from the
// cloud_resourses / cloud_accounts joins MUST appear in joinRequiringCols.
func TestRecommendationGroupingsRouting(t *testing.T) {
	const (
		fast = "fast" // no CTE at all
		lean = "lean" // ROW_NUMBER() window, no cloud_resourses join
		full = "full" // window + both joins + display projection
	)

	route := func(t *testing.T, request QueryRequest) string {
		t.Helper()
		request.Table = "recommendation_groupings_v2"
		sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
		assert.NoError(t, err)
		switch {
		case strings.Contains(sql, "cloud_resourses"):
			return full
		case strings.Contains(sql, "ROW_NUMBER"):
			return lean
		default:
			return fast
		}
	}

	tests := []struct {
		name    string
		request QueryRequest
		want    string
	}{
		{"count only takes the fast path", QueryRequest{Columns: cols("count")}, fast},
		{"savings roll-up needs the window but no join", QueryRequest{Columns: cols("count", "sum_estimated_savings")}, lean},
		{"is_primary_recommendation in where needs the window", QueryRequest{
			Columns: cols("count"),
			Where:   QueryWhereClause{Binary: BinaryWhereClause{"is_primary_recommendation": {Eq: true}}},
		}, lean},
		{"a display column forces the join", QueryRequest{Columns: cols("count", "sum_estimated_savings", "resource_cloud_service")}, full},
		{"resource_names aggregates resource_name, so it forces the join", QueryRequest{
			Columns: cols("vuln_id", "count", "resource_names"),
			GroupBy: []string{"vuln_id"},
		}, full},
		{"a namespace-scoped role's injected filter forces the join", QueryRequest{
			Columns: cols("count", "sum_estimated_savings"),
			Where: QueryWhereClause{And: []QueryWhereClause{
				{Binary: BinaryWhereClause{"resource_k8s_namespace": {In: []string{"default"}}}},
			}},
		}, full},
		{"grouping by a display column forces the join", QueryRequest{
			Columns: cols("count", "resource_region"),
			GroupBy: []string{"resource_region"},
		}, full},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, route(t, tc.request))
		})
	}
}
