package tools

import (
	"context"
	"strings"
	"testing"
)

func TestCLITool_SecurityBypass(t *testing.T) {
	workspace := t.TempDir()
	tool := NewCLITool(workspace)
	ctx := context.Background()

	// Test cases of commands that SHOULD be blocked
	dangerousCommands := []string{
		"rm -fr /",
		"rm -R /",
		"rm --recursive /",
		"rm -vfr /",
		"rm -rfv /",
	}

	for _, cmd := range dangerousCommands {
		input := map[string]any{
			"command": cmd,
		}

		response := tool.Execute(ctx, input)

		if response.Status == "error" && strings.Contains(response.Error, "Command blocked for security reasons") {
			t.Logf("Security verified: Command '%s' was correctly blocked", cmd)
		} else {
			t.Errorf("Security check failed: Command '%s' was NOT blocked", cmd)
		}
	}

	// Test cases of commands that should be ALLOWED (safe)
	safeCommands := []string{
		"rm file.txt",
		"rm -f file.txt",
	}

	for _, cmd := range safeCommands {
		input := map[string]any{
			"command": cmd,
		}

		response := tool.Execute(ctx, input)

		// It might fail execution (file not found), but it shouldn't be BLOCKED by security
		if response.Status == "error" && strings.Contains(response.Error, "Command blocked for security reasons") {
			t.Errorf("False positive: Safe command '%s' was blocked", cmd)
		} else {
			t.Logf("False positive check passed: Command '%s' was allowed (execution result irrelevant)", cmd)
		}
	}
}

func TestCLITool_BypassPatterns(t *testing.T) {
	workspace := t.TempDir()
	tool := NewCLITool(workspace)
	ctx := context.Background()

	// All of these bypass techniques should be blocked
	bypassCommands := []struct {
		name string
		cmd  string
	}{
		{"base64 decode to sh", `echo "cm0gLXJmIC8=" | base64 -d | sh`},
		{"base64 decode to bash", `echo "cm0gLXJmIC8=" | base64 -d | bash`},
		{"hex escape $'\\x...'", `$'\x72\x6d' -rf /`},
		{"octal escape $'\\0...'", `$'\0162\0155' -rf /`},
		{"eval wrapper", `eval "rm -rf /"`},
		{"source command", `source /etc/profile`},
		{"command substitution $()", `$(echo rm) -rf /`},
		{"command substitution backtick", "`echo rm` -rf /"},
		{"pipe to sh", `cat script.sh | sh`},
		{"pipe to bash no space", `cat script.sh |bash`},
		{"env prefix bypass", `env rm -rf /`},
	}

	for _, tc := range bypassCommands {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{
				"command": tc.cmd,
			}

			response := tool.Execute(ctx, input)

			if response.Status == "error" && strings.Contains(response.Error, "Command blocked for security reasons") {
				t.Logf("Bypass blocked: '%s'", tc.cmd)
			} else {
				t.Errorf("Bypass NOT blocked: '%s' (status=%s, error=%s)", tc.cmd, response.Status, response.Error)
			}
		})
	}
}

func TestCLITool_BlockedCommands(t *testing.T) {
	workspace := t.TempDir()
	tool := NewCLITool(workspace)
	ctx := context.Background()

	// System-admin commands that should be blocked in workspace pods
	blockedCommands := []string{
		"dd if=/dev/zero of=/dev/sda",
		"iptables -F",
		"useradd attacker",
		"passwd root",
		"shutdown -h now",
		"reboot",
		"systemctl stop firewall",
		"sudo rm -rf /",
		"su - root",
		"mount /dev/sda1 /mnt",
		"apt-get install malware",
		"mkfs.ext4 /dev/sda1",
	}

	for _, cmd := range blockedCommands {
		t.Run(cmd, func(t *testing.T) {
			input := map[string]any{
				"command": cmd,
			}

			response := tool.Execute(ctx, input)

			if response.Status == "error" && strings.Contains(response.Error, "Command blocked for security reasons") {
				t.Logf("Blocked: '%s'", cmd)
			} else {
				t.Errorf("Should be blocked but was NOT: '%s'", cmd)
			}
		})
	}
}

func TestCLITool_UnknownCommandsAllowed(t *testing.T) {
	workspace := t.TempDir()
	tool := NewCLITool(workspace)
	ctx := context.Background()

	// Commands not on the blocklist should be allowed (may fail at execution)
	allowedCommands := []string{
		"nmap --version",
		"htop",
		"vim --version",
		"terraform version",
		"docker ps",
		"kubectl version",
	}

	for _, cmd := range allowedCommands {
		t.Run(cmd, func(t *testing.T) {
			input := map[string]any{
				"command": cmd,
			}

			response := tool.Execute(ctx, input)

			if response.Status == "error" && strings.Contains(response.Error, "Command blocked for security reasons") {
				t.Errorf("Should be allowed but was blocked: '%s'", cmd)
			}
		})
	}
}

func TestCLITool_SafeCommandsAllowed(t *testing.T) {
	workspace := t.TempDir()
	tool := NewCLITool(workspace)
	ctx := context.Background()

	// These commands should pass security checks (may fail at execution)
	safeCommands := []string{
		"ls -la",
		"cat README.md",
		"git status",
		"grep -r pattern .",
		"find . -name '*.go'",
		"echo hello",
		"pwd",
		"date",
		"curl --version",
		"make help",
		"go version",
		"npm --version",
		"python3 script.py",
		"base64 encode.txt",
		"hexdump -C binary.dat",
	}

	for _, cmd := range safeCommands {
		t.Run(cmd, func(t *testing.T) {
			input := map[string]any{
				"command": cmd,
			}

			response := tool.Execute(ctx, input)

			if response.Status == "error" && strings.Contains(response.Error, "Command blocked for security reasons") {
				t.Errorf("Safe command was blocked: '%s'", cmd)
			}
		})
	}
}
