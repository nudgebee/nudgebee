
alter table "public"."marketplace_customers" rename column "subscription_expity" to "subscription_expiry";

alter table "public"."marketplace_customers" rename column "status" to "is_active";

alter table "public"."marketplace_customers" add column "name" text
 null;

alter table "public"."marketplace_customers" add column "marketplace" text
 not null;

alter table "public"."marketplace_customers" add column "subscription_status" text
 not null default 'new';
