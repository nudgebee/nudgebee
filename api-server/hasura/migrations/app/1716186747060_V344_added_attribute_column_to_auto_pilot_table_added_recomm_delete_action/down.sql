
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot" add column "attributes" jsonb
--  not null default jsonb_build_object();


DELETE FROM "public"."recommendation_action_type" WHERE "value" = 'Delete';
