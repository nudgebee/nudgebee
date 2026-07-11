package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wrapAgentData mirrors the relay/agent envelope: the agent's payload arrives as
// a JSON-stringified string under resp["data"]["data"] (the dispatch contract).
func wrapAgentData(payload string) map[string]any {
	return map[string]any{"data": map[string]any{"data": payload}}
}

func TestExtractIndexFieldValues_GoAgentBuckets(t *testing.T) {
	var s ElasticSource
	resp := wrapAgentData(`{
		"aggregations": {
			"unique_values": {
				"buckets": [
					{"key": "nudgebee-agent", "doc_count": 24970629},
					{"key": "nudgebee", "doc_count": 181230},
					{"key": "iteration-prod", "doc_count": 65742}
				]
			}
		}
	}`)

	got, err := s.ExtractIndexFieldValues(resp)
	require.NoError(t, err)
	assert.Equal(t, []string{"nudgebee-agent", "nudgebee", "iteration-prod"}, got)
}

func TestExtractIndexFieldValues_EmptyBuckets(t *testing.T) {
	var s ElasticSource
	got, err := s.ExtractIndexFieldValues(wrapAgentData(`{"aggregations":{"unique_values":{"buckets":[]}}}`))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestExtractIndexFieldValues_NumericKeys(t *testing.T) {
	var s ElasticSource
	// Non-keyword fields produce numeric bucket keys; they must be rendered, not dropped.
	got, err := s.ExtractIndexFieldValues(wrapAgentData(`{"aggregations":{"unique_values":{"buckets":[{"key":200},{"key":404}]}}}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"200", "404"}, got)
}

func TestExtractIndexFieldValues_SkipsNullAndEmptyKeys(t *testing.T) {
	var s ElasticSource
	got, err := s.ExtractIndexFieldValues(wrapAgentData(`{"aggregations":{"unique_values":{"buckets":[{"key":null},{"key":""},{"key":"keep"}]}}}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"keep"}, got)
}

func TestExtractIndexFieldValues_InnerDataNotString(t *testing.T) {
	var s ElasticSource
	// The legacy []any shape is no longer accepted — the agent sends a JSON string.
	resp := map[string]any{"data": map[string]any{"data": []any{"a", "b"}}}
	_, err := s.ExtractIndexFieldValues(resp)
	require.Error(t, err)
}

func TestExtractIndexFieldValues_OuterDataMissing(t *testing.T) {
	var s ElasticSource
	_, err := s.ExtractIndexFieldValues(map[string]any{"nope": 1})
	require.Error(t, err)
}

func TestExtractIndexFieldValues_InvalidInnerJSON(t *testing.T) {
	var s ElasticSource
	_, err := s.ExtractIndexFieldValues(wrapAgentData(`{not json`))
	require.Error(t, err)
}

// TestParseSourceMap_OTelShape is the regression guard for the Fluent-Bit -> OTel
// collector migration: OTel-native docs (data stream logs-generic.otel-default)
// carry the message at body.text, the stream at attributes.log.iostream, and k8s
// identifiers under resource.attributes — there is no top-level "log" field. Before
// the fix ParseSourceMap required "log" and dropped every such hit (empty API result).
func TestParseSourceMap_OTelShape(t *testing.T) {
	src := map[string]any{
		"@timestamp": "1781559826466.265767",
		"body":       map[string]any{"text": "I0615 21:43:46 registry.go:405] L7_EVENT"},
		"attributes": map[string]any{"log.iostream": "stderr", "log.file.path": "/var/log/pods/x/0.log"},
		"resource": map[string]any{"attributes": map[string]any{
			"k8s.namespace.name": "nudgebee-agent",
			"k8s.pod.name":       "nudgebee-agent-node-agent-8kcd5",
			"service.name":       "nudgebee-agent",
		}},
	}

	got, ok := ParseSourceMap(src)
	require.True(t, ok, "OTel doc must not be dropped")
	assert.Equal(t, "I0615 21:43:46 registry.go:405] L7_EVENT", got.Message)
	// epoch-millis(.micros) string normalized to RFC3339 with nanosecond precision.
	assert.Equal(t, "2026-06-15T21:43:46.466265767Z", got.Timestamp)
	assert.Equal(t, "ERROR", got.Severity) // stderr -> ERROR
	// resource.attributes + attributes flattened into labels (not nested maps).
	assert.Equal(t, "nudgebee-agent", got.Labels["k8s.namespace.name"])
	assert.Equal(t, "nudgebee-agent-node-agent-8kcd5", got.Labels["k8s.pod.name"])
	assert.Equal(t, "stderr", got.Labels["log.iostream"])
	assert.NotContains(t, got.Labels, "resource", "resource must be flattened, not kept nested")
}

// TestParseSourceMap_OTelBodyString covers the body-as-bare-string variant.
func TestParseSourceMap_OTelBodyString(t *testing.T) {
	got, ok := ParseSourceMap(map[string]any{"@timestamp": "1781559826466", "body": "hello"})
	require.True(t, ok)
	assert.Equal(t, "hello", got.Message)
}

// TestParseSourceMap_FluentBitShape guards that the legacy Fluent-Bit/ECS path
// (message at "log", stream at "stream") is unchanged by the OTel fallback.
func TestParseSourceMap_FluentBitShape(t *testing.T) {
	got, ok := ParseSourceMap(map[string]any{
		"@timestamp":                "2026-06-15T21:43:46Z",
		"log":                       "legacy line",
		"stream":                    "stdout",
		"kubernetes.namespace_name": "nudgebee",
	})
	require.True(t, ok)
	assert.Equal(t, "legacy line", got.Message)
	assert.Equal(t, "INFO", got.Severity)
	assert.Equal(t, "2026-06-15T21:43:46Z", got.Timestamp) // ISO passes through unchanged
	assert.Equal(t, "nudgebee", got.Labels["kubernetes.namespace_name"])
}

func TestNormalizeESTimestamp(t *testing.T) {
	cases := map[string]string{
		"1781559826466.265767":   "2026-06-15T21:43:46.466265767Z", // epoch millis + sub-ms -> RFC3339 nanos
		"1781559826466":          "2026-06-15T21:43:46.466Z",       // epoch millis -> RFC3339 (trailing zeros trimmed)
		"2026-06-15T21:43:46Z":   "2026-06-15T21:43:46Z",           // ISO (no millis) passthrough
		"2026-06-15T21:43:46.5Z": "2026-06-15T21:43:46.5Z",         // ISO with millis passthrough
		"":                       "",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeESTimestamp(in), "input %q", in)
	}
}

// TestParseSourceMap_NoMessageDropped ensures a doc with neither "log" nor a body
// is still dropped (ok=false) rather than emitting an empty-message log.
func TestParseSourceMap_NoMessageDropped(t *testing.T) {
	_, ok := ParseSourceMap(map[string]any{"@timestamp": "1781559826466", "resource": map[string]any{}})
	assert.False(t, ok)
}

// TestParseSourceMap_AttributesOnly covers an OTel doc that carries `attributes`
// but no `resource.attributes` (non-k8s OTel / resource detection disabled).
// attributes must still be flattened, not copied as a nested map.
func TestParseSourceMap_AttributesOnly(t *testing.T) {
	got, ok := ParseSourceMap(map[string]any{
		"@timestamp": "1781559826466",
		"body":       map[string]any{"text": "msg"},
		"attributes": map[string]any{"log.iostream": "stdout", "service.name": "svc"},
	})
	require.True(t, ok)
	assert.Equal(t, "svc", got.Labels["service.name"])
	assert.Equal(t, "stdout", got.Labels["log.iostream"])
	assert.NotContains(t, got.Labels, "attributes", "attributes must be flattened, not kept nested")
}
