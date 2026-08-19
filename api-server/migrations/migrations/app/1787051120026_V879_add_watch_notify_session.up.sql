-- Chat routing key (Slack "<channel>-<thread_ts>", Teams/GChat id) captured at registration
-- -> terminal notify routes back w/o the flaky lookup. Nullable: legacy/web rows fall back.
ALTER TABLE llm_watch_tasks
    ADD COLUMN IF NOT EXISTS notify_session text;
