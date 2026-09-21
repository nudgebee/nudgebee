package agents

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/lib/pq"
)

// WebhookSubjectExtractorAgentName is the agent that maps an incoming monitoring
// alert (PagerDuty/Datadog/Zenduty/Prometheus webhook) to the running workload it
// concerns. It is invoked explicitly by api-server webhook handlers via an
// "@webhook_subject_name_extractor" query prefix — never inferred by the router.
const WebhookSubjectExtractorAgentName = "webhook_subject_name_extractor"

// WebhookSubjectExtractorNotFound is the sentinel classification choice returned
// when no running service confidently matches the alert.
const WebhookSubjectExtractorNotFound = "Not Found"

// webhookHistoricalAttrKeys are the webhook_subject_mappings.attr_key values that
// accumulate learned title→service mappings per webhook source. They are merged
// (dedup by title) into the agent's cached system prompt so any source's history
// aids resolution. Order is fixed so the rendered prompt stays byte-stable for
// prompt-cache hits.
var webhookHistoricalAttrKeys = []string{
	"DATADOG_INCIDENT_TITLE_SERVICE_MAPPING",
	"PAGERDUTY_INCIDENT_TITLE_SERVICE_MAPPING",
	"ZENDUTY_INCIDENT_TITLE_SERVICE_MAPPING",
}

func init() {
	core.RegisterNBAgentFactory(WebhookSubjectExtractorAgentName, func(accountId string) (core.NBAgent, error) {
		return &WebhookSubjectExtractorAgent{accountId: accountId}, nil
	})
}

// WebhookSubjectExtractorAgent is a single-shot classification agent. Its stable
// context — the running-service option list and the historical title→service
// patterns — is fetched server-side from the metastore and placed in the (cached)
// system message, so repeated webhooks for an account reuse it via prompt caching
// instead of api-server re-sending the whole inventory in every request.
type WebhookSubjectExtractorAgent struct {
	accountId string
}

func (a *WebhookSubjectExtractorAgent) GetName() string { return WebhookSubjectExtractorAgentName }

func (a *WebhookSubjectExtractorAgent) GetNameAliases() []string {
	return []string{WebhookSubjectExtractorAgentName}
}

func (a *WebhookSubjectExtractorAgent) GetDescription() string {
	return "Maps an incoming monitoring-alert webhook to the running k8s workload/service it concerns. " +
		"Single-shot classifier over the account's running services; returns one service name or \"Not Found\"."
}

func (a *WebhookSubjectExtractorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeClassification
}

// GetModelCategory routes this agent to the Summary tier — mapping an alert to a
// service name is an analysis/extraction task, not cheap retrieval.
func (a *WebhookSubjectExtractorAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierSummary
}

// GetCacheScope caches the stable system prompt (running services + historical
// patterns) at Account scope. The block is account-stable, not conversation-
// specific, and api-server sends a fresh ConversationId per webhook — so Account
// scope is what actually reuses across webhooks on both providers:
//   - Anthropic: the byte-identical system prefix is cache-read regardless of
//     conversation, and content is re-sent each call so it never goes stale.
//   - Google AI: the account-keyed CachedContent slot is reused across webhooks
//     (Conversation scope would mint a single-use slot per webhook — churn, no
//     reuse). Trade-off: the Google slot holds its snapshot up to its 12h TTL, so
//     newly-added workloads/mappings can lag by that window on Google AI.
//
// The classification planner plumbs this via ContextKeyCacheScope (see
// planner_classification.go); without that this method would be a no-op.
func (a *WebhookSubjectExtractorAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// GetSupportedTools returns none: this is a pure single-shot classifier.
func (a *WebhookSubjectExtractorAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return nil
}

func (a *WebhookSubjectExtractorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptWebhookSubjectExtractor, query.AccountId)
	if promptErr != nil {
		// Return nothing rather than continue. Unlike the agents that assemble
		// constraints in Go, this one carries only a Role and the loaded text, so a
		// failed load leaves nothing worth shipping — just a classifier role with a
		// blank instruction and some historical patterns. MustResolveAll covers
		// default/v1 at startup, so this only fires for a malformed override.
		ctx.GetLogger().Error("webhook subject extractor: system prompt failed to load", "error", promptErr)
		return core.NBAgentPrompt{}
	}
	instructions := []string{promptText}

	if patterns := a.historicalPatterns(ctx); patterns != "" {
		instructions = append(instructions, "\n## Historical Title -> Service Patterns\n"+patterns)
	}

	return core.NBAgentPrompt{
		Role:         "You are a webhook subject-name classifier for monitoring alerts.",
		Instructions: instructions,
	}
}

// GetOptions returns the classification choices: the account's running
// service/workload names plus the "Not Found" sentinel. Ordered deterministically
// (SQL ORDER BY) so the generated system message is byte-stable across webhooks — a
// precondition for prompt-cache hits.
func (a *WebhookSubjectExtractorAgent) GetOptions() []string {
	return append(a.runningServiceNames(), WebhookSubjectExtractorNotFound)
}

func (a *WebhookSubjectExtractorAgent) runningServiceNames() []string {
	tenantId, err := security.GetTenantIdFromAccountId(a.accountId)
	if err != nil {
		slog.Warn("webhook_subject_extractor: failed to resolve tenant for options", "error", err, "account_id", a.accountId)
		return nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Warn("webhook_subject_extractor: failed to get database manager for options", "error", err)
		return nil
	}
	var names []string
	err = dbms.Db.Select(&names, `
		SELECT DISTINCT name FROM k8s_workloads
		WHERE tenant_id = $1 AND cloud_account_id = $2
		  AND is_active = true AND kind NOT IN ('Job', 'CronJob')
		  AND name IS NOT NULL
		UNION
		SELECT DISTINCT labels->>'tags.datadoghq.com/service' FROM k8s_workloads
		WHERE tenant_id = $1 AND cloud_account_id = $2
		  AND is_active = true AND kind NOT IN ('Job', 'CronJob')
		  AND labels->>'tags.datadoghq.com/service' IS NOT NULL
		ORDER BY 1
	`, tenantId, a.accountId)
	if err != nil {
		slog.Warn("webhook_subject_extractor: failed to query running services", "error", err, "tenant_id", tenantId)
		return nil
	}
	return names
}

// historicalMappingRow mirrors the title/services columns of the api-server
// webhook_subject_mappings table for learned title→service mappings. services
// is a comma-separated list: the same generic alert title can legitimately
// resolve to more than one real service over time (it fires for many
// different pods), so a title isn't collapsed to a single value.
type historicalMappingRow struct {
	Title    string `db:"title"`
	Services string `db:"services"`
}

// maxHistoricalPatternLines caps how many learned "title -> service" lines reach
// the system prompt. webhook_subject_mappings is unbounded: the api-server
// backfill seeds up to 5000 incidents per source and LearnSubjectMapping adds a
// row for every new raw alert title, forever. Rendering all of them put ~200k
// tokens of context in front of a one-word classification (16.2M tokens over ~80
// observed calls), which is what this cap exists to stop.
const maxHistoricalPatternLines = 200

// historicalPatterns merges the learned title→service mappings across all webhook
// sources into a deterministic, deduplicated block for the system prompt. Each
// service historically seen for a title gets its own line.
//
// Only the most recently written mappings are kept (see maxHistoricalPatternLines).
// That is safe because this block is no longer what answers a repeat alert: a title
// whose learned mappings agree on one service is short-circuited in api-server
// (core.ResolveSubjectNameViaAgent) and never reaches the LLM at all. What is left
// for the agent is fuzzy matching of titles it has NOT seen before — and for that,
// recent examples are the representative ones, so evicting the oldest is the right
// trade.
func (a *WebhookSubjectExtractorAgent) historicalPatterns(ctx *security.RequestContext) string {
	tenantId, err := security.GetTenantIdFromAccountId(a.accountId)
	if err != nil {
		ctx.GetLogger().Warn("webhook_subject_extractor: failed to resolve tenant for history", "error", err)
		return ""
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("webhook_subject_extractor: failed to get database manager for history", "error", err)
		return ""
	}

	// One query across all sources so the cap selects the most recent mappings
	// overall, rather than the most recent per source. updated_at only moves when a
	// title learns a service it did not already have, so for the vast majority of
	// rows — written once, never revised — it is the time the title was first seen.
	// Ordering by it therefore keeps the newest titles, which is the intent.
	var rows []historicalMappingRow
	if err := dbms.Db.Select(&rows,
		`SELECT title, services FROM webhook_subject_mappings
		 WHERE tenant_id = $1 AND attr_key = ANY($2)
		 ORDER BY updated_at DESC, title
		 LIMIT $3`,
		tenantId, pq.Array(webhookHistoricalAttrKeys), maxHistoricalPatternLines,
	); err != nil {
		ctx.GetLogger().Warn("webhook_subject_extractor: failed to query historical mappings", "error", err)
		return ""
	}
	return renderHistoricalPatterns(rows)
}

// renderHistoricalPatterns turns mapping rows into the prompt block: one line per
// (title, service), deduplicated case-insensitively on the title, capped at
// maxHistoricalPatternLines. The lines are sorted before joining so the rendered
// block is byte-stable for a given set of rows — a precondition for the
// account-scoped prompt cache to hit, and the reason the DB's recency ordering is
// not carried through to the output.
func renderHistoricalPatterns(rows []historicalMappingRow) string {
	seen := map[string]bool{}
	lines := make([]string, 0, maxHistoricalPatternLines)
outer:
	for _, row := range rows {
		if row.Title == "" || row.Services == "" {
			continue
		}
		titleKey := strings.ToLower(row.Title)
		for _, svc := range strings.Split(row.Services, ",") {
			svc = strings.TrimSpace(svc)
			if svc == "" {
				continue
			}
			dedupKey := titleKey + "\x00" + svc
			if seen[dedupKey] {
				continue
			}
			seen[dedupKey] = true
			lines = append(lines, fmt.Sprintf("- %q -> %q\n", row.Title, svc))
			if len(lines) >= maxHistoricalPatternLines {
				break outer
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "")
}
