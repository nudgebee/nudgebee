package tools

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/tools/core"
)

// nbctl passes the query it is given to the account's provider as written, so
// Nubi writes the provider's own query language. NbctlAccountProviders looks
// the providers up once per account, and the orchestrator prompt names them
// (NbctlProviderSummary).

// NbctlProviders names an account's logs and metrics providers (lower-cased)
// and their default index, where the provider has one.
type NbctlProviders struct {
	Logs, LogsIndex, Metrics, MetricsIndex string
}

const nbctlProvidersTTL = 5 * time.Minute

var nbctlProvidersCache = struct {
	sync.Mutex
	entries map[string]nbctlProvidersEntry
}{entries: map[string]nbctlProvidersEntry{}}

type nbctlProvidersEntry struct {
	providers NbctlProviders
	expires   time.Time
}

func init() {
	core.RegisterToolCacheInvalidator(func(accountId string) {
		nbctlProvidersCache.Lock()
		delete(nbctlProvidersCache.entries, accountId)
		nbctlProvidersCache.Unlock()
	})
}

// NbctlAccountProviders returns the account's logs and metrics providers. A
// lookup that fails leaves its fields empty. Cached per account for
// nbctlProvidersTTL; the result changes only when the account's integrations
// do, so the account's cached prompt stays stable.
func NbctlAccountProviders(accountId string) NbctlProviders {
	now := time.Now()
	nbctlProvidersCache.Lock()
	if e, ok := nbctlProvidersCache.entries[accountId]; ok && now.Before(e.expires) {
		nbctlProvidersCache.Unlock()
		return e.providers
	}
	nbctlProvidersCache.Unlock()

	// services-server's own answer, without the fallbacks GetLogProvider and
	// GetMetricsProvider add ("k8s", "prometheus", a cloud CLI): a fallback is
	// a guess that the logs and metrics actions may not serve, and it hides a
	// failed lookup. No provider leaves the group out of the prompt line.
	var out NbctlProviders
	failed := false
	if p, err := getProvider(accountId, "logs", ""); err != nil {
		slog.Warn("nbctl: unable to resolve log provider", "account_id", accountId, "error", err)
		failed = true
	} else {
		out.Logs, out.LogsIndex = strings.ToLower(p.Provider), p.DefaultIndex
	}
	if p, err := getProvider(accountId, "metrics", ""); err != nil {
		slog.Warn("nbctl: unable to resolve metrics provider", "account_id", accountId, "error", err)
		failed = true
	} else {
		out.Metrics, out.MetricsIndex = strings.ToLower(p.Provider), p.DefaultIndex
	}

	// A failed lookup is not cached, so a services-server blip does not leave
	// the account's prompt without its providers for the whole TTL.
	if !failed {
		nbctlProvidersCache.Lock()
		nbctlProvidersCache.entries[accountId] = nbctlProvidersEntry{providers: out, expires: now.Add(nbctlProvidersTTL)}
		nbctlProvidersCache.Unlock()
	}
	return out
}

// nbctlQueryLanguages names the query language nbctl must be given, by
// "group:provider" (provider as GetLogProvider / GetMetricsProvider return it,
// lower-cased). Pairs not listed get "that provider's own query language".
var nbctlQueryLanguages = map[string]string{
	"logs:loki":              "LogQL",
	"logs:es":                "a full Elasticsearch search body (Query DSL) such as `{\"query\":{\"bool\":{\"filter\":[...]}}}`; for counts set `\"size\":0` with a terms or date_histogram aggregation, which `-o json` returns as `series`",
	"logs:splunk_enterprise": "SPL",
	"logs:signoz":            "SigNoz query JSON",
	"logs:datadog":           "Datadog log search syntax",
	"logs:aws_cloudwatch":    "CloudWatch Logs Insights",
	"metrics:prometheus":     "PromQL",
	"metrics:es":             "a full Elasticsearch search body (Query DSL) such as `{\"query\":{\"bool\":{\"filter\":[...]}}}`, which `-o json` returns under `results[].payload`; pass `--index` for another metrics index",
}

// NbctlProviderSummary returns one line naming the account's logs and metrics
// providers, their default index and the query language nbctl must be given,
// for the orchestrator's system prompt.
func NbctlProviderSummary(accountId string) string {
	p := NbctlAccountProviders(accountId)
	var parts []string
	if part := nbctlProviderPart("logs", p.Logs, p.LogsIndex); part != "" {
		parts = append(parts, part)
	}
	if part := nbctlProviderPart("metrics", p.Metrics, p.MetricsIndex); part != "" {
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// nbctlServedProviders are the providers services-server's logs_list and
// metrics_list serve (observability getLogSource / getMetricsSource),
// lower-cased. llm-server's provider lookup can also return a fallback ("k8s",
// "aws", "gcp", "azure") that those actions cannot serve.
var nbctlServedProviders = map[string]bool{
	"logs:loki": true, "logs:signoz": true, "logs:datadog": true, "logs:observe": true, "logs:loggly": true,
	"logs:azure_app_insights": true, "logs:aws_cloudwatch": true, "logs:es": true, "logs:newrelic": true,
	"logs:splunk_observability_platform": true, "logs:dynatrace": true, "logs:solarwinds": true, "logs:pinot": true,
	"logs:hive": true, "logs:openobserve": true, "logs:splunk_enterprise": true, "logs:cubeapm": true,
	"metrics:prometheus": true, "metrics:es": true, "metrics:aws_cloudwatch": true, "metrics:azure_app_insights": true,
	"metrics:chronosphere": true, "metrics:cubeapm": true, "metrics:datadog": true, "metrics:dynatrace": true,
	"metrics:newrelic": true, "metrics:openobserve": true, "metrics:solarwinds": true, "metrics:splunk_enterprise": true,
	"metrics:splunk_observability_platform": true,
}

func nbctlProviderPart(group, provider, index string) string {
	provider = strings.ToLower(provider)
	if provider == "" {
		return ""
	}
	if !nbctlServedProviders[group+":"+provider] {
		return "`nbctl " + group + "` does not reach this account's " + group + "; use the " + group + " tool."
	}
	part := "Its " + group + " are in " + provider
	if index != "" {
		part += " (default index `" + index + "`)"
	}
	language, ok := nbctlQueryLanguages[group+":"+provider]
	if !ok {
		language = "that provider's own query language"
	}
	part += "; write `nbctl " + group + "` queries as " + language + "."
	if provider != "loki" && provider != "prometheus" {
		// The raw query needs the provider's own field names. list-labels mixes
		// in Nudgebee's short aliases (message, pod, level), which the raw query
		// does not know, so the reliable source is one fetched record.
		discover := "`nbctl logs list-labels --fields-only`"
		if group == "metrics" {
			discover = "`nbctl metrics list-metrics` and `nbctl metrics list-labels --metric <m>`"
		}
		part += " Use the provider's own field names: " + discover + " lists them with their types; the other names it shows (message, pod, level) are Nudgebee aliases a raw query does not know. Avoid wildcard matches on large text fields, which can run past the command's time limit."
	}
	return part
}
