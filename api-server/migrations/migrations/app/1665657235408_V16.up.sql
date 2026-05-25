

alter table "public"."projects" add column "started_at" timestamp
 null default now();

alter table "public"."projects" add column "ended_at" timestamp
 null;

CREATE TABLE "public"."cloud_account_status_types" ("value" text NOT NULL, "decription" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."cloud_account_status_types"("value", "decription") VALUES (E'active', E'active');

INSERT INTO "public"."cloud_account_status_types"("value", "decription") VALUES (E'disabled', E'disabled');

INSERT INTO "public"."cloud_account_status_types"("value", "decription") VALUES (E'inactive', E'inactive');

alter table "public"."cloud_accounts" add column "status" text
 not null default 'active';

alter table "public"."cloud_accounts"
  add constraint "cloud_accounts_status_fkey"
  foreign key ("status")
  references "public"."cloud_account_status_types"
  ("value") on update restrict on delete restrict;
