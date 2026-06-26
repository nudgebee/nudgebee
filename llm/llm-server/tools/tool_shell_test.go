package tools

import (
	"errors"
	"fmt"
	"testing"

	"nudgebee/llm/workspace"

	"github.com/stretchr/testify/assert"
)

// --- Change 1: grep/find/jq exit 1 reclassified as success-with-no-matches ---

func TestIsNoMatchExit(t *testing.T) {
	type tc struct {
		name    string
		err     error
		command string
		want    bool
	}

	exitOne := &workspace.CommandFailure{Status: "failed", StdErr: "exit status 1"}
	exitTwo := &workspace.CommandFailure{Status: "failed", StdErr: "exit status 2"}
	otherErr := errors.New("connection refused")
	wrappedOther := fmt.Errorf("wrap: %w", exitOne)

	cases := []tc{
		// Bare grep-family — exit 1 is no-match.
		{name: "grep exit 1", err: exitOne, command: "grep foo file", want: true},
		{name: "grep wrapped exit 1", err: wrappedOther, command: "grep foo file", want: true},
		{name: "egrep exit 1", err: exitOne, command: "egrep 'a|b' /tmp/x", want: true},
		{name: "fgrep exit 1", err: exitOne, command: "fgrep needle hay", want: true},
		{name: "rg exit 1", err: exitOne, command: "rg 'pattern' src/", want: true},
		{name: "ack exit 1", err: exitOne, command: "ack 'pattern' src/", want: true},
		{name: "ag exit 1", err: exitOne, command: "ag 'pattern' src/", want: true},

		// find and jq must NOT be reclassified (review feedback on PR #32007).
		// find exit 1 is ALWAYS a real error (bad path / permission). jq exit 1
		// only happens with `-e` flag and means "filter returned null/false",
		// which is uncommon; the cleaner default is to surface it.
		{name: "find exit 1 (real error: bad path / permission) — NOT reclassified", err: exitOne, command: "find . -name '*.go'", want: false},
		{name: "jq exit 1 (only happens with -e) — NOT reclassified", err: exitOne, command: "jq .foo data.json", want: false},

		// Pipeline-tail semantics — pipeline exit status is the last
		// command's, so look at the last segment, not the first token.
		{name: "grep at end of pipeline IS reclassified", err: exitOne, command: "cat x | grep foo", want: true},
		{name: "kubectl | grep no-match IS reclassified (dominant production pattern)", err: exitOne, command: "kubectl get pods | grep ready", want: true},
		{name: "three-segment pipeline ending in grep IS reclassified", err: exitOne, command: "cat f | sort | grep foo", want: true},
		{name: "pipeline ending in wc is NOT reclassified", err: exitOne, command: "grep foo file | wc -l", want: false},
		{name: "logical OR (||) is not a pipe — first segment governs", err: exitOne, command: "grep foo file || true", want: true},
		{name: "kubectl || grep — first segment governs", err: exitOne, command: "kubectl get pods || grep foo file", want: false},
		{name: "pipe inside double-quotes is not a real pipe", err: exitOne, command: `grep "a|b" file`, want: true},
		{name: "pipe inside single-quotes is not a real pipe", err: exitOne, command: "grep 'a|b' file", want: true},
		{name: "escaped pipe is not a real pipe", err: exitOne, command: `grep foo\|bar file`, want: true},

		// Non-no-match commands.
		{name: "grep exit 2 (real error)", err: exitTwo, command: "grep foo file", want: false},
		{name: "kubectl exit 1 (not a no-match command)", err: exitOne, command: "kubectl get pods", want: false},
		{name: "ls exit 1 (not a no-match command)", err: exitOne, command: "ls /missing", want: false},
		{name: "cat exit 1 (not a no-match command)", err: exitOne, command: "cat /missing", want: false},
		{name: "non-workspace error", err: otherErr, command: "grep foo file", want: false},
		{name: "nil error", err: nil, command: "grep foo file", want: false},
		{name: "leading whitespace ok", err: exitOne, command: "   grep foo file", want: true},
		{name: "exit 1 with extra text — not reclassified", err: &workspace.CommandFailure{Status: "failed", StdErr: "exit status 1: something"}, command: "grep foo file", want: false},

		// env-prefix handling — Gemini round 3 (Jun 10 2026).
		{name: "LC_ALL=C grep — env prefix skipped", err: exitOne, command: "LC_ALL=C grep foo file", want: true},
		{name: "TZ=UTC grep — env prefix skipped", err: exitOne, command: "TZ=UTC grep foo file", want: true},
		{name: "multiple env prefixes — all skipped", err: exitOne, command: "LC_ALL=C LANG=C grep foo file", want: true},
		{name: "env prefix on non-no-match command — not reclassified", err: exitOne, command: "LC_ALL=C kubectl get pods", want: false},
		{name: "only env prefix, no command — not reclassified", err: exitOne, command: "LC_ALL=C", want: false},
		{name: "malformed VAR (starts with digit) — not env prefix, returned as-is", err: exitOne, command: "123x=foo grep bar file", want: false},
		{name: "env prefix on last pipeline segment", err: exitOne, command: "kubectl get pods | LC_ALL=C grep ready", want: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isNoMatchExit(c.err, c.command)
			assert.Equal(t, c.want, got)
		})
	}
}

// --- Change 2: empty / JSON-envelope rejection ---

func TestLooksLikeJSONEnvelope(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "empty", command: "", want: false},
		{name: "plain shell", command: "ls -la", want: false},
		{name: "jq brace filter", command: "jq '{name: .name}'", want: false},
		{name: "bash brace expansion", command: "echo {1,2,3}", want: false},
		{name: "JSON envelope with command key", command: `{"command":"ls -la"}`, want: true},
		{name: "JSON envelope single-quoted", command: `{'command':'ls'}`, want: true},
		{name: "JSON envelope with whitespace before colon", command: `{"command" : "ls"}`, want: true},
		{name: "JSON envelope with leading whitespace", command: "  \n{\"command\":\"ls\"}", want: true},
		{name: "JSON envelope with extra keys", command: `{"command":"ls","work_dir":"/tmp"}`, want: true},
		{name: "leading brace without command key", command: `{"foo":"bar"}`, want: false},
		{name: "truncated JSON envelope from production", command: `{command:: not found`, want: false}, // leading { but no quoted "command"
		// False-positive guard rails — bash grouped commands that contain the
		// substring `"command"` but are not JSON envelopes must pass through.
		{name: "bash grouped echo with quoted command literal", command: `{ echo "command"; }`, want: false},
		{name: "bash grouped echo with phrase containing the word command", command: `{ echo "running command"; }`, want: false},
		{name: "bash command builtin in a group", command: `{ command -v jq; }`, want: false},
		{name: "bash assignment of the word command", command: `{ x="command"; echo $x; }`, want: false},
		// First-non-whitespace-after-`{` guard: a bash group whose body
		// embeds a full quoted JSON envelope must still not be flagged.
		// In any genuine JSON envelope the first non-whitespace char after
		// `{` is a quote; in a bash group it is a command name.
		{name: "bash grouped echo of a quoted JSON envelope literal", command: `{ echo '{"command":"ls"}'; }`, want: false},
		{name: "bash grouped printf of a quoted JSON envelope literal", command: `{ printf '%s\n' '{"command":"x"}'; }`, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, looksLikeJSONEnvelope(c.command))
		})
	}
}

// --- Change 6: structured error wrappers ---

func TestShellErrorHint(t *testing.T) {
	cases := []struct {
		name        string
		rawError    string
		command     string
		wantHintSub string // substring expected in hint; empty = expect no hint
	}{
		{
			name:        "unterminated quoted string",
			rawError:    `sh: syntax error: unterminated quoted string`,
			command:     `grep "foo file`,
			wantHintSub: "unbalanced quotes",
		},
		{
			name:        "no such file on cat",
			rawError:    `cat: /tmp/missing.json: No such file or directory`,
			command:     "cat /tmp/missing.json",
			wantHintSub: "workspace pod is per-account",
		},
		{
			name:        "no such file on grep",
			rawError:    `grep: /tmp/missing.json: No such file or directory`,
			command:     "grep foo /tmp/missing.json",
			wantHintSub: "workspace pod is per-account",
		},
		{
			name:        "no such file on non-file-reader gets no hint",
			rawError:    `kubectl: /tmp/missing.yaml: No such file or directory`,
			command:     "kubectl apply -f /tmp/missing.yaml",
			wantHintSub: "", // first token is kubectl, not a file reader
		},
		{
			name:        "command not found",
			rawError:    `sh: nosuchcmd: command not found`,
			command:     "nosuchcmd --help",
			wantHintSub: "Available CLIs",
		},
		{
			name:        "alpine ash command not found",
			rawError:    `sh: nosuchcmd: not found`,
			command:     "nosuchcmd --help",
			wantHintSub: "Available CLIs",
		},
		{
			name:        "unknown stderr gets no hint",
			rawError:    "exit status 137: killed",
			command:     "yes > /dev/null",
			wantHintSub: "",
		},
		// env-prefix handling — Gemini round 3 (Jun 10 2026)
		{
			name:        "no such file on env-prefixed cat gets the hint",
			rawError:    `cat: /tmp/missing.json: No such file or directory`,
			command:     "LC_ALL=C cat /tmp/missing.json",
			wantHintSub: "workspace pod is per-account",
		},
		{
			name:        "no such file on env-prefixed kubectl gets no hint (not a file reader)",
			rawError:    `kubectl: /tmp/missing.yaml: No such file or directory`,
			command:     "LC_ALL=C kubectl apply -f /tmp/missing.yaml",
			wantHintSub: "",
		},
		// Tightened command-not-found discriminator — Gemini Jun 12 2026.
		// `strings.Contains(": not found")` used to fire on CLI
		// resource-not-found messages and tell the LLM the CLI itself
		// was missing. Anchor to a shell prefix + trailing `: not found`.
		{
			name:        "helm resource-not-found does NOT get the command-not-found hint",
			rawError:    `Error: release: not found`,
			command:     "helm status myrelease",
			wantHintSub: "",
		},
		{
			name:        "kubectl resource-not-found does NOT get the command-not-found hint",
			rawError:    `Error from server (NotFound): pods "missing-pod": not found`,
			command:     "kubectl get pod missing-pod",
			wantHintSub: "",
		},
		{
			name:        "custom script error with `: not found` does NOT get the command-not-found hint",
			rawError:    `error: user: not found`,
			command:     "myscript --user nobody",
			wantHintSub: "",
		},
		{
			name:        "bash command-not-found still hits via 'command not found' substring",
			rawError:    `bash: nosuchcmd: command not found`,
			command:     "nosuchcmd --help",
			wantHintSub: "Available CLIs",
		},
		{
			name:        "alpine ash command-not-found still hits via shell-prefix + : not found",
			rawError:    `sh: nosuchcmd: not found`,
			command:     "nosuchcmd --help",
			wantHintSub: "Available CLIs",
		},
		{
			name:        "absolute-path /bin/sh form still hits",
			rawError:    `/bin/sh: nosuchcmd: not found`,
			command:     "nosuchcmd --help",
			wantHintSub: "Available CLIs",
		},
		{
			name:        "multi-line stderr with command-not-found on first line still hits",
			rawError:    "sh: nosuchcmd: not found\nadditional context here",
			command:     "nosuchcmd --help",
			wantHintSub: "Available CLIs",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shellErrorHint(c.rawError, c.command)
			if c.wantHintSub == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, c.wantHintSub)
		})
	}
}

func TestWrapShellError_PreservesOriginal(t *testing.T) {
	raw := "cat: /tmp/missing.json: No such file or directory"
	wrapped := wrapShellError(raw, "cat /tmp/missing.json")

	// The wrapped form must contain both the hint and the raw error verbatim.
	assert.Contains(t, wrapped, "workspace pod is per-account")
	assert.Contains(t, wrapped, raw)
	assert.Contains(t, wrapped, `"original_error"`)
	assert.Contains(t, wrapped, `"error_hint"`)
}

func TestWrapShellError_PassesThroughOnNoHint(t *testing.T) {
	raw := "exit status 137: killed"
	wrapped := wrapShellError(raw, "yes > /dev/null")
	// No hint matched → return raw unchanged so callers see the underlying message.
	assert.Equal(t, raw, wrapped)
}

func TestFirstTokenIsFileReader(t *testing.T) {
	assert.True(t, firstTokenIsFileReader("cat /tmp/x"))
	assert.True(t, firstTokenIsFileReader("grep foo /tmp/x"))
	assert.True(t, firstTokenIsFileReader("jq .foo /tmp/x.json"))
	assert.False(t, firstTokenIsFileReader("kubectl get pods"))
	assert.False(t, firstTokenIsFileReader("aws s3 ls"))
	assert.False(t, firstTokenIsFileReader("bash -c 'rm -rf /'"))
	// env-prefix handling — Gemini round 3 (Jun 10 2026)
	assert.True(t, firstTokenIsFileReader("LC_ALL=C cat /tmp/x"))
	assert.True(t, firstTokenIsFileReader("TZ=UTC LANG=C grep foo /tmp/x"))
	assert.False(t, firstTokenIsFileReader("LC_ALL=C kubectl get pods"))
}

// --- Cross-cutting: CommandFailure still satisfies errors.Is ---

func TestCommandFailure_IsErrWorkspaceCommandFailed(t *testing.T) {
	cf := &workspace.CommandFailure{Status: "failed", StdErr: "exit status 1"}
	assert.True(t, errors.Is(cf, workspace.ErrWorkspaceCommandFailed),
		"CommandFailure must satisfy errors.Is(ErrWorkspaceCommandFailed) so existing call-site discrimination keeps working")
}

// --- ShellTool.Description() carries the canonical workspace contract ---

// TestShellToolDescription_CarriesWorkspaceContract pins the tool
// description to the ground truth in
// llm/code-analysis/api/handlers/execution_handler.go:114-179 — every
// shell_execute lands in a per-conversation directory
// (workDir = baseDir/<conversationId>), with HOME and PWD pointing at
// it; /tmp is pod-wide and shared across conversations on the same
// account.
//
// Why the description is the only source of truth: the tool's
// description ships with the tool list, so every agent that has
// shell_execute available automatically receives these facts. No
// per-agent injection, no template gate. This is the consolidation
// PR #32007 made — see the merge plan in that PR description.
//
// Regressions to watch for:
//
//   - "cd /app/workspaces" — the pre-fix wrong example claiming the
//     cwd is /app. Restoring it would re-introduce the same lie that
//     14 specialized agents were unwittingly repeating.
//   - Tool-list omissions — if the canonical list of shim binaries
//     (kubectl/helm/psql/...) or native CLIs (aws/gcloud/az/gh/python3)
//     drops out, the LLM stops knowing they exist and falls back to
//     inferring availability from tool names alone.
//   - no_matches semantic — if this drops out, the LLM goes back to
//     interpreting grep-family exit-1 as a tool failure and retries.
//     (find and jq were dropped post-review; see TestIsNoMatchExit.)
//   - Credential auto-injection — if this drops out, the LLM plans
//     `aws configure` / `gcloud auth login` / `gh auth login` steps
//     that pollute the shared workspace and confuse later turns.
func TestShellToolDescription_CarriesWorkspaceContract(t *testing.T) {
	desc := ShellTool{}.Description()

	// --- Positive assertions: facts that must be present ---
	required := map[string]string{
		"per-conversation directory": "must state the cwd scope is per-conversation, not per-account or per-task",
		"`/tmp/`":                    "must explicitly call out absolute /tmp/... as the cross-conversation leak vector",
		"shared with other conversations on the same account": "must explain the /tmp/ scope so the LLM treats it as system scratch, not per-chat scratch",
		"`ls -la`":                    "must point the LLM at the discovery move for tool-saved artifacts",
		"`.nb_profile`":               "must teach env-var persistence, since each shell_execute is a fresh sh -c",
		"`kubectl`":                   "must surface the shim CLIs the LLM is supposed to call via shell when no specialized agent fits",
		"`aws`":                       "must surface the cloud CLIs the LLM is supposed to call via shell when no specialized agent fits",
		"`gh`":                        "must surface the GitHub CLI (auto-injected token relies on it)",
		"`python3`":                   "must surface python3, which the LLM uses for arithmetic / quick data shaping",
		"`logs_*`":                    "must name the tool-saved artifact pattern so cleanup advice has a concrete referent",
		"evidence chain":              "must justify why tool-saved artifacts must not be deleted",
		"no_matches":                  "must teach the grep-family exit-1 semantic so the LLM stops retrying empty matches",
		"auto-injected":               "must teach that cloud + GITHUB_TOKEN credentials are auto-injected, so the LLM does not plan an `aws configure` / `gh auth login` step",
		"`error_hint`":                "must teach the LLM how to read the structured-error envelope produced by Change 6 wrappers (unbalanced quotes, no-such-file, command-not-found)",
		"`original_error`":            "must teach that raw stderr is preserved verbatim under original_error so the LLM still sees the underlying signal",
		"It does NOT apply to `find`": "must clarify the post-review narrowing of the no-match rule — find exit 1 is a real error and is NOT reclassified",
	}
	for snippet, why := range required {
		assert.Contains(t, desc, snippet,
			"ShellTool.Description() is missing %q — %s", snippet, why)
	}

	// --- Negative assertions: claims the execution handler contradicts ---
	forbidden := map[string]string{
		"/app/workspaces":       "execution_handler.go sets cmd.Dir = workDir (= baseDir/<conversationId>), not /app — this was the wrong example that started PR #32007",
		"/app/workspaces && ls": "the pre-fix example combining `cd /app/workspaces` and `ls -la` was misleading on two axes (wrong path + suggesting cd persists)",
		"isolated workspace environment for this specific task": "the workspace pod is per-account and persistent across conversations — only cwd/HOME/PWD are conversation-scoped",
	}
	for snippet, why := range forbidden {
		assert.NotContains(t, desc, snippet,
			"ShellTool.Description() reintroduced a known-wrong claim %q — %s", snippet, why)
	}
}
