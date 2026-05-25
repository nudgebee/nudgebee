
alter table "public"."auto_pilot_approval_policy" drop constraint "auto_pilot_approval_policy_tenant_id_key";

alter table "public"."auto_pilot_reviewers" drop constraint "auto_pilot_reviewers_user_id_tenant_id_key";

alter table "public"."auto_pilot_reviewee" drop constraint "auto_pilot_reviewee_user_id_tenant_id_key";

DELETE FROM "public"."auto_pilot_status" WHERE "value" = 'DRAFT';

DELETE FROM "public"."auto_playbook_status" WHERE "value" = 'DRAFT';

alter table "public"."auto_pilot_approval_policy" alter column "updated_by" set not null;

alter table "public"."auto_pilot_approval_policy" alter column "updated_at" set not null;

alter table "public"."auto_pilot_approval_policy" rename column "tenant_id" to "tanant_id";


alter table "public"."auto_pilot_approval_policy" rename to "autopilot_approval_policy";

alter table "public"."auto_pilot_approvals" drop constraint "auto_pilot_approvals_policy_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."auto_pilot_approvals" add column "policy_id" uuid
--  not null;
