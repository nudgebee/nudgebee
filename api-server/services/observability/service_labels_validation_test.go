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
}

func (f *fakeLabelSource) QueryLogs(*security.RequestContext, FetchLogRequest) ([]OutputLog, error) {
	return nil, nil
}
func (f *fakeLabelSource) QueryLabels(*security.RequestContext, FetchLogLabelRequest) ([]OutputLogLabel, error) {
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
}

func (f *fakeTraceSource) QueryTraces(*security.RequestContext, TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return nil, nil
}
func (f *fakeTraceSource) GetQuery(*security.RequestContext, TracesV3Request) (string, error) {
	return "", nil
}
func (f *fakeTraceSource) CountTraces(*security.RequestContext, TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{}, nil
}
func (f *fakeTraceSource) GetLabelValues(*security.RequestContext, TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	return common.OpenTelemetryTraceLabelValues{}, nil
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
func (f *fakeTraceSource) GetLabelMapping() map[string]string { return nil }
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

	t.Run("canonical trace field passes without backend discovery", func(t *testing.T) {
		// service_name is a canonical trace field, so it is valid even though the
		// backend QueryLabels only returns custom attributes.
		src := &fakeTraceSource{labels: traceLabelSet("app.custom.attr")}
		assert.NoError(t, validateReferencedTraceLabels(ctx, src, traceReq, refSet("service_name"), nil))
	})

	t.Run("discovered attribute passes", func(t *testing.T) {
		src := &fakeTraceSource{labels: traceLabelSet("app.ads.category")}
		assert.NoError(t, validateReferencedTraceLabels(ctx, src, traceReq, refSet("app.ads.category"), nil))
	})

	t.Run("fails open when discovery errors", func(t *testing.T) {
		src := &fakeTraceSource{err: errors.New("clickhouse down")}
		assert.NoError(t, validateReferencedTraceLabels(ctx, src, traceReq, refSet("service_nam"), nil))
	})
}
