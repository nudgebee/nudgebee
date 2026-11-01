package query

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/security"
)

const (
	fast = "fast" // no CTE at all
	lean = "lean" // ROW_NUMBER() window, no cloud_resourses join
	full = "full" // window + both joins + display projection
)

// route classifies which of the three recommendation_groupings_v2 subquery
// shapes a request takes, by inspecting the generated SQL.
func route(t *testing.T, request QueryRequest) string {
	t.Helper()
	request.Table = "recommendation_groupings_v2"
	sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
	require.NoError(t, err)
	switch {
	case strings.Contains(sql, "cloud_resourses"):
		return full
	case strings.Contains(sql, "ROW_NUMBER"):
		return lean
	default:
		return fast
	}
}

// The recommendation_groupings_v2 DefGenerator picks one of three subquery
// shapes from the requested columns. Getting that choice wrong is silent at
// compile time and fails at runtime with `column "<x>" does not exist`, so the
// routing is pinned here: any column whose Def is only available from the
// cloud_resourses / cloud_accounts joins MUST appear in joinRequiringCols.
func TestRecommendationGroupingsRouting(t *testing.T) {
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

func TestRecommendationGroupings_VMPackageFieldsUseVulnerabilityJoin(t *testing.T) {
	request := QueryRequest{
		Table:   "recommendation_groupings_v2",
		Columns: cols("package_name", "recommendation", "count"),
		GroupBy: []string{"package_name"},
	}
	sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
	require.NoError(t, err)
	assert.Contains(t, sql, "LEFT JOIN vulnerabilities v ON v.id = r.vulnerability_id")
	assert.Contains(t, sql, "r1.v_package_name")
	assert.Contains(t, sql, "r1.recommendation - 'package_version'")
}

// See the DefGenerator comment: severity pushdown is only safe for
// rule_name='image_scan'.
func TestRecommendationGroupings_SeverityPushdownGatedToImageScanOnly(t *testing.T) {
	displayCols := cols("count", "resource_cloud_service", "severity")

	pushedDown := QueryRequest{
		Table:   "recommendation_groupings_v2",
		Columns: displayCols,
		Where: QueryWhereClause{Binary: BinaryWhereClause{
			"rule_name": {Eq: "image_scan"},
			"severity":  {Eq: "High"},
		}},
	}
	sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", pushedDown, table_metadata["recommendation_groupings_v2"])
	require.NoError(t, err)
	assert.Contains(t, sql, "r.severity = 'High'", "rule_name='image_scan' should push severity into the CTE")

	notPushedCases := []struct {
		name       string
		ruleFilter map[BinaryWhereClauseType]any
	}{
		{"rule_name IN list including other rules", map[BinaryWhereClauseType]any{In: []any{"image_scan", "vm_package_vulnerability"}}},
		{"rule_name = vm_package_vulnerability", map[BinaryWhereClauseType]any{Eq: "vm_package_vulnerability"}},
		{"no rule_name filter at all", nil},
	}
	for _, tc := range notPushedCases {
		t.Run(tc.name, func(t *testing.T) {
			binary := BinaryWhereClause{"severity": {Eq: "High"}}
			if tc.ruleFilter != nil {
				binary["rule_name"] = tc.ruleFilter
			}
			request := QueryRequest{
				Table:   "recommendation_groupings_v2",
				Columns: displayCols,
				Where:   QueryWhereClause{Binary: binary},
			}
			sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
			require.NoError(t, err)
			assert.NotContains(t, sql, "r.severity", "severity must stay out of the CTE unless rule_name is exactly 'image_scan'")
		})
	}
}

// See the DefGenerator comment: status/rule_name pushdown is only safe on the
// fast path, or for rule_name='image_scan'.
func TestRecommendationGroupings_StatusAndRuleNamePushdownGatedToWindowSafety(t *testing.T) {
	displayCols := cols("count", "resource_cloud_service", "status", "rule_name")

	windowedCases := []struct {
		name   string
		where  BinaryWhereClause
		wantIn string // a literal expected to survive somewhere (outer WHERE)
	}{
		{"status filter on a windowed request", BinaryWhereClause{"status": {Eq: "Archive"}}, "Archive"},
		{"rule_name filter (not image_scan) on a windowed request", BinaryWhereClause{"rule_name": {Eq: "vm_package_vulnerability"}}, "vm_package_vulnerability"},
	}
	for _, tc := range windowedCases {
		t.Run(tc.name, func(t *testing.T) {
			// Not route()+GenerateSqlQuery: Where.Binary is a map, so route()'s
			// call would consume the filter before this one runs.
			request := QueryRequest{
				Table:   "recommendation_groupings_v2",
				Columns: displayCols,
				Where:   QueryWhereClause{Binary: tc.where},
			}
			sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
			require.NoError(t, err)
			require.Contains(t, sql, "ROW_NUMBER", "expected this request shape to take a windowed (lean/full) path")
			// (bare "r.status" also appears in the window's own ORDER BY tie-break)
			assert.NotContains(t, sql, "r.status = 'Archive'", "status must stay out of the CTE's WHERE on a windowed path")
			assert.NotContains(t, sql, "r.rule_name", "rule_name must stay out of the CTE on a windowed path unless it's exactly 'image_scan'")
			assert.Contains(t, sql, tc.wantIn, "the filter must still be applied somewhere (the outer WHERE), not silently dropped")
		})
	}

	t.Run("rule_name='image_scan' is still pushed on a windowed request", func(t *testing.T) {
		request := QueryRequest{
			Table:   "recommendation_groupings_v2",
			Columns: displayCols,
			Where:   QueryWhereClause{Binary: BinaryWhereClause{"rule_name": {Eq: "image_scan"}}},
		}
		sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
		require.NoError(t, err)
		require.Contains(t, sql, "ROW_NUMBER", "expected this request shape to take a windowed (lean/full) path")
		assert.Contains(t, sql, "r.rule_name = 'image_scan'")
	})

	t.Run("status and rule_name both push down on the fast path", func(t *testing.T) {
		request := QueryRequest{
			Table:   "recommendation_groupings_v2",
			Columns: cols("count"),
			Where: QueryWhereClause{Binary: BinaryWhereClause{
				"status":    {Eq: "Archive"},
				"rule_name": {Eq: "vm_package_vulnerability"},
				"severity":  {Eq: "High"},
			}},
		}
		sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
		require.NoError(t, err)
		require.NotContains(t, sql, "ROW_NUMBER", "expected this request shape to take the fast path")
		assert.Contains(t, sql, "r.status = 'Archive'")
		assert.Contains(t, sql, "r.rule_name = 'vm_package_vulnerability'")
		assert.Contains(t, sql, "r.severity = 'High'")
	})
}

// nonTerminalPushdown is what nonTerminalStatusPushdown adds to the dedupe CTE.
const nonTerminalPushdown = "AND (r.status IS NULL OR ((r.status < 'Archive' OR r.status > 'Archive') AND (r.status < 'Closed' OR r.status > 'Closed')))"

// A savings roll-up filtered to live statuses used to rank every archived row
// of the account and then discard it (#35172). Terminal rows sort last in the
// window, so leaving them out cannot change the rank of a live row — but only
// when the request returns no terminal row itself. The request's own status
// filter must stay on the outer query either way (#38469).
func TestRecommendationGroupings_TerminalRowsStayOutOfTheWindow(t *testing.T) {
	savings := cols("count", "sum_estimated_savings")
	display := cols("count", "sum_estimated_savings", "resource_cloud_service")
	status := func(filter map[BinaryWhereClauseType]any) QueryWhereClause {
		return QueryWhereClause{Binary: BinaryWhereClause{"status": filter}}
	}

	tests := []struct {
		name       string
		columns    []QueryColumn
		where      QueryWhereClause
		wantPushed bool
		wantOuter  string // the request's own status filter, still applied outside the CTE
	}{
		{"live statuses on the window-only path", savings,
			status(map[BinaryWhereClauseType]any{In: []any{"Open", "Assigned"}}), true, "status IN ('Open','Assigned')"},
		{"live statuses on the joined path", display,
			status(map[BinaryWhereClauseType]any{In: []any{"Open", "Assigned"}}), true, "status IN ('Open','Assigned')"},
		{"a single live status", savings,
			status(map[BinaryWhereClauseType]any{Eq: "Open"}), true, "status = 'Open'"},
		{"live statuses sent inside an _and list", savings,
			QueryWhereClause{And: []QueryWhereClause{status(map[BinaryWhereClauseType]any{In: []string{"Open"}})}}, true, "status IN ('Open')"},

		{"a terminal status in the list", savings,
			status(map[BinaryWhereClauseType]any{In: []any{"Open", "Archive"}}), false, "status IN ('Open','Archive')"},
		{"a terminal status alone", savings,
			status(map[BinaryWhereClauseType]any{Eq: "Closed"}), false, "status = 'Closed'"},
		{"no status filter", savings, QueryWhereClause{}, false, ""},
		{"an empty list is no filter", savings,
			status(map[BinaryWhereClauseType]any{In: []any{}}), false, ""},
		{"a negative filter still returns Closed rows", savings,
			status(map[BinaryWhereClauseType]any{Nq: "Archive"}), false, "Archive"},
		{"a status filter under _or is conditional", savings,
			QueryWhereClause{Or: []QueryWhereClause{
				status(map[BinaryWhereClauseType]any{Eq: "Open"}),
				{Binary: BinaryWhereClause{"severity": {Eq: "High"}}},
			}}, false, "status = 'Open'"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := QueryRequest{Table: "recommendation_groupings_v2", Columns: tc.columns, Where: tc.where}
			sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
			require.NoError(t, err)
			require.Contains(t, sql, "ROW_NUMBER", "expected this request shape to take a windowed (lean/full) path")

			assert.Equal(t, tc.wantPushed, strings.Contains(sql, nonTerminalPushdown), "terminal-row exclusion in the CTE")
			assert.NotContains(t, sql, "r.status IN ('Open'", "the request's status filter must not be pushed into the CTE")
			assert.NotContains(t, sql, "r.status = 'Open'", "the request's status filter must not be pushed into the CTE")
			if tc.wantOuter != "" {
				assert.Contains(t, sql, tc.wantOuter, "the request's status filter must still apply on the outer query")
			}
		})
	}

	t.Run("the fast path has no window and pushes the status filter itself", func(t *testing.T) {
		request := QueryRequest{
			Table:   "recommendation_groupings_v2",
			Columns: cols("count"),
			Where:   status(map[BinaryWhereClauseType]any{In: []any{"Open", "Assigned"}}),
		}
		sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
		require.NoError(t, err)
		require.NotContains(t, sql, "ROW_NUMBER", "expected this request shape to take the fast path")
		assert.Contains(t, sql, "r.status IN ('Open','Assigned')")
		assert.NotContains(t, sql, nonTerminalPushdown)
	})
}

// The pushdown is only exact while the window keeps sorting the same statuses
// last. Both tables spell that list inline in SQL; this ties it to the Go list
// the pushdown is built from.
func TestRecommendationDedupe_TerminalStatusesMatchTheWindow(t *testing.T) {
	terminalLast := "CASE WHEN r.status IN ('" + strings.Join(recommendationTerminalStatuses, "', '") + "') THEN 1 ELSE 0 END"
	requests := []QueryRequest{
		{Table: "recommendation_groupings_v2", Columns: cols("count", "sum_estimated_savings")},
		{Table: "recommendation_groupings_v2", Columns: cols("count", "sum_estimated_savings", "resource_cloud_service")},
		{Table: "recommendations_v2", Columns: cols("id", "is_primary_recommendation")},
	}
	for _, request := range requests {
		sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata[request.Table])
		require.NoError(t, err)
		require.Contains(t, sql, "ROW_NUMBER")
		assert.Contains(t, sql, terminalLast, "%s: the window must sort exactly recommendationTerminalStatuses last", request.Table)
	}
}

// Regression test for a missing-comma bug in the fast path's SELECT list: when
// a request needs the vulnerabilities join (package_name/vuln_id/recommendation)
// but no join- or window-requiring column, the fast path used to splice vulnCols
// between `r.*,` and `TRUE AS is_primary_recommendation` with no separating
// comma, producing a Postgres syntax error ("Unable to execute query" at the API
// boundary — the real cause was masked). This is exactly the request shape the VM
// dashboard's "Most affected packages" / "Most common vulnerabilities" panels send.
func TestRecommendationGroupings_FastPathVulnJoinHasNoMissingComma(t *testing.T) {
	tests := []struct {
		name    string
		request QueryRequest
	}{
		{"group by package_name", QueryRequest{
			Columns: cols("package_name", "count", "max_severity_weight"),
			GroupBy: []string{"package_name"},
		}},
		{"group by vuln_id", QueryRequest{
			Columns: cols("vuln_id", "count", "max_severity_weight"),
			GroupBy: []string{"vuln_id"},
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, fast, route(t, tc.request), "expected this request shape to take the fast path")

			sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", tc.request, table_metadata["recommendation_groupings_v2"])
			require.NoError(t, err)
			assert.Regexp(t, `,\s*TRUE AS is_primary_recommendation`, sql, "TRUE AS is_primary_recommendation must be preceded by a comma")
		})
	}
}

// distinctCount is the request a paged, grouped table sends for its total:
// columns ["count"] with column_transformations
// [{name: "count", expr: "distinct", args: [<the group column>]}].
func distinctCount(args ...string) []QueryColumn {
	return []QueryColumn{{Name: "count", Expr: "distinct", Args: args}}
}

// Regression test for #39843. A distinct count names its column only in the
// transformation's args — the one requested column is "count" — and
// generateColumnExpression rewrites it to count(DISTINCT <that column's Def>).
// The DefGenerator read column names alone, so a count over package_name or
// vuln_id skipped the vulnerabilities join while the outer SELECT still named
// r1.v_package_name / r1.v_vuln_id: Postgres `column does not exist`, surfaced
// as "Unable to execute query". The VM Vulnerabilities tab pages its Package and
// Vulnerability groupings on exactly this count, so both showed one page only.
func TestRecommendationGroupings_DistinctCountArgsRouteLikeTheColumn(t *testing.T) {
	const vulnJoin = "LEFT JOIN vulnerabilities v ON v.id = r.vulnerability_id"
	subqueryColumn := regexp.MustCompile(`r1\.(v_\w+)`)

	tests := []struct {
		arg          string
		wantSelect   string
		wantRoute    string
		wantVulnJoin bool
	}{
		{"package_name", "count(DISTINCT COALESCE(r1.v_package_name, r1.recommendation -> 'package' ->> 'name')) AS count", fast, true},
		{"vuln_id", "count(DISTINCT COALESCE(r1.v_vuln_id, r1.recommendation ->> 'vuln_id')) AS count", fast, true},
		// Worked before the fix and must keep its plan: a plain recommendation
		// column needs neither join.
		{"resource_id", "count(DISTINCT resource_id) AS count", fast, false},
		// The same gap on the cloud_resourses side: resource_name exists only in
		// the joined CTE.
		{"resource_name", "count(DISTINCT resource_name) AS count", full, false},
	}

	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			request := QueryRequest{Table: "recommendation_groupings_v2", Columns: distinctCount(tc.arg)}
			sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_groupings_v2"])
			require.NoError(t, err)

			assert.Contains(t, sql, "SELECT "+tc.wantSelect)
			assert.Equal(t, tc.wantRoute, route(t, request))
			assert.Equal(t, tc.wantVulnJoin, strings.Contains(sql, vulnJoin), "vulnerabilities join")
			// Valid SQL here means every r1.v_* the outer query names is a column
			// the subquery projects.
			for _, ref := range subqueryColumn.FindAllStringSubmatch(sql, -1) {
				assert.Regexp(t, `\bAS `+ref[1]+`\b`, sql, "outer query reads r1.%s, which the subquery does not project", ref[1])
			}
		})
	}
}

// The shared helper behind every other DefGenerator's conditional join has to
// agree: distinct args are column references, a date_unit's args are not.
func TestRequestReferencesColumns_DistinctArgsAreColumnReferences(t *testing.T) {
	gated := map[string]bool{"package_name": true}

	assert.True(t, requestReferencesColumns(QueryRequest{Columns: distinctCount("package_name")}, gated))
	assert.True(t, requestReferencesColumns(QueryRequest{Columns: distinctCount("resource_id", "package_name")}, gated))
	assert.False(t, requestReferencesColumns(QueryRequest{Columns: distinctCount("resource_id")}, gated))
	assert.False(t, requestReferencesColumns(QueryRequest{
		Columns: []QueryColumn{{Name: "timestamp", Expr: "date_unit", Args: []string{"package_name"}}},
	}, gated), "a date_unit's args are a unit and a period, never column names")
}

// The security groupings table asks the request two different questions. Which
// columns its subquery must project follows distinct args. Whether the
// scanned-but-clean images are listed does not: a distinct count over
// vulnerability_id skips their NULL, the same as count_vulnerability_id, so
// they stay. Only the per-CVE listing, which names the column, drops them.
func TestRecommendationSecurityGroupings_DistinctCountKeepsCleanImages(t *testing.T) {
	const cleanImages = "'image_scan_summary'"
	generate := func(request QueryRequest) string {
		t.Helper()
		request.Table = "recommendation_security_groupings_v2"
		sql, err := GenerateSqlQuery(security.NewRequestContextForSuperAdmin(nil, nil, nil), "", request, table_metadata["recommendation_security_groupings_v2"])
		require.NoError(t, err)
		return sql
	}

	counted := generate(QueryRequest{
		Columns: []QueryColumn{{Name: "image"}, {Name: "count", Expr: "distinct", Args: []string{"vulnerability_id"}}},
		GroupBy: []string{"image"},
	})
	assert.Contains(t, counted, "count(DISTINCT vulnerability_id) AS count")
	assert.Contains(t, counted, "r.vulnerability_id as vulnerability_id", "the subquery must project the counted column")
	assert.Contains(t, counted, cleanImages, "clean images must stay in a per-image list")

	listed := generate(QueryRequest{Columns: cols("vulnerability_id", "count"), GroupBy: []string{"vulnerability_id"}})
	assert.NotContains(t, listed, cleanImages, "the per-CVE listing must not get a NULL-CVE bucket")
}
