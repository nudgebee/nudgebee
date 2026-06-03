alter table "public"."auto_pilot_approvals" add constraint "auto_pilot_approvals_autopilot_id_reviewer_id_account_id_tenant_id_key" unique ("autopilot_id", "reviewer_id", "account_id", "tenant_id");
