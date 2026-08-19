// mock-oauth-gateway is a standalone test double for corporate AI gateways
// that front an OpenAI-compatible LLM behind OAuth2 client-credentials auth
// (the corporate-gateway shape from #36556). It exists so the LLM-config OAuth flow
// can be exercised end-to-end without access to a client's gateway:
//
//	go run ./loadtest/mock-oauth-gateway
//
// Endpoints:
//
//	POST /oauth2/token
//	    RFC 6749 client-credentials grant. Accepts credentials form-encoded
//	    or via basic auth. Issues opaque bearer tokens with a short TTL
//	    (default 60s) so mid-conversation refresh is provable in minutes.
//
//	POST /openai/deployments/{deployment}/chat/completions   (Azure shape)
//	POST /v1/chat/completions                                 (custom shape)
//	    Both enforce what a strict gateway enforces:
//	      - Authorization: Bearer <token THIS gateway issued, unexpired>
//	      - the configured extra header (default projectId) must be present
//	      - any api-key header is rejected outright
//	    With MOCK_GW_UPSTREAM_URL set, the request is proxied to that
//	    OpenAI-compatible base URL using MOCK_GW_UPSTREAM_API_KEY (the Azure
//	    shape's deployment name is dropped in favor of the body's model).
//	    Without an upstream, a canned completion is returned so the flow is
//	    testable fully offline.
//
// Configuration (env):
//
//	MOCK_GW_PORT              listen port            (default 9990)
//	MOCK_GW_CLIENT_ID         accepted client id     (default mock-client-id)
//	MOCK_GW_CLIENT_SECRET     accepted client secret (default mock-client-secret)
//	MOCK_GW_TOKEN_TTL_SECONDS token lifetime         (default 60)
//	MOCK_GW_REQUIRED_HEADER   mandatory extra header (default projectId)
//	MOCK_GW_UPSTREAM_URL      OpenAI-compatible base URL incl. version segment
//	                          (e.g. https://openrouter.ai/api/v1); empty = canned
//	MOCK_GW_UPSTREAM_API_KEY  key sent upstream as Authorization: Bearer
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tokenStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time // token → expiry
}

func (s *tokenStore) issue(ttl time.Duration) string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	tok := "mock-" + hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Opportunistic sweep so a long-running gateway doesn't grow unbounded.
	now := time.Now()
	for t, exp := range s.tokens {
		if now.After(exp) {
			delete(s.tokens, t)
		}
	}
	s.tokens[tok] = now.Add(ttl)
	return tok
}

func (s *tokenStore) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[tok]
	return ok && time.Now().Before(exp)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// oauthError writes an RFC 6749 §5.2 error for the token endpoint.
func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// gwError writes an OpenAI-shaped error so provider clients surface the
// message verbatim — the same reason llmAuthTransport uses this shape.
func gwError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg, "type": "invalid_request_error"}})
}

// upstreamClient bounds proxied LLM calls; 5m mirrors the llm-server's own
// provider-client timeout.
var upstreamClient = &http.Client{Timeout: 5 * time.Minute}

func main() {
	port := envOr("MOCK_GW_PORT", "9990")
	clientID := envOr("MOCK_GW_CLIENT_ID", "mock-client-id")
	clientSecret := envOr("MOCK_GW_CLIENT_SECRET", "mock-client-secret")
	requiredHeader := envOr("MOCK_GW_REQUIRED_HEADER", "projectId")
	upstreamURL := strings.TrimRight(os.Getenv("MOCK_GW_UPSTREAM_URL"), "/")
	upstreamKey := os.Getenv("MOCK_GW_UPSTREAM_API_KEY")
	ttlSeconds, err := strconv.Atoi(envOr("MOCK_GW_TOKEN_TTL_SECONDS", "60"))
	if err != nil || ttlSeconds <= 0 {
		log.Fatalf("MOCK_GW_TOKEN_TTL_SECONDS must be a positive integer")
	}
	ttl := time.Duration(ttlSeconds) * time.Second

	store := &tokenStore{tokens: map[string]time.Time{}}

	mux := http.NewServeMux()

	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			oauthError(w, http.StatusMethodNotAllowed, "invalid_request", "token requests must be POST")
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			oauthError(w, http.StatusBadRequest, "invalid_request", "Content-Type must be application/x-www-form-urlencoded")
			return
		}
		if err := r.ParseForm(); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
			return
		}
		if gt := r.PostForm.Get("grant_type"); gt != "client_credentials" {
			oauthError(w, http.StatusBadRequest, "unsupported_grant_type", fmt.Sprintf("grant_type %q is not supported", gt))
			return
		}
		// Credentials arrive in the form (the common style) or via basic auth
		// (golang.org/x/oauth2 may probe both styles). RFC 6749 §2.3.1: basic
		// auth values are URL-encoded.
		id, secret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		if id == "" {
			bid, bsecret, ok := r.BasicAuth()
			if !ok {
				oauthError(w, http.StatusUnauthorized, "invalid_client", "client credentials missing")
				return
			}
			id, _ = url.QueryUnescape(bid)
			secret, _ = url.QueryUnescape(bsecret)
		}
		idOK := subtle.ConstantTimeCompare([]byte(id), []byte(clientID)) == 1
		secretOK := subtle.ConstantTimeCompare([]byte(secret), []byte(clientSecret)) == 1
		if !idOK || !secretOK {
			oauthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
			return
		}
		tok := store.issue(ttl)
		log.Printf("token issued (ttl %s) scope=%q", ttl, r.PostForm.Get("scope"))
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": tok,
			"token_type":   "Bearer",
			"expires_in":   ttlSeconds,
		})
	})

	completions := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			gwError(w, http.StatusMethodNotAllowed, "chat completions must be POST")
			return
		}
		// A strict gateway refuses static keys — that's the whole point.
		if r.Header.Get("api-key") != "" {
			gwError(w, http.StatusUnauthorized, "api-key auth is not accepted by this gateway; use an OAuth2 bearer token")
			return
		}
		auth := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || tok == "" {
			gwError(w, http.StatusUnauthorized, "missing Authorization: Bearer token")
			return
		}
		if !store.valid(tok) {
			gwError(w, http.StatusUnauthorized, "bearer token is unknown or expired; fetch a fresh one from /oauth2/token")
			return
		}
		if r.Header.Get(requiredHeader) == "" {
			gwError(w, http.StatusBadRequest, fmt.Sprintf("required header %q is missing", requiredHeader))
			return
		}

		if upstreamURL == "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"id":      "chatcmpl-mock",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "mock-model",
				"choices": []map[string]any{{
					"index":         0,
					"message":       map[string]string{"role": "assistant", "content": "pong (canned response from mock-oauth-gateway; set MOCK_GW_UPSTREAM_URL to proxy a real model)"},
					"finish_reason": "stop",
				}},
				"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			gwError(w, http.StatusBadGateway, "failed to read request body")
			return
		}
		up, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			gwError(w, http.StatusBadGateway, "failed to build upstream request")
			return
		}
		up.Header.Set("Content-Type", "application/json")
		up.Header.Set("Authorization", "Bearer "+upstreamKey)
		resp, err := upstreamClient.Do(up)
		if err != nil {
			gwError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}

	// Custom-provider shape: base URL <gateway>/v1.
	mux.HandleFunc("/v1/chat/completions", completions)
	// Azure shape: <gateway>/openai/deployments/{deployment}/chat/completions.
	// The deployment segment is accepted and ignored — the body's model field
	// drives the upstream, mirroring gateways that route on deployment name.
	mux.HandleFunc("/openai/deployments/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			gwError(w, http.StatusNotFound, "only chat/completions is implemented")
			return
		}
		completions(w, r)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mode := "canned responses"
	if upstreamURL != "" {
		mode = "proxying to " + upstreamURL
	}
	log.Printf("mock-oauth-gateway listening on :%s (%s, token ttl %s, required header %q)", port, mode, ttl, requiredHeader)
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
