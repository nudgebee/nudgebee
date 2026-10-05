package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/routing"
)

func TestClassifyEndpoint(t *testing.T) {
	cases := []struct {
		provider   schemas.ModelProvider
		method     string
		path       string
		family     string
		generative bool
	}{
		{schemas.OpenAI, "POST", "/v1/chat/completions", auth.EndpointChat, true},
		{schemas.OpenAI, "POST", "/v1/completions", auth.EndpointChat, true},
		{schemas.OpenAI, "POST", "/v1/responses", auth.EndpointResponses, true},
		{schemas.OpenAI, "GET", "/v1/responses/resp_1", auth.EndpointResponses, false},
		{schemas.OpenAI, "POST", "/v1/responses/resp_1/cancel", auth.EndpointResponses, false},
		{schemas.OpenAI, "POST", "/v1/conversations", auth.EndpointConversations, false},
		{schemas.OpenAI, "GET", "/v1/conversations/conv_1/items", auth.EndpointConversations, false},
		{schemas.OpenAI, "POST", "/v1/embeddings", auth.EndpointEmbeddings, true},
		{schemas.OpenAI, "GET", "/v1/models", endpointMeta, false},
		{schemas.OpenAI, "GET", "/v1/models/", endpointMeta, false},
		{schemas.OpenAI, "GET", "/v1/models/ft:gpt-4o-mini:acme::abc123", endpointMeta, false},
		// Paths that can reach a model the body never names stay unclassified.
		{schemas.OpenAI, "POST", "/v1/files", "", false},
		{schemas.OpenAI, "POST", "/v1/batches", "", false},
		{schemas.OpenAI, "POST", "/v1/fine_tuning/jobs", "", false},
		{schemas.OpenAI, "POST", "/v1/chat/completions/../../batches", "", false},
		// Reading the catalog is meta; any other method on it is a write with the
		// tenant's credential (DELETE removes a fine-tuned model).
		{schemas.OpenAI, "DELETE", "/v1/models/ft:gpt-4o-mini:acme::abc123", "", false},
		{schemas.OpenAI, "POST", "/v1/models", "", false},
		{schemas.Anthropic, "POST", "/v1/messages", auth.EndpointChat, true},
		{schemas.Anthropic, "POST", "/v1/messages/count_tokens", endpointMeta, false},
		{schemas.Anthropic, "POST", "/v1/messages/batches", "", false},
		{schemas.Anthropic, "GET", "/v1/models", endpointMeta, false},
		{schemas.Anthropic, "GET", "/v1/models/claude-opus-4-8", endpointMeta, false},
		{schemas.Anthropic, "POST", "/v1/models", "", false},
		{schemas.Anthropic, "DELETE", "/v1/models/claude-opus-4-8", "", false},
		{schemas.Gemini, "POST", "/models/gemini-2.5-flash:generateContent", auth.EndpointChat, true},
		{schemas.Gemini, "POST", "/models/gemini-2.5-flash:streamGenerateContent", auth.EndpointChat, true},
		{schemas.Gemini, "POST", "/models/text-embedding-004:embedContent", auth.EndpointEmbeddings, true},
		{schemas.Gemini, "POST", "/models/gemini-2.5-flash:countTokens", endpointMeta, false},
		{schemas.Gemini, "GET", "/models", endpointMeta, false},
		{schemas.Gemini, "GET", "/models/gemini-2.5-flash", endpointMeta, false},
		{schemas.Gemini, "POST", "/models/gemini-2.5-flash:batchGenerateContent", "", false},
		{schemas.Gemini, "POST", "/cachedContents", "", false},
		{schemas.Gemini, "POST", "/files", "", false},
		{schemas.Gemini, "DELETE", "/models/gemini-2.5-flash", "", false},
		{schemas.Gemini, "POST", "/models", "", false},
		// Matched segment by segment: a longer first segment or a deeper path is not the
		// model collection.
		{schemas.Gemini, "GET", "/modelsx", "", false},
		{schemas.Gemini, "GET", "/models/veo-3.0-generate-001/operations/op-1", "", false},
		{schemas.Gemini, "POST", "/models/gemini-2.5-flash/x:generateContent", "", false},
		// The collection and model are matched as sent, as modelFromPath reads the model.
		{schemas.Gemini, "POST", "/Models/gemini-2.5-flash:generateContent", "", false},
		{schemas.Gemini, "POST", "/models/:generateContent", "", false},
		{schemas.Gemini, "POST", "/models/gemini-2.5-flash:GENERATECONTENT", auth.EndpointChat, true},
	}
	for _, tc := range cases {
		family, gen := classifyEndpoint(tc.provider, tc.method, tc.path)
		assert.Equal(t, tc.family, family, "%s %s %s", tc.provider, tc.method, tc.path)
		assert.Equal(t, tc.generative, gen, "%s %s %s", tc.provider, tc.method, tc.path)
	}
}

func newPolicyTestContext(provider schemas.ModelProvider, method, path, model string, policy auth.KeyPolicy) (*RequestContext, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, nil)
	family, gen := classifyEndpoint(provider, method, path)
	return &RequestContext{
		Gin: c, Ctx: context.Background(),
		Identity: auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1", Policy: policy},
		Provider: provider, Model: model, Path: path,
		Endpoint: family, Generative: gen,
	}, rec
}

// runRouteAndPolicy runs the two stages in pipeline order, so the policy sees the
// decision routing actually produced.
func runRouteAndPolicy(t *testing.T, router routing.Resolver, rc *RequestContext) bool {
	t.Helper()
	stop, err := routeStage{router: router}.Handle(rc)
	require.NoError(t, err)
	if stop {
		return true
	}
	stop, err = policyStage{router: router}.Handle(rc)
	require.NoError(t, err)
	return stop
}

var chatOnlyMini = auth.KeyPolicy{Models: []string{"gpt-4o-mini"}, Endpoints: []string{auth.EndpointChat}}

// Ticket #38966, verify steps 1-2: the allowed model on chat succeeds; another model,
// embeddings, responses and conversations are refused.
func TestPolicyStage_ChatOnlySingleModelKey(t *testing.T) {
	router := routing.NewEngine(nil)

	rc, _ := newPolicyTestContext(schemas.OpenAI, "POST", "/v1/chat/completions", "gpt-4o-mini", chatOnlyMini)
	assert.False(t, runRouteAndPolicy(t, router, rc))

	for _, tc := range []struct{ method, path, model, code string }{
		{"POST", "/v1/chat/completions", "gpt-5", "model_not_allowed"},
		{"POST", "/v1/embeddings", "gpt-4o-mini", "endpoint_not_allowed"},
		{"POST", "/v1/responses", "gpt-4o-mini", "endpoint_not_allowed"},
		{"GET", "/v1/conversations/conv_1", "", "endpoint_not_allowed"},
		{"POST", "/v1/batches", "", "endpoint_not_allowed"},
	} {
		rc, rec := newPolicyTestContext(schemas.OpenAI, tc.method, tc.path, tc.model, chatOnlyMini)
		require.True(t, runRouteAndPolicy(t, router, rc), tc.path)
		assert.Equal(t, http.StatusForbidden, rec.Code, tc.path)
		assert.Equal(t, "key_policy", rc.RejectReason, tc.path)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, tc.code, body.Error.Code, tc.path)
	}
}

// Verify step 3: an alias or substitution from an allowed name to a disallowed target is
// refused on the target, not waved through on the requested name.
func TestPolicyStage_ChecksRoutedTargetNotRequestedName(t *testing.T) {
	router := routing.NewEngine([]routing.Rule{
		{ID: "alias", TenantID: "t", Enabled: true, Priority: 1,
			Match:  routing.Match{Provider: "openai", Model: "gpt-4o-mini"},
			Target: routing.Target{Endpoint: routing.Endpoint{Model: "gpt-5"}}},
		{ID: "sub", TenantID: "t", Enabled: true, Priority: 2,
			Match:  routing.Match{Provider: "anthropic", Model: "claude-haiku-4-5"},
			Target: routing.Target{Endpoint: routing.Endpoint{Provider: "gemini", Model: "gemini-2.5-flash"}}},
	})

	rc, rec := newPolicyTestContext(schemas.OpenAI, "POST", "/v1/chat/completions", "gpt-4o-mini", chatOnlyMini)
	require.True(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "openai/gpt-5", rc.PolicyTarget)
	assert.Contains(t, rec.Body.String(), `routes to openai/gpt-5`)

	haikuOnly := auth.KeyPolicy{Models: []string{"claude-haiku-4-5"}}
	rc, rec = newPolicyTestContext(schemas.Anthropic, "POST", "/v1/messages", "claude-haiku-4-5", haikuOnly)
	require.True(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, "gemini/gemini-2.5-flash", rc.PolicyTarget)
	// Anthropic's own envelope, so the Anthropic SDK parses the refusal.
	assert.Contains(t, rec.Body.String(), `"type":"error"`)

	// A provider restriction applies to the substituted provider too.
	anthropicOnly := auth.KeyPolicy{Providers: []string{"anthropic"}}
	rc, _ = newPolicyTestContext(schemas.Anthropic, "POST", "/v1/messages", "claude-haiku-4-5", anthropicOnly)
	require.True(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, "key_policy", rc.RejectReason)
}

// Verify step 3, failover half: a disallowed fallback is pruned before failover can use
// it, while the allowed primary still proceeds.
func TestPolicyStage_PrunesDisallowedFallbacks(t *testing.T) {
	router := routing.NewEngine([]routing.Rule{
		{ID: "fb", TenantID: "t", Enabled: true, Priority: 1,
			Match: routing.Match{Provider: "openai", Model: "gpt-4o-mini"},
			Target: routing.Target{Fallbacks: []routing.Endpoint{
				{Provider: "anthropic", Model: "claude-opus-4-8"},
				{Model: "gpt-4o-mini-2024-07-18"},
				{Provider: "openai", Model: "gpt-4o-mini"},
			}}},
	})
	rc, _ := newPolicyTestContext(schemas.OpenAI, "POST", "/v1/chat/completions", "gpt-4o-mini", chatOnlyMini)
	require.False(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, []routing.Endpoint{{Provider: "openai", Model: "gpt-4o-mini"}}, rc.Decision.Fallbacks)

	// An unrestricted key keeps every fallback.
	rc, _ = newPolicyTestContext(schemas.OpenAI, "POST", "/v1/chat/completions", "gpt-4o-mini", auth.KeyPolicy{})
	require.False(t, runRouteAndPolicy(t, router, rc))
	assert.Len(t, rc.Decision.Fallbacks, 3)
}

// Tenant block rules match the requested model and routing is first-match, so an alias
// to a blocked model used to skip the block. The target is now re-checked, for every key.
func TestPolicyStage_TenantBlockOnRoutedTarget(t *testing.T) {
	router := routing.NewEngine([]routing.Rule{
		{ID: "alias", TenantID: "t", Enabled: true, Priority: 1,
			Match:  routing.Match{Model: "nb-smart"},
			Target: routing.Target{Endpoint: routing.Endpoint{Model: "gpt-5"}}},
		{ID: "block", TenantID: "t", Enabled: true, Priority: 2,
			Match:  routing.Match{Model: "gpt-5"},
			Target: routing.Target{Deny: true}},
		{ID: "fb", TenantID: "t", Enabled: true, Priority: 3,
			Match: routing.Match{Model: "gpt-4o"},
			Target: routing.Target{Fallbacks: []routing.Endpoint{
				{Model: "gpt-5"}, {Model: "gpt-4.1"},
			}}},
	})
	rc, rec := newPolicyTestContext(schemas.OpenAI, "POST", "/v1/chat/completions", "nb-smart", auth.KeyPolicy{})
	require.True(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "blocked", rc.RejectReason)

	rc, _ = newPolicyTestContext(schemas.OpenAI, "POST", "/v1/chat/completions", "gpt-4o", auth.KeyPolicy{})
	require.False(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, []routing.Endpoint{{Model: "gpt-4.1"}}, rc.Decision.Fallbacks)
}

// A per-user exemption above a tenant block must keep working when the exempt user
// reaches the blocked model through an alias: the target is re-resolved with priority.
func TestPolicyStage_TenantBlockRespectsUserExemption(t *testing.T) {
	router := routing.NewEngine([]routing.Rule{
		{ID: "alias", TenantID: "t", Enabled: true, Priority: 1,
			Match:  routing.Match{Model: "nb-smart"},
			Target: routing.Target{Endpoint: routing.Endpoint{Model: "gpt-5"}}},
		{ID: "exempt", TenantID: "t", Enabled: true, Priority: 2,
			Match:  routing.Match{Model: "gpt-5", UserID: "u"},
			Target: routing.Target{}},
		{ID: "block", TenantID: "t", Enabled: true, Priority: 3,
			Match:  routing.Match{Model: "gpt-5"},
			Target: routing.Target{Deny: true}},
	})
	rc, _ := newPolicyTestContext(schemas.OpenAI, "POST", "/v1/chat/completions", "nb-smart", auth.KeyPolicy{})
	assert.False(t, runRouteAndPolicy(t, router, rc))
}

func TestPolicyStage_UnrestrictedKeyPassesEverything(t *testing.T) {
	router := routing.NewEngine(nil)
	for _, path := range []string{"/v1/chat/completions", "/v1/embeddings", "/v1/batches", "/v1/files"} {
		rc, _ := newPolicyTestContext(schemas.OpenAI, "POST", path, "gpt-5", auth.KeyPolicy{})
		assert.False(t, runRouteAndPolicy(t, router, rc), path)
	}
}

func TestPolicyStage_TokenCountersAlwaysAllowed(t *testing.T) {
	router := routing.NewEngine(nil)
	strict := auth.KeyPolicy{Providers: []string{"openai"}, Models: []string{"gpt-4o-mini"}, Endpoints: []string{auth.EndpointEmbeddings}}
	for _, tc := range []struct {
		p            schemas.ModelProvider
		method, path string
	}{
		{schemas.Anthropic, "POST", "/v1/messages/count_tokens"},
		{schemas.Gemini, "POST", "/models/gemini-2.5-flash:countTokens"},
	} {
		rc, _ := newPolicyTestContext(tc.p, tc.method, tc.path, "", strict)
		assert.False(t, runRouteAndPolicy(t, router, rc), tc.path)
	}
}

// Ticket #40087: a provider's model list is unfiltered, so a restricted key is refused there
// and reads the filtered /v1/models instead. An unrestricted key still reads it.
func TestPolicyStage_RestrictedKeyCannotReadProviderModelCatalog(t *testing.T) {
	router := routing.NewEngine(nil)
	for _, tc := range []struct {
		p    schemas.ModelProvider
		path string
	}{
		{schemas.OpenAI, "/v1/models"},
		{schemas.OpenAI, "/v1/models/ft:gpt-4o-mini:acme::abc123"},
		{schemas.Anthropic, "/v1/models"},
		{schemas.Gemini, "/models"},
		{schemas.Gemini, "/models/gemini-2.5-pro"},
	} {
		rc, rec := newPolicyTestContext(tc.p, "GET", tc.path, "", chatOnlyMini)
		require.True(t, runRouteAndPolicy(t, router, rc), tc.path)
		assert.Equal(t, http.StatusForbidden, rec.Code, tc.path)
		assert.Contains(t, rec.Body.String(), "/v1/models", tc.path)

		rc, _ = newPolicyTestContext(tc.p, "GET", tc.path, "", auth.KeyPolicy{})
		assert.False(t, runRouteAndPolicy(t, router, rc), tc.path)
	}
}

// Review finding: the model-list shortcut held for every method, so a restricted key could
// DELETE a fine-tuned model with the tenant's credential.
func TestPolicyStage_DeleteOnModelIsNotMeta(t *testing.T) {
	router := routing.NewEngine(nil)
	anthropicChat := auth.KeyPolicy{Providers: []string{"anthropic"}, Endpoints: []string{auth.EndpointChat}}
	rc, rec := newPolicyTestContext(schemas.OpenAI, "DELETE", "/v1/models/ft:gpt-4o-mini:acme::abc123", "ft:gpt-4o-mini:acme::abc123", anthropicChat)
	require.True(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "endpoint_not_allowed")
}

func TestAllowsModel(t *testing.T) {
	p := auth.KeyPolicy{Models: []string{"gpt-4o-mini", "anthropic/claude-haiku-4-5", "team-fast", "meta-llama/Llama-3.1-8B-Instruct"}}
	assert.True(t, allowsModel("t", p, "openai", "gpt-4o-mini"))
	assert.True(t, allowsModel("t", p, "azure", "gpt-4o-mini"), "a bare entry matches the model on any provider")
	assert.True(t, allowsModel("t", p, "anthropic", "claude-haiku-4-5"))
	assert.False(t, allowsModel("t", p, "bedrock", "claude-haiku-4-5"), "a qualified entry pins the provider")
	assert.True(t, allowsModel("t", p, "vllm", "team-fast"), "a tenant mapping matches by its client-facing name")
	assert.True(t, allowsModel("t", p, "openai", "GPT-4o-Mini"))
	assert.False(t, allowsModel("t", p, "openai", "gpt-4o"))
	assert.True(t, allowsModel("t", p, "huggingface", "meta-llama/Llama-3.1-8B-Instruct"), "a vendor namespace is not a provider")

	assert.True(t, allowsModel("t", auth.KeyPolicy{}, "openai", "gpt-5"), "no model list allows every model")
	assert.False(t, allowsModel("t", auth.KeyPolicy{Models: []string{}}, "openai", "gpt-5"), "an empty list allows nothing")
}

// Review finding: a model id may itself contain "/", so OpenRouter's
// "anthropic/claude-opus-4-8" matched an entry meant to pin Anthropic, reaching the same
// model through another provider and credential.
func TestAllowsModel_QualifiedEntryNeverMatchesAnotherProvidersModelID(t *testing.T) {
	p := auth.KeyPolicy{Models: []string{"anthropic/claude-opus-4-8", "google/gemini-2.5-flash"}}
	assert.True(t, allowsModel("t", p, "anthropic", "claude-opus-4-8"))
	assert.False(t, allowsModel("t", p, "openrouter", "anthropic/claude-opus-4-8"))
	assert.True(t, allowsModel("t", p, "gemini", "gemini-2.5-flash"), "an alias prefix pins its provider")
	assert.False(t, allowsModel("t", p, "openrouter", "google/gemini-2.5-flash"))

	// Reaching that model through OpenRouter takes an entry that names OpenRouter.
	via := auth.KeyPolicy{Models: []string{"openrouter/anthropic/claude-opus-4-8"}}
	assert.True(t, allowsModel("t", via, "openrouter", "anthropic/claude-opus-4-8"))
	assert.False(t, allowsModel("t", via, "anthropic", "claude-opus-4-8"))

	// Through the pipeline: /v1 addresses OpenRouter as "openrouter/anthropic/claude-opus-4-8".
	rc, rec := newPolicyTestContext(schemas.OpenRouter, "POST", chatCompletionsPath, "anthropic/claude-opus-4-8", p)
	rc.Endpoint, rc.Generative, rc.Generic = auth.EndpointChat, true, true
	require.True(t, runRouteAndPolicy(t, routing.NewEngine(nil), rc))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "openrouter/anthropic/claude-opus-4-8", rc.PolicyTarget)
}

// A tenant mapping is reached by its exact client-facing name, which /v1 resolves before any
// provider prefix, so pinning must not break a mapping named like "google/gemma-…-maas".
func TestAllowsModel_TenantMappingNamedLikeProviderModel(t *testing.T) {
	previous := modelCatalogResolverHook
	RegisterModelCatalogResolver(func(string) []ModelCatalogEntry {
		return []ModelCatalogEntry{{ID: "google/gemma-3-27b-it-maas", Provider: schemas.VLLM}}
	})
	t.Cleanup(func() { modelCatalogResolverHook = previous })

	p := auth.KeyPolicy{Models: []string{"google/gemma-3-27b-it-maas"}}
	assert.True(t, allowsModel("t", p, "vllm", "google/gemma-3-27b-it-maas"))
	assert.False(t, allowsModel("t", p, "openrouter", "google/gemma-3-27b-it-maas"), "only the mapping, not the same id elsewhere")
}

// newHandleTestContext builds a native-mount request for h.handle, authenticated with policy.
func newHandleTestContext(method, target, body string, policy auth.KeyPolicy) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Set("nb_identity", auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1", Policy: policy})
	return c, rec
}

// The review's bypasses, sent through the native handler itself: each must be refused at
// the edge. A refusal stops before dispatch, so no provider engine is needed (a request that
// slipped through would reach the nil client and fail the test).
func TestHandle_RefusesPolicyBypasses(t *testing.T) {
	prev := config.Config.MaxRequestBodyBytes
	config.Config.MaxRequestBodyBytes = 1 << 20
	t.Cleanup(func() { config.Config.MaxRequestBodyBytes = prev })
	router := routing.NewEngine(nil)
	h := &handler{sink: &metering.CapturingSink{}, pipeline: NewPipeline(routeStage{router: router}, policyStage{router: router})}
	prevCap := keyRefusals
	keyRefusals = newRefusalCap(100, time.Minute)
	t.Cleanup(func() { keyRefusals = prevCap })

	responsesOnly := auth.KeyPolicy{Endpoints: []string{auth.EndpointResponses}}
	flashOnly := auth.KeyPolicy{Models: []string{"gemini-2.5-flash"}}
	anthropicChat := auth.KeyPolicy{Providers: []string{"anthropic"}, Endpoints: []string{auth.EndpointChat}}

	for _, tc := range []struct {
		name, method, target, body string
		policy                     auth.KeyPolicy
		status                     int
		reason                     string
	}{
		// ".." used to be classified as written (a model list, which skips every check),
		// then resolved to /v1/messages by Bifrost's HTTP client on the way out.
		{"dot-dot into messages", "POST", "/anthropic/v1/models/../messages", `{"model":"claude-opus-4-8"}`, chatOnlyMini, 403, "key_policy"},
		{"dot-dot out of responses", "POST", "/openai/v1/responses/../files", `{}`, responsesOnly, 403, "key_policy"},
		// The client decodes the path once more, so %2e%2e would become .. after the check.
		{"double-encoded dot-dot", "POST", "/anthropic/v1/models/%252e%252e/messages", `{"model":"claude-opus-4-8"}`, chatOnlyMini, 400, "malformed_path"},
		// Gemini serves the path's model; a body "model" used to stand in for it.
		{"gemini body names another model", "POST", "/genai/v1beta/models/gemini-2.5-pro:generateContent", `{"model":"gemini-2.5-flash","contents":[]}`, flashOnly, 400, "model_mismatch"},
		{"gemini path model judged", "POST", "/genai/v1beta/models/gemini-2.5-pro:generateContent", `{"contents":[]}`, flashOnly, 403, "key_policy"},
		{"gemini path without a model", "POST", "/genai/v1beta/models/:generateContent", `{"model":"gemini-2.5-flash","contents":[]}`, flashOnly, 403, "key_policy"},
		{"gemini collection in another case", "POST", "/genai/v1beta/Models/gemini-2.5-pro:generateContent", `{"model":"gemini-2.5-flash","contents":[]}`, flashOnly, 403, "key_policy"},
		// The policy read "gpt-4o-mini" while a provider reads "model".
		{"case-variant model key", "POST", "/openai/v1/chat/completions", `{"model":"gpt-5","MODEL":"gpt-4o-mini"}`, chatOnlyMini, 400, "unreadable_body"},
		// Invalid JSON: Python's json reads past NaN and takes the last "model".
		{"NaN hides a second model", "POST", "/openai/v1/chat/completions", `{"model":"gpt-4o-mini","x":NaN,"model":"gpt-5"}`, chatOnlyMini, 400, "unreadable_body"},
		{"delete a fine-tuned model", "DELETE", "/openai/v1/models/ft:gpt-4o-mini:acme::abc123", ``, anthropicChat, 403, "key_policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &metering.CapturingSink{}
			h.sink = sink
			c, rec := newHandleTestContext(tc.method, tc.target, tc.body, tc.policy)
			h.handle(c)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Len(t, sink.Events(), 1)
			assert.Contains(t, sink.Events()[0].Attributes, `"reject_reason":"`+tc.reason+`"`)
		})
	}
}

// A model-restricted key must name its model: a generative call without one (e.g. an
// OpenAI Response driven by a stored prompt) would run whatever model the prompt names.
func TestPolicyStage_ModelRestrictedKeyMustNameModel(t *testing.T) {
	router := routing.NewEngine(nil)
	p := auth.KeyPolicy{Models: []string{"gpt-4o-mini"}}
	rc, rec := newPolicyTestContext(schemas.OpenAI, "POST", "/v1/responses", "", p)
	require.True(t, runRouteAndPolicy(t, router, rc))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Retrieving an existing response carries no model and creates nothing.
	rc, _ = newPolicyTestContext(schemas.OpenAI, "GET", "/v1/responses/resp_1", "", p)
	assert.False(t, runRouteAndPolicy(t, router, rc))
}

// The /v1 surface speaks OpenAI whatever provider serves the model, so a refusal of an
// Anthropic-served model must still use OpenAI's envelope.
func TestPolicyStage_GenericSurfaceUsesOpenAIShape(t *testing.T) {
	router := routing.NewEngine(nil)
	rc, rec := newPolicyTestContext(schemas.Anthropic, "POST", "/v1/chat/completions", "claude-opus-4-8", chatOnlyMini)
	rc.Endpoint, rc.Generative, rc.Generic = auth.EndpointChat, true, true
	require.True(t, runRouteAndPolicy(t, router, rc))
	var body struct {
		Error struct{ Code, Type string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "model_not_allowed", body.Error.Code)
	assert.NotContains(t, rec.Body.String(), `"type":"error"`)
}

// #40090: a key's refusals past the cap in one window are answered but not recorded, other
// keys keep their own allowance, and a new window starts the count again.
func TestRefusalCap_PerKeyPerWindow(t *testing.T) {
	now := time.Unix(0, 0)
	c := newRefusalCap(2, time.Minute)
	c.now = func() time.Time { return now }

	assert.True(t, c.allow("a"))
	assert.True(t, c.allow("a"))
	assert.False(t, c.allow("a"))
	assert.True(t, c.allow("b"))

	now = now.Add(time.Minute)
	assert.True(t, c.allow("a"))
}

// Refusals over the cap still get their 403 but write no usage row.
func TestHandle_KeyPolicyRefusalsCappedPerKey(t *testing.T) {
	prevCap := keyRefusals
	keyRefusals = newRefusalCap(2, time.Minute)
	t.Cleanup(func() { keyRefusals = prevCap })
	router := routing.NewEngine(nil)
	sink := &metering.CapturingSink{}
	h := &handler{sink: sink, pipeline: NewPipeline(routeStage{router: router}, policyStage{router: router})}

	for range 5 {
		c, rec := newHandleTestContext("POST", "/openai/v1/chat/completions", `{"model":"gpt-5"}`, chatOnlyMini)
		h.handle(c)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	}
	require.Len(t, sink.Events(), 2)
	assert.Contains(t, sink.Events()[0].Attributes, `"reject_reason":"key_policy"`)
}
