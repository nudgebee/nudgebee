-- Progress of one-off background data backfills run by services (first user:
-- cloud-collector's redaction of secret values already stored in
-- cloud_resourses.meta). A row per backfill: last_id is the keyset cursor so a
-- restarted pod resumes, completed_at marks the pass done so it never reruns.
-- New empty table only; no lock on existing tables.
CREATE TABLE IF NOT EXISTS public.data_backfill_runs (
    name         text PRIMARY KEY,
    last_id      text,
    completed_at timestamptz,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
