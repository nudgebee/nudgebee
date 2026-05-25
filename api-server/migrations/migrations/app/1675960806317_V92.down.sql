
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "account_type" text
--  null;

DROP TABLE "public"."compliance_findings";

DROP TABLE "public"."compliance_rules";

DROP TABLE "public"."compliance";

alter table "public"."compliance_standard" alter column "business_unit" drop not null;
alter table "public"."compliance_standard" add column "business_unit" uuid;

alter table "public"."compliance_standard" alter column "project" drop not null;
alter table "public"."compliance_standard" add column "project" uuid;

alter table "public"."compliance_standard" alter column "owner" drop not null;
alter table "public"."compliance_standard" add column "owner" uuid;

alter table "public"."compliance_standard"
  add constraint "compliance_standard_business_unit_fkey"
  foreign key ("business_unit")
  references "public"."business_unit"
  ("id") on update restrict on delete restrict;

alter table "public"."compliance_standard"
  add constraint "compliance_standard_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."compliance_standard"
  add constraint "compliance_standard_owner_fkey"
  foreign key ("owner")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."compliance_standard"
  add constraint "compliance_standard_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."compliance_standard"
  add constraint "compliance_standard_tenant_fkey"
  foreign key ("tenant")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;

alter table "public"."compliance_standard" rename column "compliance_name" to "name";

alter table "public"."compliance_check" alter column "frequency" set not null;

alter table "public"."compliance_check" alter column "region" drop not null;
alter table "public"."compliance_check" add column "region" varchar;
