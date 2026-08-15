// Package configtest is the gateway's connectivity-probe endpoint for LLM Gateway
// integration configs. The api-server (config plane) delegates "Test Connection" here so
// the probe runs from the GATEWAY's own network, with the same SSRF guard the gateway
// applies at request time — a green result means the gateway itself can reach the
// provider, not merely that the config plane can. Guarded by the shared service token
// (X-ACTION-TOKEN); the config to test is sent in the body (the row isn't saved yet).
package configtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"nudgebee/llm-gateway/rpc"
)

// RegisterRoutes mounts POST /rpc/config/test-connection, guarded by the service token.
func RegisterRoutes(r *gin.Engine, token string) {
	r.POST("/rpc/config/test-connection", rpc.ServiceToken(token), testConnection)
}

type testConnectionRequest struct {
	Config map[string]string `json:"config"`
}

// validateServiceAccountJSON checks a pasted GCP service-account key is well-formed JSON
// carrying the fields Vertex auth needs — catching a truncated paste or wrong file.
func validateServiceAccountJSON(raw string) error {
	var sa struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &sa); err != nil {
		return fmt.Errorf("must be valid JSON (paste the full service-account key)")
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return fmt.Errorf("does not look like a service-account key (missing client_email/private_key)")
	}
	return nil
}

// vertexRegionRe bounds a Vertex location token to characters that can't smuggle a different
// host into the constructed aiplatform URL. (Kept in sync with the identical guard in
// ee/providers/vertexopenai.go, which applies it at request time.)
var vertexRegionRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// validVertexRegion reports whether region is a safe Vertex location token: "global" or a
// standard regional id (us-central1, europe-west4, …).
func validVertexRegion(region string) bool {
	return region == "global" || vertexRegionRe.MatchString(region)
}

// vertexEndpointHostRe matches a Vertex host. The vertex_openai endpoint is tenant-supplied, so
// it is constrained to Google's Vertex domains — shared MaaS (*.googleapis.com) and dedicated
// prediction endpoints (*.vertexai.goog) — while blocking an arbitrary/internal one. (Kept in
// sync with the identical guard in ee/providers/vertexopenai.go, applied at request time.)
var vertexEndpointHostRe = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*\.(googleapis\.com|vertexai\.goog)$`)

// validVertexEndpoint validates an OPTIONAL vertex_openai endpoint: blank is fine (the host is
// then derived from region); otherwise the HOST (a bare host, or the host of a full
// dedicated-endpoint URL) must be a Vertex domain. Only a leading scheme is stripped, so a host
// smuggled into a path/query can't pass. (Mirrors parseVertexOpenAIEndpoint in ee/providers.)
func validVertexEndpoint(raw string) error {
	e := strings.TrimSpace(raw)
	if e == "" {
		return nil
	}
	if i := strings.Index(e, "://"); i >= 0 && !strings.ContainsAny(e[:i], "/?#") { // drop a leading scheme only
		e = e[i+3:]
	}
	host, path := e, ""
	if i := strings.IndexAny(e, "/?#"); i >= 0 { // split host from any path/query/fragment
		host, path = e[:i], e[i:]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if !vertexEndpointHostRe.MatchString(host) {
		return fmt.Errorf("must be a Vertex AI host under googleapis.com or vertexai.goog (e.g. aiplatform.googleapis.com, or a dedicated {id}.{region}-{project}.prediction.vertexai.goog)")
	}
	// Drop any query/fragment BEFORE deciding whether a path is present — so hasPath agrees with
	// parseVertexOpenAIEndpoint (which strips them first); otherwise a "host?foo=bar" would pass
	// here but be rejected at request time.
	if j := strings.IndexAny(path, "?#"); j >= 0 {
		path = path[:j]
	}
	// Match the parser: a lone trailing /v1 (OpenAI habit) counts as no path.
	path = strings.TrimSuffix(strings.TrimRight(path, "/"), "/v1")
	// A dedicated prediction host has no derivable path — require the full URL, not a bare host.
	if path == "" && strings.HasSuffix(host, ".vertexai.goog") {
		return fmt.Errorf("for a dedicated Vertex endpoint, paste the full chat-completions URL (host + path), not just the host")
	}
	return nil
}

// validateModelsEntries checks a comma-separated `models` list: each entry is a bare model id
// or an "alias=served" pair (client-facing alias → the model name forwarded upstream). Rejects
// empty entries and malformed pairs so the probe agrees with the save path + the gateway's
// registerModels. (Mirrors validateModelsEntries in api-server's llm_gateway.go.)
func validateModelsEntries(models string) error {
	seen := map[string]bool{}
	for _, part := range strings.Split(models, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("models must be a comma-separated list with no empty entries")
		}
		if strings.Count(part, "=") > 1 {
			return fmt.Errorf("models entry %q has more than one '=' (use alias=served)", part)
		}
		alias := part
		if a, served, ok := strings.Cut(part, "="); ok {
			alias = strings.TrimSpace(a)
			if alias == "" || strings.TrimSpace(served) == "" {
				return fmt.Errorf("models entry %q must be alias=served with both sides non-empty", part)
			}
		}
		// The alias is the routing key — a duplicate within one config would silently collide.
		if seen[alias] {
			return fmt.Errorf("models has a duplicate id/alias %q", alias)
		}
		seen[alias] = true
	}
	return nil
}

// normalizeBaseURL trims a trailing slash and an optional trailing /v1, so a base URL works
// whether or not the user included the version path — the vLLM lane appends /v1/... itself.
// (Kept in sync with the identical helper in ee/providers, which does the same at dial time.)
func normalizeBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	u = strings.TrimSuffix(u, "/v1")
	return strings.TrimRight(u, "/")
}

func testConnection(c *gin.Context) {
	var req testConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request body"})
		return
	}
	if err := probe(c.Request.Context(), req.Config); err != nil {
		// A failed probe is a normal outcome, not a server error — return 200 with ok=false
		// so the caller surfaces the humanized message (mirrors llm-server's test shape).
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// probe hits the provider's list-models endpoint with the configured credential over the
// SSRF-safe client. Well-known providers use their fixed public host; a custom endpoint
// uses the (SSRF-validated) base_url.
func probe(ctx context.Context, cfg map[string]string) error {
	provider := strings.ToLower(strings.TrimSpace(cfg["provider"]))
	apiKey := strings.TrimSpace(cfg["api_key"])

	var probeURL, endpointLabel string
	applyAuth := func(*http.Request) {}
	switch provider {
	case "openai":
		if apiKey == "" {
			return fmt.Errorf("api_key is required for provider %q", provider)
		}
		probeURL, endpointLabel = "https://api.openai.com/v1/models", "OpenAI"
		applyAuth = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+apiKey) }
	case "anthropic":
		if apiKey == "" {
			return fmt.Errorf("api_key is required for provider %q", provider)
		}
		probeURL, endpointLabel = "https://api.anthropic.com/v1/models", "Anthropic"
		applyAuth = func(r *http.Request) {
			r.Header.Set("x-api-key", apiKey)
			r.Header.Set("anthropic-version", "2023-06-01")
		}
	case "gemini":
		if apiKey == "" {
			return fmt.Errorf("api_key is required for provider %q", provider)
		}
		// Gemini (Google AI Studio) takes the key as a query param, not a header.
		probeURL = "https://generativelanguage.googleapis.com/v1beta/models?key=" + url.QueryEscape(apiKey)
		endpointLabel = "Gemini"
	case "vertex":
		// Structural validation only (this cut): a live probe would need an OAuth token
		// minted from the service-account JSON. Confirm project/region are set and the SA
		// JSON is well-formed so a truncated/wrong paste is caught here; real connectivity
		// is proven on the first routed request.
		if strings.TrimSpace(cfg["project_id"]) == "" || strings.TrimSpace(cfg["region"]) == "" {
			return fmt.Errorf("project_id and region are required for Vertex")
		}
		sa := strings.TrimSpace(cfg["service_account_json"])
		if sa == "" {
			return fmt.Errorf("service_account_json is required for Vertex")
		}
		if err := validateServiceAccountJSON(sa); err != nil {
			return fmt.Errorf("service_account_json %s", err)
		}
		return nil
	case "vertex_openai":
		// Vertex AI's OpenAI-compatible ("MaaS") endpoint. Structural validation only (this
		// cut), mirroring the `vertex` case: confirm project/region and a well-formed
		// service-account JSON so a truncated/wrong paste is caught here; real connectivity
		// (and the OAuth token mint from the SA JSON) is proven on the first routed request.
		if strings.TrimSpace(cfg["project_id"]) == "" || strings.TrimSpace(cfg["region"]) == "" {
			return fmt.Errorf("project_id and region are required for Vertex (OpenAI-compatible)")
		}
		if !validVertexRegion(strings.TrimSpace(cfg["region"])) {
			return fmt.Errorf("region %q is not a valid Vertex location (e.g. us-central1, or global)", strings.TrimSpace(cfg["region"]))
		}
		// endpoint is optional (blank → host derived from region); if set, it must be a
		// googleapis.com host.
		if err := validVertexEndpoint(cfg["endpoint"]); err != nil {
			return fmt.Errorf("endpoint %s", err)
		}
		sa := strings.TrimSpace(cfg["service_account_json"])
		if sa == "" {
			return fmt.Errorf("service_account_json is required for Vertex (OpenAI-compatible)")
		}
		if err := validateServiceAccountJSON(sa); err != nil {
			return fmt.Errorf("service_account_json %s", err)
		}
		// Mirror ValidateConfig (api-server): require at least one model id and reject empty
		// entries (e.g. "m1,,m2"), so the probe and the save path agree.
		if models := strings.TrimSpace(cfg["models"]); models == "" {
			return fmt.Errorf("models is required for Vertex (OpenAI-compatible) — list the MaaS model id(s), comma-separated")
		} else if err := validateModelsEntries(models); err != nil {
			return err
		}
		return nil
	case "bedrock":
		// Structural validation only (this cut): a live probe would need AWS SigV4 signing.
		// Confirm static access+secret and a region are present so an incomplete config is
		// caught here; real connectivity is proven on the first routed request.
		if strings.TrimSpace(cfg["access_key"]) == "" || strings.TrimSpace(cfg["secret_key"]) == "" {
			return fmt.Errorf("access_key and secret_key are required for Bedrock")
		}
		if strings.TrimSpace(cfg["region"]) == "" {
			return fmt.Errorf("region is required for Bedrock (an AWS region, e.g. us-east-1)")
		}
		return nil
	case "custom":
		base := strings.TrimSpace(cfg["base_url"])
		if base == "" {
			return fmt.Errorf("base_url is required for a custom endpoint")
		}
		if err := validateEgressURL(base); err != nil {
			return fmt.Errorf("base_url %s", err)
		}
		// Probe the SAME path the gateway dials at request time: Bifrost's vLLM lane
		// appends /v1/... to the base, so normalize off any trailing /v1 the user included,
		// then probe /v1/models. This keeps Test Connection consistent with real requests.
		base = normalizeBaseURL(base)
		probeURL, endpointLabel = base+"/v1/models", base
		if apiKey != "" {
			applyAuth = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+apiKey) }
		}
	case "":
		return fmt.Errorf("provider is required")
	default:
		return fmt.Errorf("unsupported provider %q", provider)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return fmt.Errorf("could not build probe request: %w", err)
	}
	applyAuth(req)

	resp, err := ssrfSafeHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s — check connectivity and the endpoint: %w", endpointLabel, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s rejected the api key (HTTP %d) — check the key", endpointLabel, resp.StatusCode)
	case http.StatusNotFound:
		return fmt.Errorf("no list-models endpoint at %s (HTTP 404) — for a custom endpoint, is base_url the OpenAI-compatible base?", endpointLabel)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		// A warming-up endpoint may still return a useful diagnostic (GPU OOM, queue full);
		// surface it when present. Skip an HTML body (leading "<") — a 502/503 from a proxy
		// (Cloudflare/Nginx/ALB) is usually an error page, which would just clutter the UI.
		if detail := strings.TrimSpace(string(body)); detail != "" && !strings.HasPrefix(detail, "<") {
			return fmt.Errorf("%s is unavailable (HTTP %d): %s — the endpoint may be starting up (cold start / scaled to zero); retry in a moment", endpointLabel, resp.StatusCode, detail)
		}
		return fmt.Errorf("%s is unavailable (HTTP %d) — the endpoint may be starting up (cold start / scaled to zero); retry in a moment", endpointLabel, resp.StatusCode)
	default:
		return fmt.Errorf("%s connectivity failed (HTTP %d): %s", endpointLabel, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}
