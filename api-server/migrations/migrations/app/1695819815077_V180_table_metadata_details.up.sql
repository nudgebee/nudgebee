


CREATE TABLE "public"."table_metadata_details" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "table_id" numeric, "table_name" text, "schema_name" text, "database_name" text, "owner" text, "row_count" numeric, "table_size" numeric, "table_created_at" timestamptz, "last_altered_at" timestamptz, "last_ddl_at" timestamptz, "table_deleted_at" timestamptz, "is_transient" text, "retention_time" numeric, "instance_id" numeric, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

DROP table if exists "public"."db_type";

CREATE TABLE "public"."db_type" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."db_type"("value") VALUES (E'postgres');

INSERT INTO "public"."db_type"("value") VALUES (E'redshit');

INSERT INTO "public"."db_type"("value") VALUES (E'snowflake');

alter table "public"."table_metadata_details" add column "db_type" text
 not null;

alter table "public"."table_metadata_details"
  add constraint "table_metadata_details_db_type_fkey"
  foreign key ("db_type")
  references "public"."db_type"
  ("value") on update restrict on delete restrict;
