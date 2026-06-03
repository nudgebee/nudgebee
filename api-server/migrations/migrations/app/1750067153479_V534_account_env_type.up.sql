
CREATE TABLE "public"."account_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."account_type"("value") VALUES (E'prod');

INSERT INTO "public"."account_type"("value") VALUES (E'non-prod');

INSERT INTO "public"."account_type"("value") VALUES (E'non_prod');

alter table "public"."account_type" rename to "account_env_type";

alter table "public"."cloud_accounts" add column "account_env" text
 not null default 'non_prod';

alter table "public"."cloud_accounts"
  add constraint "cloud_accounts_account_env_fkey"
  foreign key ("account_env")
  references "public"."account_env_type"
  ("value") on update set default on delete set default;
