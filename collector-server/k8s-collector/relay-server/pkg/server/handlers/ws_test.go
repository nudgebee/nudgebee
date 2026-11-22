package handlers

import (
	"bytes"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/models"
)

func setupWSRouter(store *fakeStore, rpc fakeRPCClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{}
	cfg.HTTP.WriteTimeout = 50 * time.Millisecond
	cfg.RabbitMQ.ExchangeName = "exch"
	tr := tracenoop.NewTracerProvider().Tracer("test")
	meter := noop.NewMeterProvider().Meter("test")
	r.POST("/ws", WSHandler(store, rpc, cfg, &tr, &meter, slog.New(slog.NewTextHandler(io.Discard, nil))))
	return r
}

// /ws runs pod exec. It reads only the top-level account_id; a tenant must not
// be able to open a shell on another tenant's cluster by naming its account.
func TestWSHandler_TenantMustOwnAccount(t *testing.T) {
	ar := models.AgentResponse{StatusCode: 200, Data: `{"ok":true}`}

	for _, tc := range []struct {
		name     string
		tenant   string
		body     string
		wantCode int
	}{
		{"own account", "tenant-a", `{"account_id":"11111111-1111-1111-1111-11111111111a","action":"start"}`, 200},
		{"other tenant's account", "tenant-a", `{"account_id":"22222222-2222-2222-2222-22222222222b","action":"exec"}`, 403},
		{"own account nested, other tenant's at top level", "tenant-a", `{"account_id":"22222222-2222-2222-2222-22222222222b","action":"exec","body":{"account_id":"11111111-1111-1111-1111-11111111111a"}}`, 403},
		{"no tenant header (internal caller)", "", `{"account_id":"22222222-2222-2222-2222-22222222222b","action":"start"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{allowed: true, tenants: map[string]string{"11111111-1111-1111-1111-11111111111a": "tenant-a", "22222222-2222-2222-2222-22222222222b": "tenant-b"}}
			router := setupWSRouter(store, fakeRPCClient{resp: jsonMust(ar)})

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/ws", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.tenant != "" {
				req.Header.Set("X-TENANT-ID", tc.tenant)
			}
			router.ServeHTTP(w, req)

			require.Equal(t, tc.wantCode, w.Code, w.Body.String())
		})
	}
}
