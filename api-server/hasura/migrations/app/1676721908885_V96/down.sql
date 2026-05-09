
alter table "public"."compliance_findings" drop constraint "compliance_findings_compliance_check_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_findings" add column "compliance_check" integer
--  not null;

alter table "public"."compliance_findings" alter column "compliance_check" drop not null;
alter table "public"."compliance_findings" add column "compliance_check" uuid;

alter table "public"."compliance_findings"
  add constraint "compliance_findings_compliance_check_fkey"
  foreign key ("compliance_check")
  references "public"."compliance_rules"
  ("id") on update cascade on delete cascade;

alter table "public"."compliance_rules" drop constraint "compliance_rules_compliance_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_rules" add column "compliance_id" uuid
--  not null;

alter table "public"."compliance_rule_executions" drop constraint "compliance_rule_executions_status_fkey",
  add constraint "compliance_rule_executions_status_fkey"
  foreign key ("status")
  references "public"."compliance_check_finding_status_type"
  ("value") on update no action on delete no action;

alter table "public"."compliance_findings" drop constraint "compliance_findings_compliance_status_fkey",
  add constraint "compliance_findings_compliance_status_fkey"
  foreign key ("compliance_status")
  references "public"."compliance_check_status_type"
  ("value") on update no action on delete no action;

alter table "public"."compliance_findings" drop constraint "compliance_findings_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_findings" add column "account_id" uuid
--  not null;

alter table "public"."compliance_findings" alter column "hashcode" set not null;

DELETE FROM "public"."compliance_rule_execution_status_type" WHERE "value" = 'FAILED';

DELETE FROM "public"."compliance_rule_execution_status_type" WHERE "value" = 'PASS';

DROP TABLE "public"."compliance_rule_execution_status_type";
