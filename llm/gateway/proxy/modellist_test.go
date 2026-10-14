package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/engine"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/routing"
)

const (
	openAIModelList    = `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model","owned_by":"system"},{"id":"ft:gpt-4o-mini:acme::abc123","object":"model","owned_by":"acme"},{"id":"gpt-5","object":"model","owned_by":"system"}]}`
	anthropicModelList = `{"data":[{"id":"claude-opus-4-8","type":"model","display_name":"Claude Opus 4.8"},{"id":"claude-haiku-4-5","type":"model","display_name":"Claude Haiku 4.5"}],"has_more":true,"first_id":"claude-opus-4-8","last_id":"claude-haiku-4-5"}`
	geminiModelList    = `{"models":[{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro"},{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"}],"nextPageToken":"tok-2"}`
)

// listIDs reads the model ids a provider list body holds, whatever its shape.
func listIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var v struct {
		Data   []struct{ ID string }
		Models []struct{ Name string }
	}
	require.NoError(t, json.Unmarshal(body, &v), string(body))
	ids := []string{}
	for _, m := range v.Data {
		ids = append(ids, m.ID)
	}
	for _, m := range v.Models {
		ids = append(ids, m.Name)
	}
	return ids
}

// #40087: a provider's model list is cut to the models the caller may use, keeping the
// provider's shape and paging fields so its SDK still reads and pages it.
func TestFilterModelList_KeepsEachProvidersShape(t *testing.T) {
	allowed := map[string]bool{"gpt-4o-mini": true, "claude-haiku-4-5": true, "gemini-2.5-flash": true}
	only := func(id string) bool { return allowed[id] }

	out, err := filterModelList(schemas.OpenAI, []byte(openAIModelList), only)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-4o-mini"}, listIDs(t, out))
	assert.JSONEq(t, `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model","owned_by":"system"}]}`, string(out))

	out, err = filterModelList(schemas.Anthropic, []byte(anthropicModelList), only)
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":[{"id":"claude-haiku-4-5","type":"model","display_name":"Claude Haiku 4.5"}],"has_more":true,"first_id":"claude-haiku-4-5","last_id":"claude-haiku-4-5"}`, string(out))

	// Gemini names a model "models/{id}"; the policy reads the bare id, as on its paths.
	out, err = filterModelList(schemas.Gemini, []byte(geminiModelList), only)
	require.NoError(t, err)
	assert.JSONEq(t, `{"models":[{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"}],"nextPageToken":"tok-2"}`, string(out))

	// Nothing allowed is an empty list, never a null one.
	out, err = filterModelList(schemas.OpenAI, []byte(openAIModelList), func(string) bool { return false })
	require.NoError(t, err)
	assert.Contains(t, string(out), `"data":[]`)
}

// Fail closed: a body the gateway cannot read as a list is never forwarded unfiltered.
func TestFilterModelList_UnreadableBodyFailsClosed(t *testing.T) {
	yes := func(string) bool { return true }
	for _, tc := range []struct {
		p    schemas.ModelProvider
		body string
	}{
		{schemas.OpenAI, `not json`},
		{schemas.OpenAI, `{"object":"list"}`},
		{schemas.Anthropic, `{"data":{"id":"claude-opus-4-8"}}`},
		{schemas.Gemini, `{"models":{}}`},
		{schemas.Gemini, `null`},
		{schemas.Gemini, `[]`},
	} {
		_, err := filterModelList(tc.p, []byte(tc.body), yes)
		assert.Error(t, err, tc.body)
	}
}

// Review finding: Anthropic's first_id/last_id are model ids, so passing them as sent named
// up to two models the key may not use. They now name kept models, or go when none is kept.
func TestFilterModelList_AnthropicPageBoundsNameOnlyKeptModels(t *testing.T) {
	body := `{"data":[{"id":"claude-opus-4-8"},{"id":"claude-haiku-4-5"},{"id":"claude-sonnet-4-6"},{"id":"claude-private-x"}],"has_more":true,"first_id":"claude-opus-4-8","last_id":"claude-private-x"}`
	keep := map[string]bool{"claude-haiku-4-5": true, "claude-sonnet-4-6": true}

	out, err := filterModelList(schemas.Anthropic, []byte(body), func(id string) bool { return keep[id] })
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":[{"id":"claude-haiku-4-5"},{"id":"claude-sonnet-4-6"}],"has_more":true,"first_id":"claude-haiku-4-5","last_id":"claude-sonnet-4-6"}`, string(out))

	out, err = filterModelList(schemas.Anthropic, []byte(body), func(string) bool { return false })
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":[],"has_more":true}`, string(out))
	assert.NotContains(t, string(out), "claude-")
}

// Review finding: proto3 JSON omits an empty repeated field, so a Gemini page with no models
// arrives without "models" and was answered 502.
func TestFilterModelList_GeminiPageWithoutModelsIsEmpty(t *testing.T) {
	yes := func(string) bool { return true }
	out, err := filterModelList(schemas.Gemini, []byte(`{}`), yes)
	require.NoError(t, err)
	assert.JSONEq(t, `{"models":[]}`, string(out))

	out, err = filterModelList(schemas.Gemini, []byte(`{"nextPageToken":"tok-3"}`), yes)
	require.NoError(t, err)
	assert.JSONEq(t, `{"models":[],"nextPageToken":"tok-3"}`, string(out))

	_, err = filterModelList(schemas.OpenAI, []byte(`{}`), yes)
	assert.Error(t, err, "only Gemini may omit its list")
}

// End to end on each native mount: a restricted key gets only its models, in the provider's
// shape; an unrestricted key gets the provider's body unchanged.
func TestHandle_RestrictedKeyGetsFilteredProviderModelList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevBody, prevPrivate := config.Config.MaxRequestBodyBytes, config.Config.AllowPrivateEndpoints
	config.Config.MaxRequestBodyBytes, config.Config.AllowPrivateEndpoints = 1<<20, true
	t.Cleanup(func() {
		config.Config.MaxRequestBodyBytes, config.Config.AllowPrivateEndpoints = prevBody, prevPrivate
	})

	for _, tc := range []struct {
		provider, path, upstreamBody string
		policy                       auth.KeyPolicy
		want                         []string
	}{
		{"openai", "/openai/v1/models", openAIModelList, auth.KeyPolicy{Models: []string{"gpt-4o-mini"}}, []string{"gpt-4o-mini"}},
		{"anthropic", "/anthropic/v1/models", anthropicModelList, auth.KeyPolicy{Models: []string{"anthropic/claude-haiku-4-5"}}, []string{"claude-haiku-4-5"}},
		{"gemini", "/genai/v1beta/models", geminiModelList, auth.KeyPolicy{Models: []string{"google/gemini-2.5-flash"}}, []string{"models/gemini-2.5-flash"}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.upstreamBody))
			}))
			t.Cleanup(upstream.Close)
			eng, err := engine.New(context.Background(), []engine.ProviderCredsConfig{{Provider: tc.provider, APIKey: "k", Endpoint: upstream.URL}})
			require.NoError(t, err)
			t.Cleanup(eng.Shutdown)
			router := routing.NewEngine(nil)
			h := &handler{client: eng.Client, sink: &metering.CapturingSink{}, router: router,
				pipeline: NewPipeline(routeStage{router: router}, policyStage{router: router}, resolverStage{creds: noopCredResolver{}})}

			getWith := func(policy auth.KeyPolicy, body string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("GET", tc.path, strings.NewReader(body))
				c.Set("nb_identity", auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1", Policy: policy})
				h.handle(c)
				return rec
			}
			get := func(policy auth.KeyPolicy) *httptest.ResponseRecorder { return getWith(policy, "") }

			rec := get(tc.policy)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tc.want, listIDs(t, rec.Body.Bytes()))
			assert.Equal(t, rec.Body.Len(), int(rec.Result().ContentLength), "Content-Length matches the filtered body")

			// A stream flag in the GET body must not route around the filter.
			rec = getWith(tc.policy, `{"stream":true}`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tc.want, listIDs(t, rec.Body.Bytes()), "a stream body is still filtered")

			rec = get(auth.KeyPolicy{})
			assert.JSONEq(t, tc.upstreamBody, rec.Body.String(), "an unrestricted key gets the provider's list unchanged")
		})
	}
}

func TestHandle_UserRuleFiltersProviderModelList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevBody, prevPrivate := config.Config.MaxRequestBodyBytes, config.Config.AllowPrivateEndpoints
	config.Config.MaxRequestBodyBytes, config.Config.AllowPrivateEndpoints = 1<<20, true
	t.Cleanup(func() {
		config.Config.MaxRequestBodyBytes, config.Config.AllowPrivateEndpoints = prevBody, prevPrivate
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openAIModelList))
	}))
	t.Cleanup(upstream.Close)
	eng, err := engine.New(context.Background(), []engine.ProviderCredsConfig{{Provider: "openai", APIKey: "k", Endpoint: upstream.URL}})
	require.NoError(t, err)
	t.Cleanup(eng.Shutdown)
	router := routing.NewEngine(nil)
	h := &handler{client: eng.Client, sink: &metering.CapturingSink{}, router: router,
		pipeline: NewPipeline(routeStage{router: router}, policyStage{router: router}, resolverStage{creds: noopCredResolver{}})}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/openai/v1/models", nil)
	c.Set("nb_identity", auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1",
		UserPolicy: auth.KeyPolicy{Models: []string{"gpt-5"}}})
	h.handle(c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"gpt-5"}, listIDs(t, rec.Body.Bytes()))
	assert.False(t, strings.Contains(rec.Body.String(), "ft:gpt-4o-mini"), "a private fine-tune is not listed")
}
