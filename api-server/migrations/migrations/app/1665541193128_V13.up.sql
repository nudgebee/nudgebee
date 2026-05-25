
alter table "public"."project_users" drop column "created_at" cascade;

alter table "public"."project_users" add column "created_at" timestamp
 not null default now();

alter table "public"."business_unit" drop column "created_at" cascade;

alter table "public"."business_unit" add column "created_at" timestamp
 not null default now();

alter table "public"."businessunit_users" drop column "created_at" cascade;

alter table "public"."businessunit_users" add column "created_at" timestamp
 not null default now();
