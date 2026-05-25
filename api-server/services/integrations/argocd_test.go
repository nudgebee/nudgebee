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

	assert.Contains(t, schema.Required, ArgoCDConfigK8sSecret)
	assert.Contains(t, schema.Required, ArgoCDConfigServer)

	assert.Contains(t, schema.Properties, ArgoCDConfigK8sSecret)
	assert.Contains(t, schema.Properties, ArgoCDConfigServer)
	assert.Contains(t, schema.Properties, ArgoCDConfigTimeout)
	assert.Contains(t, schema.Properties, ArgoCDConfigInsecure)

	assert.Equal(t, "30", schema.Properties[ArgoCDConfigTimeout].Default)
	assert.Equal(t, "false", schema.Properties[ArgoCDConfigInsecure].Default)
	assert.Equal(t, argoCDServerURLPattern, schema.Properties[ArgoCDConfigServer].Pattern)

	assert.Contains(t, schema.Properties[ArgoCDConfigInsecure].Enum, "true")
	assert.Contains(t, schema.Properties[ArgoCDConfigInsecure].Enum, "false")
}

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
			name: "missing server rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
			},
			errMsg: "server is required",
		},
		{
			name: "non-URL server rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
				{Name: ArgoCDConfigServer, Value: "argocd.example.com"},
			},
			errMsg: "invalid server URL",
		},
		{
			name: "ftp scheme rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
				{Name: ArgoCDConfigServer, Value: "ftp://argocd.example.com"},
			},
			errMsg: "invalid server URL",
		},
		{
			name: "invalid server_key_in_secret rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
				{Name: ArgoCDConfigServer, Value: "https://argocd.example.com"},
				{Name: ArgoCDConfigServerKeyInSecret, Value: "1bad-key"},
			},
			errMsg: "invalid server_key_in_secret",
		},
		{
			name: "invalid auth_token_key rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
				{Name: ArgoCDConfigServer, Value: "https://argocd.example.com"},
				{Name: ArgoCDConfigAuthMethod, Value: "token"},
				{Name: ArgoCDConfigAuthTokenKeyInSecret, Value: "bad key"},
			},
			errMsg: "invalid auth_token_key_in_secret",
		},
		{
			name: "invalid username_key rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
				{Name: ArgoCDConfigServer, Value: "https://argocd.example.com"},
				{Name: ArgoCDConfigAuthMethod, Value: "password"},
				{Name: ArgoCDConfigUsernameKeyInSecret, Value: "bad-name"},
			},
			errMsg: "invalid username_key_in_secret",
		},
		{
			name: "invalid auth_method rejects",
			configs: []core.IntegrationConfigValue{
				{Name: ArgoCDConfigK8sSecret, Value: "argocd-secret"},
				{Name: ArgoCDConfigServer, Value: "https://argocd.example.com"},
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

func TestArgoCD_Integration_Registration(t *testing.T) {
	integration, found := core.GetIntegration(IntegrationArgoCD)
	assert.True(t, found)
	assert.Equal(t, IntegrationArgoCD, integration.Name())
	assert.Equal(t, core.IntegrationCategoryCICD, integration.Category())
}
