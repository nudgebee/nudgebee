package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArgoCD_Name(t *testing.T) {
	integration := ArgoCD{}
	assert.Equal(t, IntegrationArgoCD, integration.Name())
}

func TestArgoCD_Category(t *testing.T) {
	integration := ArgoCD{}
	assert.Equal(t, core.IntegrationCategoryCICD, integration.Category())
}

func TestArgoCD_ConfigSchema(t *testing.T) {
	integration := ArgoCD{}
	schema := integration.ConfigSchema()

	// k8s_secret is the only required field — server URL lives inside the secret.
	assert.Contains(t, schema.Required, ArgoCDConfigK8sSecret)
	assert.NotContains(t, schema.Required, "server")

	assert.Contains(t, schema.Properties, ArgoCDConfigK8sSecret)
	assert.NotContains(t, schema.Properties, "server")
	assert.Contains(t, schema.Properties, ArgoCDConfigTimeout)
	assert.Contains(t, schema.Properties, ArgoCDConfigInsecure)
	assert.Contains(t, schema.Properties, ArgoCDConfigGrpcWeb)

	assert.Equal(t, "30", schema.Properties[ArgoCDConfigTimeout].Default)
	assert.Equal(t, "false", schema.Properties[ArgoCDConfigInsecure].Default)
	assert.Equal(t, "true", schema.Properties[ArgoCDConfigGrpcWeb].Default)

	assert.Contains(t, schema.Properties[ArgoCDConfigInsecure].Enum, "true")
	assert.Contains(t, schema.Properties[ArgoCDConfigInsecure].Enum, "false")
}

// TestArgoCD_ValidateConfig_FormatChecks covers the validators that run before any
// relay call — they must reject malformed input deterministically so the
// "Test Connection" button gives real feedback.
func TestArgoCD_ValidateConfig_FormatChecks(t *testing.T) {
	integration := ArgoCD{}
	securityContext := &security.SecurityContext{}
	accountId := "test-account"

	tests := []struct {
		name    string
		configs []core.IntegrationConfigValue
		errMsg  string
	}{
		{
			name:    "empty config rejects with k8s_secret required",
			configs: []core.IntegrationConfigValue{},
			errMsg:  "k8s_secret is required",
		},
		{
			name: "shell-metachar secret name rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret; rm -rf /"},
			},
			errMsg: "invalid k8s_secret name",
		},
		{
			name: "invalid auth_method rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
				{Name: ArgoCDConfigAuthMethod, Value: "saml"},
			},
			errMsg: "invalid auth_method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := integration.ValidateConfig(securityContext, tt.configs, accountId)
			assert.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), tt.errMsg)
		})
	}
}

func TestArgoCD_DetectAuthError(t *testing.T) {
	cases := map[string]bool{
		`{"loggedIn":true,"username":"admin"}`:                    false,
		`FATA[0000] rpc error: code = Unauthenticated desc = ...`: true,
		`error: invalid username or password`:                     true,
		`x509: certificate signed by unknown authority`:           true,
		`dial tcp: lookup argocd.example.com: no such host`:       true,
		`'admin' logged in successfully`:                          false,
	}
	for resp, shouldFail := range cases {
		got := detectArgoCDAuthError(resp)
		if shouldFail {
			assert.NotEmpty(t, got, "expected auth error for: %s", resp)
		} else {
			assert.Empty(t, got, "expected no auth error for: %s", resp)
		}
	}
}

func TestArgoCD_IsValidSecretName(t *testing.T) {
	valid := []string{"argocd-secret", "argocd_secret", "argocd.secret", "argocd/argocd-secret", "ABC123"}
	invalid := []string{"", "argocd-secret; rm -rf /", "secret name", "ns/sec/extra", "$(whoami)", "secret`id`", "a&&b", "sec|cat"}
	for _, v := range valid {
		assert.True(t, isValidSecretName(v), "expected valid: %q", v)
	}
	for _, v := range invalid {
		assert.False(t, isValidSecretName(v), "expected invalid: %q", v)
	}
}

func TestArgoCD_Integration_Registration(t *testing.T) {
	integration, found := core.GetIntegration(IntegrationArgoCD)
	assert.True(t, found)
	assert.Equal(t, IntegrationArgoCD, integration.Name())
	assert.Equal(t, core.IntegrationCategoryCICD, integration.Category())
}
