
alter table "public"."auto_pilot_task" drop constraint "auto_pilot_task_account_id_fkey";

alter table "public"."auto_pilot_task" alter column "account_id" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- update
--     auto_pilot_task as apt
-- set
--     account_id = (
--         select
--             account_id
--         from
--             auto_pilot ap
--         where
--             ap.id = apt.auto_pilot_id
--     );

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_task" add column "account_id" uuid
--  null;
