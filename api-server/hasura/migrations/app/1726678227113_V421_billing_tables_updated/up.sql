
alter table "public"."billing_usage_cost" drop column "billing_id" cascade;

alter table "public"."billing" add column "total_paid" float8
 not null default '0.00';

alter table "public"."billing" alter column "total_paid" set default '0.00';
