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
