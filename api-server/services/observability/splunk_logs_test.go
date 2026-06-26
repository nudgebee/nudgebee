package observability

import (
	"errors"
	"sort"
	"testing"

	"nudgebee/services/integrations"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func labelNames(labels []OutputLogLabel) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = l.Label
	}
	return out
}

func okConfigs(*security.RequestContext, string) (integrations.SplunkO11yConnConfig, error) {
	return integrations.SplunkO11yConnConfig{}, nil
}

func TestSplunkQueryLabels_DynamicDiscovery(t *testing.T) {
	// Custom OTel attributes present in the sampled logs must surface, merged with
	// the standard fallback fields, in sorted order.
	s := &SplunkLogSource{
		GetConfigs: okConfigs,
		LogSearch: func(integrations.SplunkO11yConnConfig, string, int64, int64, int) ([]integrations.O11yLogEntry, error) {
			return []integrations.O11yLogEntry{
				{Attributes: map[string]any{"message": "a", "service.version": "1.2.3"}},
				{Attributes: map[string]any{"message": "b", "http.status_code": 500, "service.version": "1.2.3"}},
			}, nil
		},
	}
	labels, err := s.QueryLabels(mockRequestContext(), FetchLogLabelRequest{AccountId: "acct-1"})
	require.NoError(t, err)
	names := labelNames(labels)
	// Custom attributes are discovered...
	assert.Contains(t, names, "service.version", "custom OTel attribute must be discovered dynamically")
	assert.Contains(t, names, "http.status_code", "custom OTel attribute must be discovered dynamically")
	// ...and standard fallback fields are still present (no regression on a sparse sample).
	assert.Contains(t, names, "kubernetes.pod.name", "standard fallback field must remain present")
	assert.Contains(t, names, "trace_id", "standard fallback field must remain present")
	assert.True(t, sort.StringsAreSorted(names), "labels must be returned in sorted order")
}

func TestSplunkQueryLabels_FallbackOnEmptySample(t *testing.T) {
	s := &SplunkLogSource{
		GetConfigs: okConfigs,
		LogSearch: func(integrations.SplunkO11yConnConfig, string, int64, int64, int) ([]integrations.O11yLogEntry, error) {
			return nil, nil // no entries sampled
		},
	}
	labels, err := s.QueryLabels(mockRequestContext(), FetchLogLabelRequest{AccountId: "acct-1"})
	require.NoError(t, err)
	assert.Equal(t, splunkO11yFallbackLogLabelNames, labelNames(labels),
		"empty sample must fall back to the static field set, not return empty")
}

func TestSplunkQueryLabels_FallbackOnSearchError(t *testing.T) {
	s := &SplunkLogSource{
		GetConfigs: okConfigs,
		LogSearch: func(integrations.SplunkO11yConnConfig, string, int64, int64, int) ([]integrations.O11yLogEntry, error) {
			return nil, errors.New("splunk unreachable")
		},
	}
	labels, err := s.QueryLabels(mockRequestContext(), FetchLogLabelRequest{AccountId: "acct-1"})
	require.NoError(t, err, "a sample-query failure must not error out the label list")
	assert.Equal(t, splunkO11yFallbackLogLabelNames, labelNames(labels))
}

func TestSplunkQueryLabels_FallbackOnConfigError(t *testing.T) {
	s := &SplunkLogSource{
		GetConfigs: func(*security.RequestContext, string) (integrations.SplunkO11yConnConfig, error) {
			return integrations.SplunkO11yConnConfig{}, errors.New("no config")
		},
		LogSearch: func(integrations.SplunkO11yConnConfig, string, int64, int64, int) ([]integrations.O11yLogEntry, error) {
			t.Fatal("log search must not be called when config lookup fails")
			return nil, nil
		},
	}
	labels, err := s.QueryLabels(mockRequestContext(), FetchLogLabelRequest{AccountId: "acct-1"})
	require.NoError(t, err)
	assert.Equal(t, splunkO11yFallbackLogLabelNames, labelNames(labels))
}

func TestDedupeO11yFieldLabels_DistinctSortedMergedWithFallback(t *testing.T) {
	entries := []integrations.O11yLogEntry{
		{Attributes: map[string]any{"b_custom": 1, "a_custom": 2}},
		{Attributes: map[string]any{"a_custom": 3, "c_custom": 4, "": "skip-empty-key"}},
	}
	names := labelNames(dedupeO11yFieldLabels(entries))
	// Distinct custom keys present, empty key skipped...
	assert.Contains(t, names, "a_custom")
	assert.Contains(t, names, "b_custom")
	assert.Contains(t, names, "c_custom")
	assert.NotContains(t, names, "")
	// ...merged with the fallback set, sorted, no duplicates.
	assert.Subset(t, names, splunkO11yFallbackLogLabelNames)
	assert.True(t, sort.StringsAreSorted(names), "labels must be sorted")
	seen := map[string]bool{}
	for _, n := range names {
		assert.False(t, seen[n], "no duplicate label %q", n)
		seen[n] = true
	}
}
