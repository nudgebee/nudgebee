

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- -- Step 1: Delete from dependent table first
-- DELETE FROM feature_flag
-- WHERE feature_id IN ('GENERATE_RCA', 'NB_SLM', 'CHAT_SUGGESTIONS', 'LLM_BASED_CHAT', 'ACCOUNT_GRAFANA');
--
-- -- Step 2: Then delete from the main table
-- DELETE FROM feature
-- WHERE value IN ('GENERATE_RCA', 'NB_SLM', 'CHAT_SUGGESTIONS', 'LLM_BASED_CHAT', 'ACCOUNT_GRAFANA');
