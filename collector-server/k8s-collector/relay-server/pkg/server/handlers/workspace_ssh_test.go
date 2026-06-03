package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/relay-server/pkg/db"
)

// TestBuildWorkspaceAction_SSH_DefaultsToSecretEnvVars verifies the legacy
// behavior: when no host_name / user_name is supplied in the action args,
// the generated command falls back to the env vars from the mounted k8s
// secret (SSH_USER / SSH_HOST). This guards against regressions in the
// override path.
func TestBuildWorkspaceAction_SSH_DefaultsToSecretEnvVars(t *testing.T) {
	action, params, err := buildWorkspaceAction("ssh", "df -h", nil, map[string]any{}, "shell:latest")
	assert.NoError(t, err)
	assert.Equal(t, "pod_script_run_enricher", action)
	cmd, _ := params["command"].(string)
	assert.Contains(t, cmd, "$SSH_USER@$SSH_HOST", "default path must use secret env vars")
	assert.Contains(t, cmd, `'df -h'`, "user command must be single-quoted before transmission")
}

// TestSshShellQuote pins the quoting helper. The values it emits feed into
// `ssh user@host <quoted>` — if any of these cases regress, the workspace
// pod's local shell will re-parse the user's command before transmission
// and silently mangle field references, command substitutions, etc.
func TestSshShellQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "''"},
		{"plain", "uname -a", "'uname -a'"},
		// awk field reference — the bug we observed in the wild
		{"awk_field_ref", `awk '{print $1}' /tmp/log`, `'awk '\''{print $1}'\'' /tmp/log'`},
		// command substitution — was running locally and tripping `sudo: not found`
		{"command_substitution", `grep "$(cat /tmp/x)" /tmp/log`, `'grep "$(cat /tmp/x)" /tmp/log'`},
		// positional / variable expansion
		{"dollar_var", "echo $HOME", "'echo $HOME'"},
		{"backticks", "echo `whoami`", "'echo `whoami`'"},
		// embedded single quote
		{"single_quote_inside", `echo 'hello'`, `'echo '\''hello'\'''`},
		// double quotes inside (pass through)
		{"double_quotes_inside", `echo "hello"`, `'echo "hello"'`},
		// newlines + tabs (passed through; ssh handles them in single quotes)
		{"newline", "echo one\necho two", "'echo one\necho two'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, sshShellQuote(c.in))
		})
	}
}

// TestBuildWorkspaceAction_SSH_AwkFieldRefSurvives is the regression test
// for the exact failure the user observed: `awk '{print $1}'` was being
// delivered to the remote host as `awk '{print }'` because the workspace
// pod's shell expanded `$1` (positional param, empty) before ssh.
func TestBuildWorkspaceAction_SSH_AwkFieldRefSurvives(t *testing.T) {
	args := map[string]any{"host_name": "1.2.3.4", "user_name": "admin"}
	in := `awk '{print $1}' /home/admin/access.log | sort | uniq -c | sort -nr | head -n 1 | awk '{print $2}'`
	_, params, err := buildWorkspaceAction("ssh", in, nil, args, "shell:latest")
	assert.NoError(t, err)
	cmd := params["command"].(string)
	// The user's command must reach the remote shell with $1 and $2 intact —
	// they must be inside single quotes (so the local pod shell doesn't
	// expand them) and the inner `'` must be broken out with `'\''`.
	assert.Contains(t, cmd, `'\''{print $1}'\''`, "awk $1 reference must survive")
	assert.Contains(t, cmd, `'\''{print $2}'\''`, "awk $2 reference must survive")
}

// TestBuildWorkspaceAction_SSH_CommandSubstitutionSurvives is the other
// half of the user-trace failure: `$(...)` was being evaluated locally,
// leading to "sudo: not found" because the pod image doesn't have sudo
// even though the remote host does.
func TestBuildWorkspaceAction_SSH_CommandSubstitutionSurvives(t *testing.T) {
	args := map[string]any{"host_name": "1.2.3.4", "user_name": "admin"}
	in := `grep -c -F "$(cat /home/admin/highestip.txt)" /home/admin/access.log`
	_, params, err := buildWorkspaceAction("ssh", in, nil, args, "shell:latest")
	assert.NoError(t, err)
	cmd := params["command"].(string)
	// $(...) must appear once, inside the single-quoted user command,
	// with no escape — so the REMOTE shell does the substitution.
	assert.Contains(t, cmd, `'grep -c -F "$(cat /home/admin/highestip.txt)" /home/admin/access.log'`)
}

// TestBuildWorkspaceAction_SSH_ConnectTimeoutSet pins the ConnectTimeout
// flag so unreachable-host failures (wrong IP, port firewalled) surface
// in 10s instead of hitting the 60s pod-execution timeout that looks
// like a hang to the LLM and triggers expensive retry loops.
func TestBuildWorkspaceAction_SSH_ConnectTimeoutSet(t *testing.T) {
	_, params, err := buildWorkspaceAction("ssh", "uptime", nil, map[string]any{}, "shell:latest")
	assert.NoError(t, err)
	assert.Contains(t, params["command"].(string), "-o ConnectTimeout=10")
}

// TestBuildWorkspaceAction_SSH_HostOverride confirms that a caller-supplied
// host_name replaces the SSH_HOST env-var reference but leaves the user side
// alone when user_name isn't provided.
func TestBuildWorkspaceAction_SSH_HostOverride(t *testing.T) {
	args := map[string]any{"host_name": "1.2.3.4"}
	_, params, err := buildWorkspaceAction("ssh", "uptime", nil, args, "shell:latest")
	assert.NoError(t, err)
	cmd := params["command"].(string)
	assert.Contains(t, cmd, "$SSH_USER@1.2.3.4")
	assert.NotContains(t, cmd, "$SSH_HOST", "SSH_HOST must be replaced when host_name is supplied")
}

// TestBuildWorkspaceAction_SSH_UserAndHostOverride confirms both overrides
// land at the same time.
func TestBuildWorkspaceAction_SSH_UserAndHostOverride(t *testing.T) {
	args := map[string]any{"host_name": "vm.example.com", "user_name": "admin"}
	_, params, err := buildWorkspaceAction("ssh", "ls", nil, args, "shell:latest")
	assert.NoError(t, err)
	cmd := params["command"].(string)
	assert.Contains(t, cmd, "admin@vm.example.com")
	assert.NotContains(t, cmd, "$SSH_USER")
	assert.NotContains(t, cmd, "$SSH_HOST")
}

// TestBuildWorkspaceAction_SSH_RejectsShellMetacharsInHost is the security
// regression guard. host_name is interpolated into a shell command; any
// metacharacter must cause the builder to reject the action rather than
// silently producing an injectable command line.
func TestBuildWorkspaceAction_SSH_RejectsShellMetacharsInHost(t *testing.T) {
	badHosts := []string{
		"1.2.3.4; rm -rf /",
		"`whoami`",
		"$(id)",
		"host name",
		"host\nrm -rf /",
		"-leading-dash",
	}
	for _, h := range badHosts {
		t.Run(h, func(t *testing.T) {
			_, _, err := buildWorkspaceAction("ssh", "uptime", nil, map[string]any{"host_name": h}, "shell:latest")
			assert.Error(t, err, "host_name %q must be rejected", h)
			assert.True(t, strings.Contains(err.Error(), "invalid host_name"), "unexpected error: %v", err)
		})
	}
}

// TestBuildWorkspaceAction_SSH_RejectsShellMetacharsInUser is the matching
// guard for user_name.
func TestBuildWorkspaceAction_SSH_RejectsShellMetacharsInUser(t *testing.T) {
	badUsers := []string{
		"admin;ls",
		"root user",
		"$(id)",
		"-flag",
		"",
	}
	for _, u := range badUsers {
		t.Run(u, func(t *testing.T) {
			args := map[string]any{"host_name": "1.2.3.4", "user_name": u}
			_, _, err := buildWorkspaceAction("ssh", "uptime", nil, args, "shell:latest")
			if u == "" {
				// Empty user_name is treated as "not provided" — falls back to $SSH_USER.
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err, "user_name %q must be rejected", u)
			assert.True(t, strings.Contains(err.Error(), "invalid user_name"), "unexpected error: %v", err)
		})
	}
}

// TestBuildWorkspaceAction_SSH_FallsBackToSavedHost confirms tier-2 of the
// documented resolution order: when no per-call host_name is supplied but
// the integration has a saved `host` config value, that value is used in
// the generated command (in place of the SSH_HOST env-var fallback).
func TestBuildWorkspaceAction_SSH_FallsBackToSavedHost(t *testing.T) {
	configs := []db.WorkspaceConfigValue{{Name: "host", Value: "saved.example.com"}}
	_, params, err := buildWorkspaceAction("ssh", "uptime", configs, map[string]any{}, "shell:latest")
	assert.NoError(t, err)
	cmd := params["command"].(string)
	assert.Contains(t, cmd, "$SSH_USER@saved.example.com")
	assert.NotContains(t, cmd, "$SSH_HOST", "saved host must replace SSH_HOST env-var fallback")
}

// TestBuildWorkspaceAction_SSH_FallsBackToSavedUser is the user-side mirror
// of the saved-host test.
func TestBuildWorkspaceAction_SSH_FallsBackToSavedUser(t *testing.T) {
	configs := []db.WorkspaceConfigValue{{Name: "username", Value: "ec2-user"}}
	_, params, err := buildWorkspaceAction("ssh", "uptime", configs, map[string]any{}, "shell:latest")
	assert.NoError(t, err)
	cmd := params["command"].(string)
	assert.Contains(t, cmd, "ec2-user@$SSH_HOST")
	assert.NotContains(t, cmd, "$SSH_USER", "saved username must replace SSH_USER env-var fallback")
}

// TestBuildWorkspaceAction_SSH_PerCallOverridesWinOverSavedConfig pins the
// precedence rule: per-call args (tier 1) > integration config (tier 2).
// A user pasting "check disk on 1.2.3.4" must hit 1.2.3.4 even when the
// integration has a different default host saved.
func TestBuildWorkspaceAction_SSH_PerCallOverridesWinOverSavedConfig(t *testing.T) {
	configs := []db.WorkspaceConfigValue{
		{Name: "host", Value: "saved.example.com"},
		{Name: "username", Value: "ec2-user"},
	}
	args := map[string]any{"host_name": "1.2.3.4", "user_name": "admin"}
	_, params, err := buildWorkspaceAction("ssh", "uptime", configs, args, "shell:latest")
	assert.NoError(t, err)
	cmd := params["command"].(string)
	assert.Contains(t, cmd, "admin@1.2.3.4")
	assert.NotContains(t, cmd, "saved.example.com")
	assert.NotContains(t, cmd, "ec2-user")
}

// TestBuildWorkspaceAction_SSH_RejectsMalformedSavedHost is the
// defense-in-depth guard: even if a corrupted DB value (or future schema
// drift) lands in configValues, it must not be interpolated raw into the
// shell command. The same regex used at save time fires here too.
func TestBuildWorkspaceAction_SSH_RejectsMalformedSavedHost(t *testing.T) {
	configs := []db.WorkspaceConfigValue{{Name: "host", Value: "evil.com; rm -rf /"}}
	_, _, err := buildWorkspaceAction("ssh", "uptime", configs, map[string]any{}, "shell:latest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid saved host")
}

// TestBuildWorkspaceAction_SSH_RejectsMalformedSavedUser is the matching
// guard on the username side.
func TestBuildWorkspaceAction_SSH_RejectsMalformedSavedUser(t *testing.T) {
	configs := []db.WorkspaceConfigValue{{Name: "username", Value: "root;ls"}}
	_, _, err := buildWorkspaceAction("ssh", "uptime", configs, map[string]any{}, "shell:latest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid saved username")
}

// TestBuildWorkspaceAction_SSH_AcceptsValidUsers covers the well-formed
// username shapes we expect from real callers.
func TestBuildWorkspaceAction_SSH_AcceptsValidUsers(t *testing.T) {
	goodUsers := []string{"admin", "ec2-user", "ubuntu", "root", "user.name", "_svc"}
	for _, u := range goodUsers {
		t.Run(u, func(t *testing.T) {
			args := map[string]any{"host_name": "1.2.3.4", "user_name": u}
			_, params, err := buildWorkspaceAction("ssh", "uptime", nil, args, "shell:latest")
			assert.NoError(t, err)
			cmd := params["command"].(string)
			assert.Contains(t, cmd, u+"@1.2.3.4")
		})
	}
}
