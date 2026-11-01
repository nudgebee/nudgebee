package observability

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promBuilderKeys returns every metric key a builder answers, read out of its own
// source.
//
// A hand-written list would go stale the first time someone adds a key, and a key
// that quietly escapes these checks is exactly the failure this file exists to catch
// — cpu_usage and memory_usage ignored `aggregate` for as long as they did because
// nothing enumerated the keys that were NOT wrapped. Reading the AST means a new
// `case` is covered the moment it is written.
func promBuilderKeys(t *testing.T, funcName string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "prometheus_queries.go", nil, 0)
	require.NoError(t, err)

	// Keys are named by constant (see metrics_utilisation_keys.go); resolve each to the
	// string a request sends. Any other string literal in a case is collected as before.
	values := registryConstantValues(t)

	var keys []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				switch e := expr.(type) {
				case *ast.Ident:
					if key, ok := values[e.Name]; ok {
						keys = append(keys, key)
					}
				case *ast.BasicLit:
					if e.Kind != token.STRING {
						continue
					}
					key, err := strconv.Unquote(e.Value)
					require.NoError(t, err)
					keys = append(keys, key)
				}
			}
			return true
		})
	}
	require.NotEmptyf(t, keys, "found no case keys in %s", funcName)
	return keys
}

// The aggregate belongs to the request, so it must reach every key the request can
// name — not just the handful whose case remembered to ask for it.
//
// This is the defect the E2E found: at workload scope cpu_usage and memory_usage
// came back identical for aggregate=max and aggregate=avg, and the value was neither
// the maximum nor the average of the window but the reading at its end. An
// aggregated request is served as an instant query, so an expression that does not
// collapse the window itself has nothing left to collapse by the time the result is
// in hand.
func TestEveryPrometheusKeyAppliesTheRequestedAggregate(t *testing.T) {
	max, err := parseAggregate("max")
	require.NoError(t, err)

	builders := map[string]struct {
		build func(RequestMetadata, []string) map[string]string
		meta  RequestMetadata
	}{
		"buildPrometheusWorkloadQueries": {
			build: buildPrometheusWorkloadQueries,
			meta: RequestMetadata{
				Kind: "deployment", Namespace: "shop", Name: "api", ContainerName: "api",
				PVCName: "data", NodeName: "node-a", NodeIP: "10.0.0.5", InternalIP: "10.0.0.5",
			},
		},
		"buildPrometheusNodeQueries": {
			build: buildPrometheusNodeQueries,
			meta: RequestMetadata{
				Kind: "node", NodeName: "node-a", NodeIP: "10.0.0.5", InternalIP: "10.0.0.5",
			},
		},
	}

	for name, b := range builders {
		t.Run(name, func(t *testing.T) {
			keys := promBuilderKeys(t, name)

			plainMeta := b.meta
			plainMeta.RangeWindow, plainMeta.Step, plainMeta.RateWindow = "14d", "75s", "75s"
			aggMeta := plainMeta
			aggMeta.Aggregate = max

			plain := b.build(plainMeta, keys)
			aggregated := b.build(aggMeta, keys)

			for _, key := range keys {
				expr, built := plain[key]
				if !built {
					continue
				}
				got := aggregated[key]

				if promSelfWindowedKeys[UtilisationKey(key)] {
					assert.Equalf(t, expr, got,
						"%s already collapses the window itself, so the aggregate must leave it alone", key)
					continue
				}
				assert.Equalf(t, promQLOverTime(max, expr, "14d", "75s"), got,
					"%s ignores the requested aggregate", key)

				// Wrapping must not produce something Prometheus will reject. The
				// subquery binds to the whole expression only because promQLOverTime
				// parenthesises it, and several keys are `or` fallback chains or
				// vector joins where that matters.
				_, parseErr := promQLParser.ParseExpr(stripClusterPlaceholder(got))
				require.NoErrorf(t, parseErr, "%s produced invalid PromQL when aggregated:\n%s", key, got)
			}
		})
	}
}

// The regression itself, stated as the reader sees it: two different statistics must
// not come back as the same query. Kept separate from the sweep above so a failure
// names the keys the E2E actually caught.
func TestScalarUsageKeysDistinguishMaxFromAvg(t *testing.T) {
	meta := RequestMetadata{
		Kind: "deployment", Namespace: "shop", Name: "api",
		RangeWindow: "14d", Step: "75s", RateWindow: "75s",
	}
	keys := []string{"cpu_usage", "memory_usage"}

	var err error
	meta.Aggregate, err = parseAggregate("max")
	require.NoError(t, err)
	maxQ := buildPrometheusWorkloadQueries(meta, keys)

	meta.Aggregate, err = parseAggregate("avg")
	require.NoError(t, err)
	avgQ := buildPrometheusWorkloadQueries(meta, keys)

	for _, key := range keys {
		assert.Containsf(t, maxQ[key], "max_over_time(", "%s does not collapse to a maximum", key)
		assert.Containsf(t, avgQ[key], "avg_over_time(", "%s does not collapse to an average", key)
		assert.NotEqualf(t, maxQ[key], avgQ[key], "%s returns the same query for max and avg", key)
	}
}

// The carve-out list has to stay honest in both directions, because it is the one
// place a key can opt out of the request's aggregate. A key listed here that does
// not already collapse the window would silently stop answering `aggregate`; a key
// that collapses the window but is missing from the list would be aggregated twice.
func TestSelfWindowedCarveOutMatchesTheQueriesThatCollapseTheWindow(t *testing.T) {
	meta := RequestMetadata{
		Kind: "deployment", Namespace: "shop", Name: "api",
		RangeWindow: "14d", Step: "75s", RateWindow: "75s",
	}
	keys := promBuilderKeys(t, "buildPrometheusWorkloadQueries")
	queries := buildPrometheusWorkloadQueries(meta, keys)

	for key := range promSelfWindowedKeys {
		query, built := queries[string(key)]
		require.Truef(t, built, "%s is carved out but the builder does not answer it", key)
		assert.Truef(t,
			strings.Contains(query, "[14d:75s]") || strings.Contains(query, "[14d]"),
			"%s is carved out of the aggregate but does not collapse the range window itself:\n%s", key, query)
	}

	for key, query := range queries {
		if strings.Contains(query, "[14d:75s]") {
			assert.Truef(t, promSelfWindowedKeys[UtilisationKey(key)],
				"%s already aggregates over the range window but is not carved out, so an aggregated request takes a statistic of a statistic:\n%s",
				key, query)
		}
	}
}
