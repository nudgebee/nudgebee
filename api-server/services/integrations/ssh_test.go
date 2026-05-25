package integrations

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTools_ExecutSSHCommand(t *testing.T) {
	ssh := SSH{}
	playbookResponse, err := ssh.Execute(playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{}), map[string]any{
		"command":          "uname -a",
		"integration_name": "nb-dev-db",
		"account_id":       os.Getenv("TEST_ACCOUNT"),
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, playbookResponse.GetData())
}

func TestSSH_ConfigSchema_HostPattern(t *testing.T) {
	schema := SSH{}.ConfigSchema()
	hostProp, ok := schema.Properties["host"]
	assert.True(t, ok, "host property must exist on schema")
	assert.Equal(t, sshHostPattern, hostProp.Pattern, "host property must expose hostname pattern for client-side validation")
}

func TestSSH_HostRegex(t *testing.T) {
	cases := []struct {
		host  string
		valid bool
	}{
		{"db.example.com", true},
		{"sub.host-1.example.com", true},
		{"localhost", true},
		{"10.0.0.5", true},
		{"192.168.1.1", true},
		{"", false},
		{"host name", false},
		{"host;rm -rf /", false},
		{"$(whoami)", false},
		{"`id`", false},
		{"host..example.com", false},
		{"-leading-dash.com", false},
	}
	for _, c := range cases {
		got := sshHostRegex.MatchString(c.host)
		assert.Equal(t, c.valid, got, "host=%q expected valid=%v got=%v", c.host, c.valid, got)
	}
}

func TestSSH_ValidateConfig_K8sHostFormat(t *testing.T) {
	ssh := SSH{}
	sc := &security.SecurityContext{}

	tests := []struct {
		name    string
		configs []core.IntegrationConfigValue
		errMsg  string
	}{
		{
			name: "empty host rejects",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
				{Name: "host", Value: ""},
			},
			errMsg: "host is required",
		},
		{
			name: "whitespace-only host rejects",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
				{Name: "host", Value: "   "},
			},
			errMsg: "host is required",
		},
		{
			name: "shell metacharacters reject",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
				{Name: "host", Value: "host;rm -rf /"},
			},
			errMsg: "invalid host",
		},
		{
			name: "spaces reject",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
				{Name: "host", Value: "junk host"},
			},
			errMsg: "invalid host",
		},
		{
			name: "command substitution rejects",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
				{Name: "host", Value: "$(whoami)"},
			},
			errMsg: "invalid host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ssh.ValidateConfig(sc, tt.configs, "test-account")
			assert.NotEmpty(t, errs)
			assert.Contains(t, errs[0].Error(), tt.errMsg)
		})
	}
}
