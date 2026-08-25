-- Attribution columns for model-tier experiments (e.g. the query→Summary tier
-- downshift, llm_server_react3_query_model_downshift_enabled). On a same-model
-- deployment a downshifted turn keeps the same llm_model, so llm_model alone
-- cannot tell a downshifted query turn from an ordinary one; and thinking_tokens
-- is a broken proxy because the per-tier thinking budget almost never binds.
-- These two columns make each call attributable in post-run review:
--   model_tier — the resolved ModelTier the call actually ran on
--                (reasoning / retrieval / summary). NULL on legacy rows and when
--                no tier was stamped on the request context.
--   task_type  — the top-level turn classification driving prompt/tier selection
--                (query = plain retrieval, investigation = RCA). NULL for
--                sub-agent calls (not classified) and legacy rows.
ALTER TABLE public.llm_conversation_token_usage
ADD COLUMN model_tier text NULL,
ADD COLUMN task_type  text NULL;

COMMENT ON COLUMN public.llm_conversation_token_usage.model_tier IS
'Resolved ModelTier this call ran on (reasoning/retrieval/summary), read from ContextKeyModelTier at write time. Attribution key for tier-downshift experiments. NULL when unstamped / legacy.';

COMMENT ON COLUMN public.llm_conversation_token_usage.task_type IS
'Top-level turn classification (query = plain retrieval, investigation = RCA) from ContextKeyTaskType. NULL for sub-agent calls (not top-level, not classified) and legacy rows. Enables query-vs-investigation segmentation and classifier-miss audits independent of any tier flag.';
