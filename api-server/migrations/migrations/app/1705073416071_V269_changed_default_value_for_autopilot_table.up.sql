
alter table "public"."auto_pilot" alter column "creation_date" set default now();

alter table "public"."auto_pilot" alter column "update_date" set default now();
