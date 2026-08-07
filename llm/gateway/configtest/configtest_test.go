package configtest

import (
	"context"
	"strings"
	"testing"
)

// TestProbe_VertexStructuralOK: a well-formed Vertex config passes the (structural) probe
// — project + region present and the service-account JSON parses with the needed fields.
func TestProbe_VertexStructuralOK(t *testing.T) {
	cfg := map[string]string{
		"provider":             "vertex",
		"project_id":           "p",
		"region":               "us-central1",
		"service_account_json": `{"type":"service_account","client_email":"x@y.iam.gserviceaccount.com","private_key":"k"}`,
	}
	if err := probe(context.Background(), cfg); err != nil {
		t.Fatalf("valid vertex config should pass structural probe, got %v", err)
	}
}

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
		{"vertex missing project/region", map[string]string{"provider": "vertex", "service_account_json": `{"client_email":"a","private_key":"b"}`}, "project_id and region"},
		{"vertex malformed SA JSON", map[string]string{"provider": "vertex", "project_id": "p", "region": "us-central1", "service_account_json": "nope"}, "service_account_json"},
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
