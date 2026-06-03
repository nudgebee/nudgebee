alter table "public"."llm_conversation_messages" add column "updated_at" timestamptz
 null default now();
