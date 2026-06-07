-- Store the failure reason directly on the KB row so the UI can show *why* a
-- load failed without depending on a rag_embedding_token_usage row (which is
-- only written when the failure happens inside the rag-server — llm-server-side
-- failures, e.g. the embedding call never reaching rag-server, leave no such
-- row). Cleared (set NULL) whenever the KB returns to processing/active.
ALTER TABLE llm_knowledgebases
ADD COLUMN IF NOT EXISTS error_message TEXT;

COMMENT ON COLUMN llm_knowledgebases.error_message IS
'Reason for the most recent failed load when status = error; NULL otherwise.';

-- Best-effort backfill for existing error KBs that DO have a recorded failure
-- row, so the reason shows up without needing a re-trigger. KBs that failed
-- before any row was written stay NULL until their next load attempt.
UPDATE llm_knowledgebases kb
SET error_message = last_err.error_message
FROM (
    SELECT DISTINCT ON (r.collection_name) r.collection_name, r.error_message
    FROM rag_embedding_token_usage r
    WHERE r.operation_type = 'batch_embedding'
      AND r.request_status = 'failure'
      AND COALESCE(r.error_message, '') != ''
    ORDER BY r.collection_name, r.created_at DESC
) last_err
WHERE kb.status = 'error'
  AND kb.error_message IS NULL
  AND last_err.collection_name = CASE
        WHEN kb.kb_type = 'integration' AND kb.integration_id IS NOT NULL
        THEN kb.integration_id::text || '_knowledge_base'
        ELSE 'kb_' || kb.id::text
    END;
