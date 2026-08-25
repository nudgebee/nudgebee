-- Drop the in-place value refresh tracking columns (#34959).
--
-- Rolling back loses the cooldown and cap history, so a pull request that had
-- already reached its refresh cap becomes eligible again on the next run.

ALTER TABLE public.recommendation_resolution
    DROP COLUMN IF EXISTS value_refresh_count,
    DROP COLUMN IF EXISTS last_value_refresh_at;
