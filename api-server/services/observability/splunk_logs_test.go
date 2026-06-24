package observability

import (
	"errors"
	"testing"

	"nudgebee/services/integrations"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// fakeSearcher — test double for o11yLogSearcher
// ---------------------------------------------------------------------------

type fakeSearcher struct {
	entries []integrations.O11yLogEntry
	err     error
}

func (f *fakeSearcher) Search(_ integrations.SplunkO11yConnConfig, _ string, _, _ int64, _ int) ([]integrations.O11yLogEntry, error) {
	return f.entries, f.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestSource returns a SplunkLogSource wired to the given fake searcher.
// It bypasses GetSplunkO11yConfigs by passing an empty config — the fake
// searcher ignores it, so no real credentials are needed.
func newTestSource(f *fakeSearcher) *SplunkLogSource {
	return newSplunkLogSourceWithSearcher(f)
}

// fakeCtx is a minimal stand-in for *security.RequestContext.
// QueryLabels only calls ctx.GetLogger() on the error path; passing nil is
// safe for the happy path. For the error paths we use a noopCtx below.
type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Debug(_ string, _ ...any) {}

// ---------------------------------------------------------------------------
// Unit tests: staticSplunkLabels
// ---------------------------------------------------------------------------

func TestStaticSplunkLabels_NeverEmpty(t *testing.T) {
	labels := staticSplunkLabels()
	require.NotEmpty(t, labels, "staticSplunkLabels() must never return an empty slice")
}

func TestStaticSplunkLabels_ContainsRequiredFields(t *testing.T) {
	required := []string{
		"message", "severity",
		"kubernetes.namespace.name", "kubernetes.pod.name", "service.name",
	}
	index := make(map[string]bool)
	for _, l := range staticSplunkLabels() {
		index[l.Label] = true
	}
	for _, f := range required {
		assert.True(t, index[f], "staticSplunkLabels() is missing required field %q", f)
	}
}

// ---------------------------------------------------------------------------
// Unit tests: QueryLabels — happy path (dynamic fields surfaced)
// ---------------------------------------------------------------------------

func TestQueryLabels_DynamicFieldsSurfaced(t *testing.T) {
	fake := &fakeSearcher{
		entries: []integrations.O11yLogEntry{
			{
				Timestamp: 1_700_000_000_000,
				Attributes: map[string]any{
					"message":                   "hello",
					"severity":                  "INFO",
					"service.version":           "1.2.3",     // custom OTel field
					"http.status_code":          "200",       // custom OTel field
					"app.tenant_id":             "acme-corp", // app-specific field
					"kubernetes.namespace.name": "production",
				},
			},
			{
				Timestamp: 1_700_000_001_000,
				Attributes: map[string]any{
					"message":        "world",
					"custom.field.x": "foo",
				},
			},
		},
	}

	src := newTestSource(fake)
	labels, err := src.QueryLabels(nil, FetchLogLabelRequest{
		AccountId: "test-account",
		StartTime: 1_700_000_000,
		EndTime:   1_700_003_600,
	})

	require.NoError(t, err)
	require.NotEmpty(t, labels)

	index := make(map[string]bool, len(labels))
	for _, l := range labels {
		index[l.Label] = true
	}

	// Custom OTel fields must be present.
	customFields := []string{"service.version", "http.status_code", "app.tenant_id", "custom.field.x"}
	for _, cf := range customFields {
		assert.True(t, index[cf], "custom field %q was not surfaced in QueryLabels output", cf)
	}

	// Static baseline fields must still be present.
	for _, sf := range splunkO11yKnownFields {
		assert.True(t, index[sf], "static field %q was unexpectedly dropped from QueryLabels output", sf)
	}
}

// ---------------------------------------------------------------------------
// Unit tests: QueryLabels — fallback on search error
// ---------------------------------------------------------------------------

func TestQueryLabels_FallbackOnSearchError(t *testing.T) {
	fake := &fakeSearcher{
		err: errors.New("connection refused"),
	}

	src := newTestSource(fake)
	labels, err := src.QueryLabels(nil, FetchLogLabelRequest{
		AccountId: "test-account",
	})

	// Must not propagate the error — degrade gracefully.
	require.NoError(t, err)
	require.NotEmpty(t, labels, "fallback must never return an empty label list")

	index := make(map[string]bool, len(labels))
	for _, l := range labels {
		index[l.Label] = true
	}
	for _, sf := range splunkO11yKnownFields {
		assert.True(t, index[sf], "fallback field %q missing on error path", sf)
	}
}

// ---------------------------------------------------------------------------
// Unit tests: QueryLabels — empty results still return static fields
// ---------------------------------------------------------------------------

func TestQueryLabels_EmptyResultsReturnStaticFields(t *testing.T) {
	fake := &fakeSearcher{
		entries: []integrations.O11yLogEntry{}, // search succeeds but returns nothing
	}

	src := newTestSource(fake)
	labels, err := src.QueryLabels(nil, FetchLogLabelRequest{
		AccountId: "test-account",
	})

	require.NoError(t, err)
	require.NotEmpty(t, labels, "static fields must fill in when dynamic search returns no entries")

	index := make(map[string]bool, len(labels))
	for _, l := range labels {
		index[l.Label] = true
	}
	for _, sf := range splunkO11yKnownFields {
		assert.True(t, index[sf], "static field %q missing when sample returns zero entries", sf)
	}
}

// ---------------------------------------------------------------------------
// Unit tests: QueryLabels — no duplicate labels
// ---------------------------------------------------------------------------

func TestQueryLabels_NoDuplicateLabels(t *testing.T) {
	// Return entries that contain only fields already in splunkO11yKnownFields.
	fake := &fakeSearcher{
		entries: []integrations.O11yLogEntry{
			{Attributes: map[string]any{"message": "hi", "severity": "INFO"}},
			{Attributes: map[string]any{"message": "bye", "severity": "ERROR"}},
		},
	}

	src := newTestSource(fake)
	labels, err := src.QueryLabels(nil, FetchLogLabelRequest{AccountId: "test-account"})

	require.NoError(t, err)

	seen := make(map[string]int)
	for _, l := range labels {
		seen[l.Label]++
	}
	for field, count := range seen {
		assert.Equal(t, 1, count, "field %q appears %d times; duplicates not allowed", field, count)
	}
}