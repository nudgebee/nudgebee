package handlers

import (
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/config"
)

// newPathGuardHandler builds a minimal ExecutionHandler suitable for exercising
// validateCommand. The path-guard rules do not touch the workspace directory,
// so an empty config is fine.
func newPathGuardHandler() *ExecutionHandler {
	return NewExecutionHandler(&config.Config{})
}

// TestValidateCommand_KubectlExecRemoteCarveOut verifies that the sensitive-path
// scan does NOT fire on paths that appear after `--` in a `kubectl exec` (or
// `oc exec`/`k exec`) invocation, since the remote portion runs inside the
// target pod and cannot reach the workspace pod's filesystem.
//
// Background: 17 of 45 path-block events over the last 21 days were of this
// shape (`kubectl exec coredns -- cat /etc/resolv.conf` and similar). The
// validator was substring-scanning the whole command and false-positived on
// `/etc` even though the target pod's /etc is irrelevant to workspace safety.
func TestValidateCommand_KubectlExecRemoteCarveOut(t *testing.T) {
	h := newPathGuardHandler()
	tests := []struct {
		name        string
		cmd         string
		expectBlock bool
		wantSub     string
	}{
		// --- Allowed: remote path inside `kubectl exec ... --` ---
		{
			name:        "kubectl exec cat /etc/resolv.conf",
			cmd:         "kubectl exec coredns-55cb58b774-6699v -n kube-system -- cat /etc/resolv.conf",
			expectBlock: false,
		},
		{
			name:        "kubectl exec -it bash session cat /etc/passwd",
			cmd:         "kubectl exec -it mypod -- cat /etc/passwd",
			expectBlock: false,
		},
		{
			name:        "kubectl exec with namespace and container, cat /var/log",
			cmd:         "kubectl exec -n prod -c app mypod -- cat /var/log/app.log",
			expectBlock: false,
		},
		{
			name:        "oc exec (OpenShift) cat /etc",
			cmd:         "oc exec mypod -- cat /etc/hosts",
			expectBlock: false,
		},
		{
			name:        "remote stdout piped to local grep (only remote part has /etc)",
			cmd:         "kubectl exec mypod -- cat /etc/os-release | grep PRETTY",
			expectBlock: false,
		},

		// --- Still blocked: /etc in workspace-side portion ---
		{
			name:        "cat /etc/passwd directly (no kubectl exec at all)",
			cmd:         "cat /etc/passwd",
			expectBlock: true,
			wantSub:     "/etc",
		},
		{
			name:        "kubectl exec then chained cat /etc/passwd (the chain runs in workspace)",
			cmd:         "kubectl exec mypod -- ls ; cat /etc/passwd",
			expectBlock: true,
			wantSub:     "/etc",
		},
		{
			name:        "kubectl exec then && chained cat /etc/passwd (the chain runs in workspace)",
			cmd:         "kubectl exec mypod -- ls && cat /etc/passwd",
			expectBlock: true,
			wantSub:     "/etc",
		},
		{
			name:        "kubectl without exec — no carve-out applies",
			cmd:         "kubectl get pods --kubeconfig /etc/k8s/config",
			expectBlock: true,
			wantSub:     "/etc",
		},
		{
			name:        "kubectl exec without `--` separator — whole thing scanned",
			cmd:         "kubectl exec mypod cat /etc/passwd",
			expectBlock: true,
			wantSub:     "/etc",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := h.validateCommand(tc.cmd, "")
			gotBlock := err != nil && strings.Contains(err.Error(), "access to absolute path")
			if gotBlock != tc.expectBlock {
				t.Fatalf("cmd=%q\nblocked=%v want=%v err=%v", tc.cmd, gotBlock, tc.expectBlock, err)
			}
			if tc.expectBlock && tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("cmd=%q err=%q want substring %q", tc.cmd, err.Error(), tc.wantSub)
			}
		})
	}
}

// TestValidateCommand_DevPseudoDeviceAllowed verifies that standard /dev
// pseudo-devices (/dev/null, /dev/stdout, /dev/stderr, /dev/fd/N, /dev/zero,
// /dev/random, /dev/urandom, /dev/tty) do NOT trigger the /dev path block.
// These are stdio / PRNG sinks, not /dev filesystem access — the most common
// shape is `> /dev/null 2>&1`.
//
// Background: 17 of 45 path-block events over the last 21 days were `/dev/null`
// redirects from `npm ci > /dev/null` and similar.
func TestValidateCommand_DevPseudoDeviceAllowed(t *testing.T) {
	h := newPathGuardHandler()
	tests := []struct {
		name        string
		cmd         string
		expectBlock bool
		wantSub     string
	}{
		// --- Allowed: standard pseudo-devices ---
		{name: "redirect stdout to /dev/null", cmd: "ls > /dev/null", expectBlock: false},
		{name: "redirect stderr to /dev/null", cmd: "ls 2> /dev/null", expectBlock: false},
		{name: "merge fds and discard", cmd: "ls > /dev/null 2>&1", expectBlock: false},
		{name: "/dev/stdout explicit redirect", cmd: "ls > /dev/stdout", expectBlock: false},
		{name: "/dev/stderr explicit redirect", cmd: "ls > /dev/stderr", expectBlock: false},
		{name: "/dev/fd/3 redirect", cmd: "exec 3>&1 ; ls > /dev/fd/3", expectBlock: false},
		{name: "/dev/zero as input for dd", cmd: "dd if=/dev/zero of=test.bin bs=1024 count=1", expectBlock: false},
		{name: "/dev/urandom for entropy", cmd: "head -c 16 /dev/urandom", expectBlock: false},
		{name: "/dev/tty interactive", cmd: "read x < /dev/tty", expectBlock: false},
		{name: "npm ci redirect", cmd: "cd /tmp/x && npm ci --legacy-peer-deps > /dev/null", expectBlock: false},

		// --- Still blocked: real /dev access ---
		{name: "write to /dev/sda1 device", cmd: "dd if=/dev/zero of=/dev/sda1", expectBlock: true, wantSub: "/dev"},
		{name: "read /dev/mem", cmd: "cat /dev/mem", expectBlock: true, wantSub: "/dev"},
		{name: "list /dev directory", cmd: "ls /dev/", expectBlock: true, wantSub: "/dev"},
		{name: "/dev/null suffix word doesn't whitelist /dev/nullx", cmd: "cat /dev/nullx", expectBlock: true, wantSub: "/dev"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := h.validateCommand(tc.cmd, "")
			gotBlock := err != nil && strings.Contains(err.Error(), "access to absolute path")
			if gotBlock != tc.expectBlock {
				t.Fatalf("cmd=%q\nblocked=%v want=%v err=%v", tc.cmd, gotBlock, tc.expectBlock, err)
			}
			if tc.expectBlock && tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("cmd=%q err=%q want substring %q", tc.cmd, err.Error(), tc.wantSub)
			}
		})
	}
}

// TestValidateCommand_PathGuardRegressions confirms the existing rules still
// fire on the canonical attack shapes after the carve-outs are applied.
func TestValidateCommand_PathGuardRegressions(t *testing.T) {
	h := newPathGuardHandler()
	tests := []struct {
		name        string
		cmd         string
		expectBlock bool
		wantSub     string
	}{
		{name: "leading-slash /etc still blocks", cmd: "/etc/init.d/networking restart", expectBlock: true, wantSub: "/etc"},
		{name: "quoted '/root/.ssh' still blocks", cmd: "cat '/root/.ssh/id_rsa'", expectBlock: true, wantSub: "/root"},
		{name: `quoted "/var/log" still blocks`, cmd: `tail -f "/var/log/syslog"`, expectBlock: true, wantSub: "/var"},
		{name: "=/proc env assignment still blocks", cmd: "export PROC=/proc/1/environ ; cat $PROC", expectBlock: true, wantSub: "/proc"},
		// `ln -s /etc/passwd …` is caught by the path-guard rule (step 1) before
		// reaching the symlink rule (step 4); both block-paths reach the same
		// outcome, we just assert the command is rejected.
		{name: "symlink to /etc/passwd still blocks", cmd: "ln -s /etc/passwd link && cat link", expectBlock: true},
		{name: "rm -rf / still blocks via dangerous-cmd", cmd: "rm -rf /", expectBlock: true, wantSub: "rm -rf"},
		{name: "sudo still blocks", cmd: "sudo ls", expectBlock: true, wantSub: "sudo"},
		{name: "harmless command passes", cmd: "kubectl get pods -n demo", expectBlock: false},
		{name: "harmless pipeline passes", cmd: "kubectl get pods -n demo | grep nginx", expectBlock: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := h.validateCommand(tc.cmd, "")
			gotErr := err != nil
			if gotErr != tc.expectBlock {
				t.Fatalf("cmd=%q\nblocked=%v want=%v err=%v", tc.cmd, gotErr, tc.expectBlock, err)
			}
			if tc.expectBlock && tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("cmd=%q err=%q want substring %q", tc.cmd, err.Error(), tc.wantSub)
			}
		})
	}
}

// TestRegex_KubectlExecRemote documents kubectlExecRemoteRe behaviour
// independently of validateCommand.
func TestRegex_KubectlExecRemote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // expected substring of the match (or "" if no match expected)
	}{
		{name: "basic kubectl exec with --", in: "kubectl exec mypod -- cat /etc/passwd", want: "kubectl exec mypod -- cat /etc/passwd"},
		{name: "oc exec", in: "oc exec mypod -- ls /etc", want: "oc exec mypod -- ls /etc"},
		{name: "stops at semicolon", in: "kubectl exec mypod -- ls ; cat /etc/passwd", want: "kubectl exec mypod -- ls "},
		{name: "stops at &&", in: "kubectl exec mypod -- ls && cat /etc/passwd", want: "kubectl exec mypod -- ls "},
		{name: "stops at |", in: "kubectl exec mypod -- ls | head", want: "kubectl exec mypod -- ls "},
		{name: "no -- separator → no match", in: "kubectl exec mypod cat /etc/passwd", want: ""},
		{name: "kubectl without exec → no match", in: "kubectl get pods --kubeconfig /etc/k8s", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := kubectlExecRemoteRe.FindString(tc.in)
			if got != tc.want {
				t.Errorf("kubectlExecRemoteRe.FindString(%q) = %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRegex_DevPseudoDevice documents devPseudoDeviceRe behaviour.
func TestRegex_DevPseudoDevice(t *testing.T) {
	matches := []string{
		"/dev/null",
		"/dev/stdout",
		"/dev/stderr",
		"/dev/stdin",
		"/dev/zero",
		"/dev/random",
		"/dev/urandom",
		"/dev/tty",
		"/dev/fd/0",
		"/dev/fd/3",
		"/dev/fd/99",
	}
	for _, s := range matches {
		if !devPseudoDeviceRe.MatchString(s) {
			t.Errorf("devPseudoDeviceRe should match %q but did not", s)
		}
	}
	misses := []string{
		"/dev/sda1",
		"/dev/mem",
		"/dev/nullx",     // word boundary: nullx ≠ null
		"/dev/randomize", // word boundary: randomize ≠ random
		"/dev/fd/",       // /dev/fd/ alone (no number) is not a pseudo-device redirect
		"/dev/",
		"/dev",
	}
	for _, s := range misses {
		if devPseudoDeviceRe.MatchString(s) {
			t.Errorf("devPseudoDeviceRe should NOT match %q but did", s)
		}
	}
}
