DROP INDEX IF EXISTS recommendation_dedupe_group_idx;

ALTER TABLE recommendation
    DROP COLUMN IF EXISTS dedupe_group;
