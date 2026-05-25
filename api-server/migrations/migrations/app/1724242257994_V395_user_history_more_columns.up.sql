
alter table "public"."user_history" add column "meta" jsonb
 null default jsonb_build_object();

alter table "public"."user_history" add column "duration" float8
 null default '0.0';

alter table "public"."user_history" add column "status" text
 null;
