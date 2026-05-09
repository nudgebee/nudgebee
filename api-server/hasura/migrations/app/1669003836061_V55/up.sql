
CREATE TABLE "public"."cloud_resource_status_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."cloud_resource_status_type"("value") VALUES (E'Active');

INSERT INTO "public"."cloud_resource_status_type"("value") VALUES (E'Deleted');

alter table "public"."cloud_resourses"
  add constraint "cloud_resourses_status_fkey"
  foreign key ("status")
  references "public"."cloud_resource_status_type"
  ("value") on update restrict on delete restrict;

alter table "public"."cloud_accounts" add column "synced_at" timestamp
 null;
