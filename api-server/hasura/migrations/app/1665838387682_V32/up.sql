
CREATE TABLE "public"."notifications" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "tenant" uuid NOT NULL, "title" citext NOT NULL, "description" text, "severity" text NOT NULL DEFAULT 'Medium', PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."notification_severity_type" ("value" text NOT NULL, "description" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."notification_severity_type"("value", "description") VALUES (E'Critical', E'Critical');

INSERT INTO "public"."notification_severity_type"("value", "description") VALUES (E'High', E'High');

INSERT INTO "public"."notification_severity_type"("value", "description") VALUES (E'Medium', E'Medium');

INSERT INTO "public"."notification_severity_type"("value", "description") VALUES (E'Low', E'Low');

INSERT INTO "public"."notification_severity_type"("value", "description") VALUES (E'Info', E'Info');

alter table "public"."notifications"
  add constraint "notifications_severity_fkey"
  foreign key ("severity")
  references "public"."notification_severity_type"
  ("value") on update restrict on delete restrict;

CREATE TABLE "public"."notification_user" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "notification" uuid NOT NULL, "user_id" uuid NOT NULL, "status" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("notification") REFERENCES "public"."notifications"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."notification_user_status_type" ("value" text NOT NULL, "description" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."notification_user_status_type"("value", "description") VALUES (E'OPEN', E'OPEN');

INSERT INTO "public"."notification_user_status_type"("value", "description") VALUES (E'CLOSED', E'CLOSED');

alter table "public"."notification_user"
  add constraint "notification_user_status_fkey"
  foreign key ("status")
  references "public"."notification_user_status_type"
  ("value") on update restrict on delete restrict;
