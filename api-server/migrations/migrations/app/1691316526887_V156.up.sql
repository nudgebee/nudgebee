
alter table "public"."insights_summary" add column "insight_type" text
 not null;

alter table "public"."metrics_summary" drop constraint "entity_type_check";
alter table "public"."metrics_summary" add constraint "entity_type_check" check (entity_type = ANY (ARRAY['tenant'::text, 'account'::text, 'service'::text, 'resource'::text, 'service'::text]));

alter table "public"."insights_summary" drop constraint "entity_type_check";
alter table "public"."insights_summary" add constraint "entity_type_check" check (entity_type = ANY (ARRAY['tenant'::text, 'account'::text, 'service'::text, 'resource'::text, 'service'::text]));

alter table "public"."insights_summary" add constraint "insight_type_check" check (insight_type in ('alert', 'insight'));
