package observability

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registry is only worth having if it cannot drift from the builders. The compiler does
// most of that now: every key switch and key table names its keys by constant, so an
// undeclared key does not build. Two gaps remain that only a test can close — Go accepts an
// untyped string literal in a switch on UtilisationKey, and a declared key can go
// unanswered — and these tests cover both.

// keyDispatch is what the package's key switches and key tables name, read from source. It
// reads the source rather than calling the builders because a case is reachable only with
// the right scope, kind and aggregate; the source is the one place every case is visible.
type keyDispatch struct {
	constants map[string]bool // Key* constants used as a case or a table key
	rendered  map[string]bool // Key* constants a builder renders a query for
	raw       []string        // string literals where a key constant belongs, with location
	files     int
	switches  int
}

// isBuilderFile reports whether a file holds a provider's query builders.
func isBuilderFile(file string) bool {
	return strings.HasSuffix(file, "queries.go") || file == "elasticsearch_saas_utilisation.go"
}

// isResultKeySwitch reports whether a switch is on a result's key (post-processing what
// came back), which answers nothing.
func isResultKeySwitch(tag ast.Expr) bool {
	call, ok := tag.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	sel, ok := call.Args[0].(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "QueryKey"
}

// isKeySwitch reports whether a switch dispatches on a utilisation key: either it switches
// on UtilisationKey(...), or any of its cases is a string equal to a declared key. The
// second form is what catches a key switch written the old way, whatever the variable is
// called — post-processing switches on qr.QueryKey were missed by a name-based check.
func isKeySwitch(sw *ast.SwitchStmt, declared map[string]bool) bool {
	if call, ok := sw.Tag.(*ast.CallExpr); ok {
		if fun, ok := call.Fun.(*ast.Ident); ok && fun.Name == "UtilisationKey" {
			return true
		}
	}
	for _, stmt := range sw.Body.List {
		for _, expr := range stmt.(*ast.CaseClause).List {
			if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && declared[v] {
					return true
				}
			}
		}
	}
	return false
}

func scanKeyDispatch(t *testing.T) keyDispatch {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	declared := map[string]bool{}
	for _, v := range registryConstantValues(t) {
		declared[v] = true
	}

	d := keyDispatch{constants: map[string]bool{}, rendered: map[string]bool{}}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "metrics_utilisation_keys.go" {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoError(t, err, file)
		d.files++

		record := func(expr ast.Expr, where string) {
			switch e := expr.(type) {
			case *ast.Ident:
				if strings.HasPrefix(e.Name, "Key") {
					d.constants[e.Name] = true
				}
			case *ast.BasicLit:
				if e.Kind == token.STRING {
					d.raw = append(d.raw, fmt.Sprintf("%s at %s (%s)", e.Value, fset.Position(e.Pos()), where))
				}
			}
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SwitchStmt:
				if x.Tag == nil || !isKeySwitch(x, declared) {
					return true
				}
				d.switches++
				renders := isBuilderFile(file) && !isResultKeySwitch(x.Tag)
				for _, stmt := range x.Body.List {
					for _, expr := range stmt.(*ast.CaseClause).List {
						record(expr, "case")
						if id, ok := expr.(*ast.Ident); ok && renders {
							d.rendered[id.Name] = true
						}
					}
				}
			case *ast.CompositeLit:
				mt, ok := x.Type.(*ast.MapType)
				if !ok {
					return true
				}
				if id, ok := mt.Key.(*ast.Ident); !ok || id.Name != "UtilisationKey" {
					return true
				}
				for _, el := range x.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						record(kv.Key, "table key")
					}
				}
			}
			return true
		})
	}

	// Guard against passing vacuously: a broken glob or a refactored dispatch would
	// otherwise find nothing and assert nothing.
	require.GreaterOrEqual(t, d.files, 10, "expected to scan the package")
	require.GreaterOrEqual(t, d.switches, 25, "expected to find the key switches")
	require.GreaterOrEqual(t, len(d.constants), 90, "expected key switches and tables to name their keys by constant")
	return d
}

// registryConstantValues maps each constant in metrics_utilisation_keys.go to its key.
func registryConstantValues(t *testing.T) map[string]string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "metrics_utilisation_keys.go", nil, 0)
	require.NoError(t, err)
	values := map[string]string{}
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						values[name.Name], _ = strconv.Unquote(lit.Value)
					}
				}
			}
		}
	}
	return values
}

func tableKeys[K ~string, V any](m map[K]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	return keys
}

// answeringTables map a key to what a provider queries for it.
func answeringTables() map[string][]string {
	return map[string][]string{
		"datadogPerPodKeys":      tableKeys(datadogPerPodKeys),
		"datadogHTTPKeys":        tableKeys(datadogHTTPKeys),
		"datadogClusterWideKeys": tableKeys(datadogClusterWideKeys),
		"openObservePerPodKeys":  tableKeys(openObservePerPodKeys),
		"openObserveClusterKeys": tableKeys(openObserveClusterKeys),
		"splunkUtilisationKeys":  tableKeys(splunkUtilisationKeys),
	}
}

// keyLists are every other list of keys in the package: the providers' refusal tables
// and the keys callers send. They must be declared, but naming a key answers nothing.
func keyLists() map[string][]string {
	var evidence []string
	for _, keys := range workloadEvidenceKeys {
		evidence = append(evidence, keys...)
	}
	return map[string][]string{
		"datadogUnanswerableKeys":   tableKeys(datadogUnanswerableKeys),
		"splunkUtilisationRefusals": tableKeys(splunkUtilisationRefusals),
		"podMetricSpecKeys":         podMetricSpecKeys,
		"podNodeMetricsKeys":        podNodeMetricsKeys,
		"workloadEvidenceKeys":      evidence,
	}
}

func TestUtilisationKeys_NoRawStringsInKeyDispatch(t *testing.T) {
	// A raw string in a key switch compiles — Go converts an untyped string constant to
	// UtilisationKey — but it bypasses the registry, which is the drift this file exists
	// to stop.
	d := scanKeyDispatch(t)
	assert.Empty(t, d.raw, "name these keys by their constant in metrics_utilisation_keys.go")
}

func TestUtilisationKeys_EveryKeyListIsDeclared(t *testing.T) {
	lists := keyLists()
	for name, keys := range answeringTables() {
		lists[name] = keys
	}
	for table, keys := range lists {
		require.NotEmpty(t, keys, table)
		for _, key := range keys {
			assert.True(t, isKnownUtilisationKey(key), "%s holds %q, which is not declared", table, key)
		}
	}
}

func TestUtilisationKeys_EveryDeclaredKeyIsAnswered(t *testing.T) {
	// A declared key no builder renders is a promise no provider keeps. Refusal tables,
	// caller lists and switches on a result's key name a key without answering it.
	values := registryConstantValues(t)
	handled := map[string]bool{}
	for name := range scanKeyDispatch(t).rendered {
		value, ok := values[name]
		require.True(t, ok, "%s is used by a builder but is not a constant in metrics_utilisation_keys.go", name)
		handled[value] = true
	}
	for _, keys := range answeringTables() {
		for _, key := range keys {
			handled[key] = true
		}
	}

	var unanswered []string
	for key := range utilisationKeys {
		if !handled[string(key)] {
			unanswered = append(unanswered, string(key))
		}
	}
	sort.Strings(unanswered)
	assert.Empty(t, unanswered, "no builder or key table handles these")
}

// The six keys every chart of a workload starts from. Only the builders that render
// without I/O can be checked here; Elasticsearch, Splunk and OpenObserve choose their
// query from what the backend holds, so their coverage is the utilisation-conformance
// skill's job, run against a live account.
var coreUtilisationKeys = []string{"cpu_usage", "memory_usage", "cpu_request", "cpu_limit", "memory_request", "memory_limit"}

func TestUtilisationKeys_FallThroughBuildersRenderTheCoreKeys(t *testing.T) {
	meta := RequestMetadata{
		Namespace: "shop", Name: "api", Kind: "deployment",
		RequestedMetrics: coreUtilisationKeys,
	}
	meta.RangeWindow, meta.Step, meta.RateWindow = resolveAggWindows(0, 3600*1000, 0)

	builders := map[string]func(RequestMetadata, []string) map[string]string{
		"prometheus": buildPrometheusWorkloadQueries,
		"datadog":    buildDatadogWorkloadQueries,
		"newrelic":   buildNewRelicWorkloadQueries,
		"dynatrace":  buildDynatraceWorkloadQueries,
		"solarwinds": buildSolarWindsWorkloadQueries,
	}
	for provider, build := range builders {
		queries := build(meta, coreUtilisationKeys)
		for _, key := range coreUtilisationKeys {
			assert.NotEmpty(t, queries[key], "%s renders no workload query for core key %q", provider, key)
		}
	}
}

func TestUnknownUtilisationKeys_OrderedAndDeduplicated(t *testing.T) {
	got := unknownUtilisationKeys([]string{"cpu_usge", "cpu_usage", "cpu_usge", "", "p91_cpu"})
	assert.Equal(t, []string{"cpu_usge", "p91_cpu"}, got, "empty and repeated keys are dropped")
}

func TestCompleteUtilisationResults_UnknownKeyIsNamedAsUnknown(t *testing.T) {
	got := completeUtilisationResults(nil, []string{"cpu_usge", "container_top_cpu_usage"}, map[string]string{}, "datadog", esScopeWorkload)

	assert.Equal(t, `unknown utilisation key "cpu_usge"`, resultFor(t, got, "cpu_usge").Note)
	assert.Equal(t, `datadog does not answer utilisation key "container_top_cpu_usage" at workload scope`,
		resultFor(t, got, "container_top_cpu_usage").Note, "a declared key the provider lacks is still the provider's gap")
}

func TestCompleteUtilisationResults_UnknownWinsOverNoProvider(t *testing.T) {
	// A misspelled key would be just as unanswerable with a provider, so saying "no
	// provider" would send the caller to fix the wrong thing.
	got := completeUtilisationResults(nil, []string{"cpu_usge", "cpu_usage"}, nil, "", esScopeWorkload)

	assert.Equal(t, `unknown utilisation key "cpu_usge"`, resultFor(t, got, "cpu_usge").Note)
	assert.Equal(t, "no metrics provider resolved for this account", resultFor(t, got, "cpu_usage").Note)
}

func TestFetchMetricUtilisation_UnknownKeyIsNotedAndLogged(t *testing.T) {
	stubUtilisationProvider(t, "prometheus", "agent")
	stubMetricSource(t, &fakeMetricSource{answers: map[string][]Result{"cpu_usage": {{Values: []float64{0.5}}}}})

	var buf bytes.Buffer
	out, err := FetchMetricUtilisation(loggingRequestContext(&buf), workloadUtilisationRequest("cpu_usage", "cpu_usge"))
	require.NoError(t, err)

	require.Len(t, out.Results, 2)
	assert.Len(t, resultFor(t, out.Results, "cpu_usage").Payload, 1, "the known key is still answered")
	assert.Equal(t, `unknown utilisation key "cpu_usge"`, resultFor(t, out.Results, "cpu_usge").Note)
	assert.Contains(t, buf.String(), `"msg":"observability: unknown utilisation keys requested"`)
	assert.Contains(t, buf.String(), `"keys":["cpu_usge"]`)
}

func TestUtilisationKeys_EveryConstantIsInTheRegistry(t *testing.T) {
	// The registry is built from a hand-written list of the constants. A constant left
	// out of it, and used nowhere else, would be declared without being known — read the
	// const block itself so that cannot happen quietly.
	parsed, err := parser.ParseFile(token.NewFileSet(), "metrics_utilisation_keys.go", nil, 0)
	require.NoError(t, err)

	constants := 0
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if typ, ok := vs.Type.(*ast.Ident); !ok || typ.Name != "UtilisationKey" {
				continue
			}
			// Every key is written as `KeyX UtilisationKey = "x"`. Anything else — an omitted
			// value, iota, a reference to another constant — is a shape this test cannot read,
			// so it fails loudly rather than skipping the constant.
			require.Len(t, vs.Values, len(vs.Names), "each UtilisationKey constant needs its own string value")
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				require.True(t, ok && lit.Kind == token.STRING, "%s must be a string literal", name.Name)
				key, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				constants++
				assert.True(t, utilisationKeys[UtilisationKey(key)], "%s (%q) is declared but missing from utilisationKeys", name.Name, key)
			}
		}
	}
	assert.Equal(t, len(utilisationKeys), constants, "utilisationKeys and the const block differ in size")
}

// Mean latency divides the duration sum's rate by the request count's rate.
// With no requests in the window the count's rate is exactly 0 while a stored
// float sum can wobble by an ulp, so an unguarded divisor yields +Inf seconds.
func TestPrometheusQueries_HTTPLatencyMeanGuardsZeroCount(t *testing.T) {
	meta := RequestMetadata{Namespace: "shop", Name: "api", Kind: "Deployment", ContainerName: "app"}
	key := string(KeyContainerHTTPLatencyMean)
	q := buildPrometheusWorkloadQueries(meta, []string{key})[key]
	assert.Contains(t, q, "/ (sum(rate(container_http_requests_duration_seconds_total_count{", "divisor must be the count's rate")
	assert.True(t, strings.HasSuffix(q, "[1h])) > 0)"), "count's rate must be filtered to > 0: %s", q)
}
