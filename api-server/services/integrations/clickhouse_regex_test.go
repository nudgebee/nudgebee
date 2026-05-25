package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClickHouse_RegexValidation(t *testing.T) {
	ch := ClickHouse{}
	sc := &security.SecurityContext{}
	accountId := "test-account"

	tests := []struct {
		name          string
		configName    string
		configValue   string
		shouldSucceed bool
	}{
		// HOST
		{"Valid Hostname", "host", "example.com", true},
		{"Valid IP", "host", "192.168.1.1", true},
		{"Valid IPv6", "host", "2001:db8::1", true}, // Now allowed
		{"Valid Host with dash", "host", "my-db.example.com", true},
		{"Invalid Host Injection", "host", "example.com; rm -rf /", false},
		{"Invalid Host Space", "host", "example .com", false},
		{"Invalid Host Pipe", "host", "example.com|", false},

		// PORT
		{"Valid Port", "port", "9000", true},
		{"Invalid Port Non-numeric", "port", "9000a", false},
		{"Invalid Port Injection", "port", "9000; ls", false},

		// DATABASE
		{"Valid Database", "database", "my_db", true},
		{"Valid Database with dash", "database", "my-db-1", true},
		{"Invalid Database Injection", "database", "db; drop table", false},
		{"Invalid Database Space", "database", "db name", false},

		// KEYS
		{"Valid Secret Key", "secret_user_key", "MY_USER_KEY", true},
		{"Valid Secret Key 2", "secret_user_key", "key123", true},
		{"Invalid Secret Key Hyphen", "secret_user_key", "my-key", false}, // keys are env vars usually, underscores allowed
		{"Invalid Secret Key Injection", "secret_user_key", "KEY; echo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configs := []core.IntegrationConfigValue{
				{Name: "k8s_secret", Value: "valid-secret"},
				{Name: tt.configName, Value: tt.configValue},
			}

			// Fill required fields if testing optional ones
			if tt.configName != "host" {
				configs = append(configs, core.IntegrationConfigValue{Name: "host", Value: "valid.host"})
			}

			errs := ch.ValidateConfig(sc, configs, accountId)

			if tt.shouldSucceed {
				// Success means no validation error found.
				// It WILL fail with "connection refused" or similar in relay,
				// but shouldn't fail with "invalid format".
				for _, err := range errs {
					if err != nil {
						assert.NotContains(t, err.Error(), "invalid", "Should not have validation error for valid input")
					}
				}
			} else {
				// Failure means we expect "invalid ... format"
				found := false
				for _, err := range errs {
					if err != nil && assert.Contains(t, err.Error(), "invalid") && assert.Contains(t, err.Error(), "format") {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected validation error for invalid input: %s", tt.configValue)
			}
		})
	}
}
