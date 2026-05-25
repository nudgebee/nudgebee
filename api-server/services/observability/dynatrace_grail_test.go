package observability

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGrailMockServer creates a mock Dynatrace Grail HTTP server.
//
//   - executeStatusCode / executeBody — returned for POST /platform/storage/query/v1/query:execute
//   - pollBodies — returned sequentially for successive GET /platform/storage/query/v1/query:poll calls;
//     once exhausted the last entry is repeated
func newGrailMockServer(t *testing.T, executeStatusCode int, executeBody string, pollBodies []string) *httptest.Server {
	t.Helper()
	var pollIdx int32

	mux := http.NewServeMux()

	mux.HandleFunc("/platform/storage/query/v1/query:execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(executeStatusCode)
		_, _ = w.Write([]byte(executeBody))
	})

	mux.HandleFunc("/platform/storage/query/v1/query:poll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		idx := int(atomic.AddInt32(&pollIdx, 1)) - 1
		switch {
		case idx < len(pollBodies):
			_, _ = w.Write([]byte(pollBodies[idx]))
		case len(pollBodies) > 0:
			_, _ = w.Write([]byte(pollBodies[len(pollBodies)-1]))
		}
	})

	return httptest.NewServer(mux)
}

// ---- executeDQLQuery ----

func TestExecuteDQLQuery_ImmediateSuccess(t *testing.T) {
	body := `{"state":"SUCCEEDED","result":{"records":[{"span.id":"abc","trace.id":"xyz"}]}}`
	srv := newGrailMockServer(t, http.StatusOK, body, nil)
	defer srv.Close()

	result, err := executeDQLQuery(srv.URL, "test-token", "fetch spans | limit 1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Records, 1)
	assert.Equal(t, "abc", grailStr(result.Records[0], "span.id"))
}

func TestExecuteDQLQuery_PollOnce(t *testing.T) {
	// Execute returns RUNNING; one poll returns SUCCEEDED.
	// Note: grailPollInterval = 1s, so this test takes ~1 second.
	executeBody := `{"state":"RUNNING","requestToken":"tok-poll-1"}`
	pollBody := `{"state":"SUCCEEDED","result":{"records":[{"span.id":"polled-span"}]}}`
	srv := newGrailMockServer(t, http.StatusOK, executeBody, []string{pollBody})
	defer srv.Close()

	result, err := executeDQLQuery(srv.URL, "test-token", "fetch spans | limit 1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Records, 1)
	assert.Equal(t, "polled-span", grailStr(result.Records[0], "span.id"))
}

func TestExecuteDQLQuery_PollTwice(t *testing.T) {
	// Execute returns RUNNING; first poll still RUNNING; second poll SUCCEEDED.
	// This test takes ~2 seconds due to poll sleep.
	executeBody := `{"state":"RUNNING","requestToken":"tok-poll-2"}`
	running := `{"state":"RUNNING","requestToken":"tok-poll-2"}`
	success := `{"state":"SUCCEEDED","result":{"records":[{"span.id":"double-polled"}]}}`
	srv := newGrailMockServer(t, http.StatusOK, executeBody, []string{running, success})
	defer srv.Close()

	result, err := executeDQLQuery(srv.URL, "test-token", "fetch spans | limit 1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Records, 1)
}

func TestExecuteDQLQuery_FailedStateImmediate(t *testing.T) {
	body := `{"state":"FAILED","error":{"code":400,"message":"bad query syntax"}}`
	srv := newGrailMockServer(t, http.StatusOK, body, nil)
	defer srv.Close()

	_, err := executeDQLQuery(srv.URL, "test-token", "INVALID DQL")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "bad query syntax")
}

func TestExecuteDQLQuery_FailedStateAfterPoll(t *testing.T) {
	executeBody := `{"state":"RUNNING","requestToken":"tok-fail"}`
	pollBody := `{"state":"FAILED","error":{"code":422,"message":"unprocessable entity"}}`
	srv := newGrailMockServer(t, http.StatusOK, executeBody, []string{pollBody})
	defer srv.Close()

	_, err := executeDQLQuery(srv.URL, "test-token", "fetch spans")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "422")
}

func TestExecuteDQLQuery_HTTP401(t *testing.T) {
	srv := newGrailMockServer(t, http.StatusUnauthorized, `Unauthorized`, nil)
	defer srv.Close()

	_, err := executeDQLQuery(srv.URL, "bad-token", "fetch logs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestExecuteDQLQuery_HTTP403(t *testing.T) {
	srv := newGrailMockServer(t, http.StatusForbidden, `Forbidden`, nil)
	defer srv.Close()

	_, err := executeDQLQuery(srv.URL, "limited-token", "fetch logs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestExecuteDQLQuery_HTTP500(t *testing.T) {
	srv := newGrailMockServer(t, http.StatusInternalServerError, `Server Error`, nil)
	defer srv.Close()

	_, err := executeDQLQuery(srv.URL, "test-token", "fetch logs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestExecuteDQLQuery_MalformedJSON(t *testing.T) {
	srv := newGrailMockServer(t, http.StatusOK, `{invalid json`, nil)
	defer srv.Close()

	_, err := executeDQLQuery(srv.URL, "test-token", "fetch logs")
	require.Error(t, err)
}

func TestExecuteDQLQuery_NilResult(t *testing.T) {
	// SUCCEEDED but result field is null — should return an empty grailResult, not nil.
	body := `{"state":"SUCCEEDED","result":null}`
	srv := newGrailMockServer(t, http.StatusOK, body, nil)
	defer srv.Close()

	result, err := executeDQLQuery(srv.URL, "test-token", "fetch logs")
	require.NoError(t, err)
	require.NotNil(t, result, "should return empty grailResult instead of nil")
	assert.Empty(t, result.Records)
}

func TestExecuteDQLQuery_EmptyRecords(t *testing.T) {
	body := `{"state":"SUCCEEDED","result":{"records":[]}}`
	srv := newGrailMockServer(t, http.StatusOK, body, nil)
	defer srv.Close()

	result, err := executeDQLQuery(srv.URL, "test-token", "fetch logs")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Records)
}

func TestExecuteDQLQuery_NetworkError(t *testing.T) {
	_, err := executeDQLQuery("http://127.0.0.1:1", "test-token", "fetch logs")
	require.Error(t, err)
}

func TestExecuteDQLQuery_WithMetadata(t *testing.T) {
	// Verify that metadata is preserved in the result (for timeseries queries).
	body := `{
		"state": "SUCCEEDED",
		"result": {
			"records": [{"val":[1.0,2.0]}],
			"metadata": {
				"metrics": {
					"val": {"timestamps": ["2024-01-01T00:00:00Z","2024-01-01T00:05:00Z"]}
				}
			}
		}
	}`
	srv := newGrailMockServer(t, http.StatusOK, body, nil)
	defer srv.Close()

	result, err := executeDQLQuery(srv.URL, "test-token", "timeseries val = avg(x)")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Metadata)
	meta, ok := result.Metadata.Metrics["val"]
	require.True(t, ok)
	assert.Len(t, meta.Timestamps, 2)
}

// ---- grailStr ----

func TestGrailStr_PresentString(t *testing.T) {
	record := map[string]any{"span.id": "abc123"}
	assert.Equal(t, "abc123", grailStr(record, "span.id"))
}

func TestGrailStr_MissingKey(t *testing.T) {
	record := map[string]any{"other": "value"}
	assert.Equal(t, "", grailStr(record, "span.id"))
}

func TestGrailStr_NonStringInt64(t *testing.T) {
	record := map[string]any{"duration": int64(4756000)}
	assert.Equal(t, "", grailStr(record, "duration"))
}

func TestGrailStr_NonStringFloat64(t *testing.T) {
	record := map[string]any{"value": float64(3.14)}
	assert.Equal(t, "", grailStr(record, "value"))
}

func TestGrailStr_NonStringBool(t *testing.T) {
	record := map[string]any{"flag": true}
	assert.Equal(t, "", grailStr(record, "flag"))
}

func TestGrailStr_NilRecord(t *testing.T) {
	assert.Equal(t, "", grailStr(nil, "span.id"))
}

func TestGrailStr_EmptyStringValue(t *testing.T) {
	record := map[string]any{"span.id": ""}
	assert.Equal(t, "", grailStr(record, "span.id"))
}
