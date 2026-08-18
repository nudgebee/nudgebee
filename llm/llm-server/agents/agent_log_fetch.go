package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"nudgebee/llm/workspace"
	"regexp"
	"sort"
	"strings"
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
}

func init() {
	common.CacheCreateNamespace(logLabelsCacheNS, common.CacheNamespaceWithExpiration(logLabelsCacheTTL))
	toolcore.RegisterToolCacheInvalidator(func(accountId string) {
		if err := common.CacheDeleteWithTag(logLabelsCacheNS, logLabelsAccountTag(accountId)); err != nil {
			slog.Debug("fetch_logs: unable to evict cached labels", "error", err, "account_id", accountId)
		}
	})

	toolDescription := `Fetches logs for a resource and returns raw log content. Translates a natural-language log question into the right backend query (Loki/Signoz/ES JSON, Datadog facet syntax, or kubectl flags) and runs it. Saves output to a workspace file so it can be downloaded or grepped via shell_execute. The caller is responsible for the strategy — fetch_logs runs whatever query the question implies; it does not add implicit error filters or widen windows on its own. For investigations, ask for a broad chronological window so the trigger (config reload, deploy, antecedent context) surfaces before the symptom storm.`
	toolInput := "Provide a natural-language log question (e.g. 'errors in <service> last 1h', 'why is <service> slow', 'logs for pod <workload>-<6-10 hex>-<5 alnum> in namespace <ns>')."
	toolOutput := "JSON envelope: {\"query\": \"<rendered backend query>\", \"logs\": \"<raw lines or preview>\", \"file_ref\": \"<workspace file path or empty>\", \"provider\": \"<loki|es|datadog|kubectl>\", \"bundle_signal\": \"<category-tagged crash-bundle sweep against file_ref, computed server-side when the query wording asked for error content (error/fail/crash/timeout/oom/…); empty when it didn't fire>\", \"fallback_note\": \"<present only when the configured backend's query matched zero rows and the agent retried via kubectl — explains that this answer came from kubectl instead of the configured provider, so a zero-row backend result isn't mistaken for 'no logs exist'; absent otherwise>\"}"

	core.RegisterNBAgentFactoryAsTool(FetchLogsAgentName, func(accountId string) (core.NBAgent, error) {
		// Always construct v2. It embeds v1 and gates the canonical path on the
		// LLM_SERVER_LOG_AGENT_V2_ENABLED env var (FetchLogsAgentV2.Execute); when
		// the gate is off it delegates to the embedded v1 agent, so behaviour is
		// identical to v1.
		return newFetchLogsAgentV2(accountId), nil
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

// effectiveProvider is pure — never writes to the receiver, which is shared
// across requests via the 30-minute tool caches. "k8s" = no services-server
// backend, i.e. the kubectl path (same as at construction).
func (a *FetchLogsAgent) effectiveProvider(request core.NBAgentRequest) services_server.ObservabilityProvider {
	provider := tools.EffectiveLogProvider(a.provider, request.QueryConfig.LogProviderOverride)
	if strings.EqualFold(provider.Provider, "k8s") {
		return services_server.ObservabilityProvider{}
	}
	return provider
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
	provider := a.effectiveProvider(request)

	switch strings.ToLower(provider.Provider) {
	case "datadog":
		return a.generateDatadogLogQueryAndExecute(ctx, request)
	case "loki", "signoz", "es", "elasticsearch":
		return a.generateLogQueryAndExecute(ctx, request, provider)
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
		if isNotFoundReason(reason) {
			if hint := discoverKubectlCandidates(ctx, a.accountId, request, intent); hint != "" {
				return errorResponse(a.GetName(), fmt.Errorf("kubectl fetch failed: %s. %s", reason, hint)), nil
			}
		}
		return errorResponse(a.GetName(), fmt.Errorf("kubectl fetch failed: %s", reason)), nil
	}
	fileRef, flattened, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, "kubectl", logs)
	bundleSignal, err := runAutoDiagnosticBundle(ctx, a.accountId, request, fileRef)
	if err != nil {
		return core.NBAgentResponse{}, err
	}
	return makeFetchResponse(a.GetName(), cmd, logs, flattened, fileRef, bundleSignal, mergeRefs(toolRefs, fileRefs)), nil
}

// isNotFoundReason excludes forbidden/unauthorized/connection errors, which a
// different resource guess can't fix.
func isNotFoundReason(reason string) bool {
	low := strings.ToLower(reason)
	return strings.Contains(low, "notfound") || strings.Contains(low, "not found")
}

// discoverKubectlCandidates surfaces similarly-named resources as a hint on a
// NotFound — it never retries or substitutes the target itself; the planner
// decides whether to retry with corrected coordinates.
func discoverKubectlCandidates(ctx *security.RequestContext, accountId string, request core.NBAgentRequest, intent kubectlLogQuery) string {
	term := strings.TrimSpace(intent.ResourceName)
	if idx := strings.LastIndex(term, "/"); idx != -1 {
		term = term[idx+1:]
	}
	if term == "" {
		return ""
	}
	cmd := fmt.Sprintf(
		"kubectl get pods,deployments,statefulsets,daemonsets --all-namespaces --no-headers | grep -F -i -- '%s' | head -5",
		escapeShellSingleQuoted(term),
	)
	out, _, err := callTool(ctx, accountId, request, tools.ToolExecuteKubectlCommand, cmd)
	if err != nil {
		ctx.GetLogger().Warn("fetch_logs: kubectl discovery call failed", "error", err, "resource_name", term)
		return ""
	}

	return formatDiscoveryHint(parseKubectlDiscoveryCandidates(extractKubectlStdout(out)))
}

// extractKubectlStdout unwraps kubectl_execute's {"stdout":...} envelope,
// falling back to the raw string when it isn't that JSON shape.
func extractKubectlStdout(out string) string {
	var env struct {
		Stdout string `json:"stdout"`
	}
	if json.Unmarshal([]byte(out), &env) == nil && env.Stdout != "" {
		return strings.TrimSpace(env.Stdout)
	}
	return strings.TrimSpace(out)
}

const discoveryCandidateCap = 5

// parseKubectlDiscoveryCandidates parses `kubectl get <types> -A --no-headers`
// lines ("<namespace> <kind>/<name> ...") into "<namespace>/<name> (<kind>)".
func parseKubectlDiscoveryCandidates(stdout string) []string {
	if stdout == "" {
		return nil
	}
	candidates := make([]string, 0, discoveryCandidateCap)
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		namespace := fields[0]
		typeName := strings.SplitN(fields[1], "/", 2)
		if len(typeName) != 2 {
			continue
		}
		candidates = append(candidates, fmt.Sprintf("%s/%s (%s)", namespace, typeName[1], typeName[0]))
		if len(candidates) >= discoveryCandidateCap {
			break
		}
	}
	return candidates
}

func formatDiscoveryHint(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Similar resources found via discovery: %s. If one of these matches your intent, retry fetch_logs with the corrected namespace/resource name.",
		strings.Join(candidates, ", "),
	)
}

func (a *FetchLogsAgent) generateLogQueryAndExecute(ctx *security.RequestContext, request core.NBAgentRequest, provider services_server.ObservabilityProvider) (core.NBAgentResponse, error) {
	fields, indices := fetchLabelsAndIndices(a.accountId, provider)
	jsonQuery, err := generateLogQuery(ctx, request, provider.Provider, fields, indices, provider.DefaultIndex)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("loki/es query extraction: %w", err)), nil
	}
	logs, toolRefs, err := callTool(ctx, a.accountId, request, tools.ToolLogsExecute, jsonQuery)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("logs_execute: %w", err)), nil
	}
	if matched, reason := looksLikeFetchError(provider.Provider, logs); matched {
		return errorResponse(a.GetName(), fmt.Errorf("%s fetch failed: %s", provider.Provider, reason)), nil
	}
	if strings.EqualFold(provider.Provider, "loki") {
		logs = unwrapLokiInnerTimestamps(ctx, logs)
	}
	fileRef, flattened, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, provider.Provider, logs)
	bundleSignal, err := runAutoDiagnosticBundle(ctx, a.accountId, request, fileRef)
	if err != nil {
		return core.NBAgentResponse{}, err
	}
	return makeFetchResponse(a.GetName(), jsonQuery, logs, flattened, fileRef, bundleSignal, mergeRefs(toolRefs, fileRefs)), nil
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
	fileRef, flattened, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, "datadog", logs)
	bundleSignal, err := runAutoDiagnosticBundle(ctx, a.accountId, request, fileRef)
	if err != nil {
		return core.NBAgentResponse{}, err
	}
	return makeFetchResponse(a.GetName(), ddQuery, logs, flattened, fileRef, bundleSignal, mergeRefs(toolRefs, fileRefs)), nil
}

// logLabelsCacheNS caches discovered backend labels (and, for ES, the account's
// index map) per account+provider. The label list is a HARD input to the
// query-generation prompt — buildCanonicalLogQueryPrompt renders it as the
// `canonical_name → backend_field` block — so it must be resolved BEFORE the
// LLM call, on the critical path of every fetch. Discovery costs an HTTP
// round-trip to services-server, which in turn queries the backend's own label
// API. The previous per-instance sync.Once memo never helped: FetchLogsAgent is
// reconstructed on every invocation (GetNBAgent → newFetchLogsAgentV2), so two
// fetches in one investigation meant two full discoveries.
const logLabelsCacheNS = "llm_log_labels"

// logLabelsCacheTTL is deliberately shorter than logProviderCacheTTL: labels
// track the workloads actually emitting logs, so a newly deployed app or
// namespace should become queryable within minutes rather than half an hour.
const logLabelsCacheTTL = 15 * time.Minute

// logLabels is the cache payload for fetchLabelsAndIndices.
//
// ExpiresAt is stamped into the payload rather than left to the cache store:
// the default in_memory backend (bigcache) silently ignores per-entry
// expiration — gocache's BigcacheStore.Set drops the option and bigcache has
// only one global LifeWindow, shared by every namespace and fixed by whichever
// namespace initialises first. Without this stamp, logLabelsCacheTTL would be
// decorative and the real TTL would be someone else's constant. A zero
// ExpiresAt (entry written by an older build) reads as already-expired, which
// degrades to a refetch — the safe direction.
type logLabels struct {
	Fields    []string          `json:"fields"`
	Indices   map[string]string `json:"indices"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// logLabelsCacheKey scopes the entry to the exact discovery inputs. The provider
// name is normalised so config whitespace/casing can't split the entry, and
// DefaultIndex is part of the key because ES label discovery is index-scoped
// (QueryLogLabels passes it through as the `index` request field) — two indices
// on the same account legitimately yield different field sets.
func logLabelsCacheKey(accountId string, provider services_server.ObservabilityProvider) string {
	return fmt.Sprintf("%s:%s:%s", accountId, strings.ToLower(strings.TrimSpace(provider.Provider)), provider.DefaultIndex)
}

// logLabelsAccountTag groups every provider/index entry belonging to one account
// under a single tag, so the integration-change invalidator can evict them all
// without having to enumerate the provider and index combinations that produced
// the keys.
func logLabelsAccountTag(accountId string) string {
	return "account:" + accountId
}

// fetchLabelsAndIndices warns on an empty list rather than failing: the caller
// then falls back to backend defaults that are wrong for ES/Signoz, and this
// warn is the only signal an operator gets that an override named a backend the
// account has no integration for.
//
// Results are cached for logLabelsCacheTTL. An empty label set is NEVER cached —
// it means discovery failed (or the override named a backend this account has no
// integration for), and pinning that would leave the query generator on wrong
// backend defaults for the whole TTL, silently returning zero rows.
func fetchLabelsAndIndices(accountId string, provider services_server.ObservabilityProvider) ([]string, map[string]string) {
	// An empty accountId bypasses the cache entirely rather than keying on
	// ":provider:index", which every empty-account caller would share. Discovery
	// for an empty account fails upstream today (QueryLabels can't resolve a
	// tenant) and an empty result is never cached, so this is unreachable — but
	// a key that could be shared across tenants must not depend on a downstream
	// error to stay empty. Mirrors the same guard in GetLogProvider.
	var cacheKey string
	if accountId != "" {
		cacheKey = logLabelsCacheKey(accountId, provider)
		if data, ok := common.CacheGet(logLabelsCacheNS, cacheKey); ok {
			var cached logLabels
			if err := common.UnmarshalJson(data, &cached); err != nil {
				// Evict rather than leave it: the success path below overwrites
				// this key anyway, but if discovery keeps failing (empty results
				// are never cached) the unreadable bytes would otherwise linger
				// and re-warn on every call.
				slog.Warn("fetch_logs: cached labels unreadable, rediscovering", "account_id", accountId, "provider", provider.Provider)
				if err := common.CacheDelete(logLabelsCacheNS, cacheKey); err != nil {
					slog.Debug("fetch_logs: unable to evict unreadable label entry", "error", err, "account_id", accountId)
				}
			} else if time.Now().Before(cached.ExpiresAt) {
				return cached.Fields, cached.Indices
			}
		}
	}

	labels := tools.NewNBLogToolWithProvider(accountId, provider).QueryLabels()
	if len(labels) == 0 {
		slog.Warn("fetch_logs: no provider labels — translator will fall back to backend defaults",
			"provider", provider.Provider, "account_id", accountId)
	}
	var indices map[string]string
	if tools.IsESLogProvider(provider.Provider) {
		indices = utils.GetESAccountIndexConfig(accountId, "logs").Indices
	}
	slog.Info("fetch_logs: fetchLabelsAndIndices complete", "account_id", accountId, "provider", provider.Provider, "label_count", len(labels), "labels", labels, "indices", indices)

	if len(labels) > 0 && cacheKey != "" {
		if data, err := common.MarshalJson(logLabels{Fields: labels, Indices: indices, ExpiresAt: time.Now().Add(logLabelsCacheTTL)}); err != nil {
			slog.Debug("fetch_logs: unable to serialize labels for cache", "error", err, "account_id", accountId)
		} else if err := common.CacheSet(logLabelsCacheNS, cacheKey, data,
			common.CacheSetWithExpiration(logLabelsCacheTTL),
			common.CacheSetWithTags(logLabelsAccountTag(accountId))); err != nil {
			slog.Debug("fetch_logs: unable to cache labels", "error", err, "account_id", accountId)
		}
	}
	return labels, indices
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
	resp, err := core.CallTool(toolCtx, tool, toolcore.NBToolCallRequest{Command: command})
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

// logInlinePreviewBytes bounds how much log text is inlined into the response
// envelope when the full logs have been saved to a workspace file. The model is
// instructed to grep <file_ref> for the full content, so the inline copy is only
// a preview — inlining the full logs (often 50k-200k tokens) alongside the
// file_ref duplicated the payload and dominated prompt size.
const logInlinePreviewBytes = 4096

// autoBundleQueryRE keeps the routine-mode fallback: fire the bundle even
// on a "get logs" request when the wording explicitly names error content
// ("get error logs", "show me warnings"). A plain "tail 100 lines" query
// still gets no bundle overhead. Investigation and enumeration modes are
// handled by classifyLogMode below (which treats "were there issues",
// "why", "diagnose", "broken", etc. as investigation intent) — the logs
// prompt actively instructs the LLM to STRIP error keywords from the
// fetch_logs question ("errors/exceptions/failures force a body filter
// that excludes surrounding context"), so keying only off literal
// keywords would let the bundle miss the exact queries it was built for.
var autoBundleQueryRE = regexp.MustCompile(`(?i)\b(error|errors|fail|failed|failure|failures|exception|exceptions|crash|crashed|crashes|warn|warning|warnings|timeout|timed\s*out|panic|refused|oom|oomkilled|fatal|traceback)\b`)

// runAutoDiagnosticBundle post-processes a successful fetch_logs call to
// pre-compute the diagnostic-bundle category signal. Deterministic bypass of
// the LLM's "should I call the bundle now?" decision — when the operator
// enabled the bundle flag AND the user's intent is an error investigation
// or enumeration AND fetch_logs saved a non-empty file, we run the crash
// bundle server-side and return its output for makeFetchResponse to include
// in the fetch envelope. Best-effort for TOOL errors (returns empty string,
// nil error — the fetch response still ships normally); context cancellation
// and timeout errors ARE propagated so the outer request can abort.
//
// Gate uses classifyLogMode over OriginalQuery/Query — the same classifier
// the LogAgent uses to pick INVESTIGATION vs ENUMERATION vs ROUTINE mode.
// For investigation/enumeration the bundle always fires (that's the entire
// point of the mode). For routine the narrow autoBundleQueryRE still gates
// so a plain "tail last 100 lines" gets no bundle overhead.
//
// Runs synchronously — the bundle IS the primary reason a follow-on
// shell_execute grep pass would exist, so paying its ~1-2s cost here saves
// a full LLM planner turn (~5-8s) in the parent's ReAct loop.
func runAutoDiagnosticBundle(ctx *security.RequestContext, accountId string, request core.NBAgentRequest, fileRef string) (string, error) {
	// Best-effort post-processing helper: nil ctx (e.g. from a code path that
	// invokes fetch_logs without a full RequestContext) must fail silently
	// instead of panicking on ctx.GetLogger() below or on the bundleTool.Call
	// context expansion.
	if ctx == nil {
		return "", nil
	}
	if !config.Config.LogsStandardGrepEnabled {
		return "", nil
	}
	if fileRef == "" {
		return "", nil
	}
	q := strings.TrimSpace(request.OriginalQuery)
	if q == "" {
		q = strings.TrimSpace(request.Query)
	}
	// Investigation / enumeration intent always gets the bundle. Routine
	// intent only gets it when the wording explicitly names error content.
	mode := classifyLogMode(request.Query, request.OriginalQuery)
	if mode == logModeRoutine && !autoBundleQueryRE.MatchString(q) {
		return "", nil
	}

	bundleTool, ok := toolcore.GetNBTool(accountId, tools.ToolStandardDiagnosticGrep)
	if !ok {
		return "", nil
	}
	toolCtx := toolcore.NewNbToolContext(ctx, bundleTool, accountId,
		request.UserId, request.ConversationId, request.MessageId, request.AgentId,
		q, nil, request.QueryContext, request.QueryConfig, "")
	resp, err := core.CallTool(toolCtx, bundleTool, toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"bundle":   "crash",
			"log_file": fileRef,
		},
	})
	if err != nil {
		// Context cancellation / deadline MUST propagate so the outer
		// request bails instead of continuing after the user (or a
		// deadline) has already terminated the call. Check ctx.Err()
		// FIRST — if the underlying tool masked the cancellation with a
		// generic error, errors.Is on the wrapped err would miss it.
		// Fall back to explicit sentinel matching in case ctx.Err() is
		// somehow still nil (a shell that observed cancellation and
		// returned a sentinel-wrapped error before our ctx observed it).
		// Only tool-side failures are swallowed as best-effort.
		if c := ctx.GetContext(); c != nil {
			if cerr := c.Err(); cerr != nil {
				return "", cerr
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		ctx.GetLogger().Debug("fetch_logs: auto-bundle skipped on tool error",
			"file_ref", fileRef, "error", err)
		return "", nil
	}
	return resp.Data, nil
}

// makeFetchResponse returns {query, logs, file_ref, provider, complete, bundle_signal} so the
// parent's scratchpad shows which query produced the data, where the raw
// logs were saved, and — when the query wording indicates an error-content
// investigation and the bundle flag is on — a pre-computed category-level
// grep sweep from standard_diagnostic_grep. bundle_signal is empty when the
// auto-bundle didn't fire (non-error query, flag off, empty file, or bundle
// tool error) and the caller should ignore the field. See
// runAutoDiagnosticBundle for the gating logic.
func makeFetchResponse(agentName, query, logs, flattened, fileRef, bundleSignal string, refs []toolcore.NBToolResponseReference) core.NBAgentResponse {
	// Preview the SAME representation that was written to file_ref, not the raw
	// backend payload. saveLogsToWorkspace writes flattenLogsToJSONL(logs) —
	// "<timestamp>\t<message>" per line — while this used to inline the raw
	// Loki/ES envelope. Showing one format and handing over a file in another
	// makes it impossible to write a working grep from the preview alone, so the
	// model had to spend a turn on `head -n 20 <file_ref>` first to discover the
	// real layout before it could filter. Observed in three separate sessions;
	// no prompt wording fixes it, because the instruction ("grep <file_ref>")
	// referred to an artifact the preview didn't describe.
	// Reuse the body saveLogsToWorkspace already flattened rather than redoing
	// it — this used to parse a multi-MB payload a second time per fetch.
	// Empty when nothing was saved (no logs, or save failed), in which case
	// there is no file to mirror and the raw payload is the only thing to show.
	previewSource := flattened
	if previewSource == "" {
		previewSource = logs
	}
	inlineLogs := previewSource
	logsComplete := true
	if fileRef != "" && len(previewSource) > logInlinePreviewBytes {
		logsComplete = false
		// ToValidUTF8 drops a partial trailing rune left by the byte slice.
		inlineLogs = strings.ToValidUTF8(previewSource[:logInlinePreviewBytes], "") + fmt.Sprintf(
			"\n\n[... %d more bytes truncated — full logs saved to file_ref %q, in EXACTLY this same line format; use shell_execute to filter it ...]",
			len(previewSource)-logInlinePreviewBytes, fileRef)
	} else if fileRef != "" && strings.TrimSpace(inlineLogs) != "" {
		// Only when a file actually exists — with no file_ref there is nothing to
		// be tempted to read, and "file_ref holds nothing further" would name an
		// artifact that was never produced.
		// Mirror of the truncation marker above, on the same in-band channel.
		// A bare JSON field is easy to skim past; the truncated case proves the
		// model reads and acts on a bracketed marker at the end of `logs`, so
		// the complete case states its conclusion the same way rather than
		// leaving it to be inferred from a flag.
		inlineLogs += "\n\n[... complete — all matching lines are shown above; file_ref holds nothing further, so no shell_execute is needed ...]"
	}
	envelope := map[string]any{
		"query":         query,
		"logs":          inlineLogs,
		"file_ref":      fileRef,
		"provider":      providerFromLogs(logs),
		"bundle_signal": bundleSignal,
		// logs_complete=true means every matching line is already inline and
		// file_ref holds nothing extra — shelling out to read it is pure cost.
		// Signalled explicitly because the previous "absence of a truncation
		// marker" was too weak a cue: measured over 14 days, 57.7% of fetches
		// returned the logs complete inline, yet the agent still spent turns
		// grepping them. Named with its subject rather than a bare `complete`,
		// which in an envelope of nouns reads as "the fetch completed" (status)
		// rather than "the log content is entire".
		"logs_complete": logsComplete,
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
func saveLogsToWorkspace(ctx *security.RequestContext, accountId, conversationId, providerLabel, logs string) (string, string, []toolcore.NBToolResponseReference) {
	if strings.TrimSpace(logs) == "" {
		return "", "", nil
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
		return "", body, nil
	}
	ctx.GetLogger().Info("fetch_logs: logs saved", "file", filename, "bytes", len(body), "raw_bytes", len(logs), "format", logsLayout(logs, body))
	return filename, body, []toolcore.NBToolResponseReference{
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
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	// Fast path: the expected envelope is `{"logs":[...]}`, so anything not
	// starting with `{` cannot be it — skip unmarshalling a potentially
	// multi-MB string only to fail. Note this does NOT cover the kubectl
	// backend: kubectl_execute wraps its output as `{"stdout":"..."}`, which
	// does start with `{` and still parses (into zero entries) before falling
	// through below. Suggested in review by gemini-code-assist on PR #36182.
	if !strings.HasPrefix(trimmed, "{") {
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
// defaultCustomAgentAccountPromptBytes is the fallback cap for the account
// GlobalContext fragment attached to custom-planner LLM calls when the
// llm_server_agent_account_prompt_max_bytes config is unset/invalid.
const defaultCustomAgentAccountPromptBytes = 8192

// customAgentAccountPromptCap returns the byte cap for the account
// GlobalContext fragment attached to custom-planner LLM calls (log/trace/
// kubectl intent generators, resource search), so a large curated context
// can't bloat every call. Configurable via
// llm_server_agent_account_prompt_max_bytes (default 8192). ReAct agents
// don't need this — the planner renders the GlobalContext into its human
// message already. The cap makes no assumption about the document's layout —
// content beyond it is simply dropped from the end.
func customAgentAccountPromptCap() int {
	if v := config.Config.LlmServerAgentAccountPromptMaxBytes; v > 0 {
		return v
	}
	return defaultCustomAgentAccountPromptBytes
}

func buildLogIntentMessages(systemPrompt string, request core.NBAgentRequest) []llms.MessageContent {
	// Propagate the account-wide GlobalContext (operator-curated deployment
	// facts: real backend field names, id formats, naming conventions) into the
	// query-generator call. Without this the generator only ever sees the
	// discovered/fallback field list — when discovery fails, account-curated
	// field guidance is the only correct signal available.
	//
	// Placed in the SYSTEM message deliberately: it is account-stable, not
	// per-call (the system prompt is already account-scoped via the discovered
	// fields list), so the cacheable prefix guarded by
	// TestBuildLogIntentMessages_SystemPromptIsStable is preserved — only
	// per-call inputs (Query/OriginalQuery/ConversationContext) must stay out
	// of the system message.
	if ap := strings.TrimSpace(request.AccountPrompt); ap != "" {
		ap = core.TruncateHead(ap, customAgentAccountPromptCap())
		systemPrompt += "\n\n**Account preferences (operator-curated for THIS deployment):**\n" +
			"The notes below may state this backend's real log field names, id formats, and query conventions. " +
			"When they name log fields for this backend, treat those names as part of the available Fields list.\n" +
			ap + "\n"
	}
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
	if kb := strings.TrimSpace(request.KBPrestepContent); kb != "" {
		fmt.Fprintf(&human, "Domain Knowledge / Runbooks:\n%s\n\n", kb)
		hasHints = true
	}
	if sc := strings.TrimSpace(request.SkillsContext); sc != "" {
		fmt.Fprintf(&human, "Skills Context:\n%s\n\n", sc)
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
- filter_pattern: Regex pattern for grep if looking for errors/warnings (string). REQUIRED for investigation queries — set to "` + kubectlErrorRegex + `" or similar so the wide tail is narrowed server-side to relevant lines only. Must be a plain POSIX extended regex (grep -E syntax): no Perl-only syntax like inline flags ("(?i)", "(?:...)"). The search is already case-insensitive (grep -i is always applied), so never add a case-insensitivity flag yourself.

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

// defaultProviderLogFields is the field list advertised to the query-generator
// LLM when the backend's label discovery (QueryLabels) fails or returns empty.
// The where-clause keys are passed to the backend verbatim (no canonical
// mapping), so the fallback must use each backend's REAL field names: Signoz
// stores logs under OTel attribute names — the generic `_body`/`namespace`/
// `pod` set matches no Signoz attribute and every query silently returns zero
// rows.
//
// Signoz is special-cased because it has ONE fixed schema (OTel), so a
// universally-correct fallback exists. Do NOT add hardcoded fallbacks for
// schema-less backends like Elasticsearch: their field names depend entirely
// on the customer's log shipper (filebeat `message`/`kubernetes.*`, OTel
// `body`/`k8s.*`, custom pipelines), and any guessed list silently returns
// zero rows for the setups it doesn't match — the exact failure mode this
// function exists to fix. Those backends must rely on label/index discovery
// and the account GlobalContext (propagated by buildLogIntentMessages).
func defaultProviderLogFields(provider string) []string {
	if strings.EqualFold(provider, "signoz") {
		return []string{"body", "service.name", "k8s.cluster.name", "k8s.namespace.name", "k8s.pod.name", "trace_id"}
	}
	return []string{"_body", "namespace", "pod"}
}

// generateLogQuery returns the JSON-where envelope logs_execute consumes.
// defaultIndex is the backend's account-default index (from get_default_provider);
// empty for backends with no index concept (e.g. Loki).
func generateLogQuery(ctx *security.RequestContext, request core.NBAgentRequest, provider string, fields []string, indices map[string]string, defaultIndex string) (string, error) {
	supportedOperators := []string{"_eq", "_neq", "_gt", "_gte", "_lt", "_lte", "_in", "_nin", "_like", "_ilike", "_nlike", "_is_null", "_or", "_and"}

	fieldsProvided := len(fields) > 0
	if !fieldsProvided {
		fields = defaultProviderLogFields(provider)
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
