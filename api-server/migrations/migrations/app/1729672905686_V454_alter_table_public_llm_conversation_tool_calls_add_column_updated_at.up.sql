alter table "public"."llm_conversation_tool_calls" add column "updated_at" timestamptz
 null default now();
