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
