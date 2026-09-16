package handlers

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/relay-server/pkg/server/health"
)

type stubBroker struct{ connected bool }

func (s *stubBroker) IsConnected() bool { return s.connected }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func probe(t *testing.T, tracker *health.Tracker, after time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz/live", Liveness(tracker, after, discardLogger()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz/live", nil))
	return rec
}

func TestLivenessOKWhenNothingIsStuck(t *testing.T) {
	tracker := health.NewTracker(&stubBroker{connected: true})

	rec := probe(t, tracker, 5*time.Minute)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestLivenessFailsWhenATenantIsStuck(t *testing.T) {
	tracker := health.NewTracker(&stubBroker{connected: true})
	tracker.ConsumeFailed("relay_requests_acct-1")

	// Any elapsed time beats a zero threshold... except zero disables the
	// check, so use the smallest positive one.
	rec := probe(t, tracker, time.Nanosecond)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// The kubelet copies a failing probe's body into the Pod's event, so the body
// must not name the queue — it embeds the account UUID.
func TestLivenessBodyDoesNotLeakTenantIdentifiers(t *testing.T) {
	tracker := health.NewTracker(&stubBroker{connected: true})
	tracker.ConsumeFailed("relay_requests_acct-1")

	rec := probe(t, tracker, time.Nanosecond)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, rec.Body.String(), "acct-1")
	assert.NotContains(t, rec.Body.String(), "relay_requests")
}

// A nil tracker must never take the pod down.
func TestLivenessWithoutATrackerIsHealthy(t *testing.T) {
	rec := probe(t, nil, 5*time.Minute)

	assert.Equal(t, http.StatusOK, rec.Code)
}
