package agents

import (
	"nudgebee/llm/config"
	"regexp"
	"strings"
)

// This file is logs_v3's OWN copy of the mode-classification and
// shared-instruction-block logic that agent_log.go (the `logs` v1 agent)
// owns. It started as a verbatim reuse of agent_log.go's helpers (see the old
// GetSystemPrompt doc comment in agent_log_v3.go) but that coupling meant any
// v3-specific curation had to fight text v1 still depends on — the exact
// failure mode that let sharedHeaderAndWorkflow's/routineInstructions'
// NL-phrasing framing silently outlast fastPathAppAnchor's fix (both told the
// model to "phrase the NL question", so patching one bullet wasn't enough).
//
// Everything here is v3-owned and safe to edit freely: agent_log.go (and
// `logs` v1's behavior) is never touched by changes in this file. Names are
// suffixed V3 to avoid colliding with agent_log.go's identical-purpose
// unexported symbols in the same package.
//
// Divergences from the agent_log.go originals, deliberate:
//   - sharedHeaderAndWorkflowV3 names the real tool names directly
//     (resource_search_execute, fetch_logs_v3) instead of "resource_search" /
//     "fetch_logs" + a separate patch note (resourceSearchToolNote, removed).
//   - routineInstructionsV3 + labelAnchorRulesV3 lead with the canonical-JSON
//     fast path (canonicalQueryAuthoringForRoutine) and demote NL phrasing to
//     the explicit fallback, instead of presenting NL phrasing as the default
//     the way v1's routineInstructions does (v1 has no canonical-JSON path at
//     all, so NL-first is correct there).
//   - investigationInstructionsV3 / enumerationInstructionsV3 /
//     outputFormatInstructionsV3 / sharedConstraintsV3 are currently
//     byte-identical to their agent_log.go originals (mode names, tool name)
//     — kept as separate copies so they can diverge later without touching
//     v1, not because they differ today.

// logModeV3 mirrors agent_log.go's logMode — the deterministic intent bucket
// the Go classifier emits, so logs_v3's prompt carries a single authoritative
// MODE flag instead of asking the LLM to classify on the fly.
type logModeV3 int

const (
	logModeRoutineV3 logModeV3 = iota
	logModeInvestigationV3
	logModeEnumerationV3
)

func logModeNameV3(m logModeV3) string {
	switch m {
	case logModeInvestigationV3:
		return "INVESTIGATION"
	case logModeEnumerationV3:
		return "ENUMERATION"
	default:
		return "ROUTINE"
	}
}

// Classifier patterns — identical to agent_log.go's enumerationVerbsRE /
// investigationVerbsRE. Own copies so a future v3-specific wording tweak
// doesn't need to reason about v1's classifier behavior.
var (
	enumerationVerbsREV3   = regexp.MustCompile(`(?i)\b(show|list|summari[sz]e|categori[sz]e|enumerate)\b.{0,30}\b(error|errors|failures|exceptions|issues)\b|what\s+(kinds?\s+of\s+)?errors|\bdistinct\s+errors?\b`)
	investigationVerbsREV3 = regexp.MustCompile(`(?i)\bwhy\b|\broot\s+cause\b|\bdiagnos\w*\b|\btroubleshoot\w*\b|\bdebug\w*\b|\binvestigat\w*\b|\bwhat\s+(caused|happened|went\s+wrong|broke|broken)\b|\bwere\s+there\b|\bbroken\b|\bfailing\b|\bcrash\w*\b`)
)

// classifyLogModeV3 mirrors agent_log.go's classifyLogMode exactly: the
// verbatim user question (OriginalQuery) wins over a parent planner's
// per-step paraphrase.
func classifyLogModeV3(query, originalQuery string) logModeV3 {
	q := strings.TrimSpace(originalQuery)
	if q == "" {
		q = strings.TrimSpace(query)
	}
	if enumerationVerbsREV3.MatchString(q) {
		return logModeEnumerationV3
	}
	if investigationVerbsREV3.MatchString(q) {
		return logModeInvestigationV3
	}
	return logModeRoutineV3
}

// sharedHeaderAndWorkflowV3 returns the role + steps 1, 2, 2a (resource
// resolve, fetch, name-resolution recovery) — applies to every mode. Names
// resource_search_execute and fetch_logs_v3 directly (v3's real tool names),
// so there is no separate patch-note block to keep in sync.
func sharedHeaderAndWorkflowV3() []string {
	step2 := "  2. **Fetch the logs** by calling `fetch_logs_v3` with a natural-language question that includes the resolved resource and time window. `fetch_logs_v3` translates the question into the right backend query (Loki, Datadog, Elasticsearch, or kubectl) and runs it. The response is a JSON envelope with the rendered query and the raw logs."
	if canonicalFastPathEnabled() {
		step2 = "  2. **Fetch the logs** by calling `fetch_logs_v3` with the resolved resource and time window. Exactly how to phrase the call — natural-language question, or (ROUTINE mode) canonical JSON — is your mode's own step 3 below; follow that, not a generic default. `fetch_logs_v3` runs the query against the right backend (Loki, Datadog, Elasticsearch, or kubectl). The response is a JSON envelope with the rendered query and the raw logs."
	}
	return []string{
		"**Role:** You are an SRE expert that retrieves and investigates logs from configured backends. Your goal is to answer the user's question accurately, citing concrete log evidence.",

		"**Workflow:**",
		"  1. **Resolve the resource** the user is asking about. Use `resource_search_execute` when ANY of these apply:",
		"     a. The name is ambiguous (no namespace, partial match, common service name).",
		"     b. The name looks like a **workload** (Deployment / StatefulSet / DaemonSet / Job) but you need pod logs. Workload-managed pods have hash suffixes (the Deployment→ReplicaSet→Pod pattern: `<workload-name>-<6-10 hex>-<5 alphanumeric>`). A bare workload name without that suffix is NOT a valid pod name; calling `fetch_logs_v3` with `pod=<workload-name>` resolves to zero entries because Kubernetes pod names always carry the suffix.",
		"     c. The user gave a service / app / deployment name and didn't specify a pod.",
		"     Skip resource_search_execute ONLY when either (1) the framework-generated `<resolved_targets>` block carries `status=\"confirmed\"`, exact pod member names, and their namespace, or (2) the question itself already carries one or more exact resolved pod names — each has the hash suffix described above, OR follows a StatefulSet ordinal pattern (`<sts-name>-<integer>`, e.g. `kafka-0`, `mysql-0`) — together with their namespace. A `candidate` target alone never suppresses discovery. Reuse every confirmed/supplied pod directly; do not rediscover a workload the parent already resolved. If a direct fetch later reports not found, Step 2a remains mandatory and safely re-resolves stale pods.",
		"     Call it with structured JSON matching its own schema — `{\"resource_name\": \"<name>\", \"namespace\": \"<ns>\", \"search_type\": \"suggestions\"}` — not a free-text command string. `suggestions` is the right mode for this workflow — it resolves a workload to its live pods (and their owner references) in one lookup — and it is also what the tool falls back to when `search_type` is absent, so a call that omits it still behaves correctly. Pass it explicitly anyway. Omit `namespace` only when the user genuinely didn't give one.",
		step2,
		"  2b. **Read the response metadata before you shell out.** The inline logs end in one of two bracketed markers, and the `logs_complete` field says the same thing machine-readably. `[... complete — all matching lines are shown above ...]` (`logs_complete: true`) means `file_ref` holds nothing you have not been given: answer from the inline logs, and do NOT call shell_execute. `[... N more bytes truncated ...]` (`logs_complete: false`) means `file_ref` has more — and the inline portion is the FIRST lines of that same file in EXACTLY the same format. Follow `logs_format_hint`: for `timestamp_tab_message`, read line-by-line and split once on TAB; never JSON-decode the whole file. You never need a separate `head` call to discover the layout.",
		"  2a. **Recovery from name resolution failure.** If `fetch_logs_v3` returns an error containing \"pod not found\", \"(NotFound)\", \"no resources found\", or similar — the resource name didn't resolve. You MUST:",
		"     - Call `resource_search_execute` with the original name and namespace: `{\"resource_name\": \"<name>\", \"namespace\": \"<ns>\", \"search_type\": \"suggestions\"}`.",
		"     - Take the resolved pod name (with hash suffix) from the search result.",
		"     - Re-call `fetch_logs_v3` with the resolved pod name.",
		"     Do NOT give up after one failed fetch. Do NOT fall back to a narrower query. Do NOT report \"no issues\" based on a fetch that errored — the fetch failure means we have no log evidence yet, so any conclusion about health is unsupported.",
	}
}

// canonicalFastPathEnabled gates logs_v3's ROUTINE-mode canonical-JSON fast
// path — see LogsV3CanonicalFastPathEnabled's doc comment (config/config.go)
// for the full rationale. Checked at every call site that varies its prompt
// text or behavior based on the fast path (fastPathAppAnchor,
// routineInstructionsV3, labelAnchorRulesV3, fetchLogsV3Tool.Description,
// fetchLogsV3Tool.Call, GetSystemPrompt) so flipping it off reproduces the
// pre-fast-path NL-only behavior exactly, not just a subset of it.
func canonicalFastPathEnabled() bool {
	return config.Config.LogsV3CanonicalFastPathEnabled
}

// routineInstructionsV3 returns the body for MODE = ROUTINE: a single small
// fetch, no shell_execute pass, no widening. When the canonical fast path is
// enabled, step 3 leads with it (canonicalQueryAuthoringForRoutine, appended
// earlier in GetSystemPrompt) instead of presenting NL phrasing as the
// default — v1's routineInstructions has no canonical-JSON path so NL-first
// is correct there, but repeating that framing here is what let the old
// shared text silently out-compete the new fast-path instructions. When
// disabled, step 3 reverts to the original NL-only phrasing verbatim.
func routineInstructionsV3() []string {
	fastPath := canonicalFastPathEnabled()
	step3 := "  3. **Single fetch is enough.** Phrase the NL question using the right label depending on whether the user gave a hashed pod name or an app/deployment name (see Label-anchor rules below). Keep limits small (200-1000 lines)."
	if fastPath {
		step3 = "  3. **Single fetch is enough — call `fetch_logs_v3` with canonical JSON, not a sentence.** This applies whether you resolved namespace + app/pod via resource_search (step 1/1c) OR the user already gave you an exact pod name (step 1's skip-resource_search case) — either way, build `{\"where\": {...}, \"time_range\": \"...\", \"limit\": <n>}` per the fast-path rules above and call `fetch_logs_v3` with it directly; that skips its internal NL-translation call. This is the ROUTINE-mode default, not an optional shortcut — fall back to a natural-language `command` ONLY if you cannot map the resolved resource to a canonical field name (see the Label-anchor rules below for the equivalent NL phrasing). Keep limits small (200-1000 lines)."
	}
	return []string{
		"**Workflow (Routine):**",
		step3,
		labelAnchorRulesV3(fastPath),
		"  4. **Read the fetch response — including the pre-computed bundle_signal.** The fetch_logs_v3 envelope carries a `bundle_signal` field. When it's non-empty (the user asked for error content), it holds a category-tagged sweep (error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP-5xx) computed server-side against the saved file — use its output as the body of your answer without issuing your own grep. When it's empty, answer from the inline logs directly.",
		"  5. **Envelope is the answer.** Rely entirely on the fetch response (inline logs + `bundle_signal` when present) for your reply. The envelope already contains the categorised signal, so a follow-up shell_execute grep would only re-derive what you already have.",
		"  6. **No widening on empty result.** If the small fetch returns nothing for the user's window, say so explicitly — this mode is for routine viewing, not investigation.",
	}
}

// investigationInstructionsV3 returns the body for MODE = INVESTIGATION:
// currently byte-identical to agent_log.go's investigationInstructions
// (mode names + tool name already match — v3 has no separate fetch_logs_v3
// naming issue here since this text already used the generic phrasing).
// Kept as its own copy per the file doc comment: free to diverge later
// without touching v1.
func investigationInstructionsV3() []string {
	out := []string{
		"**Workflow (Investigation):**",
		"  3. **Broad first-pass fetch (newest-first).** You MUST phrase the fetch with `last 24h, limit 5000`. The `time_range` and `limit` are MANDATORY — a bare phrasing like \"recent logs for X\" produces a narrow 1h/1000 default that misses errors which happened earlier in the pod's lifetime (cron schedulers, replay-style fixtures, jobs that backfill historical data at startup all routinely emit errors hours before the test runtime). A 24h/5000 fetch covers the full pod lifecycle for most workloads.",
		labelAnchorRulesV3(false),
		"     - Do NOT include words like \"errors\", \"exceptions\", \"failures\", \"5xx\" in the fetch_logs_v3 question (those force a body filter at the database that excludes the surrounding context).",
		"     - For very chatty services that hit the 5000 limit on a 24h window, narrow by container or stream — never by error keyword.",
	}

	if config.Config.LogsStandardGrepEnabled {
		out = append(out,
			"  4a. **Read `bundle_signal` first (fastest path).** The fetch_logs_v3 envelope carries a `bundle_signal` field with a pre-computed server-side sweep across error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP-5xx categories against the saved file. When it's non-empty and shows a clear signal (matches concentrated in one category with a visible transition timestamp), skip step 4 below and go straight to the WHEN report. When it's empty (bundle didn't fire — query wording wasn't error-adjacent) or thin (matches scattered across categories, no clear timestamp cluster), fall through to step 4 for the manual Sweep A+B pass and any domain-aware patterns. Use the provided bundle output directly — the server has already run the sweep.",
		)
	}
	out = append(out,
		"  4. **Mandatory diagnostic sweep (NOT optional for this mode).** When `fetch_logs_v3` returns a non-empty `file_ref`, you MUST sweep the file before producing a final answer — as **exactly ONE `shell_execute` call whose command chains every pattern you want**. Do NOT emit one `shell_execute` per pattern: each extra action costs a full planning turn (~5s) to run work the shell finishes in milliseconds, and patterns split across actions reliably end up in separate turns rather than one. One command, one round trip, every result in a single observation:",
		"     **Never batch a `fetch_logs_v3` call together with a `shell_execute` grep on the file_ref it is expected to return — not here, not anywhere else in this workflow.** `file_ref` only exists once `fetch_logs_v3` actually finishes; a grep queued in the same iteration cannot know the real value and will target a file that hasn't been written yet, producing `No such file or directory` even though your command is otherwise correct. Always let a `fetch_logs_v3` call finish and observe its `file_ref` before grepping it, in the NEXT iteration.",
		"     The sweep has two halves. Chain them in ONE command with a separator so you can tell the outputs apart:",
		"       `grep -nE \"ERROR|FATAL|PANIC|exception|traceback\" <file_ref> | head -20; echo '--- TRIGGER ---'; grep -nE \"reload|reconfigur|deploy|rollout|secret.rotat|config.changed|started|loaded|env|config\" <file_ref> | head -20`",
		"     Sweep A (before the separator) — the error transition: note the timestamp on the first matching line, that is the transition point.",
		"     Sweep B (after the separator) — the trigger: it usually lives in WARN/INFO/CONFIG lines that Sweep A skipped. Pick the trigger line that immediately precedes Sweep A's transition point.",
		"     **Tailor both pattern sets to the question actually asked** — the defaults above target generic crash-shaped failures and are a STARTING POINT, not a fixed list. A latency question wants `timeout|deadline|slow|context.deadline|elapsed`; an auth question wants `401|403|token|expired|unauthorized`; a question naming a specific id, host, or endpoint should grep for that literal. Add, replace, or drop patterns as the question warrants — but keep it to the SAME single command.",
		"  5. **Second-pass targeted antecedent fetch (only if Sweep A finds an error timestamp).** Call fetch_logs_v3 again with `\"all logs for <pod> in <namespace> from <T-5min> to <T>, limit 500, in chronological forward order\"`. This pulls the trigger context (config reload, deploy, secret rotation) immediately preceding the error — bounded by explicit `start_time`/`end_time`, so forward+limit is safe. Wait for this call to complete and observe its `file_ref`, THEN re-run Sweep B's grep on it as a separate, later iteration — never in the same batch as this fetch_logs_v3 call. Skip if Sweep B already found a clear trigger.",
		"  6. **Empty-result recovery (MANDATORY two-step — DO NOT skip):**",
		"     Step E1 — re-grep with case-insensitive + broader keywords on the SAME file:",
		"       `grep -inE \"error|fail|fatal|panic|exception|traceback|connection.refused|connectionerror|cannot.connect|timed.out|timeout|denied|unreachable\" <file_ref> | head -20`",
		"       The default Sweep A pattern is case-sensitive and misses mixed-case wording (e.g. `ConnectionError`, `Connection refused`) and protocol idioms (`timed out`, `unreachable`).",
		"     Step E2 — only if Step E1 is also empty, widen the fetch:",
		"       Re-call fetch_logs_v3 with `\"all logs for <pod> in <namespace> last 7d, limit 5000\"` (widen the *window*, not the limit — 5000 is the recommended max across providers). Wait for it to complete, THEN re-run the Step E1 grep on the new file_ref in a later iteration — never in the same batch as this fetch_logs_v3 call.",
		"       If the user's question or any earlier observation mentions a specific timestamp, ALSO issue a narrower targeted fetch around that timestamp.",
		"     **Strict prohibitions:**",
		"       - Do NOT substitute kubectl events, deployment status, or rollout history for a wider log fetch. Events ≠ logs. The critic rejects \"no issues\" answers based on event checks alone.",
		"       - Do NOT call `think` to conclude \"no issues\" without completing Step E1 AND (if E1 empty) Step E2.",
		"       - Only after Sweep A + Step E1 + Step E2 all return empty may you state \"no errors found in last 7d\".",
		"  Do NOT produce a final answer without Sweep A AND (Sweep B succeeding OR a documented second-pass attempt OR a documented widened-window attempt). The critic will reject answers that skip this step.",
		"  **Report WHEN (MANDATORY).** Your final answer MUST state the incident time window — the `timestamp` field of the FIRST and LAST matching error line from Sweep A. Naming the failure and its cause without \"between <T1> and <T2>\" is incomplete. Do NOT conflate live pod status with incident timing: a pod that is `Running` now can have had a bounded PAST incident — phrase it as \"errors occurred between <T1> and <T2>\", never \"currently failing\" when the error timestamps are in the past.",

		"**Domain-aware grep patterns (use when the default Sweep B is empty, or when the question points at one of these areas — add them to the SAME single command as extra chained greps, never as extra shell_execute actions):**",
		"  - Crashes / restarts:  `OOM|Killed|exit.code|CrashLoop|restart|signal|SIGTERM|SIGKILL`",
		"  - Latency / slowness:  `timeout|deadline|slow|connection.pool|retry|context.deadline|elapsed`",
		"  - Auth / access:       `401|403|JWT|token|expired|unauthorized|forbidden|permission`",
		"  - Memory / resources:  `OOM|heap|memory|allocation|GC|garbage.collect`",
		"  - Network:             `connection.refused|ECONNREFUSED|DNS|resolve|unreachable|reset`",
		"  - Database:            `deadlock|lock.timeout|too.many.connections|query.timeout|replication`",

		"**Useful shell_execute shapes (FILE = file_ref returned by fetch_logs_v3):**",
		"  - Shape & frequency:  `wc -l FILE; grep -c \"ERROR\\|WARN\\|FATAL\" FILE`",
		"  - Context ±5 lines:   `grep -n -B5 -A5 \"SPECIFIC\" FILE`",
		"  - Frequency table:    `grep -oE \"PATTERN\" FILE | sort | uniq -c | sort -rn | head -20`",
		"  - Time bookends:      `head -5 FILE; tail -5 FILE`",
		"  - Window slice:       `awk '/HH:MM:SS/,/HH:MM:SS/' FILE` to slice a time range",
	)
	return out
}

// enumerationInstructionsV3 returns the body for MODE = ENUMERATION —
// currently byte-identical to agent_log.go's enumerationInstructions aside
// from the fetch_logs_v3 tool name. Kept as its own copy per the file doc
// comment.
func enumerationInstructionsV3() []string {
	out := []string{
		"**Workflow (Enumeration):**",
		"  3. **Broad fetch (no error-keyword body filter).** Phrase with `last 1h, limit 2000` (or the user-specified window). The user wants a comprehensive list of distinct error categories, NOT a single root-cause narrative.",
		labelAnchorRulesV3(false),
		"     - Do NOT include error keywords (`errors`, `exceptions`, `failures`) in the question — that adds a body filter that excludes the surrounding context.",
	}

	if config.Config.LogsStandardGrepEnabled {
		out = append(out,
			"  3a. **Read `bundle_signal` first (fastest path for enumeration).** The fetch_logs_v3 envelope carries a `bundle_signal` field with a pre-computed server-side category sweep against the saved file. When it's non-empty, present those categories directly in your final answer using their tag names (error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP-5xx). Then run step 4 below only for signatures the bundle didn't cover (custom application errors, service-specific tokens) — focus step 4 exclusively on the tail patterns absent from the bundle output. When `bundle_signal` is empty (bundle didn't fire — query wording wasn't error-adjacent) start with step 4.",
		)
	}
	out = append(out,
		"  4. **Per-signature aggregation pipeline (MANDATORY — without this you miss error categories):**",
		"     ```",
		"     grep -iE 'error|fail|warn|exception|fatal|timeout|refused|denied' <file_ref> \\",
		"       | sed -E 's/[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9:.]+Z?//g; s/[0-9a-f]{8}-[0-9a-f-]+//g; s/[0-9]+(ms|s|MB|KB|GB)//g; s/\\b[0-9]+\\b/N/g' \\",
		"       | sort | uniq -c | sort -rn | head -20",
		"     ```",
		"     Step 1 (grep) filters to error-class lines. Step 2 (sed) normalises timestamps, UUIDs/hex IDs, and digit runs so messages that differ only by timestamp or request-id collapse to one signature. Step 3 (sort | uniq -c | sort -rn) ranks the distinct signatures by frequency. The pipeline operates on the saved log file directly — it does NOT need a JSON-aware extractor (the file is one entry per line, with the message field as part of the line text).",
		"  5. **Output:** lead with `\"Found N distinct error categories across M occurrences\"`, then list each signature with its count and a verbatim example line. Include EVERY distinct signature from the pipeline output — do NOT drop any. Do NOT speculate about root cause. Do NOT recommend fixes.",
	)
	return out
}

// outputFormatInstructionsV3 — identical to agent_log.go's
// outputFormatInstructions. Own copy per the file doc comment.
func outputFormatInstructionsV3(mode logModeV3) []string {
	out := []string{
		"**Output format:**",
		"  Concise markdown. Lead with the answer. Cite specific evidence from the logs (exact lines, timestamps, error signatures). Prefer log fragments over prose paraphrase — the user wants the actual evidence.",
	}
	if mode == logModeInvestigationV3 {
		out = append(out,
			"  Include a short Causality Chain explaining symptom → why → root cause.",
			"  **Time-window framing (mandatory when error timestamps cluster):** if the error lines you cite span a contiguous window (typically a few minutes — i.e. an `HH:MM–HH:MM` span of 1-15 minutes), state the window explicitly in your answer (\"errors occurred between HH:MM and HH:MM\").",
			"  How to determine the window bounds — all three in ONE `shell_execute` call, chained (the saved file is sorted newest-first because Loki's default direction is backward, so `head` gives newest and `tail` gives oldest):",
			"    `grep -nE \"<error pattern>\" <file_ref> | head -1; grep -nE \"<error pattern>\" <file_ref> | tail -1; grep -cE \"<error pattern>\" <file_ref>`",
			"    That yields, in order: window END (most recent error), window START (earliest error in the matched set), and the total count that tells you \"cluster\" vs \"isolated\".",
			"  You MUST have BOTH bounds before reporting a window — do not issue them as three separate actions, and do not report from `head` alone. A single `head -20` only shows the newest end of the cluster, which lets the user think the issue is recent when it actually started minutes or hours earlier. Cite the START and END timestamps verbatim from the grep output — do not round or approximate. If the errors are scattered (no contiguous cluster — i.e. the head/tail span is hours instead of minutes), say so and list the distinct timestamps.",
		)
	}
	return out
}

// labelAnchorRulesV3 tells the LLM how to anchor a fetch on the right
// resource. canonicalFirst is true only for ROUTINE mode when the fast path
// is enabled (routineInstructionsV3 passes canonicalFastPathEnabled();
// investigationInstructionsV3/enumerationInstructionsV3 always pass false —
// those modes never had a canonical-JSON fast path, their fetch strategy is
// broader/multi-step and needs the NL framing to carry intent like
// time-window/limit). When true, leads with "prefer the canonical-JSON fast
// path" and demotes the NL phrasing guidance to the explicit fallback form,
// matching routineInstructionsV3's step 3. When false, this is byte-identical
// to agent_log.go's labelAnchorRules.
func labelAnchorRulesV3(canonicalFirst bool) string {
	if !canonicalFirst {
		return "     - **Label anchor (CRITICAL — picks the right target):** If you ALREADY have the exact pod name (Deployment-style hash suffix `<workload>-<6-10 hex>-<5 alphanumeric>`, e.g. from `kubectl get pods` or `resource_search_execute`), anchor on `\"all logs for pod <name> in <namespace>\"` — the exact pod resolves on EVERY backend, including kubectl. If you do NOT have the exact pod, DEFAULT to the workload label by phrasing as `\"all logs for app <name> in <namespace>\"` (emits `app=<name>`, matches all pods) — BUT this app-anchor only resolves on label-indexed backends (Loki / Datadog / Elasticsearch). On the **kubectl backend** (no Loki configured — you had to use `kubectl get` / `shell_execute` to find the resource), a bare `app <name>` is NOT a valid target and errors; there, phrase as `\"all logs for deployment <name> in <namespace>\"` (or statefulset/daemonset), which runs `kubectl logs deployment/<name>`. Never anchor on `pod <bare-workload-name>` without the hash suffix — it yields zero results because entries carry the full pod-with-hash, not the workload name."
	}
	return "     - **Label anchor (CRITICAL — picks the right target):** In ROUTINE mode, prefer the canonical-JSON fast path above — use the canonical field that matches what you have (a pod-field canonical_name when you have the exact pod name with its hash suffix, else the app/workload-field canonical_name). For INVESTIGATION/ENUMERATION, or as a ROUTINE-mode fallback when you're not confident in the canonical JSON: if you ALREADY have the exact pod name (Deployment-style hash suffix `<workload>-<6-10 hex>-<5 alphanumeric>`, e.g. from `kubectl get pods` or `resource_search_execute`), anchor on `\"all logs for pod <name> in <namespace>\"` — the exact pod resolves on EVERY backend, including kubectl. If you do NOT have the exact pod, DEFAULT to the workload label by phrasing as `\"all logs for app <name> in <namespace>\"` (emits `app=<name>`, matches all pods) — BUT this app-anchor only resolves on label-indexed backends (Loki / Datadog / Elasticsearch). On the **kubectl backend** (no Loki configured — you had to use `kubectl get` / `shell_execute` to find the resource), a bare `app <name>` is NOT a valid target and errors; there, phrase as `\"all logs for deployment <name> in <namespace>\"` (or statefulset/daemonset), which runs `kubectl logs deployment/<name>`. Never anchor on `pod <bare-workload-name>` without the hash suffix — it yields zero results because entries carry the full pod-with-hash, not the workload name."
}

// sharedConstraintsV3 — identical to agent_log.go's sharedConstraints. Own
// copy per the file doc comment.
func sharedConstraintsV3(mode logModeV3) []string {
	c := []string{
		"MUST cite concrete log lines, timestamps, or error signatures as evidence. Never speculate beyond the data.",
		"MUST preserve literal identifiers from logs verbatim — `host:port`, exact resource names, error codes, request IDs, time windows. Paraphrasing them away makes the answer unverifiable.",
		"NEVER output raw CLI commands as a to-do for the user. Run them yourself via `fetch_logs_v3` or `shell_execute`.",
	}
	if mode == logModeInvestigationV3 {
		c = append(c,
			"MUST attempt to find the trigger (what changed) for any symptom — not just the symptom itself. \"Connection refused\" is a symptom; \"config reload at 10:05 swapped prod-db for staging-db\" is a root cause.",
		)
	}
	return c
}
