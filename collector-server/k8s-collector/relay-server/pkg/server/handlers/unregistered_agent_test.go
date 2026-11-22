package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"nudgebee/relay-server/pkg/config"
)

// wsEnabledStore is a store where websocket relaying is allowed (the default
// for an account with no row) but the agent connection state is configurable.
type wsEnabledStore struct {
	fakeStore
	connected bool
	connErr   error
}

func (s *wsEnabledStore) IsWSEnabled(context.Context, string, string) (bool, string, error) {
	return true, "", nil
}

func (s *wsEnabledStore) IsAgentConnected(context.Context, string, string) (bool, error) {
	return s.connected, s.connErr
}

// The api, grafana and prometheus proxies publish to relay_requests_{acct}. When
// the account's agent never registered that queue does not exist and the broker
// drops the message as unroutable, so they must answer 503 without publishing.
func TestProxyHandlers_AgentNotConnectedDoesNotPublish(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meter := noop.NewMeterProvider().Meter("test")
	tr := tracenoop.NewTracerProvider().Tracer("test")
	cfg := &config.Config{}

	build := map[string]func(store *wsEnabledStore, rpc *recordingRPCClient) gin.HandlerFunc{
		"/api/proxy": func(s *wsEnabledStore, rpc *recordingRPCClient) gin.HandlerFunc {
			return NewAPIProxyHandler(s, rpc, nil, cfg, nil, &tr, &meter, routingLogger())
		},
		"/grafana": func(s *wsEnabledStore, rpc *recordingRPCClient) gin.HandlerFunc {
			return NewGrafanaHandler(s, rpc, nil, cfg, nil, &tr, &meter, routingLogger())
		},
		"/prometheus": func(s *wsEnabledStore, rpc *recordingRPCClient) gin.HandlerFunc {
			return NewPrometheusHandler(s, rpc, nil, cfg, &tr, &meter, routingLogger())
		},
	}

	for path, mk := range build {
		for _, tc := range []struct {
			name      string
			connected bool
			connErr   error
			wantCode  int
			wantCalls int
		}{
			{"not connected", false, nil, http.StatusServiceUnavailable, 0},
			{"store error", false, assert.AnError, http.StatusInternalServerError, 0},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				rpc := &recordingRPCClient{}
				store := &wsEnabledStore{connected: tc.connected, connErr: tc.connErr}
				r := gin.New()
				r.Any(path, mk(store, rpc))

				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("X-NB-ACCOUNT-ID", "acct-1")
				req.Header.Set("X-API-Base-URL", "http://example.invalid")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)

				require.Equal(t, tc.wantCode, w.Code, w.Body.String())
				assert.Len(t, rpc.routingKeys(), tc.wantCalls)
			})
		}
	}
}
