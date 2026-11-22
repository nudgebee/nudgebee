package reports

import (
	"nudgebee/services/query"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Daily Recap and the events summary leave a row out only when it is on an
// account with hidden namespaces and in one of them; rows with no namespace and
// other accounts' rows stay.
func TestReportQueriesLeaveOutOwnNamespaces(t *testing.T) {
	own := map[string][]string{
		"00000000-0000-0000-0000-0000000000a1": {"acme-nudgebee", "nudgebee-agent"},
	}
	accounts := []string{"00000000-0000-0000-0000-0000000000a1", "00000000-0000-0000-0000-0000000000a2"}
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)

	for name, tc := range map[string]struct {
		request query.QueryRequest
		column  string
	}{
		"daily recap events":   {prepareCombinedHighlightQuery("tenant", "2026-10-28", "2026-10-29", accounts, own), "subject_namespace"},
		"events summary count": {prepareDailyTroubleshootCountsQuery("tenant", "2026-10-28", "2026-10-29", accounts, own), "subject_namespace"},
		"events summary rows":  {prepareTroubleshootSummaryQuery("tenant", "2026-10-28", "2026-10-29", accounts, own), "subject_namespace"},
		"daily recap savings":  {prepareRecommendationQuery("tenant", accounts, own), "resource_k8s_namespace"},
	} {
		t.Run(name, func(t *testing.T) {
			tableDef, ok := query.GetTableMetadata(tc.request.Table)
			require.True(t, ok)
			sql, err := query.GenerateSqlQuery(ctx, "", tc.request, tableDef)
			require.NoError(t, err)
			assert.Contains(t, sql, "'00000000-0000-0000-0000-0000000000a1'")
			assert.Contains(t, sql, "NOT IN ('acme-nudgebee','nudgebee-agent')")
			assert.Contains(t, sql, "IS NULL")
		})
	}

	t.Run("no hidden namespaces adds nothing", func(t *testing.T) {
		assert.Empty(t, notOwnNamespaceClauses(nil, "subject_namespace"))
		assert.Empty(t, notOwnNamespaceClauses(map[string][]string{"a1": nil}, "subject_namespace"))
		request := prepareCombinedHighlightQuery("tenant", "2026-10-28", "2026-10-29", accounts, nil)
		assert.Len(t, request.Where.And, 5)
	})
}
