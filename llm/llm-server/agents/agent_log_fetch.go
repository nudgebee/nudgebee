package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"nudgebee/llm/workspace"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tmc/langchaingo/llms"
)

const FetchLogsAgentName = "fetch_logs"

// FetchLogsAgent dispatches an NL log question to the configured backend:
//
//	loki / signoz / es / elasticsearch  → JSON-where → logs_execute
//	datadog                             → DD facet syntax → datadog_log_execute
//	empty / k8s-only                    → kubectlLogQuery → kubectl_execute
type FetchLogsAgent struct {
	accountId string
	provider  services_server.ObservabilityProvider

	labelsOnce sync.Once
	fields     []string
	indices    map[string]string
}

func init() {
	toolDescription := `Fetches logs for a resource and returns raw log content. Translates a natural-language log question into the right backend query (Loki/Signoz/ES JSON, Datadog facet syntax, or kubectl flags) and runs it. Saves output to a workspace file so it can be downloaded or grepped via shell_execute. The caller is responsible for the strategy — fetch_logs runs whatever query the question implies; it does not add implicit error filters or widen windows on its own. For investigations, ask for a broad chronological window so the trigger (config reload, deploy, antecedent context) surfaces before the symptom storm.`
	toolInput := "Provide a natural-language log question (e.g. 'errors in <service> last 1h', 'why is <service> slow', 'logs for pod <workload>-<6-10 hex>-<5 alnum> in namespace <ns>')."
	toolOutput := "JSON envelope: {\"query\": \"<rendered backend query>\", \"logs\": \"<raw lines or preview>\", \"file_ref\": \"<workspace file path or empty>\"}"

	core.RegisterNBAgentFactoryAsTool(FetchLogsAgentName, func(accountId string) (core.NBAgent, error) {
		return newFetchLogsAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newFetchLogsAgent(accountId string) *FetchLogsAgent {
	// Empty provider routes Execute to the kubectl path.
	provider, err := tools.GetLogProvider(accountId)
	if err != nil || strings.EqualFold(provider.Provider, "k8s") {
		provider = services_server.ObservabilityProvider{}
	}
	return &FetchLogsAgent{accountId: accountId, provider: provider}
}

func (a *FetchLogsAgent) GetName() string { return FetchLogsAgentName }

func (a *FetchLogsAgent) GetNameAliases() []string { return []string{"Fetch Logs"} }

func (a *FetchLogsAgent) GetDescription() string {
	return `Translates a natural-language log question into the right backend query and runs it.`
}

func (a *FetchLogsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "an SRE expert that retrieves logs for a resource via the configured backend",
	}
}

func (a *FetchLogsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (a *FetchLogsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (a *FetchLogsAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	provider := strings.ToLower(a.provider.Provider)

	switch provider {
	case "datadog":
		return a.generateDatadogLogQueryAndExecute(ctx, request)
	case "loki", "signoz", "es", "elasticsearch":
		return a.generateLogQueryAndExecute(ctx, request)
	default:
		return a.generateKubeCtlLogQueryAndExecute(ctx, request)
	}
}

func (a *FetchLogsAgent) generateKubeCtlLogQueryAndExecute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	intent, err := generateKubeCtlLogQuery(ctx, request)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("kubectl intent extraction: %w", err)), nil
	}
	cmd := buildKubectlLogCommand(intent)
	logs, toolRefs, err := callTool(ctx, a.accountId, request, tools.ToolExecuteKubectlCommand, cmd)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("kubectl_execute: %w", err)), nil
	}
	if matched, reason := looksLikeFetchError("kubectl", logs); matched {
		return errorResponse(a.GetName(), fmt.Errorf("kubectl fetch failed: %s", reason)), nil
	}
	fileRef, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, "kubectl", logs)
	return makeFetchResponse(a.GetName(), cmd, logs, fileRef, mergeRefs(toolRefs, fileRefs)), nil
}

func (a *FetchLogsAgent) generateLogQueryAndExecute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	a.ensureLabelsAndIndices()
	jsonQuery, err := generateLogQuery(ctx, request, a.provider.Provider, a.fields, a.indices, a.provider.DefaultIndex)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("loki/es query extraction: %w", err)), nil
	}
	logs, toolRefs, err := callTool(ctx, a.accountId, request, tools.ToolLogsExecute, jsonQuery)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("logs_execute: %w", err)), nil
	}
	if matched, reason := looksLikeFetchError(a.provider.Provider, logs); matched {
		return errorResponse(a.GetName(), fmt.Errorf("%s fetch failed: %s", a.provider.Provider, reason)), nil
	}
	if strings.EqualFold(a.provider.Provider, "loki") {
		logs = unwrapLokiInnerTimestamps(ctx, logs)
	}
	fileRef, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, a.provider.Provider, logs)
	return makeFetchResponse(a.GetName(), jsonQuery, logs, fileRef, mergeRefs(toolRefs, fileRefs)), nil
}

func (a *FetchLogsAgent) generateDatadogLogQueryAndExecute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	ddQuery, err := generateDatadogLogQuery(ctx, request)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("datadog query extraction: %w", err)), nil
	}
	// logs_execute does not handle Datadog — Datadog has its own executor.
	logs, toolRefs, err := callTool(ctx, a.accountId, request, tools.ToolDatadogLogExecute, ddQuery)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("datadog_log_execute: %w", err)), nil
	}
	if matched, reason := looksLikeFetchError("datadog", logs); matched {
		return errorResponse(a.GetName(), fmt.Errorf("datadog fetch failed: %s", reason)), nil
	}
	fileRef, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, "datadog", logs)
	return makeFetchResponse(a.GetName(), ddQuery, logs, fileRef, mergeRefs(toolRefs, fileRefs)), nil
}

// ensureLabelsAndIndices populates fields (Loki/Signoz/ES labels) and, for ES,
// the named index aliases — both injected into the query-generator prompt so
// the LLM picks correct labels/indices instead of guessing.
//
// Surfacing the fetch-labels error matters because a silent fall-through
// produces nil fields → the translator's prompt path takes the "no labels
// known" branch and uses backend defaults that are wrong for ES/Signoz
// (canonical labels differ). Round 2's deleted agent logged this; the
// unification must keep that visibility.
func (a *FetchLogsAgent) ensureLabelsAndIndices() {
	a.labelsOnce.Do(func() {
		labels, err := fetchProviderLabels(a.accountId, a.provider)
		if err != nil {
			slog.Warn("fetch_logs: failed to fetch provider labels — translator will fall back to backend defaults", "error", err, "provider", a.provider.Provider, "account_id", a.accountId)
		}
		a.fields = labels
		if tools.IsESLogProvider(a.provider.Provider) {
			a.indices = utils.GetESAccountIndexConfig(a.accountId).Indices
		}
	})
}

func fetchProviderLabels(accountId string, provider services_server.ObservabilityProvider) ([]string, error) {
	t, err := tools.NewNBLogTool(accountId)
	if err != nil {
		return nil, err
	}
	return t.QueryLabels(), nil
}

// callTool invokes a registered tool. Uses NewNbToolContext so per-account
// ToolConfig is resolved — without it, kubectl_execute and similar tools fail
// their ToolConfig.Name precondition.
// callTool runs an underlying *_execute tool and returns both its data and its
// UI references. The references must be propagated: tools like logs_execute /
// loki_execute build the canonical "#monitoring/logs" source link (with the
// rendered query pre-filled); dropping them here is why fetch_logs answers
// historically had no clickable source. saveLogsToWorkspace's file reference is
// merged on top by the callers.
func callTool(ctx *security.RequestContext, accountId string, request core.NBAgentRequest, name string, command string) (string, []toolcore.NBToolResponseReference, error) {
	tool, ok := toolcore.GetNBTool(accountId, name)
	if !ok {
		return "", nil, fmt.Errorf("tool %s not registered", name)
	}
	toolCtx := toolcore.NewNbToolContext(
		ctx, tool, accountId,
		request.UserId, request.ConversationId, request.MessageId, request.AgentId,
		command, nil, request.QueryContext, request.QueryConfig, "",
	)
	resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{Command: command})
	if err != nil {
		return "", nil, err
	}
	return resp.Data, resp.References, nil
}

// fetchErrorRecurseMaxLen caps Branch 3 — kubectl_execute wraps the actual
// command output in `{"stdout":"...","stderr":"..."}`, and a real log dump
// can be many KB. We only substring-scan the wrapped fields when they're
// short enough to plausibly be an error envelope (~512 bytes is far above
// any wrapper format we've seen and well below typical log dumps).
const fetchErrorRecurseMaxLen = 512

// looksLikeFetchError reports whether `raw` is a stdout/JSON envelope the
// upstream tool returned with err==nil but where the content is actually a
// failure (kubectl RBAC denial, Loki "too many outstanding requests", ES 5xx
// HTML, relay/workspace 5xx wrapper, etc.). The deleted per-backend agents
// had this guard; the unified agent must not silently surface these as
// ConversationStatusCompleted with the error blob masquerading as `logs`.
//
// Layers checked, in order:
//
//  1. Substring + regex scan on raw text (`matchFetchErrorSignals`).
//     Catches kubectl-API-server errors and the relay's
//     `Server returned <code>:` wrapper at top level.
//  2. JSON-envelope error fields (Loki/Signoz/ES):
//     `{"error":"..."}` / `{"status":"error", ...}`.
//  3. kubectl_execute wrapper recursion: when the body is
//     `{"stdout":"...","stderr":"..."}`, re-run the Branch-1 scan on each
//     non-empty field — capped at `fetchErrorRecurseMaxLen` so a real log
//     dump containing a line like `forbidden access` doesn't false-flag.
//
// Returns (matched, reason) — reason is a short prefix suitable for the
// user-visible error message (capped at 200 chars).
func looksLikeFetchError(provider, raw string) (bool, string) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return false, ""
	}

	// Branch 1: substring + regex scan on raw NON-JSON text only.
	// Skip when the body starts with `{` — it's a JSON wrapper, and
	// substring-scanning the entire serialised JSON risks matching against
	// log content nested inside (e.g. a real log line saying "forbidden
	// access attempt"). Branches 2/3 below inspect JSON envelopes with
	// length-bounded recursion so log content doesn't false-flag.
	if !strings.HasPrefix(t, "{") {
		if matched, reason := matchFetchErrorSignals(t); matched {
			return true, reason
		}
	}

	// Branches 2 + 3: JSON envelope inspection.
	if strings.HasPrefix(t, "{") {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(t), &doc); err == nil {
			// Branch 2: Loki/Signoz/ES — top-level `error` field non-empty.
			if errVal, ok := doc["error"]; ok {
				var errStr string
				if json.Unmarshal(errVal, &errStr) == nil && strings.TrimSpace(errStr) != "" {
					return true, truncateForLog(errStr, 200)
				}
			}
			// Branch 2: Loki/Signoz — `status:"error"`.
			if statusVal, ok := doc["status"]; ok {
				var statusStr string
				if json.Unmarshal(statusVal, &statusStr) == nil && strings.EqualFold(statusStr, "error") {
					reason := "backend returned status=error"
					if errVal, ok := doc["error"]; ok {
						var errStr string
						if json.Unmarshal(errVal, &errStr) == nil && strings.TrimSpace(errStr) != "" {
							reason = errStr
						}
					}
					return true, truncateForLog(reason, 200)
				}
			}

			// Branch 3: kubectl_execute wraps the actual command output in
			// {"stdout":"...","stderr":"..."}. Recurse into each field with
			// the same Branch-1 scan, but only if the field's value is short
			// enough to plausibly be an error envelope. Long values are
			// real log content where substring scanning would false-positive
			// on log lines that happen to contain "forbidden",
			// "connection refused", etc.
			for _, key := range []string{"stdout", "stderr"} {
				if v, ok := doc[key]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil {
						trimmed := strings.TrimSpace(s)
						if trimmed == "" || len(trimmed) > fetchErrorRecurseMaxLen {
							continue
						}
						if matched, reason := matchFetchErrorSignals(trimmed); matched {
							return true, reason
						}
					}
				}
			}
		}
	}

	return false, ""
}

// matchFetchErrorSignals scans `raw` for the full set of fetch-error
// signals — the kubectl-API-server substrings (deleted KubectlLogAgent's
// pattern set) plus the relay/workspace HTTP-failure wrapper regex.
// Shared between Branch 1 (raw top-level body) and Branch 3 (recursed into
// stdout/stderr after JSON unwrap).
func matchFetchErrorSignals(raw string) (bool, string) {
	low := strings.ToLower(raw)

	// kubectl-API-server error substrings — RBAC, auth, missing resource,
	// network. Same set the deleted KubectlLogAgent used.
	kubectlSignals := []string{
		"error from server",
		"forbidden",
		"unauthorized",
		"the server doesn't have a resource",
		"the connection to the server",
		"connection refused",
		"unable to connect to the server",
		"x509:",
	}
	for _, s := range kubectlSignals {
		if strings.Contains(low, s) {
			return true, truncateForLog(raw, 200)
		}
	}

	// Relay/workspace HTTP-failure wrapper: "Server returned <3-digit code>:".
	// Catches `Error: Server returned 500: {...}`, `Server returned 502: bad
	// gateway`, etc. — emitted by the relay when its upstream call to a
	// workspace pod or services-server fails. Anchored on word boundary so
	// it doesn't fire on prose like "...the server returned a 500 error".
	if relayWrapperRE.MatchString(raw) {
		return true, truncateForLog(raw, 200)
	}

	return false, ""
}

// relayWrapperRE matches the relay's HTTP-failure wrapper format. The
// 3-digit code covers any 4xx/5xx; the literal `Server returned ` prefix is
// specific enough to avoid false-positives on prose.
var relayWrapperRE = regexp.MustCompile(`\bServer returned \d{3}:`)

// mergeRefs combines the underlying tool's UI references (e.g. the
// "#monitoring/logs" source link from logs_execute) with saveLogsToWorkspace's
// file reference, de-duplicating by URL. The tool's navigation reference is
// ordered first so it surfaces as the primary source; the workspace file ref
// follows as a downloadable artifact. Either side may be nil.
func mergeRefs(toolRefs, fileRefs []toolcore.NBToolResponseReference) []toolcore.NBToolResponseReference {
	if len(toolRefs) == 0 && len(fileRefs) == 0 {
		return nil
	}
	merged := make([]toolcore.NBToolResponseReference, 0, len(toolRefs)+len(fileRefs))
	seen := make(map[string]struct{}, len(toolRefs)+len(fileRefs))
	for _, r := range append(append([]toolcore.NBToolResponseReference{}, toolRefs...), fileRefs...) {
		if r.Url == "" {
			continue
		}
		if _, dup := seen[r.Url]; dup {
			continue
		}
		seen[r.Url] = struct{}{}
		merged = append(merged, r)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// makeFetchResponse returns {query, logs, file_ref} so the parent's scratchpad
// shows which query produced the data and where the raw logs were saved.
// file_ref lets shell_execute grep the saved file directly without re-fetching.
func makeFetchResponse(agentName, query, logs, fileRef string, refs []toolcore.NBToolResponseReference) core.NBAgentResponse {
	envelope := map[string]string{
		"query":    query,
		"logs":     logs,
		"file_ref": fileRef,
	}
	body, err := common.MarshalJson(envelope)
	if err != nil {
		return core.NBAgentResponse{
			Response:   []string{logs},
			AgentName:  agentName,
			Status:     core.ConversationStatusCompleted,
			References: refs,
		}
	}
	return core.NBAgentResponse{
		Response:   []string{string(body)},
		AgentName:  agentName,
		Status:     core.ConversationStatusCompleted,
		References: refs,
	}
}

// saveLogsToWorkspace persists fetched logs to the conversation workspace so
// they can be downloaded from the UI and grepped via shell_execute. Returns
// the saved filename and a single file-reference entry; both are empty/nil
// on empty logs or save failure (best-effort — never blocks the response).
//
// File layout: when the input is a Loki/Signoz/ES JSON envelope of shape
// `{"logs":[{...},{...}]}`, the saved file is rewritten as JSONL — one log
// entry per line in `<timestamp>\t<message>` form. This is what makes
// `grep "<pattern>" file | head -20` work as the LogAgent prompt expects:
// each entry occupies its own line, head means N entries (not bytes), and
// matches localise to a single record. Without this, the saved file is one
// JSON document with no internal newlines — grep returns the entire blob as
// "line 1" or matches nothing because the keyword is buried inside escaped
// `\"message\":\"...\"` substrings.
//
// kubectl text logs are line-based already and pass through unchanged.
// Anything that doesn't parse as the expected JSON envelope (Datadog
// alternate shapes, "No logs found" placeholders, kubectl text) is also
// passed through unchanged.
func saveLogsToWorkspace(ctx *security.RequestContext, accountId, conversationId, providerLabel, logs string) (string, []toolcore.NBToolResponseReference) {
	if strings.TrimSpace(logs) == "" {
		return "", nil
	}
	label := strings.ToLower(strings.TrimSpace(providerLabel))
	if label == "" {
		label = "kubectl"
	}
	body := flattenLogsToJSONL(logs)
	filename := fmt.Sprintf("logs_%s_%d.txt", label, time.Now().UnixNano())
	wm := workspace.NewWorkspaceManager()
	if err := wm.SaveFile(ctx, accountId, conversationId, filename, body); err != nil {
		ctx.GetLogger().Warn("fetch_logs: failed to save logs to workspace", "error", err, "file", filename)
		return "", nil
	}
	ctx.GetLogger().Info("fetch_logs: logs saved", "file", filename, "bytes", len(body), "raw_bytes", len(logs), "format", logsLayout(logs, body))
	return filename, []toolcore.NBToolResponseReference{
		{
			Text:        filename,
			Url:         filename,
			Type:        "file",
			Description: fmt.Sprintf("Raw log data from %s", label),
		},
	}
}

// flattenLogsToJSONL converts a Loki/Signoz/ES JSON envelope to one entry per
// line. Each output line is `<outer_timestamp>\t<message>` where <message> is
// the application's emitted line (often itself JSON of the form
// `{"timestamp":"...","level":"ERROR",...}`). grep then matches per-entry
// and `head -N` means N entries.
//
// Returns the input verbatim when:
//   - it doesn't parse as the expected envelope (kubectl text, "No logs
//     found" placeholder, Datadog alternate shapes)
//   - the envelope has zero entries
func flattenLogsToJSONL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var doc struct {
		Logs []struct {
			Timestamp string `json:"timestamp"`
			Message   string `json:"message"`
		} `json:"logs"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return raw
	}
	if len(doc.Logs) == 0 {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, e := range doc.Logs {
		ts := strings.TrimSpace(e.Timestamp)
		msg := strings.TrimSpace(e.Message)
		if ts != "" {
			b.WriteString(ts)
			b.WriteByte('\t')
		}
		b.WriteString(msg)
		b.WriteByte('\n')
	}
	return b.String()
}

// logsLayout reports which save path was taken — for the structured log line
// in saveLogsToWorkspace so we can verify in production that the JSONL
// rewrite is actually firing for Loki responses.
func logsLayout(raw, body string) string {
	if raw == body {
		return "passthrough"
	}
	return "jsonl"
}

func errorResponse(agentName string, err error) core.NBAgentResponse {
	return core.NBAgentResponse{
		Response:  []string{err.Error()},
		AgentName: agentName,
		Status:    core.ConversationStatusFailed,
	}
}

// unwrapLokiInnerTimestamps replaces each Loki entry's outer ingest timestamp
// with the inner application-emitted timestamp (parsed from the entry's JSON
// `message` field). Loki responses carry two timestamps per record — the
// outer is when Loki ingested the line (often clustered tightly together,
// hiding temporal patterns), the inner is when the application actually
// logged. Surfacing the inner one lets downstream synthesis cite real
// time-window anomalies without parsing escaped JSON.
//
// Best-effort: any parse failure (non-Loki shape, non-JSON message, missing
// inner timestamp) returns the input unchanged. Each failure path logs at
// DEBUG with enough context to diagnose drift — if Loki ever changes its
// response shape, the trace shows which assumption broke.
func unwrapLokiInnerTimestamps(ctx *security.RequestContext, logsJSON string) string {
	logger := ctx.GetLogger()

	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(logsJSON), &doc); err != nil {
		logger.Debug("loki unwrap: skip — input not valid JSON",
			"error", err,
			"input_len", len(logsJSON),
			"input_prefix", truncateForLog(logsJSON, 200),
		)
		return logsJSON
	}
	logsRaw, ok := doc["logs"]
	if !ok {
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		logger.Debug("loki unwrap: skip — no 'logs' field at top level", "found_keys", keys)
		return logsJSON
	}
	var entries []map[string]any
	if err := json.Unmarshal(logsRaw, &entries); err != nil {
		logger.Debug("loki unwrap: skip — 'logs' is not an array of objects",
			"error", err,
			"logs_prefix", truncateForLog(string(logsRaw), 200),
		)
		return logsJSON
	}

	unwrapped := 0
	skipNonStringMsg := 0
	skipNonJSONMsg := 0
	skipNoInnerTs := 0
	var firstNonJSONErr error
	var firstNoTsInnerKeys string

	for i, entry := range entries {
		msg, ok := entry["message"].(string)
		if !ok {
			skipNonStringMsg++
			continue
		}
		var inner map[string]any
		if err := json.Unmarshal([]byte(msg), &inner); err != nil {
			skipNonJSONMsg++
			if firstNonJSONErr == nil {
				firstNonJSONErr = err
			}
			continue
		}
		innerTs, ok := inner["timestamp"].(string)
		if !ok || strings.TrimSpace(innerTs) == "" {
			skipNoInnerTs++
			if firstNoTsInnerKeys == "" {
				keys := make([]string, 0, len(inner))
				for k := range inner {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				firstNoTsInnerKeys = strings.Join(keys, ",")
			}
			continue
		}
		entries[i]["timestamp"] = innerTs
		unwrapped++
	}

	logger.Debug("loki unwrap: per-entry results",
		"total", len(entries),
		"unwrapped", unwrapped,
		"skip_non_string_msg", skipNonStringMsg,
		"skip_non_json_msg", skipNonJSONMsg,
		"skip_no_inner_ts", skipNoInnerTs,
		"first_non_json_err", firstNonJSONErr,
		"first_no_inner_ts_keys", firstNoTsInnerKeys,
	)

	if unwrapped == 0 {
		return logsJSON
	}

	newLogs, err := json.Marshal(entries)
	if err != nil {
		logger.Warn("loki unwrap: re-marshal entries failed", "error", err)
		return logsJSON
	}
	doc["logs"] = newLogs
	out, err := json.Marshal(doc)
	if err != nil {
		logger.Warn("loki unwrap: re-marshal doc failed", "error", err)
		return logsJSON
	}
	return string(out)
}

// truncateForLog returns at most n bytes followed by "..." when the input is
// longer. Keeps debug-log fields bounded so a 65 KB Loki blob doesn't bloat
// the trace when something goes wrong.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Intent extractors. Each reads OriginalQuery (the user's verbatim question)
// in addition to the per-step query so investigation intent isn't lost when a
// parent planner paraphrases the question into a routine sub-step.

// buildLogIntentMessages assembles the LLM message stream for log-intent calls
// while keeping the system block byte-stable. Per-call dynamic content
// (OriginalQuery, ConversationContext, the per-step query) lives in a single
// human message so the upstream provider's prompt cache can hit on the static
// system prefix across calls and conversations.
func buildLogIntentMessages(systemPrompt string, request core.NBAgentRequest) []llms.MessageContent {
	var human strings.Builder
	hasHints := false
	if orig := strings.TrimSpace(request.OriginalQuery); orig != "" && orig != strings.TrimSpace(request.Query) {
		fmt.Fprintf(&human, "Original user question: %s\n\n", orig)
		hasHints = true
	}
	if request.ConversationContext != "" {
		fmt.Fprintf(&human, "Context:\n%s\n\n", request.ConversationContext)
		hasHints = true
	}
	if hasHints {
		fmt.Fprintf(&human, "Current query: %s", request.Query)
	} else {
		human.WriteString(request.Query)
	}
	return []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, human.String()),
	}
}

// generateKubeCtlLogQuery runs on the lite model; emits a kubectlLogQuery.
func generateKubeCtlLogQuery(ctx *security.RequestContext, request core.NBAgentRequest) (kubectlLogQuery, error) {
	systemPrompt := `Extract Kubernetes log retrieval parameters from the user's query and context.
Return ONLY a JSON object with the following fields:
- resource_name: Name of pod or deployment (string)
- resource_type: "pod", "deployment", "statefulset", etc (string)
- namespace: Namespace (string)
- container: Specific container name if mentioned (string)
- tail: Number of lines to retrieve (int). Use 100 for routine "show me logs". Use 10000 for INVESTIGATION queries ("were there issues", "what is causing X", "why is Y broken") so rare errors in long streams aren't missed when combined with filter_pattern.
- is_previous: true if requesting previously crashed logs (bool)
- filter_pattern: Regex pattern for grep if looking for errors/warnings (string). REQUIRED for investigation queries — set to "` + kubectlErrorRegex + `" or similar so the wide tail is narrowed server-side to relevant lines only.

CRITICAL — Read the ORIGINAL USER QUESTION (when provided) to determine intent, not just the per-step query.
A parent planner may paraphrase an investigative question into a routine-looking sub-step (e.g. user asks
"Was the X pod affected by today's incident?" but planner forwards "get logs for pod X"). The per-step query alone is
ambiguous; the original question carries the true intent. If the original question is investigative
(contains phrasings like "were there issues", "what is causing", "why is X broken/failing", "did Y happen",
"was there an outage", "what went wrong", "diagnose", "troubleshoot", "root cause"), you MUST set
filter_pattern and tail=10000 even if the per-step query reads as routine.

Defaults: tail=100 for routine queries, tail=10000 for investigation queries. When filter_pattern is set,
tail SHOULD be 10000 so the grep has a meaningful window to scan.
`
	messages := buildLogIntentMessages(systemPrompt, request)

	liteCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyModelTier, core.ModelTierRetrieval),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	res, err := core.GenerateAndTrackLLMContent(liteCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		ctx.GetLogger().Error("fetch_logs: kubectl intent LLM call failed", "error", err, "query", request.Query)
		return kubectlLogQuery{}, fmt.Errorf("intent LLM: %w", err)
	}
	if len(res.Choices) == 0 {
		ctx.GetLogger().Error("fetch_logs: kubectl intent LLM returned no choices", "query", request.Query)
		return kubectlLogQuery{}, fmt.Errorf("intent LLM returned no choices")
	}

	var intent kubectlLogQuery
	if err := common.ExtractAndUnmarshalJSON([]byte(res.Choices[0].Content), &intent); err != nil {
		preview := res.Choices[0].Content
		if len(preview) > 200 {
			preview = preview[:200]
		}
		ctx.GetLogger().Error("fetch_logs: kubectl intent JSON unmarshal failed", "error", err, "raw_preview", preview)
		return kubectlLogQuery{}, fmt.Errorf("intent JSON unmarshal: %w", err)
	}
	if intent.Tail == 0 {
		if strings.TrimSpace(intent.FilterPattern) != "" {
			intent.Tail = 10000
		} else {
			intent.Tail = 100
		}
	}
	return intent, nil
}

// generateLogQuery returns the JSON-where envelope logs_execute consumes.
// defaultIndex is the backend's account-default index (from get_default_provider);
// empty for backends with no index concept (e.g. Loki).
func generateLogQuery(ctx *security.RequestContext, request core.NBAgentRequest, provider string, fields []string, indices map[string]string, defaultIndex string) (string, error) {
	supportedOperators := []string{"_eq", "_neq", "_gt", "_gte", "_lt", "_lte", "_in", "_nin", "_like", "_ilike", "_nlike", "_is_null", "_or", "_and"}

	fieldsProvided := len(fields) > 0
	if !fieldsProvided {
		fields = []string{"_body", "namespace", "pod"}
	}

	var b strings.Builder
	b.WriteString("**GOAL:** Only Generate Query, Cannot Execute Query.\n")
	b.WriteString("You are an expert in generating JSON queries from natural language.\n")
	b.WriteString("Your goal is to create a valid JSON query based on the user's question.\n")
	b.WriteString("Follow this JSON schema:\n")
	b.WriteString(`{"where": {"<field>": {"<operator>": "<value>"}}, "_or": [ ... ], "_and": [ ... ]}, "limit": <number>, "time_range": "<string>", "start_time": "<string>", "index": "<string>", "direction": "<forward|backward> (Loki only)"}` + "\n")
	b.WriteString("The `where` clause is for filtering. For `_and` or `_or` operators, the value is an array of filter objects.\n")
	b.WriteString("The `index` field is optional. Use it to target a specific Elasticsearch index or pattern when the user's query implies a particular log source.\n")
	b.WriteString("Do not use anything other than the provided fields and operators.\n")
	b.WriteString("Prefer ilike operator for regex matches.\n")
	b.WriteString("Prefer ilike operator for text matches over eq operator.\n")
	b.WriteString("AVAILABLE FIELDS AND OPERATORS for query building\n")
	fmt.Fprintf(&b, "  - **Fields**: %s\n", strings.Join(fields, ", "))
	fmt.Fprintf(&b, "  - **Operators**: %s\n", strings.Join(supportedOperators, ", "))

	if len(indices) > 0 {
		keys := make([]string, 0, len(indices))
		for k := range indices {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		indexList := make([]string, 0, len(indices))
		for _, name := range keys {
			indexList = append(indexList, fmt.Sprintf("%s (%s)", name, indices[name]))
		}
		b.WriteString("AVAILABLE ELASTICSEARCH INDICES:\n")
		fmt.Fprintf(&b, "  %s\n", strings.Join(indexList, ", "))
		if defaultIndex != "" {
			fmt.Fprintf(&b, "  Account default index (used when `index` is omitted): %s\n", defaultIndex)
		}
		b.WriteString("Pick the most relevant index based on the user's question. If unsure or the request is general, omit the index field to use the account default.\n")
	} else if defaultIndex != "" {
		fmt.Fprintf(&b, "Account default log index (used when `index` is omitted): %s. Omit the `index` field unless the user's question implies a different source.\n", defaultIndex)
	}

	b.WriteString("\n**Constraints:**\n")
	if fieldsProvided {
		// Loki labels (`app`, `namespace`, ...) lose to the model's stronger
		// OTel/Datadog prior (`service_name`) without an explicit prohibition.
		b.WriteString("- MUST use ONLY the labels/fields listed in the Fields section above. Do not invent labels.\n")
		b.WriteString("- NEVER emit labels that are not in the Fields list. Do not fall back to generic OTel/Datadog conventions (e.g. `service_name`, `service.name`, `kubernetes.*`) unless they are explicitly present in the Fields list. If the equivalent appears in the Fields list under a different name (e.g. `app`, `namespace`, `pod`), use that name verbatim.\n")
		b.WriteString("- When the user's natural-language question uses generic words like 'service X', 'pod X', or 'app X', map them to the matching label from the Fields list. The choice of label name is dictated by the Fields list, not by the user's wording.\n")
	}
	b.WriteString("- Do not answer questions without generating a query.\n")
	b.WriteString("- Ensure the generated JSON is a valid query.\n")
	b.WriteString("- Return only the JSON query object enclosed in triple backticks.\n")

	if strings.EqualFold(provider, "loki") {
		b.WriteString("\n**Loki direction (Loki backend only):**\n")
		b.WriteString("- Default: omit `direction` (Loki defaults to backward — newest first). This is correct for both routine fetches AND the FIRST pass of an investigation, because:\n")
		b.WriteString("  - For routine queries (\"show me recent errors\", \"tail logs\"), newest-first is what the user expects.\n")
		b.WriteString("  - For investigations (\"why did X fail\", \"diagnose\", \"root cause\"), errors are typically scattered across history; newest-first guarantees the most recent error window is in the response, which is usually what the user is asking about. The orchestrator may then issue a SECOND, narrower forward fetch around the first error timestamp to pull antecedent context (config reload, deploy, secret rotation) — but that is a follow-up call, not the default.\n")
		b.WriteString("- Use `\"direction\": \"forward\"` ONLY for that targeted second-pass: a narrow `start_time`/`end_time` window (e.g. 5-15 min) immediately preceding a known error timestamp. Forward + a wide window is almost always wrong because `limit` will truncate before reaching the error window.\n")
	}

	b.WriteString("\n**Strategy is the caller's responsibility, not yours:**\n")
	b.WriteString("Translate the natural-language question into a query that reflects exactly what was asked. ")
	b.WriteString("Do NOT add an error-pattern body filter (e.g. `{\"_body\": {\"_ilike\": \"%error%\"}}`) unless the question explicitly asks for errors/warnings/failures. ")
	b.WriteString("If the caller asks for \"all logs\" or \"recent logs\" with no error keyword, emit a query with NO body filter — even if the broader context looks investigative. ")
	b.WriteString("The orchestrator above you decides whether to filter for errors or pull a broad chronological window; your job is to honour that decision faithfully.\n")

	b.WriteString("\n**Always emit `time_range` and `limit` (mandatory):**\n")
	b.WriteString("- A query without `time_range` falls back to a narrow 1h window centred on `now`. For pods that emit historical/burst data at startup (cron schedulers, replay-style fixtures, jobs that backfill), a 1h window misses errors that happened earlier in the pod's lifetime.\n")
	b.WriteString("- If the caller's question explicitly mentions a window (\"last 30m\", \"last 6h\", \"between 10:00 and 11:00\"), honour it verbatim.\n")
	b.WriteString("- Otherwise, choose `time_range` and `limit` from the caller's intent:\n")
	b.WriteString("    * **Investigation intent** (\"why is X broken\", \"diagnose\", \"what caused\", \"were there issues\", \"what went wrong\", \"root cause\", \"troubleshoot\", \"failing\", \"crash\"): emit `\"time_range\": \"24h\"`, `\"limit\": 5000`. Errors can be hours old; a narrow window will miss them.\n")
	b.WriteString("    * **Routine intent** (\"show me logs\", \"recent logs\", \"tail\", \"any errors right now\"): emit `\"time_range\": \"1h\"`, `\"limit\": 1000`. Routine viewing favours a recent slice but still needs enough volume to surface scattered errors.\n")
	b.WriteString("- Read the caller's ORIGINAL user question (when provided) to classify intent — a parent planner often paraphrases an investigative question into a routine-looking sub-step (\"Get recent logs for X\"). The original question carries the true intent.\n")
	b.WriteString("- These are defaults, not caps. If the caller specifies `last 7d` or `limit 10000`, use those.\n")

	b.WriteString("\n**Examples:**\n")
	examples := providerSpecificQueryExamples(provider)
	if len(examples) == 0 {
		examples = defaultQueryExamples()
	}
	for i, ex := range examples {
		fmt.Fprintf(&b, "Example %d:\n  Question: %s\n  Answer: %s\n", i+1, ex.Question, ex.Answer)
		if ex.Explanation != "" {
			fmt.Fprintf(&b, "  Explanation: %s\n", ex.Explanation)
		}
	}

	messages := buildLogIntentMessages(b.String(), request)

	res, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return "", err
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("empty LLM response")
	}
	return strings.TrimSpace(res.Choices[0].Content), nil
}

// generateDatadogLogQuery returns a Datadog facet-syntax query (e.g. "service:my-api status:error").
func generateDatadogLogQuery(ctx *security.RequestContext, request core.NBAgentRequest) (string, error) {
	systemPrompt := `**Role:** an SRE expert in Datadog log queries.

**Analyze User Request:** Carefully analyze the user's request to understand the specific log information they need.
**Generate Datadog Query:** Construct a valid Datadog log query based on the user's request.
**Filters:** Use fields like ` + "`service`, `source`, `status`, `host`, `@level`, `container_id`, `container_name`, `image_name`, `image_tag`, `kube_container_name`, `kube_daemon_set`, `kube_namespace`, `kube_node`, `kube_ownerref_kind`, `kube_ownerref_name`, `kube_qos`, `kube_service`, `pod_name`, `pod_phase`, `short_image`" + ` for filtering.
**Field rules:** Only use fields listed above — match field names exactly to user intent (e.g. ` + "`pod_name`" + ` for pods, ` + "`kube_ownerref_name`" + ` for deployments, ` + "`source`" + ` for log origin); do not invent fields; use plain text search for patterns like IPs or keywords.
**Time Range (mandatory):**
  - If the caller explicitly mentions a window (\"last 30m\", \"last 6h\", \"between X and Y\"), honour it verbatim.
  - Otherwise classify intent and pick:
      * **Investigation** (\"why is X broken\", \"diagnose\", \"what caused\", \"were there issues\", \"root cause\", \"troubleshoot\", \"failing\", \"crash\"): use ` + "`from:now-24h to:now`" + ` so errors hours older than the test runtime aren't missed.
      * **Routine** (\"show me logs\", \"recent logs\", \"tail\", \"any errors right now\"): use ` + "`from:now-1h to:now`" + `.
**Output:** Return only the Datadog query with no additional text or formatting.

**Investigation classification (CRITICAL):**
Read the ORIGINAL USER QUESTION (when provided as a separate system message) to determine intent. A parent planner may paraphrase an investigative question into a routine-looking sub-step. The per-step query alone is ambiguous; the original question carries the true intent.
If the original question is investigative ("were there issues", "what is causing", "why is X broken/failing", "did Y happen", "was there an outage", "what went wrong", "diagnose", "troubleshoot", "root cause"), you MUST include an error filter (e.g. ` + "`status:error`" + ` or a free-text term like ` + "`error`" + `) so rare errors in long streams aren't missed.

**Examples:**
Question: Show me error logs for service 'my-api' in the last hour.
Answer: service:my-api status:error

Question: Get logs for pod 'my-pod-xyz' in namespace 'default'.
Answer: pod_name:my-pod-xyz kube_namespace:default

Question: Find all warning logs from source 'kubernetes'.
Answer: source:kubernetes @level:warn

Question: Show logs containing 'connection refused' from host 'my-web-server'.
Answer: host:my-web-server "connection refused"
`
	messages := buildLogIntentMessages(systemPrompt, request)

	res, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return "", err
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("empty LLM response")
	}
	return strings.TrimSpace(res.Choices[0].Content), nil
}

// kubectlErrorRegex is the default investigation-mode grep pattern. Shared by
// the kubectl intent prompt and the LogAgent prompt so the two stay in sync.
const kubectlErrorRegex = "(error|exception|fail|fatal)"

// kubectlLogQuery is the LLM-emitted intent for the kubectl-direct path.
// Wide Tail + non-empty FilterPattern = investigation; small Tail + empty
// FilterPattern = routine fetch.
type kubectlLogQuery struct {
	ResourceName  string `json:"resource_name"`
	ResourceType  string `json:"resource_type"`
	Namespace     string `json:"namespace"`
	Container     string `json:"container"`
	Tail          int    `json:"tail"`
	IsPrevious    bool   `json:"is_previous"`
	FilterPattern string `json:"filter_pattern"`
}

// podHashSuffix conservatively matches Deployment-managed pod names of the
// form `<workload>-<6-10 hex>-<5 alnum>` (the standard
// Deployment→ReplicaSet→Pod naming pattern). StatefulSet/DaemonSet/Job pods
// are left unprefixed for kubectl to resolve directly.
var podHashSuffix = regexp.MustCompile(`-[a-z0-9]{5,10}-[a-z0-9]{5,}$`)

func looksLikePodName(name string) bool {
	return podHashSuffix.MatchString(name)
}

func buildKubectlLogCommand(intent kubectlLogQuery) string {
	var args []string
	args = append(args, "kubectl logs")

	target := strings.TrimSpace(intent.ResourceName)
	if intent.ResourceType != "" && !strings.Contains(target, "/") {
		switch strings.ToLower(intent.ResourceType) {
		case "deployment", "statefulset", "daemonset", "job", "service":
			target = strings.ToLower(intent.ResourceType) + "/" + target
		case "pod", "":
			if looksLikePodName(target) {
				target = "pod/" + target
			}
		}
	} else if target != "" && !strings.Contains(target, "/") && looksLikePodName(target) {
		target = "pod/" + target
	}
	args = append(args, target)

	if ns := strings.TrimSpace(intent.Namespace); ns != "" {
		args = append(args, "-n", ns)
	}
	if c := strings.TrimSpace(intent.Container); c != "" {
		args = append(args, "-c", c)
	}
	if intent.IsPrevious {
		args = append(args, "--previous")
	}
	tail := intent.Tail
	if tail <= 0 {
		tail = 100
	}
	args = append(args, fmt.Sprintf("--tail=%d", tail))

	cmd := strings.Join(args, " ")
	if pat := strings.TrimSpace(intent.FilterPattern); pat != "" {
		cmd = fmt.Sprintf("%s | grep -i -E '%s' | head -200", cmd, escapeShellSingleQuoted(pat))
	}
	return cmd
}

func escapeShellSingleQuoted(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// defaultQueryExamples is the fallback example set for unrecognised providers.
func defaultQueryExamples() []core.NBAgentPromptExample {
	return []core.NBAgentPromptExample{
		{
			Question:    "show me recent 504 failures for services abc?",
			Answer:      `{"where":{"http.status_code": {"_eq": 504}, "service_name": {"_eq": "abc"}}}`,
			Explanation: "Available Labels - http.status_code, service_name",
		},
		{
			Question:    "How many apis are taking more than 10seconds for service abc?",
			Answer:      `{"where": {"duration_ns": {"_gt": 10000000000}, "service_name": {"_eq": "abc"}}}`,
			Explanation: "Available Labels - duration_ns, service_name",
		},
		{
			Question:    "Get Recent Api Failures on services-server?",
			Answer:      `{"where": {"service_name": {"_eq": "services-server"}, "http.status_code": {"_gte": 500}}}`,
			Explanation: "Available Labels - service_name, http.status_code",
		},
		{
			Question:    "Show me traces from the last 2 hours for ml-k8s-server",
			Answer:      `{"where": {"service_name": {"_eq": "ml-k8s-server"}}, "time_range": "2h"}`,
			Explanation: "Available Labels - service_name, time_range",
		},
		{
			Question:    "get traces of llm server",
			Answer:      `{"where": {"service_name": {"_eq": "llm-server"}}}`,
			Explanation: "Available Labels - service_name",
		},
		{
			Question:    "get traces of llm server after 2025-01-01",
			Answer:      `{"where": {"service_name": {"_eq": "llm-server"}}, "start_time": "2025-01-01T00:00:00Z"}`,
			Explanation: "Available Labels - service_name, start_time",
		},
		{
			Question:    "get 10 error logs of services-server",
			Answer:      `{"where": {"service_name": {"_eq": "services-server"}, "body": {"_ilike": "%error%"}}, "limit":10}`,
			Explanation: "Available Labels - service_name, body",
		},
		{
			Question:    "Get me recent logs of app metrics-server in kube-system namespace",
			Answer:      `{"where": {"service_name": {"_eq": "metrics-server"}}, "limit":10}`,
			Explanation: "Available Labels - service_name",
		},
	}
}

// providerSpecificQueryExamples returns few-shot examples tuned to each
// backend's canonical labels (Loki `app`, Signoz `service.name`, ES
// `kubernetes.*.keyword`). Datadog is intentionally absent — it routes to
// generateDatadogLogQuery which has its own facet-syntax few-shots, and never
// reaches this function. Cross-provider drift is guarded by
// TestProviderQueryExamples_Coverage.
func providerSpecificQueryExamples(provider string) []core.NBAgentPromptExample {
	switch strings.ToLower(provider) {
	case "signoz":
		return []core.NBAgentPromptExample{
			{
				Question:    "Show me logs for service 'web-api'.",
				Answer:      `{"where": {"service.name":{"_ilike":"%web-api%"}}}`,
				Explanation: "Available Labels - service.name. Prefer _ilike (contains) over _eq for text matching.",
			},
			{
				Question:    "Get error logs for service 'web-api'.",
				Answer:      `{"where": {"service.name":{"_ilike":"%web-api%"}, "severity_text":{"_eq":"ERROR"}}}`,
				Explanation: "Available Labels - service.name, severity_text. severity_text values: TRACE, DEBUG, INFO, WARN, ERROR, FATAL.",
			},
			{
				Question:    "Find logs for namespace 'prod'.",
				Answer:      `{"where": {"service.namespace":{"_eq":"prod"}}}`,
				Explanation: "Available Labels - service.namespace. Use service.namespace for Kubernetes namespace filtering, NOT deployment.environment.",
			},
			{
				Question:    "Get logs from pod api-server-abc123.",
				Answer:      `{"where": {"pod_name":{"_ilike":"%api-server-abc123%"}}}`,
				Explanation: "Available Labels - pod_name. IMPORTANT: Use pod_name for pod filtering, NOT host.name or service.name.",
			},
			{
				Question:    "Show debug logs from pod 'app-pod-123'.",
				Answer:      `{"where": {"pod_name":{"_ilike":"%app-pod-123%"}, "severity_text":{"_eq":"DEBUG"}}}`,
				Explanation: "Available Labels - pod_name, severity_text. Always use pod_name for pod-based queries.",
			},
			{
				Question:    "Get logs from the worker container.",
				Answer:      `{"where": {"container_name":{"_ilike":"%worker%"}}}`,
				Explanation: "Available Labels - container_name. IMPORTANT: Use container_name for container filtering, NOT service.name.",
			},
			{
				Question:    "Get logs from container 'nginx' in staging namespace.",
				Answer:      `{"where": {"container_name":{"_ilike":"%nginx%"}, "service.namespace":{"_ilike":"%staging%"}}}`,
				Explanation: "Available Labels - container_name, service.namespace. Use container_name for containers and service.namespace for namespaces.",
			},
			{
				Question:    "Find logs containing 'database error' from service 'user-service'.",
				Answer:      `{"where": {"service.name":{"_ilike":"%user-service%"}, "body":{"_ilike":"%database error%"}}}`,
				Explanation: "Available Labels - service.name, body. Use body field with _ilike for full-text log search.",
			},
			{
				Question:    "Show last 100 logs for deployment in namespace 'staging'.",
				Answer:      `{"where": {"service.namespace":{"_ilike":"%staging%"}}, "limit": 100}`,
				Explanation: "Available Labels - service.namespace, limit. Use service.namespace for namespace queries.",
			},
			{
				Question:    "Get critical logs from source 'kubernetes' after yesterday.",
				Answer:      `{"where": {"source":{"_eq":"kubernetes"}, "severity_text":{"_eq":"FATAL"}}, "start_time": "2024-01-01T00:00:00Z"}`,
				Explanation: "Available Labels - source, severity_text, start_time",
			},
			{
				Question:    "What services are logging? / List all services.",
				Answer:      `{"where": {}, "limit": 100, "range": "24h"}`,
				Explanation: "For broad queries like 'list services' or 'what services exist', use an empty where clause with a wide time range. The log output will contain service.name labels that can be summarized. NEVER use _is_null operator — it is not supported.",
			},
		}
	case "loki":
		return []core.NBAgentPromptExample{
			{
				Question:    "Show me logs for app 'web-api'.",
				Answer:      `{"where": {"app":{"_eq":"web-api"}}}`,
				Explanation: "Available Labels - app",
			},
			{
				Question:    "Get error logs for app 'web-api'.",
				Answer:      `{"where": {"app":{"_eq":"web-api"}, "_body":{"_ilike":"%error%"}}}`,
				Explanation: "Available Labels - app, _body. PRIORTIZE `app` over `k8s_deployment_name` if both are present",
			},
			{
				Question:    "Find logs for app 'web-api' in namespace 'prod'.",
				Answer:      `{"where": {"app":{"_eq":"web-api"}, "namespace":{"_eq":"prod"}}}`,
				Explanation: "Available Labels - app, namespace. PRIORTIZE `namespace` over `k8s_namespace_name` if both are present",
			},
			{
				Question:    "Show logs from container 'redis' on job 'cache-job'.",
				Answer:      `{"where": {"container":{"_eq":"redis"}, "job":{"_eq":"cache-job"}}}`,
				Explanation: "Available Labels - container, job",
			},
			{
				Question:    "Get logs for the api-server pod containing 'timeout'.",
				Answer:      `{"where": {"app":{"_eq":"api-server"}, "_body":{"_ilike":"%timeout%"}}}`,
				Explanation: "Use `app` to identify a service/pod by name — pod names have random suffixes (e.g. api-server-7f8b9c-x2k). Only use `pod` with `_like` for prefix patterns.",
			},
			{
				Question:    "Find logs from stream 'stderr' for instance 'web-01' in last 2 hours.",
				Answer:      `{"command": {"where": {"stream":{"_eq":"stderr"}, "instance":{"_eq":"web-01"}}}, "range": "2h"}`,
				Explanation: "Available Labels - stream, instance, range",
			},
			{
				Question:    "Show last 25 logs from filename '/var/log/app.log'.",
				Answer:      `{"where": {"filename":{"_eq":"/var/log/app.log"}}, "limit": 25}`,
				Explanation: "Available Labels - filename, limit",
			},
			{
				Question:    "Get logs from level 'warn' or 'error' for service 'auth-service'.",
				Answer:      `{"where": {"app":{"_eq":"auth-service"}, "_or": [{"level":{"_eq":"warn"}}, {"level":{"_eq":"error"}}]}}`,
				Explanation: "When the user says 'service X', map it to the Loki label that identifies workloads — typically `app`, NOT `service_name`. `service_name` is a Datadog/OTel convention and is rarely a Loki label. Always pick from the injected Fields list.",
			},
			{
				Question:    "Show me errors from the checkout-api service in the last 15 minutes.",
				Answer:      `{"where": {"app":{"_eq":"checkout-api"}, "_body":{"_ilike":"%error%"}}, "range": "15m"}`,
				Explanation: "English phrasing 'the X service' / 'X service' means workload=X. In Loki this is the `app` label (or `job` / `container` if `app` is not in Fields). Never emit `service_name`, `service.name`, or `kubernetes.labels.app` unless they appear in the injected Fields list.",
			},
			{
				Question:    "Find logs from node 'k8s-worker-1' after specific timestamp.",
				Answer:      `{"command": {"where": {"node_name":{"_eq":"k8s-worker-1"}}}, "start_time": "2024-01-01T10:00:00Z"}`,
				Explanation: "Available Labels - node_name, start_time",
			},
			{
				Question:    "Get logs around 2025-01-01 10:00:00.",
				Answer:      `{"command": {"where": {"app":{"_eq":"<service>"}}}, "start_time": "2025-01-01T09:30:00Z", "end_time": "2025-01-01T10:30:00Z"}`,
				Explanation: "Available Labels - start_time, end_time. For 'around' queries, calculate start and end times (e.g. +/- 30 mins).",
			},
			{
				Question:    "Get all logs for checkout-api last 1h, limit 2000.",
				Answer:      `{"where": {"app":{"_eq":"checkout-api"}}, "limit": 2000, "range": "1h"}`,
				Explanation: "Broad investigation fetch — NO `_body` filter, NO `direction` (Loki defaults to backward = newest first, which surfaces the most recent error window in the response). For 24h-of-history fixtures and long-lived services, forward+limit would truncate before reaching the error window. The orchestrator can issue a narrow forward second-pass around a specific error timestamp once it's identified.",
			},
			{
				Question:    "After finding the first error at 2026-05-06T14:30:15Z, get the 5 minutes of context just before it.",
				Answer:      `{"where": {"app":{"_eq":"<service>"}}, "limit": 200, "start_time": "2026-05-06T14:25:00Z", "end_time": "2026-05-06T14:30:15Z", "direction": "forward"}`,
				Explanation: "Targeted antecedent fetch — narrow `start_time`/`end_time` window (5 min) ending at the known first-error timestamp, with `direction: \"forward\"` so the trigger lines (config reload, deploy, etc.) appear in chronological order before the error. This is the ONE case where `direction: \"forward\"` is correct — bounded by explicit timestamps, NOT a wide `range` value.",
			},
		}
	case "es", "elasticsearch":
		return []core.NBAgentPromptExample{
			{
				Question:    "Show me logs for pod 'my-pod' in namespace 'production'.",
				Answer:      `{"where": {"kubernetes.pod_name.keyword": {"_eq": "my-pod"}, "kubernetes.namespace_name.keyword": {"_eq": "production"}}}`,
				Explanation: "Available Fields - kubernetes.pod_name.keyword, kubernetes.namespace_name.keyword",
			},
			{
				Question:    "Get error logs containing 'connection refused'.",
				Answer:      `{"where": {"message": {"_ilike": "%connection refused%"}}}`,
				Explanation: "Use 'message' field (not '_body') for full-text log body search. _ilike performs case-insensitive contains.",
			},
			{
				Question:    "Find logs for namespace 'staging' from the last 2 hours.",
				Answer:      `{"where": {"kubernetes.namespace_name.keyword": {"_eq": "staging"}}, "range": "2h"}`,
				Explanation: "Available Fields - kubernetes.namespace_name.keyword, range",
			},
			{
				Question:    "Show logs for container 'nginx' with error messages.",
				Answer:      `{"where": {"kubernetes.container_name.keyword": {"_eq": "nginx"}, "message": {"_ilike": "%error%"}}}`,
				Explanation: "Available Fields - kubernetes.container_name.keyword, message",
			},
			{
				Question:    "Get logs for pods whose name starts with 'api-server'.",
				Answer:      `{"where": {"kubernetes.pod_name.keyword": {"_ilike": "api-server%"}}}`,
				Explanation: "Use _ilike with SQL wildcards: % for any characters. kubernetes.pod_name.keyword for pod name prefix filter.",
			},
			{
				Question:    "Show logs between 2024-01-01 10:00 and 11:00.",
				Answer:      `{"where": {}, "start_time": "2024-01-01T10:00:00Z", "end_time": "2024-01-01T11:00:00Z"}`,
				Explanation: "Use start_time and end_time (RFC3339 format) for precise time windows.",
			},
			{
				Question:    "Show last 50 error logs for namespace 'prod'.",
				Answer:      `{"where": {"kubernetes.namespace_name.keyword": {"_eq": "prod"}, "message": {"_ilike": "%error%"}}, "limit": 50}`,
				Explanation: "Available Fields - kubernetes.namespace_name.keyword, message, limit",
			},
			{
				Question:    "Get warn or error logs for namespace 'default'.",
				Answer:      `{"where": {"kubernetes.namespace_name.keyword": {"_eq": "default"}, "_or": [{"message": {"_ilike": "%warn%"}}, {"message": {"_ilike": "%error%"}}]}}`,
				Explanation: "Use _or for multi-value conditions on the same field.",
			},
			{
				Question:    "Show me nginx access logs with 5xx errors.",
				Answer:      `{"where": {"message": {"_ilike": "%5___%"}}, "index": "nginx-access-*"}`,
				Explanation: "Use the 'index' field to target a specific Elasticsearch index pattern when the user's query implies a particular log source. Omit 'index' to use the account default.",
			},
		}
	default:
		return []core.NBAgentPromptExample{}
	}
}
