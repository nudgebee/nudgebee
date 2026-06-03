

INSERT INTO "public"."recommendation_action_type"("value") VALUES (E'Delete') ON CONFLICT(value) DO NOTHING;

alter table "public"."auto_pilot" add column "attributes" jsonb
 not null default jsonb_build_object();
