package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

type azureWireCapture struct {
	mu        sync.Mutex
	path      string
	query     string
	apiKey    string
	authz     string
	bodyModel string
}

func newAzureFakeServer(t *testing.T) (*httptest.Server, *azureWireCapture) {
	t.Helper()
	capture := &azureWireCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &payload)
		capture.mu.Lock()
		capture.path = r.URL.Path
		capture.query = r.URL.RawQuery
		capture.apiKey = r.Header.Get("api-key")
		capture.authz = r.Header.Get("Authorization")
		capture.bodyModel = payload.Model
		capture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func callAzure(t *testing.T, p azureOpenAIParams) {
	t.Helper()
	m, err := newAzureOpenAIModel(p)
	require.NoError(t, err)
	_, err = m.GenerateContent(context.Background(), []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")})
	require.NoError(t, err)
}

func TestNewAzureOpenAIModel_Wire(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   func(base string) string
		apiStyle   string
		apiVersion string
		wantPath   string
		wantQuery  string
		wantModel  string
		wantAPIKey string
		wantAuthz  string
	}{
		{
			name:       "v1 ignores an inherited api version",
			endpoint:   func(b string) string { return b },
			apiVersion: "2024-10-21",
			wantPath:   "/openai/v1/chat/completions",
			wantModel:  "gpt-5-mini",
			wantAuthz:  "Bearer secret-key",
		},
		{
			name:      "v1",
			endpoint:  func(b string) string { return b },
			wantPath:  "/openai/v1/chat/completions",
			wantModel: "gpt-5-mini",
			wantAuthz: "Bearer secret-key",
		},
		{
			name:      "v1 endpoint pasted with /openai/v1",
			endpoint:  func(b string) string { return b + "/openai/v1/" },
			wantPath:  "/openai/v1/chat/completions",
			wantModel: "gpt-5-mini",
			wantAuthz: "Bearer secret-key",
		},
		{
			name:       "classic",
			endpoint:   func(b string) string { return b + "/" },
			apiStyle:   "classic",
			apiVersion: "2024-10-21",
			wantPath:   "/openai/deployments/gpt-5-mini/chat/completions",
			wantQuery:  "api-version=2024-10-21",
			wantAPIKey: "secret-key",
		},
		{
			name:       "classic endpoint pasted with /openai",
			endpoint:   func(b string) string { return b + "/openai" },
			apiStyle:   "Classic",
			apiVersion: "2024-10-21",
			wantPath:   "/openai/deployments/gpt-5-mini/chat/completions",
			wantQuery:  "api-version=2024-10-21",
			wantAPIKey: "secret-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, got := newAzureFakeServer(t)
			callAzure(t, azureOpenAIParams{
				Endpoint:   tt.endpoint(srv.URL),
				APIStyle:   tt.apiStyle,
				APIVersion: tt.apiVersion,
				Model:      "gpt-5-mini",
				Token:      "secret-key",
			})
			assert.Equal(t, tt.wantPath, got.path)
			assert.Equal(t, tt.wantQuery, got.query)
			if tt.wantModel != "" {
				assert.Equal(t, tt.wantModel, got.bodyModel)
			}
			assert.Equal(t, tt.wantAPIKey, got.apiKey)
			assert.Equal(t, tt.wantAuthz, got.authz)
		})
	}
}

func TestNewAzureOpenAIModel_EntraToken(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		assert.Equal(t, "https://cognitiveservices.azure.com/.default", r.Form.Get("scope"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"entra-token","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(tokenSrv.Close)

	for _, style := range []string{"v1", "classic"} {
		t.Run(style, func(t *testing.T) {
			srv, got := newAzureFakeServer(t)
			authClient, oauthMode, err := probeAuthHTTPClient(map[string]string{
				llmAuthTypeKey:          llmAuthTypeOAuth,
				llmOAuthTokenURLKey:     tokenSrv.URL,
				llmOAuthClientIDKey:     "client-" + style,
				llmOAuthClientSecretKey: "secret",
				llmOAuthScopeKey:        "https://cognitiveservices.azure.com/.default",
			})
			require.NoError(t, err)
			require.True(t, oauthMode)
			callAzure(t, azureOpenAIParams{
				Endpoint:   srv.URL,
				APIStyle:   style,
				APIVersion: "2024-10-21",
				Model:      "gpt-5-mini",
				OAuth:      oauthMode,
				AuthClient: authClient,
			})
			assert.Equal(t, "Bearer entra-token", got.authz)
			assert.Empty(t, got.apiKey, "the placeholder key must never reach Azure")
		})
	}
}

func TestNewAzureOpenAIModel_ConfigErrors(t *testing.T) {
	_, err := newAzureOpenAIModel(azureOpenAIParams{Model: "gpt-5-mini", Token: "k"})
	assert.ErrorContains(t, err, "llm_provider_api_endpoint")

	_, err = newAzureOpenAIModel(azureOpenAIParams{Endpoint: "https://x.openai.azure.com", Model: "gpt-5-mini"})
	assert.ErrorContains(t, err, "llm_provider_api_key")

	_, err = newAzureOpenAIModel(azureOpenAIParams{Endpoint: "https://x.openai.azure.com", APIStyle: "classic", Model: "gpt-5-mini", Token: "k"})
	assert.ErrorContains(t, err, "llm_provider_api_version is required")

	_, err = newAzureOpenAIModel(azureOpenAIParams{Endpoint: "https://x.openai.azure.com", APIStyle: "v2", Model: "gpt-5-mini", Token: "k"})
	assert.ErrorContains(t, err, "must be v1 or classic")
}

func TestNormalizeAzureEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		" https://x.openai.azure.com/ ":         "https://x.openai.azure.com",
		"https://x.openai.azure.com/openai":     "https://x.openai.azure.com",
		"https://x.openai.azure.com/openai/v1/": "https://x.openai.azure.com",
		"https://x.services.ai.azure.com":       "https://x.services.ai.azure.com",
	} {
		assert.Equal(t, want, normalizeAzureEndpoint(in), in)
	}
}

func TestAzureAPIStylePerSlot(t *testing.T) {
	cfg := map[string]string{
		"llm_provider":                     "azure",
		"llm_model_name":                   "gpt-5-mini",
		llmApiStyleKey:                     "v1",
		"llm_tier_provider_reasoning":      "azure",
		"llm_tier_model_reasoning":         "gpt-4o",
		"llm_tier_api_style_reasoning":     "classic",
		"llm_provider_aws_debug":           "azure",
		"llm_model_name_aws_debug":         "gpt-4o",
		"llm_provider_api_style_aws_debug": "classic",
		"llm_fallback_provider":            "azure",
		"llm_fallback_model":               "gpt-4o",
		"llm_fallback_api_style":           "classic",
		"llm_tier_provider_summary":        "googleai",
		"llm_tier_api_style_summary":       "classic",
	}

	t.Run("pinned slots", func(t *testing.T) {
		for _, p := range []parsedConfigSource{
			{Layer: "db", Scope: "global"},
			{Layer: "db", Scope: "tier", Name: "reasoning"},
			{Layer: "db", Scope: "agent", Name: "aws_debug"},
			{Layer: "db", Scope: "fallback"},
		} {
			res := &LLMConfigResolution{}
			_, err := readDbSlotInto(res, cfg, &p)
			require.NoError(t, err)
			want := "classic"
			if p.Scope == "global" {
				want = "v1"
			}
			assert.Equal(t, want, res.PinnedApiStyle, p.Scope)
		}
	})

	t.Run("pinned tier inherits the global style only on the same provider", func(t *testing.T) {
		cfg := map[string]string{
			"llm_provider": "azure", "llm_model_name": "gpt-5-mini", llmApiStyleKey: "classic",
			"llm_tier_provider_retrieval": "azure", "llm_tier_model_retrieval": "gpt-4o",
			"llm_tier_provider_summary": "googleai", "llm_tier_model_summary": "gemini-3.7-flash",
		}
		for tier, want := range map[string]string{"retrieval": "classic", "summary": ""} {
			res := &LLMConfigResolution{}
			_, err := readDbSlotInto(res, cfg, &parsedConfigSource{Layer: "db", Scope: "tier", Name: tier})
			require.NoError(t, err)
			assert.Equal(t, want, res.PinnedApiStyle, tier)
		}
	})

	t.Run("layered resolution", func(t *testing.T) {
		resolve := func(provider, agent string, tier ModelTier) string {
			return resolveLLMApiStyle("", provider, agent, agent != "", &LLMConfigResolution{dbConfig: cfg, Tier: tier})
		}
		assert.Equal(t, "v1", resolve("azure", "", ""))
		assert.Equal(t, "classic", resolve("azure", "", ModelTierReasoning))
		assert.Equal(t, "classic", resolve("azure", "aws_debug", ""))
		assert.Equal(t, "v1", resolve("azure", "", ModelTierSummary), "a tier on another provider must not lend its style")
	})
}
