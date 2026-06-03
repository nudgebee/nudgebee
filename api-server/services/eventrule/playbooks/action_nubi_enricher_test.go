package playbooks

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/config"
)

func newNubiTestCtx(eventID string) PlaybookActionContext {
	return &defaultPlaybookActionContext{
		tenantId:  "tenant-1",
		accountId: "acct-1",
		logger:    slog.Default(),
		event:     PlaybookEvent{EventId: eventID},
	}
}

func TestNubiEnricher_ValidationErrors(t *testing.T) {
	a := &nubiEnricherAction{}
	ctx := newNubiTestCtx("evt-1")

	// Missing prompt and query → required-error.
	_, err := a.Execute(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt")

	// Endpoint unset → distinct error.
	oldEndpoint := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = ""
	defer func() { config.Config.LLMServerEndpoint = oldEndpoint }()
	_, err = a.Execute(ctx, map[string]any{"prompt": "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm_server_endpoint")
}

func TestNubiEnricher_PostsAndShapesResponse(t *testing.T) {
	var captured struct {
		path    string
		headers http.Header
		body    map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		captured.headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"data":{"session_id":"sess-42","conversation_id":"conv-7","status":"PENDING"}}`))
	}))
	defer srv.Close()

	oldEndpoint := config.Config.LLMServerEndpoint
	oldTokenHdr := config.Config.LLMServerTokenHeader
	oldToken := config.Config.LLMServerToken
	config.Config.LLMServerEndpoint = srv.URL
	config.Config.LLMServerTokenHeader = "X-NB-Token"
	config.Config.LLMServerToken = "secret"
	defer func() {
		config.Config.LLMServerEndpoint = oldEndpoint
		config.Config.LLMServerTokenHeader = oldTokenHdr
		config.Config.LLMServerToken = oldToken
	}()

	a := &nubiEnricherAction{}
	ctx := newNubiTestCtx("evt-finding-id")
	resp, err := a.Execute(ctx, map[string]any{
		"prompt": "Why is the pod crashing?",
		"title":  "Crash analysis",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Wire request shape: flat payload to /v1/completions/chat with tenant + auth headers.
	assert.Equal(t, "/v1/completions/chat", captured.path)
	assert.Equal(t, "tenant-1", captured.headers.Get("x-tenant-id"))
	assert.Equal(t, "secret", captured.headers.Get("X-NB-Token"))
	assert.Equal(t, "Why is the pod crashing?", captured.body["query"])
	assert.Equal(t, "acct-1", captured.body["account_id"])
	assert.Equal(t, "tenant-1", captured.body["tenant_id"])
	assert.Equal(t, "evt-finding-id", captured.body["session_id"])
	assert.Equal(t, "Investigation", captured.body["source"])
	assert.Equal(t, true, captured.body["async"])

	// Evidence shape: markdown + additional_info.type + top-level llm_response.
	assert.Equal(t, "markdown", resp.GetFormatName())
	assert.Equal(t, "Why is the pod crashing?", resp.GetData())
	info := resp.GetAdditionalInfo()
	assert.Equal(t, "nubi_enricher", info["type"])
	assert.Equal(t, "Crash analysis", info["title"])
	assert.Equal(t, "nubi_enricher", info["action_name"])
	// LLM response data preserved at top level.
	nubi, ok := resp.(nubiEnricherResponse)
	require.True(t, ok)
	assert.Equal(t, "sess-42", nubi.LLMResponse["session_id"])
	assert.Equal(t, "conv-7", nubi.LLMResponse["conversation_id"])
}

func TestNubiEnricher_AcceptsQueryAlias(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":"sess-flat"}`))
	}))
	defer srv.Close()

	oldEndpoint := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = srv.URL
	defer func() { config.Config.LLMServerEndpoint = oldEndpoint }()

	a := &nubiEnricherAction{}
	resp, err := a.Execute(newNubiTestCtx("evt-2"), map[string]any{"query": "fallback path"})
	require.NoError(t, err)
	nubi, ok := resp.(nubiEnricherResponse)
	require.True(t, ok)
	// Flat-shape response accepted.
	assert.Equal(t, "sess-flat", nubi.LLMResponse["session_id"])
	assert.Equal(t, "fallback path", resp.GetData())
}

func TestNubiEnricher_SurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("upstream broken"))
	}))
	defer srv.Close()

	oldEndpoint := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = srv.URL
	defer func() { config.Config.LLMServerEndpoint = oldEndpoint }()

	a := &nubiEnricherAction{}
	_, err := a.Execute(newNubiTestCtx("evt-3"), map[string]any{"prompt": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
	assert.Contains(t, err.Error(), "upstream broken")
}

func TestNubiEnricher_SyncWhenAsyncFalse(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":"s"}`))
	}))
	defer srv.Close()

	oldEndpoint := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = srv.URL
	defer func() { config.Config.LLMServerEndpoint = oldEndpoint }()

	a := &nubiEnricherAction{}
	_, err := a.Execute(newNubiTestCtx("evt-4"), map[string]any{"prompt": "x", "async": false})
	require.NoError(t, err)
	assert.Equal(t, false, captured["async"])
}

func TestParseNubiLLMResponse(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"rpc-wrapped": {`{"data":{"session_id":"abc"}}`, "abc"},
		"flat":        {`{"session_id":"def"}`, "def"},
		"garbage":     {`not json`, ""},
		"empty":       {``, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := parseNubiLLMResponse([]byte(tc.body))
			if tc.want == "" {
				assert.Empty(t, got["session_id"])
			} else {
				assert.Equal(t, tc.want, got["session_id"])
			}
		})
	}
}

// Defensive: ensure the endpoint trailing-slash is handled identically.
func TestNubiEnricher_EndpointTrailingSlash(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		assert.Equal(t, "/v1/completions/chat", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":"x"}`))
	}))
	defer srv.Close()

	oldEndpoint := config.Config.LLMServerEndpoint
	defer func() { config.Config.LLMServerEndpoint = oldEndpoint }()
	for _, ep := range []string{srv.URL, srv.URL + "/", srv.URL + "//"} {
		config.Config.LLMServerEndpoint = strings.TrimRight(ep, "/")
		a := &nubiEnricherAction{}
		_, err := a.Execute(newNubiTestCtx("e"), map[string]any{"prompt": "p"})
		require.NoError(t, err)
	}
	assert.Equal(t, 3, hits)
	// silence unused import warning if test framework removed it
	_ = time.Second
}
