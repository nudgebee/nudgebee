

alter table "public"."events" alter column "subject_type" set not null;

alter table "public"."events" alter column "subject_namespace" set not null;
