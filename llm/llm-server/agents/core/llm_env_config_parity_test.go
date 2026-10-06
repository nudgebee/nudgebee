package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every credential and endpoint key walks ENV then DB, with DB on top. Two
// resolvers did not: getLLMDisableThinking and resolveLLMAuthSettings read the
// tenant integration only, so a deployment configured entirely by environment
// -- on-prem, test, a local run -- had no way to set them at all. These tests
// pin the walk in both directions: ENV supplies the value when no integration
// does, and an integration still wins when it has one.

func TestDisableThinking_EnvSuppliesItWhenNoIntegrationDoes(t *testing.T) {
	prev := config.Config.LlmDisableThinking
	t.Cleanup(func() { config.Config.LlmDisableThinking = prev })

	config.Config.LlmDisableThinking = false
	assert.False(t, getLLMDisableThinking("", nil), "unset ENV must behave exactly as before")

	config.Config.LlmDisableThinking = true
	assert.True(t, getLLMDisableThinking("", nil), "ENV alone must be able to disable thinking")
}

func TestDisableThinking_IntegrationBeatsEnv(t *testing.T) {
	prev := config.Config.LlmDisableThinking
	t.Cleanup(func() { config.Config.LlmDisableThinking = prev })
	config.Config.LlmDisableThinking = true

	// A tenant that explicitly wants thinking on overrides the operator default.
	res := &LLMConfigResolution{dbConfig: map[string]string{"llm_disable_thinking": "false"}}
	assert.False(t, getLLMDisableThinking("acct", res), "an explicit DB value must beat ENV")

	// A blank DB value is not an opinion; ENV stands.
	res = &LLMConfigResolution{dbConfig: map[string]string{"llm_disable_thinking": ""}}
	assert.True(t, getLLMDisableThinking("acct", res), "a blank DB value must not erase ENV")
}

func TestAuthSettings_EnvSuppliesOAuthWhenNoIntegrationDoes(t *testing.T) {
	prev := struct{ t, u, i, s, sc, h string }{
		config.Config.LlmAuthType, config.Config.LlmOAuthTokenURL, config.Config.LlmOAuthClientID,
		config.Config.LlmOAuthClientSecret, config.Config.LlmOAuthScope, config.Config.LlmExtraHeaders,
	}
	t.Cleanup(func() {
		config.Config.LlmAuthType, config.Config.LlmOAuthTokenURL, config.Config.LlmOAuthClientID = prev.t, prev.u, prev.i
		config.Config.LlmOAuthClientSecret, config.Config.LlmOAuthScope, config.Config.LlmExtraHeaders = prev.s, prev.sc, prev.h
	})

	// Default must be untouched: no ENV, no integration -> api_key, as before.
	config.Config.LlmAuthType, config.Config.LlmOAuthTokenURL = "", ""
	config.Config.LlmOAuthClientID, config.Config.LlmOAuthClientSecret = "", ""
	config.Config.LlmOAuthScope, config.Config.LlmExtraHeaders = "", ""
	assert.Equal(t, llmAuthTypeAPIKey, resolveLLMAuthSettings("", nil).AuthType)

	// Deliberately padded: a Helm value or a .env line routinely carries a
	// trailing newline, and the DB resolver trims every field, so the ENV side
	// has to as well or the same credential works from one source and 401s
	// from the other.
	config.Config.LlmAuthType = "  " + llmAuthTypeOAuth + "  "
	config.Config.LlmOAuthTokenURL = "  https://issuer.example/token\n"
	config.Config.LlmOAuthClientID = " cid "
	config.Config.LlmOAuthClientSecret = " csecret\n"
	config.Config.LlmOAuthScope = " a b "
	config.Config.LlmExtraHeaders = `{"X-Route":"eu"}`

	got := resolveLLMAuthSettings("", nil)
	assert.Equal(t, llmAuthTypeOAuth, got.AuthType)
	assert.Equal(t, "https://issuer.example/token", got.OAuth.TokenURL)
	assert.Equal(t, "cid", got.OAuth.ClientID)
	assert.Equal(t, "csecret", got.OAuth.ClientSecret)
	assert.Equal(t, "a b", got.OAuth.Scope)
	assert.Equal(t, map[string]string{"X-Route": "eu"}, got.ExtraHeaders)
	assert.True(t, got.OAuth.complete(), "an ENV-only OAuth config must be usable")
}

func TestAuthSettings_IntegrationBeatsEnvPerField(t *testing.T) {
	prevID, prevURL := config.Config.LlmOAuthClientID, config.Config.LlmOAuthTokenURL
	t.Cleanup(func() { config.Config.LlmOAuthClientID, config.Config.LlmOAuthTokenURL = prevID, prevURL })
	config.Config.LlmOAuthClientID = "env-client"
	config.Config.LlmOAuthTokenURL = "https://env.example/token"

	// The integration names only a client id. Field-wise precedence means it
	// overrides that one field without blanking the ENV token URL -- otherwise
	// a partial integration would silently break an otherwise working config.
	res := &LLMConfigResolution{dbConfig: map[string]string{llmOAuthClientIDKey: "db-client"}}
	got := resolveLLMAuthSettings("", res)
	assert.Equal(t, "db-client", got.OAuth.ClientID, "DB wins where it has a value")
	assert.Equal(t, "https://env.example/token", got.OAuth.TokenURL, "ENV stands where DB is silent")
}

func TestDeploymentName_EnvSuppliesItWhenNoIntegrationDoes(t *testing.T) {
	prev := config.Config.LlmProviderDeploymentName
	t.Cleanup(func() { config.Config.LlmProviderDeploymentName = prev })

	config.Config.LlmProviderDeploymentName = ""
	assert.Equal(t, "", resolveLLMDeploymentName("", nil), "unset ENV behaves as before")

	config.Config.LlmProviderDeploymentName = "my-azure-deployment"
	assert.Equal(t, "my-azure-deployment", resolveLLMDeploymentName("", nil))

	res := &LLMConfigResolution{dbConfig: map[string]string{llmDeploymentNameKey: "db-deployment"}}
	assert.Equal(t, "db-deployment", resolveLLMDeploymentName("", res), "DB wins")
}

// TestDisableThinking_ReachesTheWire is the one that matters: the resolver can
// be right and the flag still never leave the process. A real HTTP server
// records what the real client actually sends.
func TestDisableThinking_ReachesTheWire(t *testing.T) {
	prevThink := config.Config.LlmDisableThinking
	prevEndpoint := config.Config.LlmProviderApiEndpoint
	prevKey := config.Config.LlmProviderApiKey
	prevProvider := config.Config.LlmProvider
	t.Cleanup(func() {
		config.Config.LlmDisableThinking = prevThink
		config.Config.LlmProviderApiEndpoint = prevEndpoint
		config.Config.LlmProviderApiKey = prevKey
		config.Config.LlmProvider = prevProvider
	})

	for _, tc := range []struct {
		name     string
		envValue bool
		wantKey  bool
	}{
		{"ENV off — payload unchanged from today", false, false},
		{"ENV on — the switch reaches the request", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Server and captured body belong to the subtest, so neither case
			// can observe the other's request.
			var body []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			}))
			t.Cleanup(srv.Close)

			// The ENV credential layers only fire when the global provider
			// matches the one being built (resolveLLMSecret, L1), so the slot
			// has to be coherent.
			config.Config.LlmProvider = ProviderCustom
			config.Config.LlmProviderApiEndpoint = srv.URL + "/v1"
			config.Config.LlmProviderApiKey = "test-key"
			config.Config.LlmDisableThinking = tc.envValue

			llm, err := getOpenAILLM(ProviderCustom, "some-model", "agent", false, "")
			require.NoError(t, err)
			// The upstream response is irrelevant; the request body is the subject.
			_, _ = llm.GenerateContent(t.Context(), nil)
			require.NotEmpty(t, body, "no request reached the server")

			var sent map[string]any
			require.NoError(t, json.Unmarshal(body, &sent))
			kwargs, present := sent["chat_template_kwargs"]

			if !tc.wantKey {
				assert.False(t, present, "nothing extra must be sent when the switch is off; got %v", kwargs)
				return
			}
			require.True(t, present, "chat_template_kwargs missing; body was %s", strings.TrimSpace(string(body)))
			assert.Equal(t, map[string]any{"enable_thinking": false}, kwargs)
		})
	}
}

func TestDeploymentName_TierStillBeatsGlobal(t *testing.T) {
	// The layers were reordered when this resolver moved to ENV-then-DB
	// accumulate form: tier used to be checked first and return early, so it
	// now has to come last to keep winning.
	prev := config.Config.LlmProviderDeploymentName
	t.Cleanup(func() { config.Config.LlmProviderDeploymentName = prev })
	config.Config.LlmProviderDeploymentName = "env-deployment"

	res := &LLMConfigResolution{
		Tier: ModelTierSummary,
		dbConfig: map[string]string{
			llmDeploymentNameKey: "db-global",
			"llm_tier_deployment_name_" + string(ModelTierSummary): "db-tier",
		},
	}
	assert.Equal(t, "db-tier", resolveLLMDeploymentName("", res), "tier must beat global and ENV")

	res.dbConfig = map[string]string{llmDeploymentNameKey: "db-global"}
	assert.Equal(t, "db-global", resolveLLMDeploymentName("", res), "global beats ENV when no tier value")

	res.dbConfig = nil
	assert.Equal(t, "env-deployment", resolveLLMDeploymentName("", res), "ENV stands when DB says nothing")
}

func TestFallbackRef_IntegrationBeatsEnv(t *testing.T) {
	// The fourth resolver had no precedence test; the other three did.
	prevSrc, prevModel := config.Config.LlmFallbackRefSource, config.Config.LlmFallbackRefModel
	t.Cleanup(func() {
		config.Config.LlmFallbackRefSource, config.Config.LlmFallbackRefModel = prevSrc, prevModel
	})
	config.Config.LlmFallbackRefSource = "env:global"
	config.Config.LlmFallbackRefModel = "env-model"

	// Resolution fails (no such slot configured here) but the error names the
	// source that was chosen, which is what this asserts.
	_, err := configuredFallback(nil, "", &LLMConfigResolution{
		dbConfig: map[string]string{llmFallbackRefSourceKey: "db:11111111-1111-1111-1111-111111111111"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db:11111111", "DB reference must win over the ENV one")
	assert.NotContains(t, err.Error(), "env:global")
}
