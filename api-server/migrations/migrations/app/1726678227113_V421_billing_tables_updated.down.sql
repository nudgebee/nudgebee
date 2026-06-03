
alter table "public"."billing" alter column "total_paid" set default '0'::double precision;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."billing" add column "total_paid" float8
--  not null default '0.00';

alter table "public"."billing_usage_cost"
  add constraint "billing_usage_cost_billing_id_fkey"
  foreign key (billing_id)
  references "public"."billing"
  (id) on update cascade on delete cascade;
alter table "public"."billing_usage_cost" alter column "billing_id" drop not null;
alter table "public"."billing_usage_cost" add column "billing_id" uuid;
