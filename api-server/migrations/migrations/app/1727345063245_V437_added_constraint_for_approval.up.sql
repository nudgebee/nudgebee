
alter table "public"."auto_pilot_reviewers" drop constraint "auto_pilot_reviewers_tenant_id_account_id_policy_id_key";
DROP INDEX IF EXISTS "public"."auto_pilot_reviewers_tenant_id_account_id_policy_id_key";


alter table "public"."auto_pilot_reviewee" drop constraint "auto_pilot_reviewee_tenant_id_account_id_policy_id_key";
alter table "public"."auto_pilot_reviewee" add constraint "auto_pilot_reviewee_policy_id_user_id_key" unique ("policy_id", "user_id");

DROP INDEX IF EXISTS "public"."auto_pilot_reviewee_tenant_id_account_id_policy_id_key";

alter table "public"."auto_pilot_reviewers" add constraint "auto_pilot_reviewers_policy_id_user_id_key" unique ("policy_id", "user_id");
