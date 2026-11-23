-- Same deletes as V1069. Roll the code back with it: the code this migration
-- ships with reads these rows, and without them custom roles stop applying.
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'CUSTOM_ROLES';
DELETE FROM "public"."billing_feature_mapping" WHERE "feature_id" = 'CUSTOM_ROLES';
DELETE FROM "public"."billing_plan_features" WHERE "feature_id" = 'CUSTOM_ROLES';
DELETE FROM "public"."feature" WHERE "value" = 'CUSTOM_ROLES';
