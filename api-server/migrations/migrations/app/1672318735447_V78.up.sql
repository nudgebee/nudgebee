
alter table "public"."spends" drop column "cloud_account_id" cascade;

alter table "public"."spends" drop column "region" cascade;

alter table "public"."spends" drop column "resource_name" cascade;

alter table "public"."spends" drop column "resource_group" cascade;
