package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Cyprinus12138/otelgin"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"

	"nudgebee/relay-server/pkg/cache"
	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/handlers"
	"nudgebee/relay-server/pkg/server/middleware"
	"nudgebee/relay-server/pkg/signing"

	sloggin "github.com/samber/slog-gin"
	"go.opentelemetry.io/otel/trace"
)

// SetupRouter sets up all HTTP routes, middleware, and dependencies.
func SetupRouter(cfg *config.Config, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*gin.Engine, error) {
	// 1) Create Gin
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(sloggin.NewWithFilters(logger, sloggin.IgnorePath("/status")))
	r.Use(OtelMiddlewareWithIgnorePaths(config.SERVICE_NAME, []string{"/status"}))
	r.Use(traceResponseHeaderMiddleware())
	corsOpts := cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{
			http.MethodGet,
			http.MethodPost,
		},

		AllowHeaders: []string{
			"*",
		},
	})
	r.Use(corsOpts)
	// Gate pprof behind the same X-SECRET-KEY auth as the proxy routes.
	// pprof endpoints leak heap / goroutine state that can include
	// in-flight secrets and tokens, so they must not be reachable
	// unauthenticated even on internal-only ingress. SRE / profiling
	// workflows need to send X-SECRET-KEY to pull profiles.
	// Registered AFTER the global r.Use(...) calls above so the pprof
	// group inherits Recovery / slog / OTel / CORS — gin only snapshots
	// the parent middleware chain at Group() time, not retroactively.
	pprofGroup := r.Group("/debug/pprof", middleware.ClientAuthMiddleware(cfg.Security.SecretKey))
	pprof.RouteRegister(pprofGroup, "")
	// 2) Initialize shared services
	wsCache, err := cache.NewCache(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("cache init: %w", err)
	}

	store, err := db.NewPostgresStore(
		cfg.Postgres.DSN,
		cfg.Security.EncryptionKey,
		cfg.Postgres.MaxOpenConns,
		cfg.Postgres.MaxIdleConns,
		cfg.Postgres.ConnMaxLifetime,
		wsCache,
	)
	if err != nil {
		return nil, fmt.Errorf("db init: %w", err)
	}

	connMgr, err := mq.NewConnectionManager(cfg.RabbitMQ.URL)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq connection manager: %w", err)
	}
	// handle err…
	topo, err := mq.NewTopology(connMgr, cfg)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq topology: %w", err)
	}
	// handle err…
	rpcClient, err := mq.NewRPCClient(connMgr, logger, cfg)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq rpc client: %w", err)
	}

	// Initialize message signer for proxy agent messages (nil if disabled)
	signer, err := signing.NewSigner(cfg.Signing.PrivateKey, cfg.Signing.KeyID, logger)
	if err != nil {
		return nil, fmt.Errorf("message signer init: %w", err)
	}

	// 3) Public health check
	r.GET("/status", handlers.Status)

	// 4) Agent registration (WebSocket), protected by Basic‐auth on agent keys
	r.GET("/register",
		middleware.AgentAuthMiddleware(store),
		handlers.RegisterHandler(store, connMgr, topo, cfg, cfg.RabbitMQ.ExchangeName, signer, tracer, meter, logger),
	)

	// 5) Interactive shell over WS, protected by client secret
	r.POST("/ws",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.WSHandler(store, rpcClient, cfg, tracer, meter, logger),
	)

	// 6) Action request endpoint, protected by client secret
	r.POST("/request",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.NewRequestHandler(store, rpcClient, topo, cfg, signer, tracer, meter, logger),
	)

	// 7) Grafana proxy, protected by client secret
	r.Any("/grafana/*path",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.NewGrafanaHandler(store, rpcClient, topo, cfg, signer, tracer, meter, logger),
	)

	// 8) Prometheus Proxy (new API)
	r.Any("/prometheus-v2/*path",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.NewPrometheusHandler(store, rpcClient, topo, cfg, tracer, meter, logger),
	)

	// 9) Prometheus Proxy (legacy)
	handlers.HandlePrometheusApis(r, tracer, meter, logger, store, cfg, rpcClient)

	// 10) Workspace execute - forwards shim requests from workspace pods directly to the k8s agent.
	// Auth is via JWT workspace token (X-Workspace-Token), NOT the static secret key.
	// Path mirrors llm-server's /api/v1/workspace/execute so the future cutover only requires
	// swapping the base URL env var (NB_LLM_SERVER_URL → NB_RELAY_SERVER_URL) in the shim.
	r.POST("/api/v1/workspace/execute",
		handlers.NewWorkspaceExecuteHandler(store, rpcClient, cfg, tracer, meter, logger),
	)

	// 11) Generic API Proxy - forwards any HTTP request to agents via RPC
	r.Any("/api/proxy/*path",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.NewAPIProxyHandler(store, rpcClient, topo, cfg, signer, tracer, meter, logger),
	)

	// 12) Proxy agent config push - pushes datasource configs to connected proxy agents
	r.POST("/proxy/config/push",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.NewProxyConfigPushHandler(store, rpcClient, topo, cfg, signer, tracer, meter, logger),
	)

	// 12) Proxy datasource config test - tests a single datasource config against the agent
	r.POST("/proxy/config/test",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.NewProxyConfigTestHandler(store, rpcClient, topo, cfg, signer, tracer, meter, logger),
	)

	// 13) Proxy inventory resync - triggers a connected agent to re-send its datasource inventory
	r.POST("/proxy/resync-inventory",
		middleware.ClientAuthMiddleware(cfg.Security.SecretKey),
		handlers.NewResyncInventoryHandler(store, rpcClient, topo, cfg, signer, tracer, meter, logger),
	)

	return r, nil
}

// Run builds the router and starts the HTTP server.
func Run(cfg *config.Config, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) error {
	router, err := SetupRouter(cfg, tracer, meter, logger)
	if err != nil {
		return err
	}
	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	return router.Run(addr)
}

func traceResponseHeaderMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		prop := propagation.TraceContext{}
		prop.Inject(c.Request.Context(), propagation.HeaderCarrier(c.Writer.Header()))
		c.Next()
	}
}

func OtelMiddlewareWithIgnorePaths(service string, ignorePaths []string) gin.HandlerFunc {
	otelMiddleware := otelgin.Middleware(service)

	return func(c *gin.Context) {
		for _, path := range ignorePaths {
			if c.Request.URL.Path == path {
				c.Next() // Skip Otel middleware
				return
			}
		}
		otelMiddleware(c) // Apply Otel middleware
	}
}
