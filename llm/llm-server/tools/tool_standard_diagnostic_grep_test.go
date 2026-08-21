package tools

// Unit tests for the standard_diagnostic_grep primitive. Pure — no workspace,
// no shell execution — the ShellTool is replaced with a recording mock so we
// can assert on the compound shell command the primitive assembles for each
// bundle. E2E coverage over a real workspace lives with the other tool e2e
// suites and is out of scope here.

import (
	"strings"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enabled/disabled are small helpers so each test can locally control the
// tool's feature-gate via the per-instance EnabledOverride field instead of
// mutating package-level config.Config — the latter is a shared-state hazard
// when tests run in parallel.
func enabled() *bool  { v := true; return &v }
func disabled() *bool { v := false; return &v }

// mockShellTool records the last compound command it was asked to run and
// returns a canned response. Implements core.NBTool.
type mockShellTool struct {
	lastCommand string
	resp        core.NBToolResponse
	err         error
}

func (m *mockShellTool) Name() string             { return "shell_execute" }
func (m *mockShellTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (m *mockShellTool) Description() string      { return "mock" }
func (m *mockShellTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject}
}
func (m *mockShellTool) Call(_ core.NbToolContext, in core.NBToolCallRequest) (core.NBToolResponse, error) {
	m.lastCommand = in.Command
	return m.resp, m.err
}

func TestStandardDiagnosticGrep_DisabledByFlag(t *testing.T) {
	// When the tool is disabled it must reject calls with a clear, actionable
	// message pointing the LLM back to plain shell_execute. Short-circuit
	// happens BEFORE any argument validation, so even a perfectly-formed
	// input still gets the disabled response.
	tool := StandardDiagnosticGrepTool{shell: &mockShellTool{}, EnabledOverride: disabled()}
	req := core.NBToolCallRequest{Arguments: map[string]any{"bundle": "crash", "log_file": "app.log"}}
	resp, err := tool.Call(core.NbToolContext{}, req)
	require.Error(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "disabled by config")
	assert.Contains(t, resp.Data, "shell_execute", "message should point the LLM at the fallback")
}

func TestStandardDiagnosticGrep_UnknownBundle(t *testing.T) {
	// Unknown bundle name must error with the known-bundle list in the
	// message so the LLM can retry with a valid name in-turn (no wasted
	// second tool call to discover the schema).
	tool := StandardDiagnosticGrepTool{shell: &mockShellTool{}, EnabledOverride: enabled()}
	req := core.NBToolCallRequest{Arguments: map[string]any{"bundle": "unicorn", "log_file": "app.log"}}
	resp, err := tool.Call(core.NbToolContext{}, req)
	require.Error(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "unknown bundle")
	// The error should enumerate the known bundle names so the LLM can pick
	// one on retry without opening the schema.
	assert.Contains(t, resp.Data, "crash")
}

func TestStandardDiagnosticGrep_MissingArgs(t *testing.T) {
	// Both bundle and log_file are Required in the schema; the planner's
	// pre-call validator normally catches this, but the tool defends itself
	// for direct callers (tests, custom invokers) too.
	tool := StandardDiagnosticGrepTool{shell: &mockShellTool{}, EnabledOverride: enabled()}
	for _, args := range []map[string]any{
		{"bundle": "crash"},                       // missing log_file
		{"log_file": "app.log"},                   // missing bundle
		{"bundle": "  ", "log_file": "app.log"},   // whitespace-only bundle
		{"bundle": "crash", "log_file": "\t\n  "}, // whitespace-only log_file
	} {
		req := core.NBToolCallRequest{Arguments: args}
		resp, err := tool.Call(core.NbToolContext{}, req)
		require.Error(t, err, "args=%v", args)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "required")
	}
}

func TestStandardDiagnosticGrep_ComposesCommandForKnownBundle(t *testing.T) {
	// Happy path: the primitive assembles a single shell command that runs
	// EVERY pattern in the requested bundle against the log file, prefixes
	// each match with the pattern name, and appends the end-marker. This
	// test locks the shape so a future edit that drops a pattern from a
	// bundle or breaks the tagging convention gets caught early.
	mock := &mockShellTool{resp: core.NBToolResponse{Data: "ok", Status: core.NBToolResponseStatusSuccess}}
	tool := StandardDiagnosticGrepTool{shell: mock, EnabledOverride: enabled()}
	req := core.NBToolCallRequest{Arguments: map[string]any{"bundle": "crash", "log_file": "logs_kubectl_1234.txt"}}
	resp, err := tool.Call(core.NbToolContext{}, req)
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)

	// Every pattern in the crash bundle must appear in the compound command
	// exactly once — dropping a pattern would silently reduce the bundle's
	// coverage without any prompt-side signal. The sed prefix
	// `sed 's/^/[<name>] /'` gives us a unique locator per pattern.
	for _, p := range diagnosticBundles["crash"] {
		locator := "sed 's/^/[" + p.Name + "] /'"
		assert.Equalf(t, 1, strings.Count(mock.lastCommand, locator),
			"pattern %q sed-prefix should appear exactly once", p.Name)
	}
	// The log_file argument must be quoted (see shellSingleQuote); check the
	// filename appears wrapped in single quotes so injection-y filenames can't
	// break out of the argument.
	assert.Contains(t, mock.lastCommand, "'logs_kubectl_1234.txt'")
	// File-existence pre-check MUST guard the grep chain — without it, a
	// wrong-path log_file would silently return no-signal instead of failing
	// with an actionable message. Pin the exact `[ -f ... ]` guard shape so
	// a refactor that drops it fails at test time.
	assert.Contains(t, mock.lastCommand, "[ -f 'logs_kubectl_1234.txt' ]")
	assert.Contains(t, mock.lastCommand, "does not exist",
		"missing-file error message must use 'does not exist', not 'not found', "+
			"to avoid false-positive environment-capability detection")
	// Readability guard (distinct from existence): an unreadable file
	// otherwise silently returns empty grep output (stderr suppressed) and
	// looks identical to "no signal" — a false negative for the audit.
	assert.Contains(t, mock.lastCommand, "[ -r 'logs_kubectl_1234.txt' ]")
	assert.Contains(t, mock.lastCommand, "is not readable")
	// Every grep invocation MUST use the `--` end-of-options separator so a
	// file path starting with '-' can never be reinterpreted as a grep
	// option flag (option-injection defense). Count matches the pattern
	// count (each pattern gets one grep call), and we match " -- " rather
	// than "grep -Ein -- " because word-boundary patterns use "grep -Einw --".
	assert.Equal(t, len(diagnosticBundles["crash"]),
		strings.Count(mock.lastCommand, " -- "),
		"every grep call must use ` -- ` before the file path")
	// Word-boundary patterns must go through grep -w (portable across
	// GNU / BSD / BusyBox), not \b in the regex (GNU-only, would silently
	// no-match on Alpine's musl grep). Count grep flag clusters: patterns
	// with WordRegexp=true land as "grep -Einw --", the rest as
	// "grep -Ein --".
	var wantWordCalls, wantPlainCalls int
	for _, p := range diagnosticBundles["crash"] {
		if p.WordRegexp {
			wantWordCalls++
		} else {
			wantPlainCalls++
		}
	}
	assert.Equal(t, wantWordCalls, strings.Count(mock.lastCommand, "grep -Einw -- "),
		"WordRegexp patterns must use grep -w flag (portable) instead of \\b in the pattern")
	assert.Equal(t, wantPlainCalls, strings.Count(mock.lastCommand, "grep -Ein -- "),
		"non-WordRegexp patterns must NOT get the -w flag")
	// End marker — the LLM (and the audit UI) rely on this to distinguish
	// "bundle ran with zero matches" from "tool didn't execute at all".
	assert.Contains(t, mock.lastCommand, "---standard_diagnostic_grep:end---")

	// Metadata annotations survive the shell delegation.
	require.NotNil(t, resp.AdditionalDetails)
	assert.Equal(t, "crash", resp.AdditionalDetails["bundle"])
	assert.Equal(t, len(diagnosticBundles["crash"]), resp.AdditionalDetails["pattern_count"])
}

func TestStandardDiagnosticGrep_QuotesEmbeddedSingleQuoteSafely(t *testing.T) {
	// shellSingleQuote is the security-critical bit — a mis-escaped filename
	// or regex could break the command. Test the classic cases: no quote,
	// one quote, multiple quotes back-to-back.
	cases := map[string]string{
		"plain":            "'plain'",
		"can't":            `'can'\''t'`,
		"''":               `''\'''\'''`, // two empty quotes in a row
		"path with spaces": "'path with spaces'",
		"nasty; rm -rf /":  "'nasty; rm -rf /'",
	}
	for in, want := range cases {
		got := shellSingleQuote(in)
		assert.Equalf(t, want, got, "shellSingleQuote(%q)", in)
	}
}

func TestStandardDiagnosticGrep_BundleNameCaseInsensitive(t *testing.T) {
	// LLMs sometimes emit mixed-case bundle names ("Crash", "OOM"). Reject-
	// on-case-mismatch would waste a tool call to no purpose, so bundle
	// names normalise to lowercase before the map lookup. Verify by asking
	// for "CRASH" and confirming we hit the same code path (successful
	// shell dispatch) as the canonical "crash".
	mock := &mockShellTool{resp: core.NBToolResponse{Data: "ok", Status: core.NBToolResponseStatusSuccess}}
	tool := StandardDiagnosticGrepTool{shell: mock, EnabledOverride: enabled()}
	req := core.NBToolCallRequest{Arguments: map[string]any{"bundle": "CRASH", "log_file": "app.log"}}
	resp, err := tool.Call(core.NbToolContext{}, req)
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.NotEmpty(t, mock.lastCommand, "case-normalised bundle should reach shell dispatch")
	assert.Equal(t, "crash", resp.AdditionalDetails["bundle"],
		"metadata should reflect the canonical (lowercased) bundle name, not the LLM's casing")
}

func TestStandardDiagnosticGrep_SortedBundleNamesStable(t *testing.T) {
	// sortedBundleNames is the deterministic source for the tool description,
	// input-schema enum, and unknown-bundle error message. Anything derived
	// from map iteration would break the account-scoped LLM prompt cache on
	// every process restart. Pin the "sorted alphabetically" invariant here
	// so a future edit that reverts to raw map iteration fails at test time.
	require.NotEmpty(t, sortedBundleNames)
	assert.Equal(t, len(diagnosticBundles), len(sortedBundleNames),
		"sortedBundleNames must contain exactly one entry per bundle")
	for i := 1; i < len(sortedBundleNames); i++ {
		assert.Lessf(t, sortedBundleNames[i-1], sortedBundleNames[i],
			"sortedBundleNames must be alphabetical: %q >= %q at index %d",
			sortedBundleNames[i-1], sortedBundleNames[i], i)
	}
}

func TestStandardDiagnosticGrep_ResolveShell_NilFallsBackToShellTool(t *testing.T) {
	// Direct instantiation (bypassing the factory) leaves shell=nil.
	// resolveShell must hand back a fresh ShellTool bound to the tool's
	// AccountId so downstream Call can't nil-deref the interface. Tested at
	// the helper level rather than through Call to avoid needing a live
	// workspace + valid RequestContext for the real ShellTool.
	tool := StandardDiagnosticGrepTool{AccountId: "acct-x"}
	got := tool.resolveShell()
	require.NotNil(t, got)
	shell, ok := got.(ShellTool)
	require.True(t, ok, "nil t.shell must fall back to a concrete ShellTool")
	assert.Equal(t, "acct-x", shell.AccountId,
		"fallback ShellTool must inherit the diagnostic tool's AccountId")
}

func TestStandardDiagnosticGrep_ResolveShell_ExplicitPreserved(t *testing.T) {
	// When t.shell IS set (factory path or test-injected mock), resolveShell
	// must return it verbatim — not silently substitute a real ShellTool.
	// Otherwise every mock-based test would end up hitting the workspace.
	mock := &mockShellTool{}
	tool := StandardDiagnosticGrepTool{shell: mock}
	got := tool.resolveShell()
	assert.Same(t, mock, got)
}

func TestStandardDiagnosticGrep_BundleDescriptionsCoverAllRegistered(t *testing.T) {
	// The tool description enumerates the available bundle names; if we
	// register a new bundle without adding a descriptive bullet in
	// bundleDescriptions(), the LLM sees the tool but not the guidance for
	// when to pick the new bundle. Fail loudly at build time.
	got := strings.Join(bundleDescriptions(), "\n")
	for name := range diagnosticBundles {
		assert.Containsf(t, got, "`"+name+"`", "bundle %q has no bullet in bundleDescriptions()", name)
	}
}
