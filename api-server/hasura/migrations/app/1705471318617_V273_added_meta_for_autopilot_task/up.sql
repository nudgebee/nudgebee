
alter table "public"."auto_pilot_task" add column "meta" jsonb
 not null default jsonb_build_object();
