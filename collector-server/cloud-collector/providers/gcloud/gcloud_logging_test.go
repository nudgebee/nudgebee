package gcloud

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"cloud.google.com/go/logging"
	mrpb "google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/stretchr/testify/assert"
)

// TestLogEntryToMessageCapturesHTTPRequest verifies the structured-attribute capture:
// request fields (status / IP / URL / method / UA / latency) and trace context that the
// old flattened model dropped are now preserved generically, for any GCP log type.
func TestLogEntryToMessageCapturesHTTPRequest(t *testing.T) {
	entry := &logging.Entry{
		Timestamp: time.Unix(1700000000, 0),
		Severity:  logging.Warning,
		Payload:   "unauthorized",
		LogName:   "projects/full-auth/logs/requests",
		HTTPRequest: &logging.HTTPRequest{
			Status:   401,
			RemoteIP: "203.0.113.7",
			LocalIP:  "10.0.0.1",
			Latency:  150 * time.Millisecond,
			Request: &http.Request{
				Method: "POST",
				URL:    &url.URL{Scheme: "https", Host: "app.example.com", Path: "/oauth/token"},
				Header: http.Header{"User-Agent": []string{"curl/8.0"}},
			},
		},
		Trace:  "projects/full-auth/traces/abc",
		SpanID: "0123456789abcdef",
	}

	msg := logEntryToMessage(entry)

	assert.Equal(t, 401, msg.Attributes["http.response.status_code"])
	assert.Equal(t, "203.0.113.7", msg.Attributes["client.address"])
	assert.Equal(t, "10.0.0.1", msg.Attributes["server.address"])
	assert.Equal(t, "POST", msg.Attributes["http.request.method"])
	assert.Equal(t, "https://app.example.com/oauth/token", msg.Attributes["url.full"])
	assert.Equal(t, "curl/8.0", msg.Attributes["user_agent.original"])
	assert.Equal(t, int64(150), msg.Attributes["http.server.request.duration_ms"])
	assert.Equal(t, "projects/full-auth/traces/abc", msg.Attributes["trace.id"])
	assert.Equal(t, "0123456789abcdef", msg.Attributes["span.id"])

	// Back-compat: message + labels still populated as before.
	assert.Equal(t, "unauthorized", msg.Message)
	hasSeverity := false
	for _, l := range msg.Labels {
		if l.Label == "severity" {
			hasSeverity = true
		}
	}
	assert.True(t, hasSeverity, "severity label should still be present")
}

// TestLogEntryToMessageNoAttributesWhenAbsent ensures attributes are omitted (not an empty
// map) when the entry carries no structured fields, preserving the omitempty wire shape.
func TestLogEntryToMessageNoAttributesWhenAbsent(t *testing.T) {
	entry := &logging.Entry{
		Timestamp: time.Unix(1700000000, 0),
		Payload:   "hello",
	}
	msg := logEntryToMessage(entry)
	assert.Nil(t, msg.Attributes)
}

// TestExtractResourceSpecificAttrs_AppEngineLatency: App Engine arrives as a proto payload,
// so extraction goes through the serialized-message fallback. Latency lives only in the
// RequestLog (not entry.HTTPRequest); surface it without clobbering existing attrs.
func TestExtractResourceSpecificAttrs_AppEngineLatency(t *testing.T) {
	attrs := map[string]any{"http.response.status_code": 500} // already set by HTTPRequest
	message := `{"app_id":"s~p","latency":{"nanos":54968000},"status":500,"method":"POST",` +
		`"resource":"/api/social-login/google","host":"app.example.com","response_size":1234}`
	extractResourceSpecificAttrs("gae_app", nil, message, attrs) // nil payload -> message fallback

	assert.Equal(t, int64(54), attrs["http.server.request.duration_ms"])
	assert.Equal(t, "POST", attrs["http.request.method"])
	assert.Equal(t, "/api/social-login/google", attrs["url.path"])
	assert.Equal(t, "app.example.com", attrs["server.address"])
	assert.Equal(t, int64(1234), attrs["http.response.body.size"])
	assert.Equal(t, 500, attrs["http.response.status_code"], "must not clobber existing attribute")
}

// TestExtractResourceSpecificAttrs_DirectMapNumeric: a raw map payload (no JSON round-trip)
// with native int numerics must still extract via asFloat64.
func TestExtractResourceSpecificAttrs_DirectMapNumeric(t *testing.T) {
	attrs := map[string]any{}
	payload := map[string]any{
		"latency": map[string]any{"nanos": 54968000},
		"status":  500, // native int, not float64
	}
	extractResourceSpecificAttrs("gae_app", payload, "", attrs)
	assert.Equal(t, int64(54), attrs["http.server.request.duration_ms"])
	assert.Equal(t, 500, attrs["http.response.status_code"])
}

// TestExtractResourceSpecificAttrs_LatencyForms: latency Duration may be an object
// ({seconds, nanos}) or the canonical string ("0.054968s").
func TestExtractResourceSpecificAttrs_LatencyForms(t *testing.T) {
	obj := map[string]any{}
	extractResourceSpecificAttrs("gae_app", nil, `{"latency":{"seconds":2,"nanos":500000000}}`, obj)
	assert.Equal(t, int64(2500), obj["http.server.request.duration_ms"])

	str := map[string]any{}
	extractResourceSpecificAttrs("gae_app", nil, `{"latency":"0.054968s"}`, str)
	assert.Equal(t, int64(54), str["http.server.request.duration_ms"])
}

// TestExtractResourceSpecificAttrs_LBStatusDetails: the LB arrives as a *structpb.Struct
// (jsonPayload); statusDetails — the canonical 5xx reason — must surface via AsMap (no
// JSON round-trip).
func TestExtractResourceSpecificAttrs_LBStatusDetails(t *testing.T) {
	payload, err := structpb.NewStruct(map[string]any{
		"statusDetails": "failed_to_connect_to_backend",
		"remoteIp":      "1.2.3.4",
	})
	assert.NoError(t, err)
	attrs := map[string]any{}
	extractResourceSpecificAttrs("http_load_balancer", payload, "", attrs)
	assert.Equal(t, "failed_to_connect_to_backend", attrs["gcp.lb.status_details"])
}

// TestExtractResourceSpecificAttrs_SafeNoOps: untargeted resource types and unresolvable
// payloads must be safe no-ops.
func TestExtractResourceSpecificAttrs_SafeNoOps(t *testing.T) {
	attrs := map[string]any{}
	extractResourceSpecificAttrs("cloud_run_revision", map[string]any{"statusDetails": "x"}, "", attrs)
	assert.Empty(t, attrs, "non-targeted resource type extracts nothing")
	extractResourceSpecificAttrs("gae_app", nil, "plain text log line", attrs)
	assert.Empty(t, attrs, "non-JSON message is a safe no-op")
	extractResourceSpecificAttrs("gae_app", nil, "", attrs)
	assert.Empty(t, attrs, "nil payload + empty message is a safe no-op")
}

// TestLogEntryToMessage_AppEngineRequestLogLatency exercises the full wiring: a structured
// App Engine RequestLog payload (no entry.HTTPRequest) must surface latency + request fields.
func TestLogEntryToMessage_AppEngineRequestLogLatency(t *testing.T) {
	entry := &logging.Entry{
		Timestamp: time.Unix(1700000000, 0),
		Severity:  logging.Info,
		Payload: map[string]any{
			"latency":  map[string]any{"nanos": 54968000},
			"method":   "POST",
			"resource": "/api/social-login/google",
			"status":   500,
		},
		Resource: &mrpb.MonitoredResource{Type: "gae_app"},
	}
	msg := logEntryToMessage(entry)
	assert.Equal(t, int64(54), msg.Attributes["http.server.request.duration_ms"])
	assert.Equal(t, "POST", msg.Attributes["http.request.method"])
	assert.Equal(t, "/api/social-login/google", msg.Attributes["url.path"])
	assert.Equal(t, 500, msg.Attributes["http.response.status_code"])
}
