package api

import (
	"github.com/gin-gonic/gin"

	"nudgebee/runbook/services/security"
)

// RequestDetails resolves a request's security context and account the way
// every account-scoped endpoint does; it has already written the error
// response when ok is false.
type RequestDetails func(c *gin.Context) (ctx *security.RequestContext, accountID string, ok bool)

var routeHooks []func(router *gin.Engine, details RequestDetails)

// RegisterRoutes adds an extension's routes when the server sets up its own.
// Call it from an init().
func RegisterRoutes(hook func(router *gin.Engine, details RequestDetails)) {
	routeHooks = append(routeHooks, hook)
}

// TenantDetails resolves a request's security context for an endpoint that
// acts on the tenant rather than one account (a bee install across accounts);
// it has already written the error response when ok is false. The endpoint
// checks what the caller may do.
type TenantDetails func(c *gin.Context) (ctx *security.RequestContext, ok bool)

var tenantRouteHooks []func(router *gin.Engine, details TenantDetails)

// RegisterTenantRoutes adds an extension's tenant-level routes when the server
// sets up its own. Call it from an init().
func RegisterTenantRoutes(hook func(router *gin.Engine, details TenantDetails)) {
	tenantRouteHooks = append(tenantRouteHooks, hook)
}
