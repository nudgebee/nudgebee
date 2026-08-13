package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/edgeerr"
	"nudgebee/llm-gateway/ratelimit"
	"nudgebee/llm-gateway/routing"
	"nudgebee/llm-gateway/security/egressfilter"
	"nudgebee/llm-gateway/settings"
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
	// A block rule matched: reject with a clear 403 instead of forwarding.
	if rc.Decision.Denied {
		msg := fmt.Sprintf("Model %q is not permitted for your organization.", rc.Decision.RequestedModel)
		if alt := rc.Decision.ResolvedModel; alt != "" {
			msg = fmt.Sprintf("%s Use %q instead.", msg, alt)
		}
		rc.RejectReason = "blocked"
		edgeerr.Write(rc.Gin, string(rc.Provider), http.StatusForbidden, "model_not_allowed", msg)
		return true, nil
	}
	// Cross-provider substitution: the translate path (in the handler) parses the
	// original client body and builds a fresh request for the resolved provider, so
	// the body/path must be left untouched here — rewriting the model in an
	// Anthropic body to a Gemini id would corrupt it before the parser runs.
	if rc.Decision.Reason == routing.ReasonSubstitute {
		return false, nil
	}
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
	reset := ratelimit.ResetAt(ex.Period, time.Now())
	if secs := int(time.Until(reset).Seconds()) + 1; secs > 0 {
		rc.Gin.Header("Retry-After", strconv.Itoa(secs))
	}
	rc.RejectReason = "rate_limited"
	edgeerr.Write(rc.Gin, string(rc.Provider), http.StatusTooManyRequests, "rate_limit_exceeded",
		fmt.Sprintf("%s limit exceeded for %s (%s window); resets at %s UTC",
			ex.Metric, ex.Scope, ex.Period, reset.Format("2006-01-02 15:04")))
	return true, nil
}

// CredResolver resolves the provider credential for a specific request. ok=false
// means "no request-specific credential — fall back to the operator/account default"
// (nbAccount.GetKeysForProvider). An alternate resolver may be registered.
type CredResolver interface {
	Resolve(ctx context.Context, provider schemas.ModelProvider, id auth.Identity) (schemas.Key, bool)
}

// operatorCredsHook, when registered, reports whether an operator/account credential
// is configured for a provider. The resolver stage uses it to fail fast with a clear
// 403 when neither a per-request (tenant) key nor an operator key exists — instead of
// letting core call the provider with no credential (a confusing upstream error). Left
// nil until registered, in which case the stage can't determine coverage and does not
// block (preserves prior behavior).
var operatorCredsHook func(schemas.ModelProvider) bool

// RegisterOperatorCreds registers the operator-credential availability predicate.
func RegisterOperatorCreds(fn func(schemas.ModelProvider) bool) { operatorCredsHook = fn }

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
		return false, nil
	}
	// No per-request (tenant) credential — the request falls back to the operator/
	// account default. If we can determine that no operator credential exists either,
	// fail fast with a clear 403 rather than letting core call the provider keyless
	// (which surfaces to the client as a confusing upstream auth error).
	if operatorCredsHook != nil && !operatorCredsHook(rc.Provider) {
		rc.RejectReason = "no_credentials"
		edgeerr.Write(rc.Gin, string(rc.Provider), http.StatusForbidden, "provider_not_configured",
			fmt.Sprintf("No %s credential is configured for your organization. Ask an administrator to add a %s key in NudgeBee integrations.",
				rc.Provider, rc.Provider))
		return true, nil
	}
	return false, nil
}

// filterStage scans the outbound request body for secrets before it leaves to the
// provider (egress DLP). Behavior is set by gateway_egress_filter_mode: off (no scan),
// detect (record only), enforce (block), redact (replace the secret + forward). A hit
// is signalled via x-nb-llm-dlp and recorded in the usage row either way.
type filterStage struct{}

func (filterStage) Name() string { return "filter" }

func (s filterStage) Handle(rc *RequestContext) (bool, error) {
	// Per-tenant DLP mode overrides the env default (EE); OSS resolves to the env value.
	// EffectiveMode caps enforce/redact to detect on the OSS build (detect-only).
	mode := egressfilter.EffectiveMode(egressfilter.ParseMode(settings.EgressMode(rc.Identity.TenantID, config.Config.EgressFilterMode)))
	if mode == egressfilter.ModeOff || len(rc.Body) == 0 {
		return false, nil
	}
	res := egressfilter.Scan(string(rc.Body))
	if !res.HasHits() {
		return false, nil
	}
	rules := res.RuleIDs()
	rc.Gin.Header("x-nb-llm-dlp", string(mode)+":"+strings.Join(rules, ","))
	rc.DLP = map[string]any{"mode": string(mode), "rules": rules}

	switch mode {
	case egressfilter.ModeEnforce:
		rc.RejectReason = "secret_blocked"
		edgeerr.Write(rc.Gin, string(rc.Provider), http.StatusForbidden, "secret_detected",
			"request blocked: it contains a credential/secret ("+strings.Join(rules, ", ")+"). Remove it or ask an administrator about the egress policy.")
		return true, nil
	case egressfilter.ModeRedact:
		rc.Body = []byte(egressfilter.Redact(string(rc.Body), res.Hits))
		rc.DLP["redacted"] = true
	}
	// detect (and redact) fall through — the request proceeds.
	return false, nil
}
