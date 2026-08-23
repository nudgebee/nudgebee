package tools

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
)

// StandardDiagnosticGrepTool bundles a fixed set of well-known diagnostic
// regexes and runs them against a single log file in ONE tool call. Motivated
// by #34712: today the logs agent emits one <action>shell_execute</action>
// per grep pattern, which costs an LLM turn (~3s) + a workspace roundtrip
// (~500ms) *per pattern* — so a 6-pattern crash sweep costs ~20s of LLM+API
// time to run what is ~500ms of actual grep work. This primitive collapses
// the sweep into a single planner turn: the LLM picks a bundle name, the
// server assembles + executes the whole grep chain server-side, returns one
// consolidated observation.
//
// Parallelism note: the greps are chained inside one sh -c invocation. The
// wall-clock win comes from eliminating N LLM turns, not from OS-level
// parallel grep execution — greps on a single file are I/O bound and the
// disk cache warms after the first pass, so serial chaining is within a
// few ms of parallel `&`-and-`wait`. If profiling ever shows this is the
// bottleneck we can move to per-pattern goroutines invoking ShellTool.Call.
//
// Feature-gated via LogsStandardGrepEnabled — the tool registers
// unconditionally so a config flip doesn't require a redeploy, but Call
// short-circuits when the flag is off so operators can dark-launch.
const ToolStandardDiagnosticGrep = "standard_diagnostic_grep"

// bundlePattern names a single diagnostic regex within a bundle. Fields
// are surfaced in the tool's output: name prefixes each result section so
// the LLM (and the audit UI) can see which pattern matched what.
// WordRegexp, when true, appends grep's -w flag for the pattern rather than
// relying on `\b` inside Regex — critical because `\b` is a GNU-grep
// extension and the workspace runs on Alpine (musl grep, no `\b`). -w is
// portably supported across GNU, BSD, and BusyBox grep.
type bundlePattern struct {
	Name       string
	Regex      string
	WordRegexp bool
}

// diagnosticBundles is the static registry of bundle-name → pattern-list.
// Kept intentionally small and universal — bundles are meant to catch the
// ~80% of common failure modes without any tenant/service tuning. Anything
// service-specific belongs in a follow-up shell_execute batch (which the
// logs agent still emits when the bundle doesn't produce enough signal).
//
// When adding a bundle: keep patterns broad and language-agnostic, prefer
// single-word or well-known tokens over full-sentence matches, and avoid
// per-service jargon. Every pattern uses grep -Ei so patterns MUST be POSIX
// extended regex compatible.
var diagnosticBundles = map[string][]bundlePattern{
	// crash: the default when investigating "why did service X break". Covers
	// the terminal-state failure vocabulary — the LLM asks for this bundle
	// first on any log investigation and only escalates if the sweep is empty.
	"crash": {
		// grep -Ei (case-insensitive) is set at call time so explicit case
		// variants (error/ERROR/Error) are redundant with a bare `error`.
		// Word-boundary anchoring goes through grep's -w flag (via
		// WordRegexp) instead of `\b` in the pattern because `\b` is a
		// GNU-grep extension that Alpine's musl grep does not support —
		// using it directly would silently produce zero matches in prod.
		// -w handles the common cases (word / word-adjacent-punctuation /
		// end-of-line) which is what we want here.
		{Name: "error", Regex: `error|err|err\.`, WordRegexp: true},
		{Name: "fatal", Regex: `fatal`, WordRegexp: true},
		{Name: "panic", Regex: `panic`, WordRegexp: true},
		{Name: "oom", Regex: `OOM|OOMKilled|OutOfMemory|Out of memory|Cannot allocate`},
		{Name: "timeout", Regex: `timeout|timed out|deadline exceeded|context deadline`},
		{Name: "conn_refused", Regex: `connection refused|no route to host|network unreachable`},
		{Name: "tls", Regex: `TLS handshake|x509|certificate.*(expired|invalid|unknown)`},
		{Name: "http_5xx", Regex: `HTTP/[0-9.]+ 5[0-9][0-9]|status.*5[0-9][0-9]`},
	},
	// network: subset of crash focused on connectivity failure modes. Meant
	// for "service can't reach dependency" investigations where crash's error
	// bucket would drown the signal in application-level errors.
	"network": {
		{Name: "conn_refused", Regex: `connection refused|no route to host|network unreachable`},
		{Name: "timeout", Regex: `i/o timeout|dial tcp.*timeout|deadline exceeded`},
		{Name: "dns", Regex: `no such host|DNS.*(lookup|resolution) failed|SERVFAIL`},
		{Name: "tls", Regex: `TLS handshake|x509|certificate.*(expired|invalid|unknown)`},
	},
	// oom: tight bundle for memory-pressure investigations, kept separate
	// from crash so a targeted probe doesn't have to sift through unrelated
	// error lines.
	"oom": {
		{Name: "oomkilled", Regex: `OOMKilled|Killed process|memory cgroup.*out of memory`},
		{Name: "alloc", Regex: `OutOfMemory|Cannot allocate memory|malloc failed|runtime: out of memory`},
		{Name: "gc", Regex: `GC overhead|Java heap space|too much memory pressure`},
	},
}

// sortedBundleNames is diagnosticBundles keys in stable (alphabetical) order.
// Populated at init from the map itself so adding a new bundle stays a one-
// place edit; the sort makes every string this tool emits (tool description,
// input-schema enum, unknown-bundle error) deterministic — critical because
// those strings feed the account-scoped LLM prompt cache, and any map-order
// churn would bust that cache on every prompt build.
var sortedBundleNames []string

// StandardDiagnosticGrepTool runs a named pattern bundle against one log file.
// See package doc above.
type StandardDiagnosticGrepTool struct {
	AccountId string
	// shell is the underlying command executor. Injected so tests can swap it
	// for a mock; production wiring uses the real ShellTool. When left nil,
	// Call falls back to a fresh ShellTool{AccountId} — this defends against
	// direct instantiation (tests, custom invokers) that bypasses the
	// factory, which would otherwise panic on the first shell.Call.
	shell core.NBTool
	// EnabledOverride lets tests bypass the LogsStandardGrepEnabled global
	// without mutating package-level config state. When non-nil, its value
	// wins; when nil, Call reads the global flag as usual. Kept as a pointer
	// so an explicit "override to false" is distinguishable from the zero
	// value. Never set in production wiring.
	EnabledOverride *bool
}

func init() {
	// sortedBundleNames is derived from the map itself (single source of
	// truth) and sorted alphabetically. Every string this tool emits (tool
	// description, input-schema enum, unknown-bundle error) iterates this
	// slice instead of the map, so the account-scoped LLM prompt cache
	// stays stable across process restarts — Go's randomised map iteration
	// would otherwise bust it on every prompt build.
	for name := range diagnosticBundles {
		sortedBundleNames = append(sortedBundleNames, name)
	}
	sort.Strings(sortedBundleNames)

	core.RegisterNBToolFactory(ToolStandardDiagnosticGrep, func(accountId string) (core.NBTool, error) {
		return StandardDiagnosticGrepTool{AccountId: accountId, shell: ShellTool{AccountId: accountId}}, nil
	})
}

func (t StandardDiagnosticGrepTool) Name() string {
	return ToolStandardDiagnosticGrep
}

func (t StandardDiagnosticGrepTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t StandardDiagnosticGrepTool) Description() string {
	// Description is what the LLM sees when deciding whether to call this tool.
	// Emphasise (a) that it replaces multiple shell_execute grep turns, and
	// (b) the exact bundle names available. Both are load-bearing for the
	// planner's tool-selection heuristics.
	//
	// Iterates sortedBundleNames (not the map) so the emitted string is
	// deterministic — Description feeds the LLM prompt, which is cached
	// per-account; a randomised name order would bust the cache on every
	// process restart.
	bundleNames := make([]string, 0, len(sortedBundleNames))
	for _, name := range sortedBundleNames {
		bundleNames = append(bundleNames, "`"+name+"`")
	}
	return `Runs a pre-defined bundle of diagnostic grep patterns against a single log file in ONE call, returning one consolidated observation. Use this BEFORE emitting per-pattern shell_execute greps for common failure investigations — it collapses what would otherwise be 6-8 sequential LLM turns into one.

**When to use:** starting a log investigation for "why did service X crash / error / stall / OOM". Pick the closest bundle:
` + strings.Join(bundleDescriptions(), "\n") + `

**When NOT to use:** service-specific or query-specific patterns (e.g. "orders-api trace-id XYZ"). For those, emit ONE ` + "`shell_execute`" + ` action with all your custom greps in the same parallel batch — do not one-turn-per-grep.

**Available bundles:** ` + strings.Join(bundleNames, ", ") + `.

**Output:** each pattern's matches are prefixed with ` + "`[pattern-name]`" + ` so downstream reasoning can attribute lines to categories. Empty bundle (no pattern matched) returns an explicit "no signal" marker — treat that as evidence the failure mode is NOT one of the bundle's categories, not as tool failure.`
}

// bundleDescriptions returns one bullet per bundle in stable alphabetical
// order (via sortedBundleNames) so the tool description hash is byte-stable
// across restarts — cache-hit critical.
func bundleDescriptions() []string {
	out := make([]string, 0, len(sortedBundleNames))
	for _, name := range sortedBundleNames {
		switch name {
		case "crash":
			out = append(out, "- `crash` — default for terminal-state failures: error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP 5xx.")
		case "network":
			out = append(out, "- `network` — connectivity-focused: connection-refused / i/o timeout / DNS lookup / TLS.")
		case "oom":
			out = append(out, "- `oom` — memory-pressure focused: OOMKilled / allocation failures / GC pressure.")
		default:
			out = append(out, "- `"+name+"`")
		}
	}
	return out
}

func (t StandardDiagnosticGrepTool) InputSchema() core.ToolSchema {
	// Same rationale as Description(): the JSON schema is rendered into the
	// LLM prompt; the Enum slice must be byte-stable across restarts so the
	// prompt-cache key doesn't drift with Go's map iteration order.
	bundleNames := make([]any, 0, len(sortedBundleNames))
	for _, name := range sortedBundleNames {
		bundleNames = append(bundleNames, name)
	}
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"bundle": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of the diagnostic bundle to run. See the tool description for what each bundle covers.",
				Enum:        bundleNames,
			},
			"log_file": {
				Type:        core.ToolSchemaTypeString,
				Description: "Relative path (in the workspace directory) or absolute path of the log file to search.",
			},
		},
		Required: []string{"bundle", "log_file"},
	}
}

func (t StandardDiagnosticGrepTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// Feature-gate check happens FIRST — before argument parsing — so an
	// operator flipping the flag off gets a fast, predictable response
	// regardless of what the LLM emitted. Also keeps the disabled path free
	// of validation noise in traces. EnabledOverride is honored when set
	// (test-only path) so unit tests don't have to mutate global config.
	enabled := config.Config.LogsStandardGrepEnabled
	if t.EnabledOverride != nil {
		enabled = *t.EnabledOverride
	}
	if !enabled {
		return core.NBToolResponse{
			Data:   "standard_diagnostic_grep is disabled by config; emit shell_execute greps directly (batch them in one parallel action).",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("standard_diagnostic_grep disabled")
	}

	bundle, _ := input.Arguments["bundle"].(string)
	logFile, _ := input.Arguments["log_file"].(string)
	// Lowercased on ingest so an LLM emission of "Crash" or "OOM" resolves
	// to the same map entry as "crash"/"oom" — the LLM shouldn't lose a
	// tool call to trivial casing drift. log_file stays as-emitted (paths
	// are case-sensitive on Linux, so any normalisation here would break
	// legitimate lookups).
	bundle = strings.ToLower(strings.TrimSpace(bundle))
	logFile = strings.TrimSpace(logFile)
	if bundle == "" || logFile == "" {
		return core.NBToolResponse{
			Data:   "standard_diagnostic_grep: both 'bundle' and 'log_file' are required.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("missing bundle or log_file")
	}
	// Defense-in-depth path validation: the bundle runs against a workspace
	// file path, which should always be a workspace-relative name (produced
	// by saveLogsToWorkspace). Reject absolute paths and any parent-directory
	// traversal so an untrusted caller cannot read /etc/passwd or escape the
	// workspace. filepath.Clean normalises `foo/../bar` before the ".." check.
	cleaned := filepath.Clean(logFile)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") || strings.ContainsAny(cleaned, "\x00") {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("standard_diagnostic_grep: invalid log_file path %q — must be a workspace-relative path without '..' or null bytes.", logFile),
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("invalid log_file path")
	}

	patterns, ok := diagnosticBundles[bundle]
	if !ok {
		// Enumerate from sortedBundleNames (stable order) so the error
		// message is deterministic across restarts — helps the LLM retry
		// with the same known-good list every time.
		return core.NBToolResponse{
			Data:   fmt.Sprintf("standard_diagnostic_grep: unknown bundle %q. Known bundles: %s.", bundle, strings.Join(sortedBundleNames, ", ")),
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("unknown bundle %q", bundle)
	}

	// Compose one shell invocation that runs every pattern in the bundle
	// and prefixes each match with `[pattern-name] ` for downstream
	// attribution. `|| true` on each grep keeps grep-exit-1 (no match) from
	// short-circuiting the compound command — an empty section is meaningful
	// signal, not a failure to record.
	//
	// File-existence pre-check: without it, a wrong-path log_file returns
	// an empty grep chain + the end-marker, which the LLM would honestly
	// but incorrectly read as "no crash signal found" — a silent false
	// negative on the audit trail. The pre-check emits a distinct
	// "does not exist" line to stderr and bails with exit 1, giving the
	// LLM an actionable observation to retry with the correct path. The
	// wording avoids the "not found" substring on purpose — some
	// environment-capability detectors treat "not found" as a shell
	// command-not-found signal and would misclassify this application-
	// level miss as an infrastructure fault.
	// Build the compound command as a slice + join with "; ". Two reasons
	// over string-concat with in-loop separators:
	//   1. An empty `patterns` slice would leave a stray "; ;" in the older
	//      shape (guard-clause ending in `; ` + no loop iterations + trailing
	//      end-marker with leading `; `), which is a POSIX syntax error. The
	//      slice+join composition can't produce empty statements.
	//   2. `parts` is easier to reason about — every element is a complete
	//      standalone command, no interior separators.
	quotedFile := shellSingleQuote(logFile)
	parts := make([]string, 0, len(patterns)+2)
	// printf, not echo, for the diagnostic message. `echo` behavior is
	// implementation-defined for arbitrary strings: some builds interpret
	// backslash escapes (`\n`, `\t`) in the arg, and a filename starting
	// with `-n`/`-e` is parsed as an echo flag. `printf %s` is POSIX-
	// portable and treats the arg as a literal string end-to-end. The
	// single-quoted format string keeps the shell from expanding anything;
	// the filename is safely single-quoted in quotedFile.
	parts = append(parts,
		fmt.Sprintf("[ -f %s ] || { printf 'standard_diagnostic_grep: log file %%s does not exist\\n' %s >&2; exit 1; }",
			quotedFile, quotedFile),
		// -r follows -f because "exists" and "readable" are distinct failure
		// modes with different remediations (fix the path vs. fix the
		// permissions). Without the -r guard, an unreadable file's grep
		// output would silently be empty (stderr redirected to /dev/null)
		// and the LLM would confidently report "no signal", a false negative.
		fmt.Sprintf("[ -r %s ] || { printf 'standard_diagnostic_grep: log file %%s is not readable\\n' %s >&2; exit 1; }",
			quotedFile, quotedFile),
	)
	for _, p := range patterns {
		// -E extended regex, -i case-insensitive, -n prefix with line number,
		// head caps per-pattern output at 20 lines so a chatty pattern can't
		// drown the others; total output is bounded to ~160 lines for an
		// 8-pattern bundle, well inside the observation-truncation ceiling.
		// `--` before the file separates grep options from positional args
		// so a legitimately-named log file starting with '-' (e.g. from a
		// tool that saved logs with a leading dash) is treated as a path,
		// not an option flag. Combined with shellSingleQuote for shell
		// injection, this closes the corresponding option-injection surface.
		// The sed argument is built with three characters escaped in p.Name:
		//   `/`  — sed's default delimiter; unescaped would close s///
		//   `&`  — sed replacement-string special (means "matched text")
		//   `\`  — sed escape character; unescaped would eat the next char
		// The whole `s///` expression is then fed through shellSingleQuote so
		// a hypothetical apostrophe in a bundle name can't break out of the
		// shell's quoting. All current bundle names are simple identifiers,
		// so every escape here is defense-in-depth. Backslash escaping goes
		// FIRST — later replacers would otherwise double-escape their own
		// backslashes.
		safeName := strings.NewReplacer(
			`\`, `\\`,
			`/`, `\/`,
			`&`, `\&`,
		).Replace(p.Name)
		sedExpr := fmt.Sprintf("s/^/[%s] /", safeName)
		// Compose grep flags per-pattern so word-boundary patterns get -w
		// while others keep just -Ein. The composed string looks like
		// "grep -Ein" or "grep -Einw" (a valid POSIX flag cluster).
		grepFlags := "-Ein"
		if p.WordRegexp {
			grepFlags += "w"
		}
		parts = append(parts, fmt.Sprintf(
			"{ grep %s -- %s %s 2>/dev/null | head -n 20 | sed %s; } || true",
			grepFlags,
			shellSingleQuote(p.Regex),
			quotedFile,
			shellSingleQuote(sedExpr),
		))
	}
	// Trailing marker so an empty-signal bundle still returns a distinguishable
	// observation instead of an empty string (which the planner may misread as
	// tool failure).
	parts = append(parts, "echo '---standard_diagnostic_grep:end---'")
	compound := strings.Join(parts, "; ")

	// Delegate to ShellTool for the actual workspace execution. Reuses the
	// per-conversation working directory, credential injection, and error-
	// envelope handling — nothing about grep needs its own workspace pathway.
	// Nil-shell fallback covers direct-instantiation callers (tests, custom
	// invokers) that bypass the factory — panicking on the first Call is a
	// worse failure mode than a lazy ShellTool with the tool's AccountId.
	shell := t.resolveShell()
	// input.Context carries free-form call context (tool_config_name,
	// notebook hints, etc.) that some tools read; propagate it verbatim so
	// the delegated ShellTool sees the same call envelope the primitive
	// received.
	req := core.NBToolCallRequest{Command: compound, Context: input.Context}
	resp, err := shell.Call(nbCtx, req)
	if err != nil {
		return resp, fmt.Errorf("standard_diagnostic_grep: shell exec failed: %w", err)
	}
	// Preserve the shell's exit-status + duration metadata verbatim; annotate
	// which bundle was run so audit UI can group observations by bundle.
	if resp.Metadata == nil {
		resp.Metadata = &core.NBToolResponseMetadata{}
	}
	if resp.AdditionalDetails == nil {
		resp.AdditionalDetails = map[string]any{}
	}
	resp.AdditionalDetails["bundle"] = bundle
	resp.AdditionalDetails["pattern_count"] = len(patterns)
	return resp, nil
}

// resolveShell returns t.shell when set (tests injecting a mock), or a
// fresh ShellTool bound to the tool's AccountId otherwise. Extracted so a
// unit test can pin the nil-fallback branch without spinning up the whole
// ShellTool workspace machinery.
func (t StandardDiagnosticGrepTool) resolveShell() core.NBTool {
	if t.shell != nil {
		return t.shell
	}
	return ShellTool{AccountId: t.AccountId}
}

// shellSingleQuote wraps a string in single quotes for safe interpolation
// into an sh -c command. Any embedded single-quote closes the quoting,
// appends an escaped single quote, and reopens — POSIX safe with no need
// for shell-specific escapes. Kept package-private so we don't accidentally
// leak an unsafe quoter into other tool files.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
