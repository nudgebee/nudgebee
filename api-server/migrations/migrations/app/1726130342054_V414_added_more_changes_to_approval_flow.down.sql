
alter table "public"."auto_pilot_approvals" drop constraint "auto_pilot_approvals_status_fkey";

DELETE FROM "public"."auto_pilot_approval_status" WHERE "status" = 'REJECTED';

DELETE FROM "public"."auto_pilot_approval_status" WHERE "status" = 'APPROVED';

DELETE FROM "public"."auto_pilot_approval_status" WHERE "status" = 'PENDING';

DROP TABLE "public"."auto_pilot_approval_status";

alter table "public"."auto_pilot_approvals" drop constraint "auto_pilot_approvals_auto_pilot_type_fkey";

DELETE FROM "public"."auto_pilot_type" WHERE "type" = 'auto_optimize';

DELETE FROM "public"."auto_pilot_type" WHERE "type" = 'runbook';

DROP TABLE "public"."auto_pilot_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_approvals" add column "auto_pilot_type" text
--  not null;
