-- Background jobs (watch summariser, semantic judge) record token usage with
-- no conversation or account. EE makes these columns nullable inside its
-- memory-module migration (V765), which OSS excludes, so OSS needs its own.
-- DROP NOT NULL is idempotent and metadata-only: brief ACCESS EXCLUSIVE lock,
-- no table rewrite.
ALTER TABLE public.llm_conversation_token_usage
    ALTER COLUMN conversation_id DROP NOT NULL,
    ALTER COLUMN account_id      DROP NOT NULL;
