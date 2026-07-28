package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInvestigateData_UnmarshalJSON_ErrorLogData covers the fallback ladder
// that keeps the whole events.Event decode from failing when upstream
// producers send error_log_data in shapes other than []string.
//
// Regression context: production ERROR log from account ff87fbfd
// ("eventsummary: unable to unmarshal event data") was caused by a payload
// where error_log_data carried embedded trace-span objects, which the
// []string decoder rejected — dropping the whole event onto the raw-string
// fallback path in agents/agent_events.go:1150.
func TestInvestigateData_UnmarshalJSON_ErrorLogData(t *testing.T) {
	t.Run("happy path — []string", func(t *testing.T) {
		payload := []byte(`{"error_log_data":["line one","line two"]}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Equal(t, []string{"line one", "line two"}, got.ErrorLogData)
	})

	t.Run("mixed array — string plus trace-span object", func(t *testing.T) {
		payload := []byte(`{"error_log_data":["log line",{"span_id":"abc","span_kind":"SERVER"}]}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Len(t, got.ErrorLogData, 2)
		assert.Equal(t, "log line", got.ErrorLogData[0])
		// The object entry survives as JSON-encoded text so the downstream
		// strings.Join(...) still produces useful evidence in the prompt.
		assert.Contains(t, got.ErrorLogData[1], `"span_id":"abc"`)
		assert.Contains(t, got.ErrorLogData[1], `"span_kind":"SERVER"`)
	})

	t.Run("single string — wrap into slice", func(t *testing.T) {
		payload := []byte(`{"error_log_data":"only one line"}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Equal(t, []string{"only one line"}, got.ErrorLogData)
	})

	t.Run("object payload — encode as single entry", func(t *testing.T) {
		payload := []byte(`{"error_log_data":{"span_id":"abc"}}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Len(t, got.ErrorLogData, 1)
		assert.Contains(t, got.ErrorLogData[0], `"span_id":"abc"`)
	})

	t.Run("missing field — nil", func(t *testing.T) {
		payload := []byte(`{"log_data":"only body"}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Nil(t, got.ErrorLogData)
		assert.Equal(t, "only body", got.LogData)
	})

	t.Run("explicit null — nil", func(t *testing.T) {
		payload := []byte(`{"error_log_data":null}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Nil(t, got.ErrorLogData)
	})

	t.Run("empty array — empty slice", func(t *testing.T) {
		payload := []byte(`{"error_log_data":[]}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Empty(t, got.ErrorLogData)
	})

	t.Run("other InvestigateData fields still populate", func(t *testing.T) {
		payload := []byte(`{"log_data":"body","error_log_data":["err1"],"pod_data":{"type":"pod"}}`)
		var got InvestigateData
		assert.NoError(t, json.Unmarshal(payload, &got))
		assert.Equal(t, "body", got.LogData)
		assert.Equal(t, []string{"err1"}, got.ErrorLogData)
		assert.Equal(t, "pod", got.PodData.Type)
	})
}
