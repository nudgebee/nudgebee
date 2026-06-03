
CREATE EXTENSION IF NOT EXISTS pgcrypto;
alter table "public"."cloud_resourses" add column "optscale_resource_id" uuid
 not null unique default gen_random_uuid();
