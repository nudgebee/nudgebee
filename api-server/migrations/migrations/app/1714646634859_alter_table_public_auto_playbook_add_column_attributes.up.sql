alter table "public"."auto_playbook" add column "attributes" jsonb
 not null default jsonb_build_object();
