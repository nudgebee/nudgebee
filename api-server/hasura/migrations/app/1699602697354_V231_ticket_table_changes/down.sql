
alter table "public"."tickets" drop constraint "tickets_source_fkey";

alter table "public"."ticket_source_type" rename to "ticket_source_table";

alter table "public"."tickets" rename column "error_message" to "message";

alter table "public"."tickets" rename column "source" to "type";

DELETE FROM "public"."ticket_source_table" WHERE "value" = 'recommendation';

DELETE FROM "public"."ticket_source_table" WHERE "value" = 'event';

DROP TABLE "public"."ticket_source_table";
