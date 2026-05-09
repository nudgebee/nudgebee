
alter table "public"."cloud_accounts" add column "assume_role" text
 not null;

alter table "public"."cloud_accounts" add column "region" text
 null;

alter table "public"."cloud_accounts"
  add constraint "cloud_accounts_cloud_provider_fkey"
  foreign key ("cloud_provider")
  references "public"."cloud_provider"
  ("value") on update restrict on delete restrict;

ALTER TABLE "public"."cloud_accounts" ALTER COLUMN "created_at" TYPE timestamp;

ALTER TABLE "public"."cloud_accounts" ALTER COLUMN "updated_at" TYPE timestamp;

alter table "public"."compliance_standard" add column "project" uuid
 null;

alter table "public"."compliance_check" add column "project" uuid
 null;

alter table "public"."compliance_check"
  add constraint "compliance_check_project_fkey"
  foreign key ("project")
  references "public"."projects"
  ("id") on update restrict on delete restrict;

alter table "public"."compliance_check" add column "account" uuid
 null;

alter table "public"."compliance_check"
  add constraint "compliance_check_account_fkey"
  foreign key ("account")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;

alter table "public"."compliance_check_findings" add column "hash_code" text
 null;
