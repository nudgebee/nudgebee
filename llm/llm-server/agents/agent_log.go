package agents

import (
	"fmt"
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

// Classifier patterns. Enumeration takes precedence over investigation
// because "show / list / summarise errors" is a more specific intent than the
// generic "what's wrong" investigative wording.
//
//   - Enumeration:    "show / list / summarise / enumerate" verb co-occurring
//     with an error noun within ~30 chars, plus
//     "what kinds of errors" and "distinct errors" idioms.
//   - Investigation:  causal verbs (why / diagnose / troubleshoot / debug /
//     investigate, plus their inflections) or symptom phrases
//     ("what caused", "were there issues", "broken/failing/
//     crash"). \w* tails on stem verbs cover the common
//     inflections.
var (
	enumerationVerbsRE   = regexp.MustCompile(`(?i)\b(show|list|summari[sz]e|categori[sz]e|enumerate)\b.{0,30}\b(error|errors|failures|exceptions|issues)\b|what\s+(kinds?\s+of\s+)?errors|\bdistinct\s+errors?\b`)
	investigationVerbsRE = regexp.MustCompile(`(?i)\bwhy\b|\broot\s+cause\b|\bdiagnos\w*\b|\btroubleshoot\w*\b|\bdebug\w*\b|\binvestigat\w*\b|\bwhat\s+(caused|happened|went\s+wrong|broke|broken)\b|\bwere\s+there\b|\bbroken\b|\bfailing\b|\bcrash\w*\b`)
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
	if enumerationVerbsRE.MatchString(q) {
		return logModeEnumeration
	}
	if investigationVerbsRE.MatchString(q) {
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
	if provider.Provider == "k8s" {
		provider = services_server.ObservabilityProvider{}
	}
	return newLogAgent(accountId, provider), nil
}

// LogAgent is the ReAct log investigator. Tools exposed to its LLM are
// uniform across providers (resource_search, fetch_logs, optional
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
  - Investigation — phrasings that contain causal words (why / root cause / diagnose / troubleshoot / what caused / broken / failing / crash) or that ask whether issues, outages, or failures occurred. Keep that wording; the agent's classifier uses it to pick the multi-grep investigation workflow with mandatory Call A + E1/E2 recovery.
  - Enumeration — phrasings like "list errors", "show errors", "distinct errors", "summarize errors". Preserve; routes to the per-signature aggregation flow.
  - Routine — phrasings like "recent logs", "tail logs", "last N minutes". Preserve; routes to a single small fetch.
Paraphrasing an investigation as a flat "get logs for <workload>" downgrades it to routine and skips the deeper analysis.`
}

func (l *LogAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l *LogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	l.toolsOnce.Do(func() {
		l.tools = l.buildToolList(ctx)
	})
	return l.tools
}

func (l *LogAgent) buildToolList(ctx *security.RequestContext) []toolcore.NBTool {
	names := []string{ResourceSearchAgentName, FetchLogsAgentName}
	if config.Config.LlmServerShellToolEnabled {
		names = append(names, toolcore.ToolExecuteShellCommand)
	}
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
		instructions = append(instructions, investigationInstructions(config.Config.LlmServerShellToolEnabled)...)
	case logModeEnumeration:
		instructions = append(instructions, enumerationInstructions(config.Config.LlmServerShellToolEnabled)...)
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
		"  1. **Resolve the resource** the user is asking about. Use `resource_search` when ANY of these apply:",
		"     a. The name is ambiguous (no namespace, partial match, common service name).",
		"     b. The name looks like a **workload** (Deployment / StatefulSet / DaemonSet / Job) but you need pod logs. Workload-managed pods have hash suffixes (the Deployment→ReplicaSet→Pod pattern: `<workload-name>-<6-10 hex>-<5 alphanumeric>`). A bare workload name without that suffix is NOT a valid pod name; calling `fetch_logs` with `pod=<workload-name>` resolves to zero entries because Kubernetes pod names always carry the suffix.",
		"     c. The user gave a service / app / deployment name and didn't specify a pod.",
		"     Skip resource_search ONLY when the user gave an obvious pod name — already has the hash suffix described above, OR follows a StatefulSet ordinal pattern (`<sts-name>-<integer>`, e.g. `kafka-0`, `mysql-0`).",
		"  2. **Fetch the logs** by calling `fetch_logs` with a natural-language question that includes the resolved resource and time window. `fetch_logs` translates the question into the right backend query (Loki, Datadog, Elasticsearch, or kubectl) and runs it. The response is a JSON envelope with the rendered query and the raw logs.",
		"  2a. **Recovery from name resolution failure.** If `fetch_logs` returns an error containing \"pod not found\", \"(NotFound)\", \"no resources found\", or similar — the resource name didn't resolve. You MUST:",
		"     - Call `resource_search` with the original name and namespace.",
		"     - Take the resolved pod name (with hash suffix) from the search result.",
		"     - Re-call `fetch_logs` with the resolved pod name.",
		"     Do NOT give up after one failed fetch. Do NOT fall back to a narrower query. Do NOT report \"no issues\" based on a fetch that errored — the fetch failure means we have no log evidence yet, so any conclusion about health is unsupported.",
	}
}

// routineInstructions returns the body for MODE = ROUTINE: a single small
// fetch, no shell_execute pass, no widening. Investigation/enumeration blocks
// are not included.
func routineInstructions() []string {
	return []string{
		"**Workflow (Routine):**",
		"  3. **Single fetch is enough.** Phrase the NL question using the right label depending on whether the user gave a hashed pod name or an app/deployment name (see Label-anchor rules below). Keep limits small (200-1000 lines).",
		labelAnchorRules(),
		"  4. **No shell_execute pass.** Read the fetch_logs response inline and answer.",
		"  5. **No widening on empty result.** If the small fetch returns nothing for the user's window, say so explicitly — this mode is for routine viewing, not investigation.",
	}
}

// investigationInstructions returns the body for MODE = INVESTIGATION: broad
// 24h/5000 fetch, mandatory shell_execute Call A, optional Call B/second-pass
// antecedent fetch, mandatory E1/E2 recovery on empty grep, time-window
// framing for clustered errors. Routine and enumeration blocks are not
// included.
func investigationInstructions(shellEnabled bool) []string {
	out := []string{
		"**Workflow (Investigation):**",
		"  3. **Broad first-pass fetch (newest-first).** You MUST phrase the fetch with `last 24h, limit 5000`. The `time_range` and `limit` are MANDATORY — a bare phrasing like \"recent logs for X\" produces a narrow 1h/1000 default that misses errors which happened earlier in the pod's lifetime (cron schedulers, replay-style fixtures, jobs that backfill historical data at startup all routinely emit errors hours before the test runtime). A 24h/5000 fetch covers the full pod lifecycle for most workloads.",
		labelAnchorRules(),
		"     - Do NOT include words like \"errors\", \"exceptions\", \"failures\", \"5xx\" in the fetch_logs question (those force a body filter at the database that excludes the surrounding context).",
		"     - For very chatty services that hit the 5000 limit on a 24h window, narrow by container or stream — never by error keyword.",
	}

	if shellEnabled {
		out = append(out,
			"  4. **Mandatory shell_execute pass (NOT optional for this mode).** When `fetch_logs` returns a non-empty `file_ref`, you MUST run AT LEAST TWO `shell_execute` calls before producing a final answer:",
			"     Call A — locate the first error timestamp:",
			"       `grep -nE \"ERROR|FATAL|PANIC|exception|traceback\" <file_ref> | head -20`",
			"       Note the timestamp on the first matching line — that is the transition point.",
			"     Call B — find the trigger BEFORE the transition point:",
			"       `grep -nE \"reload|reconfigur|deploy|rollout|secret.rotat|config.changed|started|loaded|env|config\" <file_ref> | head -20`",
			"       The trigger usually lives in WARN/INFO/CONFIG lines that Call A skipped.",
			"  5. **Second-pass targeted antecedent fetch (only if Call A finds an error timestamp).** Call fetch_logs again with `\"all logs for <pod> in <namespace> from <T-5min> to <T>, limit 500, in chronological forward order\"`. This pulls the trigger context (config reload, deploy, secret rotation) immediately preceding the error — bounded by explicit `start_time`/`end_time`, so forward+limit is safe. Then re-run Call B's grep on the new file_ref. Skip if Call B already found a clear trigger.",
			"  6. **Empty-result recovery (MANDATORY two-step — DO NOT skip):**",
			"     Step E1 — re-grep with case-insensitive + broader keywords on the SAME file:",
			"       `grep -inE \"error|fail|fatal|panic|exception|traceback|connection.refused|connectionerror|cannot.connect|timed.out|timeout|denied|unreachable\" <file_ref> | head -20`",
			"       The default Call A pattern is case-sensitive and misses mixed-case wording (e.g. `ConnectionError`, `Connection refused`) and protocol idioms (`timed out`, `unreachable`).",
			"     Step E2 — only if Step E1 is also empty, widen the fetch:",
			"       Re-call fetch_logs with `\"all logs for <pod> in <namespace> last 7d, limit 10000\"`, then re-run the Step E1 grep on the new file_ref.",
			"       If the user's question or any earlier observation mentions a specific timestamp, ALSO issue a narrower targeted fetch around that timestamp.",
			"     **Strict prohibitions:**",
			"       - Do NOT substitute kubectl events, deployment status, or rollout history for a wider log fetch. Events ≠ logs. The critic rejects \"no issues\" answers based on event checks alone.",
			"       - Do NOT call `think` to conclude \"no issues\" without completing Step E1 AND (if E1 empty) Step E2.",
			"       - Only after Call A + Step E1 + Step E2 all return empty may you state \"no errors found in last 7d\".",
			"  Do NOT produce a final answer without Call A AND (Call B succeeding OR a documented second-pass attempt OR a documented widened-window attempt). The critic will reject answers that skip this step.",

			"**Domain-aware grep patterns (for additional shell_execute passes when default Call B is empty):**",
			"  - Crashes / restarts:  `OOM|Killed|exit.code|CrashLoop|restart|signal|SIGTERM|SIGKILL`",
			"  - Latency / slowness:  `timeout|deadline|slow|connection.pool|retry|context.deadline|elapsed`",
			"  - Auth / access:       `401|403|JWT|token|expired|unauthorized|forbidden|permission`",
			"  - Memory / resources:  `OOM|heap|memory|allocation|GC|garbage.collect`",
			"  - Network:             `connection.refused|ECONNREFUSED|DNS|resolve|unreachable|reset`",
			"  - Database:            `deadlock|lock.timeout|too.many.connections|query.timeout|replication`",

			"**Useful shell_execute shapes (FILE = file_ref returned by fetch_logs):**",
			"  - Shape & frequency:  `wc -l FILE; grep -c \"ERROR\\|WARN\\|FATAL\" FILE`",
			"  - Context ±5 lines:   `grep -n -B5 -A5 \"SPECIFIC\" FILE`",
			"  - Frequency table:    `grep -oE \"PATTERN\" FILE | sort | uniq -c | sort -rn | head -20`",
			"  - Time bookends:      `head -5 FILE; tail -5 FILE`",
			"  - Window slice:       `awk '/HH:MM:SS/,/HH:MM:SS/' FILE` to slice a time range",
		)
	} else {
		out = append(out,
			"  4. **Antecedents rule (CRITICAL — shell_execute is unavailable, so read the fetch response directly):**",
			"     The trigger is the most important finding. The broad first-pass fetch returns lines in newest-first order. Read the output directly:",
			"       1. Locate the LAST line containing ERROR/FATAL/PANIC (most recent error) AND the FIRST such line in the response (oldest error in window). The first-of-many is the transition point.",
			"       2. If the trigger is in the same file: scan WARN/INFO/CONFIG lines just before the transition point for `reload|deploy|config|rotate|started|loaded`.",
			"       3. If the file has no errors OR no trigger context near the first error: issue a **second fetch** with a narrow `start_time`/`end_time` window (5-15 min) targeting the gap — either before the first error (for trigger context) or shifted further back in history (if the broad fetch's window was too narrow to contain any errors).",
			"     A symptom-only answer (\"connection refused to db-staging\") without naming the trigger (\"config reload at 10:07 loaded staging endpoints in prod\") is incomplete and will be rejected by the critic.",
		)
	}
	return out
}

// enumerationInstructions returns the body for MODE = ENUMERATION: broad
// fetch + per-signature aggregation, no causal narrative. Routine and
// investigation blocks are not included.
func enumerationInstructions(shellEnabled bool) []string {
	out := []string{
		"**Workflow (Enumeration):**",
		"  3. **Broad fetch (no error-keyword body filter).** Phrase with `last 1h, limit 2000` (or the user-specified window). The user wants a comprehensive list of distinct error categories, NOT a single root-cause narrative.",
		labelAnchorRules(),
		"     - Do NOT include error keywords (`errors`, `exceptions`, `failures`) in the question — that adds a body filter that excludes the surrounding context.",
	}

	if shellEnabled {
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
	} else {
		out = append(out,
			"  4. **In-process aggregation (shell_execute is unavailable):** scan the fetch response, group by error-class signature (collapse digit runs and IDs), and count occurrences. Lead the answer with `\"Found N distinct error categories\"` and list each with its count and an example line.",
			"  5. Do NOT speculate about root cause. Do NOT recommend fixes. The user wants enumeration, not diagnosis.",
		)
	}
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
			"  How to determine the window bounds (the saved file is sorted newest-first because Loki's default direction is backward — `head` gives newest, `tail` gives oldest):",
			"    - Window END (most recent error): `grep -nE \"<error pattern>\" <file_ref> | head -1`",
			"    - Window START (earliest error in the matched set): `grep -nE \"<error pattern>\" <file_ref> | tail -1`",
			"    - Total error count (to confirm \"cluster\" vs \"isolated\"): `grep -cE \"<error pattern>\" <file_ref>`",
			"  You MUST run BOTH the head and tail commands when reporting a window. A single `head -20` only shows the newest end of the cluster — citing only that lets the user think the issue is recent when it actually started minutes/hours earlier. Cite the START and END timestamps verbatim from the grep output — do not round or approximate. If the errors are scattered (no contiguous cluster — i.e. the head/tail span is hours instead of minutes), say so and list the distinct timestamps.",
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
	return "     - **Label anchor (CRITICAL — picks the right Loki label):** DEFAULT to anchoring on the workload label by phrasing as `\"all logs for app <name> in <namespace>\"`. This emits `app=<name>` and matches all pods of the Deployment/StatefulSet/DaemonSet (Loki entries always carry both `app` and `pod` labels). ONLY phrase as `\"all logs for pod <name> in <namespace>\"` (which emits `pod=<name>`) when the name has the Deployment-style hash suffix `<workload>-<6-10 hex>-<5 alphanumeric>` — e.g. you got the exact pod name from `kubectl get pods` or `resource_search`'s suggestions output. Anchoring on `pod` for a bare workload name produces zero-result fetches because Loki entries carry the full pod-with-hash, not the deployment name."
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
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("log: unable to process events request", "error", err, "input", input)
		return toolcore.NBToolResponse{}, err
	}

	if len(resp.Response) == 0 {
		return toolcore.NBToolResponse{}, toolcore.ErrUnableToFetchData
	}

	logData := resp.Response[0]

	// Workspace artifacts are saved exactly once — by FetchLogsAgent's
	// saveLogsToWorkspace inside fetch_logs (raw JSONL, gated on non-empty
	// payload). The previous double-save here (synthesised markdown, gated
	// on LlmServerShellToolEnabled + (output_file or len > 2000)) produced
	// a second workspace file per query with different content and different
	// gating rules. We propagate fetch_logs's file_ref upward via
	// resp.References so the UI / parent planner sees one canonical artifact.
	//
	// `output_file` was previously honoured here as a caller-driven save —
	// but it was silently ignored when LlmServerShellToolEnabled was off,
	// which made the contract unreliable. We honour caller-requested saves
	// only when the resp.References from fetch_logs is empty (i.e., kubectl
	// passthrough produced no JSONL artifact); otherwise we trust the
	// existing reference.
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

	if _, ok := agent.(core.NBAgentReActPlannerSummaryToolProvider); ok {
		return toolcore.NBToolResponse{
			Data:       logData,
			Type:       toolcore.NBToolResponseTypeText,
			Status:     toolcore.NBToolResponseStatusSuccess,
			References: references,
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
				Data:       respData,
				Type:       respType,
				Status:     toolcore.NBToolResponseStatusSuccess,
				References: references,
			}, nil
		}
	}
	return toolcore.NBToolResponse{
		Data:       logData,
		Type:       toolcore.NBToolResponseTypeText,
		Status:     toolcore.NBToolResponseStatusSuccess,
		References: references,
	}, nil
}
