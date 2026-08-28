package observability

import (
	"errors"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLabelSource is a minimal LogSource used to drive validateReferencedLabels and
// validateReferencedLabelValues. QueryLabels and QueryLabelValues are meaningful; the rest
// satisfy the interface. labelValues maps a label name to the values the backend returns for
// it; valuesErr forces QueryLabelValues to fail. lastValuesReq records the most recent
// QueryLabelValues request so tests can assert on the (widened) time window passed in.
type fakeLabelSource struct {
	labels        []OutputLogLabel
	err           error
	labelValues   map[string][]string
	valuesErr     error
	lastValuesReq FetchLogLabelValuesRequest
	lastLabelsReq FetchLogLabelRequest
}

func (f *fakeLabelSource) QueryLogs(*security.RequestContext, FetchLogRequest) ([]OutputLog, error) {
	return nil, nil
}
func (f *fakeLabelSource) QueryLabels(_ *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	f.lastLabelsReq = req
	return f.labels, f.err
}
func (f *fakeLabelSource) QueryLabelValues(_ *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	f.lastValuesReq = req
	if f.valuesErr != nil {
		return nil, f.valuesErr
	}
	out := make([]OutputLogLabelValue, 0, len(f.labelValues[req.LabelName]))
	for _, v := range f.labelValues[req.LabelName] {
		out = append(out, OutputLogLabelValue{Value: v, Attributes: map[string]any{}})
	}
	return out, nil
}
func (f *fakeLabelSource) GetQuery(*security.RequestContext, FetchLogRequest) (string, error) {
	return "", nil
}
func (f *fakeLabelSource) GetLabelMapping() map[string]string { return nil }
func (f *fakeLabelSource) GetSupportedOperators() []string    { return nil }

func binaryWhere(field string) query.QueryWhereClause {
	return query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			field: {query.Eq: "value"},
		},
	}
}

func labelSet(names ...string) []OutputLogLabel {
	out := make([]OutputLogLabel, len(names))
	for i, n := range names {
		out[i] = OutputLogLabel{Label: n, Attributes: map[string]any{}}
	}
	return out
}

func TestCollectWhereFieldNames(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			binaryWhere("service_name"),
			{Or: []query.QueryWhereClause{binaryWhere("namespace"), binaryWhere("pod")}},
			{Not: &query.QueryWhereClause{Binary: query.BinaryWhereClause{"container": {query.Nq: "x"}}}},
		},
	}
	got := map[string]struct{}{}
	collectWhereFieldNames(where, got)

	for _, f := range []string{"service_name", "namespace", "pod", "container"} {
		_, ok := got[f]
		assert.Truef(t, ok, "expected field %q collected", f)
	}
	assert.Len(t, got, 4)
}

func refSet(fields ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range fields {
		out[f] = struct{}{}
	}
	return out
}

func TestValidateReferencedLabels(t *testing.T) {
	ctx := mockRequestContext()
	logReq := FetchLogRequest{AccountId: "acct", StartTime: 1, EndTime: 2}

	t.Run("unknown label returns actionable error", func(t *testing.T) {
		src := &fakeLabelSource{labels: labelSet("service_name", "namespace", "pod")}
		err := validateReferencedLabels(ctx, src, logReq, refSet("service_nam"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "service_nam")
		assert.Contains(t, err.Error(), "service_name") // available labels listed
		assert.Contains(t, err.Error(), "log provider") // provider wording
	})

	t.Run("known label passes", func(t *testing.T) {
		src := &fakeLabelSource{labels: labelSet("service_name", "namespace")}
		assert.NoError(t, validateReferencedLabels(ctx, src, logReq, refSet("service_name"), nil))
	})

	t.Run("line-content field is not a label", func(t *testing.T) {
		src := &fakeLabelSource{labels: labelSet("service_name")}
		assert.NoError(t, validateReferencedLabels(ctx, src, logReq, refSet("content"), nil))
	})

	t.Run("field known via label mapping alias", func(t *testing.T) {
		src := &fakeLabelSource{labels: labelSet("k8s_namespace_name")}
		// "namespace" is not in the backend labels but is a canonical alias.
		mapping := map[string]string{"namespace": "k8s_namespace_name"}
		assert.NoError(t, validateReferencedLabels(ctx, src, logReq, refSet("namespace"), mapping))
	})

	t.Run("no referenced fields is a no-op", func(t *testing.T) {
		src := &fakeLabelSource{labels: labelSet("service_name")}
		assert.NoError(t, validateReferencedLabels(ctx, src, logReq, refSet(), nil))
	})

	t.Run("fails open when labels lookup errors", func(t *testing.T) {
		src := &fakeLabelSource{err: errors.New("relay down")}
		assert.NoError(t, validateReferencedLabels(ctx, src, logReq, refSet("service_nam"), nil))
	})

	t.Run("fails open when no labels available", func(t *testing.T) {
		src := &fakeLabelSource{labels: []OutputLogLabel{}}
		assert.NoError(t, validateReferencedLabels(ctx, src, logReq, refSet("service_nam"), nil))
	})

	t.Run("no close match gives action-agnostic guidance", func(t *testing.T) {
		src := &fakeLabelSource{labels: labelSet("k8s_cluster", "namespace", "pod")}
		// Shares no token with any available label and is far outside typo distance.
		err := validateReferencedLabels(ctx, src, logReq, refSet("zzz_unrelated_field"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zzz_unrelated_field")
		assert.Contains(t, err.Error(), "verify the name")
		assert.NotContains(t, err.Error(), "logs_list_labels") // don't name a tool the caller may lack
	})

	// Typos that are not prefix/suffix truncations must still yield a correction: the
	// substring-only matcher this replaced returned nothing for them, leaving the agent
	// with an unactionable error.
	t.Run("mid-word typo still suggests the right label", func(t *testing.T) {
		for _, typo := range []string{"k8s_deploymnt_name", "k8s_deploymetn_name", "kaas_cluster"} {
			src := &fakeLabelSource{labels: labelSet("k8s_deployment_name", "k8s_cluster", "pod")}
			err := validateReferencedLabels(ctx, src, logReq, refSet(typo), nil)
			require.Error(t, err, typo)
			assert.Contains(t, err.Error(), "closest valid label(s):", typo)
		}
	})
}

func TestUnknownReferencedLabels(t *testing.T) {
	referenced := map[string]struct{}{"service_nam": {}, "service_name": {}, "content": {}, "ns": {}}
	mapping := map[string]string{"ns": "namespace"} // "ns" is a canonical alias
	unknown, available := unknownReferencedLabels(referenced, []string{"service_name", "pod"}, mapping)

	// service_name is a backend label, content is a line filter, ns is a mapping alias → only service_nam is unknown.
	assert.Equal(t, []string{"service_nam"}, unknown)
	assert.Equal(t, []string{"pod", "service_name"}, available) // sorted
}

// fakeTraceSource is a minimal TraceSource; only QueryLabels is meaningful.
type fakeTraceSource struct {
	labels []OutputTraceLabel
	err    error
	// mapping is the source's STATIC label mapping. Empty means the provider has
	// declared nothing about which canonical fields it resolves, so the full canonical
	// set stays valid for it (see providerDeclaresTraceFields).
	mapping map[string]string
	// values / valuesErr drive GetLabelValues for the value-validation tests.
	values        map[string][]string
	valuesErr     error
	lastValuesReq TracesV3LabelValuesRequest
}

// completeFakeTraceSource is a fakeTraceSource that declares its value enumeration complete, so
// validateReferencedTraceLabelValues will actually run against it.
type completeFakeTraceSource struct{ fakeTraceSource }

func (f *completeFakeTraceSource) TraceLabelValuesAreComplete() {}

func (f *fakeTraceSource) QueryTraces(*security.RequestContext, TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return nil, nil
}
func (f *fakeTraceSource) GetQuery(*security.RequestContext, TracesV3Request) (string, error) {
	return "", nil
}
func (f *fakeTraceSource) CountTraces(*security.RequestContext, TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{}, nil
}
func (f *fakeTraceSource) GetLabelValues(_ *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	f.lastValuesReq = req
	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: f.values[req.Label]}, f.valuesErr
}
func (f *fakeTraceSource) QueryLabels(*security.RequestContext, FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return f.labels, f.err
}
func (f *fakeTraceSource) QueryGroupedTraces(*security.RequestContext, TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, nil
}
func (f *fakeTraceSource) QueryGroupedTracesCount(*security.RequestContext, TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{}, nil
}
func (f *fakeTraceSource) QueryRootSpansByTrace(*security.RequestContext, TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return nil, nil
}
func (f *fakeTraceSource) CountTracesByTrace(*security.RequestContext, TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{}, nil
}
func (f *fakeTraceSource) QueryTracesHeatmap(*security.RequestContext, TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	return nil, nil
}
func (f *fakeTraceSource) GetLabelMapping() map[string]string { return f.mapping }
func (f *fakeTraceSource) GetSupportedOperators() []string    { return nil }

func traceLabelSet(names ...string) []OutputTraceLabel {
	out := make([]OutputTraceLabel, len(names))
	for i, n := range names {
		out[i] = OutputTraceLabel{Label: n, Attributes: map[string]any{}}
	}
	return out
}

func TestValidateReferencedTraceLabels(t *testing.T) {
	ctx := mockRequestContext()
	traceReq := TracesV3Request{AccountId: "acct", StartTime: 1, EndTime: 2}

	t.Run("unknown trace label returns error naming trace provider", func(t *testing.T) {
		src := &fakeTraceSource{labels: traceLabelSet("app.custom.attr")}
		err := validateReferencedTraceLabels(ctx, src, traceReq, refSet("service_nam"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "service_nam")
		assert.Contains(t, err.Error(), "trace provider")
	})

	t.Run("canonical trace field passes for a provider that declares no mapping", func(t *testing.T) {
		// An empty static mapping means the provider has declared nothing about what it
		// resolves, so the whole canonical vocabulary stays valid — this is the
		// passthrough case (ClickHouse, Jaeger, Application Insights).
		src := &fakeTraceSource{labels: traceLabelSet("app.custom.attr")}
		assert.NoError(t, validateReferencedTraceLabels(ctx, src, traceReq, refSet("service_name"), nil))
	})

	t.Run("canonical trace field the provider declares passes without backend discovery", func(t *testing.T) {
		// service_name is a canonical trace field AND declared by this provider's label
		// mapping, so it is valid even though the backend QueryLabels only returns
		// custom attributes.
		mapping := map[string]string{"service_name": "service_name"}
		src := &fakeTraceSource{labels: traceLabelSet("app.custom.attr"), mapping: mapping}
		assert.NoError(t, validateReferencedTraceLabels(ctx, src, traceReq, refSet("service_name"), mapping))
	})

	t.Run("canonical trace field a declaring provider cannot resolve is rejected", func(t *testing.T) {
		// The counterpart of the advertising rule in buildTraceLabels. This provider DOES
		// publish a mapping, and that mapping does not cover duration_ns — so the caller
		// is told the name is unknown here instead of receiving the empty result that
		// reads as "there are no slow traces".
		mapping := map[string]string{"service_name": "service_name"}
		src := &fakeTraceSource{labels: traceLabelSet("app.custom.attr"), mapping: mapping}
		err := validateReferencedTraceLabels(ctx, src, traceReq, refSet("duration_ns"), mapping)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duration_ns")
	})

	t.Run("discovered attribute passes", func(t *testing.T) {
		src := &fakeTraceSource{labels: traceLabelSet("app.ads.category")}
		assert.NoError(t, validateReferencedTraceLabels(ctx, src, traceReq, refSet("app.ads.category"), nil))
	})

	t.Run("fails open when discovery errors", func(t *testing.T) {
		src := &fakeTraceSource{err: errors.New("clickhouse down")}
		assert.NoError(t, validateReferencedTraceLabels(ctx, src, traceReq, refSet("service_nam"), nil))
	})

	t.Run("no close match gives action-agnostic guidance", func(t *testing.T) {
		src := &fakeTraceSource{labels: traceLabelSet("app.custom.attr")}
		err := validateReferencedTraceLabels(ctx, src, traceReq, refSet("zzzzzz"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zzzzzz")
		assert.Contains(t, err.Error(), "verify the name")
		assert.NotContains(t, err.Error(), "traces_list_labels") // don't name a tool the caller may lack
	})
}

// fakeESSource is an ES-shaped LogSource: QueryLabels answers with the index's FIELDS,
// exactly as the real ES sources now do by delegating to QueryIndexFields. It adds the
// alias capability so the canonical/.keyword names are exercised too.
type fakeESSource struct {
	fakeLabelSource
	aliases func(discovered []string) []string
}

// GetQueryFieldAliases implements QueryFieldAliasSource.
func (f *fakeESSource) GetQueryFieldAliases(discovered []string) []string {
	if f.aliases == nil {
		return nil
	}
	return f.aliases(discovered)
}

// esShaped builds a source whose queryable fields are the given names, wired to the REAL
// alias function so these tests exercise the shipped logic rather than a restatement.
func esShaped(fields ...string) *fakeESSource {
	return &fakeESSource{
		fakeLabelSource: fakeLabelSource{labels: labelSet(fields...)},
		aliases:         esQueryFieldAliases,
	}
}

func TestValidateReferencedLabels_ESFieldSource(t *testing.T) {
	ctx := mockRequestContext()

	// Regression for the live defect: validating ES field names against the INDEX names
	// QueryLabels returns declared every correct field unknown, and the diagnosis told the
	// agent to remove filters that were right.
	t.Run("correct ES fields are not flagged as unknown", func(t *testing.T) {
		src := esShaped("kubernetes.container_name", "kubernetes.namespace_name", "log")
		referenced := map[string]struct{}{
			"kubernetes.container_name": {},
			"kubernetes.namespace_name": {},
		}
		err := validateReferencedLabels(ctx, src, FetchLogRequest{}, referenced, nil)
		assert.NoError(t, err, "fields present in the index mapping must pass")
	})

	t.Run("a genuinely unknown field still errors with a close match", func(t *testing.T) {
		src := esShaped("kubernetes.container_name", "kubernetes.namespace_name")
		err := validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"kubernetes.container_nam": {}}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes.container_nam")
		assert.Contains(t, err.Error(), "kubernetes.container_name")
	})

	t.Run("fails open when the field lookup errors", func(t *testing.T) {
		src := esShaped("kubernetes.container_name")
		src.err = errors.New("mapping unavailable")
		err := validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"whatever": {}}, nil)
		assert.NoError(t, err)
	})

	t.Run("fails open when the field set is empty", func(t *testing.T) {
		src := esShaped()
		err := validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"whatever": {}}, nil)
		assert.NoError(t, err)
	})

	t.Run("only the index is forwarded to the field lookup", func(t *testing.T) {
		src := esShaped("kubernetes.container_name")
		req := FetchLogRequest{Request: map[string]any{"index": "logs-*", "query_type": "dsl"}}
		_ = validateReferencedLabels(ctx, src, req,
			map[string]struct{}{"kubernetes.container_name": {}}, nil)
		assert.Equal(t, map[string]any{"index": "logs-*"}, src.lastLabelsReq.Request,
			"the index must travel; other provider-specific keys must not")
	})

	t.Run("no index yields a nil request so the source applies its own default", func(t *testing.T) {
		src := esShaped("kubernetes.container_name")
		_ = validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"kubernetes.container_name": {}}, nil)
		assert.Nil(t, src.lastLabelsReq.Request)
	})
}

// The ES query builder accepts names the index _mapping never reports — the canonical
// k8s labels it expands, and a `.keyword` suffix on a field mapped as bare keyword.
// Validating against the mapping alone reported all of them unknown and told the agent
// to remove filters that were correct.
func TestValidateReferencedLabels_QueryFieldAliases(t *testing.T) {
	ctx := mockRequestContext()

	t.Run("canonical k8s names are not flagged unknown", func(t *testing.T) {
		src := esShaped("kubernetes.namespace_name", "kubernetes.pod_name", "log")
		err := validateReferencedLabels(ctx, src, FetchLogRequest{}, map[string]struct{}{
			"app": {}, "namespace": {}, "pod": {}, "container": {},
		}, nil)
		assert.NoError(t, err, "the ES query builder expands these; none is ever in a mapping")
	})

	t.Run("_body is not flagged unknown", func(t *testing.T) {
		src := esShaped("log")
		assert.NoError(t, validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"_body": {}}, nil))
	})

	t.Run("_body is not flagged on a non-ES provider either", func(t *testing.T) {
		// llm-server advertises _body unconditionally, so it is never a typo anywhere.
		src := &fakeLabelSource{labels: labelSet("service_name")}
		assert.NoError(t, validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"_body": {}}, nil))
	})

	t.Run("a .keyword suffix on a mapped field is not flagged unknown", func(t *testing.T) {
		src := esShaped("kubernetes.pod_name")
		assert.NoError(t, validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"kubernetes.pod_name.keyword": {}}, nil))
	})

	t.Run("a canonical name is suggested for its typo", func(t *testing.T) {
		src := esShaped("log")
		err := validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"namesapce": {}}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "namespace", "aliases must reach the suggestion list, not just the known set")
	})

	t.Run("aliases do not rescue a genuinely unknown field", func(t *testing.T) {
		src := esShaped("kubernetes.pod_name")
		assert.Error(t, validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"zzz_unrelated_field": {}}, nil))
	})

	t.Run("aliases are never manufactured when discovery fails", func(t *testing.T) {
		// The fail-open guard must run BEFORE the alias merge: aliases alone are not a
		// field set, and returning them would flip every correct field to "unknown".
		src := esShaped("kubernetes.pod_name")
		src.err = errors.New("mapping unavailable")
		assert.NoError(t, validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"zzz_unrelated_field": {}}, nil))
	})

	t.Run("a canonical name with a .keyword suffix is still unknown", func(t *testing.T) {
		// Deliberate: binaryClauseForField would emit a term on a literal `namespace`
		// field that no shipper writes.
		src := esShaped("log")
		assert.Error(t, validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"namespace.keyword": {}}, nil))
	})

	t.Run("canonical ES names stay unknown on a non-ES provider", func(t *testing.T) {
		// The aliases are ES-scoped: on Loki/Pinot these names are genuine typos, and a
		// global widen would destroy real diagnoses there.
		src := &fakeLabelSource{labels: labelSet("service_name", "k8s_namespace_name")}
		assert.Error(t, validateReferencedLabels(ctx, src, FetchLogRequest{},
			map[string]struct{}{"namespace": {}}, nil))
	})
}
