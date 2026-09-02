package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// OAuth2 client-credentials support for LLM configs (#36556).
//
// Some corporate AI gateways reject static
// API keys: callers must fetch a bearer token from an OAuth2 token endpoint
// and refresh it before expiry. This file adds the ACQUISITION half — the
// presentation half (Authorization: Bearer) is owned by llmAuthTransport,
// which overrides whatever auth header the provider client set, so neither
// client library needed changes.
//
// Resolution mirrors the api-key walk where it matters: pinned source first,
// then DB-tier, then DB-global. There are deliberately no ENV layers in v1 —
// OAuth configs are tenant-supplied through the UI, not operator env.

const (
	llmAuthTypeAPIKey = "api_key"
	llmAuthTypeOAuth  = "oauth_client_credentials"
)

// Global config keys; tier variants append _<tier> per the llm_tier_* convention.
const (
	llmAuthTypeKey          = "llm_auth_type"
	llmOAuthTokenURLKey     = "llm_oauth_token_url"
	llmOAuthClientIDKey     = "llm_oauth_client_id"
	llmOAuthClientSecretKey = "llm_oauth_client_secret"
	llmOAuthScopeKey        = "llm_oauth_scope"
	llmExtraHeadersKey      = "llm_extra_headers"
)

type llmOAuthConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
}

func (c llmOAuthConfig) complete() bool {
	return c.TokenURL != "" && c.ClientID != "" && c.ClientSecret != ""
}

// llmAuthSettings is the resolved authentication shape for one config slot.
type llmAuthSettings struct {
	AuthType     string
	OAuth        llmOAuthConfig
	ExtraHeaders map[string]string
}

// resolveLLMAuthSettings walks pinned → DB-tier → DB-global for the OAuth and
// extra-header keys. It never returns an error: a missing/blank auth type is
// api_key, which is the pre-feature behavior for every existing config.
func resolveLLMAuthSettings(accountId string, res *LLMConfigResolution) llmAuthSettings {
	out := llmAuthSettings{AuthType: llmAuthTypeAPIKey}

	if res != nil && res.PinnedConfigSource != "" {
		if res.PinnedAuthType != "" {
			out.AuthType = res.PinnedAuthType
		}
		out.OAuth = llmOAuthConfig{
			TokenURL:     res.PinnedOAuthTokenURL,
			ClientID:     res.PinnedOAuthClientID,
			ClientSecret: res.PinnedOAuthClientSecret,
			Scope:        res.PinnedOAuthScope,
		}
		out.ExtraHeaders = parseExtraHeaders(res.PinnedExtraHeaders)
		return out
	}

	var dbConfig map[string]string
	var tier ModelTier
	if res != nil {
		dbConfig = res.dbConfig
		tier = res.Tier
	}
	if accountId != "" {
		if cfg, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && cfg != nil {
			dbConfig = cfg
		}
	}
	if dbConfig == nil {
		return out
	}

	read := func(key string) string {
		// Tier value wins over global, matching every other credential key.
		if tier != "" {
			if v := strings.TrimSpace(dbConfig[strings.Replace(key, "llm_", "llm_tier_", 1)+"_"+string(tier)]); v != "" {
				return v
			}
		}
		return strings.TrimSpace(dbConfig[key])
	}

	if v := read(llmAuthTypeKey); v != "" {
		out.AuthType = v
	}
	out.OAuth = llmOAuthConfig{
		TokenURL:     read(llmOAuthTokenURLKey),
		ClientID:     read(llmOAuthClientIDKey),
		ClientSecret: read(llmOAuthClientSecretKey),
		Scope:        read(llmOAuthScopeKey),
	}
	out.ExtraHeaders = parseExtraHeaders(read(llmExtraHeadersKey))
	return out
}

// parseExtraHeaders decodes a JSON object of header name → value. Invalid JSON
// is logged and ignored rather than failing the LLM call: a malformed optional
// nicety must not take down inference.
func parseExtraHeaders(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		slog.Warn("llm-auth: llm_extra_headers is not a JSON object of strings; ignoring", "error", err)
		return nil
	}
	// Never let a header override the auth the transport itself manages.
	for k := range out {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "api-key") {
			slog.Warn("llm-auth: refusing auth-bearing key in llm_extra_headers", "header", k)
			delete(out, k)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// llmOAuthTokenSources pools auto-refreshing token sources keyed by a hash of
// the credentials, so every LLM client sharing a config shares one token and
// its refreshes — the same pattern the AI Gateway uses for Vertex tokens.
// Pointer so tests can swap in a fresh pool without copying the mutex.
var llmOAuthTokenSources = &sync.Map{}

func oauthTokenSourceFor(oc llmOAuthConfig) oauth2.TokenSource {
	sum := sha256.Sum256([]byte(oc.TokenURL + "\x00" + oc.ClientID + "\x00" + oc.ClientSecret + "\x00" + oc.Scope))
	key := hex.EncodeToString(sum[:])
	if cached, ok := llmOAuthTokenSources.Load(key); ok {
		return cached.(oauth2.TokenSource)
	}
	cc := &clientcredentials.Config{
		ClientID:     oc.ClientID,
		ClientSecret: oc.ClientSecret,
		TokenURL:     oc.TokenURL,
	}
	if oc.Scope != "" {
		cc.Scopes = strings.Fields(oc.Scope)
	}
	// Token() is called synchronously inside RoundTrip; without this the
	// oauth2 lib uses http.DefaultClient (no timeout) and a hung token
	// endpoint blocks the LLM request indefinitely.
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	ts := oauth2.TokenSource(&loggingTokenSource{inner: cc.TokenSource(tokenCtx), tokenURL: oc.TokenURL, scope: oc.Scope})
	actual, _ := llmOAuthTokenSources.LoadOrStore(key, ts)
	return actual.(oauth2.TokenSource)
}

// loggingTokenSource logs whenever the wrapped source mints a new token — the
// first fetch and every refresh. The inner source caches until near expiry, so
// per-request Token() calls that reuse a live token stay silent. Never logs
// the token itself.
type loggingTokenSource struct {
	inner    oauth2.TokenSource
	tokenURL string
	scope    string

	mu   sync.Mutex
	last string
}

func (l *loggingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := l.inner.Token()
	if err != nil || tok == nil {
		return tok, err
	}
	l.mu.Lock()
	fresh := tok.AccessToken != l.last
	l.last = tok.AccessToken
	l.mu.Unlock()
	if fresh {
		slog.Info("llm-auth: fetched OAuth token for LLM gateway",
			"token_url", l.tokenURL, "scope", l.scope, "expires_at", tok.Expiry.Format(time.RFC3339))
	}
	return tok, nil
}

// llmAuthTransport stamps authentication and extra headers on every outbound
// LLM request. Living at the transport layer (rather than resolving a token at
// client construction) is what keeps tokens fresh mid-conversation: LLM
// clients are cached, tokens expire, and the transport re-reads the token
// source on each request.
type llmAuthTransport struct {
	base         http.RoundTripper
	tokenSource  oauth2.TokenSource // nil → leave the client's own auth intact
	extraHeaders map[string]string
}

func (t *llmAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Per the RoundTripper contract the request must not be mutated; clone it.
	clone := req.Clone(req.Context())
	if t.tokenSource != nil {
		tok, err := t.tokenSource.Token()
		if err != nil {
			// Surfaced as a synthetic 401 rather than a transport error:
			// http.Client wraps RoundTrip errors in *url.Error, which the
			// provider clients sanitize into a generic "network error" —
			// losing the step label. An OpenAI-shaped error body survives
			// both client libraries' non-200 paths verbatim, so the user
			// sees WHICH step failed (token fetch, not the LLM call).
			slog.Error("llm-auth: OAuth token fetch failed for LLM gateway", "error", err)
			// RoundTripper contract: the body must be closed on every
			// return path, including this early synthetic response.
			if req.Body != nil {
				_ = req.Body.Close()
			}
			msg := fmt.Sprintf("llm-auth: OAuth token fetch failed for LLM gateway: %s", err.Error())
			body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": msg, "type": "authentication_error"}})
			return &http.Response{
				Status:     "401 Unauthorized",
				StatusCode: http.StatusUnauthorized,
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header: http.Header{
					"Content-Type":   []string{"application/json"},
					"Content-Length": []string{strconv.Itoa(len(body))},
				},
				ContentLength: int64(len(body)),
				Body:          io.NopCloser(bytes.NewReader(body)),
				Request:       req,
			}, nil
		}
		// The transport owns auth in OAuth mode: drop whatever the provider
		// client set (api-key or a placeholder bearer) and present the token.
		clone.Header.Del("api-key")
		clone.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	}
	for k, v := range t.extraHeaders {
		clone.Header.Set(k, v)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// buildLLMAuthHTTPClient returns an HTTP client carrying OAuth and/or extra
// headers for the resolved config, or nil when neither applies (caller keeps
// the provider default). The bool reports whether OAuth mode is active, so
// callers can substitute a placeholder for the unused static key.
func buildLLMAuthHTTPClient(accountId string, res *LLMConfigResolution) (*http.Client, bool, error) {
	return resolveLLMAuthSettings(accountId, res).httpClient()
}

// httpClient materializes the settings into an injectable *http.Client, or
// nil when neither OAuth nor extra headers apply. Shared by the runtime
// resolution path (buildLLMAuthHTTPClient) and the request-time connectivity
// probe (probeAuthHTTPClient), so both present auth identically.
func (s llmAuthSettings) httpClient() (*http.Client, bool, error) {
	oauthMode := s.AuthType == llmAuthTypeOAuth
	if !oauthMode && len(s.ExtraHeaders) == 0 {
		return nil, false, nil
	}
	var ts oauth2.TokenSource
	if oauthMode {
		if !s.OAuth.complete() {
			return nil, true, fmt.Errorf("llm-auth: auth type is %s but token URL, client id or client secret is missing", llmAuthTypeOAuth)
		}
		ts = oauthTokenSourceFor(s.OAuth)
	}
	return &http.Client{Transport: &llmAuthTransport{tokenSource: ts, extraHeaders: s.ExtraHeaders}}, oauthMode, nil
}

// probeAuthHTTPClient is the connectivity-probe counterpart of
// buildLLMAuthHTTPClient: it reads the auth keys straight from the flat
// request-time config payload (no DB, tier or pinned resolution — the probe
// caller has already scoped the map to one target).
func probeAuthHTTPClient(cfg map[string]string) (*http.Client, bool, error) {
	settings := llmAuthSettings{AuthType: llmAuthTypeAPIKey}
	if v := strings.TrimSpace(cfg[llmAuthTypeKey]); v != "" {
		settings.AuthType = v
	}
	settings.OAuth = llmOAuthConfig{
		TokenURL:     strings.TrimSpace(cfg[llmOAuthTokenURLKey]),
		ClientID:     strings.TrimSpace(cfg[llmOAuthClientIDKey]),
		ClientSecret: strings.TrimSpace(cfg[llmOAuthClientSecretKey]),
		Scope:        strings.TrimSpace(cfg[llmOAuthScopeKey]),
	}
	settings.ExtraHeaders = parseExtraHeaders(cfg[llmExtraHeadersKey])
	return settings.httpClient()
}
