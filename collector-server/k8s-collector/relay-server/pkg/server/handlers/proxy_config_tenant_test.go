package handlers

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	tracer "go.opentelemetry.io/otel/trace/noop"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
)

func setupProxyConfigRouter(store db.AgentStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{}
	tr := tracer.NewTracerProvider().Tracer("test")
	meter := noop.NewMeterProvider().Meter("test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	topo := &fakeEnsurer{}
	r.POST("/proxy/config/push", NewProxyConfigPushHandler(store, fakeRPCClient{}, topo, cfg, nil, &tr, &meter, logger))
	r.POST("/proxy/config/test", NewProxyConfigTestHandler(store, fakeRPCClient{}, topo, cfg, nil, &tr, &meter, logger))
	r.POST("/proxy/resync-inventory", NewResyncInventoryHandler(store, fakeRPCClient{}, topo, cfg, nil, &tr, &meter, logger))
	return r
}

// The proxy config routes take the account from the body. A caller acting for
// a tenant (X-TENANT-ID, set by the app proxy from the session) must own it;
// internal callers that send no tenant are unaffected.
func TestProxyConfigRoutes_TenantMustOwnAccount(t *testing.T) {
	const (
		own   = "11111111-1111-1111-1111-11111111111a"
		other = "22222222-2222-2222-2222-22222222222b"
	)
	// allowed=false: a request that gets past the tenant check stops at the
	// agent-not-connected branch (push queues it with 202, the others answer
	// 503) before anything is sent to an agent. passed = that branch's code.
	const passed = 0
	store := &fakeStore{allowed: false, tenants: map[string]string{own: "tenant-a", other: "tenant-b"}}
	router := setupProxyConfigRouter(store)

	for _, path := range []string{"/proxy/config/push", "/proxy/config/test", "/proxy/resync-inventory"} {
		for _, tc := range []struct {
			name     string
			tenant   string
			account  string
			wantCode int
		}{
			{"internal caller, no tenant", "", other, passed},
			{"own account", "tenant-a", own, passed},
			{"own account, header case differs", "TENANT-A", own, passed},
			{"other tenant's account", "tenant-a", other, http.StatusForbidden},
			{"unknown account", "tenant-a", "33333333-3333-3333-3333-33333333333c", http.StatusForbidden},
			{"not a uuid", "tenant-a", "../x", http.StatusBadRequest},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				body := fmt.Sprintf(`{"account_id":%q,"datasources":[],"datasource":{}}`, tc.account)
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				if tc.tenant != "" {
					req.Header.Set("X-TENANT-ID", tc.tenant)
				}
				router.ServeHTTP(w, req)
				if tc.wantCode == passed {
					want := http.StatusServiceUnavailable
					if path == "/proxy/config/push" {
						want = http.StatusAccepted
					}
					require.Equal(t, want, w.Code, w.Body.String())
					return
				}
				require.Equal(t, tc.wantCode, w.Code, w.Body.String())
			})
		}
	}
}
