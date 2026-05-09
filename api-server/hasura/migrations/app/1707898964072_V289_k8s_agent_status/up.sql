
alter table "public"."agent" add column "k8s_version" text
 null;

alter table "public"."agent" add column "connection_status" jsonb
 null;

alter table "public"."agent" add column "k8s_provider" text
 null;
