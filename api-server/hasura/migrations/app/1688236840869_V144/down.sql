
alter table "public"."cloud_resource_job_schedule" drop constraint "cloud_resource_job_schedule_entity_id_entity_type_action_account_id_key";
alter table "public"."cloud_resource_job_schedule" add constraint "cloud_resource_job_schedule_entity_id_entity_type_action_key" unique ("entity_id", "entity_type", "action");

alter table "public"."cloud_resource_job_schedule" drop constraint "cloud_resource_job_schedule_entity_type_entity_id_action_key";

alter table "public"."cloud_resource_job_schedule"
  add constraint "cloud_resource_job_schedule_resource_id_fkey"
  foreign key (resource_id)
  references "public"."cloud_resourses"
  (id) on update restrict on delete restrict;
alter table "public"."cloud_resource_job_schedule" alter column "resource_id" drop not null;
alter table "public"."cloud_resource_job_schedule" add column "resource_id" uuid;

CREATE  INDEX "cloud_resource_job_schedule_resource_id_action_key" on
  "public"."cloud_resource_job_schedule" using btree ("action", "resource_id");

alter table "public"."cloud_resource_job_schedule" add constraint "cloud_resource_job_schedule_action_resource_id_key" unique ("action", "resource_id");

alter table "public"."cloud_resource_job_schedule" alter column "entity_type" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule" add column "entity_id" text
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule" add column "entity_type" varchar
--  null default 'resource';

alter table "public"."cloud_resource_job_schedule" alter column "account_id" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_job_schedule" add column "account_id" uuid
--  null;
