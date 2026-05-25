

alter table "public"."recommendation_resolution" add column "created_at" timestamp
 not null default now();

alter table "public"."recommendation_resolution" add column "updated_at" timestamp
 null default now();

alter table "public"."recommendation_resolution" add column "status_message" text
 null;

alter table "public"."recommendation_resolution" drop constraint "status_check";
alter table "public"."recommendation_resolution" add constraint "status_check" check (status = ANY (ARRAY['InProgress'::text, 'Failed'::text, 'Success'::text]));
