// Package decision is a client for hosted decision models: models that pick one
// answer from a fixed list and return a probability for each, instead of
// generating text. Providers: Cloudflare's Clef on Workers AI and TypeSafe's Jev,
// which share the "state + typed questions" request shape, and OpenAI Decisions,
// which has its own. See docs/classification-api.md.
package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider values for the decision slot (llm_tier_provider_decision). The slot,
// not the provider name, marks a model as decision-only.
const (
	ProviderCloudflare = "cloudflare"
	ProviderTypeSafe   = "typesafe"
	ProviderOpenAI     = "openai"
)

const (
	cloudflareAccountsURL = "https://api.cloudflare.com/client/v4/accounts/%s/ai/run"
	typesafeURL           = "https://api.typesafe.ai/v1/systemone"
	openaiURL             = "https://api.openai.com/v1/decisions"
	defaultTimeout        = 10 * time.Second
	// questionKey is the single question id sent per request; ids are not seen by the model.
	questionKey = "decision"
)

// Models lists the models each provider serves in the decision slot. Keep in
// sync with api-server's integrations/llm.go.
var Models = map[string][]string{
	ProviderCloudflare: {"clef", "clef-flash"},
	ProviderTypeSafe:   {"jev-latest"},
	ProviderOpenAI:     {"gpt-6-luna"},
}

func normalize(provider string) string { return strings.ToLower(strings.TrimSpace(provider)) }

// IsProvider reports whether provider can serve the decision slot.
func IsProvider(provider string) bool {
	_, ok := Models[normalize(provider)]
	return ok
}

// IsDecisionOnlyProvider reports whether provider sells only decision models, so
// it must never serve a chat call. OpenAI also sells chat models (gpt-6-luna is
// both), so for it only the decision slot marks a model as decision-only.
func IsDecisionOnlyProvider(provider string) bool {
	p := normalize(provider)
	return p == ProviderCloudflare || p == ProviderTypeSafe
}

// IsDecisionModel reports whether model is one of provider's decision models.
func IsDecisionModel(provider, model string) bool {
	for _, m := range Models[normalize(provider)] {
		if m == strings.TrimSpace(model) {
			return true
		}
	}
	return false
}

// Config selects a provider and model and carries its credentials.
type Config struct {
	Provider string // ProviderCloudflare, ProviderTypeSafe or ProviderOpenAI
	Model    string // one of Models[Provider]
	APIKey   string
	// Endpoint is Cloudflare's account id or a full ".../ai/run" base URL. The
	// other providers use a fixed URL and ignore it.
	Endpoint   string
	HTTPClient *http.Client
}

// Client calls one decision-model provider.
type Client struct {
	cfg      Config
	provider string
	url      string
	http     *http.Client
}

// Option is one answer a question allows, in the order the caller gives them.
type Option struct {
	Label       string
	Description string
}

// Choice is the answer to one "choice" question.
type Choice struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"` // nil when the provider omits it
	// Refused is set when the provider declined to answer (OpenAI); Choice is empty.
	Refused     bool   `json:"-"`
	Model       string `json:"-"` // the model version the provider reports
	InputTokens int    `json:"-"`
}

// New validates cfg and returns a client.
func New(cfg Config) (*Client, error) {
	provider := normalize(cfg.Provider)
	if !IsProvider(provider) {
		return nil, fmt.Errorf("decision: unknown provider %q", cfg.Provider)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("decision: model is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("decision: API token is not configured")
	}
	var target string
	switch provider {
	case ProviderCloudflare:
		base := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
		if base == "" {
			return nil, fmt.Errorf("decision: Cloudflare account id is not configured")
		}
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			base = fmt.Sprintf(cloudflareAccountsURL, base)
		}
		target = base + "/@cf/cloudflare/" + url.PathEscape(cfg.Model)
	case ProviderTypeSafe:
		target = typesafeURL
	case ProviderOpenAI:
		target = openaiURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{cfg: cfg, provider: provider, url: target, http: httpClient}, nil
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

// Choose asks one "choice" question about state.
func (c *Client) Choose(ctx context.Context, state, instructions string, options []Option) (Choice, error) {
	payload, decode := any(jevRequest(c.cfg.Model, state, instructions, options)), decodeJev
	if c.provider == ProviderOpenAI {
		payload, decode = openaiRequestFor(c.cfg.Model, state, instructions, options), decodeOpenAI
	}
	raw, err := c.post(ctx, payload)
	if err != nil {
		return Choice{}, err
	}
	choice, err := decode(raw)
	if err != nil {
		return Choice{}, fmt.Errorf("decision: %w", err)
	}
	if choice.Model == "" {
		choice.Model = c.cfg.Model
	}
	return choice, nil
}

func (c *Client) post(ctx context.Context, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("decision: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decision: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	// Cloudflare's edge rejects Go's default user agent on some routes.
	req.Header.Set("User-Agent", "nudgebee-llm-server")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("decision: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("decision: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("decision: provider returned HTTP %d: %s", resp.StatusCode, truncate(raw, 300))
	}
	return raw, nil
}

// truncate shortens b to at most n bytes without splitting a UTF-8 character.
func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	cut := 0
	for i := range s {
		if i > n {
			break
		}
		cut = i
	}
	return s[:cut] + "…"
}
