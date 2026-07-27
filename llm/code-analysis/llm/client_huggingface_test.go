package llm

import (
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/config"
)

// A tenant on a HuggingFace dedicated endpoint must produce a working client.
// Before this was supported, NewClient fell through to the default branch and
// every analysis that relied on it failed at construction with "unsupported LLM
// provider: huggingface" before doing any work.
func TestNewClient_HuggingFaceOpenAICompatible(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "huggingface"
	cfg.LLM.Model = "Qwen/Qwen3.6-35B-A3B-FP8"
	cfg.LLM.ApiKey = "hf-test-token"
	cfg.LLM.ApiEndpoint = "https://example.endpoints.huggingface.cloud"
	cfg.LLM.ApiType = "openai"

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.llm == nil {
		t.Fatal("NewClient returned a client with no underlying model")
	}
}

// The endpoint is configured as a bare host, but langchaingo posts to
// "{baseURL}/chat/completions" while HuggingFace serves the OpenAI-compatible
// route under /v1 — so the base URL must carry the /v1, exactly once.
func TestHuggingFaceBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://example.endpoints.huggingface.cloud":     "https://example.endpoints.huggingface.cloud/v1",
		"https://example.endpoints.huggingface.cloud/":    "https://example.endpoints.huggingface.cloud/v1",
		"https://example.endpoints.huggingface.cloud/v1":  "https://example.endpoints.huggingface.cloud/v1",
		"https://example.endpoints.huggingface.cloud/v1/": "https://example.endpoints.huggingface.cloud/v1",
	}
	for in, want := range cases {
		if got := huggingFaceBaseURL(in); got != want {
			t.Errorf("huggingFaceBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The native HuggingFace inference protocol is a different wire format. Fail at
// construction with a clear message rather than sending it OpenAI-shaped
// requests that only blow up once an agent is mid-run.
func TestNewClient_HuggingFaceRejectsNonOpenAIAPIType(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "huggingface"
	cfg.LLM.Model = "meta-llama/Llama-3.3-70B-Instruct"
	cfg.LLM.ApiKey = "hf-test-token"
	cfg.LLM.ApiEndpoint = "https://api-inference.huggingface.co"
	cfg.LLM.ApiType = "inference"

	if _, err := NewClient(cfg); err == nil {
		t.Fatal("expected an error for a non-OpenAI-compatible huggingface endpoint")
	} else if !strings.Contains(err.Error(), "OpenAI-compatible") {
		t.Fatalf("error should name the requirement, got: %v", err)
	}
}

func TestNewClient_HuggingFaceRequiresEndpoint(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "huggingface"
	cfg.LLM.Model = "Qwen/Qwen3.6-35B-A3B-FP8"
	cfg.LLM.ApiKey = "hf-test-token"
	cfg.LLM.ApiType = "openai"

	if _, err := NewClient(cfg); err == nil {
		t.Fatal("expected an error when no endpoint is configured")
	}
}

// An unknown provider must still be rejected — the huggingface case is an
// addition to the switch, not a catch-all.
func TestNewClient_UnknownProviderStillRejected(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "not-a-provider"

	if _, err := NewClient(cfg); err == nil {
		t.Fatal("expected an error for an unknown provider")
	} else if !strings.Contains(err.Error(), "unsupported LLM provider") {
		t.Fatalf("unexpected error: %v", err)
	}
}
