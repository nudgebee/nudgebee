
alter table "public"."cloud_resource_job_schedule" add column "account_id" uuid
 null;

alter table "public"."cloud_resource_job_schedule" alter column "account_id" set not null;

alter table "public"."cloud_resource_job_schedule" add column "entity_type" varchar
 null default 'resource';

alter table "public"."cloud_resource_job_schedule" add column "entity_id" text
 not null;

alter table "public"."cloud_resource_job_schedule" alter column "entity_type" set not null;

alter table "public"."cloud_resource_job_schedule" drop constraint "cloud_resource_job_schedule_resource_id_action_key";

DROP INDEX IF EXISTS "public"."cloud_resource_job_schedule_resource_id_action_key";

alter table "public"."cloud_resource_job_schedule" drop column "resource_id" cascade;

alter table "public"."cloud_resource_job_schedule" add constraint "cloud_resource_job_schedule_entity_type_entity_id_action_key" unique ("entity_type", "entity_id", "action");

alter table "public"."cloud_resource_job_schedule" drop constraint "cloud_resource_job_schedule_entity_type_entity_id_action_key";
alter table "public"."cloud_resource_job_schedule" add constraint "cloud_resource_job_schedule_entity_id_entity_type_action_account_id_key" unique ("entity_id", "entity_type", "action", "account_id");
