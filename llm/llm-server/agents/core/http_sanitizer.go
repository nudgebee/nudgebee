package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const defaultLLMHTTPTimeout = 5 * time.Minute

// temperatureSanitizingClient intercepts outgoing HTTP requests to LLM provider APIs
// and removes the "temperature" field from the JSON payload when the targeted model
// does not support temperature (e.g. Anthropic claude-sonnet-5 / opus-5, OpenAI o1/o3/gpt-5).
type temperatureSanitizingClient struct {
	provider string
	client   *http.Client
}

func newAnthropicHTTPClient(baseClient ...*http.Client) *temperatureSanitizingClient {
	return newTemperatureSanitizingClient("anthropic", baseClient...)
}

func newOpenAIHTTPClient(baseClient ...*http.Client) *temperatureSanitizingClient {
	return newTemperatureSanitizingClient("openai", baseClient...)
}

func newTemperatureSanitizingClient(provider string, baseClient ...*http.Client) *temperatureSanitizingClient {
	var hc *http.Client
	if len(baseClient) > 0 && baseClient[0] != nil {
		hc = baseClient[0]
	} else {
		hc = &http.Client{
			Timeout: defaultLLMHTTPTimeout,
		}
	}
	return &temperatureSanitizingClient{
		provider: provider,
		client:   hc,
	}
}

func (c *temperatureSanitizingClient) Do(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return c.client.Do(req)
	}
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		return c.client.Do(req)
	}

	bodyBytes, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("temperature sanitizer: read request body: %w", err)
	}

	bodyBytes = c.sanitizeTemperature(bodyBytes)
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.ContentLength = int64(len(bodyBytes))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}

	return c.client.Do(req)
}

func (c *temperatureSanitizingClient) sanitizeTemperature(bodyBytes []byte) []byte {
	if !bytes.Contains(bodyBytes, []byte(`"temperature"`)) {
		return bodyBytes
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return bodyBytes
	}
	if _, hasTemp := payload["temperature"]; !hasTemp {
		return bodyBytes
	}

	var model string
	if modelRaw, hasModel := payload["model"]; hasModel {
		_ = json.Unmarshal(modelRaw, &model)
	}
	if ModelSupportsTemperature(c.provider, model) {
		return bodyBytes
	}

	delete(payload, "temperature")
	newBytes, err := json.Marshal(payload)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}
