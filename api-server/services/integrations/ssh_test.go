package integrations

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTools_ExecutSSHCommand(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
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

func TestSSH_UserRegex(t *testing.T) {
	cases := []struct {
		user  string
		valid bool
	}{
		{"admin", true},
		{"ec2-user", true},
		{"ubuntu", true},
		{"root", true},
		{"user.name", true},
		{"_svc", true},
		{"", false},
		{"1user", false},
		{"-flag", false},
		{"root user", false},
		{"admin;ls", false},
		{"$(id)", false},
		{"`whoami`", false},
	}
	for _, c := range cases {
		got := sshUserRegex.MatchString(c.user)
		assert.Equal(t, c.valid, got, "user=%q expected valid=%v got=%v", c.user, c.valid, got)
	}
}

// TestResolveSSHTarget pins the user@host resolver. The function feeds a
// shell template downstream, so any input that passes here will be
// interpolated raw — meaning this is also the executor's command-injection
// guard for params.HostName / params.UserName flowing from playbook YAML
// or LLM tool args.
func TestResolveSSHTarget(t *testing.T) {
	t.Run("no configs no params returns env-var defaults", func(t *testing.T) {
		u, h, err := resolveSSHTarget(nil, sshParams{})
		assert.NoError(t, err)
		assert.Equal(t, "$SSH_USER", u)
		assert.Equal(t, "$SSH_HOST", h)
	})

	t.Run("saved host used as tier 2", func(t *testing.T) {
		configs := []core.IntegrationConfigValue{{Name: "host", Value: "saved.example.com"}}
		u, h, err := resolveSSHTarget(configs, sshParams{})
		assert.NoError(t, err)
		assert.Equal(t, "$SSH_USER", u)
		assert.Equal(t, "saved.example.com", h)
	})

	t.Run("saved username used as tier 2", func(t *testing.T) {
		configs := []core.IntegrationConfigValue{{Name: "username", Value: "ec2-user"}}
		u, h, err := resolveSSHTarget(configs, sshParams{})
		assert.NoError(t, err)
		assert.Equal(t, "ec2-user", u)
		assert.Equal(t, "$SSH_HOST", h)
	})

	t.Run("per-call params beat saved configs", func(t *testing.T) {
		configs := []core.IntegrationConfigValue{
			{Name: "host", Value: "saved.example.com"},
			{Name: "username", Value: "ec2-user"},
		}
		u, h, err := resolveSSHTarget(configs, sshParams{HostName: "1.2.3.4", UserName: "admin"})
		assert.NoError(t, err)
		assert.Equal(t, "admin", u)
		assert.Equal(t, "1.2.3.4", h)
	})

	t.Run("malformed per-call host rejected", func(t *testing.T) {
		_, _, err := resolveSSHTarget(nil, sshParams{HostName: "1.2.3.4; rm -rf /"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid host_name")
	})

	t.Run("malformed per-call user rejected", func(t *testing.T) {
		_, _, err := resolveSSHTarget(nil, sshParams{UserName: "admin;ls"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid user_name")
	})

	t.Run("malformed saved host rejected (defense in depth)", func(t *testing.T) {
		configs := []core.IntegrationConfigValue{{Name: "host", Value: "`whoami`"}}
		_, _, err := resolveSSHTarget(configs, sshParams{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid saved host")
	})

	t.Run("malformed saved username rejected (defense in depth)", func(t *testing.T) {
		configs := []core.IntegrationConfigValue{{Name: "username", Value: "root;ls"}}
		_, _, err := resolveSSHTarget(configs, sshParams{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid saved username")
	})

	t.Run("shell metachar host_name with valid saved fallback still rejected", func(t *testing.T) {
		// Per-call value must be vetted even when a perfectly-good saved
		// host is available — the per-call value is what would actually be
		// substituted into the command if validation didn't catch it.
		configs := []core.IntegrationConfigValue{{Name: "host", Value: "saved.example.com"}}
		_, _, err := resolveSSHTarget(configs, sshParams{HostName: "$(id)"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid host_name")
	})
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

func TestSSH_ValidateConfig_K8sEmptyHostAccepted(t *testing.T) {
	// An integration can be saved with no default host: the host will be
	// supplied per-call (e.g. by an LLM tool for an ephemeral VM). Both an
	// unset value and a whitespace-only value should pass validation without
	// triggering the live uname -a probe.
	ssh := SSH{}
	sc := &security.SecurityContext{}

	cases := []struct {
		name    string
		configs []core.IntegrationConfigValue
	}{
		{
			name: "host omitted entirely",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
			},
		},
		{
			name: "host explicitly empty",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
				{Name: "host", Value: ""},
			},
		},
		{
			name: "host is whitespace only",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "ssh-secret"},
				{Name: "host", Value: "   "},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := ssh.ValidateConfig(sc, c.configs, "test-account")
			assert.Empty(t, errs, "empty host should be accepted, got: %v", errs)
		})
	}
}

func TestSSH_ValidateConfig_VMAgentNoUsernameAccepted(t *testing.T) {
	// vm_agent + cloud_push no longer requires username at save time;
	// callers supply user_name per command alongside host_name.
	ssh := SSH{}
	sc := &security.SecurityContext{}

	configs := []core.IntegrationConfigValue{
		{Name: "connection_mode", Value: "vm_agent"},
		{Name: "credential_source", Value: "cloud_push"},
		{Name: "private_key", Value: "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----"},
	}
	errs := ssh.ValidateConfig(sc, configs, "test-account")
	assert.Empty(t, errs, "username should be optional in vm_agent+cloud_push, got: %v", errs)
}

func TestSSH_ValidateConfig_K8sRequiresK8sSecret(t *testing.T) {
	// k8s mode without a k8s_secret reference is invalid even when host is
	// empty (the executor has no credentials to use). This is the
	// defense-in-depth backstop for callers that bypass the form-level
	// RequiredWhen check.
	ssh := SSH{}
	sc := &security.SecurityContext{}

	cases := []struct {
		name    string
		configs []core.IntegrationConfigValue
	}{
		{
			name: "k8s mode with no k8s_secret and no host",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
			},
		},
		{
			name: "k8s mode with empty k8s_secret",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: ""},
			},
		},
		{
			name: "k8s mode with whitespace-only k8s_secret",
			configs: []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "k8s"},
				{Name: "k8s_secret", Value: "   "},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := ssh.ValidateConfig(sc, c.configs, "test-account")
			assert.NotEmpty(t, errs, "missing k8s_secret must be rejected")
			assert.Contains(t, errs[0].Error(), "k8s_secret is required")
		})
	}
}

func TestSSH_ValidateConfig_VMAgentRejectsMalformedUsername(t *testing.T) {
	// When username IS supplied, its format must be vetted so a malformed
	// default can't smuggle shell metacharacters through to the executor.
	ssh := SSH{}
	sc := &security.SecurityContext{}

	badUsers := []string{"admin;ls", "root user", "$(id)", "`whoami`", "-flag"}
	for _, u := range badUsers {
		t.Run(u, func(t *testing.T) {
			configs := []core.IntegrationConfigValue{
				{Name: "connection_mode", Value: "vm_agent"},
				{Name: "credential_source", Value: "cloud_push"},
				{Name: "username", Value: u},
				{Name: "private_key", Value: "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----"},
			}
			errs := ssh.ValidateConfig(sc, configs, "test-account")
			assert.NotEmpty(t, errs, "malformed username %q must be rejected", u)
			assert.Contains(t, errs[0].Error(), "invalid username")
		})
	}
}

func TestSSH_ValidateConfig_VMAgentStillRequiresCredential(t *testing.T) {
	// Dropping the username requirement must NOT regress the requirement
	// that some authentication material be present.
	ssh := SSH{}
	sc := &security.SecurityContext{}

	configs := []core.IntegrationConfigValue{
		{Name: "connection_mode", Value: "vm_agent"},
		{Name: "credential_source", Value: "cloud_push"},
		// no password, no private_key
	}
	errs := ssh.ValidateConfig(sc, configs, "test-account")
	assert.NotEmpty(t, errs, "missing both password and private_key must still fail")
	assert.Contains(t, errs[0].Error(), "password or private_key")
}
