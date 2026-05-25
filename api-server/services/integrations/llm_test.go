package integrations

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func llmConfig(m map[string]string) []core.IntegrationConfigValue {
	out := make([]core.IntegrationConfigValue, 0, len(m))
	for k, v := range m {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

// errorsContain reports whether any error message contains substr.
func errorsContain(errs []error, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), substr) {
			return true
		}
	}
	return false
}

func TestLLMValidateConfig(t *testing.T) {
	tests := []struct {
		name        string
		cfg         map[string]string
		wantErr     bool
		wantSubstrs []string
	}{
		{
			name: "valid openai",
			cfg: map[string]string{
				"llm_provider":         "openai",
				"llm_model_name":       "gpt-4o",
				"llm_provider_api_key": "sk-abc",
			},
			wantErr: false,
		},
		{
			name: "valid bedrock",
			cfg: map[string]string{
				"llm_provider":            "bedrock",
				"llm_model_name":          "anthropic.claude-3",
				"llm_provider_access_key": "AKIA...",
				"llm_provider_secret_key": "secret",
				"llm_provider_region":     "us-east-1",
			},
			wantErr: false,
		},
		{
			name:    "missing provider and model",
			cfg:     map[string]string{},
			wantErr: true,
			wantSubstrs: []string{
				"llm_provider is required",
				"llm_model_name is required",
			},
		},
		{
			name: "invalid provider enum",
			cfg: map[string]string{
				"llm_provider":   "not-a-provider",
				"llm_model_name": "x",
			},
			wantErr:     true,
			wantSubstrs: []string{`llm_provider "not-a-provider" is invalid`},
		},
		{
			name: "bedrock missing access_key",
			cfg: map[string]string{
				"llm_provider":            "bedrock",
				"llm_model_name":          "m",
				"llm_provider_secret_key": "secret",
				"llm_provider_region":     "us-east-1",
			},
			wantErr:     true,
			wantSubstrs: []string{"llm_provider_access_key is required", "must be provided together"},
		},
		{
			name: "bedrock missing secret_key",
			cfg: map[string]string{
				"llm_provider":            "bedrock",
				"llm_model_name":          "m",
				"llm_provider_access_key": "AKIA",
				"llm_provider_region":     "us-east-1",
			},
			wantErr:     true,
			wantSubstrs: []string{"llm_provider_secret_key is required", "must be provided together"},
		},
		{
			name: "bedrock orphan access_key",
			cfg: map[string]string{
				"llm_provider":            "bedrock",
				"llm_model_name":          "m",
				"llm_provider_access_key": "AKIA",
				"llm_provider_secret_key": "",
				"llm_provider_region":     "us-east-1",
			},
			wantErr:     true,
			wantSubstrs: []string{"must be provided together"},
		},
		{
			name: "bedrock invalid region",
			cfg: map[string]string{
				"llm_provider":            "bedrock",
				"llm_model_name":          "m",
				"llm_provider_access_key": "AKIA",
				"llm_provider_secret_key": "secret",
				"llm_provider_region":     "useast1",
			},
			wantErr:     true,
			wantSubstrs: []string{"is not a valid AWS region"},
		},
		{
			name: "azure missing endpoint",
			cfg: map[string]string{
				"llm_provider":             "azure",
				"llm_model_name":           "gpt-4",
				"llm_provider_api_key":     "k",
				"llm_provider_api_version": "2024-02-01",
			},
			wantErr:     true,
			wantSubstrs: []string{"llm_provider_api_endpoint is required"},
		},
		{
			name: "sagemaker missing region",
			cfg: map[string]string{
				"llm_provider":              "sagemaker",
				"llm_model_name":            "m",
				"llm_provider_api_endpoint": "https://runtime.sagemaker.us-east-1.amazonaws.com",
			},
			wantErr:     true,
			wantSubstrs: []string{"llm_provider_region is required"},
		},
		{
			name: "invalid endpoint url",
			cfg: map[string]string{
				"llm_provider":              "azure",
				"llm_model_name":            "gpt-4",
				"llm_provider_api_key":      "k",
				"llm_provider_api_version":  "2024-02-01",
				"llm_provider_api_endpoint": "not a url",
			},
			wantErr:     true,
			wantSubstrs: []string{"must be a valid http(s) URL"},
		},
		{
			name: "empty fallback entry",
			cfg: map[string]string{
				"llm_provider":         "openai",
				"llm_model_name":       "gpt-4o",
				"llm_provider_api_key": "k",
				"llm_model_fallbacks":  "gpt-4, ,gpt-3.5",
			},
			wantErr:     true,
			wantSubstrs: []string{"comma-separated list with no empty entries"},
		},
		{
			name: "summarization on without summary fields",
			cfg: map[string]string{
				"llm_provider":                "openai",
				"llm_model_name":              "gpt-4o",
				"llm_provider_api_key":        "k",
				"add_model_for_summarization": "true",
			},
			wantErr: true,
			wantSubstrs: []string{
				"llm_provider_summary_agent is required",
				"llm_model_name_summary_agent is required",
			},
		},
		{
			name: "summarization on valid",
			cfg: map[string]string{
				"llm_provider":                       "openai",
				"llm_model_name":                     "gpt-4o",
				"llm_provider_api_key":               "k",
				"add_model_for_summarization":        "true",
				"llm_provider_summary_agent":         "openai",
				"llm_model_name_summary_agent":       "gpt-4o-mini",
				"llm_provider_api_key_summary_agent": "k2",
			},
			wantErr: false,
		},
		{
			name: "anthropic does not require endpoint",
			cfg: map[string]string{
				"llm_provider":         "anthropic",
				"llm_model_name":       "claude-3",
				"llm_provider_api_key": "k",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := LLM{}.ValidateConfig(nil, llmConfig(tt.cfg), "")
			if tt.wantErr {
				assert.NotEmpty(t, errs, "expected validation errors")
			} else {
				assert.Empty(t, errs, "expected no validation errors, got: %v", errs)
			}
			for _, sub := range tt.wantSubstrs {
				assert.Truef(t, errorsContain(errs, sub), "expected an error containing %q, got: %v", sub, errs)
			}
		})
	}
}

// asStringSlice coerces a RequiredWhen condition value to []string.
func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{t}
	default:
		return nil
	}
}

// TestLLMRequiredFieldsMatchSchema is a drift guard: the hardcoded
// providerRequiredFields table must stay consistent with the schema's
// RequiredWhen declarations keyed off llm_provider. If a future schema edit
// (e.g. another PR like #29663) changes a RequiredWhen without updating the
// validator, this test fails.
func TestLLMRequiredFieldsMatchSchema(t *testing.T) {
	schema := LLM{}.ConfigSchema()

	for _, provider := range llmProviders {
		var fromSchema []string
		for fieldName, prop := range schema.Properties {
			cond, ok := prop.RequiredWhen["llm_provider"]
			if !ok {
				continue
			}
			for _, p := range asStringSlice(cond) {
				if p == provider {
					fromSchema = append(fromSchema, fieldName)
				}
			}
		}
		fromTable := append([]string(nil), providerRequiredFields(provider)...)
		sort.Strings(fromSchema)
		sort.Strings(fromTable)
		assert.Equalf(t, fromSchema, fromTable,
			"providerRequiredFields(%q) is out of sync with schema RequiredWhen", provider)
	}
}

// TestLLMRequiredSummaryFieldsMatchSchema is the summary-agent counterpart of
// the drift guard above. The provider-conditional summary fields
// (endpoint/version/region) must mirror the schema's RequiredWhen keyed off
// llm_provider_summary_agent.
//
// llm_provider_api_key_summary_agent is deliberately NOT covered by the
// schema-derived comparison: in the schema it is unconditionally required when
// add_model_for_summarization is true (not keyed on the summary provider),
// whereas providerRequiredSummaryFields intentionally scopes it to the
// providers that actually use an API key (mirroring llm-server runtime, see
// the [2026-05] decision). That intentional deviation is asserted explicitly.
func TestLLMRequiredSummaryFieldsMatchSchema(t *testing.T) {
	schema := LLM{}.ConfigSchema()

	const apiKeySummary = "llm_provider_api_key_summary_agent"
	apiKeyProviders := map[string]bool{
		"azure": true, "anthropic": true, "googleai": true,
		"huggingface": true, "openai": true, "vertexai": true,
	}

	for _, provider := range llmProviders {
		var fromSchema []string
		for fieldName, prop := range schema.Properties {
			cond, ok := prop.RequiredWhen["llm_provider_summary_agent"]
			if !ok {
				continue
			}
			for _, p := range asStringSlice(cond) {
				if p == provider {
					fromSchema = append(fromSchema, fieldName)
				}
			}
		}

		// Compare the provider-conditional fields only (exclude the
		// intentionally-unconditional api_key field from the table side).
		var fromTable []string
		hasAPIKey := false
		for _, f := range providerRequiredSummaryFields(provider) {
			if f == apiKeySummary {
				hasAPIKey = true
				continue
			}
			fromTable = append(fromTable, f)
		}
		sort.Strings(fromSchema)
		sort.Strings(fromTable)
		assert.Equalf(t, fromSchema, fromTable,
			"providerRequiredSummaryFields(%q) conditional fields are out of sync with schema RequiredWhen", provider)

		// Assert the intentional api_key scoping deviation.
		assert.Equalf(t, apiKeyProviders[provider], hasAPIKey,
			"providerRequiredSummaryFields(%q) api_key scoping changed unexpectedly", provider)
	}
}

// TestLLMProvidersMatchSchemaEnum guards the llmProviders slice against the
// schema enum drifting apart.
func TestLLMProvidersMatchSchemaEnum(t *testing.T) {
	schema := LLM{}.ConfigSchema()
	enum := schema.Properties["llm_provider"].Enum
	got := make([]string, 0, len(enum))
	for _, e := range enum {
		if s, ok := e.(string); ok {
			got = append(got, s)
		}
	}
	want := append([]string(nil), llmProviders...)
	sort.Strings(got)
	sort.Strings(want)
	assert.Equal(t, want, got, "llmProviders is out of sync with ConfigSchema llm_provider enum")
}

// TestLLMTestConnection drives LLM.TestConnection against a fake llm-server
// to verify each branch (provider success, provider rejection, HTTP error)
// without standing up the real provider clients.
func TestLLMTestConnection(t *testing.T) {
	type llmServerResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}

	tests := []struct {
		name       string
		status     int
		body       any
		wantErrSub string // empty = expect nil
		assertBody func(t *testing.T, body map[string]any)
	}{
		{
			name:   "provider ok",
			status: http.StatusOK,
			body:   llmServerResp{OK: true},
			assertBody: func(t *testing.T, body map[string]any) {
				cfg, ok := body["config"].(map[string]any)
				assert.True(t, ok, "request must contain a config map")
				assert.Equal(t, "openai", cfg["llm_provider"])
				assert.Equal(t, "gpt-4o", cfg["llm_model_name"])
			},
		},
		{
			name:       "provider rejected",
			status:     http.StatusOK,
			body:       llmServerResp{OK: false, Error: "invalid api key"},
			wantErrSub: "invalid api key",
		},
		{
			name:       "transport error from llm-server",
			status:     http.StatusInternalServerError,
			body:       map[string]string{"error": "boom"},
			wantErrSub: "HTTP 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/llm-config/test-connection", r.URL.Path)
				assert.Equal(t, "token-xyz", r.Header.Get("X-ACTION-TOKEN"))
				if tt.assertBody != nil {
					raw, _ := io.ReadAll(r.Body)
					var parsed map[string]any
					assert.NoError(t, json.Unmarshal(raw, &parsed))
					tt.assertBody(t, parsed)
				}
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(tt.body)
			}))
			defer srv.Close()

			oldEP, oldHdr, oldTok := config.Config.LLMServerEndpoint, config.Config.LLMServerTokenHeader, config.Config.LLMServerToken
			config.Config.LLMServerEndpoint = srv.URL
			config.Config.LLMServerTokenHeader = "X-ACTION-TOKEN"
			config.Config.LLMServerToken = "token-xyz"
			defer func() {
				config.Config.LLMServerEndpoint = oldEP
				config.Config.LLMServerTokenHeader = oldHdr
				config.Config.LLMServerToken = oldTok
			}()

			err := LLM{}.TestConnection(nil, llmConfig(map[string]string{
				"llm_provider":         "openai",
				"llm_model_name":       "gpt-4o",
				"llm_provider_api_key": "sk-test",
			}), "")
			if tt.wantErrSub == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				if err != nil {
					assert.Contains(t, err.Error(), tt.wantErrSub)
				}
			}
		})
	}
}

// TestLLMTestConnection_MissingEndpoint asserts the helpful error when the
// llm-server endpoint isn't configured (deploy-time misconfiguration).
func TestLLMTestConnection_MissingEndpoint(t *testing.T) {
	old := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = ""
	defer func() { config.Config.LLMServerEndpoint = old }()

	err := LLM{}.TestConnection(nil, nil, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "llm_server_endpoint not configured")
}
