// Command gateway is the NB AI Gateway edge: an everyone-facing service that
// authenticates NB virtual keys, meters usage, optionally enforces rate limits,
// and forwards traffic to providers via a co-located Bifrost HTTP sidecar
// (native passthrough). This file is the process bootstrap; concern-specific
// logic lives in sibling packages (auth, proxy, metering, ratelimit, routing).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/engine"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/proxy"
	"nudgebee/llm-gateway/ratelimit"
	"nudgebee/llm-gateway/routing"
	"nudgebee/llm-gateway/rpc"
	"nudgebee/llm-gateway/security/egressfilter"
	"nudgebee/llm-gateway/settings"
	"nudgebee/llm-gateway/usage"

	"github.com/Cyprinus12138/otelgin"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	sloggin "github.com/samber/slog-gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func logLevel() slog.Level {
	switch strings.ToLower(config.Config.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))

// traceResponseHeaderMiddleware echoes the trace context back to the caller so
// a client can correlate a request with a gateway trace.
func traceResponseHeaderMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		propagation.TraceContext{}.Inject(c.Request.Context(), propagation.HeaderCarrier(c.Writer.Header()))
		c.Next()
	}
}

// providerCreds assembles the operator provider credentials from config: a key
// per API-key provider (Anthropic/OpenAI/Gemini — set any subset to serve them
// all at once) plus the LLM_PROVIDER_* block for a single cloud provider
// (Bedrock/Vertex/Azure) that needs structured creds. engine.New dedupes by
// provider (per-provider keys win) and skips entries with no credential.
func providerCreds() []engine.ProviderCredsConfig {
	var creds []engine.ProviderCredsConfig

	// LLM_PROVIDER_* block — only when it actually carries a credential (avoids a
	// spurious "configured but unusable" warning for the default provider name).
	c := config.Config
	if c.LlmProviderApiKey != "" || c.LlmProviderAccessKey != "" || c.LlmProviderSecretKey != "" {
		creds = append(creds, engine.ProviderCredsConfig{
			Provider:     c.LlmProvider,
			APIKey:       c.LlmProviderApiKey,
			Endpoint:     c.LlmProviderApiEndpoint,
			Region:       c.LlmProviderRegion,
			AccessKey:    c.LlmProviderAccessKey,
			SecretKey:    c.LlmProviderSecretKey,
			SessionToken: c.LlmProviderSessionToken,
		})
	}

	// Per-provider API keys (appended last so they take precedence per provider).
	for _, pc := range []engine.ProviderCredsConfig{
		{Provider: "anthropic", APIKey: c.AnthropicAPIKey},
		{Provider: "openai", APIKey: c.OpenAIAPIKey},
		{Provider: "gemini", APIKey: c.GeminiAPIKey},
	} {
		if pc.APIKey != "" {
			creds = append(creds, pc)
		}
	}
	return creds
}

func main() {
	fmt.Fprintln(os.Stderr, "BOOT: entered main()")
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			logger.Error("main: fatal panic", "panic", r, "stack", string(stack))
			fmt.Fprintf(os.Stderr, "BOOT: fatal panic: %v\n%s\n", r, stack)
			os.Exit(2)
		}
	}()

	slog.SetDefault(logger)

	tp, mp, err := initOtel()
	if err != nil {
		slog.Error("main: otel init failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if sdk, ok := tp.(*sdktrace.TracerProvider); ok {
			if err := sdk.Shutdown(context.Background()); err != nil {
				slog.Error("main: tracer shutdown", "error", err)
			}
		}
		if sdk, ok := mp.(*sdkmetric.MeterProvider); ok {
			if err := sdk.Shutdown(context.Background()); err != nil {
				slog.Error("main: meter shutdown", "error", err)
			}
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	pprof.Register(r)
	r.Use(gin.Recovery())
	r.Use(sloggin.NewWithFilters(logger, sloggin.IgnorePath("/health")))
	r.Use(otelgin.Middleware(config.SERVICE_NAME))
	r.Use(traceResponseHeaderMiddleware())

	tracer := otel.Tracer(config.SERVICE_NAME)
	_ = tracer // reserved for handler wiring (metering spans)

	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Embedded Bifrost core (provider clients + native passthrough) with the NB
	// account and credential-injection plugin. The gateway edge below forwards
	// provider-native requests to it.
	eng, err := engine.New(context.Background(), providerCreds())
	if err != nil {
		slog.Error("main: engine init failed", "error", err)
		os.Exit(1)
	}
	defer eng.Shutdown()

	// Metering sink (async). Degrades to no-op when no sink DB is configured, so
	// the gateway proxies fine without a database.
	sink := metering.NewSink()
	// Body-log sink: off unless gateway_capture_body=true (data custody).
	bodyLog := metering.NewBodyLogSink()
	defer func() {
		if err := sink.Close(); err != nil {
			slog.Error("main: metering sink close failed", "error", err)
		}
		if err := bodyLog.Close(); err != nil {
			slog.Error("main: body-log sink close failed", "error", err)
		}
		common.Close()
	}()

	// Routing: static config-file rules (fail startup if invalid). A dynamic rule
	// loader may additionally be registered; absent, only the static rules apply.
	staticRules, err := routing.LoadFile(config.Config.RoutingConfig)
	if err != nil {
		slog.Error("main: routing config invalid", "error", err)
		os.Exit(1)
	}
	// RuleLoader returns the registered dynamic rule loader, or nil when none is
	// registered (static config-file rules only). NewStore treats a nil loader as
	// "static only". The loader reads the metastore, so it's a no-op when no DB is
	// configured.
	var ruleLoader routing.LoaderFunc
	if config.Config.GatewayDBURL != "" {
		ruleLoader = routing.RuleLoader()
	}
	router := routing.NewStore(staticRules, ruleLoader,
		time.Duration(config.Config.RoutingRefreshSeconds)*time.Second)
	defer func() { _ = router.Close() }()

	// Per-tenant data-governance settings (body-capture opt-in / DLP mode override).
	// EE registers the DB loader; OSS has none, so the store is env-only (no override).
	settingsStore := settings.NewStore(settings.Loader(),
		time.Duration(config.Config.GatewaySettingsRefreshSeconds)*time.Second)
	settings.SetActive(settingsStore)
	defer settingsStore.Close()

	// enforce/redact are EE-only; an OSS build configured for one runs in detect-only.
	// Warn loudly so a "blocking" expectation isn't silently observe-only.
	if m := egressfilter.ParseMode(config.Config.EgressFilterMode); (m == egressfilter.ModeEnforce || m == egressfilter.ModeRedact) && !egressfilter.EnforcementEnabled() {
		slog.Warn("egress filter: enforce/redact require the enterprise build — running in detect-only", "configured_mode", m)
	}

	// Rate-limit enforcement is active only when a limiter is registered. ratelimit.Build
	// returns the registered limiter, or nil when none is registered — a nil *Limiter is
	// allow-all (Enabled() is nil-safe). The pricing catalog + usage plane stay here.
	limiter := ratelimit.Build()
	defer func() { _ = limiter.Close() }() // nil-safe; stops the limit-source refresher
	var pricer *metering.Pricer
	if config.Config.GatewayDBURL != "" {
		pricer = metering.NewPricer(5 * time.Minute)
		defer func() { _ = pricer.Close() }()
	}

	// Fail closed at boot rather than silently serve as an open proxy: mode "none"
	// (no auth) is only allowed when explicitly acknowledged as insecure.
	if err := auth.CheckConfig(); err != nil {
		slog.Error("main: refusing to start", "error", err)
		os.Exit(1)
	}

	// Passthrough edge (/anthropic, /openai, /genai) over the embedded core, with
	// auth resolving identity from the configured mode (user_auths by default). The
	// request credential resolver falls back to the operator/account default when
	// none is registered.
	// Tell the proxy which providers have an operator credential so it can return a
	// clear 403 (rather than a keyless upstream error) when no credential is available.
	proxy.RegisterOperatorCreds(eng.HasProviderCred)
	proxy.RegisterRoutes(r, eng.Client, sink, bodyLog, auth.NewValidator(), router, limiter, pricer)

	// Read-only usage query plane (POST /rpc/usage/aggregate) for the AI Gateway
	// dashboard — app → gateway service-to-service, guarded by the action token.
	// Reads the metering store (cost is now snapshotted per row, so no pricer needed
	// on the read path); skipped when the DB isn't wired. pricer != nil ⇒ DB configured.
	if pricer != nil {
		usage.RegisterRoutes(r, config.Config.GatewayActionToken)
	}

	// Admin config-CRUD plane (POST /rpc/config/…) for the AI Gateway admin UI —
	// app → gateway service-to-service, guarded by the action token. EE-only: the
	// registrar is nil in the OSS build (ee/admin removed), so nothing mounts.
	if reg := rpc.AdminRoutes(); reg != nil {
		reg(r, config.Config.GatewayActionToken)
	}

	srv := &http.Server{
		Addr:    "0.0.0.0:" + config.Config.Port,
		Handler: r,
		// Bound header read only. ReadTimeout/WriteTimeout are intentionally unset:
		// they would break large uploads and long-lived SSE streaming respectively.
		ReadHeaderTimeout: time.Duration(config.Config.ReadHeaderTimeoutSeconds) * time.Second,
	}

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		slog.Info("main: got SIGTERM, shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("main: server shutdown failed", "error", err)
		}
	}()

	fmt.Fprintln(os.Stderr, "BOOT: listener about to start on", srv.Addr)
	slog.Info("main: gateway listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("main: server listen failed", "error", err)
		os.Exit(1)
	}
}
