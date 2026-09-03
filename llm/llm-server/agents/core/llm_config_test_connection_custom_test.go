package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/llms/openai"
)

// Regression: buildLLMFromConfig's provider switch is separate from the runtime
// resolver's (llm_config.go), and "custom" was added to the latter only. Every
// custom config therefore failed Test Connection with `unknown llm_provider
// "custom"` before reaching the network — which blocked saving one through the
// UI, since the form gates on the probe passing.
func TestBuildLLMFromConfig_Custom(t *testing.T) {
	t.Run("builds against the supplied endpoint", func(t *testing.T) {
		llm, err := buildLLMFromConfig(ProviderCustom, "google/gemma-4-31b-it:free", map[string]string{
			cfgKeyProvider:    ProviderCustom,
			cfgKeyAPIKey:      "sk-fake-key",
			cfgKeyAPIEndpoint: "https://openrouter.ai/api/v1",
		})
		require.NoError(t, err)
		assert.NotNil(t, llm)
	})

	// Without the endpoint the OpenAI client would silently default to
	// api.openai.com, so a user holding a valid OpenAI key would see the probe
	// pass and the runtime — which requires the endpoint — fail afterwards.
	t.Run("rejects a missing endpoint rather than defaulting to OpenAI", func(t *testing.T) {
		for _, endpoint := range []string{"", "   "} {
			_, err := buildLLMFromConfig(ProviderCustom, "google/gemma-4-31b-it:free", map[string]string{
				cfgKeyProvider:    ProviderCustom,
				cfgKeyAPIKey:      "sk-fake-key",
				cfgKeyAPIEndpoint: endpoint,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "requires llm_provider_api_endpoint")
		}
	})
}

// The transport must rewrite only the body's model field, leaving the URL
// (which carries the deployment) untouched.
func TestDeploymentBodyModelTransport_RewritesBodyModelOnly(t *testing.T) {
	var gotPath, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(b, &payload))
		gotModel = payload.Model
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := wrapDeploymentBodyModel(nil, "gpt-5.6-terra")
	resp, err := client.Post(srv.URL+"/openai/deployments/gpt-5.6-terra_2026-07-09/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-5.6-terra_2026-07-09","messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Contains(t, gotPath, "deployments/gpt-5.6-terra_2026-07-09", "URL deployment untouched")
	assert.Equal(t, "gpt-5.6-terra", gotModel, "body model rewritten to the configured model name")
}

// Probe parity for the Azure-shaped custom gateway (the UHG sample contract):
// the probe URL must carry the deployment segment and api-version, and the
// body must carry the MODEL name, not the deployment.
func TestProbeOne_CustomAzureShapeWithDeployment(t *testing.T) {
	var gotPath, gotAPIVersion, gotBodyModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIVersion = r.URL.Query().Get("api-version")
		b, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(b, &payload)
		gotBodyModel = payload.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	cfg := map[string]string{
		cfgKeyProvider:       ProviderCustom,
		cfgKeyAPIEndpoint:    srv.URL,
		cfgKeyAPIKey:         "static-key",
		cfgKeyAPIType:        "azure_ad",
		cfgKeyAPIVersion:     "2025-01-01-preview",
		llmDeploymentNameKey: "gpt-5.6-terra_2026-07-09",
	}
	res := probeOne(context.Background(), probeTarget{provider: ProviderCustom, model: "gpt-5.6-terra", source: "global", cfg: cfg})

	require.True(t, res.OK, "probe must pass: %s", res.Error)
	assert.Contains(t, gotPath, "/openai/deployments/gpt-5.6-terra_2026-07-09/chat/completions", "Azure URL shape with deployment")
	assert.Equal(t, "2025-01-01-preview", gotAPIVersion, "configured api-version on the wire")
	assert.Equal(t, "gpt-5.6-terra", gotBodyModel, "body carries the model name, not the deployment")
}

// Plain custom shape stays byte-identical: no api-type → no deployment logic,
// no api-version, plain /chat/completions.
func TestProbeOne_CustomPlainShapeUnchanged(t *testing.T) {
	var gotPath, gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	cfg := map[string]string{
		cfgKeyProvider:    ProviderCustom,
		cfgKeyAPIEndpoint: srv.URL + "/v1",
		cfgKeyAPIKey:      "static-key",
	}
	res := probeOne(context.Background(), probeTarget{provider: ProviderCustom, model: "some-model", source: "global", cfg: cfg})

	require.True(t, res.OK, "probe must pass: %s", res.Error)
	assert.Equal(t, "/v1/chat/completions", gotPath)
	assert.Empty(t, gotRawQuery, "no api-version on the plain shape")
}

// resolveLLMDeploymentName: pinned wins, then tier, then global.
func TestResolveLLMDeploymentName_Layering(t *testing.T) {
	assert.Equal(t, "pinned-dep", resolveLLMDeploymentName("", &LLMConfigResolution{
		PinnedConfigSource: "db:x:global", PinnedDeploymentName: "pinned-dep",
	}))
	res := &LLMConfigResolution{Tier: ModelTierReasoning}
	res.dbConfig = map[string]string{
		"llm_tier_deployment_name_reasoning": "tier-dep",
		llmDeploymentNameKey:                 "global-dep",
	}
	assert.Equal(t, "tier-dep", resolveLLMDeploymentName("", res))
	res2 := &LLMConfigResolution{}
	res2.dbConfig = map[string]string{llmDeploymentNameKey: "global-dep"}
	assert.Equal(t, "global-dep", resolveLLMDeploymentName("", res2))
}

// Sanity: the deployment-wrapped client still works through the real OpenAI
// client end-to-end (URL from client model, body from rewrite).
func TestOpenAIClientWithDeploymentWrap_EndToEnd(t *testing.T) {
	var gotPath, gotBodyModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(b, &payload)
		gotBodyModel = payload.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	llm, err := openai.New(
		openai.WithToken("t"), openai.WithBaseURL(srv.URL),
		openai.WithAPIType(openai.APITypeAzureAD), openai.WithAPIVersion("2025-01-01-preview"),
		openai.WithModel("dep_2026-07-09"),
		openai.WithHTTPClient(newOpenAIHTTPClient(wrapDeploymentBodyModel(nil, "real-model"))),
	)
	require.NoError(t, err)
	_, err = llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hi"),
	})
	require.NoError(t, err)
	assert.Contains(t, gotPath, "/openai/deployments/dep_2026-07-09/chat/completions")
	assert.Equal(t, "real-model", gotBodyModel)
}
