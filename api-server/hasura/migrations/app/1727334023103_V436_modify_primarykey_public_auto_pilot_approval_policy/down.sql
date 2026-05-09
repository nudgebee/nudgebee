alter table "public"."auto_pilot_approval_policy" drop constraint "auto_pilot_approval_policy_pkey";
alter table "public"."auto_pilot_approval_policy"
    add constraint "autopilot_approval_policy_pkey"
    primary key ("tenant_id", "account_id");
