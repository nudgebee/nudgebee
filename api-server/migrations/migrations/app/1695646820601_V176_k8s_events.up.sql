
alter table "public"."events" alter column "fingerprint" drop not null;

alter table "public"."events" alter column "subject_node" drop not null;

alter table "public"."events" alter column "service_key" drop not null;
