
alter table "public"."compliance_check_findings" alter column "account" set not null;

alter table "public"."compliance_check_findings" alter column "compliance_standard" set not null;

alter table "public"."compliance_check_findings" alter column "project" set not null;

alter table "public"."compliance_check_findings" alter column "updated_by" set not null;

alter table "public"."compliance_check_findings" alter column "created_by" set not null;
