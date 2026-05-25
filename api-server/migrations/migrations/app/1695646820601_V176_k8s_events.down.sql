
alter table "public"."events" alter column "service_key" set not null;

alter table "public"."events" alter column "subject_node" set not null;

alter table "public"."events" alter column "fingerprint" set not null;
