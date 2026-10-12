// Package decision is a client for hosted decision models: models that pick one
// answer from a fixed list and return a probability for each, instead of
// generating text. The first provider is Cloudflare's Clef on Workers AI, which
// uses the "state + typed questions" request shape that TypeSafe's Jev also
// accepts. See docs/classification-api.md.
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

// ProviderCloudflare is the provider value for Cloudflare Workers AI in the
// decision slot (llm_tier_provider_decision). The slot, not the provider name,
// marks a model as decision-only; Cloudflare is not a chat provider today.
const ProviderCloudflare = "cloudflare"

const (
	cloudflareAccountsURL = "https://api.cloudflare.com/client/v4/accounts/%s/ai/run"
	defaultTimeout        = 10 * time.Second
	// questionKey is the single question id sent per request; ids are not seen by the model.
	questionKey = "decision"
)

// IsProvider reports whether provider is a decision-model provider.
func IsProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), ProviderCloudflare)
}

// Config selects a provider and model and carries its credentials.
type Config struct {
	Provider string // ProviderCloudflare
	Model    string // "clef" or "clef-flash"
	APIKey   string // Cloudflare API token
	// Endpoint is the Cloudflare account id, or a full ".../ai/run" base URL.
	Endpoint   string
	HTTPClient *http.Client
}

// Client calls one decision-model provider.
type Client struct {
	cfg  Config
	url  string
	http *http.Client
}

// Choice is the answer to one "choice" question.
type Choice struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"` // nil when the provider omits it
	Model         string             `json:"-"`
	InputTokens   int                `json:"-"`
}

// New validates cfg and returns a client.
func New(cfg Config) (*Client, error) {
	if !IsProvider(cfg.Provider) {
		return nil, fmt.Errorf("decision: unknown provider %q", cfg.Provider)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("decision: model is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("decision: API token is not configured")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if base == "" {
		return nil, fmt.Errorf("decision: Cloudflare account id is not configured")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = fmt.Sprintf(cloudflareAccountsURL, base)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{cfg: cfg, url: base + "/@cf/cloudflare/" + url.PathEscape(cfg.Model), http: httpClient}, nil
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

type question struct {
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Criteria     map[string]any `json:"criteria"`
}

type request struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type answers struct {
	Model   string            `json:"model"`
	Answers map[string]Choice `json:"answers"`
	Usage   struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// Choose asks one "choice" question about state. criteria maps each option label
// to its description; an empty description is sent as null, which the API accepts.
func (c *Client) Choose(ctx context.Context, state, instructions string, criteria map[string]string) (Choice, error) {
	sent := make(map[string]any, len(criteria))
	for label, desc := range criteria {
		if desc == "" {
			sent[label] = nil
		} else {
			sent[label] = desc
		}
	}
	body, err := json.Marshal(request{
		Model: c.cfg.Model,
		State: state,
		Questions: map[string]question{questionKey: {
			Type:         "choice",
			Instructions: instructions,
			Criteria:     sent,
		}},
	})
	if err != nil {
		return Choice{}, fmt.Errorf("decision: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return Choice{}, fmt.Errorf("decision: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	// Cloudflare's edge rejects Go's default user agent on some routes.
	req.Header.Set("User-Agent", "nudgebee-llm-server")

	resp, err := c.http.Do(req)
	if err != nil {
		return Choice{}, fmt.Errorf("decision: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Choice{}, fmt.Errorf("decision: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Choice{}, fmt.Errorf("decision: provider returned HTTP %d: %s", resp.StatusCode, truncate(raw, 300))
	}
	parsed, err := decodeAnswers(raw)
	if err != nil {
		return Choice{}, fmt.Errorf("decision: %w", err)
	}
	choice, ok := parsed.Answers[questionKey]
	if !ok || choice.Choice == "" {
		return Choice{}, fmt.Errorf("decision: response has no answer")
	}
	choice.Model = parsed.Model
	if choice.Model == "" {
		choice.Model = c.cfg.Model
	}
	choice.InputTokens = parsed.Usage.InputTokens
	return choice, nil
}

// decodeAnswers accepts the Workers AI envelope ({"result": {...}, "success": true})
// and a bare body.
func decodeAnswers(raw []byte) (answers, error) {
	var envelope struct {
		Result  *answers `json:"result"`
		Success *bool    `json:"success"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		// Checked before Result: a Workers AI error has success=false and no result.
		if envelope.Success != nil && !*envelope.Success {
			return answers{}, fmt.Errorf("provider reported success=false: %s", truncate(raw, 300))
		}
		if envelope.Result != nil {
			return *envelope.Result, nil
		}
	}
	var bare answers
	if err := json.Unmarshal(raw, &bare); err != nil {
		return answers{}, fmt.Errorf("decode response: %w", err)
	}
	return bare, nil
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
