
alter table "public"."compliance_findings" drop column "compliance_check" cascade;

alter table "public"."compliance_rule_executions" drop constraint "compliance_rule_executions_pkey";

alter table "public"."compliance_rule_executions" drop column "id" cascade;

alter table "public"."compliance_rule_executions" add column "id" uuid
 not null unique;

alter table "public"."compliance_rule_executions"
    add constraint "compliance_rule_executions_pkey"
    primary key ("id");

alter table "public"."compliance_rule_executions" alter column "id" set default gen_random_uuid();

CREATE EXTENSION IF NOT EXISTS pgcrypto;
alter table "public"."compliance_findings" add column "compliance_id" uuid
 not null default gen_random_uuid();

alter table "public"."compliance_findings" rename column "compliance_id" to "compliance_check";

alter table "public"."compliance_findings"
  add constraint "compliance_findings_compliance_check_fkey"
  foreign key ("compliance_check")
  references "public"."compliance_rule_executions"
  ("id") on update cascade on delete cascade;
