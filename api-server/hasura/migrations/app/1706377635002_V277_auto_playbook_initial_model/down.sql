
DROP TABLE "public"."auto_playbook_task";

DROP TABLE "public"."auto_playbook";

comment on column "public"."auto_pilot_task"."account_id" is E'will track tasks scheduled by schedulers';
alter table "public"."auto_pilot_task" alter column "account_id" drop not null;
alter table "public"."auto_pilot_task" add column "account_id" uuid;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_task" add column "account_id" uuid
--  null;
