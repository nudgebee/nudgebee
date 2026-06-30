package gcloud

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"cloud.google.com/go/logging"

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
