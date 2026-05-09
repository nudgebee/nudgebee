

alter table "public"."auto_pilot_reviewee" drop constraint "auto_pilot_reviewee_user_id_tenant_id_key";

alter table "public"."auto_pilot_approval_policy" drop constraint "auto_pilot_approval_policy_tenant_id_key";

alter table "public"."auto_pilot_approval_policy" add constraint "auto_pilot_approval_policy_tenant_id_account_id_key" unique ("tenant_id", "account_id");

BEGIN TRANSACTION;
ALTER TABLE "public"."auto_pilot_approval_policy" DROP CONSTRAINT "autopilot_approval_policy_pkey";

ALTER TABLE "public"."auto_pilot_approval_policy"
    ADD CONSTRAINT "autopilot_approval_policy_pkey" PRIMARY KEY ("tenant_id", "account_id");
COMMIT TRANSACTION;

alter table "public"."auto_pilot_reviewers" add constraint "auto_pilot_reviewers_tenant_id_account_id_policy_id_key" unique ("tenant_id", "account_id", "policy_id");

alter table "public"."auto_pilot_reviewee" add constraint "auto_pilot_reviewee_tenant_id_account_id_policy_id_key" unique ("tenant_id", "account_id", "policy_id");
