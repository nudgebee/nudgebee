package integrations

import (
	"nudgebee/services/integrations/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTekton_Name(t *testing.T) {
	integration := Tekton{}
	assert.Equal(t, IntegrationTekton, integration.Name())
}

func TestTekton_Category(t *testing.T) {
	integration := Tekton{}
	assert.Equal(t, core.IntegrationCategoryCICD, integration.Category())
}

func TestTekton_ConfigSchema(t *testing.T) {
	integration := Tekton{}
	schema := integration.ConfigSchema()

	assert.Empty(t, schema.Required)
	assert.True(t, schema.Testable)

	assert.Contains(t, schema.Properties, "integration_config_name")
	assert.Contains(t, schema.Properties, "account_id")
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "timeout")

	assert.Equal(t, "", schema.Properties["namespace"].Default)
	assert.Equal(t, "30", schema.Properties["timeout"].Default)
}

func TestTekton_Integration_Registration(t *testing.T) {
	integration, found := core.GetIntegration(IntegrationTekton)
	assert.True(t, found)
	assert.Equal(t, IntegrationTekton, integration.Name())
	assert.Equal(t, core.IntegrationCategoryCICD, integration.Category())
}

func TestTekton_ValidateConfig_InvalidNamespace(t *testing.T) {
	integration := Tekton{}
	tests := []struct {
		name string
		ns   string
	}{
		{"shell injection", "default; rm -rf /"},
		{"uppercase", "MyNamespace"},
		{"spaces", "my namespace"},
		{"too long", "a23456789012345678901234567890123456789012345678901234567890abcd"},
		{"special chars", "ns$(whoami)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configs := []core.IntegrationConfigValue{
				{Name: "namespace", Value: tt.ns},
			}
			errs := integration.ValidateConfig(nil, configs, "test-account")
			assert.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), "invalid namespace")
		})
	}
}

func TestTekton_ValidateConfig_ValidNamespace(t *testing.T) {
	valid := []string{"tekton-pipelines", "default", "my-ns-01", "a"}
	for _, ns := range valid {
		assert.True(t, k8sNamespaceRegex.MatchString(ns) && len(ns) <= 63, "expected valid: %q", ns)
	}
}

func TestTekton_DetectNotInstalled(t *testing.T) {
	cases := map[string]bool{
		"tkn: command not found":                                true,
		"executable file not found in $PATH":                    true,
		"the server doesn't have a resource type \"pipelines\"": true,
		"Pipeline version: v0.62.0\nPipelines version: v0.62.0": false,
		"Client version: 0.45.0\nPipeline version: v0.62.0":     false,
		"error: unable to resolve Tekton Pipelines":             true,
	}
	for input, expected := range cases {
		got := detectTektonNotInstalled(input)
		if expected {
			assert.True(t, got, "expected not-installed for: %s", input)
		} else {
			assert.False(t, got, "expected installed for: %s", input)
		}
	}
}

func TestTekton_DetectError(t *testing.T) {
	cases := map[string]bool{
		"Pipeline version: v0.62.0":                   false,
		"error: forbidden: User cannot list resource": true,
		"failed: unable to connect to cluster":        true,
		"permission denied":                           true,
		"dial tcp: connection refused":                true,
		"FATA[0000] failed to connect":                true,
		"no such host":                                true,
		"Client version: 0.45.0":                      false,
	}
	for input, shouldFail := range cases {
		got := detectTektonError(input)
		if shouldFail {
			assert.NotEmpty(t, got, "expected error for: %s", input)
		} else {
			assert.Empty(t, got, "expected no error for: %s", input)
		}
	}
}
