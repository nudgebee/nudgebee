
alter table "public"."insights_summary" drop constraint "insight_type_check";

alter table "public"."insights_summary" drop constraint "entity_type_check";
alter table "public"."insights_summary" add constraint "entity_type_check" check (CHECK (entity_type = ANY (ARRAY['tenant'::text, 'account'::text, 'service'::text, 'resource'::text])));

alter table "public"."metrics_summary" drop constraint "entity_type_check";
alter table "public"."metrics_summary" add constraint "entity_type_check" check (CHECK (entity_type = ANY (ARRAY['tenant'::text, 'account'::text, 'service'::text, 'resource'::text])));

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."insights_summary" add column "insight_type" text
--  not null;
