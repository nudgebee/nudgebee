
alter table "public"."recommendation" alter column "region" drop not null;
alter table "public"."recommendation" add column "region" text;

alter table "public"."recommendation" alter column "resource_group" drop not null;
alter table "public"."recommendation" add column "resource_group" text;

alter table "public"."recommendation" alter column "resource_type" drop not null;
alter table "public"."recommendation" add column "resource_type" text;

alter table "public"."recommendation" alter column "resource_name" drop not null;
alter table "public"."recommendation" add column "resource_name" text;
