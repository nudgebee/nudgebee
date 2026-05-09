
alter table "public"."compliance_findings" drop constraint "compliance_findings_compliance_check_fkey";

alter table "public"."compliance_findings" rename column "compliance_check" to "compliance_id";

alter table "public"."compliance_findings" drop column "compliance_id" cascade
alter table "public"."compliance_findings" drop column "compliance_id";
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE "public"."compliance_rule_executions" ALTER COLUMN "id" drop default;

alter table "public"."compliance_rule_executions" drop constraint "compliance_rule_executions_pkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_rule_executions" add column "id" uuid
--  not null unique;

alter table "public"."compliance_rule_executions" alter column "id" set default nextval('compliance_rule_executions_id_seq'::regclass);
alter table "public"."compliance_rule_executions" alter column "id" drop not null;
alter table "public"."compliance_rule_executions" add column "id" int4;

alter table "public"."compliance_rule_executions"
    add constraint "compliance_rule_executions_pkey"
    primary key ("id");

alter table "public"."compliance_findings"
  add constraint "compliance_findings_compliance_check_fkey"
  foreign key (compliance_check)
  references "public"."compliance_rule_executions"
  (id) on update cascade on delete cascade;
alter table "public"."compliance_findings" alter column "compliance_check" drop not null;
alter table "public"."compliance_findings" add column "compliance_check" int4;
