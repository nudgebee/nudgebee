-- llm_watch_tasks: persisted background "watch" tasks for the llm-server.
-- A watch periodically observes a source (a tool call or a SQL query),
-- evaluates a predicate against the observation, and notifies the user's
-- conversation when the predicate is met or the watch expires.
--
-- The (source_kind, source_config) pair is intentionally generic so that
-- new sources (timer, http, listen, composite) can be added without a
-- schema change.

CREATE TABLE IF NOT EXISTS public.llm_watch_tasks (
    id                  uuid        NOT NULL DEFAULT gen_random_uuid(),
    conversation_id     uuid        NOT NULL,
    account_id          uuid        NOT NULL,
    tenant_id           uuid        NOT NULL,
    user_id             uuid        NULL,

    -- The agent reply that registered this watch. Lets the responder
    -- append the terminal "Watch update" block to the exact parent reply
    -- instead of "the most recent generation message" — which is wrong
    -- when the user keeps chatting while the watch runs (the responder
    -- would otherwise land the block on an unrelated newer reply).
    -- Nullable so legacy / migration-time rows fall back to the
    -- "latest generation" heuristic.
    parent_message_id   uuid        NULL,

    -- WHAT to observe each tick. v1 supports "tool" and "sql".
    source_kind         text        NOT NULL,
    source_config       jsonb       NOT NULL,

    -- HOW to decide we're done. v1 supports "regex", "substring", "llm_judge".
    predicate_kind      text        NOT NULL,
    predicate_expr      text        NOT NULL,
    predicate_negate    boolean     NOT NULL DEFAULT false,

    notify_template     text        NULL,
    poll_interval_sec   integer     NOT NULL,
    max_duration_sec    integer     NOT NULL,

    status              text        NOT NULL,
    poll_count          integer     NOT NULL DEFAULT 0,
    failure_count       integer     NOT NULL DEFAULT 0,

    next_poll_at        timestamptz NOT NULL,
    last_poll_at        timestamptz NULL,
    last_poll_result    text        NULL,
    final_result        text        NULL,
    error               text        NULL,
    expires_at          timestamptz NOT NULL,

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT llm_watch_tasks_pk PRIMARY KEY (id),
    CONSTRAINT llm_watch_tasks_conversation_fk
        FOREIGN KEY (conversation_id)
        REFERENCES public.llm_conversations(id)
        ON UPDATE RESTRICT ON DELETE CASCADE,
    CONSTRAINT llm_watch_tasks_status_check
        CHECK (status IN ('PENDING','ACTIVE','COMPLETED','EXPIRED','FAILED','CANCELLED')),
    CONSTRAINT llm_watch_tasks_source_kind_check
        CHECK (source_kind IN ('tool','sql')),
    CONSTRAINT llm_watch_tasks_predicate_kind_check
        CHECK (predicate_kind IN ('regex','substring','llm_judge')),
    CONSTRAINT llm_watch_tasks_intervals_check
        CHECK (poll_interval_sec > 0 AND max_duration_sec > 0)
);

CREATE INDEX IF NOT EXISTS llm_watch_tasks_due_idx
    ON public.llm_watch_tasks (next_poll_at)
    WHERE status IN ('PENDING','ACTIVE');

CREATE INDEX IF NOT EXISTS llm_watch_tasks_account_status_idx
    ON public.llm_watch_tasks (account_id, status);

CREATE INDEX IF NOT EXISTS llm_watch_tasks_tenant_status_idx
    ON public.llm_watch_tasks (tenant_id, status);

CREATE INDEX IF NOT EXISTS llm_watch_tasks_conversation_idx
    ON public.llm_watch_tasks (conversation_id);
