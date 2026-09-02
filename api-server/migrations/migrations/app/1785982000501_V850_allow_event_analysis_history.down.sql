-- Collapse the history back to one row per tuple so the unique constraint can be
-- recreated. Keep the newest: updated_at is nullable with no default, and NULLs
-- sort first on a plain DESC, so ordering on the bare column would keep a legacy
-- row and delete the current one.
--
-- PARTITION BY lists the columns in the same order as
-- idx_event_log_analysis_fingerprint_account_agg_type, and that index is dropped
-- at the end of this script rather than the start, so the window can be fed
-- straight from it. Column order within PARTITION BY is semantically irrelevant
-- (same grouping either way), but the planner will not reorder partition keys to
-- match an index -- with any other order it falls back to a full sort of the
-- table, which spills to disk on a large one.
--
-- Rows with a NULL in any constraint column are left alone. Postgres treats NULLs
-- as distinct for UNIQUE, so those rows never block the constraint, while
-- PARTITION BY groups them together -- deduplicating them would destroy data the
-- constraint would happily accept.
DELETE FROM public.event_log_analysis a
USING (
    SELECT id, ROW_NUMBER() OVER (
        PARTITION BY event_fingerprint, cloud_account_id, event_aggregation_key, analysis_type
        ORDER BY COALESCE(updated_at, recorded_at) DESC
    ) as rn
    FROM public.event_log_analysis
    WHERE cloud_account_id IS NOT NULL
      AND event_fingerprint IS NOT NULL
      AND event_aggregation_key IS NOT NULL
      AND analysis_type IS NOT NULL
) b
WHERE a.id = b.id AND b.rn > 1;

-- Restore the constraint under the name and column order V495 (1742248467003)
-- established, so this is a true inverse of the up migration.
CREATE UNIQUE INDEX IF NOT EXISTS event_log_analysis_event_fingerprint_cloud_account_id_event_agg
ON public.event_log_analysis (event_fingerprint, cloud_account_id, event_aggregation_key, analysis_type);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'event_log_analysis_event_fingerprint_cloud_account_id_event_agg'
          AND conrelid = 'public.event_log_analysis'::regclass
    ) THEN
        ALTER TABLE public.event_log_analysis
            ADD CONSTRAINT event_log_analysis_event_fingerprint_cloud_account_id_event_agg
            UNIQUE USING INDEX event_log_analysis_event_fingerprint_cloud_account_id_event_agg;
    END IF;
END $$;

DROP TABLE IF EXISTS public.event_analysis_mapping;

-- Dropped last: the dedupe above reads through this index.
DROP INDEX IF EXISTS public.idx_event_log_analysis_fingerprint_account_agg_type;
