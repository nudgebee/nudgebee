

alter table "public"."auto_pilot_approvals" add column "policy_id" uuid
 not null;

alter table "public"."auto_pilot_approvals"
  add constraint "auto_pilot_approvals_policy_id_fkey"
  foreign key ("policy_id")
  references "public"."autopilot_approval_policy"
  ("id") on update restrict on delete restrict;

alter table "public"."autopilot_approval_policy" rename to "auto_pilot_approval_policy";

alter table "public"."auto_pilot_approval_policy" rename column "tanant_id" to "tenant_id";

alter table "public"."auto_pilot_approval_policy" alter column "updated_at" drop not null;

alter table "public"."auto_pilot_approval_policy" alter column "updated_by" drop not null;

INSERT INTO "public"."auto_playbook_status"("description", "value") VALUES (E'the runbook is waiting for approval', E'DRAFT');

INSERT INTO "public"."auto_pilot_status"("description", "value") VALUES (E'the auto  optimize is waiting for approval', E'DRAFT');

alter table "public"."auto_pilot_reviewee" add constraint "auto_pilot_reviewee_user_id_tenant_id_key" unique ("user_id", "tenant_id");

alter table "public"."auto_pilot_reviewers" add constraint "auto_pilot_reviewers_user_id_tenant_id_key" unique ("user_id", "tenant_id");

alter table "public"."auto_pilot_approval_policy" add constraint "auto_pilot_approval_policy_tenant_id_key" unique ("tenant_id");
