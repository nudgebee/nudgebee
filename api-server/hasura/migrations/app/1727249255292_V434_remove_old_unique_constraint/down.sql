
alter table "public"."auto_pilot_reviewee" drop constraint "auto_pilot_reviewee_tenant_id_account_id_policy_id_key";

alter table "public"."auto_pilot_reviewers" drop constraint "auto_pilot_reviewers_tenant_id_account_id_policy_id_key";

alter table "public"."auto_pilot_approval_policy" drop constraint "auto_pilot_approval_policy_pkey";
alter table "public"."auto_pilot_approval_policy"
    add constraint "autopilot_approval_policy_pkey"
    primary key ("tenant_id");

alter table "public"."auto_pilot_approval_policy" drop constraint "auto_pilot_approval_policy_tenant_id_account_id_key";


alter table "public"."auto_pilot_approval_policy" add constraint "auto_pilot_approval_policy_tenant_id_key" unique ("tenant_id");

alter table "public"."auto_pilot_reviewee" add constraint "auto_pilot_reviewee_user_id_tenant_id_key" unique ("user_id", "tenant_id");
