
alter table "public"."recommendation_resolution" drop constraint "status_check";
alter table "public"."recommendation_resolution" add constraint "status_check" check (CHECK (type = ANY (ARRAY['InProgress'::text, 'Failed'::text, 'Success'::text])));


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation_resolution" add column "status_message" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation_resolution" add column "updated_at" timestamp
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."recommendation_resolution" add column "created_at" timestamp
--  not null default now();
