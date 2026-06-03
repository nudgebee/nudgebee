
alter table "public"."runbook_custom_action" drop constraint "runbook_custom_action_status_fkey";

DELETE FROM "public"."runbook_custom_action_status" WHERE "value" = 'DISABLED';

DELETE FROM "public"."runbook_custom_action_status" WHERE "value" = 'ACTIVE';

alter table "public"."runbook_custom_action_status" drop constraint "runbook_custom_action_status_pkey";

comment on column "public"."runbook_custom_action_status"."key" is E'stores enum for custom action status for enum';
alter table "public"."runbook_custom_action_status" alter column "key" drop not null;
alter table "public"."runbook_custom_action_status" add column "key" text;

alter table "public"."runbook_custom_action_status"
    add constraint "runbook_custom_action_status_pkey"
    primary key ("key");

DROP TABLE "public"."runbook_custom_action_status";

alter table "public"."runbook_custom_action" drop constraint "runbook_custom_action_library_id_fkey";

DROP TABLE "public"."runbook_custom_action_library";

DROP TABLE "public"."runbook_custom_action";
