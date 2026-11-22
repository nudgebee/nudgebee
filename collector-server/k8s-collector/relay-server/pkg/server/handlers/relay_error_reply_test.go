package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	tracer "go.opentelemetry.io/otel/trace/noop"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/models"
)

// The relay's own replies reached the HTTP proxy handlers (Grafana,
// Prometheus, API) and were parsed as a proxied HTTP response; their
// plain-text data failed that parse, so a request that expired in the queue
// came back as 500 "invalid HTTP response" instead of 504.
func TestWriteRelayErrorReply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oversized, _ := fitReplyToBroker(make([]byte, 17<<20), "req-1", 16<<20)

	tests := []struct {
		name       string
		reply      []byte
		wantStatus int
	}{
		{"expired in queue", expiredRequestReply, http.StatusGatewayTimeout},
		{"agent connection lost", []byte(`{"status_code":502,"action":"error","data":"agent connection lost"}`), http.StatusBadGateway},
		{"too big for the broker", oversized, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ar models.AgentResponse
			require.NoError(t, json.Unmarshal(tt.reply, &ar))
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			require.True(t, writeRelayErrorReply(c, ar))
			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Contains(t, w.Body.String(), ar.Data)
		})
	}
}

// An agent's proxied response is left for the caller to unwrap, whatever
// status the proxied service returned.
func TestWriteRelayErrorReply_IgnoresAgentResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, action := range []string{"response", "http_request", ""} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		assert.False(t, writeRelayErrorReply(c, models.AgentResponse{StatusCode: 500, Action: action, Data: `{"status_code":500}`}), action)
		assert.Equal(t, 0, w.Body.Len())
	}
}

// End to end through the Prometheus proxy: before, this request was answered
// 500 "invalid HTTP response".
func TestPrometheusHandler_ExpiredRequestIs504(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.HTTP.WriteTimeout = time.Second
	cfg.RabbitMQ.ExchangeName = "exch"
	tr := tracer.NewTracerProvider().Tracer("test")
	meter := noop.NewMeterProvider().Meter("test")
	r := gin.New()
	r.Any("/prometheus-v2/*path", NewPrometheusHandler(&fakeStore{allowed: true}, fakeRPCClient{resp: expiredRequestReply},
		nil, cfg, &tr, &meter, slog.New(slog.NewTextHandler(io.Discard, nil))))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/prometheus-v2/api/v1/query?query=up", nil)
	req.Header.Set("X-NB-ACCOUNT-ID", "acct1")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
	assert.Contains(t, w.Body.String(), "expired in queue")
}
