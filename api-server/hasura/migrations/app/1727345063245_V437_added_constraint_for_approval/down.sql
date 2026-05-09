
alter table "public"."auto_pilot_reviewers" drop constraint "auto_pilot_reviewers_policy_id_user_id_key";

CREATE  INDEX "auto_pilot_reviewee_tenant_id_account_id_policy_id_key" on
  "public"."auto_pilot_reviewee" using btree ("account_id", "policy_id", "tenant_id");

alter table "public"."auto_pilot_reviewee" drop constraint "auto_pilot_reviewee_policy_id_user_id_key";
alter table "public"."auto_pilot_reviewee" add constraint "auto_pilot_reviewee_account_id_policy_id_tenant_id_key" unique ("account_id", "policy_id", "tenant_id");

alter table "public"."auto_pilot_reviewers" add constraint "auto_pilot_reviewers_tenant_id_account_id_policy_id_key" unique ("tenant_id", "account_id", "policy_id");
