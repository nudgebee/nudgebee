
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."marketplace_customers" add column "subscription_status" text
--  not null default 'new';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."marketplace_customers" add column "marketplace" text
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."marketplace_customers" add column "name" text
--  null;

alter table "public"."marketplace_customers" rename column "is_active" to "status";

alter table "public"."marketplace_customers" rename column "subscription_expiry" to "subscription_expity";
