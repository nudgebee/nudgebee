-- Remove backfilled data
-- Only removes records with retry_attempt = 0 and NULL latency (backfilled records)
-- Preserves new records created after migration
DELETE FROM llm_conversation_token_usage
WHERE retry_attempt = 0
  AND latency_seconds IS NULL
  AND fallback_from_model IS NULL;

DO $$
DECLARE
    deleted_count INT;
BEGIN
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RAISE NOTICE 'Backfilled records removed: %', deleted_count;
END $$;
