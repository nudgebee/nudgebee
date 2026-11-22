-- The identity of a governed agent run, written once when the conversation is
-- created: the extension that owns the run, its reference (for Worker Bees,
-- the bee run id) and the agent it runs. NULL for an ordinary conversation.
-- llm-server writes it only from a service-only claim on the request that
-- creates the conversation, and never updates it. Lock window: an instant
-- nullable column add.
ALTER TABLE public.llm_conversations ADD COLUMN IF NOT EXISTS run_profile jsonb;
