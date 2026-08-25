package configtest

import (
	"context"
	"net"
	"strings"
	"testing"
)

// TestIsBlockedEgressIP: the private-network opt-in gates loopback + RFC1918 private, but
// link-local/cloud-metadata (169.254.169.254) and unspecified stay blocked even when opted in.
func TestIsBlockedEgressIP(t *testing.T) {
	cases := []struct {
		name    string
		ip      string
		allow   bool
		blocked bool
	}{
		{"metadata blocked (flag off)", "169.254.169.254", false, true},
		{"metadata STILL blocked (flag on)", "169.254.169.254", true, true},
		{"unspecified blocked (flag on)", "0.0.0.0", true, true},
		{"private blocked (flag off)", "10.0.0.5", false, true},
		{"private allowed (flag on)", "10.0.0.5", true, false},
		{"loopback blocked (flag off)", "127.0.0.1", false, true},
		{"loopback allowed (flag on)", "127.0.0.1", true, false},
		{"public allowed (flag off)", "8.8.8.8", false, false},
	}
	for _, tc := range cases {
		if got := isBlockedEgressIP(net.ParseIP(tc.ip), tc.allow); got != tc.blocked {
			t.Errorf("%s: isBlockedEgressIP(%s, allow=%v) = %v, want %v", tc.name, tc.ip, tc.allow, got, tc.blocked)
		}
	}
}

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

// TestProbe_VertexOpenAIStructuralOK: a well-formed vertex_openai config passes the
// (structural) probe — project + region + a well-formed SA JSON + models all present.
func TestProbe_VertexOpenAIStructuralOK(t *testing.T) {
	cfg := map[string]string{
		"provider":             "vertex_openai",
		"project_id":           "p",
		"region":               "global",
		"service_account_json": `{"type":"service_account","client_email":"x@y.iam.gserviceaccount.com","private_key":"k"}`,
		"models":               "google/gemma-3-27b-it-maas",
	}
	if err := probe(context.Background(), cfg); err != nil {
		t.Fatalf("valid vertex_openai config should pass structural probe, got %v", err)
	}
	// A bare-host endpoint override passes.
	cfg["endpoint"] = "https://aiplatform.googleapis.com"
	if err := probe(context.Background(), cfg); err != nil {
		t.Fatalf("valid vertex_openai config with endpoint override should pass, got %v", err)
	}
	// A full DEDICATED-endpoint URL (prediction.vertexai.goog host) passes too.
	cfg["endpoint"] = "https://456.us-central1-1234567890.prediction.vertexai.goog/v1beta1/projects/1234567890/locations/us-central1/endpoints/456/chat/completions"
	if err := probe(context.Background(), cfg); err != nil {
		t.Fatalf("valid vertex_openai config with a dedicated-endpoint URL should pass, got %v", err)
	}
	// An alias=served models entry passes.
	cfg["models"] = "qwen-vertex=Qwen/Qwen3.6-35B-A3B-FP8, google/gemma-3-27b-it-maas"
	if err := probe(context.Background(), cfg); err != nil {
		t.Fatalf("valid vertex_openai config with an alias=served models entry should pass, got %v", err)
	}
}

// TestProbe_BedrockStructuralOK: a well-formed Bedrock config passes the (structural) probe
// — static access + secret + region all present.
func TestProbe_BedrockStructuralOK(t *testing.T) {
	cfg := map[string]string{"provider": "bedrock", "access_key": "AKIA", "secret_key": "sk", "region": "us-east-1"}
	if err := probe(context.Background(), cfg); err != nil {
		t.Fatalf("valid bedrock config should pass structural probe, got %v", err)
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
		{"unsupported provider", map[string]string{"provider": "groq"}, "unsupported provider"},
		{"openai without key", map[string]string{"provider": "openai"}, "api_key is required"},
		{"anthropic without key", map[string]string{"provider": "anthropic"}, "api_key is required"},
		{"gemini without key", map[string]string{"provider": "gemini"}, "api_key is required"},
		{"custom without base_url", map[string]string{"provider": "custom"}, "base_url is required"},
		{"custom non-http scheme", map[string]string{"provider": "custom", "base_url": "ftp://host/v1"}, "http or https"},
		{"custom private base_url shape ok", map[string]string{"provider": "custom", "base_url": "not a url"}, "valid URL"},
		{"vertex missing project/region", map[string]string{"provider": "vertex", "service_account_json": `{"client_email":"a","private_key":"b"}`}, "project_id and region"},
		{"vertex malformed SA JSON", map[string]string{"provider": "vertex", "project_id": "p", "region": "us-central1", "service_account_json": "nope"}, "service_account_json"},
		{"vertex_openai missing project/region", map[string]string{"provider": "vertex_openai", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "m"}, "project_id and region"},
		{"vertex_openai invalid region", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "US_CENTRAL1", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "m"}, "not a valid Vertex location"},
		{"vertex_openai missing models", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": `{"client_email":"a","private_key":"b"}`}, "models is required"},
		{"vertex_openai empty model entry", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "m1,,m2"}, "no empty entries"},
		{"vertex_openai models empty alias", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "=served"}, "alias=served"},
		{"vertex_openai models double equals", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "a=b=c"}, "more than one"},
		{"vertex_openai duplicate alias", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "qwen=A, qwen=B"}, "duplicate"},
		{"vertex_openai non-googleapis endpoint", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "m", "endpoint": "internal.evil.com"}, "endpoint must be a Vertex AI host"},
		{"vertex_openai bare dedicated host", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "asia-southeast1", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "m", "endpoint": "mg-endpoint-abc.asia-southeast1-000000000000.prediction.vertexai.goog"}, "full chat-completions URL"},
		{"vertex_openai dedicated host query only", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "asia-southeast1", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "m", "endpoint": "mg-endpoint-abc.asia-southeast1-000000000000.prediction.vertexai.goog?foo=bar"}, "full chat-completions URL"},
		{"vertex_openai dedicated host trailing v1", map[string]string{"provider": "vertex_openai", "project_id": "p", "region": "asia-southeast1", "service_account_json": `{"client_email":"a","private_key":"b"}`, "models": "m", "endpoint": "mg-endpoint-abc.asia-southeast1-000000000000.prediction.vertexai.goog/v1"}, "full chat-completions URL"},
		{"bedrock missing creds", map[string]string{"provider": "bedrock", "region": "us-east-1"}, "access_key and secret_key"},
		{"bedrock missing region", map[string]string{"provider": "bedrock", "access_key": "AKIA", "secret_key": "sk"}, "region is required for Bedrock"},
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
