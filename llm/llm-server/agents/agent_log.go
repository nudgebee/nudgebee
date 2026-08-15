package agents

import (
	"fmt"
	"nudgebee/llm/agents/aws"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const LogsAgentName = "logs"

// logMode is the deterministic intent bucket the Go classifier emits. The
// LogAgent's prompt then has a single authoritative MODE flag instead of
// asking the LLM to classify routine vs investigation vs enumeration on the
// fly. The LLM frequently treats a parent's paraphrased "Get logs for X" as
// routine even when OriginalQuery is "Were there issues" — the classifier in
// Go fixes this deterministically.
type logMode int

const (
	logModeRoutine logMode = iota
	logModeInvestigation
	logModeEnumeration
)

func logModeName(m logMode) string {
	switch m {
	case logModeInvestigation:
		return "INVESTIGATION"
	case logModeEnumeration:
		return "ENUMERATION"
	default:
		return "ROUTINE"
	}
}

// Classifier patterns. Investigation has two tiers so precedence against
// enumeration can differ by strength of signal:
//
//   - Enumeration:        "show / list / summarise / enumerate" verb
//     co-occurring with an error/symptom noun within
//     ~30 chars, plus "what kinds of errors" and
//     "distinct errors" idioms. The noun list includes
//     symptom words (failing/broken/crash*), not just
//     "error"-family nouns, so "list the pods that are
//     failing" matches here rather than falling through
//     to the weak investigation tier below.
//   - strongInvestigationRE: causal verbs/phrases (why / root cause /
//     diagnose / troubleshoot / debug / investigate,
//     plus inflections; "what caused/happened/went
//     wrong/broke", "were there"). These checked BEFORE
//     enumeration — a query naming both a causal ask and
//     a listing verb ("show me the errors and explain why
//     it broke") is an investigation that happens to
//     mention "show", not an enumeration.
//   - weakSymptomRE:        bare "broken" / "failing" / "crash*" with no
//     causal verb and no enumeration match — checked
//     AFTER enumeration, so a plain listing request
//     ("list failing pods") isn't misrouted into the
//     heavier investigation workflow just because it
//     names a symptom instead of the word "error".
var (
	strongInvestigationRE = regexp.MustCompile(`(?i)\bwhy\b|\broot\s+cause\b|\bdiagnos\w*\b|\btroubleshoot\w*\b|\bdebug\w*\b|\binvestigat\w*\b|\bwhat\s+(caused|happened|went\s+wrong|broke|broken)\b|\bwere\s+there\b`)
	enumerationVerbsRE    = regexp.MustCompile(`(?i)\b(show|list|summari[sz]e|categori[sz]e|enumerate)\b.{0,30}\b(errors?|fail\w*|exceptions?|issues?|broken|crash\w*)\b|what\s+(kinds?\s+of\s+)?errors|\bdistinct\s+errors?\b`)
	weakSymptomRE         = regexp.MustCompile(`(?i)\bbroken\b|\bfail\w*\b|\bcrash\w*\b`)
)

// classifyLogMode buckets the user's intent. The verbatim user question
// (OriginalQuery) wins over per-step paraphrases from a parent planner —
// "Get logs for X" emitted by a parent does not erase the user's "why is X
// broken" intent. When OriginalQuery is empty we fall back to Query.
func classifyLogMode(query, originalQuery string) logMode {
	q := strings.TrimSpace(originalQuery)
	if q == "" {
		q = strings.TrimSpace(query)
	}
	if strongInvestigationRE.MatchString(q) {
		return logModeInvestigation
	}
	if enumerationVerbsRE.MatchString(q) {
		return logModeEnumeration
	}
	if weakSymptomRE.MatchString(q) {
		return logModeInvestigation
	}
	return logModeRoutine
}

func init() {
	core.RegisterNBAgentFactory(LogsAgentName, func(accountId string) (core.NBAgent, error) {
		return getLogAgent(security.NewRequestContextForSuperAdmin(), accountId)
	})
	toolcore.RegisterNBToolFactory(LogsAgentName, func(accountId string) (toolcore.NBTool, error) {
		return LogAgentTool{}, nil
	})
}

// getLogAgent resolves the account's observability provider; empty (or
// unresolved) routes fetch_logs to the kubectl path.
func getLogAgent(ctx *security.RequestContext, accountId string) (core.NBAgent, error) {
	provider, err := tools.GetLogProvider(accountId)
	if err != nil {
		ctx.GetLogger().Warn("log: unable to resolve log provider, defaulting to kubectl-only path", "error", err)
		provider = services_server.ObservabilityProvider{}
	}
	// Cloud-only GCP/Azure accounts with no first-class log provider read logs
	// via the cloud CLI (Cloud Logging / Log Analytics) instead of the generic
	// LogAgent's kubectl/Loki path. GetLogProvider resolves these from the
	// account's cloud type (see cloudFallbackProvider).
	switch strings.ToLower(strings.TrimSpace(provider.Provider)) {
	case "aws":
		return aws.NewAwsLogsAgent(accountId), nil
	case "gcp":
		return newGcpLogsAgent(accountId), nil
	case "azure":
		return newAzureLogsAgent(accountId), nil
	}
	if provider.Provider == "k8s" {
		provider = services_server.ObservabilityProvider{}
	}
	return newLogAgent(accountId, provider), nil
}

// LogAgent is the ReAct log investigator. Tools exposed to its LLM are
// uniform across providers (resource_search_execute, fetch_logs, optional
// shell_execute). Per-backend dispatch happens inside fetch_logs.
type LogAgent struct {
	accountId string
	provider  services_server.ObservabilityProvider

	toolsOnce sync.Once
	tools     []toolcore.NBTool
}

func newLogAgent(accountId string, provider services_server.ObservabilityProvider) *LogAgent {
	return &LogAgent{accountId: accountId, provider: provider}
}

func (l *LogAgent) GetName() string { return LogsAgentName }

func (l *LogAgent) GetNameAliases() []string { return []string{"Logs"} }

func (l *LogAgent) GetDescription() string {
	return `Retrieves and analyzes logs from various sources (Kubernetes, Loki, Elasticsearch, Datadog, Signoz) by translating natural language questions into log queries. Handles its own resource discovery (e.g., finding the correct pod name or namespace) and runs investigation loops over saved log files when the user is asking about root causes. Use this for: fetching application or container logs, searching log entries by keyword or time range, troubleshooting pod/container errors via log output, correlating logs across services. Do NOT use for: querying performance metrics (use ` + "`metrics`" + ` agent), running kubectl commands (use ` + "`kubectl`" + ` or ` + "`kubectl_execute`" + `), or querying Kubernetes events (use ` + "`events`" + ` agent).

When invoking this agent, preserve the user's intent wording in the per-step query:
  - Investigation — phrasings that contain causal words (why / root cause / diagnose / troubleshoot / what caused / broken / failing / crash) or that ask whether issues, outages, or failures occurred. Keep that wording; the agent's classifier uses it to pick the multi-grep investigation workflow with mandatory Sweep A + E1/E2 recovery.
  - Enumeration — phrasings like "list errors", "show errors", "distinct errors", "summarize errors". Preserve; routes to the per-signature aggregation flow.
  - Routine — phrasings like "recent logs", "tail logs", "last N minutes". Preserve; routes to a single small fetch.
Paraphrasing an investigation as a flat "get logs for <workload>" downgrades it to routine and skips the deeper analysis.`
}

func (l *LogAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetModelCategory explicitly opts into Retrieval, matching agentModelCategory's default for untagged agents.
func (l *LogAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}

func (l *LogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	l.toolsOnce.Do(func() {
		l.tools = l.buildToolList(ctx)
	})
	return l.tools
}

// logAgentToolNames is the logs agent's tool set as a plain name list, so a
// test can lock the resource-search split without standing up a populated
// tool registry. Mirrors cloudLeanCoreToolNames.
func logAgentToolNames() []string {
	return []string{
		// resource_search_execute is the DIRECT tool, not the resource_search
		// AGENT. The agent spends an extra LLM call turning natural language
		// into a tool call, and its prompt is written to fan that call out to
		// cloud (AWS/GCP/Azure) and Datadog resource search alongside the K8s
		// one — both wasted work when all we need is the pod behind a
		// workload. The logs agent already carries the resource name and
		// namespace from the user's question, so it can fill the tool's input
		// itself. Same swap #36109 made for the AWS orchestrators.
		//
		// Note the cost profile differs from that AWS change:
		// cloud_resource_search_execute is pure-DB, whereas this tool is
		// DB-first with a live-kubectl fallback (plus a strategy loop and an
		// owner-reference enrichment pass) when the inventory misses. A hit is
		// one Postgres query; a miss is still several relay round-trips.
		tools.ToolResourceSearch,
		FetchLogsAgentName,
		toolcore.ToolExecuteShellCommand,
	}
}

func (l *LogAgent) buildToolList(ctx *security.RequestContext) []toolcore.NBTool {
	names := logAgentToolNames()
	// standard_diagnostic_grep is no longer exposed to the LLM — the bundle
	// now fires deterministically inside fetch_logs when the query wording
	// asks for error content, and the result rides back in the fetch
	// envelope's `bundle_signal` field. Advertising the tool here would let
	// the LLM issue a redundant second call for the bundle it already has
	// in hand. See runAutoDiagnosticBundle in agent_log_fetch.go.
	var tl []toolcore.NBTool
	for _, name := range names {
		if t, ok := toolcore.GetNBTool(l.accountId, name); ok {
			tl = append(tl, t)
		}
	}
	return tl
}

func (l *LogAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// Parents (k8s_debug, etc.) often paraphrase the user's investigative
	// question into a routine-looking sub-step ("Get recent logs for X"). The
	// per-step query the LLM receives in its human turn loses the original
	// intent. We classify in Go (over OriginalQuery first, then Query) and
	// build a prompt containing ONLY the relevant mode's workflow — rather
	// than presenting all three modes and asking the LLM to pick (which it
	// does inconsistently — treats "Get logs for X" as routine even when
	// OriginalQuery is "Were there issues with X"). Single-mode prompts are
	// shorter, faster to generate, and remove the failure mode of the LLM
	// following the wrong block.
	mode := classifyLogMode(query.Query, query.OriginalQuery)

	var instructions []string
	instructions = append(instructions,
		fmt.Sprintf("**MODE = %s** (set deterministically from the user's verbatim question; workflow below is narrowed to this mode only).", logModeName(mode)),
	)
	if orig := strings.TrimSpace(query.OriginalQuery); orig != "" && orig != strings.TrimSpace(query.Query) {
		instructions = append(instructions,
			fmt.Sprintf("**Original user question:** %q", orig),
		)
	}

	instructions = append(instructions, sharedHeaderAndWorkflow()...)

	switch mode {
	case logModeInvestigation:
		instructions = append(instructions, investigationInstructions()...)
	case logModeEnumeration:
		instructions = append(instructions, enumerationInstructions()...)
	default:
		instructions = append(instructions, routineInstructions()...)
	}

	instructions = append(instructions, outputFormatInstructions(mode)...)

	return core.NBAgentPrompt{
		Role:         "an SRE expert that investigates logs across multiple backends",
		Instructions: instructions,
		Constraints:  sharedConstraints(mode),
	}
}

// sharedHeaderAndWorkflow returns the role + steps 1, 2, 2a (resource resolve,
// fetch, name-resolution recovery) — applies to every mode.
func sharedHeaderAndWorkflow() []string {
	return []string{
		"**Role:** You are an SRE expert that retrieves and investigates logs from configured backends. Your goal is to answer the user's question accurately, citing concrete log evidence.",

		"**Workflow:**",
		"  1. **Resolve the resource** the user is asking about. Use `resource_search_execute` when ANY of these apply:",
		"     a. The name is ambiguous (no namespace, partial match, common service name).",
		"     b. The name looks like a **workload** (Deployment / StatefulSet / DaemonSet / Job) but you need pod logs. Workload-managed pods have hash suffixes (the Deployment→ReplicaSet→Pod pattern: `<workload-name>-<6-10 hex>-<5 alphanumeric>`). A bare workload name without that suffix is NOT a valid pod name; calling `fetch_logs` with `pod=<workload-name>` resolves to zero entries because Kubernetes pod names always carry the suffix.",
		"     c. The user gave a service / app / deployment name and didn't specify a pod.",
		"     Skip resource_search_execute ONLY when the user gave an obvious pod name — already has the hash suffix described above, OR follows a StatefulSet ordinal pattern (`<sts-name>-<integer>`, e.g. `kafka-0`, `mysql-0`).",
		"     Call it with the workload name and namespace directly: `{\"resource_name\": \"<name>\", \"namespace\": \"<ns>\", \"search_type\": \"suggestions\"}`. `suggestions` is the right mode for this workflow — it resolves a workload to its live pods (and their owner references) in one lookup — and it is also what the tool falls back to when `search_type` is absent, so a call that omits it still behaves correctly. Pass it explicitly anyway. Omit `namespace` only when the user genuinely didn't give one.",
		"  2. **Fetch the logs** by calling `fetch_logs` with a natural-language question that includes the resolved resource and time window. `fetch_logs` translates the question into the right backend query (Loki, Datadog, Elasticsearch, or kubectl) and runs it. The response is a JSON envelope with the rendered query and the raw logs.",
		"  2b. **Read the marker at the end of `logs` before you shell out.** The inline logs end in one of two bracketed markers, and the `logs_complete` field says the same thing machine-readably. `[... complete — all matching lines are shown above ...]` (`logs_complete: true`) means `file_ref` holds nothing you have not been given: answer from the inline logs, and do NOT call shell_execute. `[... N more bytes truncated ...]` (`logs_complete: false`) means `file_ref` has more — and the inline portion is the FIRST lines of that same file in EXACTLY the same line format, so write your filter directly from it. You never need a separate `head` call to discover the layout.",
		"  2a. **Recovery from name resolution failure.** If `fetch_logs` returns an error containing \"pod not found\", \"(NotFound)\", \"no resources found\", or similar — the resource name didn't resolve. You MUST:",
		"     - Call `resource_search_execute` with the original name and namespace: `{\"resource_name\": \"<name>\", \"namespace\": \"<ns>\", \"search_type\": \"suggestions\"}`.",
		"     - Take the resolved pod name (with hash suffix) from the search result.",
		"     - Re-call `fetch_logs` with the resolved pod name.",
		"     Do NOT give up after one failed fetch. Do NOT fall back to a narrower query. Do NOT report \"no issues\" based on a fetch that errored — the fetch failure means we have no log evidence yet, so any conclusion about health is unsupported.",
	}
}

// routineInstructions returns the body for MODE = ROUTINE: a single small
// fetch, no shell_execute pass, no widening. Investigation/enumeration blocks
// are not included.
//
// The fetch_logs envelope carries a pre-computed `bundle_signal` field when
// the user's query wording asked for error content — the LLM just reads it
// out of the response, no separate tool call needed.
func routineInstructions() []string {
	return []string{
		"**Workflow (Routine):**",
		"  3. **Single fetch is enough.** Phrase the NL question using the right label depending on whether the user gave a hashed pod name or an app/deployment name (see Label-anchor rules below). Keep limits small (200-1000 lines).",
		labelAnchorRules(),
		"  4. **Read the fetch response — including the pre-computed bundle_signal.** The fetch_logs envelope carries a `bundle_signal` field. When it's non-empty (the user asked for error content), it holds a category-tagged sweep (error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP-5xx) computed server-side against the saved file — use its output as the body of your answer without issuing your own grep. When it's empty, answer from the inline logs directly.",
		"  5. **Envelope is the answer.** Rely entirely on the fetch response (inline logs + `bundle_signal` when present) for your reply. The envelope already contains the categorised signal, so a follow-up shell_execute grep would only re-derive what you already have.",
		"  6. **No widening on empty result.** If the small fetch returns nothing for the user's window, say so explicitly — this mode is for routine viewing, not investigation.",
	}
}

// investigationInstructions returns the body for MODE = INVESTIGATION: broad
// 24h/5000 fetch, mandatory shell_execute Sweep A, optional Sweep B/second-pass
// antecedent fetch, mandatory E1/E2 recovery on empty grep, time-window
// framing for clustered errors. Routine and enumeration blocks are not
// included.
func investigationInstructions() []string {
	out := []string{
		"**Workflow (Investigation):**",
		"  3. **Broad first-pass fetch (newest-first).** You MUST phrase the fetch with `last 24h, limit 5000`. The `time_range` and `limit` are MANDATORY — a bare phrasing like \"recent logs for X\" produces a narrow 1h/1000 default that misses errors which happened earlier in the pod's lifetime (cron schedulers, replay-style fixtures, jobs that backfill historical data at startup all routinely emit errors hours before the test runtime). A 24h/5000 fetch covers the full pod lifecycle for most workloads.",
		labelAnchorRules(),
		"     - Do NOT include words like \"errors\", \"exceptions\", \"failures\", \"5xx\" in the fetch_logs question (those force a body filter at the database that excludes the surrounding context).",
		"     - For very chatty services that hit the 5000 limit on a 24h window, narrow by container or stream — never by error keyword.",
	}

	// The fetch_logs envelope carries `bundle_signal` — a pre-computed
	// category sweep that runs deterministically server-side whenever
	// the query wording asks for error content. That obsoletes the old
	// "please call standard_diagnostic_grep first" step and saves an
	// LLM turn. If the bundle signal is clear the LLM can answer from
	// it directly; if it's thin or ambiguous, the manual Sweep A + B
	// pass below still runs.
	if config.Config.LogsStandardGrepEnabled {
		out = append(out,
			"  4a. **Read `bundle_signal` first (fastest path).** The fetch_logs envelope carries a `bundle_signal` field with a pre-computed server-side sweep across error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP-5xx categories against the saved file. When it's non-empty and shows a clear signal (matches concentrated in one category with a visible transition timestamp), skip step 4 below and go straight to the WHEN report. When it's empty (bundle didn't fire — query wording wasn't error-adjacent) or thin (matches scattered across categories, no clear timestamp cluster), fall through to step 4 for the manual Sweep A+B pass and any domain-aware patterns. Use the provided bundle output directly — the server has already run the sweep.",
		)
	}
	out = append(out,
		"  4. **Mandatory diagnostic sweep (NOT optional for this mode).** When `fetch_logs` returns a non-empty `file_ref`, you MUST sweep the file before producing a final answer — as **exactly ONE `shell_execute` call whose command chains every pattern you want**. Do NOT emit one `shell_execute` per pattern: each extra action costs a full planning turn (~5s) to run work the shell finishes in milliseconds, and patterns split across actions reliably end up in separate turns rather than one. One command, one round trip, every result in a single observation:",
		"     **Never batch a `fetch_logs` call together with a `shell_execute` grep on the file_ref it is expected to return — not here, not anywhere else in this workflow.** `file_ref` only exists once `fetch_logs` actually finishes; a grep queued in the same iteration cannot know the real value and will target a file that hasn't been written yet, producing `No such file or directory` even though your command is otherwise correct. Always let a `fetch_logs` call finish and observe its `file_ref` before grepping it, in the NEXT iteration.",
		"     The sweep has two halves. Chain them in ONE command with a separator so you can tell the outputs apart:",
		"       `grep -nE \"ERROR|FATAL|PANIC|exception|traceback\" <file_ref> | head -20; echo '--- TRIGGER ---'; grep -nE \"reload|reconfigur|deploy|rollout|secret.rotat|config.changed|started|loaded|env|config\" <file_ref> | head -20`",
		"     Sweep A (before the separator) — the error transition: note the timestamp on the first matching line, that is the transition point.",
		"     Sweep B (after the separator) — the trigger: it usually lives in WARN/INFO/CONFIG lines that Sweep A skipped. Pick the trigger line that immediately precedes Sweep A's transition point.",
		"     **Tailor both pattern sets to the question actually asked** — the defaults above target generic crash-shaped failures and are a STARTING POINT, not a fixed list. A latency question wants `timeout|deadline|slow|context.deadline|elapsed`; an auth question wants `401|403|token|expired|unauthorized`; a question naming a specific id, host, or endpoint should grep for that literal. Add, replace, or drop patterns as the question warrants — but keep it to the SAME single command.",
		"  5. **Second-pass targeted antecedent fetch (only if Sweep A finds an error timestamp).** Call fetch_logs again with `\"all logs for <pod> in <namespace> from <T-5min> to <T>, limit 500, in chronological forward order\"`. This pulls the trigger context (config reload, deploy, secret rotation) immediately preceding the error — bounded by explicit `start_time`/`end_time`, so forward+limit is safe. Wait for this call to complete and observe its `file_ref`, THEN re-run Sweep B's grep on it as a separate, later iteration — never in the same batch as this fetch_logs call. Skip if Sweep B already found a clear trigger.",
		"  6. **Empty-result recovery (MANDATORY two-step — DO NOT skip):**",
		"     Step E1 — re-grep with case-insensitive + broader keywords on the SAME file:",
		"       `grep -inE \"error|fail|fatal|panic|exception|traceback|connection.refused|connectionerror|cannot.connect|timed.out|timeout|denied|unreachable\" <file_ref> | head -20`",
		"       The default Sweep A pattern is case-sensitive and misses mixed-case wording (e.g. `ConnectionError`, `Connection refused`) and protocol idioms (`timed out`, `unreachable`).",
		"     Step E2 — only if Step E1 is also empty, widen the fetch:",
		"       Re-call fetch_logs with `\"all logs for <pod> in <namespace> last 7d, limit 5000\"` (widen the *window*, not the limit — 5000 is the recommended max across providers). Wait for it to complete, THEN re-run the Step E1 grep on the new file_ref in a later iteration — never in the same batch as this fetch_logs call.",
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

		"**Composing shell_execute commands (FILE = file_ref returned by fetch_logs).** You have a full shell — `grep`, `awk`, `sed`, `sort`, `uniq`, `wc`, `head`, `tail`, pipes and `;` are all available. Every distinct question you have about the file can be answered in ONE call: add another clause, not another action. Each separate action costs a planning turn (~5s) to run work the shell does in milliseconds, so a command that answers four questions is four times cheaper than four commands.",
		"  - Several probes at once, labelled so you can tell the output apart:",
		"      `echo '== COUNTS =='; wc -l FILE; grep -cE \"ERROR|FATAL\" FILE; echo '== FIRST/LAST =='; head -3 FILE; tail -3 FILE; echo '== ERRORS =='; grep -nE \"ERROR|FATAL|PANIC\" FILE | head -20`",
		"  - One pass, several categories, using awk instead of repeated greps:",
		"      `awk '/ERROR|FATAL/{e++; if(!f)f=$0; l=$0} /reload|deploy|config.changed/{t[++n]=$0} END{print \"errors=\"e; print \"first=\"f; print \"last=\"l; for(i=1;i<=n&&i<=10;i++) print \"trigger: \"t[i]}' FILE`",
		"  - Frequency of distinct signatures, timestamps and ids normalised away:",
		"      `grep -iE \"error|fail|exception\" FILE | sed -E 's/[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9:.]+Z?//g; s/[0-9a-f-]{8,}//g; s/\\b[0-9]+\\b/N/g' | sort | uniq -c | sort -rn | head -20`",
		"  - Context around a specific line, plus the window it sits in:",
		"      `grep -n -B5 -A5 \"SPECIFIC\" FILE; echo '== WINDOW =='; awk '/HH:MM:SS/,/HH:MM:SS/' FILE | head -40`",
		"  These are shapes to adapt, not commands to copy — combine whichever clauses answer the question you actually have.",
	)
	return out
}

// enumerationInstructions returns the body for MODE = ENUMERATION: broad
// fetch + per-signature aggregation, no causal narrative. Routine and
// investigation blocks are not included.
func enumerationInstructions() []string {
	out := []string{
		"**Workflow (Enumeration):**",
		"  3. **Broad fetch (no error-keyword body filter).** Phrase with `last 1h, limit 2000` (or the user-specified window). The user wants a comprehensive list of distinct error categories, NOT a single root-cause narrative.",
		labelAnchorRules(),
		"     - Do NOT include error keywords (`errors`, `exceptions`, `failures`) in the question — that adds a body filter that excludes the surrounding context.",
	}

	// The fetch_logs envelope's `bundle_signal` field already gives us
	// a category-tagged sweep for the well-known failure axes (crash /
	// network / oom / …) — exactly what enumeration wants. Present
	// those categories directly, then use the manual per-signature
	// pipeline (step 4) to pick up tail signatures the bundle doesn't
	// know about (custom app errors, service-specific tokens).
	if config.Config.LogsStandardGrepEnabled {
		out = append(out,
			"  3a. **Read `bundle_signal` first (fastest path for enumeration).** The fetch_logs envelope carries a `bundle_signal` field with a pre-computed server-side category sweep against the saved file. When it's non-empty, present those categories directly in your final answer using their tag names (error / fatal / panic / OOM / timeout / connection-refused / TLS / HTTP-5xx). Then run step 4 below only for signatures the bundle didn't cover (custom application errors, service-specific tokens) — focus step 4 exclusively on the tail patterns absent from the bundle output. When `bundle_signal` is empty (bundle didn't fire — query wording wasn't error-adjacent) start with step 4.",
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

// outputFormatInstructions tail — the time-window framing rule fires only
// for investigation, since routine/enumeration don't need a window callout.
func outputFormatInstructions(mode logMode) []string {
	out := []string{
		"**Output format:**",
		"  Concise markdown. Lead with the answer. Cite specific evidence from the logs (exact lines, timestamps, error signatures). Prefer log fragments over prose paraphrase — the user wants the actual evidence.",
	}
	if mode == logModeInvestigation {
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

// labelAnchorRules tells the LLM how to phrase the fetch_logs NL question so
// the downstream Loki/Signoz/ES translator picks the right label.
//
// Defaults to `app=<name>` because:
//   - It works for Deployments, StatefulSets, and DaemonSets — Loki entries
//     always carry both `app` and `pod` labels.
//   - It matches all pods of the workload, which is what the user almost
//     always wants when they give a workload name.
//
// Uses `pod=<name>` ONLY when the name has the unambiguous Deployment hash
// suffix `<workload>-<6-10 hex>-<5 alnum>`. We deliberately drop a
// StatefulSet-ordinal exception (`<sts>-<integer>`) — names like
// `<workload>-N` are ambiguous (could be a StatefulSet pod OR a deployment
// whose name ends in a number, like a benchmark suffix). Defaulting to
// `app=` covers both cases without false-positive `pod=` lookups.
func labelAnchorRules() string {
	return "     - **Label anchor (CRITICAL — picks the right target):** If you ALREADY have the exact pod name (Deployment-style hash suffix `<workload>-<6-10 hex>-<5 alphanumeric>`, e.g. from `kubectl get pods` or `resource_search_execute`), anchor on `\"all logs for pod <name> in <namespace>\"` — the exact pod resolves on EVERY backend, including kubectl. If you do NOT have the exact pod, DEFAULT to the workload label by phrasing as `\"all logs for app <name> in <namespace>\"` (emits `app=<name>`, matches all pods) — BUT this app-anchor only resolves on label-indexed backends (Loki / Datadog / Elasticsearch). On the **kubectl backend** (no Loki configured — you had to use `kubectl get` / `shell_execute` to find the resource), a bare `app <name>` is NOT a valid target and errors; there, phrase as `\"all logs for deployment <name> in <namespace>\"` (or statefulset/daemonset), which runs `kubectl logs deployment/<name>`. Never anchor on `pod <bare-workload-name>` without the hash suffix — it yields zero results because entries carry the full pod-with-hash, not the workload name."
}

// sharedConstraints — the cite-evidence / preserve-identifiers rules apply to
// every mode. The "find the trigger" rule fires only for investigation.
func sharedConstraints(mode logMode) []string {
	c := []string{
		"MUST cite concrete log lines, timestamps, or error signatures as evidence. Never speculate beyond the data.",
		"MUST preserve literal identifiers from logs verbatim — `host:port`, exact resource names, error codes, request IDs, time windows. Paraphrasing them away makes the answer unverifiable.",
		"NEVER output raw CLI commands as a to-do for the user. Run them yourself via `fetch_logs` or `shell_execute`.",
	}
	if mode == logModeInvestigation {
		c = append(c,
			"MUST attempt to find the trigger (what changed) for any symptom — not just the symptom itself. \"Connection refused\" is a symptom; \"config reload at 10:05 swapped prod-db for staging-db\" is a root cause.",
		)
	}
	return c
}

// LogAgentTool wraps LogAgent so other agents can invoke `logs` as a tool.
// Handles workspace file save for large responses.
type LogAgentTool struct{}

func (m LogAgentTool) Name() string {
	return LogsAgentName
}

func (m LogAgentTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeAgent
}

func (m LogAgentTool) Description() string {
	return `Retrieves and analyzes logs from various sources (Kubernetes, Loki, Elasticsearch, Datadog) by translating natural language questions into log queries. This tool handles its own resource discovery (e.g., finding the correct pod name or namespace). Use this for: fetching application or container logs, searching log entries by keyword or time range, troubleshooting pod/container errors via log output, and correlating logs across services. Do NOT use for: querying performance metrics (use ` + "`" + `metrics` + "`" + ` agent instead), running kubectl commands (use ` + "`" + `kubectl` + "`" + ` or ` + "`" + `kubectl_execute` + "`" + `), or querying Kubernetes events (use ` + "`" + `events` + "`" + ` agent). Returns log data and summaries based on your query.

	Calling guidance — phrase the question to match the user's intent:

	* **Investigation** (root cause / what went wrong): preserve the user's investigative wording — phrasings that contain words like "why", "diagnose", "troubleshoot", "root cause", "what caused", "broken", "failing", "crash", or that ask whether issues / outages / failures occurred. DO NOT paraphrase such a question to a flat fetch like "get logs for <workload>" — that downgrades the agent to a shallow routine fetch and skips the multi-grep investigation flow that finds rare errors and trigger context. The agent picks its workflow from this wording.
	* **Enumeration** (list / show me errors): preserve wording like "list all distinct errors in <workload>", "summarize errors in <workload>", "what kinds of errors appear in <workload>". Routes to the per-signature aggregation flow.
	* **Routine** (recent viewing): "show me last <window> logs for <workload>", "tail logs for <workload>". Routes to a single small fetch.

	Usage:

	* Input: Provide a question in natural language. Match the user's intent verbatim — investigation wording, enumeration wording, or a routine fetch.
	* Output: Markdown answer with cited log evidence (timestamps, error signatures). For investigations, includes a 5-Why causality chain and a time-window callout when errors cluster.
	`
}

func (m LogAgentTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Log Query Question",
			},
			"output_file": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Optional: Path in the workspace to save the raw log data (e.g., 'logs.json'). Relative paths are saved in the conversation directory.",
			},
		},
		Required: []string{"command"},
	}
}

func (m LogAgentTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	agent, err := getLogAgent(nbRequestContext.Ctx, nbRequestContext.AccountId)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Info("log: unable to get logsAgent", "error", err.Error())
		return toolcore.NBToolResponse{}, err
	}

	resp, err := core.ExecuteAgentToolCall(nbRequestContext, agent, input)
	return buildLogToolResponse(nbRequestContext, agent, input, resp, err)
}

// buildLogToolResponse shapes ExecuteAgentToolCall's result into the
// NBToolResponse this tool returns. Split out from Call() so the shaping
// logic — in particular that every return path carries AdditionalDetails —
// is directly unit-testable against hand-built resp/err values, without
// needing to run the real LogAgent (LLM calls, DB-backed
// ExecuteAgentToolCall). See TestBuildLogToolResponse_AdditionalDetails.
func buildLogToolResponse(nbRequestContext toolcore.NbToolContext, agent core.NBAgent, input toolcore.NBToolCallRequest, resp core.NBAgentResponse, err error) (toolcore.NBToolResponse, error) {
	// Use the canonical "agent_id"/"message_id" keys the planner-callback
	// persistence layer reads (planner_callback_handler.go, factory_agent.go)
	// to populate child_agent_id on the saved tool_call row — without them the
	// UI conversation tree (conversation_tree.go) can't nest this call's
	// sub-tool calls (fetch_logs, shell_execute, resource_search_execute) under the
	// `logs` node. This wrapper bypasses factory_agent's generic Call(), which
	// sets these on every return path; agent_delegate.go hit and fixed the
	// same bug class for delegate_agent.
	additionalDetails := map[string]any{
		"agent_id":   resp.AgentId,
		"message_id": resp.MessageId,
	}
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("log: unable to process events request", "error", err, "input", input)
		return toolcore.NBToolResponse{AdditionalDetails: additionalDetails, References: resp.References}, err
	}

	if len(resp.Response) == 0 {
		return toolcore.NBToolResponse{AdditionalDetails: additionalDetails, References: resp.References}, toolcore.ErrUnableToFetchData
	}

	logData := resp.Response[0]

	// Workspace artifacts are saved exactly once — by FetchLogsAgent's
	// saveLogsToWorkspace inside fetch_logs (raw JSONL, gated on non-empty
	// payload). The previous double-save here (synthesised markdown, with
	// different content and different gating rules) produced a second
	// workspace file per query. We propagate fetch_logs's file_ref upward
	// via resp.References so the UI / parent planner sees one canonical
	// artifact.
	//
	// We honour caller-requested `output_file` saves only when the
	// resp.References from fetch_logs is empty (i.e., kubectl passthrough
	// produced no JSONL artifact); otherwise we trust the existing
	// reference.
	references := append([]toolcore.NBToolResponseReference{}, resp.References...)
	outputFile, _ := input.Arguments["output_file"].(string)
	if outputFile != "" && len(references) == 0 {
		outputFile = common.SanitizePath(outputFile)
		wm := workspace.NewWorkspaceManager()
		if err := wm.SaveFile(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, outputFile, logData); err != nil {
			nbRequestContext.Ctx.GetLogger().Error("log: caller-requested save failed", "error", err, "path", outputFile)
		} else {
			references = append(references, toolcore.NBToolResponseReference{
				Text:        outputFile,
				Url:         outputFile,
				Type:        "file",
				Description: "Caller-requested save of log data",
			})
		}
	} else if outputFile != "" {
		// Caller asked for output_file but fetch_logs already saved a
		// canonical artifact — log so the omission is auditable.
		nbRequestContext.Ctx.GetLogger().Info("log: caller's output_file ignored — using fetch_logs's existing reference", "requested", outputFile, "existing", references[0].Url)
	}

	// Build the bounded evidence manifest from the chronological step order
	// BEFORE the in-place reverse below — otherwise the `logs` sub-agent (a
	// high-volume target of the manifest) drops its evidence at this bespoke
	// tool boundary, since this wrapper bypasses factory_agent's generic path.
	subAgentEvidence := core.BuildSubAgentEvidenceForTool(nbRequestContext.Ctx, LogsAgentName, resp.AgentStepResponse)

	if _, ok := agent.(core.NBAgentReActPlannerSummaryToolProvider); ok {
		return toolcore.NBToolResponse{
			Data:              logData,
			Type:              toolcore.NBToolResponseTypeText,
			Status:            toolcore.NBToolResponseStatusSuccess,
			References:        references,
			SubAgentEvidence:  subAgentEvidence,
			AdditionalDetails: additionalDetails,
		}, nil
	}

	slices.Reverse(resp.AgentStepResponse)
	for _, invocation := range resp.AgentStepResponse {
		if invocation.Response.Content != "" {
			respData := invocation.Response.Content
			respType := toolcore.NBToolResponseTypeJson
			if len(references) > 0 {
				respData = logData
				respType = toolcore.NBToolResponseTypeText
			}
			return toolcore.NBToolResponse{
				Data:              respData,
				Type:              respType,
				Status:            toolcore.NBToolResponseStatusSuccess,
				References:        references,
				SubAgentEvidence:  subAgentEvidence,
				AdditionalDetails: additionalDetails,
			}, nil
		}
	}
	return toolcore.NBToolResponse{
		Data:              logData,
		Type:              toolcore.NBToolResponseTypeText,
		Status:            toolcore.NBToolResponseStatusSuccess,
		References:        references,
		AdditionalDetails: additionalDetails,
		SubAgentEvidence:  subAgentEvidence,
	}, nil
}
