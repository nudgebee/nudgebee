package integrations

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func TestLoggly_Name(t *testing.T) {
	assert.Equal(t, IntegrationLoggly, Loggly{}.Name())
}

func TestLoggly_Category(t *testing.T) {
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, Loggly{}.Category())
}

func TestLoggly_ConfigSchema(t *testing.T) {
	schema := Loggly{}.ConfigSchema()

	assert.True(t, schema.Testable, "schema should be testable")

	assert.Contains(t, schema.Required, LogglySubdomain)
	assert.Contains(t, schema.Required, LogglyApiToken)

	assert.Contains(t, schema.Properties, LogglySubdomain)
	assert.Contains(t, schema.Properties, LogglyApiToken)
	assert.Contains(t, schema.Properties, core.AccountId)
	assert.Contains(t, schema.Properties, core.DefaultLogProvider)
	assert.Contains(t, schema.Properties, core.IntegrationConfigName)

	assert.True(t, schema.Properties[LogglyApiToken].IsEncrypted, "api_token should be encrypted")
	assert.False(t, schema.Properties[LogglySubdomain].IsEncrypted, "subdomain should not be encrypted")
}

func TestLoggly_Validate_SubdomainFormat(t *testing.T) {
	m := &Loggly{}

	invalid := []string{
		"https://foo.loggly.com",
		"foo.loggly.com",
		"foo/bar",
		"-foo",
		"foo-",
		"foo bar",
	}
	for _, sub := range invalid {
		err := m.ValidateLogglyConfig(LogglyConfig{Subdomain: sub, ApiToken: "t"})
		require.Error(t, err, "expected format error for subdomain=%q", sub)
		assert.Contains(t, err.Error(), "subdomain must be the prefix only", "subdomain=%q", sub)
	}
}

func TestLoggly_Validate_EmptySubdomain(t *testing.T) {
	m := &Loggly{}
	err := m.ValidateLogglyConfig(LogglyConfig{Subdomain: "", ApiToken: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subdomain must not be empty")
}

func TestLoggly_Validate_EmptyToken(t *testing.T) {
	m := &Loggly{}
	// Valid subdomain format so we pass the format check, then hit the empty-token guard.
	err := m.ValidateLogglyConfig(LogglyConfig{Subdomain: "foo", ApiToken: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api token must not be empty")
}

// setTestEncryptionKey seeds config.Config with a deterministic AES-256 hex key
// so common.Encrypt / common.Decrypt round-trip works under unit tests. Restores
// the prior value on cleanup.
func setTestEncryptionKey(t *testing.T) {
	t.Helper()
	prev := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.Config.NudgebeeEncryptionKey = prev })
}

func TestGetLogglyConfigs_DecryptsApiToken(t *testing.T) {
	setTestEncryptionKey(t)

	plaintext := "secret-loggly-token-xyz"
	ciphertext, err := common.Encrypt(plaintext)
	require.NoError(t, err)
	require.NotEqual(t, plaintext, ciphertext, "encrypt must mutate the value")

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(core.ListIntegrationConfigs, func(_ *security.RequestContext, _ string, _ string) ([]core.IntegrationDto, error) {
		return []core.IntegrationDto{
			{
				Name: IntegrationLoggly,
				Configs: []core.IntegrationConfigValue{
					{Name: LogglySubdomain, Value: "pollux", IsEncrypted: false},
					{Name: LogglyApiToken, Value: ciphertext, IsEncrypted: true},
				},
			},
		}, nil
	})

	cfg, err := GetLogglyConfigs(nil, "acc-1")
	require.NoError(t, err)
	assert.Equal(t, "pollux", cfg.Subdomain)
	assert.Equal(t, plaintext, cfg.ApiToken, "api token must be decrypted for callers")
}

func TestGetLogglyConfigs_BadCiphertextReturnsError(t *testing.T) {
	setTestEncryptionKey(t)

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(core.ListIntegrationConfigs, func(_ *security.RequestContext, _ string, _ string) ([]core.IntegrationDto, error) {
		return []core.IntegrationDto{
			{
				Name: IntegrationLoggly,
				Configs: []core.IntegrationConfigValue{
					{Name: LogglySubdomain, Value: "pollux", IsEncrypted: false},
					{Name: LogglyApiToken, Value: "not-hex-not-valid", IsEncrypted: true},
				},
			},
		}, nil
	})

	_, err := GetLogglyConfigs(nil, "acc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decrypt loggly api token")
}

func TestGetLogglyConfigs_UnencryptedTokenPassesThrough(t *testing.T) {
	// Back-compat: if IsEncrypted is false, callers must get the raw value as-is.
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(core.ListIntegrationConfigs, func(_ *security.RequestContext, _ string, _ string) ([]core.IntegrationDto, error) {
		return []core.IntegrationDto{
			{
				Name: IntegrationLoggly,
				Configs: []core.IntegrationConfigValue{
					{Name: LogglySubdomain, Value: "pollux", IsEncrypted: false},
					{Name: LogglyApiToken, Value: "plaintext-token", IsEncrypted: false},
				},
			},
		}, nil
	})

	cfg, err := GetLogglyConfigs(nil, "acc-1")
	require.NoError(t, err)
	assert.Equal(t, "plaintext-token", cfg.ApiToken)
}
