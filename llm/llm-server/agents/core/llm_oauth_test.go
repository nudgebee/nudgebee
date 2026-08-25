package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startFakeTokenEndpoint returns a token server that validates the exact
// client-credentials contract corporate gateways use and counts issuances.
func startFakeTokenEndpoint(t *testing.T, clientID, clientSecret string, expiresIn int, issued *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
		require.NoError(t, r.ParseForm())
		form := r.PostForm
		// oauth2/clientcredentials may also send creds via basic auth; accept both.
		id, secret := form.Get("client_id"), form.Get("client_secret")
		if id == "" {
			var ok bool
			id, secret, ok = r.BasicAuth()
			require.True(t, ok, "credentials must arrive in the form or basic auth")
			// Basic auth values are URL-encoded per RFC 6749 §2.3.1.
			id, _ = url.QueryUnescape(id)
			secret, _ = url.QueryUnescape(secret)
		}
		if id != clientID || secret != clientSecret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		n := atomic.AddInt32(issued, 1)
		require.Equal(t, "client_credentials", form.Get("grant_type"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("tok-%d", n),
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
		})
	}))
}

func TestLLMAuthTransport_OAuthBearerAndExtraHeaders(t *testing.T) {
	var issued int32
	tokenSrv := startFakeTokenEndpoint(t, "cid", "csecret", 3600, &issued)
	defer tokenSrv.Close()

	var gotAuth, gotAPIKey, gotProject string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		gotProject = r.Header.Get("projectId")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	resetTokenSourcePool(t)
	client := &http.Client{Transport: &llmAuthTransport{
		tokenSource:  oauthTokenSourceFor(llmOAuthConfig{TokenURL: tokenSrv.URL, ClientID: "cid", ClientSecret: "csecret", Scope: "https://api.example.com/.default"}),
		extraHeaders: map[string]string{"projectId": "proj-123"},
	}}

	req, _ := http.NewRequest(http.MethodPost, upstream.URL, nil)
	// Simulate the provider client having set its own static auth: the
	// transport must replace both header styles with the fresh bearer.
	req.Header.Set("api-key", "static-placeholder")
	req.Header.Set("Authorization", "Bearer oauth-managed")
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Equal(t, "Bearer tok-1", gotAuth, "fresh token must replace the placeholder")
	assert.Empty(t, gotAPIKey, "api-key header must be stripped in OAuth mode")
	assert.Equal(t, "proj-123", gotProject)

	// Second call within the token lifetime: no re-issuance.
	resp, err = client.Do(req.Clone(req.Context()))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, int32(1), atomic.LoadInt32(&issued), "unexpired token must be reused")
}

func TestLLMAuthTransport_RefreshesExpiredToken(t *testing.T) {
	var issued int32
	// expires_in below oauth2's 10s early-expiry margin → every call refreshes.
	tokenSrv := startFakeTokenEndpoint(t, "cid", "csecret", 1, &issued)
	defer tokenSrv.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()

	resetTokenSourcePool(t)
	client := &http.Client{Transport: &llmAuthTransport{
		tokenSource: oauthTokenSourceFor(llmOAuthConfig{TokenURL: tokenSrv.URL, ClientID: "cid", ClientSecret: "csecret"}),
	}}
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	assert.GreaterOrEqual(t, atomic.LoadInt32(&issued), int32(2), "an expired token must be re-fetched")
}

func TestLLMAuthTransport_TokenEndpointFailureIsClear(t *testing.T) {
	var issued int32
	tokenSrv := startFakeTokenEndpoint(t, "cid", "right-secret", 3600, &issued)
	defer tokenSrv.Close()

	resetTokenSourcePool(t)
	client := &http.Client{Transport: &llmAuthTransport{
		tokenSource: oauthTokenSourceFor(llmOAuthConfig{TokenURL: tokenSrv.URL, ClientID: "cid", ClientSecret: "wrong-secret"}),
	}}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1", nil) // never reached
	// Token failure surfaces as a synthetic 401 with an OpenAI-shaped error
	// body, NOT a transport error — http.Client wraps transport errors in
	// *url.Error, which the provider clients sanitize into a generic
	// "network error" that loses the step label.
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	assert.Contains(t, string(body), "OAuth token fetch failed", "failure must name the OAuth step, not the LLM call")
	assert.NotContains(t, string(body), "wrong-secret", "the secret must never appear in errors")
}

func TestResolveLLMAuthSettings_Layering(t *testing.T) {
	cfg := map[string]string{
		llmAuthTypeKey:                     llmAuthTypeOAuth,
		llmOAuthTokenURLKey:                "https://issuer.example.com/token",
		llmOAuthClientIDKey:                "global-id",
		llmOAuthClientSecretKey:            "global-secret",
		llmOAuthScopeKey:                   "global-scope",
		llmExtraHeadersKey:                 `{"projectId":"p1"}`,
		"llm_tier_oauth_client_id_summary": "tier-id",
	}

	// Global slot.
	res := &LLMConfigResolution{dbConfig: cfg}
	got := resolveLLMAuthSettings("", res)
	assert.Equal(t, llmAuthTypeOAuth, got.AuthType)
	assert.Equal(t, "global-id", got.OAuth.ClientID)
	assert.Equal(t, map[string]string{"projectId": "p1"}, got.ExtraHeaders)

	// Tier slot: tier key overrides the global one, everything else inherits.
	res = &LLMConfigResolution{dbConfig: cfg, Tier: "summary"}
	got = resolveLLMAuthSettings("", res)
	assert.Equal(t, "tier-id", got.OAuth.ClientID)
	assert.Equal(t, "https://issuer.example.com/token", got.OAuth.TokenURL)

	// No auth keys at all → api_key, no headers: the pre-feature default.
	got = resolveLLMAuthSettings("", &LLMConfigResolution{dbConfig: map[string]string{}})
	assert.Equal(t, llmAuthTypeAPIKey, got.AuthType)
	assert.Nil(t, got.ExtraHeaders)
}

// A tier may override only part of the OAuth quad — one shared issuer, a
// separate client per tier is a normal corporate-gateway shape — so the
// pinned-slot reader must fall back to global PER FIELD. Gating the whole
// quad on one field silently authenticates the tier with the global client.
func TestReadDbSlotInto_PartialTierOAuthOverride(t *testing.T) {
	cfg := map[string]string{
		llmAuthTypeKey:                           llmAuthTypeOAuth,
		llmOAuthTokenURLKey:                      "https://shared-issuer.example.com/token",
		llmOAuthClientIDKey:                      "global-id",
		llmOAuthClientSecretKey:                  "global-secret",
		"llm_tier_provider_reasoning":            "openai",
		"llm_tier_model_reasoning":               "gpt-5",
		"llm_tier_oauth_client_id_reasoning":     "tier-id",
		"llm_tier_oauth_client_secret_reasoning": "tier-secret",
		// Token URL deliberately NOT tier-scoped: inherits the shared issuer.
	}
	res := &LLMConfigResolution{}
	_, err := readDbSlotInto(res, cfg, &parsedConfigSource{Layer: "db", Scope: "tier", Name: "reasoning"})
	require.NoError(t, err)
	assert.Equal(t, "tier-id", res.PinnedOAuthClientID, "tier client id must survive the global fallback")
	assert.Equal(t, "tier-secret", res.PinnedOAuthClientSecret, "tier client secret must survive the global fallback")
	assert.Equal(t, "https://shared-issuer.example.com/token", res.PinnedOAuthTokenURL, "unset tier token URL inherits global")
	assert.Equal(t, llmAuthTypeOAuth, res.PinnedAuthType)
}

func TestResolveLLMAuthSettings_PinnedSourceWins(t *testing.T) {
	res := &LLMConfigResolution{
		PinnedConfigSource:      "db:abc:all",
		PinnedAuthType:          llmAuthTypeOAuth,
		PinnedOAuthTokenURL:     "https://pinned.example.com/token",
		PinnedOAuthClientID:     "pin-id",
		PinnedOAuthClientSecret: "pin-secret",
		PinnedExtraHeaders:      `{"projectId":"pinned"}`,
		// A populated dbConfig must NOT leak through when pinned.
		dbConfig: map[string]string{llmOAuthClientIDKey: "other-id"},
	}
	got := resolveLLMAuthSettings("", res)
	assert.Equal(t, llmAuthTypeOAuth, got.AuthType)
	assert.Equal(t, "pin-id", got.OAuth.ClientID)
	assert.Equal(t, "pinned", got.ExtraHeaders["projectId"])
}

func TestParseExtraHeaders_RejectsAuthKeysAndBadJSON(t *testing.T) {
	assert.Nil(t, parseExtraHeaders(""))
	assert.Nil(t, parseExtraHeaders("not-json"))
	got := parseExtraHeaders(`{"Authorization":"spoof","api-key":"spoof","projectId":"ok"}`)
	assert.Equal(t, map[string]string{"projectId": "ok"}, got, "auth-bearing keys must be dropped")
}

func TestBuildLLMAuthHTTPClient_IncompleteOAuthErrors(t *testing.T) {
	res := &LLMConfigResolution{dbConfig: map[string]string{
		llmAuthTypeKey:      llmAuthTypeOAuth,
		llmOAuthTokenURLKey: "https://issuer.example.com/token",
		// client id/secret missing
	}}
	_, oauthMode, err := buildLLMAuthHTTPClient("", res)
	require.Error(t, err)
	assert.True(t, oauthMode)

	// api_key mode with no headers → nil client, no error: zero behavior change.
	client, oauthMode, err := buildLLMAuthHTTPClient("", &LLMConfigResolution{dbConfig: map[string]string{}})
	require.NoError(t, err)
	assert.Nil(t, client)
	assert.False(t, oauthMode)
}

// The connectivity probe must exercise the same OAuth path as the runtime:
// token fetched from the config's token URL, presented as Bearer, api-key
// stripped, extra headers attached — all against the custom provider client.
func TestProbeOne_CustomProviderWithOAuth(t *testing.T) {
	var issued int32
	tokenSrv := startFakeTokenEndpoint(t, "cid", "csecret", 3600, &issued)
	defer tokenSrv.Close()

	var gotAuth, gotAPIKey, gotProject string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		gotProject = r.Header.Get("projectId")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	resetTokenSourcePool(t)
	cfg := map[string]string{
		cfgKeyProvider:          ProviderCustom,
		cfgKeyModel:             "test-model",
		cfgKeyAPIEndpoint:       upstream.URL + "/v1",
		llmAuthTypeKey:          llmAuthTypeOAuth,
		llmOAuthTokenURLKey:     tokenSrv.URL,
		llmOAuthClientIDKey:     "cid",
		llmOAuthClientSecretKey: "csecret",
		llmExtraHeadersKey:      `{"projectId":"p-1"}`,
	}
	res := probeOne(context.Background(), probeTarget{provider: ProviderCustom, model: "test-model", source: "global", cfg: cfg})
	require.True(t, res.OK, "probe failed: %s", res.Error)
	assert.Equal(t, "Bearer tok-1", gotAuth)
	assert.Empty(t, gotAPIKey, "api-key must not reach the gateway in OAuth mode")
	assert.Equal(t, "p-1", gotProject)
}

// A wrong secret must fail the probe with an error that names the OAuth step
// (so the UI can say "token fetch failed" instead of a generic LLM error) and
// must never echo the secret.
func TestProbeOne_OAuthTokenRejectedIsStepLabelled(t *testing.T) {
	var issued int32
	tokenSrv := startFakeTokenEndpoint(t, "cid", "right-secret", 3600, &issued)
	defer tokenSrv.Close()

	resetTokenSourcePool(t)
	cfg := map[string]string{
		cfgKeyProvider:          ProviderCustom,
		cfgKeyModel:             "test-model",
		cfgKeyAPIEndpoint:       "http://127.0.0.1:1/v1", // never reached
		llmAuthTypeKey:          llmAuthTypeOAuth,
		llmOAuthTokenURLKey:     tokenSrv.URL,
		llmOAuthClientIDKey:     "cid",
		llmOAuthClientSecretKey: "wrong-secret",
	}
	res := probeOne(context.Background(), probeTarget{provider: ProviderCustom, model: "test-model", source: "global", cfg: cfg})
	require.False(t, res.OK)
	assert.Contains(t, res.Error, "OAuth token fetch failed")
	assert.NotContains(t, res.Error, "wrong-secret")
}

// resetTokenSourcePool swaps in a fresh pool and restores the old one after
// the test, so cached sources from other tests can't serve stale tokens here.
func resetTokenSourcePool(t *testing.T) {
	t.Helper()
	old := llmOAuthTokenSources
	llmOAuthTokenSources = &sync.Map{}
	t.Cleanup(func() { llmOAuthTokenSources = old })
}
