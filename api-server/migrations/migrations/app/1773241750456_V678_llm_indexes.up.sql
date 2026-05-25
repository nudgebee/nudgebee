-- Fix llm_conversation_messages polling performance
-- The query uses status = $1 AND worker_name NOT IN (...) ORDER BY created_at
CREATE INDEX IF NOT EXISTS idx_llm_conversation_messages_polling 
ON public.llm_conversation_messages (status, worker_name, created_at);

-- Fix llm_conversation_token_usage sequential scans
-- Used in Grafana Token Usage dashboard, currently 0 index scans
CREATE INDEX IF NOT EXISTS idx_llm_conversation_token_usage_conversation_id 
ON public.llm_conversation_token_usage (conversation_id);

-- Fix cloud_resourses account filtering
-- UNION queries currently seq-scan 7.5 GB because of missing single-column account index
CREATE INDEX IF NOT EXISTS idx_cloud_resourses_account_only 
ON public.cloud_resourses (account);

-- Optimize llm_conversations Hasura queries
-- Constant seq scans from Hasura queries often sorting by created_at
CREATE INDEX IF NOT EXISTS idx_llm_conversations_account_created_at 
ON public.llm_conversations (account_id, created_at DESC);
