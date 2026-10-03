package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/security"
)

// recommendationsV2Route classifies which of the two recommendations_v2
// subquery shapes a request takes, by inspecting the generated SQL.
func recommendationsV2Route(t *testing.T, request QueryRequest) string {
	t.Helper()
	request.Table = "recommendations_v2"
	sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendations_v2"])
	require.NoError(t, err)
	if strings.Contains(sql, "ROW_NUMBER") {
		return full
	}
	return lean
}

// recommendations_v2 backs the main recommendations table and the
// vulnerabilities list — neither reads is_primary_recommendation, so both
// must take the lean (no ROW_NUMBER) path. Only a caller that actually asks
// for that column (today, only the dashboard-panel "Is primary" filter/
// column) should pay for the window. Getting this routing wrong is silent at
// compile time: the lean path would either omit a column a caller needs, or
// the full path would needlessly re-add the window's cost to every request.
func TestRecommendationsV2Routing(t *testing.T) {
	tests := []struct {
		name    string
		request QueryRequest
		want    string
	}{
		{"main recommendations table columns take the lean path", QueryRequest{
			Columns: cols("id", "resource_name", "resource_type", "resource_cloud_service", "severity", "category", "rule_name", "estimated_savings", "status"),
		}, lean},
		{"vulnerabilities list columns take the lean path", QueryRequest{
			Columns: cols("id", "account_id", "resource_id", "resource_name", "severity", "status", "updated_at", "created_at", "recommendation"),
		}, lean},
		{"is_primary_recommendation column forces the window", QueryRequest{
			Columns: cols("id", "resource_name", "is_primary_recommendation"),
		}, full},
		{"is_primary_recommendation in where forces the window", QueryRequest{
			Columns: cols("id", "resource_name"),
			Where:   QueryWhereClause{Binary: BinaryWhereClause{"is_primary_recommendation": {Eq: true}}},
		}, full},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, recommendationsV2Route(t, tc.request))
		})
	}
}

// The lean path must still project every column the main recommendations
// table and the vulnerabilities list actually request — dropping the window
// must not drop the display columns that ride along with it.
func TestRecommendationsV2_LeanPathKeepsDisplayColumns(t *testing.T) {
	request := QueryRequest{
		Table:   "recommendations_v2",
		Columns: cols("id", "resource_name", "resource_type", "resource_cloud_service", "resource_k8s_namespace", "resource_meta"),
	}
	sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendations_v2"])
	require.NoError(t, err)
	assert.NotContains(t, sql, "ROW_NUMBER")
	assert.Contains(t, sql, "LEFT JOIN cloud_resourses cr ON cr.id = r.resource_id")
	assert.Contains(t, sql, "resource_k8s_namespace")
	assert.Contains(t, sql, "resource_cloud_service")
}

// Vulnerabilities join gating (needsVulnJoin) is independent of needsWindow —
// a request needing both the vuln join and the window must still get both,
// with valid SQL (no missing comma between the two conditionally-spliced
// column groups), mirroring the regression covered on the sibling table.
func TestRecommendationsV2_VulnJoinWithWindow(t *testing.T) {
	request := QueryRequest{
		Table:   "recommendations_v2",
		Columns: cols("id", "vuln_id", "package_name", "is_primary_recommendation"),
	}
	sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendations_v2"])
	require.NoError(t, err)
	assert.Contains(t, sql, "ROW_NUMBER")
	assert.Contains(t, sql, "LEFT JOIN vulnerabilities v ON v.id = r.vulnerability_id")
}
