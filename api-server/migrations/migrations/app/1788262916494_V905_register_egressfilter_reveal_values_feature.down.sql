-- Enrolment rows first (feature_flag.feature_id FKs to feature.value), then
-- the registration itself.
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'EGRESSFILTER_REVEAL_VALUES';
DELETE FROM "public"."feature" WHERE "value" = 'EGRESSFILTER_REVEAL_VALUES';
