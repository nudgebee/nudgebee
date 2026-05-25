
alter table "public"."cloud_resourses" alter column "external_resource_id" set default gen_random_uuid()::text;

alter table "public"."cloud_resourses" add constraint "cloud_resourses_account_external_resource_id_key" unique ("account", "external_resource_id");

DROP INDEX IF EXISTS "public"."cloud_resourses_optscale_resource_id_key";

CREATE  INDEX "cloud_resourses_external_resource_id" on
  "public"."cloud_resourses" using btree ("external_resource_id");
