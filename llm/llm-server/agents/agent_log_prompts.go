package agents

import (
	"nudgebee/llm/config"
	"regexp"
	"strings"
)

// This file holds the `logs` agent's own mode-classification and prompt
// content. logModeV3 (and classifyLogModeV3, enumerationVerbsREV3) keep a V3
// suffix solely to avoid colliding with agent_log_fetch.go's own, unrelated
// classifier, which gates the separate fetch_logs tool's auto-diagnostic
// bundle — not because of any versioning relationship between the two.

// logModeV3 is the deterministic intent bucket the Go classifier emits, so
// the `logs` agent's prompt carries a single authoritative MODE flag instead
// of asking the LLM to classify on the fly.
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

// Classifier patterns for this agent's own mode classifier.
var (
	enumerationVerbsREV3 = regexp.MustCompile(`(?i)\b(show|list|summari[sz]e|categori[sz]e|enumerate)\b.{0,30}\b(error|errors|failures|exceptions|issues)\b|what\s+(kinds?\s+of\s+)?errors|\bdistinct\s+errors?\b`)
	investigationVerbsRE = regexp.MustCompile(`(?i)\bwhy\b|\broot\s+cause\b|\bdiagnos\w*\b|\btroubleshoot\w*\b|\bdebug\w*\b|\binvestigat\w*\b|\bwhat\s+(caused|happened|went\s+wrong|broke|broken)\b|\bwere\s+there\b|\bbroken\b|\bfailing\b|\bcrash\w*\b`)
)

// classifyLogModeV3 buckets the user's intent: the verbatim user question
// (OriginalQuery) wins over a parent planner's per-step paraphrase.
func classifyLogModeV3(query, originalQuery string) logModeV3 {
	q := strings.TrimSpace(originalQuery)
	if q == "" {
		q = strings.TrimSpace(query)
	}
	if enumerationVerbsREV3.MatchString(q) {
		return logModeEnumerationV3
	}
	if investigationVerbsRE.MatchString(q) {
		return logModeInvestigationV3
	}
	return logModeRoutineV3
}

// sharedHeaderAndWorkflow returns the role + steps 1, 2, 2a (resource
// resolve, fetch, name-resolution recovery) — applies to every mode. Names
// resource_search_execute and fetch_logs_direct directly (this agent's real tool names),
// so there is no separate patch-note block to keep in sync.
func sharedHeaderAndWorkflow() []string {
	step2 := "  2. **Fetch the logs** by calling `fetch_logs_direct`. A complete query matching the account's default log provider is passed verbatim and takes precedence over canonical JSON. A provider-native template is passed with the concrete values needed to resolve its placeholders. If you loaded relevant user knowledge, copy its relevant query examples, templates, field/index conventions, and mandatory scope constraints verbatim into the optional `knowledge_context`; do not classify them yourself. Otherwise follow your mode's step 3: use natural language, or canonical JSON in ROUTINE mode. The response is a JSON envelope with the rendered query and raw logs."
	return []string{
		"**Role:** You are an SRE expert that retrieves and investigates logs from configured backends. Your goal is to answer the user's question accurately, citing concrete log evidence.",

		"**Workflow:**",
		"  1. **Resolve the resource** the user is asking about. Use `resource_search_execute` when ANY of these apply:",
		"     a. The name is ambiguous (no namespace, partial match, common service name).",
		"     b. The name looks like a **workload** (Deployment / StatefulSet / DaemonSet / Job) but you need pod logs. Workload-managed pods have hash suffixes (the Deployment→ReplicaSet→Pod pattern: `<workload-name>-<6-10 hex>-<5 alphanumeric>`). A bare workload name without that suffix is NOT a valid pod name; calling `fetch_logs_direct` with `pod=<workload-name>` resolves to zero entries because Kubernetes pod names always carry the suffix.",
		"     c. The user gave a service / app / deployment name and didn't specify a pod.",
		"     Skip resource_search_execute ONLY when either (1) the framework-generated `<resolved_targets>` block carries `status=\"confirmed\"`, exact pod member names, and their namespace, or (2) the question itself already carries one or more exact resolved pod names — each has the hash suffix described above, OR follows a StatefulSet ordinal pattern (`<sts-name>-<integer>`, e.g. `kafka-0`, `mysql-0`) — together with their namespace. A `candidate` target alone never suppresses discovery. Reuse every confirmed/supplied pod directly; do not rediscover a workload the parent already resolved. If a direct fetch later reports not found, Step 2a remains mandatory and safely re-resolves stale pods.",
		"     Call it with structured JSON matching its own schema — `{\"resource_name\": \"<name>\", \"namespace\": \"<ns>\", \"search_type\": \"suggestions\"}` — not a free-text command string. `suggestions` is the right mode for this workflow — it resolves a workload to its live pods (and their owner references) in one lookup — and it is also what the tool falls back to when `search_type` is absent, so a call that omits it still behaves correctly. Pass it explicitly anyway. Omit `namespace` only when the user genuinely didn't give one.",
		step2,
		"  2b. **Read the response metadata before you shell out.** The inline logs end in bracketed markers, and the `logs_complete` field says machine-readably whether the inline text is the whole saved file — it is about inline versus `file_ref` only, never about whether the fetch got the whole window (that is `read_coverage`, 2d, and a `[... partial read ...]` marker can sit next to `logs_complete: true`). `[... complete — all fetched lines are shown above ...]` (`logs_complete: true`) means all fetched lines are visible: a simple listing needs no shell call. For requested counts, grouping, correlation, or investigation sweeps not already fully supplied with counts and coverage by the response, process the saved file with Python or shell instead of estimating from inline text. `[... N more bytes truncated ...]` (`logs_complete: false`) means `file_ref` has more — and the inline portion is the FIRST lines of that same file in EXACTLY the same format. Follow `logs_format_hint`: for `timestamp_tab_message`, read line-by-line and split once on TAB; never JSON-decode the whole file. You never need a separate `head` call to discover the layout.",
		"  2c. **Surface `kubectl_disclosure` when present.** On the kubectl backend, the fetch response may carry a `kubectl_disclosure` field — it means what you're seeing was narrowed: a multi-pod workload was read from only one pod, and/or a filter/line-cap was applied server-side. When it's non-empty, state that caveat explicitly in your final answer (which pod(s) went unread, any filter applied) rather than reporting on the narrowed slice as if it were the whole picture. Empty or absent means nothing was narrowed.",
		"  2d. **Surface `read_coverage` when present.** The fetch response carries a `read_coverage` field, and the same text as a `[... partial read ...]` marker at the end of `logs`, when the read stopped at its line limit: it names the span of the lines returned against the window that was asked for. The lines you have are then part of that window, not all of it — never describe them as all the logs for the window, and treat anything in the unread span (when a problem began, what preceded it) as unknown. State the caveat in your final answer with the span actually read and the span that was not, so whoever reads your answer can ask for it. Do not chain further fetches to cover the rest: in INVESTIGATION mode the bounded antecedent fetch of step 5 is the one follow-up read, and when that read is itself partial you report it; in ROUTINE mode you do not fetch again. When the user asked for a number of lines (\"last 200 lines\"), reaching the limit is the request being met and needs no caveat. Absent means only that the limit asked for was not reached.",
		"  2a. **Recovery from name resolution failure.** If `fetch_logs_direct` returns an error containing \"pod not found\", \"(NotFound)\", \"no resources found\", or similar — the resource name didn't resolve. You MUST:",
		"     - Call `resource_search_execute` with the original name and namespace: `{\"resource_name\": \"<name>\", \"namespace\": \"<ns>\", \"search_type\": \"suggestions\"}`.",
		"     - Take the resolved pod name (with hash suffix) from the search result.",
		"     - Re-call `fetch_logs_direct` with the resolved pod name.",
		"     Do NOT give up after one failed fetch. Do NOT fall back to a narrower query. Do NOT report \"no issues\" based on a fetch that errored — the fetch failure means we have no log evidence yet, so any conclusion about health is unsupported.",
	}
}

// routineInstructions returns the body for MODE = ROUTINE: a single small
// fetch, no shell_execute pass, no widening. Canonical query authoring is the
// default; natural language remains a fallback when field mapping is uncertain.
func routineInstructions() []string {
	step3 := "  3. **Single fetch is enough — call `fetch_logs_direct` with canonical JSON, not a sentence.** This applies whether you resolved namespace + app/pod via resource_search (step 1/1c) OR the user already gave you an exact pod name (step 1's skip-resource_search case) — either way, build `{\"where\": {...}, \"time_range\": \"...\", \"limit\": <n>}` per the field rules above and call `fetch_logs_direct` with it. The unified planner checks the query before execution. This is the ROUTINE-mode default — use a natural-language `command` ONLY if you cannot map the resolved resource to a canonical field name (see the Label-anchor rules below for the equivalent NL phrasing). Keep limits small (200-1000 lines)."
	return []string{
		"**Workflow (Routine):**",
		step3,
		labelAnchorRules(true),
		"  4. **Read the fetch response — including the pre-computed bundle_signal.** The fetch_logs_direct envelope carries a `bundle_signal` field. When it's non-empty (the user asked for error content), it holds a category-tagged sweep (error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP-5xx) computed server-side against the saved file — use its output as the body of your answer without issuing your own grep. When it's empty, answer from the inline logs directly.",
		"  5. **Use the envelope for simple listings.** For requested calculations or grouping absent from the envelope, process the saved file once with Python or shell. Otherwise rely on the fetch response (inline logs + `bundle_signal` when present) for your reply. The envelope already contains the categorised signal, so a follow-up shell_execute grep would only re-derive what you already have.",
		"  6. **No widening on empty result — this is a hard stop, not a suggestion.** If `fetch_logs_direct` returns no matching log lines for the user's window, you MUST NOT call `resource_search_execute`, `shell_execute`, or `fetch_logs_direct` again to broaden, re-check, or verify — not a different label, not a wider filter, not raw kubectl, not pod events, not sibling resources. State \"no logs found in <window>\" in your final answer and STOP: make NO further tool calls this turn. This mode is for routine viewing, not investigation — an empty result here is a valid, complete answer, not a signal to dig further.",
	}
}

// investigationInstructions returns the body for MODE = INVESTIGATION.
func investigationInstructions() []string {
	out := []string{
		"**Workflow (Investigation):**",
		"  3. **Broad first-pass fetch (newest-first).** You MUST phrase the fetch with `last 24h, limit 5000`. The `time_range` and `limit` are MANDATORY — a bare phrasing like \"recent logs for X\" produces a narrow 1h/1000 default that misses errors which happened earlier in the pod's lifetime (cron schedulers, replay-style fixtures, jobs that backfill historical data at startup all routinely emit errors hours before the test runtime). A 24h/5000 fetch covers the full pod lifecycle for most workloads.",
		labelAnchorRules(false),
		"     - Do NOT include words like \"errors\", \"exceptions\", \"failures\", \"5xx\" in the fetch_logs_direct question (those force a body filter at the database that excludes the surrounding context).",
		"     - For very chatty services that hit the 5000 limit on a 24h window, narrow by container or stream — never by error keyword.",
	}

	if config.Config.LogsStandardGrepEnabled {
		out = append(out,
			"  4a. **Read `bundle_signal` first.** It contains at most 20 example lines per diagnostic pattern, not complete counts or computed time bounds. Reuse those examples as evidence. A clear error category alone does not establish the incident window or trigger: continue with step 4 for missing counts, parsed bounds, and trigger context. Skip only work already supplied with sufficient coverage by the fetch response or previous analysis; never infer complete coverage from a short bundle.",
		)
	}
	out = append(out,
		"  4. **Complete the diagnostic evidence.** Reuse evidence already available with sufficient coverage. If error bounds/counts or trigger context are missing and file_ref is non-empty, compute the missing evidence in exactly ONE shell_execute call using Python or a shell pipeline. Do not run a separate action for each pattern or repeat completed calculations.",
		"     Wait for fetch_logs_direct to finish and use its actual file_ref; never batch analysis with the fetch that creates its input file.",
		"     In that execution, scan all fetched records, parsing them according to logs_format_hint. Sweep A: match the question-relevant error patterns, count matches, and compute parsed earliest/latest timestamps across all matches. Sweep B: search for relevant trigger context preceding the earliest observed error. Keep exact representative lines and their line numbers; bound displayed examples only AFTER computing counts and time bounds. Return these results together. The saved file may be newest-first or unordered; do not derive bounds from head/tail samples.",
		"     Use the earliest observed match as T for any antecedent fetch. If the fetched window or limit cuts off earlier records, disclose that true onset is unknown. Trigger evidence often lives in WARN/INFO/CONFIG lines; temporal proximity alone does not prove causation.",
		"     **Tailor both pattern sets to the question actually asked** — starting patterns for Sweep A are `error|fatal|panic|exception|traceback`, and for Sweep B `reload|reconfigur|deploy|rollout|secret.rotat|config.changed|started|loaded`; use case-insensitive matching. These are starting points, not a fixed list. A latency question wants `timeout|deadline|slow|context.deadline|elapsed`; an auth question wants `401|403|token|expired|unauthorized`; a question naming a specific id, host, or endpoint should grep for that literal. Add, replace, or drop patterns as the question warrants — but keep it to the SAME single command.",
		"  5. **Second-pass targeted antecedent fetch (only if Sweep A finds an error timestamp).** Call fetch_logs_direct again with `\"all logs for <pod> in <namespace> from <T-5min> to <T>, limit 500, in chronological forward order\"`. This pulls the trigger context (config reload, deploy, secret rotation) immediately preceding the error — bounded by explicit `start_time`/`end_time`, so forward+limit is safe. Wait for this call to complete and observe its `file_ref`, THEN re-run Sweep B's grep on it as a separate, later iteration — never in the same batch as this fetch_logs_direct call. Skip if Sweep B already found a clear trigger.",
		"  6. **Empty-result recovery (MANDATORY two-step — DO NOT skip):**",
		"     Step E1 — re-grep with case-insensitive + broader keywords on the SAME file:",
		"       `grep -inE \"error|fail|fatal|panic|exception|traceback|connection.refused|connectionerror|cannot.connect|timed.out|timeout|denied|unreachable\" <file_ref> | head -20`",
		"       If the broader case-insensitive patterns were already checked across the full fetched file in step 4, reuse that result. Otherwise check them now; a narrower pattern set can miss protocol idioms such as `timed out` or `unreachable`.",
		"     Step E2 — only if Step E1 is also empty, widen the fetch:",
		"       Re-call fetch_logs_direct with `\"all logs for <pod> in <namespace> last 7d, limit 5000\"` (widen the *window*, not the limit — 5000 is the recommended max across providers). Wait for it to complete, THEN re-run the Step E1 grep on the new file_ref in a later iteration — never in the same batch as this fetch_logs_direct call.",
		"       If the user's question or any earlier observation mentions a specific timestamp, ALSO issue a narrower targeted fetch around that timestamp.",
		"     Step E3 — only if Step E2 is also empty, read the file plainly before concluding \"no errors\": `head -100 <file_ref>` (or a representative sample). Every grep pass so far assumed the incident is logged as an error-shaped line — a component that fails quietly (an eviction policy, a rate limiter, a resource ceiling correctly doing its job) can log the actual mechanism at INFO or with no error-shaped word at all, and Sweep A/B/E1 are all built to miss exactly that. A plain, unfiltered read is what catches it. Only conclude \"no errors found\" after this step also shows nothing relevant — not after E2 alone.",
		"     **Strict prohibitions:**",
		"       - Do NOT substitute kubectl events, deployment status, or rollout history for a wider log fetch. Events ≠ logs. The critic rejects \"no issues\" answers based on event checks alone.",
		"       - Do NOT call `think` to conclude \"no issues\" without completing Step E1 AND (if E1 empty) Step E2 AND (if E2 empty) Step E3.",
		"       - Only after Sweep A + Step E1 + Step E2 + Step E3 all return empty may you state \"no errors found in last 7d\".",
		"  Before answering, establish the error evidence and attempt the relevant trigger search, using either sufficiently complete existing results or step 4. If trigger evidence is missing, make the required follow-up attempt and report any unresolved cause. A sampled bundle alone does not satisfy these requirements.",
		"  **Report WHEN (MANDATORY).** When matching errors have usable timestamps, state their observed window using parsed earliest/latest timestamps from complete existing results or Sweep A, with coverage limits disclosed. If no matches or usable timestamps exist, say that no error window could be established; do not invent bounds. Report available bounds explicitly. Do NOT conflate live pod status with incident timing: a pod that is `Running` now can have had a bounded PAST incident — phrase it as \"errors occurred between <T1> and <T2>\", never \"currently failing\" when the error timestamps are in the past.",

		"**Domain-aware grep patterns (use when the default Sweep B is empty, or when the question points at one of these areas — add them to the SAME single command as extra chained greps, never as extra shell_execute actions):**",
		"  - Crashes / restarts:  `OOM|Killed|exit.code|CrashLoop|restart|signal|SIGTERM|SIGKILL`",
		"  - Latency / slowness:  `timeout|deadline|slow|connection.pool|retry|context.deadline|elapsed`",
		"  - Auth / access:       `401|403|JWT|token|expired|unauthorized|forbidden|permission`",
		"  - Memory / resources:  `OOM|heap|memory|allocation|GC|garbage.collect`",
		"  - Network:             `connection.refused|ECONNREFUSED|DNS|resolve|unreachable|reset`",
		"  - Database:            `deadlock|lock.timeout|too.many.connections|query.timeout|replication`",

		"**Useful shell_execute shapes (FILE = file_ref returned by fetch_logs_direct):**",
		"  - Shape & frequency:  `wc -l FILE; grep -c \"ERROR\\|WARN\\|FATAL\" FILE`",
		"  - Context ±5 lines:   `grep -n -B5 -A5 \"SPECIFIC\" FILE`",
		"  - Frequency table:    `grep -oE \"PATTERN\" FILE | sort | uniq -c | sort -rn | head -20`",
		"  - Time bounds: parse timestamps across matching records and compute min/max; head/tail only sample file order.",
		"  - Window slice:       `awk '/HH:MM:SS/,/HH:MM:SS/' FILE` to slice a time range",
	)
	return out
}

// enumerationInstructions returns the body for MODE = ENUMERATION.
func enumerationInstructions() []string {
	out := []string{
		"**Workflow (Enumeration):**",
		"  3. **Broad fetch (no error-keyword body filter).** Phrase with `last 1h, limit 2000` (or the user-specified window). The user wants a comprehensive list of distinct error categories, NOT a single root-cause narrative.",
		labelAnchorRules(false),
		"     - Do NOT include error keywords (`errors`, `exceptions`, `failures`) in the question — that adds a body filter that excludes the surrounding context.",
	}

	if config.Config.LogsStandardGrepEnabled {
		out = append(out,
			"  3a. **Read `bundle_signal` first.** It contains a pre-computed category sweep against the saved file. Reuse it when it fully supplies the requested result. Category examples alone do not establish all distinct signatures or their counts: for those requests, compute the complete requested grouping in step 4, including categories already illustrated in the bundle. Avoid repeating a calculation already supplied with complete counts and coverage.",
		)
	}
	out = append(out,
		"  4. **Per-signature aggregation pipeline (MANDATORY unless the requested grouping and complete counts are already fully supplied by bundle_signal):**",
		"     ```",
		"     grep -iE 'error|fail|warn|exception|fatal|timeout|refused|denied' <file_ref> \\",
		"       | sed -E 's/[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9:.]+Z?//g; s/[0-9a-f]{8}-[0-9a-f-]+//g; s/[0-9]+(ms|s|MB|KB|GB)//g; s/\\b[0-9]+\\b/N/g' \\",
		"       | sort | uniq -c | sort -rn > \"grouped-errors.txt\"",
		"     awk '{groups++; occurrences += $1} END {printf \"distinct=%d occurrences=%d\\n\", groups, occurrences}' \"grouped-errors.txt\"",
		"     head -20 \"grouped-errors.txt\"",
		"     # Use a distinct output artifact path for each input file; link the complete grouped file.",
		"     ```",
		"     Step 1 (grep) filters to error-class lines. Step 2 (sed) normalises timestamps, UUIDs/hex IDs, and digit runs so messages that differ only by timestamp or request-id collapse to one signature. Step 3 (sort | uniq -c | sort -rn) ranks the distinct signatures by frequency. For plain-text messages, save the complete normalized frequency table before display limiting; retain the original file for verbatim examples. For JSON messages, use Python to extract the message and requested fields before normalizing; follow logs_format_hint rather than grouping host/agent metadata as error signatures. Compute totals before applying a top-N display limit.",
		"  5. **Output:** lead with `\"Found N distinct error categories across M occurrences\"`, then list each signature with its count and a verbatim example line. State the total distinct-signature count before display limiting. If showing only the top signatures, label that subset and link the full grouped artifact; include one verbatim example for each displayed signature. Do NOT speculate about root cause. Do NOT recommend fixes.",
	)
	return out
}

// outputFormatInstructions tail — the time-window framing rule fires only for
// investigation, since routine/enumeration don't need a window callout.
func outputFormatInstructions(mode logModeV3) []string {
	out := []string{
		"**Output format:**",
		"  Concise markdown. Lead with the answer. Cite specific evidence from the logs (exact lines, timestamps, error signatures). Show each log excerpt once, not both a table and a repeated raw block. Include bulky host/cloud/agent metadata only when relevant or explicitly requested. Preserve exact evidence and link the full saved artifact; label any abbreviated excerpt and never present rewritten JSON as the original raw record.",
	}
	if mode == logModeInvestigationV3 {
		out = append(out,
			"  Include a short Causality Chain explaining symptom → why → root cause.",
			"  **Coverage line (mandatory when any fetch response carried `read_coverage`):** open your answer with one line stating the span of the lines you actually read against the window you were asked about, and that the rest of that window was not read (for example \"Read 09:32–09:39 of the last 24 h; earlier lines were not read\"). Every count and start time you report holds for that span only — never word a finding as covering the whole window.",
			"  **Time-window framing (mandatory when error timestamps cluster):** if the error lines you cite span a contiguous window (typically a few minutes — i.e. an `HH:MM–HH:MM` span of 1-15 minutes), state the window explicitly in your answer (\"errors occurred between HH:MM and HH:MM\").",
			"  Reuse the earliest/latest matching timestamps and count from your diagnostic pass. If missing, compute all three together in ONE Python script or shell pipeline; parse timestamps instead of assuming file order. You MUST have BOTH bounds before reporting a window, with exact source timestamps and the matched count. A limited head sample cannot establish the full window. If errors are scattered rather than clustered, say so and show their distribution.",
		)
	}
	return out
}

// labelAnchorRules tells the LLM how to anchor a fetch on the right
// resource. canonicalFirst is true only for ROUTINE mode;
// investigationInstructions/enumerationInstructions always pass false —
// those modes use broader, multi-step fetch strategies and need NL framing
// to carry intent like time-window/limit. When true, leads with
// canonical JSON and demotes the NL phrasing guidance to the fallback form,
// matching routineInstructions's step 3.
func labelAnchorRules(canonicalFirst bool) string {
	if !canonicalFirst {
		return "     - **Label anchor (CRITICAL — picks the right target):** If you ALREADY have the exact pod name (Deployment-style hash suffix `<workload>-<6-10 hex>-<5 alphanumeric>`, e.g. from `kubectl get pods` or `resource_search_execute`), anchor on `\"all logs for pod <name> in <namespace>\"` — the exact pod resolves on EVERY backend, including kubectl. If you do NOT have the exact pod, DEFAULT to the workload label by phrasing as `\"all logs for app <name> in <namespace>\"` (emits `app=<name>`, matches all pods) — BUT this app-anchor only resolves on label-indexed backends (Loki / Datadog / Elasticsearch). On the **kubectl backend** (no Loki configured — you had to use `kubectl get` / `shell_execute` to find the resource), a bare `app <name>` is NOT a valid target and errors; there, phrase as `\"all logs for deployment <name> in <namespace>\"` (or statefulset/daemonset), which runs `kubectl logs deployment/<name>`. Never anchor on `pod <bare-workload-name>` without the hash suffix — it yields zero results because entries carry the full pod-with-hash, not the workload name."
	}
	return "     - **Label anchor (CRITICAL — picks the right target):** In ROUTINE mode, prefer canonical JSON above — use the canonical field that matches what you have (a pod-field canonical_name when you have the exact pod name with its hash suffix, else the app/workload-field canonical_name). For INVESTIGATION/ENUMERATION, or as a ROUTINE-mode fallback when you're not confident in the canonical JSON: if you ALREADY have the exact pod name (Deployment-style hash suffix `<workload>-<6-10 hex>-<5 alphanumeric>`, e.g. from `kubectl get pods` or `resource_search_execute`), anchor on `\"all logs for pod <name> in <namespace>\"` — the exact pod resolves on EVERY backend, including kubectl. If you do NOT have the exact pod, DEFAULT to the workload label by phrasing as `\"all logs for app <name> in <namespace>\"` (emits `app=<name>`, matches all pods) — BUT this app-anchor only resolves on label-indexed backends (Loki / Datadog / Elasticsearch). On the **kubectl backend** (no Loki configured — you had to use `kubectl get` / `shell_execute` to find the resource), a bare `app <name>` is NOT a valid target and errors; there, phrase as `\"all logs for deployment <name> in <namespace>\"` (or statefulset/daemonset), which runs `kubectl logs deployment/<name>`. Never anchor on `pod <bare-workload-name>` without the hash suffix — it yields zero results because entries carry the full pod-with-hash, not the workload name."
}

// sharedConstraints — the cite-evidence / preserve-identifiers rules apply to
// every mode. The "find the trigger" and "stop on empty routine result" rules
// each fire only for their respective mode.
func sharedConstraints(mode logModeV3) []string {
	c := []string{
		"MUST cite concrete log lines, timestamps, or error signatures as evidence. Never speculate beyond the data.",
		"MUST preserve literal identifiers from logs verbatim — `host:port`, exact resource names, error codes, request IDs, time windows. Paraphrasing them away makes the answer unverifiable.",
		"NEVER output raw CLI commands as a to-do for the user. Run them yourself via `fetch_logs_direct` or `shell_execute`.",
	}
	if mode == logModeInvestigationV3 {
		c = append(c,
			"MUST attempt to find the trigger (what changed) for any symptom — not just the symptom itself. \"Connection refused\" is a symptom; \"config reload at 10:05 swapped prod-db for staging-db\" is a root cause.",
		)
	}
	if mode == logModeRoutineV3 {
		c = append(c,
			"MUST NOT call any further tool (resource_search_execute, shell_execute, or another fetch_logs_direct) after a ROUTINE-mode fetch returns zero matching log lines. Report \"no logs found in <window>\" and stop — an empty result is a complete, valid answer in this mode, not a cue to investigate.",
		)
	}
	return c
}

// Python and shell are interchangeable execution choices; evidence and
// recovery obligations are unchanged by the choice of parser.
func logAnalysisExecutionInstructions() []string {
	return []string{
		"**Analyze with code, then explain the evidence:** First reuse any requested result already fully supplied with counts and coverage by the fetch response or previous analysis. Only for missing aggregation, distinct signatures, time bounds, or correlation, use `shell_execute` to run one Python script or one shell pipeline over the saved `file_ref`. Prefer Python for structured JSON messages, timestamp parsing, grouping, and multi-field correlations; use grep/awk for simple text matching. A Python pass may replace the active mode's grep commands. Only INVESTIGATION mode requires its diagnostic sweep, trigger search, and empty-result recovery; do not add these to routine listings/counts or enumeration. A complete inline routine listing needs no extra tool call.",
		"Wait for fetch_logs_direct to finish and use its actual file_ref. Follow logs_format_hint: timestamp_tab_message is read line-by-line and split once on TAB, then JSON-decode only the message when appropriate; JSONL is decoded one record at a time. Do not JSON-decode a whole TSV file. Treat log text as data, never interpolate it into shell commands or eval it. Quote file paths.",
		"Compute only the outputs needed for the current question and active mode, together in the SAME execution: processed and matched row counts, counts per signature, earliest/latest matching timestamps, relevant trigger context, and a few exact example lines with line numbers. Determine time bounds from parsed timestamps, not assumed file order. Reuse computed results instead of separate head, count, grep and timestamp tool turns. Additional execution is justified by a new hypothesis, missing evidence, or a failed command, not reformatting the same data.",
		"Return compact structured statistics and bounded representative excerpts to the model, not the entire file or repeated per-row infrastructure metadata. Save full grouped results to an artifact and link it when the user needs all groups or raw records. Report malformed/skipped records and fetched-window/limit/truncation coverage; never turn a parse failure into a zero count or claim sampled counts cover all matching backend logs.",
	}
}
