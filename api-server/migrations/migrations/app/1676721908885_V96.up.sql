
CREATE TABLE "public"."compliance_rule_execution_status_type" ("value" text NOT NULL, "description" text, PRIMARY KEY ("value") );

INSERT INTO "public"."compliance_rule_execution_status_type"("value", "description") VALUES (E'PASS', E'pass');

INSERT INTO "public"."compliance_rule_execution_status_type"("value", "description") VALUES (E'FAILED', E'failed');

alter table "public"."compliance_findings" alter column "hashcode" drop not null;

alter table "public"."compliance_findings" add column "account_id" uuid
 not null;

alter table "public"."compliance_findings"
  add constraint "compliance_findings_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update cascade on delete cascade;

alter table "public"."compliance_findings" drop constraint "compliance_findings_compliance_status_fkey",
  add constraint "compliance_findings_compliance_status_fkey"
  foreign key ("compliance_status")
  references "public"."compliance_rule_execution_status_type"
  ("value") on update no action on delete no action;

alter table "public"."compliance_rule_executions" drop constraint "compliance_rule_executions_status_fkey",
  add constraint "compliance_rule_executions_status_fkey"
  foreign key ("status")
  references "public"."compliance_rule_execution_status_type"
  ("value") on update no action on delete no action;

alter table "public"."compliance_rules" add column "compliance_id" uuid
 not null;

alter table "public"."compliance_rules"
  add constraint "compliance_rules_compliance_id_fkey"
  foreign key ("compliance_id")
  references "public"."compliance"
  ("id") on update cascade on delete cascade;

alter table "public"."compliance_findings" drop constraint "compliance_findings_compliance_check_fkey";

alter table "public"."compliance_findings" drop column "compliance_check" cascade;

alter table "public"."compliance_findings" add column "compliance_check" integer
 not null;

alter table "public"."compliance_findings"
  add constraint "compliance_findings_compliance_check_fkey"
  foreign key ("compliance_check")
  references "public"."compliance_rule_executions"
  ("id") on update cascade on delete cascade;
