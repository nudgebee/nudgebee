

alter table "public"."events" alter column "subject_namespace" drop not null;

alter table "public"."events" alter column "subject_type" drop not null;
