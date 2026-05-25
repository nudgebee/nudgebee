alter table "public"."llm_conversation_agent" add column "updated_at" timestamptz
 null default now();
