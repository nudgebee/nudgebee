
DROP INDEX IF EXISTS "public"."cloud_resourses_external_resource_id";

CREATE  INDEX "cloud_resourses_optscale_resource_id_key" on
  "public"."cloud_resourses" using btree ("external_resource_id");

alter table "public"."cloud_resourses" drop constraint "cloud_resourses_account_external_resource_id_key";

ALTER TABLE "public"."cloud_resourses" ALTER COLUMN "external_resource_id" drop default;
