package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

// IntegrationLLMGateway is the AI Gateway's OWN provider-credential surface ("LLM
// Gateway"): one config per (provider, credential) the gateway may use. `provider` selects
// the shape — openai/anthropic/gemini need only an api_key (native lane, default
// endpoint), while `custom` is a self-hosted / OpenAI-compatible endpoint (base_url +
// models, routed on the vLLM lane, e.g. an HF Endpoint serving Qwen).
//
// It is DEDICATED to the gateway and deliberately separate from the `llm` integration
// that llm-server uses for its own direct calls — the gateway no longer reads `llm` rows,
// keeping the dependency arrow pointing llm-server → gateway (never the reverse).
// The gateway's ee/providers resolver consumes these rows: custom → a per-request vLLM
// DirectKey carrying the base URL; known providers → the provider's key (winning over the
// operator GATEWAY_* env default). base_url is SSRF-validated here at save/test time; at
// request time the gateway relies on Bifrost's dial-time guard (NetworkConfig.
// AllowPrivateNetwork defaults false → private/link-local/metadata blocked).
const IntegrationLLMGateway = "llm_gateway"

func init() {
	core.RegisterIntegration(LLMGateway{})
}

type LLMGateway struct{}

func (m LLMGateway) Name() string { return IntegrationLLMGateway }

// Category groups it under LLM in the integrations UI; Name() (the DB `type`) keeps it
// distinct from the `llm` integration so the two never collide.
func (m LLMGateway) Category() core.IntegrationCategory { return core.IntegrationLLM }

// TenantScoped opts out of the account_id requirement in the generic create + test paths:
// the gateway resolves credentials per-tenant, so this config has no account mapping.
func (m LLMGateway) TenantScoped() bool { return true }

// Provider values the umbrella supports. "custom" is a self-hosted / hosted
// OpenAI-compatible endpoint (base_url + models, routed on the vLLM lane); the rest are
// well-known providers keyed only by an api_key (routed on their native lane, default
// endpoint). Kept in sync with engine.NormalizeProvider on the gateway side.
const (
	llmGatewayProviderCustom    = "custom"
	llmGatewayProviderOpenAI    = "openai"
	llmGatewayProviderAnthropic = "anthropic"
	llmGatewayProviderGemini    = "gemini"
	llmGatewayProviderVertex    = "vertex"
	llmGatewayProviderBedrock   = "bedrock"
	// vertex_openai is Vertex AI's OpenAI-compatible ("MaaS" / Model Garden) endpoint. It
	// shares Vertex's GCP creds (project + region + service-account JSON) but is matched BY
	// MODEL like `custom` (so it also needs `models`) and routed on the vLLM lane to Vertex's
	// openapi/chat/completions path. Kept in sync with the gateway resolver's vertex_openai case.
	llmGatewayProviderVertexOpenAI = "vertex_openai"
)

func (m LLMGateway) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"provider"},
		Testable: true, // renders the "Test Connection" button → TestConnection below
		Properties: map[string]core.IntegrationSchemaProperty{
			// No account_id: the gateway resolves credentials per TENANT (it has no
			// account concept), so an account binding would be dead config.
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "A name to identify this config (e.g. 'qwen-hf' or 'team-openai').",
				Priority:    9,
			},
			"provider": {
				Type:        core.ToolSchemaTypeString,
				Description: "Which provider this credential is for. Choose 'custom' for a self-hosted / OpenAI-compatible endpoint, or 'vertex_openai' for Vertex AI's OpenAI-compatible (Model Garden / MaaS) endpoint.",
				Enum:        []any{llmGatewayProviderOpenAI, llmGatewayProviderAnthropic, llmGatewayProviderGemini, llmGatewayProviderVertex, llmGatewayProviderVertexOpenAI, llmGatewayProviderBedrock, llmGatewayProviderCustom},
				Priority:    8,
			},
			"api_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "API key for the provider. Required for OpenAI/Anthropic/Gemini; optional for a keyless custom server.",
				Priority:    7,
				IsEncrypted: true, // encrypted at rest + redacted from UI reads
				// Shown for EVERY provider. A field with only required_when (no show_when) is
				// hidden by the dynamic form whenever that condition isn't met — so a custom
				// endpoint (not in the required set) would lose the field entirely. Listing all
				// providers here keeps it visible; RequiredWhen below still drives the * marker.
				ShowWhen:     map[string]any{"provider": []any{llmGatewayProviderOpenAI, llmGatewayProviderAnthropic, llmGatewayProviderGemini, llmGatewayProviderCustom}},
				RequiredWhen: map[string]any{"provider": []any{llmGatewayProviderOpenAI, llmGatewayProviderAnthropic, llmGatewayProviderGemini}},
			},
			// base_url + models apply ONLY to a custom endpoint — a well-known provider uses
			// its native default endpoint and its key covers all of that provider's models.
			"base_url": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OpenAI-compatible base URL of the endpoint. A trailing /v1 is optional — both https://<host> and https://<host>/v1 work. Must be https and publicly reachable.",
				Priority:     6,
				ShowWhen:     map[string]any{"provider": llmGatewayProviderCustom},
				RequiredWhen: map[string]any{"provider": llmGatewayProviderCustom},
			},
			"models": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Comma-separated model ids this endpoint serves (e.g. Qwen/Qwen3.6-35B-A3B-FP8, or google/gemma-3-27b-it-maas for Vertex MaaS). A client addresses the model by one of these names.",
				Priority:     5,
				ShowWhen:     map[string]any{"provider": []any{llmGatewayProviderCustom, llmGatewayProviderVertexOpenAI}},
				RequiredWhen: map[string]any{"provider": []any{llmGatewayProviderCustom, llmGatewayProviderVertexOpenAI}},
			},
			// Vertex (provider=vertex): structured GCP creds — the endpoint is derived from
			// project + region, and auth is a service-account JSON. Shown only for vertex; api_key
			// is not used (its ShowWhen excludes vertex).
			"project_id": {
				Type:         core.ToolSchemaTypeString,
				Description:  "GCP project ID that hosts the Vertex AI models.",
				Priority:     4,
				ShowWhen:     map[string]any{"provider": []any{llmGatewayProviderVertex, llmGatewayProviderVertexOpenAI}},
				RequiredWhen: map[string]any{"provider": []any{llmGatewayProviderVertex, llmGatewayProviderVertexOpenAI}},
			},
			// region is shared by Vertex (a GCP region, or 'global') and Bedrock (an AWS region).
			"region": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Provider region — a GCP region for Vertex (e.g. us-central1, or 'global') or an AWS region for Bedrock (e.g. us-east-1). For Vertex (OpenAI-compatible) this sets the request path's location; the host is derived from it unless 'endpoint' is set.",
				Priority:     3,
				ShowWhen:     map[string]any{"provider": []any{llmGatewayProviderVertex, llmGatewayProviderVertexOpenAI, llmGatewayProviderBedrock}},
				RequiredWhen: map[string]any{"provider": []any{llmGatewayProviderVertex, llmGatewayProviderVertexOpenAI, llmGatewayProviderBedrock}},
			},
			// endpoint is OPTIONAL for Vertex (OpenAI-compatible). Two uses: (1) shared MaaS —
			// leave blank to derive the host from region, or set just a host (e.g.
			// aiplatform.googleapis.com) to override it; (2) a DEDICATED endpoint — paste the full
			// chat-completions URL Google gives you and it is dialed verbatim. Host must be under
			// googleapis.com or vertexai.goog.
			"endpoint": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional — where requests are sent. • Shared MaaS: leave blank to use the region's host, or set just a host to override it (e.g. aiplatform.googleapis.com). • Dedicated endpoint: paste the FULL chat-completions URL (e.g. https://{id}.{region}-{project}.prediction.vertexai.goog/v1beta1/projects/{project}/locations/{region}/endpoints/{id}/chat/completions) — a bare dedicated host won't work (its path can't be derived). Host must be under googleapis.com or vertexai.goog.",
				Priority:    2,
				ShowWhen:    map[string]any{"provider": llmGatewayProviderVertexOpenAI},
			},
			"service_account_json": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Service-account key JSON with Vertex AI access — paste the full JSON.",
				Priority:     1,
				IsEncrypted:  true, // encrypted at rest + redacted from UI reads
				Multiline:    true, // it's a multi-line JSON blob
				ShowWhen:     map[string]any{"provider": []any{llmGatewayProviderVertex, llmGatewayProviderVertexOpenAI}},
				RequiredWhen: map[string]any{"provider": []any{llmGatewayProviderVertex, llmGatewayProviderVertexOpenAI}},
			},
			// Bedrock (provider=bedrock): STATIC AWS creds — a tenant must supply access +
			// secret together (a keyless config would borrow the pod's IAM role, an
			// operator-only path). session_token is optional; region is the shared field above.
			"access_key": {
				Type:         core.ToolSchemaTypeString,
				Description:  "AWS access key ID with Bedrock access.",
				Priority:     6,
				ShowWhen:     map[string]any{"provider": llmGatewayProviderBedrock},
				RequiredWhen: map[string]any{"provider": llmGatewayProviderBedrock},
			},
			"secret_key": {
				Type:         core.ToolSchemaTypeString,
				Description:  "AWS secret access key.",
				Priority:     5,
				IsEncrypted:  true, // encrypted at rest + redacted from UI reads
				ShowWhen:     map[string]any{"provider": llmGatewayProviderBedrock},
				RequiredWhen: map[string]any{"provider": llmGatewayProviderBedrock},
			},
			"session_token": {
				Type:        core.ToolSchemaTypeString,
				Description: "AWS session token — only for temporary (STS) credentials; leave blank for long-lived keys.",
				Priority:    4,
				IsEncrypted: true, // encrypted at rest + redacted from UI reads
				ShowWhen:    map[string]any{"provider": llmGatewayProviderBedrock},
			},
		},
	}
}

func (m LLMGateway) ValidateConfig(_ *security.SecurityContext, values []core.IntegrationConfigValue, _ string) []error {
	cfg := make(map[string]string, len(values))
	for _, v := range values {
		cfg[v.Name] = strings.TrimSpace(v.Value)
	}
	var errs []error

	provider := strings.ToLower(cfg["provider"])
	switch provider {
	case llmGatewayProviderOpenAI, llmGatewayProviderAnthropic, llmGatewayProviderGemini:
		// Well-known provider: only an api_key is needed (default endpoint, all models).
		if cfg["api_key"] == "" {
			errs = append(errs, fmt.Errorf("api_key is required for provider %q", provider))
		}
	case llmGatewayProviderCustom:
		// Custom endpoint: needs a base_url (SSRF-validated) + the model ids it serves;
		// api_key is optional (a keyless server is allowed).
		if cfg["base_url"] == "" {
			errs = append(errs, fmt.Errorf("base_url is required for a custom endpoint"))
		} else if err := validateEgressURL(cfg["base_url"]); err != nil {
			// SSRF guard + URL shape, surfaced as a config error so a bad/internal URL is
			// rejected before it can ever be saved.
			errs = append(errs, fmt.Errorf("base_url %s", err))
		}
		if cfg["models"] == "" {
			errs = append(errs, fmt.Errorf("models is required for a custom endpoint (comma-separated model ids)"))
		} else {
			for _, part := range strings.Split(cfg["models"], ",") {
				if strings.TrimSpace(part) == "" {
					errs = append(errs, fmt.Errorf("models must be a comma-separated list with no empty entries"))
					break
				}
			}
		}
	case llmGatewayProviderVertex:
		// Vertex: project + GCP region + a well-formed service-account JSON.
		if cfg["project_id"] == "" {
			errs = append(errs, fmt.Errorf("project_id is required for Vertex"))
		}
		if cfg["region"] == "" {
			errs = append(errs, fmt.Errorf("region is required for Vertex (a GCP region, e.g. us-central1)"))
		}
		errs = append(errs, vertexServiceAccountErrors(values)...)
	case llmGatewayProviderVertexOpenAI:
		// Vertex OpenAI-compatible (MaaS): Vertex's GCP creds PLUS the model ids it serves
		// (matched by model like custom). region may be a GCP region or 'global'.
		if cfg["project_id"] == "" {
			errs = append(errs, fmt.Errorf("project_id is required for Vertex (OpenAI-compatible)"))
		}
		if cfg["region"] == "" {
			errs = append(errs, fmt.Errorf("region is required for Vertex (OpenAI-compatible) — a GCP region, e.g. us-central1, or 'global'"))
		}
		errs = append(errs, vertexServiceAccountErrors(values)...)
		// endpoint is OPTIONAL (blank → the gateway derives the host from region); if set it
		// must be a googleapis.com host — validated here too so a bad value is rejected at save
		// time, not silently skipped by the gateway resolver at request time.
		if err := validateVertexEndpoint(cfg["endpoint"]); err != nil {
			errs = append(errs, fmt.Errorf("endpoint %s", err))
		}
		if cfg["models"] == "" {
			errs = append(errs, fmt.Errorf("models is required for Vertex (OpenAI-compatible) — the MaaS model id(s), comma-separated"))
		} else {
			for _, part := range strings.Split(cfg["models"], ",") {
				if strings.TrimSpace(part) == "" {
					errs = append(errs, fmt.Errorf("models must be a comma-separated list with no empty entries"))
					break
				}
			}
		}
	case llmGatewayProviderBedrock:
		// Bedrock: STATIC AWS creds (access + secret together) + an AWS region. secret_key is
		// encrypted, so on edit an unchanged value arrives as the mask/ciphertext — that's a
		// non-empty string, so the required check passes without needing to parse it.
		if cfg["access_key"] == "" {
			errs = append(errs, fmt.Errorf("access_key is required for Bedrock"))
		}
		if cfg["secret_key"] == "" {
			errs = append(errs, fmt.Errorf("secret_key is required for Bedrock"))
		}
		if cfg["region"] == "" {
			errs = append(errs, fmt.Errorf("region is required for Bedrock (an AWS region, e.g. us-east-1)"))
		}
	case "":
		errs = append(errs, fmt.Errorf("provider is required"))
	default:
		errs = append(errs, fmt.Errorf("unsupported provider %q (expected openai, anthropic, gemini, vertex, vertex_openai, bedrock, or custom)", provider))
	}
	return errs
}

// vertexEndpointHostRe matches a Vertex host. The vertex_openai endpoint is dialed by the
// gateway with a Google OAuth token, so it is constrained to Google's Vertex domains — shared
// MaaS (*.googleapis.com) and dedicated prediction endpoints (*.vertexai.goog) — as an SSRF
// guard. Kept in sync with the guards in the gateway resolver + Test-Connection probe.
var vertexEndpointHostRe = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*\.(googleapis\.com|vertexai\.goog)$`)

// validateVertexEndpoint validates an OPTIONAL vertex_openai endpoint: blank is fine (the
// gateway then derives the host from region); otherwise the HOST (a bare host, or the host of a
// full dedicated-endpoint URL) must be a Vertex domain. Only a leading scheme is stripped, so a
// host smuggled into a path/query is not extracted. Mirrors parseVertexOpenAIEndpoint in the
// gateway.
func validateVertexEndpoint(raw string) error {
	e := strings.TrimSpace(raw)
	if e == "" {
		return nil
	}
	if i := strings.Index(e, "://"); i >= 0 && !strings.ContainsAny(e[:i], "/?#") {
		e = e[i+3:]
	}
	host, path := e, ""
	if i := strings.IndexAny(e, "/?#"); i >= 0 {
		host, path = e[:i], e[i:]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if !vertexEndpointHostRe.MatchString(host) {
		return fmt.Errorf("must be a Vertex AI host under googleapis.com or vertexai.goog (e.g. aiplatform.googleapis.com, or a dedicated {id}.{region}-{project}.prediction.vertexai.goog)")
	}
	// Drop any query/fragment BEFORE deciding whether a path is present — so this agrees with
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

// vertexServiceAccountErrors validates the service_account_json for a Vertex-family provider
// (vertex and vertex_openai). On EDIT an unchanged value arrives as the UI redaction mask or
// as ciphertext (is_encrypted) — neither is parseable JSON — so only a freshly-typed
// plaintext value is format-checked; the "required" check still applies in every case.
func vertexServiceAccountErrors(values []core.IntegrationConfigValue) []error {
	sa, saOpaque := "", false
	for _, v := range values {
		if v.Name == "service_account_json" {
			sa = strings.TrimSpace(v.Value)
			saOpaque = v.IsEncrypted || isRedactionMask(sa)
			break
		}
	}
	if sa == "" {
		return []error{fmt.Errorf("service_account_json is required for Vertex")}
	}
	if !saOpaque {
		if err := validateServiceAccountJSON(sa); err != nil {
			return []error{fmt.Errorf("service_account_json %s", err)}
		}
	}
	return nil
}

// isRedactionMask reports whether a value is the UI's "secret unchanged" placeholder — a
// run of asterisks — so validation never tries to parse it as a real value.
func isRedactionMask(s string) bool {
	return s != "" && strings.Trim(s, "*") == ""
}

// validateServiceAccountJSON checks a pasted GCP service-account key is well-formed JSON
// carrying the fields Vertex auth needs — catching a truncated paste or wrong file at save
// time rather than at request time.
func validateServiceAccountJSON(raw string) error {
	var sa struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if err := json.Unmarshal([]byte(raw), &sa); err != nil {
		return fmt.Errorf("must be valid JSON (paste the full service-account key)")
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return fmt.Errorf("does not look like a service-account key (missing client_email/private_key)")
	}
	return nil
}

// TestConnection delegates the connectivity probe to the GATEWAY — the service that
// actually dials the provider at request time — so a green result reflects the REAL
// serving path (the gateway's own network reachability + SSRF guard), not just the config
// plane's. Mirrors how the `llm` integration delegates its test to llm-server.
func (m LLMGateway) TestConnection(rc *security.RequestContext, values []core.IntegrationConfigValue, _ string) error {
	if config.Config.LLMGatewayURL == "" {
		return fmt.Errorf("llm_gateway_url not configured; cannot run connectivity test")
	}

	cfg := make(map[string]string, len(values))
	for _, v := range values {
		cfg[v.Name] = strings.TrimSpace(v.Value)
	}

	// Fail fast on obvious shape errors before the round-trip (the gateway re-validates).
	if strings.ToLower(cfg["provider"]) == llmGatewayProviderCustom {
		if cfg["base_url"] == "" {
			return fmt.Errorf("base_url is required for a custom endpoint")
		}
		if err := validateEgressURL(cfg["base_url"]); err != nil {
			return fmt.Errorf("base_url %s", err)
		}
	}

	// Thread the request's trace context so the gateway continues the same traceparent.
	// rc may be nil (unit tests call TestConnection directly); fall back to background.
	reqCtx := context.Background()
	if rc != nil && rc.GetContext() != nil {
		reqCtx = rc.GetContext()
	}

	endpoint := strings.TrimRight(config.Config.LLMGatewayURL, "/") + "/rpc/config/test-connection"
	headers := map[string]string{"Content-Type": "application/json"}
	if config.Config.LLMGatewayActionToken != "" {
		headers["X-ACTION-TOKEN"] = config.Config.LLMGatewayActionToken
	}

	resp, err := common.HttpPost(endpoint,
		common.HttpWithContext(reqCtx),
		common.HttpWithJsonBody(map[string]any{"config": cfg}),
		common.HttpWithHeaders(headers),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return fmt.Errorf("could not reach the gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("connectivity test failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("connectivity test returned unparseable response: %w", err)
	}
	if parsed.OK {
		return nil
	}
	if parsed.Error != "" {
		return errors.New(parsed.Error)
	}
	return fmt.Errorf("connectivity test failed")
}
