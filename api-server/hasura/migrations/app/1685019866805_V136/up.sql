
alter table "public"."cloud_accounts" add column "sync_status_message" varchar(255)
 null;

CREATE TABLE "public"."cloud_account_sync_status_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."cloud_account_sync_status_type"("value") VALUES (E'Completed');

INSERT INTO "public"."cloud_account_sync_status_type"("value") VALUES (E'Error');

INSERT INTO "public"."cloud_account_sync_status_type"("value") VALUES (E'Running');

INSERT INTO "public"."cloud_account_sync_status_type"("value") VALUES (E'Queued');

alter table "public"."cloud_accounts"
  add constraint "cloud_accounts_sync_status_fkey"
  foreign key ("sync_status")
  references "public"."cloud_account_sync_status_type"
  ("value") on update restrict on delete restrict;
