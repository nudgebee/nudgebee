
alter table "public"."integrations" drop constraint "integrations_source_type_name_tenant_id_key";

alter table "public"."integrations" alter column "account_id" set not null;

alter table "public"."integrations"
  add constraint "integrations_accountid_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update no action on delete no action;
