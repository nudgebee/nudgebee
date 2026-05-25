
alter table "public"."alert_history" alter column "created_at" set default now();

alter table "public"."alert_history" alter column "updated_at" set default now();
