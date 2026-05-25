package api

import (
	"log/slog"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// RouteRegistrar registers a group of routes on the gin engine. EE packages
// register their route handlers (marketplace, entitlement, billing migration,
// etc.) here from package init(), so the OSS api.ConfigureRoutes loop picks
// them up without having to import EE packages directly. When EE files are
// absent, no handlers register and the routes simply don't exist.
type RouteRegistrar func(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger)

var eeRouteRegistrars []RouteRegistrar

// RegisterEERoute registers an EE route group to be wired up during
// ConfigureRoutes. Call from package init() in ee/api/*.
func RegisterEERoute(r RouteRegistrar) {
	eeRouteRegistrars = append(eeRouteRegistrars, r)
}

// StartupHook is a long-running goroutine started during server bootstrap.
// Used for EE background jobs (e.g. marketplace SQS pollers).
type StartupHook func()

var eeStartupHooks []StartupHook

// RegisterEEStartupHook registers a long-running EE goroutine to be started
// from cmd/main.go after server init. Call from package init().
func RegisterEEStartupHook(h StartupHook) {
	eeStartupHooks = append(eeStartupHooks, h)
}

// RunEEStartupHooks fires every registered startup hook in its own goroutine.
// Called once from cmd/main.go.
func RunEEStartupHooks() {
	for _, h := range eeStartupHooks {
		go h()
	}
}

// BootstrapHook runs synchronously during server start, after DB init and
// before routes serve. Use for one-shot EE work that must complete before
// requests arrive (e.g. seeding the feature_flag table from license JWT
// claims). Errors are logged; they don't halt startup so the server still
// comes up in OSS-equivalent mode.
type BootstrapHook func() error

var eeBootstrapHooks []BootstrapHook

// RegisterEEBootstrap registers a one-shot EE bootstrap step. Call from
// package init() in ee/*.
func RegisterEEBootstrap(h BootstrapHook) {
	eeBootstrapHooks = append(eeBootstrapHooks, h)
}

// RunEEBootstrapHooks invokes every registered bootstrap hook in order.
// Called once from cmd/main.go before serving requests.
func RunEEBootstrapHooks(logger *slog.Logger) {
	for _, h := range eeBootstrapHooks {
		if err := h(); err != nil {
			logger.Error("EE bootstrap hook failed", "error", err)
		}
	}
}

// ActionHandler handles a single Hasura action by name. EE handlers
// (billing_list, billing_usage_cost_list, billing_infographics, etc.)
// register via RegisterAction and are dispatched from the default
// case of the tenant action switch in actions_tenant.go.
type ActionHandler func(payload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger)

var actionRegistry = map[string]ActionHandler{}

// RegisterAction registers a handler for a specific Hasura action name.
// Call from package init() in ee/api/*.
func RegisterAction(name string, h ActionHandler) {
	actionRegistry[name] = h
}

// BuildContextFromPayload is the exported wrapper around the internal
// helper, so EE handlers in nudgebee/services/ee/api can build the same
// RequestContext as native action cases without exposing internals.
func BuildContextFromPayload(c *gin.Context, h *ActionRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	return buildContextFromPayload(c, h, tracer, meter, logger)
}

// dispatchAction looks up an EE-registered handler for the action;
// returns true if a handler was found and invoked.
func dispatchAction(payload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) bool {
	if h, ok := actionRegistry[payload.Action.Name]; ok {
		h(payload, c, tracer, meter, logger)
		return true
	}
	return false
}
