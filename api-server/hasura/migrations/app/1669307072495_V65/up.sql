
alter table "public"."recommendation" drop column "resource_name" cascade;

alter table "public"."recommendation" drop column "resource_type" cascade;

alter table "public"."recommendation" drop column "resource_group" cascade;

alter table "public"."recommendation" drop column "region" cascade;
