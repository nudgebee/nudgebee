
alter table "public"."slack_installations" add column "tenant_id" uuid
 null;

alter table "public"."slack_installations"
  add constraint "slack_installations_tenant_id_fkey"
  foreign key ("tenant_id")
  references "public"."tenant"
  ("id") on update cascade on delete cascade;

alter table "public"."slack_user" add column "slack_app_id" text
 null;
