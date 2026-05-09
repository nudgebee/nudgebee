alter table "public"."llm_conversation_tool_calls" drop constraint "llm_conversation_tool_calls_unique";
alter table "public"."llm_conversation_tool_calls" add constraint "llm_conversation_tool_calls_conversation_id_message_id_tool_id_tool_name_key" unique ("conversation_id", "message_id", "tool_id", "tool_name");
