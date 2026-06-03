package observability

import (
	"nudgebee/services/security"
	"strings"
	"testing"
)

func TestInjectPromQLMatchers(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		matchers []LabelMatcher
		labels   map[string]string
		want     string
	}{
		{
			name: "eq matcher renders with =",
			expr: "up",
			matchers: []LabelMatcher{
				{Label: "job", Operator: "_eq", Value: "api-server"},
			},
			want: `up{job="api-server"}`,
		},
		{
			name: "neq matcher renders with !=",
			expr: "up",
			matchers: []LabelMatcher{
				{Label: "job", Operator: "_neq", Value: "api-server"},
			},
			want: `up{job!="api-server"}`,
		},
		{
			name: "regex matcher renders with =~",
			expr: "up",
			matchers: []LabelMatcher{
				{Label: "pod", Operator: "_regex", Value: "api-.*"},
			},
			want: `up{pod=~"api-.*"}`,
		},
		{
			name: "matchers sorted by (label, operator, value)",
			expr: "up",
			matchers: []LabelMatcher{
				{Label: "pod", Operator: "_eq", Value: "x"},
				{Label: "instance", Operator: "_neq", Value: "node-1"},
				{Label: "job", Operator: "_eq", Value: "api-server"},
			},
			want: `up{instance!="node-1",job="api-server",pod="x"}`,
		},
		{
			name: "same label twice with different operators kept in order",
			expr: "up",
			matchers: []LabelMatcher{
				{Label: "pod", Operator: "_regex", Value: "a.*"},
				{Label: "pod", Operator: "_eq", Value: "b"},
			},
			want: `up{pod="b",pod=~"a.*"}`,
		},
		{
			name: "legacy labels appended after matchers, sorted",
			expr: "up",
			matchers: []LabelMatcher{
				{Label: "pod", Operator: "_regex", Value: "a.*"},
			},
			labels: map[string]string{"job": "api-server", "instance": "node-1"},
			want:   `up{pod=~"a.*",instance="node-1",job="api-server"}`,
		},
		{
			name:   "legacy labels only — back-compat with eq-only callers",
			expr:   "up",
			labels: map[string]string{"job": "api-server", "instance": "node-1"},
			want:   `up{instance="node-1",job="api-server"}`,
		},
		{
			name: "matchers injected into existing selector",
			expr: `up{cluster="prod"}`,
			matchers: []LabelMatcher{
				{Label: "job", Operator: "_eq", Value: "api"},
			},
			want: `up{cluster="prod",job="api"}`,
		},
		{
			name: "matchers injected before range selector",
			expr: "rate(http_requests_total[5m])",
			matchers: []LabelMatcher{
				{Label: "status", Operator: "_regex", Value: "5.."},
			},
			want: `rate(http_requests_total{status=~"5.."}[5m])`,
		},
		{
			name: "value with double-quote and backslash is escaped",
			expr: "up",
			matchers: []LabelMatcher{
				{Label: "msg", Operator: "_eq", Value: `a\b"c`},
			},
			want: `up{msg="a\\b\"c"}`,
		},
		{
			name: "no matchers and no labels returns expression unchanged",
			expr: "up",
			want: "up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := injectPromQLMatchers(tt.expr, tt.matchers, tt.labels)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInjectPromQLMatchersErrors(t *testing.T) {
	tests := []struct {
		name        string
		matchers    []LabelMatcher
		labels      map[string]string
		wantErrSubs string
	}{
		{
			name:        "_in is not yet supported",
			matchers:    []LabelMatcher{{Label: "namespace", Operator: "_in", Value: "prod,staging"}},
			wantErrSubs: "not yet supported",
		},
		{
			name:        "_not_in is not yet supported",
			matchers:    []LabelMatcher{{Label: "namespace", Operator: "_not_in", Value: "test"}},
			wantErrSubs: "not yet supported",
		},
		{
			name:        "unknown operator returns error",
			matchers:    []LabelMatcher{{Label: "x", Operator: "_foo", Value: "y"}},
			wantErrSubs: "unsupported operator",
		},
		{
			name:        "label name with comma rejected (injection guard)",
			matchers:    []LabelMatcher{{Label: `foo,bar`, Operator: "_eq", Value: "x"}},
			wantErrSubs: "invalid label name",
		},
		{
			name:        "label name with quote rejected (injection guard)",
			matchers:    []LabelMatcher{{Label: `foo"`, Operator: "_eq", Value: "x"}},
			wantErrSubs: "invalid label name",
		},
		{
			name:        "label name starting with digit rejected",
			matchers:    []LabelMatcher{{Label: "1foo", Operator: "_eq", Value: "x"}},
			wantErrSubs: "invalid label name",
		},
		{
			name:        "legacy labels also validated",
			labels:      map[string]string{"foo,bar": "x"},
			wantErrSubs: "invalid label name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := injectPromQLMatchers("up", tt.matchers, tt.labels)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrSubs)
			}
			if !strings.Contains(err.Error(), tt.wantErrSubs) {
				t.Errorf("expected error containing %q, got %q", tt.wantErrSubs, err.Error())
			}
		})
	}
}

// renderPerItem mirrors what GetMetricsQuery does for a single QueryItem,
// without the provider lookup. Useful for asserting per-item rendering and
// cross-block isolation without needing a real account/source registry.
func renderPerItem(t *testing.T, items map[string]QueryItem) map[string]string {
	t.Helper()
	src := &PrometheusMetricSource{}
	out := make(map[string]string, len(items))
	for key, item := range items {
		req := FetchMetricsRequest{
			Queries:       map[string]string{key: item.Metric},
			LabelMatchers: item.LabelMatchers,
		}
		got, err := src.GetQuery((*security.RequestContext)(nil), req)
		if err != nil {
			t.Fatalf("GetQuery for key %q: %v", key, err)
		}
		out[key] = got
	}
	return out
}

func TestGetMetricsQueryPerItem(t *testing.T) {
	results := renderPerItem(t, map[string]QueryItem{
		"a": {Metric: "up", LabelMatchers: []LabelMatcher{
			{Label: "job", Operator: "_eq", Value: "api"},
		}},
		"b": {Metric: "down", LabelMatchers: []LabelMatcher{
			{Label: "pod", Operator: "_neq", Value: "x"},
		}},
	})
	if got, want := results["a"], `up{job="api"}`; got != want {
		t.Errorf("results[a] = %q, want %q", got, want)
	}
	if got, want := results["b"], `down{pod!="x"}`; got != want {
		t.Errorf("results[b] = %q, want %q", got, want)
	}
}

func TestGetMetricsQueryNoCrossBlockLeakage(t *testing.T) {
	results := renderPerItem(t, map[string]QueryItem{
		"a": {Metric: "up", LabelMatchers: []LabelMatcher{
			{Label: "job", Operator: "_eq", Value: "only-in-a"},
		}},
		"b": {Metric: "down", LabelMatchers: []LabelMatcher{
			{Label: "instance", Operator: "_regex", Value: "only-in-b.*"},
		}},
	})
	if strings.Contains(results["a"], "only-in-b") {
		t.Errorf("matcher from b leaked into a: %q", results["a"])
	}
	if strings.Contains(results["b"], "only-in-a") {
		t.Errorf("matcher from a leaked into b: %q", results["b"])
	}
}

// TestGetMetricsQueryTopLevelLabelsDoNotLeak verifies that when a request
// uses the BUILDER QueryItems shape, the request-level Labels field (used
// by internal callers only) does not bleed into per-item rendered queries.
// Mirrors the per-item copy logic in GetMetricsQuery (service.go).
func TestGetMetricsQueryTopLevelLabelsDoNotLeak(t *testing.T) {
	src := &PrometheusMetricSource{}
	req := FetchMetricsRequest{
		Labels: map[string]string{
			"container": "acmesolver",
			"namespace": "prod",
		},
		QueryItems: map[string]QueryItem{
			"a": {Metric: "up", LabelMatchers: []LabelMatcher{
				{Label: "cluster", Operator: "_eq", Value: "cluster-name"},
			}},
			"b": {Metric: "down", LabelMatchers: []LabelMatcher{
				{Label: "container", Operator: "_neq", Value: "ad"},
			}},
		},
	}

	results := make(map[string]string, len(req.QueryItems))
	for key, item := range req.QueryItems {
		perItem := req
		perItem.Queries = map[string]string{key: item.Metric}
		perItem.LabelMatchers = item.LabelMatchers
		perItem.QueryItems = nil
		perItem.Labels = nil

		got, err := src.GetQuery((*security.RequestContext)(nil), perItem)
		if err != nil {
			t.Fatalf("GetQuery for key %q: %v", key, err)
		}
		results[key] = got
	}

	for key, q := range results {
		if strings.Contains(q, `container="acmesolver"`) || strings.Contains(q, `namespace="prod"`) {
			t.Errorf("top-level Labels leaked into rendered query for key %q: %q", key, q)
		}
	}
	if got, want := results["a"], `up{cluster="cluster-name"}`; got != want {
		t.Errorf("results[a] = %q, want %q", got, want)
	}
	if got, want := results["b"], `down{container!="ad"}`; got != want {
		t.Errorf("results[b] = %q, want %q", got, want)
	}
}

func TestWrapPromQLAggregator(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		op      string
		want    string
		wantErr string
	}{
		{name: "empty op passes through unchanged", expr: `up{job="api"}`, op: "", want: `up{job="api"}`},
		{name: "sum wraps", expr: `up{job="api"}`, op: "sum", want: `sum(up{job="api"})`},
		{name: "avg wraps", expr: `up`, op: "avg", want: `avg(up)`},
		{name: "min wraps", expr: `up`, op: "min", want: `min(up)`},
		{name: "max wraps", expr: `up`, op: "max", want: `max(up)`},
		{name: "count wraps", expr: `up`, op: "count", want: `count(up)`},
		{name: "stddev wraps", expr: `up`, op: "stddev", want: `stddev(up)`},
		{name: "stdvar wraps", expr: `up`, op: "stdvar", want: `stdvar(up)`},
		{name: "group wraps", expr: `up`, op: "group", want: `group(up)`},
		{name: "topk rejected (parametric)", expr: `up`, op: "topk", wantErr: "scalar parameter"},
		{name: "bottomk rejected (parametric)", expr: `up`, op: "bottomk", wantErr: "scalar parameter"},
		{name: "quantile rejected (parametric)", expr: `up`, op: "quantile", wantErr: "scalar parameter"},
		{name: "unknown op rejected", expr: `up`, op: "median", wantErr: "unsupported aggregate_operator"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wrapPromQLAggregator(tt.expr, tt.op)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetMetricsQueryWithAggregator(t *testing.T) {
	src := &PrometheusMetricSource{}
	items := map[string]QueryItem{
		"a": {Metric: "up", LabelMatchers: []LabelMatcher{
			{Label: "job", Operator: "_eq", Value: "api"},
		}, AggregateOperator: "sum"},
		"b": {Metric: "down", LabelMatchers: []LabelMatcher{
			{Label: "pod", Operator: "_regex", Value: "api-.*"},
		}, AggregateOperator: ""},
	}

	results := make(map[string]string, len(items))
	for key, item := range items {
		req := FetchMetricsRequest{
			Queries:       map[string]string{key: item.Metric},
			LabelMatchers: item.LabelMatchers,
		}
		got, err := src.GetQuery((*security.RequestContext)(nil), req)
		if err != nil {
			t.Fatalf("GetQuery for key %q: %v", key, err)
		}
		wrapped, werr := wrapPromQLAggregator(got, item.AggregateOperator)
		if werr != nil {
			t.Fatalf("wrapPromQLAggregator for key %q: %v", key, werr)
		}
		results[key] = wrapped
	}

	if got, want := results["a"], `sum(up{job="api"})`; got != want {
		t.Errorf("results[a] = %q, want %q", got, want)
	}
	if got, want := results["b"], `down{pod=~"api-.*"}`; got != want {
		t.Errorf("results[b] = %q, want %q (no aggregator → no wrap)", got, want)
	}
}

func TestPromqlMatcherOp(t *testing.T) {
	tests := []struct {
		token   string
		want    string
		wantErr bool
	}{
		{"_eq", "=", false},
		{"_neq", "!=", false},
		{"_regex", "=~", false},
		{"_in", "", true},
		{"_not_in", "", true},
		{"_foo", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			got, err := promqlMatcherOp(tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
