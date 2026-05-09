ALTER TABLE recommendation
    ADD COLUMN IF NOT EXISTS dedupe_group TEXT;

CREATE INDEX IF NOT EXISTS recommendation_dedupe_group_idx
    ON recommendation (cloud_account_id, dedupe_group, category)
    WHERE dedupe_group IS NOT NULL;
