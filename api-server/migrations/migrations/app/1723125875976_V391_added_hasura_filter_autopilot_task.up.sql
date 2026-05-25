
alter table "public"."auto_pilot_task" add column "resource_filter" jsonb
 null default jsonb_build_object();
