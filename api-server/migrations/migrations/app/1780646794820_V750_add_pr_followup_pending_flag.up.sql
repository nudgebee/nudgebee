-- pr_followup_pending coalesces "a new actionable PR signal (review / comment /
-- CI failure) arrived while a followup was already in flight (state='addressing')".
-- The webhook dispatcher sets it via an atomic claim-or-mark; the in-flight run's
-- finalize step reads-and-clears it and re-dispatches once if it was set, so a
-- signal landing mid-run is never lost and never waits for the cron. A single
-- boolean (not a counter/queue) intentionally coalesces N concurrent signals into
-- exactly one re-run — the agent re-reads current PR state and decides relevance.
ALTER TABLE event_resolution
    ADD COLUMN IF NOT EXISTS pr_followup_pending boolean NOT NULL DEFAULT false;

ALTER TABLE recommendation_resolution
    ADD COLUMN IF NOT EXISTS pr_followup_pending boolean NOT NULL DEFAULT false;
