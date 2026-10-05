package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/edgeerr"
	"nudgebee/llm-gateway/routing"
)

// endpointMeta marks calls that list or count but never generate: reading the model
// catalog (GET only: a DELETE on a model is a write) and counting tokens. Every key may
// make them, whatever its endpoint restriction.
const endpointMeta = "meta"

// classifyEndpoint maps a provider-native path (mount prefix stripped, cleaned by
// cleanPath) to the endpoint family a key policy restricts. generative is true for the
// calls that create output from a model, which is where a model restriction must see a
// model name. An empty family means the gateway does not model that path or method
// (files, batches, fine-tuning, caches, deleting a model): a restricted key is refused
// there, since those calls can reach a model the request body never names.
func classifyEndpoint(provider schemas.ModelProvider, method, path string) (family string, generative bool) {
	p := strings.TrimSuffix(strings.ToLower(path), "/")
	post, get := method == http.MethodPost, method == http.MethodGet
	switch provider {
	case schemas.OpenAI:
		switch {
		case p == "/v1/chat/completions", p == "/v1/completions":
			return auth.EndpointChat, post
		case p == "/v1/responses":
			return auth.EndpointResponses, post
		case strings.HasPrefix(p, "/v1/responses/"):
			return auth.EndpointResponses, false
		case p == "/v1/conversations", strings.HasPrefix(p, "/v1/conversations/"):
			return auth.EndpointConversations, false
		case p == "/v1/embeddings":
			return auth.EndpointEmbeddings, post
		case get && (p == "/v1/models" || strings.HasPrefix(p, "/v1/models/")):
			return endpointMeta, false
		}
	case schemas.Anthropic:
		switch {
		case p == "/v1/messages", p == "/v1/complete":
			return auth.EndpointChat, post
		case post && p == "/v1/messages/count_tokens":
			return endpointMeta, false
		case get && (p == "/v1/models" || strings.HasPrefix(p, "/v1/models/")):
			return endpointMeta, false
		}
	case schemas.Gemini:
		// Gemini carries the model and method in the path: /models/{model}:{method}. Only
		// that shape and the model list are modelled; a deeper path (a model's operations) is
		// not. The collection and model are matched as sent, the way modelFromPath reads the
		// model the policy judges; only the method is matched in lower case.
		g := strings.TrimSuffix(path, "/")
		if g == "/models" {
			if get {
				return endpointMeta, false
			}
			return "", false
		}
		name, ok := strings.CutPrefix(g, "/models/")
		if !ok || strings.Contains(name, "/") {
			return "", false
		}
		model, verb, hasVerb := strings.Cut(name, ":")
		if model == "" {
			return "", false
		}
		verb = strings.ToLower(verb)
		switch {
		case !hasVerb && get:
			return endpointMeta, false // get one model
		case verb == "generatecontent", verb == "streamgeneratecontent":
			return auth.EndpointChat, true
		case verb == "embedcontent", verb == "batchembedcontents":
			return auth.EndpointEmbeddings, true
		case verb == "counttokens" && post:
			return endpointMeta, false
		}
	}
	return "", false
}

// policyStage enforces the key's policy and the tenant's block rules against every
// target the request can reach: the routed target and each failover fallback. It runs
// after routeStage, which has already applied aliases, tiers, deprecations and
// substitution to rc.Decision, and before ratelimitStage, so a refused request spends
// no quota. Failover reads only rc.Decision.Fallbacks, so pruning them here is what keeps
// a disallowed fallback unreachable.
type policyStage struct{ router routing.Resolver }

func (policyStage) Name() string { return "policy" }

func (s policyStage) Handle(rc *RequestContext) (bool, error) {
	p := rc.Identity.Policy
	// A provider's own model list is unfiltered, so a restricted key reads the filtered /v1/models.
	if p.Restricted() && rc.Endpoint == endpointMeta && rc.Gin.Request.Method == http.MethodGet {
		return s.deny(rc, "endpoint_not_allowed", "", "This API key is restricted; list the models it may use at /v1/models.")
	}
	if p.Restricted() && rc.Endpoint != endpointMeta {
		if rc.Endpoint == "" {
			return s.deny(rc, "endpoint_not_allowed", "", fmt.Sprintf(
				"This API key is restricted and cannot call %s %s. Restricted keys may call chat, responses, embeddings and conversations endpoints only.",
				rc.Gin.Request.Method, rc.Path))
		}
		if !p.AllowsEndpoint(rc.Endpoint) {
			return s.deny(rc, "endpoint_not_allowed", "", fmt.Sprintf(
				"This API key is not allowed to call the %s endpoint.", rc.Endpoint))
		}
	}

	provider, model := routedTarget(rc)
	if p.Restricted() && rc.Endpoint != endpointMeta {
		if !p.AllowsProvider(provider) {
			return s.deny(rc, "provider_not_allowed", provider+"/"+model, fmt.Sprintf(
				"This API key is not allowed to use provider %q.", provider))
		}
		if model == "" && rc.Generative && p.Models != nil {
			return s.deny(rc, "model_not_allowed", provider+"/", "This API key is restricted to specific models, so the request must name a model.")
		}
		if model != "" && !allowsModel(rc.Identity.TenantID, p, provider, model) {
			return s.deny(rc, "model_not_allowed", provider+"/"+model, keyModelMessage(rc, provider, model))
		}
	}
	// The tenant's block rules matched the requested model in routeStage. Routing is
	// first-match, so an alias or substitution to a blocked model never met the block
	// rule; re-resolve the target so a block on it still wins.
	if model != "" && rc.Decision.Reason != routing.ReasonBlocked &&
		(provider != rc.Decision.RequestedProvider || model != rc.Decision.RequestedModel) &&
		s.blocked(rc, provider, model) {
		rc.RejectReason = "blocked"
		rc.PolicyTarget = provider + "/" + model
		edgeerr.Write(rc.Gin, rc.errorShape(), http.StatusForbidden, "model_not_allowed",
			fmt.Sprintf("Model %q is not permitted for your organization.", model))
		return true, nil
	}

	s.pruneFallbacks(rc, p)
	return false, nil
}

// allowsModel reports whether the key may reach model on provider. An entry is read the way
// /v1 reads a model name: one whose first segment names a provider ("anthropic/claude-…",
// "openrouter/anthropic/claude-…") pins that provider, and any other entry is a model name
// matched on every provider. So a model id that itself contains a provider-like prefix
// (OpenRouter's "anthropic/claude-…") never matches an entry pinned to another provider. The
// exception is a tenant mapping, which /v1 resolves by its exact client-facing name before any
// provider prefix: an entry naming the mapping allows it even when the name reads as
// "provider/model" (a Vertex MaaS id such as "google/gemma-…-maas").
func allowsModel(tenantID string, p auth.KeyPolicy, provider, model string) bool {
	if p.Models == nil {
		return true
	}
	namesMapping := false
	for _, entry := range p.Models {
		pinned, name, qualified := explicitProvider(entry)
		switch {
		case !qualified && strings.EqualFold(entry, model):
			return true
		case qualified && strings.EqualFold(string(pinned), provider) && strings.EqualFold(name, model):
			return true
		case qualified && strings.EqualFold(entry, model):
			namesMapping = true
		}
	}
	return namesMapping && isTenantMapping(tenantID, provider, model)
}

// isTenantMapping reports whether model on provider is one of the tenant's client-facing
// model mappings. It reads the catalog: resolving the mapping itself can mint an OAuth token.
func isTenantMapping(tenantID, provider, model string) bool {
	for _, m := range resolveModelCatalog(tenantID) {
		if m.ID == model && string(m.Provider) == provider {
			return true
		}
	}
	return false
}

// routedTarget is the (provider, model) the request will be served by, named as a client
// would name it: a tenant mapping keeps its client-facing name, since the served model
// behind it is an integration detail an allowlist should not have to know.
func routedTarget(rc *RequestContext) (string, string) {
	provider, model := string(rc.Provider), rc.Model
	if rc.Decision.ResolvedProvider != "" {
		provider = rc.Decision.ResolvedProvider
	}
	if rc.Decision.ResolvedModel != "" {
		model = rc.Decision.ResolvedModel
	}
	return provider, model
}

func (s policyStage) blocked(rc *RequestContext, provider, model string) bool {
	if s.router == nil {
		return false
	}
	return s.router.Resolve(routing.Input{
		Provider: provider, Model: model,
		TenantID: rc.Identity.TenantID, UserID: rc.Identity.UserID,
	}).Denied
}

// pruneFallbacks drops every fallback the key or the tenant would refuse. The request
// itself proceeds: the primary is allowed, and failover simply has fewer places to go.
func (s policyStage) pruneFallbacks(rc *RequestContext, p auth.KeyPolicy) {
	fbs := rc.Decision.Fallbacks
	if len(fbs) == 0 {
		return
	}
	kept := make([]routing.Endpoint, 0, len(fbs))
	for _, fb := range fbs {
		target, model := failoverTarget(rc.Decision.RequestedProvider, fb)
		if model == "" {
			continue // unusable for failover anyway
		}
		if p.Restricted() && (!p.AllowsProvider(string(target)) || !allowsModel(rc.Identity.TenantID, p, string(target), model)) {
			slog.Info("policy: fallback dropped by key policy", "api_key_id", rc.Identity.APIKeyID, "fallback", string(target)+"/"+model)
			continue
		}
		if s.blocked(rc, string(target), model) {
			slog.Info("policy: fallback dropped by block rule", "tenant_id", rc.Identity.TenantID, "fallback", string(target)+"/"+model)
			continue
		}
		kept = append(kept, fb)
	}
	rc.Decision.Fallbacks = kept
}

func (s policyStage) deny(rc *RequestContext, code, target, message string) (bool, error) {
	rc.RejectReason = "key_policy"
	rc.PolicyTarget = target
	if !keyRefusals.allow(rc.Identity.APIKeyID) {
		rc.RefusalSuppressed = true
		edgeerr.Write(rc.Gin, rc.errorShape(), http.StatusForbidden, code, message)
		return true, nil
	}
	slog.Info("policy: request denied by key policy",
		"api_key_id", rc.Identity.APIKeyID, "tenant_id", rc.Identity.TenantID, "code", code,
		"endpoint", rc.Endpoint, "requested_model", rc.Decision.RequestedModel, "resolved_target", target)
	edgeerr.Write(rc.Gin, rc.errorShape(), http.StatusForbidden, code, message)
	return true, nil
}

// keyRefusals caps key-policy refusals at 10 rows and log lines per key per minute. The policy
// stage runs before the rate limiter, so a leaked key could otherwise write without bound.
var keyRefusals = newRefusalCap(10, time.Minute)

// refusalCap counts refusals per key in a fixed window shared by all keys, allowing the first
// max of each key; the window that ends logs one line per key that went over.
type refusalCap struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	start  time.Time
	counts map[string]int
}

func newRefusalCap(max int, window time.Duration) *refusalCap {
	return &refusalCap{max: max, window: window, now: time.Now, counts: map[string]int{}}
}

func (c *refusalCap) allow(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now := c.now(); now.Sub(c.start) >= c.window {
		for k, n := range c.counts {
			if n > c.max {
				slog.Warn("policy: key-policy refusals suppressed", "api_key_id", k, "suppressed", n-c.max, "window", c.window.String())
			}
		}
		c.start, c.counts = now, map[string]int{}
	}
	c.counts[key]++
	return c.counts[key] <= c.max
}

// keyModelMessage names the requested model and, when routing changed it, the target
// too, so a caller denied after an alias sees why a model they did not ask for matters.
func keyModelMessage(rc *RequestContext, provider, model string) string {
	if req := rc.Decision.RequestedModel; req != "" && req != model {
		return fmt.Sprintf("This API key is not allowed to use model %q (%q routes to %s/%s).", model, req, provider, model)
	}
	return fmt.Sprintf("This API key is not allowed to use model %q.", model)
}

// CatalogModel is one model name a key policy can allow.
type CatalogModel struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

// ModelCatalog lists the concrete model names a tenant's key policy can name: the
// tenant's own mappings, then the static catalog. Tier aliases are left out because a
// policy judges the model an alias routes to, not the alias.
func ModelCatalog(tenantID string) []CatalogModel {
	tenantModels := resolveModelCatalog(tenantID)
	out := make([]CatalogModel, 0, len(tenantModels)+len(genericModelCatalog))
	seen := map[string]bool{}
	for _, m := range tenantModels {
		out = append(out, CatalogModel{ID: m.ID, Provider: string(m.Provider)})
		seen[m.ID] = true
	}
	for _, m := range genericModelCatalog {
		if m.ownedBy == "nudgebee" || seen[m.id] {
			continue
		}
		out = append(out, CatalogModel{ID: m.id, Provider: m.ownedBy})
	}
	return out
}
