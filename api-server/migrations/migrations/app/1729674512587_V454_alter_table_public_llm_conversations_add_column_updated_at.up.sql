alter table "public"."llm_conversations" add column "updated_at" timestamptz
 null default now();
