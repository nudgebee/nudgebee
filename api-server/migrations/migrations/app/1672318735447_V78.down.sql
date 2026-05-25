
alter table "public"."spends"
  add constraint "spends_resource_group_fkey"
  foreign key (resource_group)
  references "public"."spends_resource_group_type"
  (value) on update restrict on delete restrict;
alter table "public"."spends" alter column "resource_group" drop not null;
alter table "public"."spends" add column "resource_group" text;

alter table "public"."spends" alter column "resource_name" drop not null;
alter table "public"."spends" add column "resource_name" text;

alter table "public"."spends" alter column "region" drop not null;
alter table "public"."spends" add column "region" text;

alter table "public"."spends" alter column "cloud_account_id" drop not null;
alter table "public"."spends" add column "cloud_account_id" text;
