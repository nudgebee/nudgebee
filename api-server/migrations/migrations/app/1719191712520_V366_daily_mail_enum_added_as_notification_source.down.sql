
alter table "public"."notification_rules" alter column "cluster" set not null;


DELETE FROM "public"."notification_source_type" WHERE "value" = 'daily_recap';
