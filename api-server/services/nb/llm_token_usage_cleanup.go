package nb

import (
	"fmt"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strconv"
)

// CleanupLLMTokenUsageShortTerm nulls out the captured prompt/response bodies on
// llm_conversation_token_usage rows that are old enough AND represent a
// "standard" successful call (within the latency/token thresholds). Anything
// that breaks a threshold is left untouched here and instead collected by the
// daily catch-all (CleanupLLMTokenUsageCatchAll) once it ages past the longer
// window. The (prompt_messages IS NOT NULL OR response_content IS NOT NULL)
// guard both protects already-cleaned rows and lets the batched loop terminate.
//
// Note: latency_seconds is nullable, so `latency_seconds <= threshold`
// intentionally excludes rows with NULL latency from this short pass — they are
// swept up by the catch-all instead.
func CleanupLLMTokenUsageShortTerm(ctx *security.RequestContext) error {
	job := dataCleanupJob{
		Name:      "llm_token_usage_short_term_cleanup",
		Metastore: database.Metastore,
		Batched:   true,
		Query: fmt.Sprintf(`WITH to_cleanup AS (
			SELECT id FROM llm_conversation_token_usage
			WHERE request_status = 'success'
			  AND created_at < now() - interval '%d hours'
			  AND latency_seconds <= %s
			  AND input_tokens <= %d
			  AND output_tokens <= %d
			  AND (prompt_messages IS NOT NULL OR response_content IS NOT NULL)
			LIMIT %d
		) UPDATE llm_conversation_token_usage
		  SET prompt_messages = NULL, response_content = NULL
		  WHERE id IN (SELECT id FROM to_cleanup)`,
			config.Config.LLMTokenUsageCleanupShortTermHours,
			strconv.FormatFloat(config.Config.LLMTokenUsageCleanupMaxLatencySeconds, 'f', -1, 64),
			config.Config.LLMTokenUsageCleanupMaxInputTokens,
			config.Config.LLMTokenUsageCleanupMaxOutputTokens,
			cleanupBatchSize),
	}
	return executeBatchedMetastoreJob(ctx, job.Name, job.Query)
}

// CleanupLLMTokenUsageCatchAll nulls out the prompt/response bodies on every
// successful llm_conversation_token_usage row past the longer retention window,
// regardless of latency/token size. This collects the heavy/complex calls that
// the short-term pass deliberately skips. Disk space is reclaimed by autovacuum;
// no explicit VACUUM is issued (plain VACUUM does not shrink on-disk size, and
// VACUUM FULL would take an ACCESS EXCLUSIVE lock unsafe from a cron).
func CleanupLLMTokenUsageCatchAll(ctx *security.RequestContext) error {
	job := dataCleanupJob{
		Name:      "llm_token_usage_catch_all_cleanup",
		Metastore: database.Metastore,
		Batched:   true,
		Query: fmt.Sprintf(`WITH to_cleanup AS (
			SELECT id FROM llm_conversation_token_usage
			WHERE request_status = 'success'
			  AND created_at < now() - interval '%d days'
			  AND (prompt_messages IS NOT NULL OR response_content IS NOT NULL)
			LIMIT %d
		) UPDATE llm_conversation_token_usage
		  SET prompt_messages = NULL, response_content = NULL
		  WHERE id IN (SELECT id FROM to_cleanup)`,
			config.Config.LLMTokenUsageCleanupCatchAllDays,
			cleanupBatchSize),
	}
	return executeBatchedMetastoreJob(ctx, job.Name, job.Query)
}
