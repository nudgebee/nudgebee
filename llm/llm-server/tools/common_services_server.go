package tools

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// lokiMaxLogLimit is the per-query cap Loki enforces (HTTP 400 above this).
// Sourced from the production error string `loki: limit exceeds maximum of 5000`
// (services-server wrapped) — pin here so the clamp does not lag the upstream
// reality. If Loki raises its cap, bump this constant.
const lokiMaxLogLimit = 5000

// clampLogLimitForProvider returns the (possibly capped) limit + a flag that
// indicates whether a clamp actually happened. Returns (limit, false) for
// providers without a known cap so the caller logs nothing in the common path.
// Provider name is normalised (trimmed + lowercased) so accidental whitespace
// in integration config doesn't bypass the clamp.
func clampLogLimitForProvider(provider string, limit int) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "loki":
		if limit > lokiMaxLogLimit {
			return lokiMaxLogLimit, true
		}
	}
	return limit, false
}

// fetchLogParams holds the time-window/paging knobs parsed from the loosely-typed
// `configs` map shared by the legacy (executeFetchLogs) and canonical
// (executeFetchLogsCanonical) log-fetch paths.
type fetchLogParams struct {
	limit     int
	startTime int64
	endTime   int64
	offset    int
	request   map[string]any
	index     string
}

// parseFetchLogConfigs normalizes the loosely-typed `configs` map into typed
// fetch params. Extracted verbatim from executeFetchLogs so the legacy and
// canonical paths share identical limit/time-window/offset handling.
func parseFetchLogConfigs(configs map[string]any) (fetchLogParams, error) {
	p := fetchLogParams{
		limit:     1000,
		endTime:   int64(time.Now().UnixMilli()),
		startTime: int64(time.Now().Add(-1 * time.Hour).UnixMilli()),
		request:   map[string]any{},
	}
	if val, ok := configs["limit"]; ok {
		switch limitValue := val.(type) {
		case string:
			limit1, err := strconv.Atoi(limitValue)
			if err != nil {
				return p, err
			}
			p.limit = limit1
		case float64:
			p.limit = int(limitValue)
		case int:
			p.limit = limitValue
		case int64:
			p.limit = int(limitValue)
		default:
			return p, fmt.Errorf("invalid limit value - %v", val)
		}
	}
	if val, ok := configs["end_time"]; ok {
		if intVal, ok := val.(int64); ok {
			p.endTime = intVal
		} else {
			return p, fmt.Errorf("invalid end_time value - %v", val)
		}
	}
	if val, ok := configs["start_time"]; ok {
		if intVal, ok := val.(int64); ok {
			p.startTime = intVal
		} else {
			return p, fmt.Errorf("invalid start_time value - %v", val)
		}
	}

	// Guard: if startTime >= endTime (LLM can produce same/inverted timestamps),
	// default to a 1-hour window ending at endTime.
	if p.startTime >= p.endTime {
		slog.Warn("parseFetchLogConfigs: startTime >= endTime, defaulting to 1h window", "startTime", p.startTime, "endTime", p.endTime)
		p.startTime = p.endTime - time.Hour.Milliseconds()
	}

	if val, ok := configs["offset"]; ok {
		switch offsetValue := val.(type) {
		case string:
			offsetParsed, err := strconv.Atoi(offsetValue)
			if err != nil {
				return p, err
			}
			p.offset = offsetParsed
		case float64:
			p.offset = int(offsetValue)
		case int:
			p.offset = offsetValue
		case int64:
			p.offset = int(offsetValue)
		default:
			return p, fmt.Errorf("invalid offset value - %v", val)
		}
	}
	if val, ok := configs["request"]; ok {
		if valMap, ok := val.(map[string]any); ok {
			p.request = valMap
		} else {
			return p, fmt.Errorf("invalid request value - %v", val)
		}
	}
	if val, ok := configs["index"]; ok {
		if s, ok := val.(string); ok {
			p.index = s
		}
	}
	return p, nil
}

func executeFetchLogs(ctx core.NbToolContext, logProvider services_server.ObservabilityProvider, query string, configs map[string]any) (core.ObservabilityLogResponse, error) {
	if logProvider.Provider == "" {
		return core.ObservabilityLogResponse{}, errors.New("log_provider is required")
	}
	p, err := parseFetchLogConfigs(configs)
	if err != nil {
		return core.ObservabilityLogResponse{}, err
	}

	// Defensive per-provider limit clamp. Some backends reject above their own
	// ceiling (Loki: HTTP 400 `limit exceeds maximum of 5000`); rather than
	// letting that surface to the LLM as an opaque error, cap the request and
	// log it. A capped fetch is strictly more useful than a failed one.
	if newLimit, clamped := clampLogLimitForProvider(logProvider.Provider, p.limit); clamped {
		slog.Warn("executeFetchLogs: clamped limit to provider backend cap", "provider", logProvider.Provider, "requested", p.limit, "capped_to", newLimit)
		p.limit = newLimit
	}

	// Leave an empty source empty: api-server re-resolves it from the account's
	// integration. Forcing "agent" here would pin SaaS providers to the wrong backend.

	logRequest := services_server.LogQueryRequest{
		Query:             query,
		Limit:             p.limit,
		StartTime:         p.startTime,
		EndTime:           p.endTime,
		AccountId:         ctx.AccountId,
		LogProvider:       logProvider.Provider,
		LogProviderSource: logProvider.IntegrationSource,
		Offset:            p.offset,
		Request:           p.request,
		Index:             p.index,
	}
	logs, err := services_server.QueryLogs(*ctx.Ctx, logRequest)
	if err != nil {
		return core.ObservabilityLogResponse{}, err
	}
	return logs, nil
}

// executeFetchLogsCanonical sends a provider-independent canonical `where` clause
// to services-server with an EMPTY Query, so FetchLogs resolves canonical→provider
// labels and builds the native query server-side (the same path the UI query
// builder uses). Sibling to executeFetchLogs (which sends a pre-built
// provider-native Query); both share parseFetchLogConfigs. Used by the canonical
// fetch_logs v2 tool (logs_execute_v2).
//
// LogProvider/LogProviderSource ARE forwarded from the caller's already-resolved
// logProvider (mirroring the legacy path) — services-server falls back to its own
// account-default lookup when they're empty, so this is a no-op for the common
// case FetchLogsAgentV2 relies on (a well-formed account with exactly one default
// log provider: services-server would resolve the same provider either way). It
// stops being a no-op for a per-request provider override (e.g.
// ai_generate_log_query honoring the user's "Log Provider:" dropdown selection on
// an account with multiple integrations) — without forwarding these fields,
// services-server would silently re-resolve its own account default and ignore
// the override the agent already used to build the prompt.
func executeFetchLogsCanonical(ctx core.NbToolContext, logProvider services_server.ObservabilityProvider, where core.QueryWhereClause, configs map[string]any) (core.ObservabilityLogResponse, error) {
	p, err := parseFetchLogConfigs(configs)
	if err != nil {
		return core.ObservabilityLogResponse{}, err
	}

	// Same per-provider limit clamp as the legacy path (Loki rejects >5000).
	if newLimit, clamped := clampLogLimitForProvider(logProvider.Provider, p.limit); clamped {
		slog.Warn("executeFetchLogsCanonical: clamped limit to provider backend cap", "provider", logProvider.Provider, "requested", p.limit, "capped_to", newLimit)
		p.limit = newLimit
	}

	logRequest := services_server.LogQueryRequest{
		Query:             "",
		Limit:             p.limit,
		StartTime:         p.startTime,
		EndTime:           p.endTime,
		AccountId:         ctx.AccountId,
		LogProvider:       logProvider.Provider,
		LogProviderSource: logProvider.IntegrationSource,
		Offset:            p.offset,
		Request:           p.request,
		Index:             p.index,
		QueryRequest:      &services_server.LogsQueryBuilderRequest{Where: where},
		// Opt into label-name validation: on an empty/failed result the agent gets
		// an actionable error naming the mistyped label instead of a silent empty
		// result, so it can self-correct. Only meaningful on this canonical path,
		// which sends the structured where-clause services-server validates.
		ValidateRequest: false,
	}

	logs, err := services_server.QueryLogs(*ctx.Ctx, logRequest)
	if err != nil {
		return core.ObservabilityLogResponse{}, err
	}
	return logs, nil
}

// GetLogsQueryPreview resolves a canonical where-clause into the provider-native
// query text via GetLogsQuery/logs_get_query — no execution against the real
// backend, no log data fetched. Sibling to executeFetchLogsCanonical, but for
// callers (ai_generate_log_query) whose job is only to show the resolved query
// text, not to also validate it by actually running it. Exported for LogQueryAgent,
// which calls it directly (not through a tool.Call) since there is no log data
// to fetch or tool-config to resolve — just account/tenant context.
func GetLogsQueryPreview(ctx *security.RequestContext, accountId string, logProvider services_server.ObservabilityProvider, where core.QueryWhereClause, configs map[string]any) (string, error) {
	p, err := parseFetchLogConfigs(configs)
	if err != nil {
		return "", err
	}

	logRequest := services_server.LogQueryRequest{
		StartTime:         p.startTime,
		EndTime:           p.endTime,
		Limit:             p.limit,
		Offset:            p.offset,
		AccountId:         accountId,
		LogProvider:       logProvider.Provider,
		LogProviderSource: logProvider.IntegrationSource,
		Index:             p.index,
		QueryRequest:      &services_server.LogsQueryBuilderRequest{Where: where},
	}

	output, err := services_server.GetLogsQuery(*ctx, logRequest)
	if err != nil {
		return "", err
	}
	return output.Query, nil
}

func executeFetchLogLabels(accountId string, logProvider services_server.ObservabilityProvider) (core.ObservabilityLogLabelResponse, error) {
	if logProvider.Provider == "" {
		return core.ObservabilityLogLabelResponse{}, errors.New("log_provider is required")
	}

	tenantId, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil {
		return core.ObservabilityLogLabelResponse{}, err
	}
	ctx := security.NewRequestContextForTenantAccountAdmin(tenantId, "", []string{accountId})

	labels, err := services_server.QueryLogLabels(*ctx, accountId, logProvider)
	if err != nil {
		return core.ObservabilityLogLabelResponse{}, err
	}
	return labels, nil
}

// executeFetchTraceLabels returns the live label names for the account's trace
// provider (canonical fields ∪ merged mapping ∪ backend-discovered attribute keys)
// via the traces_list_labels action. Trace counterpart of executeFetchLogLabels.
func executeFetchTraceLabels(accountId string, traceProvider services_server.ObservabilityProvider) ([]string, error) {
	if traceProvider.Provider == "" {
		return nil, errors.New("trace_provider is required")
	}
	tenantId, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil {
		return nil, err
	}
	ctx := security.NewRequestContextForTenantAccountAdmin(tenantId, "", []string{accountId})
	return services_server.QueryTraceLabels(*ctx, accountId, traceProvider)
}

// FetchTraceLabelKeys returns the live trace label names for prompt building,
// degrading to an empty slice (with a warn) on error so a label-discovery failure
// never blocks query generation. Exported for the v2 traces agent — mirrors
// NBLogTool.QueryLabels for the log agent.
func FetchTraceLabelKeys(accountId string, traceProvider services_server.ObservabilityProvider) []string {
	labels, err := executeFetchTraceLabels(accountId, traceProvider)
	if err != nil {
		slog.Warn("traces: unable to fetch provider labels", "error", err,
			"provider", traceProvider.Provider, "account_id", accountId)
		return []string{}
	}
	return labels
}

func getProvider(accountId, providerType, requestedProvider string) (services_server.ObservabilityProvider, error) {
	if accountId == "" || providerType == "" {
		return services_server.ObservabilityProvider{}, fmt.Errorf("accountId or providerType cannot be empty")
	}

	tenantId, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil {
		return services_server.ObservabilityProvider{}, err
	}
	securityContext := security.NewRequestContextForTenantAccountAdmin(tenantId, "", []string{accountId})
	provider, err := services_server.GetObservabilityProvider(*securityContext, accountId, providerType, requestedProvider)
	if err != nil {
		return services_server.ObservabilityProvider{}, err
	}
	return provider, nil
}

func executeFetchTrace(ctx core.NbToolContext, traceProvider string, traceProviderSource string, query string, queryBuilder core.TraceQueryBuilder, config map[string]any) (core.ObservabilityTraceResponse, error) {
	limit := 1000
	if val, ok := config["limit"]; ok {
		switch limitValue := val.(type) {
		case string:
			limit1, err := strconv.Atoi(limitValue)
			if err != nil {
				return core.ObservabilityTraceResponse{}, err
			} else {
				limit = limit1
			}
		case float64:
			limit = int(limitValue)
		case int:
			limit = limitValue
		case int64:
			limit = int(limitValue)
		default:
			return core.ObservabilityTraceResponse{}, fmt.Errorf("invalid limit value - %v", val)

		}
	}
	endTime := int64(time.Now().UnixMilli())
	if val, ok := config["end_time"]; ok {
		if intVal, ok := val.(int64); ok {
			endTime = intVal
		} else {
			return core.ObservabilityTraceResponse{}, fmt.Errorf("invalid end_time value - %v", val)
		}
	}
	startTime := int64(time.Now().Add(-1 * time.Hour).UnixMilli())
	if val, ok := config["start_time"]; ok {
		if intVal, ok := val.(int64); ok {
			startTime = intVal
		} else {
			return core.ObservabilityTraceResponse{}, fmt.Errorf("invalid start_time value - %v", val)
		}
	}

	offset := 0
	if val, ok := config["offset"]; ok {
		switch offsetValue := val.(type) {
		case string:
			offsetParsed, err := strconv.Atoi(offsetValue)
			if err != nil {
				return core.ObservabilityTraceResponse{}, err
			} else {
				offset = offsetParsed
			}
		case float64:
			offset = int(offsetValue)
		case int:
			offset = offsetValue
		case int64:
			offset = int(offsetValue)
		default:
			return core.ObservabilityTraceResponse{}, fmt.Errorf("invalid offset value - %v", val)
		}
	}
	queryBuilder.Offset = offset
	queryBuilder.Limit = limit
	traceRequest := core.ObservabilityTracesV3Request{
		AccountId:      ctx.AccountId,
		ProviderType:   traceProvider,
		ProviderSource: traceProviderSource,
		StartTime:      startTime,
		EndTime:        endTime,
		Limit:          limit,
		Offset:         offset,
		QueryRequest:   queryBuilder,
		Query:          query,
		// Only the free-form ClickHouse SQL path (traces_execute) asks for the raw column/row
		// table, so aggregation / custom-projection queries keep their real values instead of
		// being zeroed by the fixed span-schema mapping. Other providers and the structured
		// query-builder path keep the typed span array.
		IncludeRawResult: traceProvider == "otel_clickhouse" && query != "",
	}
	traces, err := services_server.QueryTraces(*ctx.Ctx, traceRequest)
	if err != nil {
		return core.ObservabilityTraceResponse{}, err
	}
	return traces, nil
}

// executeFetchTraceCanonical is the integration-agnostic (v2) trace executor: it sends
// the canonical where-clause with an EMPTY provider so services-server resolves the
// account's default trace provider and translates the query per its label mapping
// (mirrors executeFetchLogsCanonical). Query is always empty and IncludeRawResult false
// — the raw-SQL path stays on the dedicated ClickHouse agent. Reuses executeFetchTrace
// for time/limit/offset parsing and the QueryTraces round-trip; returns errors verbatim
// (never a soft-completion "no traces" string).
func executeFetchTraceCanonical(ctx core.NbToolContext, queryBuilder core.TraceQueryBuilder, configs map[string]any) (core.ObservabilityTraceResponse, error) {
	providerType := ""
	providerSource := ""
	// Dev-only escape hatch for local setups with no integration rows for services-server
	// to resolve: pin the provider so the query targets that backend.
	if override := strings.TrimSpace(config.Config.LLMServerTraceProviderOverride); override != "" {
		providerType = override
	}
	return executeFetchTrace(ctx, providerType, providerSource, "", queryBuilder, configs)
}

func GetTraceProvider(accountId string) (services_server.ObservabilityProvider, error) {
	providerFromServicesServer, err := getProvider(accountId, "traces", "")
	if err == nil {
		if providerFromServicesServer.Provider != "" {
			return providerFromServicesServer, nil
		}
	}

	if err != nil {
		slog.Warn("trace: could not fetch provider from services-server, falling back to default provider", "error", err, "accountId", accountId)
	}

	// No first-class trace provider configured. For a cloud-ONLY GCP/Azure account
	// (no connected k8s agent) the ClickHouse default can't answer, so fall back to
	// the account's cloud CLI (Cloud Trace / Application Insights). The
	// hasConnectedK8sAgent guard keeps a GKE/AKS account on its in-cluster
	// ClickHouse. AWS/unknown keep the clickhouse default.
	if cloud := cloudFallbackProvider(accountId); cloud != "" && !hasConnectedK8sAgent(accountId) {
		return services_server.ObservabilityProvider{
			IntegrationSource: "agent",
			Provider:          cloud,
		}, nil
	}

	traceProvider := "clickhouse"
	return services_server.ObservabilityProvider{
		IntegrationSource: "agent",
		Provider:          traceProvider,
	}, nil
}

func GetMetricsProvider(accountId string) (services_server.ObservabilityProvider, error) {
	metricsConnectionProvider := "prometheus"
	providerFromServicesServer, err := getProvider(accountId, "metrics", "")
	if err == nil {
		if providerFromServicesServer.Provider != "" {
			return providerFromServicesServer, nil
		}
	}

	if err != nil {
		slog.Warn("metrics: could not fetch provider from services-server, falling back to local DB", "error", err, "accountId", accountId)
	}

	// No first-class metrics provider (Prometheus / Datadog / Elasticsearch) is
	// configured. For a cloud-ONLY GCP/Azure account (no connected k8s agent) the
	// in-cluster Prometheus default can't answer, so fall back to the account's
	// cloud CLI (Cloud Monitoring / Azure Monitor). The hasConnectedK8sAgent guard
	// keeps a GKE/AKS account that merely also has cloud creds on its cluster's
	// Prometheus. AWS and unknown providers keep the prometheus default.
	if cloud := cloudFallbackProvider(accountId); cloud != "" && !hasConnectedK8sAgent(accountId) {
		return services_server.ObservabilityProvider{
			Provider:          cloud,
			IntegrationSource: "agent",
		}, nil
	}

	return services_server.ObservabilityProvider{
		Provider:          metricsConnectionProvider,
		IntegrationSource: "agent",
	}, nil
}

// cloudFallbackProvider returns "gcp" or "azure" when the account is a GCP/Azure
// cloud account, else "". It is the shared "decide the fallback observability
// backend from the account's cloud type" hook used by the Get*Provider resolvers
// when no first-class observability provider (Loki/Prometheus/Datadog/…) is
// configured. AWS is intentionally excluded — it has no CLI-based observability
// fallback wired here and keeps the existing k8s/prometheus/clickhouse defaults.
// A lookup failure degrades to "" (no fallback) so provider resolution never
// blocks on a missing cloud_accounts row.
func cloudFallbackProvider(accountId string) string {
	if _, err := uuid.Parse(accountId); err != nil {
		return ""
	}
	creds, err := GetCloudAccountCredentials(accountId)
	if err != nil {
		slog.Warn("observability: could not resolve cloud type for CLI fallback", "error", err, "accountId", accountId)
		return ""
	}
	return cloudProviderToObservabilityFallback(creds.CloudProvider)
}

// cloudProviderToObservabilityFallback maps a cloud_accounts.cloud_provider value
// to the observability fallback provider name, or "" when the cloud has no
// CLI-based fallback wired here. Pure (no DB) so the mapping — including the
// case/whitespace normalization and the deliberate AWS/unknown exclusion — is
// unit-testable in isolation from GetCloudAccountCredentials.
func cloudProviderToObservabilityFallback(cloudProvider string) string {
	switch strings.ToLower(strings.TrimSpace(cloudProvider)) {
	case "gcp":
		return "gcp"
	case "azure":
		return "azure"
	}
	return ""
}

// hasConnectedK8sAgent reports whether the account has a CONNECTED in-cluster
// Kubernetes agent. When it does, in-cluster observability (Prometheus /
// ClickHouse / streamed pod logs) is authoritative for the account's workloads,
// so the cloud-CLI observability fallback must NOT fire — otherwise a GKE/AKS
// account that also has GCP/Azure cloud creds connected would be pulled off its
// cluster's Prometheus/ClickHouse and onto gcloud monitoring / Azure Monitor,
// where the pod-level data the k8s orchestrator wants does not live. Mirrors the
// connected-agent probe in tools/core/tool_config.go. Fails closed (treats a DB
// error as "agent present") so a transient error never silently enables the
// hijack this guard exists to prevent.
func hasConnectedK8sAgent(accountId string) bool {
	if _, err := uuid.Parse(accountId); err != nil {
		return false
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Warn("observability: could not check k8s agent presence; assuming present", "error", err, "accountId", accountId)
		return true
	}
	var connectedCount int
	if err := dbms.Db.Get(&connectedCount, "select count(*) from agent where status = 'CONNECTED' and cloud_account_id = $1", accountId); err != nil {
		slog.Warn("observability: k8s agent presence query failed; assuming present", "error", err, "accountId", accountId)
		return true
	}
	return connectedCount > 0
}

func GetLogProvider(accountId string) (services_server.ObservabilityProvider, error) {
	logConnectionProvider := "k8s"
	providerFromServicesServer, err := getProvider(accountId, "logs", "")
	if err == nil {
		if providerFromServicesServer.Provider != "" {
			return providerFromServicesServer, nil
		}
	}

	if err != nil {
		slog.Warn("logs: could not fetch provider from services-server, falling back to local DB", "error", err, "accountId", accountId)
	}

	if _, err := uuid.Parse(accountId); err != nil {
		return services_server.ObservabilityProvider{Provider: logConnectionProvider, IntegrationSource: "agent"}, nil
	}

	// Fallback to fetching from DB
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("logs: unable to fetch dbms", "error", err)
		return services_server.ObservabilityProvider{}, err
	}
	rows, err := dbms.Db.Queryx("select connection_status::text, status from agent where cloud_account_id = $1", accountId)
	if err != nil {
		slog.Error("logs: unable to fetch dbms", "error", err)
		return services_server.ObservabilityProvider{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("logs: failed to close rows", "error", err)
		}
	}()

	explicitlySet := false
	hasConnectedAgent := false
	for rows.Next() {
		var connectionStatusString *string
		var agentStatus string
		err := rows.Scan(&connectionStatusString, &agentStatus)
		if err != nil {
			slog.Error("logs: unable to scan rows", "error", err)
			break
		}
		if agentStatus == "CONNECTED" {
			hasConnectedAgent = true
		}
		connectionStatus := map[string]any{}
		if connectionStatusString != nil {
			err = common.UnmarshalJson([]byte(*connectionStatusString), &connectionStatus)
			if err != nil {
				slog.Error("logs: unable to unmarshal rows", "error", err)
				break
			}
		}
		logConnectionProvider1 := connectionStatus["logsConnectionProvider"]
		if s, ok := logConnectionProvider1.(string); ok {
			logConnectionProvider = s
			explicitlySet = true
		} else {
			slog.Info("logs: unable to find log connection provider, will be using default")
		}
	}

	// No log provider was explicitly configured (neither services-server nor the
	// agent connection_status named one). For a cloud-ONLY GCP/Azure account (no
	// connected k8s agent) the bare "k8s" default can't read logs, so fall back to
	// the account's cloud CLI (Cloud Logging / Log Analytics). Both gates matter: an
	// explicit provider (even "k8s" from a GKE/AKS agent that streams logs) is
	// respected via explicitlySet, and the hasConnectedAgent check additionally
	// covers a connected agent whose status omitted logsConnectionProvider.
	// AWS/unknown keep the k8s default.
	if !explicitlySet {
		if cloud := cloudFallbackProvider(accountId); cloud != "" && !hasConnectedAgent {
			return services_server.ObservabilityProvider{Provider: cloud, IntegrationSource: "agent"}, nil
		}
	}

	return services_server.ObservabilityProvider{Provider: logConnectionProvider, IntegrationSource: "agent"}, nil
}

// GetLogProviderWithOverride resolves the account's log provider, optionally
// pinned to requestedProvider (e.g. "loki") instead of the account default —
// mirrors the logs tab's provider switcher (log_provider on observability.fetchLogs).
// Empty requestedProvider behaves exactly like GetLogProvider. Unlike
// GetLogProvider, an override that fails to resolve (or isn't configured for
// this account) is returned as an error rather than silently falling back to
// the account's default provider — a caller that explicitly asked for a
// specific provider should be told it isn't available, not served a different
// one without realizing it.
func GetLogProviderWithOverride(accountId, requestedProvider string) (services_server.ObservabilityProvider, error) {
	if requestedProvider == "" {
		return GetLogProvider(accountId)
	}
	provider, err := getProvider(accountId, "logs", requestedProvider)
	if err != nil {
		return services_server.ObservabilityProvider{}, fmt.Errorf("logs: unable to resolve requested provider %q: %w", requestedProvider, err)
	}
	if provider.Provider == "" {
		return services_server.ObservabilityProvider{}, fmt.Errorf("logs: requested provider %q is not configured for this account", requestedProvider)
	}
	return provider, nil
}

func HasDatadogIntegration(accountId string) bool {
	if accountId == "" {
		return false
	}

	// Query database for Datadog integration
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("HasDatadogIntegration: unable to get database manager", "error", err)
		return false
	}

	// Check if account has Datadog integration configured
	// Based on pattern from tools/tool_docs.go:204 and agents/core/llm_common.go:1443
	query := `
		SELECT COUNT(*)
		FROM integrations i
		JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
		WHERE i.type = 'datadog' AND ia.cloud_account_id = $1 and i.status = 'enabled'
	`

	var count int
	err = dbms.Db.Get(&count, query, accountId)
	if err != nil {
		slog.Warn("HasDatadogIntegration: error querying integrations", "error", err, "accountId", accountId)
		return false
	}

	return count > 0
}
