package integrations

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
)

// Test configuration - set these environment variables to run integration tests:
// SPLUNK_O11Y_REALM        - Splunk Observability Cloud realm (e.g. us1)
// SPLUNK_O11Y_ACCESS_TOKEN - Splunk Observability Cloud organization access token

func getSplunkO11yTestConfig() (realm, accessToken string, skip bool) {
	realm = os.Getenv("SPLUNK_O11Y_REALM")
	accessToken = os.Getenv("SPLUNK_O11Y_ACCESS_TOKEN")
	if realm == "" || accessToken == "" {
		return "", "", true
	}
	return realm, accessToken, false
}

// --- Unit tests: integration struct ---

func TestSplunkO11y_Name(t *testing.T) {
	s := SplunkO11y{}
	assert.Equal(t, IntegrationSplunkO11y, s.Name())
	assert.Equal(t, "splunk_observability_platform", s.Name())
}

func TestSplunkO11y_Category(t *testing.T) {
	s := SplunkO11y{}
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, s.Category())
}

func TestSplunkO11y_ConfigSchema(t *testing.T) {
	s := SplunkO11y{}
	schema := s.ConfigSchema()

	assert.Contains(t, schema.Required, SplunkO11yConfigRealm)
	assert.Contains(t, schema.Required, SplunkO11yConfigAccessToken)

	assert.Contains(t, schema.Properties, SplunkO11yConfigRealm)
	assert.Contains(t, schema.Properties, SplunkO11yConfigAccessToken)
	assert.Contains(t, schema.Properties, core.DefaultMetricsProvider)

	// Access token must be marked encrypted
	assert.True(t, schema.Properties[SplunkO11yConfigAccessToken].IsEncrypted)
	// Realm must not be encrypted
	assert.False(t, schema.Properties[SplunkO11yConfigRealm].IsEncrypted)
}

// --- Unit tests: query escaping ---

func TestEscapeO11yQueryString_Colon(t *testing.T) {
	assert.Equal(t, `field\:value`, EscapeO11yQueryString("field:value"))
}

func TestEscapeO11yQueryString_Newline(t *testing.T) {
	assert.Equal(t, "line1 line2", EscapeO11yQueryString("line1\nline2"))
}

func TestEscapeO11yQueryString_SpecialChars(t *testing.T) {
	result := EscapeO11yQueryString(`a+b-c!d(e)`)
	assert.Equal(t, `a\+b\-c\!d\(e\)`, result)
}

func TestEscapeO11yQueryString_Clean(t *testing.T) {
	assert.Equal(t, "normal string", EscapeO11yQueryString("normal string"))
}

func TestEscapeO11yFieldValue_Simple(t *testing.T) {
	assert.Equal(t, `my\-service`, EscapeO11yFieldValue("my-service"))
}

func TestEscapeO11yFieldValue_WithSpaces(t *testing.T) {
	// Values with spaces should be quoted
	result := EscapeO11yFieldValue("my service name")
	assert.Equal(t, `"my service name"`, result)
}

func TestEscapeO11yFieldValue_WithInternalQuote(t *testing.T) {
	result := EscapeO11yFieldValue(`say "hello"`)
	assert.Equal(t, `"say \"hello\""`, result)
}

// --- Unit tests: connection config ---

func TestSplunkO11yConnConfig_APIBase(t *testing.T) {
	cfg := SplunkO11yConnConfig{Realm: "us1", AccessToken: "tok"}
	assert.Equal(t, "https://api.us1.signalfx.com", cfg.apiBase())
}

func TestSplunkO11yConnConfig_StreamBase(t *testing.T) {
	cfg := SplunkO11yConnConfig{Realm: "eu0", AccessToken: "tok"}
	assert.Equal(t, "https://stream.eu0.signalfx.com", cfg.streamBase())
}

// --- Unit tests: SignalFlow response parsing ---

func TestParseSignalFlowResponseTypes(t *testing.T) {
	// Verify SignalFlow struct fields compile and hold expected values.
	var meta SignalFlowMetadataLine
	meta.TsID = "ts1"
	meta.Properties = map[string]any{"sf_metric": "cpu.utilization", "host": "web-1"}
	assert.Equal(t, "ts1", meta.TsID)
	assert.Equal(t, "cpu.utilization", meta.Properties["sf_metric"])

	var dl SignalFlowDataLine
	dl.LogicalTimestampMs = 1700000000000
	dl.Data = []SignalFlowDataEntry{{TsID: "ts1", Value: 42.5}}
	assert.Equal(t, float64(42.5), dl.Data[0].Value)
	assert.Equal(t, "ts1", dl.Data[0].TsID)
	assert.Equal(t, int64(1700000000000), dl.LogicalTimestampMs)
}

func TestParseSignalFlowSSE(t *testing.T) {
	// Exact SSE format returned by the real Splunk Observability Cloud API.
	sseData := "event: control-message\ndata: {\ndata:   \"event\" : \"STREAM_START\"\ndata: }\n\n" +
		"event: metadata\ndata: {\ndata:   \"properties\" : {\ndata:     \"sf_originatingMetric\" : \"http.client.duration_bucket\",\ndata:     \"sf_metric\" : \"_SF_COMP_01\"\ndata:   },\ndata:   \"tsId\" : \"ts1\"\ndata: }\n\n" +
		"event: data\nid: data-123\ndata: {\ndata:   \"data\" : [ {\ndata:     \"tsId\" : \"ts1\",\ndata:     \"value\" : 42.5\ndata:   } ],\ndata:   \"logicalTimestampMs\" : 1700000000000\ndata: }\n\n" +
		"event: control-message\ndata: {\ndata:   \"event\" : \"END_OF_CHANNEL\"\ndata: }\n\n"

	points, err := parseSignalFlowResponse(strings.NewReader(sseData))
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, "http.client.duration_bucket", points[0].MetricName)
	assert.InDelta(t, 42.5, points[0].Value, 0.001)
	assert.Equal(t, int64(1700000000000), points[0].TimestampMs)
}

// --- Unit tests: trigger time parsing (from webhook, still in use) ---

func TestParseSplunkTriggerTime_EpochString(t *testing.T) {
	result := parseSplunkTriggerTime("1700000000")
	assert.Equal(t, int64(1700000000), result.Unix())
}

func TestParseSplunkTriggerTime_EpochFloat(t *testing.T) {
	result := parseSplunkTriggerTime(float64(1700000000))
	assert.Equal(t, int64(1700000000), result.Unix())
}

func TestParseSplunkTriggerTime_EpochInt64(t *testing.T) {
	result := parseSplunkTriggerTime(int64(1700000000))
	assert.Equal(t, int64(1700000000), result.Unix())
}

func TestParseSplunkTriggerTime_RFC3339(t *testing.T) {
	result := parseSplunkTriggerTime("2023-11-14T22:13:20Z")
	assert.False(t, result.IsZero())
	assert.Equal(t, 2023, result.Year())
}

func TestParseSplunkTriggerTime_Nil(t *testing.T) {
	result := parseSplunkTriggerTime(nil)
	assert.True(t, result.IsZero())
}

func TestParseSplunkTriggerTime_Invalid(t *testing.T) {
	result := parseSplunkTriggerTime("not-a-time")
	assert.True(t, result.IsZero())
}

// --- Unit tests: severity mapping (webhook, still in use) ---

func TestMapSplunkSeverity_Critical(t *testing.T) {
	assert.Equal(t, event.EventPriortiyHigh, mapSplunkSeverity("critical", "", ""))
}

func TestMapSplunkSeverity_High(t *testing.T) {
	assert.Equal(t, event.EventPriortiyHigh, mapSplunkSeverity("high", "", ""))
}

func TestMapSplunkSeverity_Error(t *testing.T) {
	assert.Equal(t, event.EventPriortiyHigh, mapSplunkSeverity("error", "", ""))
}

func TestMapSplunkSeverity_Medium(t *testing.T) {
	assert.Equal(t, event.EventPriortiyMedium, mapSplunkSeverity("medium", "", ""))
}

func TestMapSplunkSeverity_Warning(t *testing.T) {
	assert.Equal(t, event.EventPriortiyMedium, mapSplunkSeverity("warning", "", ""))
}

func TestMapSplunkSeverity_Low(t *testing.T) {
	assert.Equal(t, event.EventPriortiyLow, mapSplunkSeverity("low", "", ""))
}

func TestMapSplunkSeverity_Info(t *testing.T) {
	assert.Equal(t, event.EventPriortiyInfo, mapSplunkSeverity("info", "", ""))
}

func TestMapSplunkSeverity_Debug(t *testing.T) {
	assert.Equal(t, event.EventPriortiyInfo, mapSplunkSeverity("debug", "", ""))
}

func TestMapSplunkSeverity_FallbackToUrgency(t *testing.T) {
	assert.Equal(t, event.EventPriortiyHigh, mapSplunkSeverity("", "critical", ""))
}

func TestMapSplunkSeverity_FallbackToPriority(t *testing.T) {
	assert.Equal(t, event.EventPriortiyMedium, mapSplunkSeverity("", "", "medium"))
}

func TestMapSplunkSeverity_Unknown(t *testing.T) {
	assert.Equal(t, event.EventPriortiyLow, mapSplunkSeverity("", "", ""))
}

func TestMapSplunkSeverity_CaseInsensitive(t *testing.T) {
	assert.Equal(t, event.EventPriortiyHigh, mapSplunkSeverity("CRITICAL", "", ""))
	assert.Equal(t, event.EventPriortiyMedium, mapSplunkSeverity("Warning", "", ""))
}

// --- Unit tests: webhook struct ---

func TestSplunkWebhook_Name(t *testing.T) {
	s := SplunkWebhook{}
	assert.Equal(t, IntegrationSplunkWebhook, s.Name())
}

func TestSplunkWebhook_Category(t *testing.T) {
	s := SplunkWebhook{}
	assert.Equal(t, core.IntegrationCategoryIncidentWebhook, s.Category())
}

func TestSplunkWebhook_ValidateConfig_AlwaysEmpty(t *testing.T) {
	s := SplunkWebhook{}
	errs := s.ValidateConfig(nil, nil, "")
	assert.Empty(t, errs)
}

func TestSplunkWebhook_TimestampParsing(t *testing.T) {
	_ = time.Now() // ensure time pkg is used
	tests := []struct {
		input    interface{}
		expected int64
		zero     bool
	}{
		{"1700000000", 1700000000, false},
		{float64(1700000000), 1700000000, false},
		{int64(1700000000), 1700000000, false},
		{nil, 0, true},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		result := parseSplunkTriggerTime(tt.input)
		if tt.zero {
			assert.True(t, result.IsZero(), "expected zero time for input %v", tt.input)
		} else {
			assert.Equal(t, tt.expected, result.Unix(), "for input %v", tt.input)
		}
	}
}

// --- Integration tests (require environment variables) ---

func TestSplunkO11y_ValidateConnection(t *testing.T) {
	realm, accessToken, skip := getSplunkO11yTestConfig()
	if skip {
		t.Skip("Skipping integration test: SPLUNK_O11Y_REALM and SPLUNK_O11Y_ACCESS_TOKEN not set")
	}

	cfg := SplunkO11yConnConfig{Realm: realm, AccessToken: accessToken}
	err := validateSplunkO11yConnection(cfg)
	require.NoError(t, err, "Splunk O11y connection validation should succeed with valid credentials")
}

func TestSplunkO11y_LogSearch(t *testing.T) {
	realm, accessToken, skip := getSplunkO11yTestConfig()
	if skip {
		t.Skip("Skipping integration test: SPLUNK_O11Y_REALM and SPLUNK_O11Y_ACCESS_TOKEN not set")
	}

	cfg := SplunkO11yConnConfig{Realm: realm, AccessToken: accessToken}
	endMs := time.Now().UnixMilli()
	startMs := time.Now().Add(-1 * time.Hour).UnixMilli()

	entries, err := ExecuteO11yLogSearch(cfg, "", startMs, endMs, 10)
	if err != nil && strings.Contains(err.Error(), "Log Observer feature may not be enabled") {
		t.Skipf("Skipping: Log Observer not provisioned for this org: %v", err)
	}
	require.NoError(t, err, "Log Observer search should succeed with valid credentials")
	_ = entries // results may be empty depending on data
}

func TestSplunkO11y_SignalFlow(t *testing.T) {
	realm, accessToken, skip := getSplunkO11yTestConfig()
	if skip {
		t.Skip("Skipping integration test: SPLUNK_O11Y_REALM and SPLUNK_O11Y_ACCESS_TOKEN not set")
	}

	cfg := SplunkO11yConnConfig{Realm: realm, AccessToken: accessToken}
	stopMs := time.Now().Add(-1 * time.Hour).UnixMilli()
	startMs := time.Now().Add(-2 * time.Hour).UnixMilli()

	points, err := ExecuteSignalFlow(cfg, "data('http.client.duration_bucket').mean().publish()", startMs, stopMs, 0)
	require.NoError(t, err, "SignalFlow execute should succeed with valid credentials")
	assert.NotEmpty(t, points, "expected at least one data point")
	if len(points) > 0 {
		assert.NotEmpty(t, points[0].MetricName, "expected MetricName to be set from sf_originatingMetric")
		t.Logf("First point: metric=%s ts=%d value=%f labels=%v", points[0].MetricName, points[0].TimestampMs, points[0].Value, points[0].Labels)
	}
}

func TestSplunkO11y_InvalidToken(t *testing.T) {
	realm, _, skip := getSplunkO11yTestConfig()
	if skip {
		t.Skip("Skipping integration test: SPLUNK_O11Y_REALM not set")
	}

	cfg := SplunkO11yConnConfig{Realm: realm, AccessToken: "invalid-token-xyz-000"}
	err := validateSplunkO11yConnection(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}
