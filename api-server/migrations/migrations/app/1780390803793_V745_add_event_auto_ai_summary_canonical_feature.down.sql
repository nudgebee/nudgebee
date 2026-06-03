-- Only remove the canonical row if no feature_flag rows reference it; otherwise
-- a blind DELETE would fail the FK from feature_flag(feature_id) → feature(value).
DELETE FROM "public"."feature"
WHERE "value" = 'EVENT_AUTO_AI_SUMMARY'
  AND NOT EXISTS (
    SELECT 1 FROM "public"."feature_flag" WHERE "feature_id" = 'EVENT_AUTO_AI_SUMMARY'
  );
