package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGitlab_Name(t *testing.T) {
	integration := Gitlab{}
	assert.Equal(t, IntegrationGitlab, integration.Name())
	assert.Equal(t, "gitlab", integration.Name())
}

func TestGitlab_Category(t *testing.T) {
	integration := Gitlab{}
	assert.Equal(t, core.IntegrationCategoryTicketing, integration.Category())
}

func TestGitlab_ConfigSchema(t *testing.T) {
	integration := Gitlab{}
	schema := integration.ConfigSchema()

	// Test schema type
	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)

	// Test required fields
	assert.Contains(t, schema.Required, GitlabConfigUsername)
	assert.Contains(t, schema.Required, GitlabConfigPassword)
	assert.Len(t, schema.Required, 2)

	// Test all properties exist
	assert.Contains(t, schema.Properties, GitlabConfigUrl)
	assert.Contains(t, schema.Properties, GitlabConfigUsername)
	assert.Contains(t, schema.Properties, GitlabConfigPassword)
	assert.Len(t, schema.Properties, 3)

	// Test default values
	assert.Equal(t, "https://gitlab.com", schema.Properties[GitlabConfigUrl].Default)

	// Test encryption flag for password
	assert.True(t, schema.Properties[GitlabConfigPassword].IsEncrypted)
	assert.False(t, schema.Properties[GitlabConfigUsername].IsEncrypted)
	assert.False(t, schema.Properties[GitlabConfigUrl].IsEncrypted)

	// Test property types
	assert.Equal(t, core.ToolSchemaTypeString, schema.Properties[GitlabConfigUrl].Type)
	assert.Equal(t, core.ToolSchemaTypeString, schema.Properties[GitlabConfigUsername].Type)
	assert.Equal(t, core.ToolSchemaTypeString, schema.Properties[GitlabConfigPassword].Type)

	// Test descriptions are not empty
	assert.NotEmpty(t, schema.Properties[GitlabConfigUrl].Description)
	assert.NotEmpty(t, schema.Properties[GitlabConfigUsername].Description)
	assert.NotEmpty(t, schema.Properties[GitlabConfigPassword].Description)
}

func TestGitlab_ValidateConfig_MissingUsername(t *testing.T) {
	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigPassword,
			Value: "test-token",
		},
	}

	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Len(t, errors, 1)
	assert.Contains(t, errors[0].Error(), "gitlab username is required")
}

func TestGitlab_ValidateConfig_MissingPassword(t *testing.T) {
	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigUsername,
			Value: "testuser",
		},
	}

	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Len(t, errors, 1)
	assert.Contains(t, errors[0].Error(), "gitlab personal access token is required")
}

func TestGitlab_ValidateConfig_EmptyValues(t *testing.T) {
	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	// Test with completely empty config
	errors := integration.ValidateConfig(securityContext, []core.IntegrationConfigValue{}, "")
	assert.Len(t, errors, 1)
	assert.Contains(t, errors[0].Error(), "gitlab username is required")
}

func TestGitlab_ValidateConfig_EmptyUsername(t *testing.T) {
	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigUsername,
			Value: "",
		},
		{
			Name:  GitlabConfigPassword,
			Value: "test-token",
		},
	}

	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Len(t, errors, 1)
	assert.Contains(t, errors[0].Error(), "gitlab username is required")
}

func TestGitlab_ValidateConfig_EmptyPassword(t *testing.T) {
	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigUsername,
			Value: "testuser",
		},
		{
			Name:  GitlabConfigPassword,
			Value: "",
		},
	}

	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Len(t, errors, 1)
	assert.Contains(t, errors[0].Error(), "gitlab personal access token is required")
}

func TestGitlab_ValidateConfig_InvalidToken(t *testing.T) {
	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigUsername,
			Value: "testuser",
		},
		{
			Name:  GitlabConfigPassword,
			Value: "invalid-token",
		},
	}

	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Len(t, errors, 1)
	assert.Contains(t, errors[0].Error(), "gitlab authentication failed")
}

func TestGitlab_ValidateConfig_CustomUrl(t *testing.T) {
	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigUrl,
			Value: "https://gitlab.example.com",
		},
		{
			Name:  GitlabConfigUsername,
			Value: "testuser",
		},
		{
			Name:  GitlabConfigPassword,
			Value: "invalid-token",
		},
	}

	// Should fail with auth error (not URL error), confirming custom URL is used
	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Len(t, errors, 1)
	// The error should be about authentication or connection, not about missing fields
	assert.True(t,
		strings.Contains(errors[0].Error(), "authentication failed") ||
			strings.Contains(errors[0].Error(), "failed to create gitlab client"),
		"Expected authentication or client creation error, got: %s", errors[0].Error())
}

func TestGitlab_Integration_Registration(t *testing.T) {
	// Test that the integration is registered correctly
	integration, found := core.GetIntegration(IntegrationGitlab)
	assert.True(t, found)
	assert.NotNil(t, integration)
	assert.Equal(t, IntegrationGitlab, integration.Name())
	assert.Equal(t, core.IntegrationCategoryTicketing, integration.Category())
}

func TestGitlab_Integration_Registration_CaseInsensitive(t *testing.T) {
	// Test case-insensitive lookup
	integration1, found1 := core.GetIntegration("gitlab")
	integration2, found2 := core.GetIntegration("GITLAB")
	integration3, found3 := core.GetIntegration("GitLab")

	assert.True(t, found1)
	assert.True(t, found2)
	assert.True(t, found3)
	assert.Equal(t, integration1.Name(), integration2.Name())
	assert.Equal(t, integration2.Name(), integration3.Name())
}

// TestGitlab_ValidateConfig_RealCredentials tests with real GitLab credentials
// This test is skipped unless GITLAB_TEST_TOKEN and GITLAB_TEST_USERNAME env vars are set
func TestGitlab_ValidateConfig_RealCredentials(t *testing.T) {
	token := os.Getenv("GITLAB_TEST_TOKEN")
	username := os.Getenv("GITLAB_TEST_USERNAME")
	url := os.Getenv("GITLAB_TEST_URL")

	if token == "" || username == "" {
		t.Skip("Skipping real GitLab API test: GITLAB_TEST_TOKEN and GITLAB_TEST_USERNAME not set")
	}

	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigUsername,
			Value: username,
		},
		{
			Name:  GitlabConfigPassword,
			Value: token,
		},
	}

	if url != "" {
		configValues = append(configValues, core.IntegrationConfigValue{
			Name:  GitlabConfigUrl,
			Value: url,
		})
	}

	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Empty(t, errors, "Expected no errors with valid credentials")
}

// TestGitlab_ValidateConfig_UsernameMismatch tests that username mismatch is detected
func TestGitlab_ValidateConfig_UsernameMismatch(t *testing.T) {
	token := os.Getenv("GITLAB_TEST_TOKEN")
	username := os.Getenv("GITLAB_TEST_USERNAME")
	if token == "" || username == "" {
		t.Skip("Skipping username mismatch test: GITLAB_TEST_TOKEN and GITLAB_TEST_USERNAME not set")
	}

	integration := Gitlab{}
	securityContext := &security.SecurityContext{}

	// Use a wrong username with valid token
	configValues := []core.IntegrationConfigValue{
		{
			Name:  GitlabConfigUsername,
			Value: "wrong-username-that-does-not-match",
		},
		{
			Name:  GitlabConfigPassword,
			Value: token,
		},
	}

	errors := integration.ValidateConfig(securityContext, configValues, "")
	assert.Len(t, errors, 1)
	assert.Contains(t, errors[0].Error(), "gitlab username mismatch")
}
