
alter table "public"."funding_sources" drop column "amount" cascade;

alter table "public"."funding_sources" add column "amount" float8
 default 0;

alter table "public"."funding_sources" alter column "amount" set not null;
