
alter table "public"."compliance_check_findings" alter column "created_by" drop not null;

alter table "public"."compliance_check_findings" alter column "updated_by" drop not null;

alter table "public"."compliance_check_findings" alter column "project" drop not null;

alter table "public"."compliance_check_findings" alter column "compliance_standard" drop not null;

alter table "public"."compliance_check_findings" alter column "account" drop not null;
