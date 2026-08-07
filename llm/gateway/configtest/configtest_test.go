package configtest

import (
	"context"
	"strings"
	"testing"
)

// TestNormalizeBaseURL: a base URL works with or without a trailing /v1 (the vLLM lane
// appends /v1/... itself), so both forms normalize to the same host.
func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://host":        "https://host",
		"https://host/":       "https://host",
		"https://host/v1":     "https://host",
		"https://host/v1/":    "https://host",
		"  https://host/v1  ": "https://host",
		"https://host/openai": "https://host/openai", // only an exact trailing /v1 is stripped
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProbe_ConfigValidation covers the branches that reject a config BEFORE any network
// call — so they run without egress. A valid config would proceed to dial the provider,
// which is exercised live, not here.
func TestProbe_ConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]string
		wantErr string
	}{
		{"missing provider", map[string]string{}, "provider is required"},
		{"unsupported provider", map[string]string{"provider": "bedrock"}, "unsupported provider"},
		{"openai without key", map[string]string{"provider": "openai"}, "api_key is required"},
		{"anthropic without key", map[string]string{"provider": "anthropic"}, "api_key is required"},
		{"gemini without key", map[string]string{"provider": "gemini"}, "api_key is required"},
		{"custom without base_url", map[string]string{"provider": "custom"}, "base_url is required"},
		{"custom http base_url", map[string]string{"provider": "custom", "base_url": "http://localhost/v1"}, "https"},
		{"custom private base_url shape ok", map[string]string{"provider": "custom", "base_url": "not a url"}, "valid URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := probe(context.Background(), tc.cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
