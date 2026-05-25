
INSERT INTO "public"."upgrade_plan_status_type"("comment", "value") VALUES (null, E'Incomplete');

alter table "public"."upgrade_plan_tasks" add column "resource_type" text
 null;

alter table "public"."upgrade_plan_tasks" add column "is_required" boolean
 not null default 'false';
