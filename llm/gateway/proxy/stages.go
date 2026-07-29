package proxy

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/ratelimit"
	"nudgebee/llm-gateway/routing"
)

// routeStage resolves the requested (provider, model) to a target via the routing
// engine and, when a rule resolves a different model, rewrites ONLY the model
// field/segment (faithful — not a schema translation). It runs first so downstream
// stages (limits, creds) see the resolved target.
type routeStage struct{ router routing.Resolver }

func (routeStage) Name() string { return "route" }

func (s routeStage) Handle(rc *RequestContext) (bool, error) {
	rc.Decision = s.router.Resolve(routing.Input{
		Provider: string(rc.Provider), Model: rc.Model,
		TenantID: rc.Identity.TenantID, UserID: rc.Identity.UserID,
	})
	if rc.Decision.ResolvedModel != rc.Decision.RequestedModel {
		rc.Body, rc.Path = rewriteModel(rc.Provider, rc.Body, rc.Path, rc.Decision.ResolvedModel)
		rc.Model = rc.Decision.ResolvedModel
	}
	return false, nil
}

// ratelimitStage rejects (429) requests over a configured quota, keyed by identity.
// It reserves the request pre-call; token/cost usage is reconciled post-response by
// the metering step. A no-op for unlimited callers.
type ratelimitStage struct{ limiter *ratelimit.Limiter }

func (ratelimitStage) Name() string { return "ratelimit" }

func (s ratelimitStage) Handle(rc *RequestContext) (bool, error) {
	if !s.limiter.Enabled() {
		return false, nil
	}
	ex := s.limiter.Check(rc.Ctx, ratelimit.Scope{TenantID: rc.Identity.TenantID, UserID: rc.Identity.UserID})
	if ex == nil {
		return false, nil
	}
	rc.Gin.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error": gin.H{
			"type": "rate_limit_exceeded",
			"message": fmt.Sprintf("%s limit exceeded for %s (%s window)",
				ex.Metric, ex.Scope, ex.Period),
		},
	})
	return true, nil
}

// CredResolver resolves the provider credential for a specific request. ok=false
// means "no request-specific credential — fall back to the operator/account default"
// (nbAccount.GetKeysForProvider). An alternate resolver may be registered.
type CredResolver interface {
	Resolve(ctx context.Context, provider schemas.ModelProvider, id auth.Identity) (schemas.Key, bool)
}

// noopCredResolver always falls back to the account/operator default. It is the
// default until an alternate resolver is registered.
type noopCredResolver struct{}

func (noopCredResolver) Resolve(context.Context, schemas.ModelProvider, auth.Identity) (schemas.Key, bool) {
	return schemas.Key{}, false
}

// credResolverHook is an optionally-registered credential resolver. When none is
// registered it is nil and credResolver falls back to noopCredResolver (operator
// default).
var credResolverHook CredResolver

// RegisterCredResolver registers an alternate credential resolver. When none is
// registered, credResolver returns the no-op (operator/account default) resolver.
func RegisterCredResolver(cr CredResolver) { credResolverHook = cr }

// credResolver returns the registered resolver, or the no-op default (falls back to
// the operator/account credential).
func credResolver() CredResolver {
	if credResolverHook == nil {
		return noopCredResolver{}
	}
	return credResolverHook
}

// resolverStage injects the tenant's provider credential for THIS request. When one
// is found it is set on the Bifrost context under BifrostContextKeyDirectKey, which
// core honors directly (bypassing the operator key pool); otherwise the request
// falls through to nbAccount's operator default.
type resolverStage struct{ creds CredResolver }

func (resolverStage) Name() string { return "resolver" }

func (s resolverStage) Handle(rc *RequestContext) (bool, error) {
	if key, ok := s.creds.Resolve(rc.Ctx, rc.Provider, rc.Identity); ok {
		rc.Bctx.SetValue(schemas.BifrostContextKeyDirectKey, key)
	}
	return false, nil
}

// filterStage is an outbound-body filter seam; no-op by default.
type filterStage struct{}

func (filterStage) Name() string                         { return "filter" }
func (filterStage) Handle(*RequestContext) (bool, error) { return false, nil }
