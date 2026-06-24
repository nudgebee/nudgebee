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
