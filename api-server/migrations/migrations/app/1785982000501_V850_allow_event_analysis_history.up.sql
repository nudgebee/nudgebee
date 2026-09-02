CREATE TABLE IF NOT EXISTS public.event_analysis_mapping (
    event_id uuid NOT NULL,
    analysis_id uuid NOT NULL,
    analysis_type text NOT NULL,
    PRIMARY KEY (event_id, analysis_type),
    FOREIGN KEY (event_id) REFERENCES public.events(id) ON UPDATE RESTRICT ON DELETE CASCADE,
    FOREIGN KEY (analysis_id) REFERENCES public.event_log_analysis(id) ON UPDATE RESTRICT ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_event_analysis_mapping_analysis_id ON public.event_analysis_mapping (analysis_id);

-- The live unique constraint on this tuple is the one added by V495
-- (1742248467003). Its name must be matched exactly: DROP CONSTRAINT IF EXISTS
-- against a name that does not exist is a silent no-op, which would leave the
-- constraint in place and make every second analysis row for a fingerprint fail
-- with a unique violation -- i.e. this migration would appear to succeed while
-- the feature it enables does not work.
ALTER TABLE public.event_log_analysis
    DROP CONSTRAINT IF EXISTS event_log_analysis_event_fingerprint_cloud_account_id_event_agg;

-- Predecessor from V449, superseded by V495. Dropped defensively so this
-- migration is correct on any environment still carrying the older name.
ALTER TABLE public.event_log_analysis
    DROP CONSTRAINT IF EXISTS event_log_analysis_fingerprint_aggregationkey_accountid;

-- Trailing sort key must match the ORDER BY expression used by the latest-row
-- lookups (COALESCE(updated_at, recorded_at) DESC). updated_at is nullable with
-- no default, so those queries cannot sort on updated_at alone; indexing the
-- bare column leaves the planner reading every row for the fingerprint and
-- sorting. That row count is unbounded now that the unique constraint above is
-- dropped. Both columns are `timestamp` (no time zone) and must stay the same
-- type as each other: COALESCE over a matched pair is immutable, but converting
-- only one of them to timestamptz introduces an implicit cast that is merely
-- stable, and this CREATE INDEX would then fail with "functions in index
-- expression must be marked IMMUTABLE".
CREATE INDEX IF NOT EXISTS idx_event_log_analysis_fingerprint_account_agg_type
ON public.event_log_analysis (event_fingerprint, cloud_account_id, event_aggregation_key, analysis_type, COALESCE(updated_at, recorded_at) DESC);
